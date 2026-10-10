package journeys

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kayiik/facet/internal/app/toolkit"
)

type toolCall struct {
	tool string
	dir  string
	data []byte
}

// stubTool replaces the Toolkit runner for one test and records each call.
func stubTool(t *testing.T, envelope toolkit.Envelope) *[]toolCall {
	t.Helper()
	calls := &[]toolCall{}
	previous := runTool
	runTool = func(_ context.Context, dir, tool string, data []byte) (toolkit.Envelope, error) {
		*calls = append(*calls, toolCall{tool: tool, dir: dir, data: append([]byte(nil), data...)})
		return envelope, nil
	}
	t.Cleanup(func() { runTool = previous })
	return calls
}

func passingEnvelope() toolkit.Envelope {
	return toolkit.Envelope{
		OK:        true,
		Tool:      "output_review",
		Operation: "run",
		Result:    json.RawMessage(`{"execution_status":"succeeded","review_status":"pass","gates":[]}`),
		Warnings:  []string{},
	}
}

type sentReview struct {
	Input   string `json:"input"`
	Profile struct {
		Width  int     `json:"width"`
		Height int     `json:"height"`
		FPS    float64 `json:"fps"`
	} `json:"profile"`
	Checks struct {
		Duration struct {
			Expected  float64 `json:"expected"`
			Tolerance float64 `json:"tolerance"`
		} `json:"duration"`
		VideoCodec  string `json:"video_codec"`
		PixelFormat string `json:"pixel_format"`
		Audio       struct {
			Required   bool   `json:"required"`
			Codec      string `json:"codec"`
			SampleRate int    `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"audio"`
	} `json:"checks"`
	EvidenceDir string `json:"evidence_dir"`
}

func decodeSentReview(t *testing.T, call toolCall) sentReview {
	t.Helper()
	if call.tool != "output_review" {
		t.Fatalf("ran tool %q, want output_review", call.tool)
	}
	// Every field sent is one output_review takes; with a real facet, the
	// Toolkit's own schema checks the request too.
	var sent sentReview
	dec := json.NewDecoder(strings.NewReader(string(call.data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sent); err != nil {
		t.Fatalf("decode output_review request: %v\n%s", err, call.data)
	}
	if path := os.Getenv("FACET_APP_FACET"); path != "" {
		env, err := toolkit.New(path).Estimate(context.Background(), t.TempDir(), "output_review", json.RawMessage(call.data))
		if err != nil || !env.OK {
			t.Fatalf("output_review request does not match the tool schema: %v %+v\n%s", err, env.Error, call.data)
		}
	}
	return sent
}

func TestReviewRenderStatesEveryExpectation(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "demo")
	render := filepath.Join(project, "renders", "final.mp4")
	mustWriteTestFile(t, render, "rendered video")
	calls := stubTool(t, passingEnvelope())

	envelope, err := ReviewRender(context.Background(), project, ReviewRequest{Path: "renders/final.mp4", Width: 1920, Height: 1080, FPS: 30, Duration: 12, Audio: true})
	if err != nil || !envelope.OK {
		t.Fatalf("ReviewRender = %#v, %v", envelope, err)
	}
	if len(*calls) != 1 {
		t.Fatalf("output_review ran %d times", len(*calls))
	}
	sent := decodeSentReview(t, (*calls)[0])
	if sent.Input != mustCanonical(t, render) || sent.EvidenceDir != mustCanonical(t, filepath.Join(project, "review")) {
		t.Fatalf("review paths: input=%q evidence=%q", sent.Input, sent.EvidenceDir)
	}
	if sent.Profile.Width != 1920 || sent.Profile.Height != 1080 || sent.Profile.FPS != 30 {
		t.Fatalf("profile = %+v", sent.Profile)
	}
	checks := sent.Checks
	if checks.Duration.Expected != 12 || checks.Duration.Tolerance != DefaultDurationTolerance || checks.VideoCodec != "h264" || checks.PixelFormat != "yuv420p" {
		t.Fatalf("checks = %+v", checks)
	}
	if !checks.Audio.Required || checks.Audio.Codec != "aac" || checks.Audio.SampleRate != 48000 || checks.Audio.Channels != 2 {
		t.Fatalf("audio checks = %+v", checks.Audio)
	}

	var stored map[string]any
	if err := json.Unmarshal([]byte(mustReadTestFile(t, filepath.Join(project, "review", "report.json"))), &stored); err != nil {
		t.Fatalf("stored report: %v", err)
	}
	if stored["ok"] != true || stored["tool"] != "output_review" {
		t.Fatalf("stored report = %#v", stored)
	}
	details, err := GetProjectDetails(Options{Workspace: root}, "demo")
	if err != nil {
		t.Fatal(err)
	}
	report, ok := details.ReviewReport.(map[string]any)
	if !ok || report["status"] != "pass" || !details.Stages.Review || details.ReviewReportPath != "projects/demo/review/report.json" {
		t.Fatalf("stored review evidence = %#v, stages %#v", details.ReviewReport, details.Stages)
	}
}

func TestReviewRenderAcceptsStatedOverrides(t *testing.T) {
	project := t.TempDir()
	render := filepath.Join(project, "renders", "square.mov")
	mustWriteTestFile(t, render, "rendered video")
	calls := stubTool(t, passingEnvelope())

	_, err := ReviewRender(context.Background(), project, ReviewRequest{
		Path:              render,
		Width:             1080,
		Height:            1080,
		FPS:               29.97,
		Duration:          30,
		DurationTolerance: 1.5,
		VideoCodec:        "hevc",
		PixelFormat:       "yuv420p10le",
		AudioCodec:        "opus",
		AudioSampleRate:   44100,
		AudioChannels:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := decodeSentReview(t, (*calls)[0])
	checks := sent.Checks
	if sent.Input != mustCanonical(t, render) || sent.Profile.FPS != 29.97 || checks.Duration.Tolerance != 1.5 || checks.VideoCodec != "hevc" || checks.PixelFormat != "yuv420p10le" {
		t.Fatalf("overrides were not stated: %+v", sent)
	}
	if checks.Audio.Required || checks.Audio.Codec != "opus" || checks.Audio.SampleRate != 44100 || checks.Audio.Channels != 1 {
		t.Fatalf("audio overrides were not stated: %+v", checks.Audio)
	}
}

func TestReviewRenderKeepsThePreviousReportWhenTheToolFails(t *testing.T) {
	project := t.TempDir()
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "rendered video")
	previous := `{"ok":true,"result":{"review_status":"pass"}}`
	mustWriteTestFile(t, filepath.Join(project, "review", "report.json"), previous)
	failed := toolkit.Envelope{OK: false, Tool: "output_review", Operation: "run", Error: &toolkit.ToolError{Code: "output_validation_failed", Message: "output has no video stream"}, Warnings: []string{}}
	stubTool(t, failed)

	envelope, err := ReviewRender(context.Background(), project, ReviewRequest{Path: "renders/final.mp4", Width: 640, Height: 360, FPS: 25, Duration: 3})
	if err != nil {
		t.Fatalf("a tool failure was reported as a request error: %v", err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "output_validation_failed" {
		t.Fatalf("tool failure was not returned: %#v", envelope)
	}
	if got := mustReadTestFile(t, filepath.Join(project, "review", "report.json")); got != previous {
		t.Fatalf("a failed review replaced the previous report: %s", got)
	}
}

func TestReviewRenderRejectsInvalidRequests(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "rendered video")
	mustWriteTestFile(t, filepath.Join(parent, "outside.mp4"), "outside video")
	calls := stubTool(t, passingEnvelope())
	valid := ReviewRequest{Path: "renders/final.mp4", Width: 1920, Height: 1080, FPS: 30, Duration: 10}

	for name, test := range map[string]struct {
		mutate func(*ReviewRequest)
		want   error
	}{
		"zero width":         {func(r *ReviewRequest) { r.Width = 0 }, ErrInvalidRequest},
		"negative height":    {func(r *ReviewRequest) { r.Height = -1080 }, ErrInvalidRequest},
		"zero fps":           {func(r *ReviewRequest) { r.FPS = 0 }, ErrInvalidRequest},
		"NaN fps":            {func(r *ReviewRequest) { r.FPS = math.NaN() }, ErrInvalidRequest},
		"infinite duration":  {func(r *ReviewRequest) { r.Duration = math.Inf(1) }, ErrInvalidRequest},
		"zero duration":      {func(r *ReviewRequest) { r.Duration = 0 }, ErrInvalidRequest},
		"negative tolerance": {func(r *ReviewRequest) { r.DurationTolerance = -1 }, ErrInvalidRequest},
		"negative channels":  {func(r *ReviewRequest) { r.AudioChannels = -2 }, ErrInvalidRequest},
		"no path":            {func(r *ReviewRequest) { r.Path = "" }, ErrInvalidRequest},
		"parent escape":      {func(r *ReviewRequest) { r.Path = "../outside.mp4" }, ErrOutsideProject},
		"absolute outside":   {func(r *ReviewRequest) { r.Path = filepath.Join(parent, "outside.mp4") }, ErrOutsideProject},
		"drive letter":       {func(r *ReviewRequest) { r.Path = "C:/outside.mp4" }, ErrOutsideProject},
		"missing render":     {func(r *ReviewRequest) { r.Path = "renders/missing.mp4" }, ErrNotFound},
		"directory":          {func(r *ReviewRequest) { r.Path = "renders" }, ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			req := valid
			test.mutate(&req)
			if _, err := ReviewRender(context.Background(), project, req); !errors.Is(err, test.want) {
				t.Fatalf("ReviewRender = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := ReviewRender(context.Background(), filepath.Join(parent, "missing"), valid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReviewRender in a missing project = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid requests reached the tool %d times", len(*calls))
	}
	if _, err := os.Stat(filepath.Join(project, "review")); !os.IsNotExist(err) {
		t.Fatalf("an invalid request created the review directory: %v", err)
	}
	if got := mustReadTestFile(t, filepath.Join(parent, "outside.mp4")); got != "outside video" {
		t.Fatalf("the outside file changed: %q", got)
	}
}

func TestRecordDecisionRecordsTheReviewedFileDigest(t *testing.T) {
	project := t.TempDir()
	contents := "rendered video bytes"
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), contents)
	before := time.Now().UTC().Add(-time.Second)

	record, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accept", Note: "  Ready to publish.  "})
	if err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	want := sha256Hex(contents)
	if record.SHA256 != want || record.Size != int64(len(contents)) || record.Artifact != "renders/final.mp4" {
		t.Fatalf("decision identity = %#v, want digest %s", record, want)
	}
	if record.Decision != "accepted" || !record.HumanApproved || record.Note != "Ready to publish." || record.DecidedBy != DefaultDecidedBy {
		t.Fatalf("decision = %#v", record)
	}
	if record.DecidedAt.Before(before) || record.DecidedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("decision time %s is not now", record.DecidedAt)
	}

	decisionFile := mustReadTestFile(t, filepath.Join(project, "review", "decision.json"))
	var stored struct {
		Decision      string    `json:"decision"`
		HumanApproved bool      `json:"human_approved"`
		Artifact      string    `json:"artifact"`
		SHA256        string    `json:"sha256"`
		Size          int64     `json:"size"`
		Note          string    `json:"note"`
		DecidedBy     string    `json:"decided_by"`
		DecidedAt     time.Time `json:"decided_at"`
	}
	if err := json.Unmarshal([]byte(decisionFile), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.SHA256 != want || stored.Artifact != "renders/final.mp4" || stored.Decision != "accepted" || !stored.HumanApproved || stored.Note != "Ready to publish." || !stored.DecidedAt.Equal(record.DecidedAt) || stored.Size != record.Size || stored.DecidedBy != DefaultDecidedBy {
		t.Fatalf("stored decision = %+v", stored)
	}
	if acceptance := mustReadTestFile(t, filepath.Join(project, "review", "acceptance.json")); acceptance != decisionFile {
		t.Fatalf("acceptance record differs from the decision:\n%s\n%s", acceptance, decisionFile)
	}
}

func TestRecordDecisionDigestFollowsTheExactBytes(t *testing.T) {
	project := t.TempDir()
	render := filepath.Join(project, "renders", "final.mp4")
	mustWriteTestFile(t, render, "first cut")
	first, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accepted"})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteTestFile(t, render, "second cut")
	second, err := RecordDecision(project, DecisionRequest{Path: render, Decision: "ACCEPT", DecidedBy: "editor@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != sha256Hex("first cut") || second.SHA256 != sha256Hex("second cut") || first.SHA256 == second.SHA256 {
		t.Fatalf("digests do not follow the reviewed bytes: %s then %s", first.SHA256, second.SHA256)
	}
	if second.Artifact != "renders/final.mp4" || second.DecidedBy != "editor@example.test" {
		t.Fatalf("absolute request recorded %#v", second)
	}
	var stored Decision
	if err := json.Unmarshal([]byte(mustReadTestFile(t, filepath.Join(project, "review", "decision.json"))), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.SHA256 != second.SHA256 {
		t.Fatalf("decision file keeps digest %s, want the latest %s", stored.SHA256, second.SHA256)
	}
}

func TestRecordDecisionRecordsRejectionWithoutApproval(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "project")
	video := filepath.Join(project, "renders", "final.mp4")
	mustWriteTestFile(t, video, "rendered video")
	mustWriteTestFile(t, filepath.Join(project, "review", "acceptance.json"), `{"human_approved":true}`)

	record, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "reject", Note: "Typography needs revision."})
	if err != nil {
		t.Fatalf("review decision failed: %v", err)
	}
	if record.Decision != "rejected" || record.HumanApproved || record.SHA256 != sha256Hex("rendered video") {
		t.Fatalf("returned rejection = %#v", record)
	}
	var decision map[string]any
	if err := json.Unmarshal([]byte(mustReadTestFile(t, filepath.Join(project, "review", "decision.json"))), &decision); err != nil {
		t.Fatal(err)
	}
	if decision["decision"] != "rejected" || decision["human_approved"] != false || decision["note"] != "Typography needs revision." || decision["sha256"] != sha256Hex("rendered video") {
		t.Fatalf("recorded rejection = %#v", decision)
	}
	if _, err := os.Stat(filepath.Join(project, "review", "acceptance.json")); !os.IsNotExist(err) {
		t.Fatal("rejection left an acceptance record")
	}
}

func TestRecordDecisionIsNeverAssumed(t *testing.T) {
	project := t.TempDir()
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "rendered video")
	for _, req := range []DecisionRequest{
		{Path: "renders/final.mp4"},
		{Path: "renders/final.mp4", Decision: "maybe"},
		{Path: "renders/final.mp4", Decision: "reject"},
		{Path: "renders/final.mp4", Decision: "rejected", Note: "   "},
		{Decision: "accept"},
	} {
		if record, err := RecordDecision(project, req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("RecordDecision(%+v) = %#v, %v; want ErrInvalidRequest", req, record, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "review")); !os.IsNotExist(err) {
		t.Fatalf("a refused decision wrote review files: %v", err)
	}
}

func TestRecordDecisionRejectsPathEscapes(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "rendered video")
	outside := filepath.Join(parent, "outside.mp4")
	mustWriteTestFile(t, outside, "outside video")

	for _, path := range []string{"../outside.mp4", `..\outside.mp4`, "renders/../../outside.mp4", outside, "C:/outside.mp4", "c:outside.mp4"} {
		if record, err := RecordDecision(project, DecisionRequest{Path: path, Decision: "accept"}); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("RecordDecision(%q) = %#v, %v; want ErrOutsideProject", path, record, err)
		}
	}
	if _, err := RecordDecision(project, DecisionRequest{Path: "renders/missing.mp4", Decision: "accept"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decision about a missing file = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(project, "review")); !os.IsNotExist(err) {
		t.Fatalf("a refused decision wrote review files: %v", err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 2 {
		t.Fatalf("a refused decision wrote beside the project: %v, %v", entries, err)
	}
}

func TestRecordDecisionRejectsLinksLeavingTheProject(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	mustWriteTestFile(t, filepath.Join(outside, "final.mp4"), "outside video")
	if !makeTestSymlink(t, filepath.Join(outside, "final.mp4"), filepath.Join(project, "renders", "linked.mp4")) {
		return
	}
	if _, err := RecordDecision(project, DecisionRequest{Path: "renders/linked.mp4", Decision: "accept"}); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("decision about a file outside the project = %v, want ErrOutsideProject", err)
	}

	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "rendered video")
	elsewhere := t.TempDir()
	if !makeTestSymlink(t, elsewhere, filepath.Join(project, "review")) {
		return
	}
	if _, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accept"}); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("decision through a review link leaving the project = %v, want ErrOutsideProject", err)
	}
	stubTool(t, passingEnvelope())
	if _, err := ReviewRender(context.Background(), project, ReviewRequest{Path: "renders/final.mp4", Width: 1, Height: 1, FPS: 1, Duration: 1}); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("review through a review link leaving the project = %v, want ErrOutsideProject", err)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("review records were written outside the project: %v, %v", entries, err)
	}
}

