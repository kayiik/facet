package toolbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type gflowVideoRequest struct {
	Prompt         string  `json:"prompt"`
	Model          string  `json:"model,omitempty"`
	Duration       float64 `json:"duration,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
	Resolution     string  `json:"resolution,omitempty"`
	StartFrame     string  `json:"start_frame,omitempty"`
	EndFrame       string  `json:"end_frame,omitempty"`
	OutputPath     string  `json:"output_path,omitempty"`
	Mock           bool    `json:"mock,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty"`
}

type gflowImageRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model,omitempty"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	Count          int    `json:"count,omitempty"`
	ReferenceImage string `json:"reference_image,omitempty"`
	OutputPath     string `json:"output_path,omitempty"`
	Mock           bool   `json:"mock,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func doGFlowVideo(op string, data []byte) (any, []string, error) {
	return doGFlowVideoContext(context.Background(), op, data)
}

func doGFlowVideoContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r gflowVideoRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, nil, failure("invalid_request", "prompt is required", nil)
	}
	if r.Model == "" {
		r.Model = "veo-3.1"
	}
	if r.Duration == 0 {
		r.Duration = 6
	}
	if r.AspectRatio == "" {
		r.AspectRatio = "landscape"
	}
	if r.Resolution == "" {
		r.Resolution = "1080p"
	}
	if r.Model != "veo-3.1" || !slices.Contains([]float64{4, 6, 8, 10}, r.Duration) ||
		!slices.Contains([]string{"landscape", "portrait", "square"}, r.AspectRatio) ||
		!slices.Contains([]string{"720p", "1080p", "4k"}, r.Resolution) {
		return nil, nil, failure("invalid_request", "gflow video requires model veo-3.1, duration 4/6/8/10, aspect landscape/portrait/square, and resolution 720p/1080p/4k", nil)
	}
	timeout, err := cloudTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}
	if op == "estimate" {
		return gflowEstimate("gflow_video_generate", r.Mock), nil, nil
	}
	if r.OutputPath == "" {
		r.OutputPath = defaultOutput(ctx, "gflow_video.mp4")
	}
	res := map[string]any{"provider": "google_flow", "model": r.Model, "prompt": r.Prompt,
		"duration": r.Duration, "aspect_ratio": r.AspectRatio, "resolution": r.Resolution,
		"output": r.OutputPath, "mock": r.Mock}
	if r.Mock {
		if err := gflowSafeOutput(r.OutputPath); err != nil {
			return nil, nil, err
		}
		if err := createMockVideo(r.OutputPath, 1920, 1080, r.Duration); err != nil {
			return nil, nil, err
		}
		return res, nil, nil
	}
	args := []string{"video", "-d", fmt.Sprint(int(r.Duration)), "-a", r.AspectRatio, "-r", r.Resolution}
	if r.StartFrame != "" {
		args = append(args, "--start", r.StartFrame)
	}
	if r.EndFrame != "" {
		args = append(args, "--end", r.EndFrame)
	}
	outputs, err := generateGFlowContext(ctx, args, r.Prompt, "video", r.OutputPath, 1, timeout)
	if err != nil {
		return nil, nil, err
	}
	res["outputs"] = outputs
	return res, nil, nil
}

func doGFlowImage(op string, data []byte) (any, []string, error) {
	return doGFlowImageContext(context.Background(), op, data)
}

func doGFlowImageContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r gflowImageRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, nil, failure("invalid_request", "prompt is required", nil)
	}
	if r.Model == "" {
		r.Model = "narwhal"
	}
	if r.AspectRatio == "" {
		r.AspectRatio = "landscape"
	}
	if r.Count == 0 {
		r.Count = 1
	}
	if !slices.Contains([]string{"narwhal", "harbor_seal", "gem_pix_2"}, r.Model) ||
		!slices.Contains([]string{"landscape", "portrait", "square", "4:3", "3:4"}, r.AspectRatio) || r.Count < 1 || r.Count > 4 {
		return nil, nil, failure("invalid_request", "gflow image requires model narwhal/harbor_seal/gem_pix_2, aspect landscape/portrait/square/4:3/3:4, and count 1-4", nil)
	}
	timeout, err := cloudTimeout(r.TimeoutSeconds, 180)
	if err != nil {
		return nil, nil, err
	}
	if op == "estimate" {
		return gflowEstimate("gflow_image_generate", r.Mock), nil, nil
	}
	if r.OutputPath == "" {
		r.OutputPath = defaultOutput(ctx, "gflow_image.png")
	}
	res := map[string]any{"provider": "google_flow", "model": r.Model, "prompt": r.Prompt,
		"aspect_ratio": r.AspectRatio, "output": r.OutputPath, "mock": r.Mock}
	if r.Mock {
		if err := gflowSafeOutput(r.OutputPath); err != nil {
			return nil, nil, err
		}
		if err := createMockPNG(r.OutputPath, 1920, 1080, "Google Flow: "+r.Prompt); err != nil {
			return nil, nil, failure("command_failed", "failed to create mock image", nil)
		}
		return res, nil, nil
	}
	args := []string{"image", "-a", r.AspectRatio, "-m", r.Model, "-c", fmt.Sprint(r.Count)}
	if r.ReferenceImage != "" {
		args = append(args, "--ref", r.ReferenceImage)
	}
	outputs, err := generateGFlowContext(ctx, args, r.Prompt, "image", r.OutputPath, r.Count, timeout)
	if err != nil {
		return nil, nil, err
	}
	res["outputs"] = outputs
	return res, nil, nil
}

func gflowEstimate(operation string, mock bool) map[string]any {
	res := estimateResult([]string{operation})
	res["provider"] = "google_flow"
	res["network"] = !mock
	res["mock"] = mock
	res["cost_known"] = mock
	if !mock {
		res["estimated_cost"] = nil
	}
	return res
}

// Guard multiplication as well as negative values before filesystem/network work.
func cloudTimeout(seconds, fallback int) (time.Duration, error) {
	if seconds < 0 || int64(seconds) > int64((1<<63-1)/time.Second) {
		return 0, failure("invalid_request", "timeout_seconds is out of range", nil)
	}
	return positiveTimeout(seconds, fallback)
}

type gflowMedia struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	LocalPath string `json:"local_path"`
	MIMEType  string `json:"mime_type"`
}

type gflowOutput struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	MIMEType   string `json:"mime_type"`
	Output     string `json:"output"`
	SourceFile string `json:"source_file"`
	SHA256     string `json:"sha256"`
}

const gflowStdoutLimit = 1 << 20

type gflowStdout struct {
	data     bytes.Buffer
	overflow bool
}

func (b *gflowStdout) Write(p []byte) (int, error) {
	n := len(p)
	remaining := gflowStdoutLimit - b.data.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.data.Write(p)
	// Drain excess output without retaining it or disrupting a possibly paid download.
	return n, nil
}

