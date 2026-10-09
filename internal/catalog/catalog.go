// Package catalog reads Facet's bundled production knowledge: the pipeline
// files, stage guides, producer stances, styles and record schemas. It owns the
// closed vocabularies a pipeline file may use and refuses anything else, so a
// pipeline can never name a stage, role, capability or scene that doesn't exist.
package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	facet "github.com/kayiik/facet"
)

// Closed vocabularies.
var (
	StageIDs   = []string{"intake", "research", "proposal", "script", "scene_plan", "assets", "edit", "compose", "review", "publish"}
	Roles      = []string{"producer", "researcher", "director", "writer", "art_director", "editor", "sound_designer", "critic"}
	Categories = []string{"generated", "animation", "cinematic", "documentary", "footage", "screen", "avatar", "localization", "hybrid", "repurpose"}
	StartsFrom = []string{"topic", "product", "website", "footage", "audio", "track", "reference", "script", "brief"}
	// Capabilities are what tools provide and what pipeline stages need.
	Capabilities     = []string{"voice", "image_stock", "image_generation", "video_stock", "video_generation", "music", "sfx", "transcription", "capture", "avatar", "composition", "editing", "audio", "captions", "analysis", "review", "studio_checks"}
	RendererFamilies = []string{"explainer-data", "explainer-teacher", "product-reveal", "screen-demo", "animation-first", "cinematic-trailer", "documentary-montage", "presenter"}
	Runtimes         = []string{"remotion", "hyperframes", "ffmpeg"}
	Modes            = []string{"templated", "atelier"}
	DeliveryPromises = []string{"motion_led", "source_led", "data_explainer", "teacher_explainer", "screen_demo", "avatar_presenter", "hybrid", "localization"}
	// SceneTypes are the composer's Explainer cut types (see
	// remotion-composer/src/contract.ts).
	SceneTypes = []string{"text_card", "hero_title", "section_title", "callout", "stat_card", "stat_reveal", "kpi_grid", "progress_bar", "comparison", "bar_chart", "line_chart", "pie_chart", "terminal_scene", "screenshot_scene", "anime_scene", "provider_chip", "image", "video"}
)

type Beat struct {
	ID      string   `yaml:"id" json:"id"`
	Share   float64  `yaml:"share" json:"share"`
	Purpose string   `yaml:"purpose" json:"purpose"`
	Scenes  []string `yaml:"scenes" json:"scenes"`
}

type Structure struct {
	ID      string `yaml:"id" json:"id"`
	Title   string `yaml:"title" json:"title"`
	UseWhen string `yaml:"use_when" json:"use_when"`
	Beats   []Beat `yaml:"beats" json:"beats"`
}

type Stage struct {
	ID          string   `yaml:"id" json:"id"`
	Role        string   `yaml:"role" json:"role"`
	Consumes    []string `yaml:"consumes" json:"consumes"`
	Produces    []string `yaml:"produces" json:"produces"`
	Approval    bool     `yaml:"approval" json:"approval"`
	Needs       []string `yaml:"needs" json:"needs"`
	ReviewFocus []string `yaml:"review_focus" json:"review_focus"`
	Success     []string `yaml:"success" json:"success"`
	Notes       string   `yaml:"notes,omitempty" json:"notes,omitempty"`
}

type Styles struct {
	Recommended []string `yaml:"recommended" json:"recommended"`
	Also        []string `yaml:"also" json:"also"`
}

type Pipeline struct {
	Name             string      `yaml:"name" json:"name"`
	Title            string      `yaml:"title" json:"title"`
	Summary          string      `yaml:"summary" json:"summary"`
	Category         string      `yaml:"category" json:"category"`
	StartsFrom       []string    `yaml:"starts_from" json:"starts_from"`
	Stance           string      `yaml:"stance" json:"stance"`
	DeliveryPromises []string    `yaml:"delivery_promises" json:"delivery_promises"`
	RendererFamilies []string    `yaml:"renderer_families" json:"renderer_families"`
	Runtimes         []string    `yaml:"runtimes" json:"runtimes"`
	Modes            []string    `yaml:"modes" json:"modes"`
	Styles           Styles      `yaml:"styles" json:"styles"`
	ReferenceInput   bool        `yaml:"reference_input" json:"reference_input"`
	Structures       []Structure `yaml:"structures" json:"structures"`
	Stages           []Stage     `yaml:"stages" json:"stages"`
}

// Stage returns the pipeline's stage with this id.
func (p Pipeline) Stage(id string) (Stage, bool) {
	for _, s := range p.Stages {
		if s.ID == id {
			return s, true
		}
	}
	return Stage{}, false
}

// Names lists the bundled pipeline names, sorted.
func Names() []string {
	return baseNames("pipelines", ".yaml")
}

// StyleIDs lists the bundled style ids, sorted.
func StyleIDs() []string { return baseNames("styles", ".yaml") }

