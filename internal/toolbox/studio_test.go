package toolbox

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kolonist/edgetts"
)

// studioRun runs a tool and returns its result in JSON form.
func studioRun(t *testing.T, tool string, req any) map[string]any {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := RunContext(ctx, tool, data)
	if !env.OK {
		encoded, _ := json.Marshal(env.Error)
		t.Fatalf("%s failed: %s", tool, encoded)
	}
	assertResultConforms(t, tool, env.Result)
	encoded, _ := json.Marshal(env.Result)
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// studioIssues joins a check's findings into one searchable text.
func studioIssues(t *testing.T, result map[string]any) string {
	t.Helper()
	findings, ok := result["findings"].([]any)
	if !ok {
		t.Fatalf("findings missing: %v", result)
	}
	var issues []string
	for _, f := range findings {
		issues = append(issues, f.(map[string]any)["issue"].(string))
	}
	return strings.Join(issues, "\n")
}

func studioWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.011 }

// testToneWAV is a voice clip: a 440 Hz tone for its first tone seconds, then
// silence.
func testToneWAV(seconds, tone float64) []byte {
	wav := testWAV(seconds)
	for i := 44; i+1 < len(wav) && float64((i-44)/2) < tone*22050; i += 2 {
		sample := int16(8000 * math.Sin(2*math.Pi*440*float64((i-44)/2)/22050))
		wav[i], wav[i+1] = byte(sample), byte(uint16(sample)>>8)
	}
	return wav
}

func TestScriptCheckMeasuresTheScript(t *testing.T) {
	long := strings.Repeat("word ", 200) + "end."
	result := studioRun(t, "script_check", map[string]any{
		"script": map[string]any{"total_duration_seconds": 30, "sections": []any{
			map[string]any{"id": "hook", "text": long, "start_seconds": 0, "end_seconds": 5},
			map[string]any{"id": "close", "text": "Short close.", "start_seconds": 5, "end_seconds": 30},
		}},
		"structure": map[string]any{"beats": []any{map[string]any{"id": "hook", "share": 0.2}, map[string]any{"id": "close", "share": 0.8}}},
	})
	if result["fit"] != "too_long" || result["words"] != 203.0 {
		t.Errorf("fit = %v, words = %v; want too_long, 203", result["fit"], result["words"])
	}
	if hook := result["hook"].(map[string]any); hook["within"] != false {
		t.Errorf("a 201-word first sentence was taken as a hook: %v", hook)
	}
	issues := studioIssues(t, result)
	for _, want := range []string{"the video has 30.0 s", "the hook should land within 5.0 s", "two caption lines in sections hook", "its beat plans 20%"} {
		if !strings.Contains(issues, want) {
			t.Errorf("findings lack %q:\n%s", want, issues)
		}
	}

	fits := studioRun(t, "script_check", map[string]any{"duration_seconds": 3, "script": map[string]any{"sections": []any{
		map[string]any{"id": "a", "text": "Why is the sky blue? Light scatters.", "start_seconds": 0, "end_seconds": 3},
	}}})
	if fits["fit"] != "ok" || len(fits["findings"].([]any)) != 0 {
		t.Errorf("a fitting script was flagged: %v", fits)
	}
}

func TestPlanCheckFindsGapsOverlapsAndSlideshows(t *testing.T) {
	timing := filepath.Join(t.TempDir(), "narration.timing.json")
	studioWriteJSON(t, timing, map[string]any{"duration_seconds": 14, "lines": []any{map[string]any{"id": "l1", "start": 0, "end": 14}}})
	result := studioRun(t, "plan_check", map[string]any{
		"scene_plan": map[string]any{"scenes": []any{
			map[string]any{"id": "s1", "type": "text_card", "start_seconds": 0, "end_seconds": 3},
			map[string]any{"id": "s2", "type": "text_card", "start_seconds": 4, "end_seconds": 7},
			map[string]any{"id": "s3", "type": "text_card", "start_seconds": 6.5, "end_seconds": 10},
		}},
		"delivery_promise": "motion_led", "duration_seconds": 12, "timing_path": timing,
	})
	if result["slideshow_risk"] != "high" || result["motion_share"] != 0.0 {
		t.Errorf("risk = %v, motion = %v; want high, 0", result["slideshow_risk"], result["motion_share"])
	}
	issues := studioIssues(t, result)
	for _, want := range []string{"the timeline has gaps: s1→s2", "scenes overlap: s2/s3", "three text_card scenes in a row", "a motion-led video", "the video should last 12.0 s", "the narration runs to 14.0 s"} {
		if !strings.Contains(issues, want) {
			t.Errorf("findings lack %q:\n%s", want, issues)
		}
	}

	clean := studioRun(t, "plan_check", map[string]any{"delivery_promise": "data_explainer", "scene_plan": map[string]any{"scenes": []any{
		map[string]any{"id": "s1", "type": "hero_title", "start_seconds": 0, "end_seconds": 3},
		map[string]any{"id": "s2", "type": "bar_chart", "start_seconds": 3, "end_seconds": 8},
	}}})
	if clean["slideshow_risk"] != "medium" || len(clean["findings"].([]any)) != 0 || clean["unavailable"] == nil {
		t.Errorf("a sound plan was flagged: %v", clean)
	}
}

