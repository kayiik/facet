package toolbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kayiik/facet/internal/proctree"
)

var expectedToolNames = []string{
	"audio_mix",
	"audio_probe",
	"color_grade",
	"direct_clip_search",
	"edge_tts",
	"elevenlabs_tts",
	"ffmpeg_caption_burn",
	"flux_image",
	"frame_sample",
	"gflow_image",
	"gflow_video",
	"hyperframes_compose",
	"image_selector",
	"kling_video",
	"media_probe",
	"music_library",
	"openai_image",
	"openai_tts",
	"output_review",
	"pexels_video",
	"piper_tts",
	"pixabay_video",
	"plan_check",
	"scene_detect",
	"script_check",
	"silence_cutter",
	"sora_video",
	"source_edit",
	"subtitle_gen",
	"video_compose",
	"video_selector",
	"video_stitch",
	"video_trimmer",
	"visual_qa",
	"wikimedia",
}

func TestNames(t *testing.T) {
	if got := Names(); !reflect.DeepEqual(got, expectedToolNames) {
		t.Fatalf("Names() = %v, want %v", got, expectedToolNames)
	}
}

func TestCLIListIsExact(t *testing.T) {
	envelope, ok := CLI([]string{"tools", "list"})
	if !ok || !envelope.OK {
		t.Fatalf("tools list failed: %#v", envelope)
	}
	items := envelope.Result.(map[string]any)["tools"].([]any)
	got := make([]string, len(items))
	for i, item := range items {
		m := item.(map[string]any)
		got[i] = m["name"].(string)
		if m["capability"] == "" {
			t.Fatalf("missing capability for tool %s", got[i])
		}
	}
	if !reflect.DeepEqual(got, expectedToolNames) {
		t.Fatalf("tools list = %v, want %v", got, expectedToolNames)
	}
}

func TestDescribeReturnsStructuredSchemas(t *testing.T) {
	for _, name := range Names() {
		envelope, ok := CLI([]string{"tools", "describe", name})
		if !ok {
			t.Fatalf("describe %s failed", name)
		}
		description := envelope.Result.(map[string]any)
		request := description["request_schema"].(map[string]any)
		result := description["result_schema"].(map[string]any)
		if request["type"] != "object" || request["additionalProperties"] != false || len(result) == 0 {
			t.Fatalf("%s schemas are not structured and complete: %#v", name, description)
		}
	}
}

func TestStrictValidation(t *testing.T) {
	_, _, err := doMediaProbe("estimate", []byte(`{"input":"missing","surprise":true}`))
	if err == nil || errorEnvelope("media_probe", "estimate", err).Error.Code != "invalid_request" {
		t.Fatalf("expected invalid_request, got %v", err)
	}
}

func TestEstimateDoesNotCreateOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.mp4")
	_ = os.WriteFile(input, []byte("not media"), 0600)
	request := map[string]any{"input": input, "output_dir": filepath.Join(dir, "new"), "strategy": map[string]any{"type": "timestamps", "timestamps": []float64{0}}}
	data, _ := json.Marshal(request)
	_, _, _ = doFrameSample("estimate", data)
	if _, err := os.Stat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
		t.Fatal("estimate created output directory")
	}
}

func TestFrameEstimateValidatesStrategyWithoutDecoding(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(input, []byte("not media"), 0600); err != nil {
		t.Fatal(err)
	}
	valid, _ := json.Marshal(map[string]any{"input": input, "output_dir": filepath.Join(dir, "frames"), "strategy": map[string]any{"type": "uniform", "count": 2}})
	if result, _, err := doFrameSample("estimate", valid); err != nil || result.(map[string]any)["side_effect_free"] != true {
		t.Fatalf("side-effect-free estimate failed: %#v, %v", result, err)
	}
	invalid, _ := json.Marshal(map[string]any{"input": input, "output_dir": filepath.Join(dir, "frames"), "strategy": map[string]any{"type": "uniform", "count": 2, "threshold": .3}})
	if _, _, err := doFrameSample("estimate", invalid); err == nil {
		t.Fatal("estimate accepted cross-strategy fields")
	}
	if _, err := os.Stat(filepath.Join(dir, "frames")); !os.IsNotExist(err) {
		t.Fatal("estimate created output directory")
	}
}

func TestFinalizeOutputRollbackPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "final.mp4")
	if err := os.WriteFile(output, []byte("old-valid-output"), 0600); err != nil {
		t.Fatal(err)
	}
	err := finalizeOutput(filepath.Join(dir, "missing-temp.mp4"), output, true)
	if err == nil {
		t.Fatal("expected finalization failure")
	}
	got, readErr := os.ReadFile(output)
	if readErr != nil || string(got) != "old-valid-output" {
		t.Fatalf("existing output was not restored: %q, %v", got, readErr)
	}
}

func TestPublishFileSetRollsBack(t *testing.T) {
	dir := t.TempDir()
	oldA, oldB := filepath.Join(dir, "a.jpg"), filepath.Join(dir, "b.jpg")
	for _, path := range []string{oldA, oldB} {
		if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stageA := filepath.Join(dir, "new-a.jpg")
	if err := os.WriteFile(stageA, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	err := publishFileSet([]string{stageA, filepath.Join(dir, "missing.jpg")}, []string{oldA, oldB}, true)
	if err == nil {
		t.Fatal("expected set publication failure")
	}
	for _, path := range []string{oldA, oldB} {
		got, readErr := os.ReadFile(path)
		if readErr != nil || string(got) != "old" {
			t.Fatalf("frame %s was not rolled back: %q, %v", path, got, readErr)
		}
	}
}

func TestAudioMixRejectsNoOpAndInvalidDuckingDuringEstimate(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "video.mp4")
	music := filepath.Join(dir, "music.wav")
	for _, path := range []string{video, music} {
		if err := os.WriteFile(path, []byte("shape-only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := func(source, musicValue any) []byte {
		m := map[string]any{"video": video, "duration": "video", "output": filepath.Join(dir, "out.mp4")}
		if source != nil {
			m["source"] = source
		}
		if musicValue != nil {
			m["music"] = musicValue
		}
		data, _ := json.Marshal(m)
		return data
	}
	if _, _, err := doAudioMix("estimate", request(map[string]any{}, nil)); err == nil {
		t.Fatal("accepted no-op source operation")
	}
	badMusic := map[string]any{"input": music, "ducking": map[string]any{"enabled": true, "threshold": 0, "ratio": 1}}
	if _, _, err := doAudioMix("estimate", request(nil, badMusic)); err == nil {
		t.Fatal("accepted invalid ducking")
	}
}

func TestSourceEditEstimateValidatesFramingAndCutOnly(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(input, []byte("shape-only"), 0600); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"segments": []map[string]any{{"input": input, "start": 0, "end": 1, "transition": "cut", "focal_point": map[string]any{"x": .25, "y": .75}}}, "target": map[string]any{"width": 240, "height": 320, "fps": 30, "fit": "cover", "video_codec": "h264", "pixel_format": "yuv420p", "audio_codec": "aac", "audio_sample_rate": 48000, "audio_channels": 2}, "output": filepath.Join(dir, "out.mp4")}
	data, _ := json.Marshal(base)
	if _, _, err := doSourceEdit("estimate", data); err != nil {
		t.Fatal(err)
	}
	base["segments"].([]map[string]any)[0]["transition"] = "dissolve"
	data, _ = json.Marshal(base)
	if _, _, err := doSourceEdit("estimate", data); err == nil {
		t.Fatal("accepted speculative transition")
	}
}

func TestGateOmitsFailureWordingOnPass(t *testing.T) {
	g := gate("profile", true, "dimensions differ")
	if _, exists := g["message"]; exists {
		t.Fatalf("pass gate retained mismatch wording: %#v", g)
	}
}

func TestProbeContextHonorsCancelledHash(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "source.mp4")
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=size=16x16:duration=0.1", "-c:v", "libx264", input)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := probeContext(ctx, input)
	if err == nil || errorEnvelope("media_probe", "run", err).Error.Code != "command_timeout" {
		t.Fatalf("cancelled probe = %v", err)
	}
}

func TestCLIErrorMarshalsAsJSON(t *testing.T) {
	envelope, ok := CLI([]string{"tools", "describe", "unknown"})
	if ok {
		t.Fatal("unknown tool returned success exit state")
	}
	data, err := json.Marshal(envelope)
	if err != nil || !strings.Contains(string(data), `"ok":false`) || !strings.Contains(string(data), `"code":"invalid_request"`) {
		t.Fatalf("unexpected CLI error JSON: %s, %v", data, err)
	}
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(n); err != nil {
			t.Skip(n + " not installed")
		}
	}
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	if out, err := fixtureCommand(t, "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %v\n%s", err, out)
	}
}

