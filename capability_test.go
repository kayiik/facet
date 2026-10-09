package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedGuidanceMatchesTheRepositoryFiles(t *testing.T) {
	files := GuidanceFiles("")
	if len(files) == 0 {
		t.Fatal("no guidance is embedded")
	}
	for _, name := range files {
		embedded, err := Guidance(name)
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.FromSlash(name))
		if err != nil {
			t.Fatal(err)
		}
		if embedded != string(source) {
			t.Fatalf("embedded guidance drifted from its source: %s", name)
		}
	}
}

func TestGuidanceServesTheCoreFiles(t *testing.T) {
	for _, name := range []string{
		"skills/facet/SKILL.md",
		"agents/facet-critic.md",
		"pipelines/animated-explainer.yaml",
		"guidance/stages/script.md",
		"guidance/stances/explainer.md",
		"guidance/runtimes/scene-types.md",
		"styles/clean-professional.yaml",
		"schemas/artifacts/script.schema.json",
	} {
		if body, err := Guidance(name); err != nil || strings.TrimSpace(body) == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestGuidanceRefusesPathsOutsideTheBundle(t *testing.T) {
	for _, name := range []string{"", "../go.mod", "go.mod", "internal/toolbox/toolbox.go", "skills/facet/../../go.mod", "guidance/stages/script.txt"} {
		if _, err := Guidance(name); err == nil {
			t.Errorf("%q was served", name)
		}
	}
}

func TestGuidanceFilesFiltersByPrefix(t *testing.T) {
	stages := GuidanceFiles("guidance/stages")
	if len(stages) != 10 {
		t.Fatalf("want the 10 stage guides, got %d: %v", len(stages), stages)
	}
	for _, name := range stages {
		if !strings.HasPrefix(name, "guidance/stages/") {
			t.Errorf("%s is outside the prefix", name)
		}
	}
}
