package journeys

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kayiik/facet/internal/app/toolkit"
)

// Toolkit runs the Facet tools the journeys need. The host sets it, normally
// to a *toolkit.Runner, which runs only tools the window may run itself (R1).
var Toolkit interface {
	Run(ctx context.Context, dir, tool string, request any) (toolkit.Envelope, error)
}

// runTool runs a tool through Toolkit, in dir; tests replace it to observe
// the request.
var runTool = func(ctx context.Context, dir, tool string, data []byte) (toolkit.Envelope, error) {
	if Toolkit == nil {
		return toolkit.Envelope{}, errors.New("journeys: no Facet Toolkit is configured")
	}
	return Toolkit.Run(ctx, dir, tool, json.RawMessage(data))
}

// Delivery defaults stated to output_review when a ReviewRequest leaves the
// corresponding expectation empty.
const (
	DefaultDurationTolerance = 0.1
	DefaultVideoCodec        = "h264"
	DefaultPixelFormat       = "yuv420p"
	DefaultAudioCodec        = "aac"
	DefaultAudioSampleRate   = 48000
	DefaultAudioChannels     = 2
)

// ReviewRequest states what a render is expected to be. Every expectation is
// given to output_review explicitly, so no gate passes by comparing the file
// with itself.
type ReviewRequest struct {
	// Path is the render, project-relative ("renders/final.mp4") or an
	// absolute path inside the project.
	Path   string
	Width  int
	Height int
	FPS    float64
	// Duration is the expected length in seconds.
	Duration float64
	// DurationTolerance is in seconds; zero means DefaultDurationTolerance.
	DurationTolerance float64
	// Audio states whether the render must carry an audio stream.
	Audio bool
	// Empty or zero values below mean the corresponding Default constant.
	VideoCodec      string
	PixelFormat     string
	AudioCodec      string
	AudioSampleRate int
	AudioChannels   int
}

// outputReviewRequest is the output_review request shape.
type outputReviewRequest struct {
	Input       string              `json:"input"`
	Profile     outputReviewProfile `json:"profile"`
	Checks      outputReviewChecks  `json:"checks"`
	EvidenceDir string              `json:"evidence_dir"`
}

type outputReviewProfile struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
}

type outputReviewChecks struct {
	Duration    outputReviewDuration `json:"duration"`
	VideoCodec  string               `json:"video_codec"`
	PixelFormat string               `json:"pixel_format"`
	Audio       outputReviewAudio    `json:"audio"`
}

type outputReviewDuration struct {
	Expected  float64 `json:"expected"`
	Tolerance float64 `json:"tolerance"`
}

