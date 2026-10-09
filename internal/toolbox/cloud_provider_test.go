package toolbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCloudProvidersRequireCredentialsOrExplicitMock(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "FAL_KEY", "FLUX_API_KEY", "KLING_API_KEY"} {
		t.Setenv(key, "")
	}
	t.Setenv("PATH", t.TempDir())
	for name, fn := range map[string]func(string, []byte) (any, []string, error){
		"openai": doOpenAIImage, "flux": doFluxImage, "kling": doKlingVideo, "sora": doSoraVideo,
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "new", "output")
			r := map[string]any{"prompt": "test", "output_path": out}
			value, _, err := fn("run", providerRequest(t, r))
			if err == nil || value != nil {
				t.Fatalf("missing credentials succeeded: %v %v", value, err)
			}
			if _, err := os.Stat(filepath.Dir(out)); !os.IsNotExist(err) {
				t.Fatal("missing credentials created output directory")
			}
			r["mock"] = true
			r["timeout_seconds"] = -1
			if _, _, err := fn("run", providerRequest(t, r)); err == nil {
				t.Fatal("invalid timeout accepted in mock mode")
			}
			if _, err := os.Stat(filepath.Dir(out)); !os.IsNotExist(err) {
				t.Fatal("invalid timeout created output directory")
			}
			delete(r, "timeout_seconds")
			value, _, err = fn("run", providerRequest(t, r))
			if name == "kling" || name == "sora" {
				// A mock video is made with FFmpeg, which this PATH lacks:
				// the run fails rather than writing bytes that only claim
				// to be video.
				var failed *toolFailure
				if err == nil || !errors.As(err, &failed) || failed.err.Code != "dependency_missing" {
					t.Fatalf("mock video without ffmpeg: %v %v", value, err)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("a failed mock wrote an artifact")
				}
				return
			}
			if err != nil || value.(map[string]any)["mock"] != true {
				t.Fatalf("explicit mock failed: %v %v", value, err)
			}
			if info, err := os.Stat(out); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("missing mock artifact: %v", err)
			}
		})
	}
}

func TestCloudProviderValidationBeforeEffects(t *testing.T) {
	for name, fn := range map[string]func(string, []byte) (any, []string, error){
		"openai": doOpenAIImage, "flux": doFluxImage, "kling": doKlingVideo, "sora": doSoraVideo,
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "new", "output")
			for _, field := range []string{"aspect_ratio", "timeout_seconds"} {
				r := map[string]any{"prompt": "test", "mock": true, "output_path": out, field: "bad"}
				if field == "timeout_seconds" {
					r[field] = int64(9223372036854775807)
				}
				for _, op := range []string{"run", "estimate"} {
					if _, _, err := fn(op, providerRequest(t, r)); err == nil {
						t.Fatalf("accepted invalid %s for %s", field, op)
					}
				}
			}
			if _, err := os.Stat(filepath.Dir(out)); !os.IsNotExist(err) {
				t.Fatal("validation created output directory")
			}
		})
	}
}