// TestReviewJourneyWithToolkit runs the whole journey against a real facet
// (FACET_APP_FACET): create a project, review a real render, see the review
// reported, and record the human decision against the render's digest.
func TestReviewJourneyWithToolkit(t *testing.T) {
	path := os.Getenv("FACET_APP_FACET")
	if path == "" {
		t.Skip("FACET_APP_FACET is not set")
	}
	for _, program := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not available: %v", program, err)
		}
	}
	previous := Toolkit
	Toolkit = toolkit.New(path)
	t.Cleanup(func() { Toolkit = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	opts := catalogOptions(t)
	project, err := CreateProject(opts, NewProject{Name: "Toolbox Journey"})
	if err != nil {
		t.Fatal(err)
	}
	render := filepath.Join(project.Path, "renders", "final.mp4")
	if err := os.MkdirAll(filepath.Dir(render), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", render)
	if output, err := fixture.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot produce the fixture render: %v\n%s", err, output)
	}

	envelope, err := ReviewRender(ctx, project.Path, ReviewRequest{Path: "renders/final.mp4", Width: 320, Height: 240, FPS: 30, Duration: 2, DurationTolerance: 0.25, Audio: true})
	if err != nil {
		t.Fatalf("ReviewRender: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("output_review failed: %#v", envelope.Error)
	}

	var report struct {
		OK     bool `json:"ok"`
		Result struct {
			ReviewStatus string `json:"review_status"`
			Gates        []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"gates"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(mustReadTestFile(t, filepath.Join(project.Path, "review", "report.json"))), &report); err != nil {
		t.Fatal(err)
	}
	stated := map[string]string{}
	for _, gate := range report.Result.Gates {
		stated[gate.Name] = gate.Status
	}
	for _, name := range []string{"profile", "duration", "video_codec", "pixel_format", "audio"} {
		if stated[name] != "pass" {
			t.Errorf("gate %s = %q, want a verified pass (gates %v)", name, stated[name], stated)
		}
	}
	if !report.OK || (report.Result.ReviewStatus != "pass" && report.Result.ReviewStatus != "warn") {
		t.Fatalf("stored report = %+v", report)
	}

	details, err := GetProjectDetails(opts, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewReport, ok := details.ReviewReport.(map[string]any)
	if !ok || reviewReport["status"] != report.Result.ReviewStatus || !details.Stages.Review || !details.Stages.Master {
		t.Fatalf("project does not report the review: %#v, stages %#v", details.ReviewReport, details.Stages)
	}
	media, err := serveMediaURL(t, opts, details.VideoURL)
	if err != nil || media.ContentType != "video/mp4" || media.Path != mustCanonical(t, render) {
		t.Fatalf("reviewed render media = %#v, %v", media, err)
	}

	renderBytes, err := os.ReadFile(render)
	if err != nil {
		t.Fatal(err)
	}
	record, err := RecordDecision(project.Path, DecisionRequest{Path: media.Name, Decision: "accept", Note: "Matches the brief."})
	if err != nil {
		t.Fatal(err)
	}
	if record.SHA256 != sha256Hex(string(renderBytes)) || record.Artifact != "renders/final.mp4" || !record.HumanApproved {
		t.Fatalf("decision = %#v", record)
	}
	if !strings.Contains(mustReadTestFile(t, filepath.Join(project.Path, "review", "acceptance.json")), record.SHA256) {
		t.Fatal("acceptance record lacks the render digest")
	}
}
