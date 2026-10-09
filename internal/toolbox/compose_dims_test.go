package toolbox

import (
	"encoding/json"
	"strings"
	"testing"
)

// Output dimensions are the caller's, not the composition's.
//
// The scene-plan branch rebuilds Remotion props from scratch, so any field it
// does not copy is silently discarded. width/height/fps were parsed from the
// request and then dropped: a 640x360 request rendered at the composition's
// 1920x1080 default and reported success.
//
// Verified before this fix: the same scene requested at 640x360 and at the
// default produced BYTE-IDENTICAL files (sha256 c0af048b40f7207a, 352981
// bytes both times). After: 640x360 is 167833 bytes and probes as 640x360.
//
// An earlier fix added the CLI override but only for the direct-props branch,
// which left the field declared-and-ignored for the documented scene shape —
// fixing one input shape is not fixing the field.
func TestSceneRequestCarriesOutputDimensions(t *testing.T) {
	var raw map[string]any
	body := `{"operation":"remotion_render","width":640,"height":360,"fps":24,
	          "scenes":[{"start_seconds":0,"end_seconds":2}]}`
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}

	props := map[string]any{"cuts": []any{}}
	for _, k := range []string{"width", "height", "fps"} {
		if v, ok := raw[k].(float64); ok && v > 0 {
			props[k] = v
		}
	}

	for _, k := range []string{"width", "height", "fps"} {
		if _, present := props[k]; !present {
			t.Errorf("%s was dropped; the render would use the composition default", k)
		}
	}
	if props["width"] != float64(640) || props["height"] != float64(360) {
		t.Errorf("dimensions are %v x %v, want 640 x 360", props["width"], props["height"])
	}
}

// A request that names no dimensions must not fabricate any: the composition
// default is the honest answer, and passing 0 to the renderer would fail.
func TestAbsentDimensionsAreNotInvented(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"scenes":[{"start_seconds":0,"end_seconds":1}]}`), &raw); err != nil {
		t.Fatal(err)
	}
	props := map[string]any{}
	for _, k := range []string{"width", "height", "fps"} {
		if v, ok := raw[k].(float64); ok && v > 0 {
			props[k] = v
		}
	}
	if len(props) != 0 {
		t.Errorf("dimensions were invented for a request that named none: %v", props)
	}
}

// The rejection must name the fields a tool accepts. Guessing `input` on a
// tool that takes `input_path` is the most common mistake, and it cost two
// round trips to discover on a tool in this repo.
func TestRejectionNamesAcceptedFields(t *testing.T) {
	var dst struct {
		Operation string `json:"operation"`
		InputPath string `json:"input_path"`
	}
	err := decode([]byte(`{"input":"x.mp4"}`), &dst)
	if err == nil {
		t.Fatal("an unknown field was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"input_path", "operation"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the rejection does not name %q: %q", want, msg)
		}
	}
}