func gflowSafeOutput(path string) error {
	if strings.TrimSpace(path) == "" {
		return failure("invalid_request", "output path is required", nil)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return failure("invalid_request", "output path cannot be resolved", nil)
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return failure("invalid_request", "output path cannot be inspected", nil)
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || (p == filepath.Clean(path) && !info.Mode().IsRegular()) || (p != filepath.Clean(path) && !info.IsDir())) {
			return failure("invalid_request", "output must be a regular file without symlink ancestors", nil)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

func generateGFlow(args []string, prompt, kind, output string, count int, timeout time.Duration) (_ []gflowOutput, resultErr error) {
	return generateGFlowContext(context.Background(), args, prompt, kind, output, count, timeout)
}

func generateGFlowContext(caller context.Context, args []string, prompt, kind, output string, count int, timeout time.Duration) (_ []gflowOutput, resultErr error) {
	targets := make([]string, count)
	for i := range targets {
		targets[i] = output
		if i > 0 {
			ext := filepath.Ext(output)
			targets[i] = strings.TrimSuffix(output, ext) + fmt.Sprintf("-%d", i+1) + ext
		}
		if err := gflowSafeOutput(targets[i]); err != nil {
			return nil, err
		}
	}
	bin, err := lookPath("gflow")
	if err != nil {
		return nil, failure("dependency_missing", "gflow CLI is required; install and authenticate it or explicitly set mock=true", nil)
	}
	if err := outputPath(output, true, false); err != nil {
		return nil, err
	}
	parent, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return nil, failure("invalid_request", "invalid output directory", nil)
	}
	stage, err := os.MkdirTemp(parent, ".gflow-*")
	if err != nil {
		return nil, failure("command_failed", "could not create gflow staging directory", nil)
	}
	quarantine := false
	defer func() {
		// A failed or timed-out CLI may already have downloaded paid media:
		// that is kept and reported as recovery_path, never deleted. An empty
		// staging folder holds nothing to recover and is removed, so a failure
		// leaves no stray folder behind.
		if !quarantine || emptyDir(stage) {
			_ = os.RemoveAll(stage)
			return
		}
		var toolErr *toolFailure
		if errors.As(resultErr, &toolErr) {
			toolErr.err.Details["recovery_path"] = stage
			toolErr.err.Details["quarantined"] = true
		}
	}()
	// Resolve both sides of containment, including Windows short-name ancestors.
	canonicalStage, err := filepath.EvalSymlinks(stage)
	if err != nil {
		return nil, failure("command_failed", "could not resolve gflow staging directory", nil)
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return nil, failure("command_failed", "could not open gflow staging directory", nil)
	}
	defer root.Close()
	ctx, cancel := context.WithTimeout(caller, timeout)
	defer cancel()
	// -o is a directory in the public CLI. Never retry through a second transport.
	args = append(args, "-o", stage, "--json", "--", prompt)
	cmd := newCommand(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	var stdout gflowStdout
	cmd.Stdout = &stdout
	// Provider diagnostics may contain credentials or signed URLs, so stderr is
	// NOT passed through — but the provider's own error line is the only thing
	// that explains a failure, and discarding it entirely left every failure
	// reading the same way.
	//
	// Verified: an auth problem in the gflow CLI reported
	//
	//	Error: generation failed (500): CAPTCHA_FAILED: Cannot access contents
	//	of the page. Extension manifest must request permission...
	//
	// and Facet reported "gflow CLI failed; check provider authentication and
	// availability" — true, unactionable, and identical to what a quota
	// exhaustion or a network outage would say.
	//
	// Captured to a bounded buffer and mined for the CLI's own "Error:" line,
	// which is prose the CLI prints for a human. Anything token-shaped is
	// dropped rather than relayed.
	var stderr boundedBuffer
	cmd.Stderr = &stderr
	quarantine = true
	runErr := cmd.Run()
	if stdout.overflow {
		return nil, failure("provider_response_invalid", "gflow stdout exceeded the 1 MiB limit; no retry was attempted", nil)
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return nil, failure("command_timeout", "gflow timed out; generation may still be running; no retry was attempted", nil)
		}
		if errors.Is(runErr, exec.ErrWaitDelay) {
			return nil, failure("command_timeout", "gflow output pipes did not close after CLI exit; no retry was attempted", nil)
		}
		msg := "gflow CLI failed; check provider authentication and availability; no retry was attempted"
		if reason := providerErrorLine(stderr.String()); reason != "" {
			msg = "gflow CLI failed: " + reason + "; no retry was attempted"
		}
		return nil, failure("command_failed", msg, nil)
	}
	var media []gflowMedia
	// gflow v1.0.0 prints polling dots on stdout during video upscaling, even --json.
	// Strip only that known prefix; Unmarshal still rejects prose and trailing JSON.
	response := bytes.TrimLeft(stdout.data.Bytes(), ". \t\r\n")
	if err := json.Unmarshal(response, &media); err != nil || len(media) != count {
		return nil, failure("provider_response_invalid", "gflow must return a JSON media array matching the requested count; no retry was attempted", nil)
	}
	outputs := make([]gflowOutput, count)
	seen := make(map[string]bool, count)
	// Validate every artifact before replacing any requested output.
	for i, item := range media {
		if item.ID == "" || item.Type != kind || !strings.HasPrefix(item.MIMEType, kind+"/") || item.LocalPath == "" || seen[item.ID] {
			return nil, failure("provider_response_invalid", "gflow returned incomplete or inconsistent media metadata", nil)
		}
		seen[item.ID] = true
		source := item.LocalPath
		if !filepath.IsAbs(source) {
			source = filepath.Join(stage, source)
		}
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return nil, failure("provider_response_invalid", "gflow returned an inaccessible local_path", nil)
		}
		rel, err := filepath.Rel(canonicalStage, resolved)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, failure("provider_response_invalid", "gflow local_path escapes its staging directory", nil)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, failure("provider_response_invalid", "gflow local_path must name a nonempty regular file", nil)
		}
		media[i].LocalPath = rel
		// Report the media type of the BYTES, not the provider's claim about
		// them. Google Flow returns JPEG data while declaring "image/png", so
		// passing its value through published a false content type to any host
		// that trusts it — and the host validates artifact metadata. Detection
		// reads the file's own signature; the provider's value is kept only
		// when the bytes do not identify themselves.
		mime := item.MIMEType
		if detected := detectMediaType(resolved); detected != "" {
			mime = detected
		}
		outputs[i] = gflowOutput{ID: item.ID, Type: item.Type, MIMEType: mime, Output: targets[i], SourceFile: filepath.ToSlash(rel)}
	}
	copies := make([]string, count)
	for i, item := range media {
		source, err := root.Open(item.LocalPath)
		if err != nil {
			return nil, failure("command_failed", "could not open gflow artifact", nil)
		}
		temp, err := os.CreateTemp(parent, ".gflow-copy-*")
		if err != nil {
			source.Close()
			return nil, failure("command_failed", "could not stage gflow artifact copy", nil)
		}
		defer os.Remove(temp.Name())
		copies[i] = temp.Name()
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(temp, hash), source)
		source.Close()
		closeErr := temp.Close()
		if copyErr != nil || closeErr != nil {
			return nil, failure("command_failed", "could not copy gflow artifact", nil)
		}
		if err := gflowSafeOutput(targets[i]); err != nil {
			return nil, err
		}
		outputs[i].SHA256 = hex.EncodeToString(hash.Sum(nil))
	}
	if err := publishFileSet(copies, targets, true); err != nil {
		return nil, err
	}
	quarantine = false
	return outputs, nil
}

