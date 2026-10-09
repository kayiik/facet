package toolbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// EffectsFor is what adapters declare to their hosts, so it must say exactly
// what the listing and describe say, for every tool.
func TestEffectsForAgreesWithEveryPublishedSurface(t *testing.T) {
	var readOnly []string
	for _, name := range Names() {
		effects := EffectsFor(name)
		listed := summary(name)
		described, ok := Describe(name)
		if !ok {
			t.Fatalf("%s cannot be described", name)
		}
		for field, want := range map[string]bool{
			"may_charge":     effects.MayCharge,
			"network":        effects.Network,
			"external_write": effects.ExternalWrite,
			"deterministic":  effects.Deterministic,
		} {
			if listed[field] != want || described[field] != want {
				t.Errorf("%s.%s: EffectsFor says %v, listing %v, describe %v", name, field, want, listed[field], described[field])
			}
		}
		if effects.ReadOnly == effects.ExternalWrite {
			t.Errorf("%s: read_only %v and external_write %v must be complements", name, effects.ReadOnly, effects.ExternalWrite)
		}
		if effects.ReadOnly {
			readOnly = append(readOnly, name)
		}
		if effects.Deterministic && (effects.Network || effects.MayCharge) {
			t.Errorf("%s is declared deterministic while networked or chargeable", name)
		}
	}
	sort.Strings(readOnly)
	want := []string{"audio_probe", "image_selector", "media_probe", "music_library", "plan_check", "script_check", "video_selector"}
	if !reflect.DeepEqual(readOnly, want) {
		t.Fatalf("read-only tools = %v, want %v", readOnly, want)
	}
}

// visual_qa's probe writes nothing, but its review writes frames: read-only
// is a property of every request a tool accepts, so visual_qa is not.
func TestReadOnlyHoldsForEveryOperation(t *testing.T) {
	for _, tool := range []string{"visual_qa", "video_stitch", "hyperframes_compose", "scene_detect", "silence_cutter"} {
		if EffectsFor(tool).ReadOnly {
			t.Errorf("%s has an operation that writes files but is declared read-only", tool)
		}
	}
}

func TestHyperFramesDeclaresNetwork(t *testing.T) {
	effects := EffectsFor("hyperframes_compose")
	if !effects.Network || effects.Deterministic || effects.MayCharge {
		t.Fatalf("hyperframes_compose effects = %+v; it loads remote scripts and is not deterministic", effects)
	}
	if !executionFor("hyperframes_compose").Network {
		t.Fatal("the envelope's execution does not declare network for hyperframes_compose")
	}
}

// A misspelt name must over-gate, never under-gate.
func TestEffectsForUnknownToolIsConservative(t *testing.T) {
	got := EffectsFor("not_a_facet_tool")
	want := Effects{MayCharge: true, Network: true, ExternalWrite: true}
	if got != want {
		t.Fatalf("EffectsFor(unknown) = %+v, want %+v", got, want)
	}
	if EffectsFor("edgetts") != want {
		t.Fatal("a name that is not a tool's own must over-gate: there are no aliases")
	}
}

func TestEffectsMarshalWithStableNames(t *testing.T) {
	data, err := json.Marshal(EffectsFor("media_probe"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"may_charge"`, `"network"`, `"external_write"`, `"deterministic"`, `"read_only"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("effects JSON lacks %s: %s", key, data)
		}
	}
}

// Describe is the describe JSON, and a caller may modify what it returns
// without changing what the next caller sees.
func TestDescribeReturnsTheDescribeJSON(t *testing.T) {
	for _, name := range Names() {
		got, ok := Describe(name)
		if !ok {
			t.Fatalf("Describe(%q) failed", name)
		}
		env, cliOK := CLI([]string{"tools", "describe", name})
		if !cliOK {
			t.Fatalf("describe %s failed", name)
		}
		want := contractJSON(t, env.Result)
		if !reflect.DeepEqual(contractJSON(t, got), want) {
			t.Fatalf("Describe(%q) differs from `facet tools describe`", name)
		}
	}
	first, _ := Describe("media_probe")
	first["request_schema"].(map[string]any)["properties"] = nil
	first["name"] = "changed"
	second, _ := Describe("media_probe")
	if second["name"] != "media_probe" || second["request_schema"].(map[string]any)["properties"] == nil {
		t.Fatal("modifying a description changed the registry")
	}
	if _, ok := Describe("not_a_facet_tool"); ok {
		t.Fatal("an unknown tool was described")
	}
}

func TestEstimateContextValidatesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "new", "subs.srt")
	request, _ := json.Marshal(map[string]any{"segments": []any{map[string]any{"text": "hi", "start": 0, "end": 1}}, "output_path": output})
	env := EstimateContext(context.Background(), "subtitle_gen", request)
	if !env.OK || env.Operation != "estimate" || env.Tool != "subtitle_gen" || env.Execution.ExternalWrite || len(env.Artifacts) != 0 {
		t.Fatalf("unexpected estimate envelope: %+v", env)
	}
	if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
		t.Fatal("an estimate wrote output")
	}

	if env := EstimateContext(context.Background(), "subtitle_gen", []byte(`{"segments":[],"surprise":1}`)); env.OK || env.Error.Code != "invalid_request" {
		t.Fatalf("an invalid request was estimated: %+v", env)
	}
	if env := EstimateContext(context.Background(), "not_a_facet_tool", request); env.OK || env.Error.Code != "unknown_tool" {
		t.Fatalf("an unknown tool was estimated: %+v", env)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if env := EstimateContext(ctx, "subtitle_gen", request); env.OK || env.Error.Code != "cancelled" {
		t.Fatalf("a cancelled estimate ran: %+v", env)
	}
	// Chargeable tools keep an unknown cost through the exported path too.
	if env := EstimateContext(context.Background(), "gflow_image", []byte(`{"prompt":"x"}`)); !env.OK || env.Execution.EstimatedCost != nil {
		t.Fatalf("chargeable estimate reported a known cost: %+v", env)
	}
}

// `facet tools --help` and `facet tools <op> --help` succeed with usage.
func TestToolsHelpSucceedsWithUsage(t *testing.T) {
	for _, args := range [][]string{
		{"tools", "--help"},
		{"tools", "-h"},
		{"tools", "help"},
		{"tools", "list", "--help"},
		{"tools", "describe", "--help"},
		{"tools", "estimate", "--help"},
		{"tools", "run", "--help"},
		{"tools", "run", "media_probe", "--help"},
		{"tools", "help", "run"},
	} {
		env, ok := CLI(args)
		if !ok || !env.OK || env.Operation != "help" {
			t.Fatalf("%v did not succeed with help: %+v", args, env)
		}
		help, _ := env.Result.(map[string]any)
		usage, _ := help["usage"].(string)
		if !strings.Contains(usage, "usage: facet tools") {
			t.Fatalf("%v printed no usage: %#v", args, help)
		}
		if len(args) > 2 && args[1] != "help" && args[1] != "--help" && args[1] != "-h" {
			if help["operation"] != args[1] {
				t.Fatalf("%v did not describe its operation: %#v", args, help)
			}
		}
		if _, err := json.Marshal(env); err != nil {
			t.Fatalf("%v help does not marshal: %v", args, err)
		}
	}
	env, _ := CLI([]string{"tools", "run", "media_probe", "--help"})
	help := env.Result.(map[string]any)
	if help["tool"] != "media_probe" || help["request_schema"] == nil {
		t.Fatalf("tool-specific help lacks the request schema: %#v", help)
	}
	general, _ := CLI([]string{"tools", "--help"})
	if ops, _ := general.Result.(map[string]any)["operations"].([]any); len(ops) != 4 {
		t.Fatalf("general help does not list the four operations: %#v", general.Result)
	}
}

// Help never runs anything, and a bare `facet tools` is still a usage error.
func TestToolsHelpRunsNothingAndBareToolsFails(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "must-not-exist.srt")
	request, _ := json.Marshal(map[string]any{"segments": []any{map[string]any{"text": "hi", "start": 0, "end": 1}}, "output_path": output})
	if env, ok := CLI([]string{"tools", "run", "subtitle_gen", "--input", string(request), "--help"}); !ok || env.Operation != "help" {
		t.Fatalf("help with a request did not print help: %+v", env)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("asking for help ran the tool")
	}
	env, ok := CLI([]string{"tools"})
	if ok || env.OK || env.Error == nil || !strings.Contains(env.Error.Message, "usage") {
		t.Fatalf("bare tools did not fail with usage: %+v", env)
	}
}

// The CLI run path reports artifacts exactly as RunContext does.
func TestCLIRunReportsArtifacts(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "subs.srt")
	request, _ := json.Marshal(map[string]any{"segments": []any{map[string]any{"text": "hello world", "start": 0, "end": 1}}, "output_path": output})
	env, ok := CLI([]string{"tools", "run", "subtitle_gen", "--input", string(request)})
	if !ok || len(env.Artifacts) != 1 {
		t.Fatalf("CLI run did not describe its output: %+v", env)
	}
	assertArtifacts(t, "subtitle_gen", env, 1)
	if !sameFile(env.Artifacts[0].Path, output) || env.Artifacts[0].MediaType != "application/x-subrip" {
		t.Fatalf("unexpected artifact: %+v", env.Artifacts[0])
	}
}
