package toolbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Narration longer than the timeline is silently CUT OFF.
//
// Verified before this warning existed: 8.16s of narration against a 4s
// timeline produced a 4s file with no warning at all — half the script gone,
// and the run reported ok:true. The walkthrough documents the hazard; the tool
// said nothing, so a caller learned it only by listening to the result.
func TestTruncatedNarrationIsWarnedAbout(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	audio := synthAudio(t, 6)

	got := truncatedAudioWarning(map[string]any{
		"audio":            map[string]any{"narration": map[string]any{"src": audio}},
		"duration_seconds": 2.0,
	}, 30*time.Second)

	if got == "" {
		t.Fatal("audio three times the timeline produced no warning")
	}
	// The caller needs the NUMBER — how much is lost — not just that something
	// is wrong.
	for _, want := range []string{"6.0", "2.00", "duration_seconds"} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning does not mention %q: %q", want, got)
		}
	}
}

// Audio that fits must not warn. A warning that fires on correct input is
// noise callers learn to ignore, which would cost the real one its meaning.
func TestAudioThatFitsIsNotWarnedAbout(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	audio := synthAudio(t, 2)

	if got := truncatedAudioWarning(map[string]any{
		"audio":            map[string]any{"narration": map[string]any{"src": audio}},
		"duration_seconds": 5.0,
	}, 30*time.Second); got != "" {
		t.Errorf("audio well inside the timeline warned: %q", got)
	}
}

// Nothing to compare means nothing to say: a missing file, no audio at all, or
// no stated duration must not produce a warning built on a guess.
func TestNoWarningWithoutBothDurations(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no audio":        {"duration_seconds": 5.0},
		"no duration":     {"audio": map[string]any{"narration": map[string]any{"src": "whatever.mp3"}}},
		"missing file":    {"audio": map[string]any{"narration": map[string]any{"src": "does-not-exist.mp3"}}, "duration_seconds": 5.0},
		"empty path":      {"audio": map[string]any{"narration": map[string]any{"src": ""}}, "duration_seconds": 5.0},
		"audio not a map": {"audio": "voice.mp3", "duration_seconds": 5.0},
	} {
		if got := truncatedAudioWarning(props, 30*time.Second); got != "" {
			t.Errorf("%s produced a warning: %q", name, got)
		}
	}
}

func synthAudio(t *testing.T, seconds int) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "tone.mp3")
	cmd := fixtureCommand(t, "ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration="+itoa(seconds), out)
	if err := cmd.Run(); err != nil {
		t.Skipf("could not synthesise audio: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Skipf("synthesised audio missing: %v", err)
	}
	return out
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