// fixtureCommand prepares a program that builds a test fixture. It has a hard
// timeout, and its whole process tree is killed if it overruns or the test
// ends first.
func fixtureCommand(t *testing.T, name string, args ...string) *proctree.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return proctree.CommandContext(ctx, name, args...)
}

func TestSubtitleGen(t *testing.T) {
	dir := t.TempDir()
	outSRT := filepath.Join(dir, "subs.srt")
	req := map[string]any{
		"segments": []map[string]any{
			{
				"text":  "Hello world this is a test",
				"start": 0.0,
				"end":   2.5,
				"words": []map[string]any{
					{"word": "Hello", "start": 0.0, "end": 0.5},
					{"word": "cloud", "start": 0.5, "end": 1.0},
					{"word": "this", "start": 1.0, "end": 1.5},
					{"word": "is", "start": 1.5, "end": 2.0},
					{"word": "test", "start": 2.0, "end": 2.5},
				},
			},
		},
		"format":      "srt",
		"output_path": outSRT,
		"corrections": map[string]string{
			"cloud": "Claude",
		},
	}
	data, _ := json.Marshal(req)
	res, _, err := doSubtitleGen("run", data)
	if err != nil {
		t.Fatalf("subtitle_gen failed: %v", err)
	}
	m := res.(map[string]any)
	if m["cue_count"].(int) == 0 {
		t.Fatal("expected cues to be generated")
	}
	content, err := os.ReadFile(outSRT)
	if err != nil || !strings.Contains(string(content), "Claude") {
		t.Fatalf("expected correction in subtitle output: %s", string(content))
	}
}

func TestMusicLibrary(t *testing.T) {
	dir := t.TempDir()
	trackFile := filepath.Join(dir, "song.mp3")
	_ = os.WriteFile(trackFile, []byte("fake mp3 data"), 0644)

	req := map[string]any{
		"library_dir": dir,
	}
	data, _ := json.Marshal(req)
	res, _, err := doMusicLibrary("run", data)
	if err != nil {
		t.Fatalf("music_library failed: %v", err)
	}
	m := res.(map[string]any)
	if m["track_count"].(int) != 1 {
		t.Fatalf("expected 1 track, got %v", m["track_count"])
	}
}

func TestSceneDetect(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "scenes.mp4")
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:size=160x120:rate=24:duration=1.5", "-f", "lavfi", "-i", "color=c=blue:size=160x120:rate=24:duration=1.5", "-filter_complex", "[0:v][1:v]concat=n=2:v=1[v]", "-map", "[v]", "-c:v", "libx264", "-pix_fmt", "yuv420p", video)

	sceneJSON := filepath.Join(dir, "scenes.json")
	req := map[string]any{
		"input_path":  video,
		"output_path": sceneJSON,
		"threshold":   0.2,
	}
	data, _ := json.Marshal(req)
	res, _, err := doSceneDetect("run", data)
	if err != nil {
		t.Fatalf("scene_detect failed: %v", err)
	}
	m := res.(map[string]any)
	if m["scene_count"].(int) == 0 {
		t.Fatalf("expected detected scenes, got %v", m)
	}
}

