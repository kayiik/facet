package toolbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kolonist/edgetts"
)

// A fake piper: the test binary installed under a fake runtime's
// dependencies answers as piper when asked to, writing real WAV audio.
const fakePiperEnv = "FACET_TEST_FAKE_PIPER"

func init() {
	if os.Getenv(fakePiperEnv) != "1" || strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") != "piper" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	for i, arg := range os.Args {
		if arg == "--output_file" && i+1 < len(os.Args) {
			if err := os.WriteFile(os.Args[i+1], testWAV(0.5), 0o644); err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
	os.Exit(1)
}

// testWAV is mono 16-bit PCM silence: valid audio without any encoder.
func testWAV(seconds float64) []byte {
	const rate = 22050
	data := make([]byte, int(seconds*rate)*2)
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString("RIFF")
	_ = binary.Write(&b, le, uint32(36+len(data)))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, le, uint32(16))
	_ = binary.Write(&b, le, uint16(1))
	_ = binary.Write(&b, le, uint16(1))
	_ = binary.Write(&b, le, uint32(rate))
	_ = binary.Write(&b, le, uint32(rate*2))
	_ = binary.Write(&b, le, uint16(2))
	_ = binary.Write(&b, le, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, le, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

// offlineTransport answers the provider APIs the stock and speech tools call,
// from memory. Any other request fails the test: these runs must never reach
// a network.
type offlineTransport struct {
	t     *testing.T
	video []byte
}

func (o offlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	respond := func(contentType string, body []byte) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header:  http.Header{"Content-Type": []string{contentType}},
			Body:    io.NopCloser(bytes.NewReader(body)),
			Request: req, ContentLength: int64(len(body)),
		}, nil
	}
	host, path := req.URL.Host, req.URL.Path
	switch {
	case host == "api.pexels.com" && path == "/videos/search":
		return respond("application/json", []byte(`{"total_results":1,"videos":[{"id":7,"duration":2,"url":"https://www.pexels.com/video/7","user":{"name":"Ada"},"video_files":[{"id":1,"quality":"hd","width":320,"height":240,"fps":24,"link":"https://media.example.test/pexels.mp4"}]}]}`))
	case host == "pixabay.com" && path == "/api/videos/":
		return respond("application/json", []byte(`{"total":1,"hits":[{"id":9,"duration":2,"user":"Bo","tags":"ocean","pageURL":"https://pixabay.com/videos/9","videos":{"large":{"url":"https://media.example.test/pixabay.mp4","width":320,"height":240,"size":1000}}}]}`))
	case host == "commons.wikimedia.org" && path == "/w/api.php":
		return respond("application/json", []byte(`{"query":{"pages":{"11":{"pageid":11,"title":"File:Ocean.mp4","imageinfo":[{"url":"https://media.example.test/wikimedia.mp4","descriptionurl":"https://commons.wikimedia.org/wiki/File:Ocean.mp4","width":320,"height":240,"duration":2.0,"mime":"video/mp4"}]}}}}`))
	case host == "media.example.test":
		return respond("video/mp4", o.video)
	case host == "api.openai.com" && path == "/v1/audio/speech",
		host == "api.elevenlabs.io" && strings.HasPrefix(path, "/v1/text-to-speech/"):
		return respond("audio/wav", testWAV(0.5))
	}
	o.t.Errorf("unexpected network request: %s %s", req.Method, req.URL)
	return nil, fmt.Errorf("network disabled in offline conformance test")
}

type conformFixtures struct {
	dir, video, video2, silent, speech, music, image, subtitles string
}

