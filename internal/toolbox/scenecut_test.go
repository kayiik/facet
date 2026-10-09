package toolbox

import (
	"encoding/json"
	"testing"
)

// A scene becomes a cut by renaming its timings and carrying everything else
// through.
//
// The mapping used to copy five fields — id, type, the two timings, and
// description->text — and silently drop the rest. The Explainer composition
// reads 63 distinct cut fields, so a scene describing its content through any
// of them rendered as an EMPTY cut: a valid mp4 with nothing in it, reported
// as a success.
//
// Verified before the fix: four renders with different text and different
// backgrounds produced byte-identical files (sha256 6d1487ed..., 167833 bytes
// each) and output_review's content gate reported "every sampled frame is
// blank". After: 200183 bytes, content gate passes, and a sampled frame is
// 10176 bytes against 2078 for the blank render.
func TestSceneFieldsReachTheComposition(t *testing.T) {
	var raw map[string]any
	body := `{"scenes":[{"id":"s1","type":"text_card","start_seconds":0,"end_seconds":2,
	           "text":"Hello","fontSize":54,"backgroundColor":"#1e293b","color":"#f8fafc"}]}`
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	cut := mapSceneToCut(raw["scenes"].([]any)[0].(map[string]any))

	// Timings are RENAMED: the composition reads the in_/out_ pair.
	if cut["in_seconds"] != float64(0) || cut["out_seconds"] != float64(2) {
		t.Errorf("timings not renamed: in=%v out=%v", cut["in_seconds"], cut["out_seconds"])
	}
	if _, present := cut["start_seconds"]; present {
		t.Error("start_seconds survived; the composition does not read it")
	}

	// Content fields must survive, or the cut renders blank.
	for k, want := range map[string]any{
		"type": "text_card", "text": "Hello", "fontSize": float64(54),
		"backgroundColor": "#1e293b", "color": "#f8fafc", "id": "s1",
	} {
		if cut[k] != want {
			t.Errorf("%s = %v, want %v; a dropped field renders as blank", k, cut[k], want)
		}
	}
}

// description is the scene-plan name for a cut's text, but an explicit text
// must win: a caller supplying both should not have it overridden by prose.
func TestExplicitTextBeatsDescription(t *testing.T) {
	scene := map[string]any{
		"start_seconds": float64(0), "end_seconds": float64(1),
		"description": "prose summary", "text": "the real caption",
	}
	if got := mapSceneToCut(scene)["text"]; got != "the real caption" {
		t.Errorf("text = %v; an explicit text must not be replaced by description", got)
	}
}

// A scene with only a description still gets its text, which is what the
// scene-plan shape relies on.
func TestDescriptionBecomesTextWhenTextIsAbsent(t *testing.T) {
	scene := map[string]any{
		"start_seconds": float64(0), "end_seconds": float64(1),
		"description": "prose summary",
	}
	if got := mapSceneToCut(scene)["text"]; got != "prose summary" {
		t.Errorf("text = %v, want the description", got)
	}
}