func TestAudioProbeAndVisualQA(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	media := filepath.Join(dir, "probe_test.mp4")
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=30:duration=1.5", "-f", "lavfi", "-i", "sine=frequency=1000:duration=1.5", "-c:v", "libx264", "-c:a", "aac", "-shortest", media)

	// Audio probe
	apReq, _ := json.Marshal(map[string]any{"input_path": media})
	apRes, _, err := doAudioProbe("run", apReq)
	if err != nil {
		t.Fatalf("audio_probe failed: %v", err)
	}
	apMap := apRes.(map[string]any)
	if apMap["duration_seconds"].(float64) <= 0 || apMap["audio"] == nil {
		t.Fatalf("unexpected audio probe result: %#v", apMap)
	}

	// Visual QA probe
	qaReq, _ := json.Marshal(map[string]any{
		"operation":  "probe",
		"input_path": media,
		"expected": map[string]any{
			"width":     320,
			"height":    240,
			"has_audio": true,
		},
	})
	qaRes, _, err := doVisualQA("run", qaReq)
	if err != nil {
		t.Fatalf("visual_qa probe failed: %v", err)
	}
	qaMap := qaRes.(map[string]any)
	if qaMap["validation_passed"] != true {
		t.Fatalf("visual_qa validation expected pass: %#v", qaMap)
	}

	// Visual QA review
	reviewDir := filepath.Join(dir, "qa_review")
	qarReq, _ := json.Marshal(map[string]any{
		"operation":  "review",
		"input_path": media,
		"output_dir": reviewDir,
		"timestamps": []float64{0.5, 1.0},
	})
	qarRes, _, err := doVisualQA("run", qarReq)
	if err != nil {
		t.Fatalf("visual_qa review failed: %v", err)
	}
	qarMap := qarRes.(map[string]any)
	if qarMap["frame_count"].(int) != 2 {
		t.Fatalf("expected 2 frames extracted: %#v", qarMap)
	}
}

func TestVideoTrimmerAndStitch(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	clipA := filepath.Join(dir, "clipA.mp4")
	clipB := filepath.Join(dir, "clipB.mp4")
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=green:size=320x240:rate=30:duration=2", "-f", "lavfi", "-i", "sine=frequency=300:duration=2", "-c:v", "libx264", "-c:a", "aac", "-shortest", clipA)
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=yellow:size=320x240:rate=30:duration=2", "-f", "lavfi", "-i", "sine=frequency=600:duration=2", "-c:v", "libx264", "-c:a", "aac", "-shortest", clipB)

	// Trimmer cut
	trimOut := filepath.Join(dir, "trimmed.mp4")
	trimReq, _ := json.Marshal(map[string]any{
		"operation":     "cut",
		"input_path":    clipA,
		"output_path":   trimOut,
		"start_seconds": 0.5,
		"end_seconds":   1.5,
		"codec":         "libx264",
	})
	if _, _, err := doVideoTrimmer("run", trimReq); err != nil {
		t.Fatalf("video_trimmer cut failed: %v", err)
	}

	// Stitch validate
	stitchValReq, _ := json.Marshal(map[string]any{
		"operation": "validate",
		"clips":     []string{clipA, clipB},
	})
	valRes, _, err := doVideoStitch("run", stitchValReq)
	if err != nil {
		t.Fatalf("video_stitch validate failed: %v", err)
	}
	if valRes.(map[string]any)["compatible"] != true {
		t.Fatalf("expected clips to be compatible: %#v", valRes)
	}

	// Stitch spatial (side by side)
	spatialOut := filepath.Join(dir, "spatial.mp4")
	spatialReq, _ := json.Marshal(map[string]any{
		"operation":   "spatial",
		"layout":      "side_by_side",
		"clips":       []string{clipA, clipB},
		"output_path": spatialOut,
	})
	if _, _, err := doVideoStitch("run", spatialReq); err != nil {
		t.Fatalf("video_stitch spatial failed: %v", err)
	}
}