// StanceIDs lists the bundled producer stances, sorted.
func StanceIDs() []string { return baseNames("guidance/stances", ".md") }

// ArtifactNames lists the bundled record schemas, sorted.
func ArtifactNames() []string { return baseNames("schemas/artifacts", ".schema.json") }

func baseNames(dir, suffix string) []string {
	entries, err := fs.ReadDir(facet.Assets, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			out = append(out, strings.TrimSuffix(e.Name(), suffix))
		}
	}
	sort.Strings(out)
	return out
}

// Load reads and validates one pipeline.
func Load(name string) (Pipeline, error) {
	if !contains(Names(), name) {
		return Pipeline{}, fmt.Errorf("no pipeline %q; pipelines are %s", name, strings.Join(Names(), ", "))
	}
	file := "pipelines/" + name + ".yaml"
	data, err := facet.Assets.ReadFile(file)
	if err != nil {
		return Pipeline{}, err
	}
	var p Pipeline
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Pipeline{}, fmt.Errorf("%s: %w", file, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Pipeline{}, fmt.Errorf("%s: holds more than one document", file)
	}
	if err := Validate(p, name); err != nil {
		return Pipeline{}, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

// All loads every pipeline, sorted by name.
func All() ([]Pipeline, error) {
	var out []Pipeline
	for _, name := range Names() {
		p, err := Load(name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Validate checks a pipeline against the closed vocabularies and the bundled
// stances, styles and record schemas.
func Validate(p Pipeline, file string) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	oneOf := func(field, value string, allowed []string) {
		if !contains(allowed, value) {
			add("%s %q is not one of %s", field, value, strings.Join(allowed, ", "))
		}
	}
	each := func(field string, values, allowed []string) {
		for _, v := range values {
			oneOf(field, v, allowed)
		}
	}
	if p.Name != file {
		add("name %q must equal the file name %q", p.Name, file)
	}
	if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Summary) == "" {
		add("title and summary are required")
	}
	oneOf("category", p.Category, Categories)
	each("starts_from", p.StartsFrom, StartsFrom)
	oneOf("stance", p.Stance, StanceIDs())
	each("delivery_promises", p.DeliveryPromises, DeliveryPromises)
	each("renderer_families", p.RendererFamilies, RendererFamilies)
	each("runtimes", p.Runtimes, Runtimes)
	each("modes", p.Modes, Modes)
	each("styles.recommended", p.Styles.Recommended, StyleIDs())
	each("styles.also", p.Styles.Also, StyleIDs())
	if len(p.Runtimes) == 0 || len(p.Styles.Recommended) == 0 {
		add("runtimes and styles.recommended must not be empty")
	}
	if len(p.Structures) == 0 {
		add("at least one structure is required")
	}
	for _, s := range p.Structures {
		total := 0.0
		for _, b := range s.Beats {
			total += b.Share
			if b.Share <= 0 || strings.TrimSpace(b.ID) == "" || strings.TrimSpace(b.Purpose) == "" {
				add("structure %s: every beat needs an id, a purpose and a positive share", s.ID)
			}
			each("structure "+s.ID+" beat "+b.ID+" scene", b.Scenes, SceneTypes)
		}
		if math.Abs(total-1) > 0.02 {
			add("structure %s: beat shares add up to %.2f, not 1.0", s.ID, total)
		}
	}
	seen := map[string]int{}
	last := -1
	for _, st := range p.Stages {
		oneOf("stage", st.ID, StageIDs)
		oneOf("stage "+st.ID+" role", st.Role, Roles)
		each("stage "+st.ID+" needs", st.Needs, Capabilities)
		each("stage "+st.ID+" produces", st.Produces, ArtifactNames())
		each("stage "+st.ID+" consumes", st.Consumes, ArtifactNames())
		if seen[st.ID] > 0 {
			add("stage %s appears twice", st.ID)
		}
		seen[st.ID]++
		order := index(StageIDs, st.ID)
		if order < last {
			add("stage %s is out of order", st.ID)
		}
		last = order
	}
	if len(p.Stages) == 0 {
		add("at least one stage is required")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// StageGuide returns the shared guide for a stage.
func StageGuide(stage string) (string, error) {
	if !contains(StageIDs, stage) {
		return "", fmt.Errorf("no stage %q; stages are %s", stage, strings.Join(StageIDs, ", "))
	}
	return facet.Guidance(path.Join("guidance/stages", stage+".md"))
}

// StanceGuide returns a producer stance.
func StanceGuide(stance string) (string, error) {
	if !contains(StanceIDs(), stance) {
		return "", fmt.Errorf("no stance %q; stances are %s", stance, strings.Join(StanceIDs(), ", "))
	}
	return facet.Guidance(path.Join("guidance/stances", stance+".md"))
}

func contains(values []string, value string) bool { return index(values, value) >= 0 }

func index(values []string, value string) int {
	for i, v := range values {
		if v == value {
			return i
		}
	}
	return -1
}
