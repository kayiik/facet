package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const toolsList = `{"ok":true,"operation":"list","result":{"tools":[
 {"name":"media_probe","may_charge":false,"network":false,"external_write":false},
 {"name":"output_review","may_charge":false,"network":false,"external_write":true},
 {"name":"wikimedia","may_charge":false,"network":true,"external_write":true},
 {"name":"sora_video","may_charge":true,"network":true,"external_write":true},
 {"name":"openai_tts","may_charge":true,"network":true,"external_write":true}]},"warnings":[]}`

func fake(t *testing.T, calls *[][]string) Exec {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		*calls = append(*calls, args)
		if strings.Join(args, " ") == "tools list" {
			return []byte(toolsList), nil
		}
		return []byte(`{"ok":true,"tool":"` + args[2] + `","operation":"` + args[1] + `","result":{"echo":` + args[4] + `},"warnings":[]}`), nil
	}
}

func TestDirectFollowsTheDeclaredEffects(t *testing.T) {
	for _, c := range []struct {
		e    Effects
		want bool
	}{
		{Effects{}, true},                                    // read-only and local
		{Effects{ExternalWrite: true}, true},                 // local, writes evidence
		{Effects{Network: true}, true},                       // read-only lookup
		{Effects{Network: true, ExternalWrite: true}, false}, // downloads
		{Effects{MayCharge: true}, false},                    // never, whatever else
	} {
		if got := c.e.Direct(); got != c.want {
			t.Errorf("%+v: Direct() = %v, want %v", c.e, got, c.want)
		}
	}
}

func TestRunRefusesWhatTheWindowMayNotRun(t *testing.T) {
	var calls [][]string
	r := &Runner{Exec: fake(t, &calls)}
	ctx := context.Background()
	env, err := r.Run(ctx, "", "media_probe", map[string]any{"input": "a.mp4"})
	if err != nil || !env.OK || env.Tool != "media_probe" {
		t.Fatalf("media_probe: %+v, %v", env, err)
	}
	var result struct{ Echo map[string]any }
	if json.Unmarshal(env.Result, &result); result.Echo["input"] != "a.mp4" {
		t.Errorf("the request did not reach facet: %s", env.Result)
	}
	for _, tool := range []string{"sora_video", "wikimedia"} {
		before := len(calls)
		if _, err := r.Run(ctx, "", tool, map[string]any{}); !errors.Is(err, ErrNotDirect) {
			t.Errorf("%s ran from the window: %v", tool, err)
		}
		if len(calls) != before {
			t.Errorf("%s reached facet", tool)
		}
	}
	// Estimating a paid tool is allowed: it charges nothing.
	if _, err := r.Estimate(ctx, "", "sora_video", map[string]any{"prompt": "x"}); err != nil {
		t.Errorf("estimate refused: %v", err)
	}
	if _, err := r.Run(ctx, "", "no_such_tool", nil); err == nil {
		t.Error("an unknown tool ran")
	}
	paid, err := r.PaidTools(ctx)
	if err != nil || strings.Join(paid, ",") != "openai_tts,sora_video" {
		t.Errorf("PaidTools = %v, %v", paid, err)
	}
	lists := 0
	for _, c := range calls {
		if c[1] == "list" {
			lists++
		}
	}
	if lists != 1 {
		t.Errorf("tools list ran %d times; the effects are read once", lists)
	}
}