func TestSilenceCutter(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	// Audio with 1s tone, 1.5s silence, 1s tone
	media := filepath.Join(dir, "silence_test.mp4")
	ffmpeg(t, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:size=160x120:rate=24:duration=3.5",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1.0",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo:d=1.5",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1.0",
		"-filter_complex", "[1:a][2:a][3:a]concat=n=3:v=0:a=1[a]",
		"-map", "0:v:0", "-map", "[a]",
		"-c:v", "libx264", "-c:a", "aac",
		"-shortest", media)

	outCut := filepath.Join(dir, "silence_cut.mp4")
	cutReq, _ := json.Marshal(map[string]any{
		"input_path":           media,
		"output_path":          outCut,
		"mode":                 "remove",
		"silence_threshold_db": -30,
		"min_silence_duration": 0.5,
	})
	res, _, err := doSilenceCutter("run", cutReq)
	if err != nil {
		t.Fatalf("silence_cutter remove failed: %v", err)
	}
	m := res.(map[string]any)
	if m["silence_segments"].(int) == 0 {
		t.Fatalf("expected detected silence segment: %#v", m)
	}
}

func TestHyperFramesDoctorAndScaffold(t *testing.T) {
	docRes, _, err := doHyperFramesCompose("doctor", []byte(`{"operation":"doctor"}`))
	if err != nil {
		t.Fatalf("hyperframes doctor failed: %v", err)
	}
	if docRes.(map[string]any)["operation"] != "doctor" {
		t.Fatalf("unexpected doctor result: %#v", docRes)
	}

	dir := t.TempDir()
	scaffReq, _ := json.Marshal(map[string]any{
		"operation":      "scaffold_workspace",
		"workspace_path": dir,
		"edit_decisions": map[string]any{
			"cuts": []map[string]any{
				{"type": "text_card", "text": "Welcome", "in_seconds": 0, "out_seconds": 5},
			},
		},
	})
	scaffRes, _, err := doHyperFramesCompose("run", scaffReq)
	if err != nil {
		t.Fatalf("hyperframes scaffold failed: %v", err)
	}
	if scaffRes.(map[string]any)["cut_count"].(int) != 1 {
		t.Fatalf("expected 1 cut scaffolded: %#v", scaffRes)
	}
	if !fileExists(filepath.Join(dir, "index.html")) {
		t.Fatal("index.html was not generated")
	}
}

