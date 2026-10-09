package toolbox

import (
	"encoding/json"
	"testing"
)

// AUDIO DECLARED IN THE ENVELOPE FORM MUST REACH THE COMPOSITION.
//
// composeRequest.Audio was accepted, decoded, and read by nothing. The envelope
// path marshalled only edit_decisions into props, and composeEditDecisions has
// no audio field -- so a request declaring narration rendered a SILENT video
// and reported success.
//
// The same declared-and-ignored defect was already fixed here once for
// width/height. The fix was not generalized, so the next field of that shape
// failed identically.
//
// Asserted at the PROPS level rather than by rendering: a render takes minutes
// and needs Remotion, while the defect is entirely in what gets marshalled.
// The real render was done once by hand to confirm the fix end to end; this
// keeps it from regressing cheaply.
func TestEnvelopeAudioReachesProps(t *testing.T) {
	body := []byte(`{
	  "operation":"compose",
	  "output_path":"out.mp4",
	  "audio":{"narration":{"src":"narration/voice.mp3","volume":1}},
	  "edit_decisions":{"render_runtime":"remotion","cuts":[
	    {"id":"c1","source":"","type":"hero_title","text":"x","in_seconds":0,"out_seconds":1}]}
	}`)

	var r composeRequest
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if r.Audio == nil {
		t.Fatal("audio was not decoded from the request at all")
	}
	if r.EditDecisions == nil {
		t.Fatal("edit_decisions did not decode")
	}

	// CALL THE REAL FUNCTION. The first version of this test rebuilt the merge
	// inside the test body, so it passed no matter what compose.go did --
	// removing the fix left it green. That is the circular-oracle shape: both
	// sides computed the same answer from the same idea.
	raw, err := buildRemotionProps(r)
	if err != nil {
		t.Fatal(err)
	}
	merged := map[string]any{}
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatal(err)
	}

	if _, ok := merged["audio"]; !ok {
		t.Error("audio is absent from the props the composition receives; " +
			"narration would be silently dropped and the render would report success")
	}
	// The composition reads `audio` at the TOP LEVEL. Nested under
	// edit_decisions it would be equally invisible.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["audio"]; !ok {
		t.Error("audio is not at the top level of props")
	}
	if _, ok := top["cuts"]; !ok {
		t.Error("cuts were lost while merging audio; the merge must preserve the edit decisions")
	}
}
