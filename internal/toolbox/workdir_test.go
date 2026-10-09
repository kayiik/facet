package toolbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultOutputFollowsTheCallsWorkingFolder(t *testing.T) {
	if got := defaultOutput(context.Background(), "edge_tts.mp3"); got != "edge_tts.mp3" {
		t.Fatalf("without a working folder the CLI keeps its relative default: %q", got)
	}
	dir := t.TempDir()
	ctx := WithWorkDir(context.Background(), dir)
	if got, want := defaultOutput(ctx, "edge_tts.mp3"), filepath.Join(dir, "edge_tts.mp3"); got != want {
		t.Fatalf("defaultOutput = %q, want %q", got, want)
	}
	abs := filepath.Join(t.TempDir(), "named.mp3")
	if got := defaultOutput(ctx, abs); got != abs {
		t.Fatalf("an absolute name moved: %q", got)
	}
	if WithWorkDir(context.Background(), "") != context.Background() {
		t.Fatal("an empty working folder changed the context")
	}
}

// A tool run with a working folder writes its default output there, not in
// the process's working directory.
func TestRunWritesDefaultOutputsInTheWorkingFolder(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	t.Chdir(elsewhere)
	env := RunContext(WithWorkDir(context.Background(), root), "subtitle_gen",
		[]byte(`{"segments":[{"text":"Hello","start":0,"end":1}]}`))
	if !env.OK {
		t.Fatalf("subtitle_gen failed: %+v", env.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "subtitles.srt")); err != nil {
		t.Fatalf("the default output is not in the working folder: %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("the default output landed in the process's working directory: %v", entries)
	}
}