func TestRealSourceEditAndReview(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	withAudio := filepath.Join(dir, "a.mp4")
	silent := filepath.Join(dir, "b.mp4")
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", withAudio)
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:size=180x320:rate=30:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", silent)
	output := filepath.Join(dir, "edit.mp4")
	req := map[string]any{"segments": []map[string]any{{"input": withAudio, "start": 0.2, "end": 1.2, "position": "top"}, {"input": silent, "start": 0.1, "end": 1.1, "focal_point": map[string]any{"x": .5, "y": .25}}}, "target": map[string]any{"width": 240, "height": 320, "fps": 30, "fit": "cover", "video_codec": "h264", "pixel_format": "yuv420p", "audio_codec": "aac", "audio_sample_rate": 48000, "audio_channels": 2}, "output": output, "timeout_seconds": 60}
	data, _ := json.Marshal(req)
	result, _, err := doSourceEdit("run", data)
	if err != nil {
		t.Fatalf("%s: %#v", err, errorEnvelope("source_edit", "run", err).Error)
	}
	if result.(map[string]any)["silent_inputs_filled"] != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.(map[string]any)["requested_operations"].([]string)) == 0 || len(result.(map[string]any)["realized_operations"].([]string)) == 0 {
		t.Fatalf("operation reporting missing: %#v", result)
	}
	p, _, err := probe(output, 30_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	v := firstVideo(p)
	if v["width"] != 240 || v["height"] != 320 || !hasAudio(p) {
		t.Fatalf("bad normalized output: %#v", p)
	}
	mixed := filepath.Join(dir, "mixed.mp4")
	music := filepath.Join(dir, "music.wav")
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=48000:duration=2", music)
	mix := map[string]any{"video": output, "source": map[string]any{"gain_db": -1.0, "fade_in": 0.1, "fade_out": 0.1}, "music": map[string]any{"input": music, "gain_db": -12.0, "fade_in": 0.1, "fade_out": 0.1, "ducking": map[string]any{"enabled": true, "threshold": 0.05, "ratio": 8.0}}, "loudness": map[string]any{"enabled": true, "integrated_lufs": -14.0, "true_peak_db": -1.0}, "duration": "video", "output": mixed, "timeout_seconds": 60}
	md, _ := json.Marshal(mix)
	if _, _, err = doAudioMix("run", md); err != nil {
		t.Fatal(err)
	}
	review := map[string]any{"input": output, "profile": map[string]any{"width": 240, "height": 320, "fps": 30}, "checks": map[string]any{"duration": map[string]any{"expected": 2.0, "tolerance": 0.2}, "video_codec": "h264", "pixel_format": "yuv420p", "audio": map[string]any{"required": true, "codec": "aac", "sample_rate": 48000, "channels": 2}}, "samples": map[string]any{"type": "uniform", "count": 4}, "evidence_dir": filepath.Join(dir, "review"), "timeout_seconds": 60}
	review["input"] = mixed
	rd, _ := json.Marshal(review)
	rr, _, err := doOutputReview("run", rd)
	if err != nil {
		t.Fatal(err)
	}
	if rr.(map[string]any)["execution_status"] != "succeeded" {
		t.Fatalf("review: %#v", rr)
	}
}