type outputReviewAudio struct {
	Required   bool   `json:"required"`
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

// ReviewRender runs the Toolkit's technical output review (`facet tools run
// output_review`) on a render inside projectDir. Sampled frames go to the
// project's review/ directory and, when the tool succeeds, its envelope is
// stored as review/report.json, which GetProjectDetails reports as the review
// report.
//
// projectDir is a project the host has already resolved, such as
// ProjectDetails.Path or Media.Project from ResolveMediaRef (whose Media.Name
// is a valid req.Path); the render is confined to it under the same rules as
// ResolveMedia, and review/ may not lead out of it.
//
// A tool failure is returned in the envelope (OK false) and leaves any
// previous report in place; err is non-nil only when the request is invalid,
// the Toolkit cannot run, or the report cannot be stored. Technical review is
// evidence, never acceptance: the human decision is recorded with
// RecordDecision.
func ReviewRender(ctx context.Context, projectDir string, req ReviewRequest) (toolkit.Envelope, error) {
	request, err := req.expectations()
	if err != nil {
		return toolkit.Envelope{}, err
	}
	target, err := resolveProjectFile(projectDir, req.Path)
	if err != nil {
		return toolkit.Envelope{}, err
	}
	reviewDir, err := ensureReviewDir(target.root)
	if err != nil {
		return toolkit.Envelope{}, err
	}
	request.Input = target.path
	request.EvidenceDir = reviewDir
	data, err := json.Marshal(request)
	if err != nil {
		return toolkit.Envelope{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}

	envelope, err := runTool(ctx, target.root, "output_review", data)
	if err != nil {
		return toolkit.Envelope{}, err
	}
	if !envelope.OK {
		return envelope, nil
	}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return envelope, fmt.Errorf("the review ran but its report could not be encoded: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(reviewDir, "report.json"), append(encoded, '\n'), 0o644); err != nil {
		return envelope, fmt.Errorf("the review ran but its report could not be saved: %w", err)
	}
	return envelope, nil
}

// expectations validates the request and fills the stated defaults.
func (req ReviewRequest) expectations() (outputReviewRequest, error) {
	if req.Width <= 0 || req.Height <= 0 || !positiveFinite(req.FPS) || !positiveFinite(req.Duration) {
		return outputReviewRequest{}, fmt.Errorf("%w: enter the expected dimensions, frame rate and duration", ErrInvalidRequest)
	}
	tolerance := req.DurationTolerance
	if tolerance == 0 {
		tolerance = DefaultDurationTolerance
	}
	if !positiveFinite(tolerance) {
		return outputReviewRequest{}, fmt.Errorf("%w: the duration tolerance must be a positive number of seconds", ErrInvalidRequest)
	}
	if req.AudioSampleRate < 0 || req.AudioChannels < 0 {
		return outputReviewRequest{}, fmt.Errorf("%w: audio sample rate and channels must be positive", ErrInvalidRequest)
	}
	return outputReviewRequest{
		Profile: outputReviewProfile{Width: req.Width, Height: req.Height, FPS: req.FPS},
		Checks: outputReviewChecks{
			Duration:    outputReviewDuration{Expected: req.Duration, Tolerance: tolerance},
			VideoCodec:  orDefault(req.VideoCodec, DefaultVideoCodec),
			PixelFormat: orDefault(req.PixelFormat, DefaultPixelFormat),
			Audio: outputReviewAudio{
				Required:   req.Audio,
				Codec:      orDefault(req.AudioCodec, DefaultAudioCodec),
				SampleRate: orDefaultInt(req.AudioSampleRate, DefaultAudioSampleRate),
				Channels:   orDefaultInt(req.AudioChannels, DefaultAudioChannels),
			},
		},
	}, nil
}

// DecisionRequest is a human verdict on one project file.
type DecisionRequest struct {
	// Path is the reviewed file, project-relative or an absolute path inside
	// the project.
	Path string
	// Decision is "accept" or "reject" ("accepted" and "rejected" are also
	// accepted). It is never assumed.
	Decision string
	// Note explains the verdict; a rejection must say what has to change.
	Note string
	// DecidedBy names who decided; empty means DefaultDecidedBy.
	DecidedBy string
}

// DefaultDecidedBy is recorded when a DecisionRequest names no decider.
const DefaultDecidedBy = "user via Facet review view"

// Decision is the record written to review/decision.json and, for an
// acceptance, review/acceptance.json.
type Decision struct {
	Decision      string `json:"decision"`
	HumanApproved bool   `json:"human_approved"`
	// Artifact is the reviewed file's project-relative path.
	Artifact string `json:"artifact"`
	// SHA256 and Size identify the exact bytes the decision applies to.
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	Note      string    `json:"note"`
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
	// Seal is the HMAC-SHA256 of the record under the App's decision key
	// (see SetDecisionKey). It shows the App wrote the record.
	Seal string `json:"seal,omitempty"`
}

// RecordedDecision is the decision GetProjectDetails finds in
// review/decision.json.
type RecordedDecision struct {
	Decision
	// Sealed reports whether the record carries a valid seal: only then did
	// this App record it for the person. The agent can write files in a
	// workspace project, so an unsealed record is shown as a warning, never
	// as the person's decision.
	Sealed bool `json:"sealed"`
	// Current reports whether the reviewed file still has the recorded
	// digest. It is false when the file changed or is gone: the decision was
	// about other bytes.
	Current bool `json:"current"`
	// Path is the record's media reference, and URL that reference under
	// Options.MediaURLPrefix.
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}

// decisionKey seals decision records. The App keeps it in its own data
// folder, outside the agent's workspace, so the agent cannot seal a record.
var decisionKey struct {
	sync.Mutex
	key []byte
}

// SetDecisionKey sets the key that seals decision records. Without a key,
// decisions are recorded unsealed and none is reported as sealed.
func SetDecisionKey(key []byte) {
	decisionKey.Lock()
	defer decisionKey.Unlock()
	decisionKey.key = append([]byte(nil), key...)
}

// sealOf returns d's seal under the decision key, or "" without a key. The
// seal covers every field of the record except the seal itself.
func sealOf(d Decision) string {
	decisionKey.Lock()
	key := decisionKey.key
	decisionKey.Unlock()
	if len(key) == 0 {
		return ""
	}
	d.Seal = ""
	canonical, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil))
}

