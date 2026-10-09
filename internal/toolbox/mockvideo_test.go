package toolbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Mock mode makes a real placeholder video or none: without FFmpeg it once
// wrote a line of text into the .mp4 and reported it as video/mp4.
func TestMockVideoWithoutFFmpegFailsAndWritesNothing(t *testing.T) {
	fakeRuntime(t)
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	for _, tool := range []string{"kling_video", "sora_video", "gflow_video"} {
		out := filepath.Join(dir, tool+".mp4")
		req, _ := json.Marshal(map[string]any{"prompt": "waves", "mock": true, "output_path": out})
		env := RunContext(context.Background(), tool, req)
		if env.OK || env.Error == nil || env.Error.Code != "dependency_missing" {
			t.Errorf("%s: mock without ffmpeg = %+v, want dependency_missing", tool, env.Error)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("%s: wrote %s although no video could be made", tool, out)
		}
	}
}
