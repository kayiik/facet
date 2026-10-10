// Package planning serves Facet's read-only planning surface: the capability
// menu, the pipelines and the bundled guidance. The CLI (`facet capabilities`,
// `facet pipelines`, `facet guidance`) and the MCP planning tools share it.
package planning

import (
	"encoding/json"
	"strings"

	facet "github.com/kayiik/facet"
	"github.com/kayiik/facet/internal/catalog"
	"github.com/kayiik/facet/internal/components"
	"github.com/kayiik/facet/internal/toolbox"
)

// Envelope is the JSON every planning command prints.
type Envelope struct {
	OK        bool     `json:"ok"`
	Operation string   `json:"operation"`
	Result    any      `json:"result,omitempty"`
	Error     *Error   `json:"error,omitempty"`
	Warnings  []string `json:"warnings"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ok(op string, result any) Envelope {
	return Envelope{OK: true, Operation: op, Result: result, Warnings: []string{}}
}

func fail(op, code, message string) Envelope {
	return Envelope{OK: false, Operation: op, Error: &Error{Code: code, Message: message}, Warnings: []string{}}
}

// Capabilities is the preflight menu: every capability with its tools, free
// first, and whether each is ready; plus the composition runtimes installed.
// A tool that is not ready names the `facet install` command that readies it,
// when Facet can install what it lacks.
func Capabilities() Envelope {
	reports := withInstall(toolbox.Capabilities(catalog.Capabilities))
	configured := map[string]bool{}
	for _, r := range reports {
		for _, t := range r.Tools {
			if t.Configured {
				configured[t.Tool] = true
			}
		}
	}
	return ok("capabilities", map[string]any{
		"capabilities": reports,
		"composition_runtimes": map[string]bool{
			"remotion":    configured["video_compose"],
			"hyperframes": configured["hyperframes_compose"],
			"ffmpeg":      configured["source_edit"],
		},
	})
}

// PipelinesList lists every pipeline with what it is for.
func PipelinesList() Envelope {
	all, err := catalog.All()
	if err != nil {
		return fail("pipelines_list", "catalog_invalid", err.Error())
	}
	items := make([]map[string]any, 0, len(all))
	for _, p := range all {
		items = append(items, map[string]any{
			"name": p.Name, "title": p.Title, "summary": p.Summary, "category": p.Category,
			"starts_from": p.StartsFrom, "stance": p.Stance, "runtimes": p.Runtimes,
			"modes": p.Modes, "styles": p.Styles, "reference_input": p.ReferenceInput,
		})
	}
	return ok("pipelines_list", map[string]any{"pipelines": items})
}

// PipelineDescribe returns a whole pipeline, its producer stance, and the
// live availability of every capability its stages need. With a stage it
// returns what that stage needs and nothing it repeats: the pipeline without
// its other stages, the stage's guide and the pipeline's notes for it, and the
// availability of the capabilities the stage needs. An agent calls it before
// every stage, so the whole pipeline and the stance guide (read at intake) are
// not sent again each time; the stance guide stays one guidance call away.
func PipelineDescribe(name, stage string) Envelope {
	name = strings.TrimSpace(name)
	if name == "" {
		return fail("pipeline_describe", "invalid_request", `"name" must name a pipeline; pipelines_list lists them`)
	}
	p, err := catalog.Load(name)
	if err != nil {
		return fail("pipeline_describe", "unknown_pipeline", err.Error())
	}
	if stage = strings.TrimSpace(stage); stage == "" {
		stance, _ := catalog.StanceGuide(p.Stance)
		needs := map[string]bool{}
		for _, st := range p.Stages {
			for _, c := range st.Needs {
				needs[c] = true
			}
		}
		return ok("pipeline_describe", map[string]any{
			"pipeline":     p,
			"stance_guide": stance,
			"availability": availability(needs),
		})
	}
	st, found := p.Stage(stage)
	if !found {
		ids := make([]string, 0, len(p.Stages))
		for _, s := range p.Stages {
			ids = append(ids, s.ID)
		}
		return fail("pipeline_describe", "unknown_stage", "pipeline "+name+" has no stage "+stage+"; its stages are "+strings.Join(ids, ", "))
	}
	guide, err := catalog.StageGuide(stage)
	if err != nil {
		return fail("pipeline_describe", "guidance_missing", err.Error())
	}
	stageNeeds := map[string]bool{}
	for _, c := range st.Needs {
		stageNeeds[c] = true
	}
	var header map[string]any
	data, err := json.Marshal(p)
	if err == nil {
		err = json.Unmarshal(data, &header)
	}
	if err != nil {
		return fail("pipeline_describe", "invalid_pipeline", err.Error())
	}
	delete(header, "stages")
	return ok("pipeline_describe", map[string]any{
		"pipeline":          header,
		"stance_guide_path": "guidance/stances/" + p.Stance + ".md",
		"stage": map[string]any{
			"stage":        st,
			"guide":        guide,
			"availability": availability(stageNeeds),
		},
	})
}

func availability(needs map[string]bool) []toolbox.CapabilityReport {
	var order []string
	for _, c := range catalog.Capabilities {
		if needs[c] {
			order = append(order, c)
		}
	}
	return withInstall(toolbox.Capabilities(order))
}

// withInstall names, for each tool that is not ready, the command that
// installs the components it lacks.
func withInstall(reports []toolbox.CapabilityReport) []toolbox.CapabilityReport {
	for i := range reports {
		for j := range reports[i].Tools {
			tool := &reports[i].Tools[j]
			if tool.Configured {
				continue
			}
			var ids []string
			for _, missing := range tool.Missing {
				if id := components.ForDependency(missing); id != "" {
					ids = append(ids, id)
				}
			}
			if ids = components.Minimal(ids); len(ids) > 0 {
				tool.Install = "facet install " + strings.Join(ids, " ")
			}
		}
	}
	return reports
}

// Guidance returns one bundled file, or the file list under a folder such as
// "guidance/craft".
func Guidance(path string) Envelope {
	path = strings.Trim(strings.TrimSpace(strings.ReplaceAll(path, "\\", "/")), "/")
	if path == "" {
		return ok("guidance", map[string]any{"roots": facet.GuidanceRoots, "files": facet.GuidanceFiles("skills"), "hint": "pass a folder such as guidance/craft to list it, or a file path to read it"})
	}
	if body, err := facet.Guidance(path); err == nil {
		return ok("guidance", map[string]any{"path": path, "content": body})
	}
	if files := facet.GuidanceFiles(path); len(files) > 0 {
		return ok("guidance", map[string]any{"path": path, "files": files})
	}
	return fail("guidance", "unknown_guidance", "no bundled guidance at "+path+"; folders are "+strings.Join(facet.GuidanceRoots, ", "))
}

const usage = `usage:
  facet capabilities
  facet pipelines list
  facet pipelines describe <name> [--stage <stage>]
  facet guidance [<path>]`

// CLI runs `facet capabilities`, `facet pipelines …` or `facet guidance …`.
// args starts with the command name.
func CLI(args []string) (Envelope, bool) {
	if len(args) == 0 {
		return fail("", "invalid_request", usage), false
	}
	var env Envelope
	switch args[0] {
	case "capabilities":
		if len(args) != 1 {
			return fail("capabilities", "invalid_request", usage), false
		}
		env = Capabilities()
	case "guidance":
		if len(args) > 2 {
			return fail("guidance", "invalid_request", usage), false
		}
		path := ""
		if len(args) == 2 {
			path = args[1]
		}
		env = Guidance(path)
	case "pipelines":
		switch {
		case len(args) == 2 && args[1] == "list":
			env = PipelinesList()
		case len(args) >= 3 && args[1] == "describe":
			stage := ""
			rest := args[3:]
			if len(rest) == 2 && rest[0] == "--stage" {
				stage = rest[1]
			} else if len(rest) != 0 {
				return fail("pipeline_describe", "invalid_request", usage), false
			}
			env = PipelineDescribe(args[2], stage)
		default:
			return fail("pipelines", "invalid_request", usage), false
		}
	default:
		return fail("", "invalid_request", usage), false
	}
	return env, env.OK
}
