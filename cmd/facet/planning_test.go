package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPlanningCommandsAreMachineReadableAndReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "capabilities", args: []string{"capabilities"}},
		{name: "pipelines-list", args: []string{"pipelines", "list"}},
		{name: "pipelines-describe", args: []string{"pipelines", "describe", "animated-explainer", "--stage", "script"}},
		{name: "guidance-file", args: []string{"guidance", "guidance/stages/script.md"}},
		{name: "guidance-folder", args: []string{"guidance", "guidance/craft"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, home := t.TempDir(), t.TempDir()
			out, code := runCLI(t, dir, home, "", tc.args...)
			if code != 0 {
				t.Fatalf("exit=%d output=%s", code, out)
			}
			var envelope map[string]any
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			if envelope["ok"] != true {
				t.Fatalf("unexpected envelope: %#v", envelope)
			}
			for _, root := range []string{dir, home} {
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("%s wrote in %s: %v (%v)", tc.name, root, entries, err)
				}
			}
		})
	}
}

func TestPlanningCommandsRejectUnknownNames(t *testing.T) {
	for _, args := range [][]string{
		{"pipelines", "describe", "commercial"},
		{"pipelines", "describe", "animated-explainer", "--stage", "shoot"},
		{"pipelines", "list", "extra"},
		{"guidance", "../go.mod"},
		{"capabilities", "extra"},
	} {
		dir, home := t.TempDir(), t.TempDir()
		if out, code := runCLI(t, dir, home, "", args...); code == 0 {
			t.Errorf("%v succeeded: %s", args, out)
		}
	}
}

// Before every stage the agent asks for that stage alone: the pipeline's
// other stages and the stance guide it read at intake are not sent again.
func TestPipelineDescribeWithAStageSendsOnlyThatStage(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	out, code := runCLI(t, dir, home, "", "pipelines", "describe", "animated-explainer", "--stage", "script")
	if code != 0 {
		t.Fatalf("exit=%d output=%s", code, out)
	}
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope.Result
	pipeline, _ := r["pipeline"].(map[string]any)
	stage, _ := r["stage"].(map[string]any)
	if pipeline == nil || pipeline["stages"] != nil || pipeline["structures"] == nil {
		t.Errorf("the pipeline header should keep its structures and drop its stages: %v", pipeline)
	}
	if stage == nil || stage["guide"] == nil || stage["availability"] == nil || r["stance_guide"] != nil || r["stance_guide_path"] == nil {
		t.Errorf("result = %v", r)
	}
	if out, code := runCLI(t, dir, home, "", "guidance", r["stance_guide_path"].(string)); code != 0 {
		t.Errorf("the stance guide path does not resolve: %s", out)
	}
}