// emptyDir reports whether dir exists and holds nothing. Anything else —
// entries, or an error reading it — counts as not empty, so nothing that
// might be recovered is ever deleted on a guess.
func emptyDir(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	return len(names) == 0 && errors.Is(err, io.EOF)
}

// detectMediaType reports the media type implied by a file's own signature, or
// "" when the bytes do not identify themselves.
//
// A provider's declared content type is a claim; the bytes are evidence. Google
// Flow declares "image/png" for JPEG data, so relaying its value published a
// content type contradicted by the file itself.
func detectMediaType(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var header [512]byte
	n, err := f.Read(header[:])
	if err != nil && n == 0 {
		return ""
	}
	switch detected := http.DetectContentType(header[:n]); {
	case strings.HasPrefix(detected, "image/"), strings.HasPrefix(detected, "video/"),
		strings.HasPrefix(detected, "audio/"):
		return detected
	}
	return ""
}

// boundedBuffer keeps at most a small prefix of what is written to it.
//
// Provider stderr is untrusted in both size and content: it may be a firehose
// of progress output, and it may carry credentials or signed URLs. Bounding it
// means a chatty CLI cannot exhaust memory, and the extraction below is what
// keeps secrets out of the envelope.
type boundedBuffer struct {
	data []byte
}

const providerStderrLimit = 8 << 10

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := providerStderrLimit - len(b.data); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.data = append(b.data, p[:room]...)
	}
	// Always report the full length: reporting less makes the writer retry.
	return len(p), nil
}

func (b *boundedBuffer) String() string { return string(b.data) }

// providerErrorLine extracts the CLI's own error sentence, or "" when there is
// nothing safe to report.
//
// Only the line the CLI prints for a human — "Error: ..." — is considered.
// That is prose describing what went wrong, not a dump of request state. A
// line carrying anything token-shaped is dropped rather than relayed, because
// a helpful message is not worth leaking a credential.
func providerErrorLine(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Error:") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "Error:"))
		if line == "" || looksSecret(line) {
			return ""
		}
		return bounded(line)
	}
	return ""
}

// looksSecret reports whether a line may carry a credential or signed URL.
//
// Deliberately broad: the cost of dropping a useful message is one generic
// error, and the cost of relaying a token is a leaked credential in whatever
// the host stores or displays.
func looksSecret(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range []string{
		"key=", "token=", "secret", "password", "authorization",
		"bearer ", "signature=", "x-goog-", "sig=", "credential",
		"api_key", "apikey", "access_token",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// A long unbroken run of base64-ish characters is a token, not prose.
	for _, field := range strings.Fields(line) {
		if len(field) >= 40 && !strings.ContainsAny(field, " .,;:!?") {
			alnum := 0
			for _, r := range field {
				if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
					r == '-' || r == '_' {
					alnum++
				}
			}
			if alnum*10 >= len(field)*9 {
				return true
			}
		}
	}
	return false
}