func TestNarrationLinesWriteATimingRecord(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	previous := edgeSynthesize
	calls := 0
	edgeSynthesize = func(_ context.Context, text, _, _, _ string, _ time.Duration) ([]byte, []edgetts.SpeechMetadata, error) {
		words := strings.Fields(text)
		meta := make([]edgetts.SpeechMetadata, len(words))
		for i, w := range words {
			// Edge reports words without their punctuation.
			meta[i] = edgetts.SpeechMetadata{Offset: i * 100, Duration: 90, Text: strings.Trim(w, ".?!,")}
		}
		calls++
		if calls == 1 {
			// A second of the voice's own trailing silence, trimmed to 0.12 s.
			return testToneWAV(1.5, 0.5), meta, nil
		}
		return testToneWAV(0.5, 0.5), meta, nil
	}
	t.Cleanup(func() { edgeSynthesize = previous })
	dir := t.TempDir()
	result := studioRun(t, "edge_tts", map[string]any{"output_path": filepath.Join(dir, "narration.mp3"), "lines": []any{
		map[string]any{"id": "hook", "text": "Why is the sky blue?"},
		map[string]any{"id": "answer", "text": "Sunlight scatters.", "pause_after_seconds": 1},
	}})
	if result["line_count"] != 2.0 || result["timing_path"] != filepath.Join(dir, "narration.timing.json") {
		t.Fatalf("result = %v", result)
	}
	var record narrationTiming
	data, err := os.ReadFile(result["timing_path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if len(record.Lines) != 2 || len(record.Words) != 7 {
		t.Fatalf("record has %d lines and %d words, want 2 and 7", len(record.Lines), len(record.Words))
	}
	// The first line keeps 0.12 s of its trailing silence, then the default
	// pause; the last line's own pause is not padded onto the end.
	about := func(a, b float64) bool { return math.Abs(a-b) < 0.03 }
	if !about(record.Lines[0].End, 0.62) || !about(record.Lines[1].Start, 0.97) || !about(record.Lines[1].End, 1.47) {
		t.Errorf("lines = %+v", record.Lines)
	}
	if record.Words[4].Word != "blue?" || record.Words[6].Word != "scatters." {
		t.Errorf("the line's punctuation was not restored: %+v", record.Words)
	}
	if record.Words[5].Word != "Sunlight" || !about(record.Words[5].Start, 0.97) || !about(record.Words[6].Start, 1.07) {
		t.Errorf("the second line's words are not placed after the pause: %+v", record.Words[5:])
	}
	if math.Abs(record.Duration-1.47) > 0.06 {
		t.Errorf("duration = %v, want about 1.47", record.Duration)
	}
	segments, err := timingSegments(result["timing_path"].(string))
	if err != nil || len(segments) != 2 || segments[1].Text != "Sunlight scatters." {
		t.Errorf("subtitle segments = %+v, %v", segments, err)
	}
}

func TestComposerPropsFollowTheNarration(t *testing.T) {
	timing := filepath.Join(t.TempDir(), "narration.timing.json")
	studioWriteJSON(t, timing, map[string]any{"duration_seconds": 6.2, "file": "narration.mp3",
		"lines": []any{
			map[string]any{"id": "hook", "start": 0.1, "end": 2.0},
			map[string]any{"id": "why", "start": 2.2, "end": 4.0},
			map[string]any{"id": "close", "start": 5.0, "end": 6.2},
		},
		"words": []any{
			map[string]any{"word": "Why", "start": 0.1, "end": 0.4, "line": "hook"},
			map[string]any{"word": "blue?", "start": 0.5, "end": 0.9, "line": "hook"},
			map[string]any{"word": "Light", "start": 2.2, "end": 2.4, "line": "why"},
			map[string]any{"word": "scatters", "start": 2.4, "end": 2.6, "line": "why"},
			map[string]any{"word": "so", "start": 5.0, "end": 5.2, "line": "close"},
		},
	})
	plan, err := prepareComposerProps(map[string]any{
		"renderer_family": "explainer-data", "timing_path": timing, "output": "renders/final.mp4",
		"delivery_promise": "data_explainer", "metadata": map[string]any{"brief": "sky"},
		"cuts": []any{
			map[string]any{"type": "hero_title", "text": "Why is the sky blue?", "lines": []any{"hook"}},
			map[string]any{"type": "text_card", "text": "Scattering", "lines": []any{"why"}},
			map[string]any{"type": "stat_card", "stat": "5.5x", "lines": []any{"close"}},
		},
		"captions": map[string]any{"words_per_page": 3.0, "highlight_color": "#FFD700"},
		"audio": map[string]any{
			"narration": "narration.mp3",
			"music":     map[string]any{"src": "music.mp3", "volume": 0.4, "duck_under_narration": true, "fade_in_seconds": 1.0},
			"sfx":       []any{map[string]any{"src": "whoosh.mp3", "at_seconds": 2.0, "gain_db": -6.0}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.composition != "Explainer" || plan.loudness != -14 {
		t.Errorf("composition %q at %v LUFS", plan.composition, plan.loudness)
	}
	for _, key := range facetOnlyKeys {
		if _, present := plan.props[key]; present {
			t.Errorf("%s reached the composer", key)
		}
	}
	// Each cut runs from its first line to the next cut, the first from 0
	// and the last past the narration's end.
	want := [][2]float64{{0, 2.2}, {2.2, 5.0}, {5.0, 6.8}}
	for i, c := range plan.props["cuts"].([]any) {
		cut := c.(map[string]any)
		if _, present := cut["lines"]; present || !near(cut["in_seconds"].(float64), want[i][0]) || !near(cut["out_seconds"].(float64), want[i][1]) {
			t.Errorf("cut %d = %v, want %v", i, cut, want[i])
		}
	}
	if !near(plan.props["duration_seconds"].(float64), 6.8) {
		t.Errorf("duration_seconds = %v", plan.props["duration_seconds"])
	}
	captions := plan.props["captions"].(map[string]any)
	words := captions["words"].([]map[string]any)
	// Pages end at a sentence and at the end of a narration line.
	breaks := []bool{false, true, false, true, false}
	for i, w := range words {
		if (w["pageBreakAfter"] == true) != breaks[i] {
			t.Errorf("word %d %v: pageBreakAfter = %v", i, w["word"], w["pageBreakAfter"])
		}
	}
	if len(words) != 5 || words[0]["startMs"] != 100.0 || words[3]["endMs"] != 2600.0 || captions["wordsPerPage"] != 3.0 || captions["highlightColor"] != "#FFD700" {
		t.Errorf("captions = %v", captions)
	}
	audio := plan.props["audio"].(map[string]any)
	if !reflect.DeepEqual(audio["narration"], map[string]any{"src": "narration.mp3"}) {
		t.Errorf("narration = %v", audio["narration"])
	}
	music := audio["music"].(map[string]any)
	duck := music["duck"].(map[string]any)
	// Lines 0.2 s apart duck as one span; a 1 s pause lets the music up.
	if !reflect.DeepEqual(duck["ranges"], []any{[]any{0.1, 4.0}, []any{5.0, 6.2}}) || duck["level"] != 0.25 || music["fadeInSeconds"] != 1.0 {
		t.Errorf("music = %v", music)
	}
	if _, present := music["duck_under_narration"]; present {
		t.Error("duck_under_narration reached the composer")
	}
	sfx := audio["sfx"].([]any)[0].(map[string]any)
	if sfx["atSeconds"] != 2.0 || !near(sfx["volume"].(float64), 0.501) {
		t.Errorf("sfx = %v", sfx)
	}

	if plan, err := prepareComposerProps(map[string]any{"renderer_family": "cinematic-trailer"}); err != nil || plan.composition != "CinematicRenderer" {
		t.Errorf("cinematic-trailer chose %q, %v", plan.composition, err)
	}
	for name, bad := range map[string]map[string]any{
		"unknown family":    {"renderer_family": "commercial"},
		"unknown comp":      {"composition": "Slideshow"},
		"lines, no timing":  {"cuts": []any{map[string]any{"type": "text_card", "text": "x", "lines": []any{"hook"}}}},
		"unknown line":      {"timing_path": timing, "cuts": []any{map[string]any{"type": "text_card", "text": "x", "lines": []any{"nope"}}}},
		"duck, no timing":   {"audio": map[string]any{"music": map[string]any{"src": "m.mp3", "duck_under_narration": true}}},
		"captions, no word": {"captions": map[string]any{"words_per_page": 3.0}},
	} {
		if _, err := prepareComposerProps(bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestRemotionFailureReasonNamesTheRenderersError(t *testing.T) {
	for stderr, want := range map[string]string{
		"\x1b[31m\x1b[41m\x1b[37m Error \x1b[39m\x1b[31m\x1b[49m\x1b[39m \x1b[31mA delayRender() \"Loading bundled fonts\" was called but not cleared after 28000ms.\x1b[39m\n": `A delayRender() "Loading bundled fonts" was called but not cleared after 28000ms.`,
		"Error  Error: Could not find composition with ID X\n":                     "Error: Could not find composition with ID X",
		"Error: Was not able to close puppeteer page\nTypeError: x is undefined\n": "TypeError: x is undefined",
		"Rendered 12/30\n": "",
	} {
		if got := remotionFailureReason(stderr, ""); got != want {
			t.Errorf("reason(%q) = %q, want %q", stderr, got, want)
		}
	}
	flags := strings.Join(remotionLoadFlags(), " ")
	if !strings.Contains(flags, "--timeout=120000") || !strings.Contains(flags, "--concurrency=") {
		t.Errorf("render flags = %s", flags)
	}
}