// readDecision reads the project's decision record. A record that is not a
// decision RecordDecision could have written is not reported.
func readDecision(project projectScope) *RecordedDecision {
	artifact, ok := readJSONArtifact(project, filepath.Join(project.projectPath, "review", "decision.json"))
	if !ok {
		return nil
	}
	var d Decision
	if err := json.Unmarshal([]byte(artifact.Text), &d); err != nil {
		return nil
	}
	if (d.Decision != "accepted" && d.Decision != "rejected") || d.Artifact == "" || len(d.SHA256) != sha256.Size*2 {
		return nil
	}
	recorded := &RecordedDecision{Decision: d, Path: artifact.Path, URL: artifact.URL}
	if expected := sealOf(d); expected != "" && hmac.Equal([]byte(expected), []byte(d.Seal)) {
		recorded.Sealed = true
	}
	// The reviewed file is confined like any other request: a record naming
	// a file outside the project is never followed there. Its digest is
	// computed afresh: a file rewritten with the same size and modification
	// time must not pass for the reviewed bytes.
	if target, err := resolveProjectFile(project.canonicalProject, d.Artifact); err == nil && target.info.Size() == d.Size {
		digest, _, err := fileDigest(target.path)
		recorded.Current = err == nil && digest == d.SHA256
	}
	return recorded
}

// RecordDecision records a human review decision about a file inside
// projectDir against the file's SHA-256 digest. projectDir and the path are
// confined as for ReviewRender. The record always replaces
// review/decision.json; an acceptance is also written to
// review/acceptance.json, and a rejection removes any earlier acceptance.
func RecordDecision(projectDir string, req DecisionRequest) (*Decision, error) {
	decision, err := normalizeDecision(req.Decision)
	if err != nil {
		return nil, err
	}
	note := strings.TrimSpace(req.Note)
	if decision == "rejected" && note == "" {
		return nil, fmt.Errorf("%w: explain what must change before recording a rejection", ErrInvalidRequest)
	}
	target, err := resolveProjectFile(projectDir, req.Path)
	if err != nil {
		return nil, err
	}
	digest, size, err := fileDigest(target.path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", target.name, err)
	}
	reviewDir, err := ensureReviewDir(target.root)
	if err != nil {
		return nil, err
	}

	approved := decision == "accepted"
	record := &Decision{
		Decision:      decision,
		HumanApproved: approved,
		Artifact:      target.name,
		SHA256:        digest,
		Size:          size,
		Note:          note,
		DecidedBy:     orDefault(req.DecidedBy, DefaultDecidedBy),
		DecidedAt:     time.Now().UTC(),
	}
	record.Seal = sealOf(*record)
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')

	acceptancePath := filepath.Join(reviewDir, "acceptance.json")
	if !approved {
		if err := os.Remove(acceptancePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("withdraw the earlier acceptance: %w", err)
		}
	}
	if err := writeFileAtomic(filepath.Join(reviewDir, "decision.json"), encoded, 0o644); err != nil {
		return nil, fmt.Errorf("record the decision: %w", err)
	}
	if approved {
		if err := writeFileAtomic(acceptancePath, encoded, 0o644); err != nil {
			return nil, fmt.Errorf("record the acceptance: %w", err)
		}
	}
	return record, nil
}

func normalizeDecision(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accept", "accepted":
		return "accepted", nil
	case "reject", "rejected":
		return "rejected", nil
	case "":
		return "", fmt.Errorf("%w: choose accept or reject; a review decision is never assumed", ErrInvalidRequest)
	default:
		return "", fmt.Errorf("%w: review decision must be accept or reject, not %q", ErrInvalidRequest, value)
	}
}

// ensureReviewDir returns the project's review/ directory, creating it when
// missing and refusing one that leads out of the project through a link,
// junction or mount point.
func ensureReviewDir(root string) (string, error) { return ensureProjectDir(root, "review") }

// ensureProjectDir returns the directory name directly inside the canonical
// project root, creating it when missing and refusing one that leads out of
// the project through a link, junction or mount point.
func ensureProjectDir(root, name string) (string, error) {
	dir := filepath.Join(root, name)
	if info, err := os.Lstat(dir); err != nil {
		if !missing(err) {
			return "", err
		}
		if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	} else if isJunction(info) {
		return "", fmt.Errorf("%w: the %s directory is a junction or mount point", ErrOutsideProject, name)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve the %s directory: %w", name, err)
	}
	if !pathStrictlyWithin(root, resolved) {
		return "", fmt.Errorf("%w: the %s directory leaves the project", ErrOutsideProject, name)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if isJunction(info) {
		return "", fmt.Errorf("%w: the %s directory is a junction or mount point", ErrOutsideProject, name)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not a directory", ErrInvalidRequest, name)
	}
	return resolved, nil
}

func fileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func orDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func orDefaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
