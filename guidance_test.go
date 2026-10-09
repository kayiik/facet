package facet_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	facet "github.com/kayiik/facet"
	"github.com/kayiik/facet/internal/toolbox"
)

// toolLike matches a backticked name that reads like a tool: snake_case with a
// tool-shaped ending.
var toolLike = regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)*_(?:tts|image|video|compose|check|review|mix|mixer|gen|burn|probe|sample|sampler|detect|cutter|grade|edit|trimmer|stitch|search|library|selector|qa|transcribe|capture|record|recorder|export|music|sfx|enhance|reframe|energy|avatar))`")

// retired names an agent might be tempted to call because OpenMontage has
// them; Facet's guidance must not name them as tools.
var retired = []string{"transcriber", "tts_selector", "audio_mixer", "frame_sampler", "export_bundle", "remotion_caption_burn", "web_capture", "screen_record", "audio_energy", "audio_enhance", "auto_reframe", "music_gen"}

var planningTools = map[string]bool{
	"tools_list": true, "describe": true, "estimate": true, "capabilities": true,
	"pipelines_list": true, "pipeline_describe": true, "guidance": true,
}

func TestGuidanceNamesOnlyRealTools(t *testing.T) {
	known := map[string]bool{}
	for _, name := range toolbox.Names() {
		known[name] = true
	}
	for name := range planningTools {
		known[name] = true
	}
	// Record names (final_review, source_media_review), a video_stitch
	// operation and a request field share the tool-like shape.
	for _, file := range facet.GuidanceFiles("schemas/artifacts") {
		known[strings.TrimSuffix(strings.TrimPrefix(file, "schemas/artifacts/"), ".schema.json")] = true
	}
	for _, term := range []string{"preview_stitch", "reference_image"} {
		known[term] = true
	}
	problems := map[string][]string{}
	for _, file := range facet.GuidanceFiles("") {
		if strings.HasPrefix(file, "guidance/vendor/") || strings.HasPrefix(file, "schemas/") {
			continue // third-party knowledge and record schemas name their own fields
		}
		body, err := facet.Guidance(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range toolLike.FindAllStringSubmatch(body, -1) {
			if !known[m[1]] {
				problems[m[1]] = append(problems[m[1]], file)
			}
		}
		for _, name := range retired {
			if strings.Contains(body, "`"+name+"`") {
				problems[name] = append(problems[name], file)
			}
		}
	}
	if len(problems) > 0 {
		var lines []string
		for name, files := range problems {
			sort.Strings(files)
			lines = append(lines, name+": "+strings.Join(dedupe(files), ", "))
		}
		sort.Strings(lines)
		t.Fatalf("guidance names tools Facet does not have:\n%s", strings.Join(lines, "\n"))
	}
}

func TestToolKnowledgePointersExist(t *testing.T) {
	for _, name := range toolbox.Names() {
		for _, path := range toolbox.ToolMeta(name).Knowledge {
			if _, err := facet.Guidance(path); err != nil {
				t.Errorf("%s points to %s: %v", name, path, err)
			}
		}
	}
}

func dedupe(values []string) []string {
	var out []string
	for i, v := range values {
		if i == 0 || v != values[i-1] {
			out = append(out, v)
		}
	}
	return out
}
