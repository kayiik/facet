package toolbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The content gate must separate a SPARSE frame from an EMPTY one.
//
// It judged frames by encoded size, which produced a false negative on exactly
// the case the threshold comment claimed to protect. Measured at 640x360:
//
//	"Fix check"      3179 bytes  0.247% bright pixels  REAL TEXT, was FAILED
//	"Made from chat" 9464 bytes  3.331% bright pixels  real text, passed
//	no text at all   2267 bytes  0.035% bright pixels  genuinely blank
//
// A short caption encodes below the 6000-byte threshold, so a correct render
// was reported as "every sampled frame is blank". A gate that cries wolf on
// correct output is worse than no gate: callers learn to ignore it, and it is
// the only automatic signal that a render produced nothing.
func TestSparseTextIsNotCalledBlank(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable; the gate falls back to encoded size")
	}
	dir := t.TempDir()

	// A flat background with one small glyph-sized mark: the shape of a short
	// title card, and the case that regressed.
	sparse := filepath.Join(dir, "sparse.jpg")
	render(t, sparse, "color=c=#1e293b:s=640x360",
		"drawbox=x=300:y=170:w=40:h=20:color=white:t=fill")

	// A flat background and nothing else.
	empty := filepath.Join(dir, "empty.jpg")
	render(t, empty, "color=c=#1e293b:s=640x360", "null")

	sparseBytes := size(t, sparse)
	if !frameHasContent(sparse, sparseBytes) {
		t.Errorf("a frame with real content was called blank (%d bytes encoded)", sparseBytes)
	}
	if frameHasContent(empty, size(t, empty)) {
		t.Error("a frame with nothing drawn was reported as having content")
	}
}

// When a frame cannot be decoded the gate must not invent a verdict; it falls
// back to encoded size rather than claiming either answer confidently.
func TestUndecodableFrameFallsBackToSize(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-frame.jpg")
	if !frameHasContent(missing, blankFrameBytes+1) {
		t.Error("a large undecodable frame was called blank")
	}
	if frameHasContent(missing, blankFrameBytes-1) {
		t.Error("a tiny undecodable frame was called content")
	}
}

func render(t *testing.T, out, source, filter string) {
	t.Helper()
	cmd := fixtureCommand(t, "ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", source, "-vf", filter, "-frames:v", "1", "-q:v", "2", out)
	if err := cmd.Run(); err != nil {
		t.Skipf("could not synthesise a test frame: %v", err)
	}
}

func size(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return int(info.Size())
}