func TestAllToolEstimates(t *testing.T) {
	dir := t.TempDir()
	dummyFile := filepath.Join(dir, "dummy.mp4")
	_ = os.WriteFile(dummyFile, []byte("fake video data"), 0644)
	outPath := filepath.Join(dir, "out.mp4")

	sampleInputs := map[string]any{
		"audio_mix":           map[string]any{"video": dummyFile, "source": map[string]any{"gain_db": -1.0}, "duration": "video", "output": outPath},
		"audio_probe":         map[string]any{"input_path": dummyFile},
		"color_grade":         map[string]any{"input_path": dummyFile, "output_path": outPath, "profile": "cinematic_warm"},
		"direct_clip_search":  map[string]any{"output_dir": dir, "queries": []map[string]any{{"query": "nature"}}},
		"edge_tts":            map[string]any{"text": "Hello world from Microsoft Edge neural voice"},
		"elevenlabs_tts":      map[string]any{"text": "Hello world from ElevenLabs"},
		"flux_image":          map[string]any{"prompt": "sunset over mountains", "aspect_ratio": "16:9"},
		"frame_sample":        map[string]any{"input": dummyFile, "output_dir": filepath.Join(dir, "frames"), "strategy": map[string]any{"type": "uniform", "count": 2}},
		"gflow_image":         map[string]any{"prompt": "origami eagle logo", "aspect_ratio": "square"},
		"gflow_video":         map[string]any{"prompt": "futuristic dragon soaring through clouds", "duration": 6.0, "aspect_ratio": "landscape"},
		"hyperframes_compose": map[string]any{"operation": "doctor"},
		"image_selector":      map[string]any{"prompt": "modern data center server room", "aspect_ratio": "16:9", "style": "photorealistic"},
		"kling_video":         map[string]any{"prompt": "ocean waves crashing on rocks", "duration": 5.0, "aspect_ratio": "16:9"},
		"media_probe":         map[string]any{"input": dummyFile},
		"music_library":       map[string]any{"library_dir": dir},
		"openai_image":        map[string]any{"prompt": "futuristic smart city with flying cars", "aspect_ratio": "16:9"},
		"openai_tts":          map[string]any{"text": "Hello world from OpenAI"},
		"output_review":       map[string]any{"input": dummyFile, "profile": map[string]any{"width": 1920, "height": 1080, "fps": 30}, "checks": map[string]any{"duration": map[string]any{"expected": 10, "tolerance": 0.5}, "video_codec": "h264", "pixel_format": "yuv420p", "audio": map[string]any{"required": true, "codec": "aac", "sample_rate": 48000, "channels": 2}}, "samples": map[string]any{"type": "uniform", "count": 4}, "evidence_dir": dir},
		"pexels_video":        map[string]any{"query": "mountains"},
		"piper_tts":           map[string]any{"text": "Hello world from Piper"},
		"pixabay_video":       map[string]any{"query": "ocean"},
		"plan_check":          map[string]any{"scene_plan": map[string]any{"scenes": []map[string]any{{"id": "s1", "type": "text_card", "start_seconds": 0, "end_seconds": 2}}}},
		"script_check":        map[string]any{"script": map[string]any{"total_duration_seconds": 10, "sections": []map[string]any{{"id": "hook", "text": "Hello world.", "start_seconds": 0, "end_seconds": 2}}}},
		"ffmpeg_caption_burn": map[string]any{"input_path": dummyFile, "output_path": outPath, "segments": []map[string]any{{"start": 0, "end": 1, "text": "hello"}}},
		"scene_detect":        map[string]any{"input_path": dummyFile},
		"silence_cutter":      map[string]any{"input_path": dummyFile},
		"sora_video":          map[string]any{"prompt": "close up shot of a cup of coffee", "duration": 5.0, "aspect_ratio": "16:9"},
		"source_edit":         map[string]any{"segments": []map[string]any{{"input": dummyFile, "start": 0, "end": 1}}, "target": map[string]any{"width": 1920, "height": 1080, "fps": 30, "fit": "contain", "video_codec": "h264", "pixel_format": "yuv420p", "audio_codec": "aac", "audio_sample_rate": 48000, "audio_channels": 2}, "output": outPath},
		"subtitle_gen":        map[string]any{"segments": []map[string]any{{"start": 0, "end": 1, "text": "hello"}}},
		"video_compose":       map[string]any{"operation": "compose", "edit_decisions": map[string]any{"cuts": []map[string]any{{"source": dummyFile, "in_seconds": 0, "out_seconds": 1}}}},
		"video_selector":      map[string]any{"prompt": "drone flight over pine forest", "duration": 5.0, "aspect_ratio": "16:9", "intent": "cinematic"},
		"video_stitch":        map[string]any{"operation": "stitch", "clips": []string{dummyFile, dummyFile}, "output_path": outPath},
		"video_trimmer":       map[string]any{"operation": "cut", "input_path": dummyFile, "output_path": outPath, "start_seconds": 0, "end_seconds": 1},
		"visual_qa":           map[string]any{"operation": "probe", "input_path": dummyFile},
		"wikimedia":           map[string]any{"query": "planet"},
	}

	for _, tool := range Names() {
		req, ok := sampleInputs[tool]
		if !ok {
			t.Fatalf("no sample input for tool: %s", tool)
		}
		reqFile := filepath.Join(dir, tool+"_req.json")
		b, _ := json.Marshal(req)
		_ = os.WriteFile(reqFile, b, 0644)

		envelope, success := CLI([]string{"tools", "estimate", tool, "--input", reqFile})
		if !success || !envelope.OK {
			t.Fatalf("estimate failed for %s: %#v", tool, envelope)
		}
		if envelope.Tool != tool || envelope.Operation != "estimate" {
			t.Fatalf("unexpected estimate envelope for %s: %#v", tool, envelope)
		}
	}
}