func newConformFixtures(t *testing.T) conformFixtures {
	t.Helper()
	requireFFmpeg(t)
	dir := t.TempDir()
	f := conformFixtures{
		dir:       dir,
		video:     filepath.Join(dir, "video.mp4"),
		video2:    filepath.Join(dir, "video2.mp4"),
		silent:    filepath.Join(dir, "silent.mp4"),
		speech:    filepath.Join(dir, "speech.mp4"),
		music:     filepath.Join(dir, "library", "music.wav"),
		image:     filepath.Join(dir, "overlay.png"),
		subtitles: filepath.Join(dir, "captions.srt"),
	}
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", f.video)
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=24:duration=2", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", f.video2)
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:size=320x240:rate=24:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", f.silent)
	ffmpeg(t, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:size=160x120:rate=24:duration=3.5",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1.0",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo:d=1.5",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1.0",
		"-filter_complex", "[1:a][2:a][3:a]concat=n=3:v=0:a=1[a]",
		"-map", "0:v:0", "-map", "[a]", "-c:v", "libx264", "-c:a", "aac", "-shortest", f.speech)
	if err := os.MkdirAll(filepath.Dir(f.music), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=48000:duration=2", f.music)
	if err := createMockPNG(f.image, 64, 48, "overlay"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.subtitles, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

type conformCase struct {
	name  string
	tool  string
	setup func(t *testing.T) // optional per-case environment
	// request builds the request; it runs after setup.
	request func(t *testing.T) any
	// artifacts is how many files the run must describe; -1 skips the count.
	artifacts int
}

// Every tool's success result must satisfy the result schema it publishes,
// and the envelope must describe exactly the files the run produced.
//
// Each tool runs for real with offline inputs: ffmpeg on generated media,
// in-memory provider responses, mock modes, and fake CLIs. A result field the
// schema does not declare, a declared type the result does not honour, or an
// artifact whose digest does not match its bytes fails here.
func TestRunResultsConformToSchemas(t *testing.T) {
	f := newConformFixtures(t)
	videoBytes, err := os.ReadFile(f.video)
	if err != nil {
		t.Fatal(err)
	}
	previousTransport := http.DefaultTransport
	http.DefaultTransport = offlineTransport{t: t, video: videoBytes}
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	for _, key := range []string{"PEXELS_API_KEY", "PIXABAY_API_KEY", "FACET_PIPER_MODEL", hyperframesEnv} {
		t.Setenv(key, "")
	}
	out := func(name string) string { return filepath.Join(f.dir, "out", name) }
	target := map[string]any{"width": 240, "height": 320, "fps": 24, "fit": "cover", "video_codec": "h264", "pixel_format": "yuv420p", "audio_codec": "aac", "audio_sample_rate": 48000, "audio_channels": 2}
	req := func(v map[string]any) func(*testing.T) any { return func(*testing.T) any { return v } }

	cases := []conformCase{
		{name: "media_probe", tool: "media_probe", request: req(map[string]any{"input": f.video}), artifacts: 0},
		{name: "audio_probe", tool: "audio_probe", request: req(map[string]any{"input_path": f.video}), artifacts: 0},
		{name: "frame_sample", tool: "frame_sample", request: req(map[string]any{"input": f.video, "output_dir": out("frames"), "strategy": map[string]any{"type": "uniform", "count": 2}}), artifacts: 2},
		{name: "scene_detect", tool: "scene_detect", request: req(map[string]any{"input_path": f.video, "output_path": out("scenes.json"), "threshold": 0.2}), artifacts: 1},
		{name: "visual_qa/probe", tool: "visual_qa", request: req(map[string]any{"operation": "probe", "input_path": f.video, "expected": map[string]any{"width": 320, "has_audio": true}}), artifacts: 0},
		{name: "visual_qa/review", tool: "visual_qa", request: req(map[string]any{"operation": "review", "input_path": f.video, "output_dir": out("qa"), "timestamps": []float64{0.5, 1.0}}), artifacts: 2},
		{name: "visual_qa/audio_levels", tool: "visual_qa", request: req(map[string]any{"operation": "audio_levels", "input_path": f.video, "timestamps": []float64{0.2}}), artifacts: 0},
		{name: "output_review", tool: "output_review", request: req(map[string]any{"input": f.video, "samples": map[string]any{"type": "uniform", "count": 2}, "evidence_dir": out("evidence")}), artifacts: 2},
		{name: "source_edit", tool: "source_edit", request: req(map[string]any{"segments": []any{map[string]any{"input": f.video, "start": 0, "end": 1}, map[string]any{"input": f.silent, "start": 0, "end": 1}}, "target": target, "output": out("edit.mp4")}), artifacts: 1},
		{name: "video_trimmer/cut", tool: "video_trimmer", request: req(map[string]any{"operation": "cut", "input_path": f.video, "output_path": out("cut.mp4"), "start_seconds": 0.5, "end_seconds": 1.5, "codec": "libx264"}), artifacts: 1},
		{name: "video_trimmer/cut-open-ended", tool: "video_trimmer", request: req(map[string]any{"operation": "cut", "input_path": f.video, "output_path": out("cut-open.mp4"), "start_seconds": 1}), artifacts: 1},
		{name: "video_trimmer/speed", tool: "video_trimmer", request: req(map[string]any{"operation": "speed", "input_path": f.video, "output_path": out("speed.mp4"), "speed_factor": 2}), artifacts: 1},
		{name: "video_trimmer/concat", tool: "video_trimmer", request: req(map[string]any{"operation": "concat", "input_path": f.video, "segments": []any{map[string]any{"input": f.video}, map[string]any{"input": f.video2}}, "output_path": out("concat.mp4")}), artifacts: 1},
		{name: "video_stitch/validate", tool: "video_stitch", request: req(map[string]any{"operation": "validate", "clips": []string{f.video, f.silent}}), artifacts: 0},
		{name: "video_stitch/stitch", tool: "video_stitch", request: req(map[string]any{"operation": "stitch", "clips": []string{f.video, f.video2}, "output_path": out("stitch.mp4"), "preset": "ultrafast"}), artifacts: 1},
		{name: "video_stitch/spatial", tool: "video_stitch", request: req(map[string]any{"operation": "spatial", "clips": []string{f.video, f.video2}, "output_path": out("spatial.mp4"), "layout": "side_by_side"}), artifacts: 1},
		{name: "video_compose/encode", tool: "video_compose", request: req(map[string]any{"operation": "encode", "input_path": f.video, "output_path": out("encoded.mp4"), "preset": "ultrafast"}), artifacts: 1},
		{name: "video_compose/overlay", tool: "video_compose", request: req(map[string]any{"operation": "overlay", "input_path": f.video, "overlays": []any{map[string]any{"asset_path": f.image, "start_seconds": 0, "end_seconds": 1}}, "output_path": out("overlay.mp4")}), artifacts: 1},
		{name: "video_compose/burn_subtitles", tool: "video_compose", request: req(map[string]any{"operation": "burn_subtitles", "input_path": f.video, "subtitle_path": f.subtitles, "output_path": out("burned.mp4")}), artifacts: 1},
		{name: "video_compose/compose", tool: "video_compose", request: req(map[string]any{"operation": "compose", "edit_decisions": map[string]any{"cuts": []any{map[string]any{"source": f.video, "in_seconds": 0, "out_seconds": 1}}}, "output_path": out("composed.mp4")}), artifacts: 1},
		{name: "video_compose/remotion_render", tool: "video_compose",
			setup: func(t *testing.T) {
				workspace := composeDeliveryFixture(t, "require('fs').copyFileSync('../source.mp4', process.argv[5]);\n")
				composeDeliveryMedia(t, filepath.Join(workspace, "source.mp4"), false)
			},
			request:   req(map[string]any{"cuts": []any{map[string]any{"type": "text_card", "text": "Hello", "in_seconds": 0, "out_seconds": 1}}, "output": out("remotion.mp4")}),
			artifacts: 1},
		{name: "subtitle_gen", tool: "subtitle_gen", request: req(map[string]any{"segments": []any{map[string]any{"text": "hello world again", "start": 0, "end": 1.5}}, "output_path": out("subs.srt")}), artifacts: 1},
		{name: "ffmpeg_caption_burn", tool: "ffmpeg_caption_burn", request: req(map[string]any{"input_path": f.video, "output_path": out("captioned.mp4"), "segments": []any{map[string]any{"text": "one two three four five", "start": 0, "end": 1.5}}, "words_per_page": 2}), artifacts: 1},
		{name: "silence_cutter/remove", tool: "silence_cutter", request: req(map[string]any{"input_path": f.speech, "output_path": out("trimmed.mp4"), "mode": "remove", "silence_threshold_db": -30}), artifacts: 1},
		{name: "silence_cutter/speed_up", tool: "silence_cutter", request: req(map[string]any{"input_path": f.speech, "output_path": out("faster.mp4"), "mode": "speed_up", "silence_threshold_db": -30}), artifacts: 1},
		{name: "silence_cutter/mark", tool: "silence_cutter", request: req(map[string]any{"input_path": f.speech, "output_path": out("silences.json"), "mode": "mark", "silence_threshold_db": -30}), artifacts: 1},
		{name: "hyperframes_compose/doctor", tool: "hyperframes_compose", request: req(map[string]any{"operation": "doctor"}), artifacts: 0},
		{name: "hyperframes_compose/scaffold_workspace", tool: "hyperframes_compose", request: req(map[string]any{"operation": "scaffold_workspace", "workspace_path": out("hf-scaffold")}), artifacts: 2},
		{name: "hyperframes_compose/lint", tool: "hyperframes_compose", setup: func(t *testing.T) { fakeHyperFrames(t, f.video) }, request: req(map[string]any{"operation": "lint", "workspace_path": f.dir}), artifacts: 0},
		{name: "hyperframes_compose/add_block", tool: "hyperframes_compose", setup: func(t *testing.T) { fakeHyperFrames(t, f.video) }, request: req(map[string]any{"operation": "add_block", "block_name": "title-card", "workspace_path": f.dir}), artifacts: 0},
		{name: "hyperframes_compose/render", tool: "hyperframes_compose", setup: func(t *testing.T) { fakeHyperFrames(t, f.video) }, request: req(map[string]any{"operation": "render", "workspace_path": f.dir, "output_path": out("hyperframes.mp4")}), artifacts: 1},
		{name: "audio_mix", tool: "audio_mix", request: req(map[string]any{"video": f.video, "music": map[string]any{"input": f.music, "gain_db": -12}, "duration": "video", "output": out("mixed.mp4")}), artifacts: 1},
		{name: "music_library", tool: "music_library", request: req(map[string]any{"library_dir": filepath.Dir(f.music)}), artifacts: 0},
		{name: "direct_clip_search", tool: "direct_clip_search", request: req(map[string]any{"output_dir": out("clips"), "queries": []any{map[string]any{"query": "ocean", "slot_id": "s1"}}, "clips_per_query": 1, "extract_thumbnails": true}), artifacts: 2},
		{name: "pexels_video", tool: "pexels_video", setup: func(t *testing.T) { t.Setenv("PEXELS_API_KEY", "offline-test") }, request: req(map[string]any{"query": "ocean", "output_path": out("pexels.mp4")}), artifacts: 1},
		{name: "pixabay_video", tool: "pixabay_video", setup: func(t *testing.T) { t.Setenv("PIXABAY_API_KEY", "offline-test") }, request: req(map[string]any{"query": "ocean", "output_path": out("pixabay.mp4")}), artifacts: 1},
		{name: "wikimedia", tool: "wikimedia", request: req(map[string]any{"query": "ocean", "output_path": out("wikimedia.mp4")}), artifacts: 1},
		{name: "edge_tts", tool: "edge_tts", setup: func(t *testing.T) { fakeEdgeSynthesis(t) }, request: req(map[string]any{"text": "Hello there", "output_path": out("edge.wav")}), artifacts: 1},
		{name: "openai_tts", tool: "openai_tts", setup: func(t *testing.T) { t.Setenv("OPENAI_API_KEY", "offline-test") }, request: req(map[string]any{"text": "Hello there", "response_format": "wav", "output_path": out("openai.wav")}), artifacts: 1},
		{name: "elevenlabs_tts", tool: "elevenlabs_tts", setup: func(t *testing.T) { t.Setenv("ELEVENLABS_API_KEY", "offline-test") }, request: req(map[string]any{"text": "Hello there", "output_format": "pcm_22050", "output_path": out("eleven.wav")}), artifacts: 1},
		{name: "piper_tts", tool: "piper_tts", setup: func(t *testing.T) { fakePiper(t) }, request: req(map[string]any{"text": "Hello there", "output_path": out("piper.wav")}), artifacts: 1},
		{name: "openai_image", tool: "openai_image", request: req(map[string]any{"prompt": "a lighthouse", "mock": true, "output_path": out("openai.png")}), artifacts: 1},
		{name: "flux_image", tool: "flux_image", request: req(map[string]any{"prompt": "a lighthouse", "mock": true, "output_path": out("flux.png")}), artifacts: 1},
		{name: "kling_video", tool: "kling_video", request: req(map[string]any{"prompt": "waves", "mock": true, "output_path": out("kling.mp4")}), artifacts: 1},
		{name: "sora_video", tool: "sora_video", request: req(map[string]any{"prompt": "waves", "mock": true, "output_path": out("sora.mp4")}), artifacts: 1},
		{name: "gflow_video/mock", tool: "gflow_video", request: req(map[string]any{"prompt": "waves", "mock": true, "output_path": out("gflow-mock.mp4")}), artifacts: 1},
		{name: "gflow_video/cli", tool: "gflow_video", setup: func(t *testing.T) { fakeGFlow(t, "success") }, request: req(map[string]any{"prompt": "waves", "output_path": out("gflow.mp4")}), artifacts: 1},
		{name: "gflow_image/mock", tool: "gflow_image", request: req(map[string]any{"prompt": "a logo", "mock": true, "output_path": out("gflow-mock.png")}), artifacts: 1},
		{name: "gflow_image/cli", tool: "gflow_image", setup: func(t *testing.T) { fakeGFlow(t, "success") }, request: req(map[string]any{"prompt": "a logo", "count": 2, "output_path": out("gflow.png")}), artifacts: 2},
		{name: "color_grade", tool: "color_grade", request: req(map[string]any{"input_path": f.video, "output_path": out("graded.mp4"), "profile": "neutral"}), artifacts: 1},
		{name: "image_selector", tool: "image_selector", request: req(map[string]any{"prompt": "a lighthouse", "aspect_ratio": "16:9"}), artifacts: 0},
		{name: "video_selector", tool: "video_selector", request: req(map[string]any{"prompt": "waves", "duration": 5}), artifacts: 0},
		{name: "script_check", tool: "script_check", request: req(map[string]any{"script": map[string]any{"total_duration_seconds": 10, "sections": []any{
			map[string]any{"id": "hook", "text": "Why is the sky blue? Sunlight scatters off the air.", "start_seconds": 0, "end_seconds": 4},
			map[string]any{"id": "body", "text": "Blue light scatters most, so it reaches your eye from every direction.", "start_seconds": 4, "end_seconds": 10},
		}}, "structure": map[string]any{"beats": []any{map[string]any{"id": "hook", "share": 0.3}, map[string]any{"id": "body", "share": 0.7}}}}), artifacts: 0},
		{name: "plan_check", tool: "plan_check", request: req(map[string]any{"scene_plan": map[string]any{"scenes": []any{
			map[string]any{"id": "s1", "type": "hero_title", "start_seconds": 0, "end_seconds": 3},
			map[string]any{"id": "s2", "type": "bar_chart", "start_seconds": 3, "end_seconds": 8},
		}}, "delivery_promise": "data_explainer", "duration_seconds": 8}), artifacts: 0},
	}

	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.tool] = true
	}
	for _, tool := range Names() {
		if !covered[tool] {
			t.Errorf("%s has no offline conformance case; its result schema is unguarded", tool)
		}
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.setup != nil {
				c.setup(t)
			}
			data, err := json.Marshal(c.request(t))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			env := RunContext(ctx, c.tool, data)
			if !env.OK {
				encoded, _ := json.MarshalIndent(env.Error, "", "  ")
				t.Fatalf("%s failed: %s", c.tool, encoded)
			}
			assertResultConforms(t, c.tool, env.Result)
			assertArtifacts(t, c.tool, env, c.artifacts)
		})
	}
}

