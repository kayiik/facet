package toolbox

import (
	"os"
	"path/filepath"
	"testing"
)

// The content gate exists because every other gate reads metadata: a video of
// entirely blank frames satisfies profile, duration, codec, pixel format and
// audio, so `pass` was reachable for output with nothing drawn in it.
func TestContentGate(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int, fill byte) string {
		p := filepath.Join(dir, name)
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = fill
		}
		if err := os.WriteFile(p, buf, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	samples := func(paths ...string) any {
		list := make([]map[string]any, 0, len(paths))
		for _, p := range paths {
			list = append(list, map[string]any{"path": p})
		}
		return map[string]any{"samples": list}
	}

	t.Run("all frames blank fails", func(t *testing.T) {
		g := contentGate(samples(write("a.jpg", 2900, 'a'), write("b.jpg", 3100, 'b')))
		if g["status"] != "fail" {
			t.Error("a video whose every frame is blank was reported as content-bearing")
		}
	})

	t.Run("frames with content pass", func(t *testing.T) {
		g := contentGate(samples(write("c.jpg", 14000, 'c'), write("d.jpg", 17000, 'd')))
		if g["status"] != "pass" {
			t.Errorf("real frames rejected: %v", g["message"])
		}
	})

	t.Run("identical frames fail even when large", func(t *testing.T) {
		// Nothing changed across the timeline: the scenes never rendered.
		g := contentGate(samples(write("e.jpg", 15000, 'x'), write("f.jpg", 15000, 'x')))
		if g["status"] != "fail" {
			t.Error("a static video was reported as varying content")
		}
	})

	t.Run("unreadable or absent samples do not manufacture a verdict", func(t *testing.T) {
		if g := contentGate(samples(filepath.Join(dir, "missing.jpg"))); g["status"] != "pass" {
			t.Error("an unreadable sample produced a failure verdict it cannot support")
		}
		if g := contentGate(map[string]any{"samples": []map[string]any{}}); g["status"] != "pass" {
			t.Error("no samples produced a failure verdict")
		}
	})

	t.Run("a single frame is not judged static", func(t *testing.T) {
		// One sample cannot show variation; refusing it would be wrong.
		if g := contentGate(samples(write("g.jpg", 15000, 'g'))); g["status"] != "pass" {
			t.Error("a single content-bearing frame was rejected")
		}
	})
}
