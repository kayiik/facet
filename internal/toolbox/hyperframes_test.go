package toolbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// HyperFrames runs only from a pinned install. An `npx` on PATH must never be
// used as a fallback: npx resolves whatever the registry serves that day.
func TestHyperFramesNeverFallsBackToNpx(t *testing.T) {
	fakeRuntime(t)
	t.Setenv(hyperframesEnv, "")
	onPath := t.TempDir()
	for _, name := range []string{"npx", "node", "ffmpeg"} {
		writeExecutable(t, filepath.Join(onPath, name+exeSuffix()))
	}
	t.Setenv("PATH", onPath)
	workspace := t.TempDir()
	for _, operation := range []string{"lint", "render", "add_block"} {
		request, _ := json.Marshal(map[string]any{"operation": operation, "workspace_path": workspace, "block_name": "x", "output_path": filepath.Join(workspace, "out.mp4")})
		_, _, err := doHyperFramesCompose("run", request)
		var tf *toolFailure
		if !asToolFailure(err, &tf) || tf.err.Code != "dependency_missing" || !strings.Contains(tf.err.Message, "HyperFrames is not installed") {
			t.Fatalf("%s without a pinned install: %v", operation, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "out.mp4")); !os.IsNotExist(err) {
		t.Fatal("a render without a runtime produced output")
	}
}

func TestHyperFramesResolvesThePinnedEntry(t *testing.T) {
	root := fakeRuntime(t)
	t.Setenv(hyperframesEnv, "")
	pinned := filepath.Join(root, "dependencies", "hyperframes", "node_modules", "hyperframes", "bin", "hyperframes.mjs")
	if _, err := hyperframesEntry(); err == nil {
		t.Fatal("an entry resolved before one was installed")
	}
	if err := os.MkdirAll(filepath.Dir(pinned), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinned, []byte("// pinned"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := hyperframesEntry()
	if err != nil || !sameFile(got, pinned) {
		t.Fatalf("hyperframesEntry() = %q, %v; want %q", got, err, pinned)
	}

	// An explicit FACET_HYPERFRAMES wins, and a missing one is an error rather
	// than a silent switch to another install.
	explicit := filepath.Join(t.TempDir(), "hyperframes.mjs")
	if err := os.WriteFile(explicit, []byte("// explicit"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hyperframesEnv, explicit)
	if got, err := hyperframesEntry(); err != nil || !sameFile(got, explicit) {
		t.Fatalf("FACET_HYPERFRAMES ignored: %q, %v", got, err)
	}
	t.Setenv(hyperframesEnv, filepath.Join(t.TempDir(), "missing.mjs"))
	if _, err := hyperframesEntry(); err == nil {
		t.Fatal("a missing FACET_HYPERFRAMES fell back to another install")
	}
}

// The listing declares what a run needs: node, the pinned CLI and ffmpeg —
// and no longer npx.
func TestHyperFramesDeclaresItsRuntime(t *testing.T) {
	fakeRuntime(t)
	t.Setenv(hyperframesEnv, "")
	deps, _ := summary("hyperframes_compose")["dependencies"].([]any)
	var names []string
	for _, d := range deps {
		m := d.(map[string]any)
		names = append(names, m["name"].(string))
		if m["name"] == "hyperframes" && (m["available"] != false || m["type"] != "runtime" || m["resolution"] != ResolutionUnsatisfied) {
			t.Fatalf("an absent pinned CLI is reported as %#v", m)
		}
	}
	if strings.Join(names, ",") != "node,hyperframes,ffmpeg" {
		t.Fatalf("hyperframes_compose declares %v", names)
	}
	if summary("hyperframes_compose")["configured"] != false {
		t.Fatal("hyperframes_compose reports configured without its pinned CLI")
	}
}

// Scaffolding writes files and needs no runtime at all.
func TestHyperFramesScaffoldNeedsNoRuntime(t *testing.T) {
	fakeRuntime(t)
	t.Setenv(hyperframesEnv, "")
	t.Setenv("PATH", t.TempDir())
	workspace := filepath.Join(t.TempDir(), "hf")
	request, _ := json.Marshal(map[string]any{"operation": "scaffold_workspace", "workspace_path": workspace})
	result, _, err := doHyperFramesCompose("run", request)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := result.(map[string]any)["files"].([]string)
	if len(files) != 2 {
		t.Fatalf("scaffold reported files %v", files)
	}
	for _, file := range files {
		if !fileExists(file) {
			t.Fatalf("reported file %s was not written", file)
		}
	}
	doctor, _, err := doHyperFramesCompose("run", []byte(`{"operation":"doctor"}`))
	if err != nil {
		t.Fatal(err)
	}
	check := doctor.(map[string]any)["runtime_check"].(map[string]any)
	if check["runtime_available"] != false || check["hyperframes_available"] != false {
		t.Fatalf("doctor reports a runtime that is not installed: %#v", check)
	}
}

// video_compose declares no network; HyperFrames needs it. The runtime is
// offered only through hyperframes_compose, where the effect is declared.
func TestVideoComposeRefusesTheHyperFramesRuntime(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "renders", "out.mp4")
	request, _ := json.Marshal(map[string]any{"operation": "compose", "output_path": output,
		"edit_decisions": map[string]any{"render_runtime": "hyperframes", "cuts": []any{map[string]any{"source": "a.mp4", "in_seconds": 0, "out_seconds": 1}}}})
	for _, op := range []string{"estimate", "run"} {
		_, _, err := doVideoCompose(op, request)
		var tf *toolFailure
		if !asToolFailure(err, &tf) || tf.err.Code != "invalid_request" || !strings.Contains(tf.err.Message, "hyperframes_compose") {
			t.Fatalf("%s accepted render_runtime hyperframes: %v", op, err)
		}
	}
	if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
		t.Fatal("a refused compose created its output directory")
	}
	if EffectsFor("video_compose").Network {
		t.Fatal("video_compose must stay a local, network-free tool")
	}
}