// assertResultConforms validates a result, in its JSON form, against the
// tool's published result schema.
func assertResultConforms(t *testing.T, tool string, result any) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	if reason := schemaMismatch(resultSchemas[tool], value, "$"); reason != "" {
		t.Fatalf("%s result violates its published schema: %s\n%s", tool, reason, encoded)
	}
}

// assertArtifacts checks every descriptor against the bytes on disk.
func assertArtifacts(t *testing.T, tool string, env Envelope, want int) {
	t.Helper()
	if want >= 0 && len(env.Artifacts) != want {
		listed := make([]string, len(env.Artifacts))
		for i, a := range env.Artifacts {
			listed[i] = a.Path
		}
		sort.Strings(listed)
		t.Fatalf("%s described %d artifacts, want %d: %v", tool, len(env.Artifacts), want, listed)
	}
	for _, a := range env.Artifacts {
		if a.Tool != tool || a.FacetVersion != ProductVersion() || a.MediaType == "" || !filepath.IsAbs(a.Path) {
			t.Fatalf("incomplete artifact descriptor: %+v", a)
		}
		data, err := os.ReadFile(a.Path)
		if err != nil {
			t.Fatalf("artifact %s is not readable: %v", a.Path, err)
		}
		sum := sha256.Sum256(data)
		if a.SHA256 != hex.EncodeToString(sum[:]) || a.SizeBytes != int64(len(data)) {
			t.Fatalf("artifact %s does not describe its bytes: %+v (size %d)", a.Path, a, len(data))
		}
	}
}