// With FACET_APP_FACET pointing at a real facet, the Toolkit's own
// declarations agree with the rule.
func TestRealToolkit(t *testing.T) {
	path := os.Getenv("FACET_APP_FACET")
	if path == "" {
		t.Skip("FACET_APP_FACET is not set")
	}
	r := New(path)
	ctx := context.Background()
	effects, err := r.Effects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !effects["media_probe"].Direct() || effects["sora_video"].Direct() {
		t.Errorf("media_probe %+v, sora_video %+v", effects["media_probe"], effects["sora_video"])
	}
	paid, _ := r.PaidTools(ctx)
	if len(paid) != 8 {
		t.Errorf("paid tools = %v", paid)
	}
	env, err := r.Run(ctx, t.TempDir(), "media_probe", map[string]any{"input": "missing.mp4"})
	if err != nil || env.OK || env.Error == nil {
		t.Errorf("a failing run should return its envelope: %+v %v", env, err)
	}
	version, err := r.Version(ctx)
	if err != nil || version == "" {
		t.Errorf("version = %q, %v", version, err)
	}
	tools, err := r.Tools(ctx)
	if err != nil || len(tools) != len(effects) {
		t.Fatalf("tools = %d, %v; effects list %d", len(tools), err, len(effects))
	}
	for _, tool := range tools {
		if tool.Name == "sora_video" && (len(tool.Dependencies) == 0 || tool.Dependencies[0].Name != "OPENAI_API_KEY" || !tool.MayCharge) {
			t.Errorf("sora_video = %+v", tool)
		}
	}
}

const readinessList = `{"ok":true,"operation":"list","result":{"tools":[
 {"name":"media_probe","capability":"media inspection","configured":true,"cost":{"currency":"USD","amount":0,"known":true},
  "dependencies":[{"name":"ffprobe","available":true,"path":"/usr/bin/ffprobe","type":"binary","resolution":"satisfied"}],
  "may_charge":false,"network":false,"external_write":false},
 {"name":"flux_image","capability":"cloud AI image generation via FLUX","configured":false,"cost":{"currency":"USD","amount":null,"known":false},
  "dependencies":[{"name":"FAL_KEY","available":false,"path":"","type":"env","resolution":"unsatisfied","alternatives":["FAL_KEY","FLUX_API_KEY"]}],
  "may_charge":true,"network":true,"external_write":true}]},"warnings":[]}`

func TestToolsAndVersionOnlyRead(t *testing.T) {
	var calls []string
	version := "facet v0.0.1\n"
	r := &Runner{Exec: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "tools list":
			return []byte(readinessList), nil
		case "version":
			return []byte(version), nil
		}
		t.Errorf("unexpected facet call %v", args)
		return nil, errors.New("unexpected")
	}}
	ctx := context.Background()
	tools, err := r.Tools(ctx)
	if err != nil || len(tools) != 2 {
		t.Fatalf("Tools = %+v, %v", tools, err)
	}
	probe, flux := tools[0], tools[1]
	if !probe.Configured || probe.Capability != "media inspection" || !probe.Cost.Known || *probe.Cost.Amount != 0 || !probe.Direct() {
		t.Errorf("media_probe = %+v", probe)
	}
	if flux.Configured || flux.Cost.Known || flux.Cost.Amount != nil || !flux.MayCharge || flux.Direct() {
		t.Errorf("flux_image = %+v", flux)
	}
	if dep := flux.Dependencies[0]; dep.Name != "FAL_KEY" || dep.Type != "env" || dep.Available || strings.Join(dep.Alternatives, ",") != "FAL_KEY,FLUX_API_KEY" {
		t.Errorf("flux_image needs %+v", dep)
	}
	// Readiness is read again on every call; the effects R1 checks are read
	// once and stay the ones the App started with.
	r.Tools(ctx)
	r.Effects(ctx)
	r.Effects(ctx)
	if strings.Join(calls, ",") != "tools list,tools list,tools list" {
		t.Errorf("calls = %v", calls)
	}

	if v, err := r.Version(ctx); err != nil || v != "0.0.1" {
		t.Errorf("Version = %q, %v", v, err)
	}
	for _, printed := range []string{"", "0.0.1", "facet", "facet v", "compa v1.0", "facet v1 extra words"} {
		version = printed
		if v, err := r.Version(ctx); err == nil {
			t.Errorf("%q was read as version %q", printed, v)
		}
	}
}