func TestNoAliasesAndInlineJSON(t *testing.T) {
	// 1. A tool has one name: shorthand spellings are unknown tools.
	for _, alias := range []string{"edgetts", "edit", "probe", "review", "compose", "veo", "elevenlabs"} {
		if env, ok := CLI([]string{"tools", "describe", alias}); ok || env.OK {
			t.Errorf("%s is described, but tools have no aliases: %#v", alias, env)
		}
	}

	// The retired compatibility tools are gone: no legacy request shape is
	// accepted under their names any more.
	dir := t.TempDir()
	input := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(input, []byte("not decoded during estimate"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyFrameRequest := `{"input_path":` + strconv.Quote(input) + `,"strategy":"count","count":2,"output_dir":` + strconv.Quote(filepath.Join(dir, "frames")) + `}`
	for _, retired := range []string{"frame_sampler", "audio_mixer"} {
		if env, ok := CLI([]string{"tools", "estimate", retired, "--input", legacyFrameRequest}); ok || env.OK {
			t.Fatalf("retired alias %s is still invokable: %#v", retired, env)
		}
		if env, ok := CLI([]string{"tools", "describe", retired}); ok || env.OK {
			t.Fatalf("retired alias %s is still described: %#v", retired, env)
		}
		if env := RunContext(context.Background(), retired, []byte(legacyFrameRequest)); env.OK || env.Error.Code != "unknown_tool" {
			t.Fatalf("retired alias %s still runs: %#v", retired, env)
		}
	}
	// frame_sample no longer accepts the legacy fields it used to ignore.
	if env, ok := CLI([]string{"tools", "estimate", "frame_sample", "--input", `{"input":` + strconv.Quote(input) + `,"output_dir":"frames","strategy":{"type":"uniform","count":2},"quality":2}`}); ok || env.Error.Code != "invalid_request" {
		t.Fatalf("frame_sample accepted a legacy field: %#v", env)
	}

	// 2. Test inline JSON execution for estimate
	inlineJSON := `{"text": "Hello world from inline json", "output": "narration/test.mp3"}`
	env, ok := CLI([]string{"tools", "estimate", "edge_tts", "--input", inlineJSON})
	if !ok || !env.OK {
		t.Fatalf("estimate with inline JSON failed: %#v", env)
	}
	if env.Tool != "edge_tts" {
		t.Errorf("expected tool edge_tts, got %s", env.Tool)
	}

	// 3. Test inline JSON with surrounding single quotes
	inlineWithQuotes := `'{"text": "Hello world quoted", "output": "narration/test.mp3"}'`
	env, ok = CLI([]string{"tools", "estimate", "edge_tts", "--input", inlineWithQuotes})
	if !ok || !env.OK {
		t.Fatalf("estimate with quoted inline JSON failed: %#v", env)
	}
}

func TestVideoComposeDirectProps(t *testing.T) {
	// 1. Test direct Explainer props estimate
	explainerProps := `{
		"cuts": [
			{"id": "sc1", "type": "hero_title", "in_seconds": 0, "out_seconds": 4, "text": "The Universe"}
		],
		"output": "renders/final.mp4"
	}`
	env, ok := CLI([]string{"tools", "estimate", "video_compose", "--input", explainerProps})
	if !ok || !env.OK {
		t.Fatalf("estimate with direct explainer props failed: %#v", env)
	}

	// 2. Test direct Scene Plan JSON estimate
	scenePlan := `{
		"scenes": [
			{"id": "sc1", "type": "text_card", "text": "Intro to space", "start_seconds": 0, "end_seconds": 5}
		],
		"output": "renders/final.mp4"
	}`
	env, ok = CLI([]string{"tools", "estimate", "video_compose", "--input", scenePlan})
	if !ok || !env.OK {
		t.Fatalf("estimate with direct scene plan failed: %#v", env)
	}
}
