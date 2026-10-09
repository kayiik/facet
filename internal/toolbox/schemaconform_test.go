package toolbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A TOOL MUST SATISFY ITS OWN PUBLISHED SCHEMA.
//
// media_probe declares video_streams and audio_streams as REQUIRED arrays and
// returned `null` for a video-only file -- a legitimate input making the tool
// violate the contract it advertises. Every consumer that trusted the schema
// would have been forced into a nil check the schema says is unnecessary.
//
// FOUND BY A TARGET CLI DURING RELEASE C BUNDLE TESTING, not by this repo. The
// agent read `tools describe` and the run output together and compared them --
// which nothing here had ever done. That is the argument for the CLI-bundle
// surface being a real test surface rather than only a packaging exercise: the
// driver reads the contract the way a consumer does.
//
// Same class as the null-collection defect the v2 envelope work fixed one layer
// up, surviving inside a tool RESULT where that fix did not reach.
func TestRequiredArrayFieldsAreNeverNull(t *testing.T) {
	// A real media file with no audio track: the input that exposed it.
	sample := filepath.Join("..", "..", ".quality-run", "jb", "hello.mp4")
	if _, err := os.Stat(sample); err != nil {
		t.Skipf("no sample media available: %v", err)
	}

	req := map[string]any{"input": sample}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := execute("media_probe", "run", raw)
	if err != nil {
		t.Skipf("media_probe unavailable here: %v", err)
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}

	// Read the REQUIRED list from the tool's own published schema rather than
	// restating it here. A second list would drift from the schema, and then
	// the test and the contract could disagree about what the contract says.
	schema, ok := resultSchemas["media_probe"]
	if !ok {
		t.Fatal("media_probe publishes no result schema")
	}
	sraw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(sraw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Required) == 0 {
		// A check with no required fields would pass on any output at all.
		t.Fatal("media_probe's result schema declares no required fields; this check would be vacuous")
	}

	var checkedArrays int
	for _, name := range decoded.Required {
		val, present := body[name]
		if !present {
			t.Errorf("required field %q is absent from the result", name)
			continue
		}
		// Only array-typed fields can exhibit the null-collection defect.
		propRaw, ok := decoded.Properties[name]
		if !ok {
			continue
		}
		var prop struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(propRaw, &prop); err != nil || prop.Type != "array" {
			continue
		}
		checkedArrays++
		if string(val) == "null" {
			t.Errorf("required array field %q is null; the schema declares it an array, "+
				"so every consumer is forced into a nil check the contract says is unnecessary", name)
		}
	}
	if checkedArrays == 0 {
		t.Error("no required array field was examined; the check had no subject")
	}
}
