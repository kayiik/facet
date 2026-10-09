package toolbox

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func composeDeliveryFixture(t *testing.T, script string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is needed for the local fake renderer")
	}
	_, workspace := isolateComposeRuntime(t)
	composer := filepath.Join(workspace, "remotion-composer")
	t.Setenv(ComposerDirEnv, composer)
	composeRuntimeFixture(t, filepath.Join(composer, "package.json"), "{}")
	composeRuntimeFixture(t, filepath.Join(composer, "src", "index.tsx"), "")
	// This CLI only copies local fixture bytes; it cannot invoke Remotion or a provider.
	composeRuntimeFixture(t, filepath.Join(composer, "node_modules", "@remotion", "cli", "remotion-cli.js"), script)
	return workspace
}

func composeDeliveryMedia(t *testing.T, path string, audio bool) {
	t.Helper()
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi"}
	if audio {
		args = append(args, "-i", "sine=frequency=440:duration=0.2", "-c:a", "pcm_s16le")
	} else {
		args = append(args, "-i", "color=size=16x16:rate=10:duration=0.2", "-c:v", "libx264", "-pix_fmt", "yuv420p")
	}
	args = append(args, path)
	if _, err := runCommand(10*time.Second, "ffmpeg", args...); err != nil {
		t.Fatal(err)
	}
}

func TestRemotionDeliveryFacts(t *testing.T) {
	schema := description("video_compose")["result_schema"].(map[string]any)
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("compose result must advertise a closed object: %#v", schema)
	}
	properties := schema["properties"].(map[string]any)
	required := schema["required"].([]string)
	for _, key := range required {
		if key == "output_facts" {
			t.Fatal("output_facts must remain optional for non-render operations")
		}
	}
	requireFFmpeg(t)
	for _, mux := range []bool{false, true} {
		t.Run(fmt.Sprintf("mux=%t", mux), func(t *testing.T) {
			workspace := composeDeliveryFixture(t, "require('fs').copyFileSync('../source.mp4', process.argv[5]);\n")
			source := filepath.Join(workspace, "source.mp4")
			composeDeliveryMedia(t, source, false)
			req := composeRequest{}
			if mux {
				req.AudioPath = filepath.Join(workspace, "audio.wav")
				composeDeliveryMedia(t, req.AudioPath, true)
			}
			output := filepath.Join(workspace, "output.mp4")
			composeRuntimeFixture(t, output, "previous delivery")
			result, warnings, err := doRemotionRender(req, output, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			got := result.(map[string]any)
			for _, key := range required {
				if _, ok := got[key]; !ok {
					t.Fatalf("render result missing required property %q", key)
				}
			}
			for key, value := range got {
				property, ok := properties[key].(map[string]any)
				if !ok {
					t.Fatalf("render result contains unadvertised property %q", key)
				}
				switch property["type"] {
				case "string":
					if _, ok := value.(string); !ok {
						t.Fatalf("render property %q must be a string, got %T", key, value)
					}
				case "object":
					if _, ok := value.(map[string]any); !ok {
						t.Fatalf("render property %q must be an object, got %T", key, value)
					}
				default:
					t.Fatalf("unexpected schema type for render property %q: %#v", key, property)
				}
			}
			facts, ok := got["output_facts"].(map[string]any)
			if !ok || got["output"] != output || facts["input"] != output {
				t.Fatalf("missing published output facts: %#v", got)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			wantHash := fmt.Sprintf("%x", sha256.Sum256(data))
			if facts["sha256"] != wantHash || hasAudio(facts) != mux {
				t.Fatalf("facts do not match delivered bytes/audio: %#v", facts)
			}
			if mux {
				original, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if wantHash == fmt.Sprintf("%x", sha256.Sum256(original)) {
					t.Fatal("hash binds the pre-mux video, not the final delivery")
				}
			} else if !strings.Contains(strings.Join(warnings, ";"), "no audio stream found") {
				t.Fatalf("probe warnings lost: %v", warnings)
			}
			// Container metadata is unchanged, but an appended byte changes artifact identity.
			if err := os.WriteFile(output, append(data, 0), 0644); err != nil {
				t.Fatal(err)
			}
			tampered, _, err := probe(output, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if tampered["sha256"] == facts["sha256"] {
				t.Fatal("tampered delivery still matches recorded evidence")
			}
		})
	}
}

func TestRemotionRenderRequestsDeliverySafeColorProfile(t *testing.T) {
	requireFFmpeg(t)
	script := `
if (!process.argv.includes('--pixel-format=yuv420p')) throw Error('missing delivery-safe pixel format');
if (!process.argv.includes('--color-space=bt709')) throw Error('missing delivery-safe color space');
require('fs').copyFileSync('../source.mp4', process.argv[5]);
`
	workspace := composeDeliveryFixture(t, script)
	composeDeliveryMedia(t, filepath.Join(workspace, "source.mp4"), false)

	_, _, err := doRemotionRender(composeRequest{}, filepath.Join(workspace, "output.mp4"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRemotionDeliveryFailsClosed(t *testing.T) {
	requireFFmpeg(t)
	for _, scenario := range []string{"missing-artifact", "invalid-artifact", "missing-audio", "mux-error", "publish-error"} {
		t.Run(scenario, func(t *testing.T) {
			script := "require('fs').copyFileSync('../source.mp4', process.argv[5]);\n"
			if scenario == "missing-artifact" {
				script = "process.exit(0);\n"
			} else if scenario == "invalid-artifact" {
				script = "require('fs').writeFileSync(process.argv[5], 'not a video');\n"
			}
			workspace := composeDeliveryFixture(t, script)
			composeDeliveryMedia(t, filepath.Join(workspace, "source.mp4"), false)
			output := filepath.Join(workspace, "output.mp4")
			preserved := output
			if scenario == "publish-error" {
				preserved = filepath.Join(output, "keep.txt")
			}
			composeRuntimeFixture(t, preserved, "previous delivery")
			req := composeRequest{}
			wantCode, wantMessage := "input_not_found", "input does not exist"
			switch scenario {
			case "invalid-artifact":
				wantCode, wantMessage = "input_probe_failed", "ffprobe could not inspect input"
			case "missing-audio", "mux-error":
				req.AudioPath = filepath.Join(workspace, "audio.wav")
				if scenario == "mux-error" {
					composeRuntimeFixture(t, req.AudioPath, "invalid audio")
					wantCode, wantMessage = "command_failed", "ffmpeg failed"
				}
			case "publish-error":
				wantCode, wantMessage = "command_failed", "remotion output could not be published"
			}
			result, _, err := doRemotionRender(req, output, 10*time.Second)
			if err == nil || result != nil {
				t.Fatalf("false delivery success: result=%v err=%v", result, err)
			}
			failure := errorEnvelope("video_compose", "run", err).Error
			if failure.Code != wantCode || !strings.Contains(failure.Message, wantMessage) {
				encoded, _ := json.Marshal(failure)
				t.Fatalf("wrong failure: %s; want %s / %s", encoded, wantCode, wantMessage)
			}
			data, err := os.ReadFile(preserved)
			if err != nil || string(data) != "previous delivery" {
				t.Fatalf("previous output lost: %q, %v", data, err)
			}
			for _, pattern := range []string{".facet-*", ".remotion_props.json"} {
				leftovers, err := filepath.Glob(filepath.Join(workspace, pattern))
				if err != nil || len(leftovers) != 0 {
					t.Fatalf("temporary artifacts left behind: %v, %v", leftovers, err)
				}
			}
		})
	}
}