// fakeHyperFrames points FACET_HYPERFRAMES at a stand-in CLI run by the real
// node, so the tool's own process handling is exercised end to end.
func fakeHyperFrames(t *testing.T, source string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is needed to run the stand-in HyperFrames CLI")
	}
	entry := filepath.Join(t.TempDir(), "node_modules", "hyperframes", "bin", "hyperframes.mjs")
	script := `import fs from 'node:fs';
import path from 'node:path';
const args = process.argv.slice(2);
if (args[0] === 'render') {
  const out = args[args.indexOf('--output') + 1];
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.copyFileSync(process.env.FACET_TEST_HF_SOURCE, out);
  process.stdout.write('rendered\n');
} else if (args[0] === 'add') {
  process.stdout.write(JSON.stringify({ added: args[1] }));
} else {
  process.stderr.write('a warning on stderr must not corrupt the JSON report\n');
  process.stdout.write(JSON.stringify({ command: args[0], findings: [] }));
}
`
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hyperframesEnv, entry)
	t.Setenv("FACET_TEST_HF_SOURCE", source)
}

// fakeEdgeSynthesis replaces the network synthesis with local audio.
func fakeEdgeSynthesis(t *testing.T) {
	t.Helper()
	previous := edgeSynthesize
	edgeSynthesize = func(context.Context, string, string, string, string, time.Duration) ([]byte, []edgetts.SpeechMetadata, error) {
		return testWAV(0.5), nil, nil
	}
	t.Cleanup(func() { edgeSynthesize = previous })
}

// fakePiper installs the test binary as the runtime's private piper.
func fakePiper(t *testing.T) {
	t.Helper()
	root := fakeRuntime(t)
	piper := privatePiperPath(root)
	if err := os.MkdirAll(filepath.Dir(piper), 0o755); err != nil {
		t.Fatal(err)
	}
	installTestBinary(t, filepath.Dir(piper), "piper")
	t.Setenv(fakePiperEnv, "1")
}
