package catalog

import (
	"strings"
	"testing"
)

func TestEveryBundledPipelineLoadsAndValidates(t *testing.T) {
	names := Names()
	if len(names) != 12 {
		t.Fatalf("want OpenMontage's 12 pipelines, got %d: %v", len(names), names)
	}
	pipelines, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pipelines {
		for _, st := range p.Stages {
			if _, err := StageGuide(st.ID); err != nil {
				t.Errorf("%s stage %s has no guide: %v", p.Name, st.ID, err)
			}
		}
		if _, err := StanceGuide(p.Stance); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
	}
}

func TestVocabulariesMatchTheBundle(t *testing.T) {
	if got := strings.Join(StanceIDs(), ","); got != "animation,cinematic,editorial,explainer,product" {
		t.Errorf("stances: %s", got)
	}
	if got := strings.Join(StyleIDs(), ","); got != "anime-ghibli,clean-professional,flat-motion-graphics,minimalist-diagram,premium-minimalist" {
		t.Errorf("styles: %s", got)
	}
	for _, name := range []string{"brief", "script", "scene_plan", "edit_decisions", "narration_timing", "review"} {
		if !contains(ArtifactNames(), name) {
			t.Errorf("missing record schema %s", name)
		}
	}
}

func TestValidateRefusesUnknownTerms(t *testing.T) {
	p, err := Load("animated-explainer")
	if err != nil {
		t.Fatal(err)
	}
	p.Stages = append([]Stage(nil), p.Stages...)
	p.Stages[0].Role = "showrunner"
	p.Stages[0].Needs = []string{"telepathy"}
	err = Validate(p, "animated-explainer")
	if err == nil || !strings.Contains(err.Error(), "showrunner") || !strings.Contains(err.Error(), "telepathy") {
		t.Fatalf("unknown role and capability were accepted: %v", err)
	}
}

func TestLoadRefusesAnUnknownPipeline(t *testing.T) {
	if _, err := Load("commercial"); err == nil {
		t.Fatal("an unknown pipeline loaded")
	}
}
