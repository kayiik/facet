package toolbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePiperModelUsesInstalledVoiceForDefaultName(t *testing.T) {
	fakeRuntime(t)
	model := filepath.Join(t.TempDir(), "en_US-lessac-medium.onnx")
	if err := os.WriteFile(model, []byte("model"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACET_PIPER_MODEL", model)

	for _, requested := range []string{"", "en_US-lessac-medium", "en_US-lessac-medium.onnx"} {
		if got := resolvePiperModel(requested); got != model {
			t.Errorf("resolvePiperModel(%q) = %q, want %q", requested, got, model)
		}
	}

	explicit := filepath.Join(t.TempDir(), "other.onnx")
	if got := resolvePiperModel(explicit); got != explicit {
		t.Errorf("explicit model = %q, want %q", got, explicit)
	}
}

// Without FACET_PIPER_MODEL, the voice the installer placed under the
// runtime's dependencies is used, for the default and for any bare voice name
// installed there.
func TestResolvePiperModelUsesRuntimeVoices(t *testing.T) {
	root := fakeRuntime(t)
	t.Setenv("FACET_PIPER_MODEL", "")
	if got := resolvePiperModel(""); got != defaultPiperVoice {
		t.Fatalf("with no voice installed, resolvePiperModel(\"\") = %q, want the bare default", got)
	}
	voices := filepath.Join(root, "dependencies", "voices")
	if err := os.MkdirAll(voices, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{defaultPiperVoice, "en_GB-alan-low"} {
		for _, ext := range []string{".onnx", ".onnx.json"} {
			if err := os.WriteFile(filepath.Join(voices, name+ext), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for requested, want := range map[string]string{
		"":                           filepath.Join(voices, defaultPiperVoice+".onnx"),
		defaultPiperVoice:            filepath.Join(voices, defaultPiperVoice+".onnx"),
		"en_GB-alan-low.onnx":        filepath.Join(voices, "en_GB-alan-low.onnx"),
		"en_US-not-installed":        "en_US-not-installed",
		filepath.Join("x", "y.onnx"): filepath.Join("x", "y.onnx"),
	} {
		if got := resolvePiperModel(requested); !(got == want || sameFile(got, want)) {
			t.Errorf("resolvePiperModel(%q) = %q, want %q", requested, got, want)
		}
	}

	// An explicit FACET_PIPER_MODEL still wins over the runtime voice.
	configured := filepath.Join(t.TempDir(), "custom.onnx")
	t.Setenv("FACET_PIPER_MODEL", configured)
	if got := resolvePiperModel(""); got != configured {
		t.Fatalf("FACET_PIPER_MODEL ignored: %q", got)
	}
}

// A missing piper names what to install, under the code every other absent
// binary uses.
func TestPiperMissingIsADependencyFailure(t *testing.T) {
	fakeRuntime(t)
	t.Setenv("PATH", t.TempDir())
	_, _, err := doPiperTTS("run", []byte(`{"text":"hello","output_path":"`+filepath.ToSlash(filepath.Join(t.TempDir(), "x.wav"))+`"}`))
	var tf *toolFailure
	if !asToolFailure(err, &tf) || tf.err.Code != "dependency_missing" {
		t.Fatalf("missing piper reported as %v", err)
	}
}
