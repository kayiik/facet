package toolbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExplainerDirectProfileReachesRenderer(t *testing.T) {
	// Capture actual --props bytes and fail intentionally before any media is rendered.
	workspace := composeDeliveryFixture(t, `
const fs = require('fs');
const arg = process.argv.find(value => value.startsWith('--props='));
fs.copyFileSync(arg.slice('--props='.length), '../captured-profile.json');
process.exit(23);
`)
	request := map[string]any{
		"composition_id": "Explainer", "output": filepath.Join(workspace, "output.mp4"),
		"width": 320, "height": 180, "fps": 24, "duration_seconds": 3,
		"cuts": []map[string]any{{"id": "intro", "type": "text_card", "source": "", "in_seconds": 0, "out_seconds": 3, "text": "Test"}},
	}

	_, _, err := doVideoCompose("run", mustProfileJSON(t, request))
	if err == nil {
		t.Fatal("capture-only renderer must fail, never report a successful render")
	}
	data, err := os.ReadFile(filepath.Join(workspace, "captured-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var props map[string]any
	if err := json.Unmarshal(data, &props); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]float64{"width": 320, "height": 180, "fps": 24, "duration_seconds": 3} {
		if props[field] != want {
			t.Errorf("renderer lost %s: got %v, want %v", field, props[field], want)
		}
	}
}

func TestScenePlanBackgroundColorReachesRenderer(t *testing.T) {
	workspace := composeDeliveryFixture(t, `
const fs = require('fs');
const arg = process.argv.find(value => value.startsWith('--props='));
fs.copyFileSync(arg.slice('--props='.length), '../captured-scene-props.json');
process.exit(23);
`)
	request := map[string]any{
		"output":          filepath.Join(workspace, "output.mp4"),
		"backgroundColor": "#123456",
		"scenes": []map[string]any{{
			"id": "intro", "type": "text_card", "text": "Test",
			"start_seconds": 0, "end_seconds": 1,
		}},
	}

	_, _, err := doVideoCompose("run", mustProfileJSON(t, request))
	if err == nil {
		t.Fatal("capture-only renderer must fail, never report a successful render")
	}
	data, err := os.ReadFile(filepath.Join(workspace, "captured-scene-props.json"))
	if err != nil {
		t.Fatal(err)
	}
	var props map[string]any
	if err := json.Unmarshal(data, &props); err != nil {
		t.Fatal(err)
	}
	if got := props["backgroundColor"]; got != "#123456" {
		t.Fatalf("scene-plan projection backgroundColor = %v, want #123456", got)
	}
}

func mustProfileJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
