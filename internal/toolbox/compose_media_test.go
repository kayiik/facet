package toolbox

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func composeMediaFileURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestStageRemotionMediaPaths(t *testing.T) {
	_, workspace := isolateComposeRuntime(t)
	composer := t.TempDir()
	public := t.TempDir()
	// Signature fixtures test staging, not decodability. The opt-in test below
	// exercises actual generated audio/video through the real Remotion renderer.
	wav := "RIFF\x24\x00\x00\x00WAVEfmt "
	video := "\x00\x00\x00\x18ftypisom\x00\x00\x00\x00isommp42"
	png := "\x89PNG\r\n\x1a\n"
	audioPath := filepath.Join(workspace, "narration", "tone #1.wav")
	composeRuntimeFixture(t, audioPath, wav)
	composeRuntimeFixture(t, filepath.Join(workspace, "assets", "clip.mp4"), video)
	composeRuntimeFixture(t, filepath.Join(workspace, "assets", "still.png"), png)
	composeRuntimeFixture(t, filepath.Join(composer, "public", "bundled.wav"), wav)
	composeRuntimeFixture(t, filepath.Join(workspace, ".env"), "never-public")
	composeRuntimeFixture(t, filepath.Join(composer, "public", "unreferenced.png"), png)
	remote := "https://example.invalid/a.mp4?token=keep%2Bexact#fragment"
	props := map[string]any{
		"cuts":     []any{map[string]any{"type": "media", "source": "assets/clip.mp4", "media_kind": "video", "title": ".env"}},
		"audio":    map[string]any{"narration": map[string]any{"src": composeMediaFileURL(audioPath)}, "music": map[string]any{"src": "bundled.wav"}},
		"scenes":   []any{map[string]any{"type": "media", "source": remote, "media_kind": "video"}},
		"metadata": map[string]any{"src": ".env"},
	}
	original, _ := json.Marshal(props)
	result, err := stageRemotionMedia(original, composer, public, "")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(props)
	if !bytes.Equal(original, again) {
		t.Fatal("caller props mutated")
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	assertAsset := func(value any, content string) {
		t.Helper()
		name, ok := value.(string)
		if !ok || !strings.HasPrefix(name, "asset-") || filepath.Base(name) != name {
			t.Fatalf("not an isolated staticFile reference: %v", value)
		}
		data, err := os.ReadFile(filepath.Join(public, name))
		if err != nil || string(data) != content {
			t.Fatalf("wrong staged bytes: %q %v", data, err)
		}
	}
	cut := got["cuts"].([]any)[0].(map[string]any)
	assertAsset(cut["source"], video)
	audio := got["audio"].(map[string]any)
	assertAsset(audio["narration"].(map[string]any)["src"], wav)
	assertAsset(audio["music"].(map[string]any)["src"], wav)
	scene := got["scenes"].([]any)[0].(map[string]any)
	if scene["source"] != remote || got["metadata"].(map[string]any)["src"] != ".env" || cut["title"] != ".env" {
		t.Fatalf("non-media props or remote URLs changed: %s", result)
	}
	entries, err := os.ReadDir(public)
	if err != nil || len(entries) != 3 {
		t.Fatalf("unexpected staged files: %v %v", entries, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "asset-") || entry.IsDir() {
			t.Fatalf("unreferenced data exposed: %s", entry.Name())
		}
	}
}

func TestStageRemotionMediaRejectsUnsafeSources(t *testing.T) {
	_, workspace := isolateComposeRuntime(t)
	composeRuntimeFixture(t, filepath.Join(workspace, ".env"), "private")
	composeRuntimeFixture(t, filepath.Join(workspace, "disguised.mp4"), "private configuration, not media")
	composeRuntimeFixture(t, filepath.Join(workspace, "active.svg"), "<svg><script>alert(1)</script></svg>")
	if err := os.Mkdir(filepath.Join(workspace, "folder.wav"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{".env", "disguised.mp4", "active.svg", "folder.wav", "missing.wav", "../outside.wav", "file://server/share/secret.wav", "file:relative.wav", "file:///tmp/a.wav?x=1", "file:///tmp/a.wav#x", "ftp://example.test/a.wav", "//server/share/a.wav", `\\server\share\a.wav`, "audio.wav:secret", "javascript:alert(1)"} {
		t.Run(src, func(t *testing.T) {
			data, _ := json.Marshal(map[string]any{"audio": map[string]any{"narration": map[string]any{"src": src}}})
			public := t.TempDir()
			if _, err := stageRemotionMedia(data, t.TempDir(), public, ""); err == nil {
				t.Fatalf("unsafe/missing media accepted: %q", src)
			}
			entries, _ := os.ReadDir(public)
			if len(entries) != 0 {
				t.Fatalf("rejected media exposed: %v", entries)
			}
		})
	}
}

func TestStageRemotionMediaConfinesSymlinks(t *testing.T) {
	_, workspace := isolateComposeRuntime(t)
	outside := filepath.Join(t.TempDir(), "outside.wav")
	composeRuntimeFixture(t, outside, "RIFF\x24\x00\x00\x00WAVEfmt ")
	if err := os.Symlink(outside, filepath.Join(workspace, "escape.wav")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := stageRemotionMedia([]byte(`{"audio":{"narration":{"src":"escape.wav"}}}`), t.TempDir(), t.TempDir(), ""); err == nil {
		t.Fatal("relative symlink escaped project")
	}
}

func TestStageRemotionMediaLimits(t *testing.T) {
	_, workspace := isolateComposeRuntime(t)
	file, err := os.Create(filepath.Join(workspace, "oversized.wav"))
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate((2 << 30) + 1)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	public := t.TempDir()
	if _, err := stageRemotionMedia([]byte(`{"audio":{"narration":{"src":"oversized.wav"}}}`), t.TempDir(), public, ""); err == nil {
		t.Fatal("oversized source accepted")
	}
	entries, _ := os.ReadDir(public)
	if len(entries) != 0 {
		t.Fatal("oversized source copied before validation")
	}
}

func TestRemotionMediaStagingCLIAndCleanup(t *testing.T) {
	requireFFmpeg(t)
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			script := `
const fs = require('fs'), path = require('path');
const flag = name => process.argv.find(v => v.startsWith(name + '=')).slice(name.length + 1);
const publicDir = flag('--public-dir'), propsPath = flag('--props');
fs.writeFileSync('../staging-path.txt', path.dirname(publicDir));
const props = JSON.parse(fs.readFileSync(propsPath));
if (path.dirname(propsPath) !== path.dirname(publicDir)) throw Error('props placement');
if (fs.readdirSync(publicDir).length !== 2) throw Error('blanket public exposure');
for (const src of [props.cuts[0].source, props.audio.narration.src]) {
  if (!/^asset-\d+\.(wav|mp4)$/.test(src) || !fs.statSync(path.join(publicDir, src)).isFile()) throw Error('unstaged media');
}
fs.copyFileSync('../source.mp4', process.argv[5]);
`
			if fail {
				script += "process.exit(23);\n"
			}
			workspace := composeDeliveryFixture(t, script)
			composeDeliveryMedia(t, filepath.Join(workspace, "source.mp4"), false)
			composeDeliveryMedia(t, filepath.Join(workspace, "tone.wav"), true)
			composeRuntimeFixture(t, filepath.Join(workspace, ".env"), "secret")
			composeRuntimeFixture(t, filepath.Join(workspace, ".remotion_props.json"), "user-owned props")
			props := map[string]any{"cuts": []any{map[string]any{"type": "media", "source": "source.mp4", "media_kind": "video", "in_seconds": 0.0, "out_seconds": 1.0}}, "audio": map[string]any{"narration": map[string]any{"src": composeMediaFileURL(filepath.Join(workspace, "tone.wav"))}}}
			before, _ := json.Marshal(props)
			_, _, err := doRemotionRender(composeRequest{RawProps: props}, filepath.Join(workspace, "output.mp4"), 15*time.Second)
			if (err != nil) != fail {
				t.Fatalf("render error=%v, want failure=%v", err, fail)
			}
			after, _ := json.Marshal(props)
			if !bytes.Equal(before, after) {
				t.Fatal("caller props mutated")
			}
			staging, err := os.ReadFile(filepath.Join(workspace, "staging-path.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(string(staging)); !os.IsNotExist(err) {
				t.Fatalf("staging not cleaned: %v", err)
			}
			original, err := os.ReadFile(filepath.Join(workspace, ".remotion_props.json"))
			if err != nil || string(original) != "user-owned props" {
				t.Fatalf("existing props file clobbered: %q %v", original, err)
			}
		})
	}
}

func TestRemotionLocalMediaOfflineRender(t *testing.T) {
	if testing.Short() || os.Getenv("FACET_REMOTION_MEDIA_INTEGRATION") != "1" {
		t.Skip("opt in with FACET_REMOTION_MEDIA_INTEGRATION=1; requires installed composer dependencies and Chromium, no downloads")
	}
	requireFFmpeg(t)
	composer, err := filepath.Abs(filepath.Join("..", "..", "remotion-composer"))
	if err != nil {
		t.Fatal(err)
	}
	if installed := os.Getenv("FACET_REMOTION_TEST_COMPOSER"); installed != "" {
		composer, err = filepath.Abs(installed)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !fileExists(filepath.Join(composer, "node_modules", "@remotion", "cli", "remotion-cli.js")) || findBrowserExecutable() == "" {
		t.Fatal("offline render requires existing composer dependencies and browser")
	}
	_, workspace := isolateComposeRuntime(t)
	t.Setenv(ComposerDirEnv, composer)
	composeDeliveryMedia(t, filepath.Join(workspace, "clip.mp4"), false)
	composeDeliveryMedia(t, filepath.Join(workspace, "tone.wav"), true)
	props := map[string]any{
		"cuts":  []any{map[string]any{"id": "local-video", "type": "media", "source": "clip.mp4", "media_kind": "video", "in_seconds": 0, "out_seconds": 0.2}},
		"audio": map[string]any{"narration": map[string]any{"src": composeMediaFileURL(filepath.Join(workspace, "tone.wav"))}},
	}
	props["timeout_seconds"] = 120
	data, _ := json.Marshal(props)
	result, _, err := doVideoCompose("run", data)
	if err != nil {
		t.Fatalf("offline fixture render: %+v", errorEnvelope("video_compose", "run", err).Error)
	}
	facts := result.(map[string]any)["output_facts"].(map[string]any)
	if !hasAudio(facts) {
		t.Fatalf("real render lacks local narration: %v", facts)
	}
	if _, err := runCommand(15*time.Second, "ffmpeg", "-v", "error", "-i", "renders/final.mp4", "-f", "null", "-"); err != nil {
		t.Fatalf("rendered fixture does not decode: %v", err)
	}
}
