package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kayiik/facet/internal/catalog"
	"github.com/kayiik/facet/internal/planning"
	"github.com/kayiik/facet/internal/toolbox"
)

// toolDef is one MCP tool and the operation behind it.
type toolDef struct {
	tool *mcp.Tool
	call operation
}

// tools lists every MCP tool the server offers: one per Facet tool, named
// exactly as the registry names it, then the read-only planning tools.
func (s *server) tools() []toolDef {
	var defs []toolDef
	for _, name := range toolbox.Names() {
		defs = append(defs, toolDef{facetTool(name), s.runFacetTool(name)})
	}
	readOnly := toolbox.Effects{ReadOnly: true}
	names := toolbox.Names()
	plan := []struct {
		name, description string
		schema            map[string]any
		call              operation
	}{
		{"tools_list", "List every Facet tool with its capability, readiness (dependencies and how far each is " +
			"resolved), cost and effects. The same listing as `facet tools list`.",
			objectSchema(map[string]any{}), s.toolsList},
		{"describe", "Describe one Facet tool: request schema, result schema, provider, dependencies, cost " +
			"and effects. Read it before building a request; field names differ between tools.",
			objectSchema(map[string]any{
				"tool": map[string]any{"type": "string", "enum": names, "description": "A Facet tool name, as listed by tools_list."},
			}, "tool"), s.describe},
		{"estimate", "Validate a request for a Facet tool and report its estimated cost and effects without " +
			"running it: nothing is written and nothing is billed. An estimate does not prove credentials, " +
			"availability or success.",
			objectSchema(map[string]any{
				"tool":      map[string]any{"type": "string", "enum": names, "description": "The Facet tool to estimate."},
				"arguments": map[string]any{"type": "object", "description": "The request exactly as the tool would receive it. Paths follow the same root policy as a run."},
			}, "tool"), s.estimate},
		{"capabilities", "The preflight menu: every capability (voice, stock and generated images and video, music, " +
			"captions, composition, editing, review, ...) with its tools listed free first, how many are configured " +
			"on this machine, what each is missing, and the credit note of each paid tool; plus which composition " +
			"runtimes (Remotion, HyperFrames, FFmpeg) are installed. Run it before planning a production.",
			objectSchema(map[string]any{}), s.capabilities},
		{"pipelines_list", "List Facet's production pipelines (OpenMontage's production methods): what each makes, " +
			"what it starts from, its default producer stance, runtimes, composition modes and styles. Every " +
			"production goes through one pipeline.",
			objectSchema(map[string]any{}), s.pipelinesList},
		{"pipeline_describe", "Describe one pipeline: its stages in order (role, records consumed and produced, " +
			"approval point, capabilities needed, review focus, success criteria, notes), its story structures, " +
			"its producer stance guide, and live availability of the capabilities it needs. Pass a stage to also " +
			"get that stage's guide; call it before working on each stage.",
			objectSchema(map[string]any{
				"name":  map[string]any{"type": "string", "enum": catalog.Names(), "description": "A pipeline name, as listed by pipelines_list."},
				"stage": map[string]any{"type": "string", "enum": catalog.StageIDs, "description": "Optional: a stage id to get its guide and the pipeline's notes for it."},
			}, "name"), s.pipelineDescribe},
		{"guidance", "Read Facet's bundled production knowledge by path: the guide (skills/facet/SKILL.md), " +
			"role agents (agents/), pipelines/, stage guides (guidance/stages/), stances (guidance/stances/), craft " +
			"(guidance/craft/), runtimes and the scene reference (guidance/runtimes/), vendor knowledge " +
			"(guidance/vendor/), styles/ and record schemas (schemas/artifacts/). A folder path lists its files.",
			objectSchema(map[string]any{
				"path": map[string]any{"type": "string", "description": "A bundled file or folder, for example guidance/runtimes/scene-types.md or guidance/craft."},
			}), s.guidance},
	}
	for _, p := range plan {
		defs = append(defs, toolDef{&mcp.Tool{
			Name:        p.name,
			Description: describeTool(p.description, "", readOnly),
			InputSchema: p.schema,
			Annotations: annotations(readOnly),
			Meta:        mcp.Meta{"facet": facetMeta(readOnly, true, true)},
		}, p.call})
	}
	return defs
}

// facetTool declares one registry tool with its effects.
func facetTool(name string) *mcp.Tool {
	effects := toolbox.EffectsFor(name)
	facts := describedFacts()[name]
	schema, note := inputSchema(name)
	return &mcp.Tool{
		Name:        name,
		Description: describeTool(toolbox.Description(name), note, effects),
		InputSchema: schema,
		Annotations: annotations(effects),
		Meta:        mcp.Meta{"facet": facetMeta(effects, facts.costKnown, facts.hasCost)},
	}
}

// staticFacts are the parts of `describe` that are fixed for a build: the
// provider and whether the cost is known.
type staticFacts struct {
	provider  string
	costKnown bool
	hasCost   bool // describe states cost.known
}

// describedFacts reads staticFacts for every tool once per process. Describe
// also resolves every dependency on disk, which is slow, so the tools are
// described in parallel.
var describedFacts = sync.OnceValue(func() map[string]staticFacts {
	names := toolbox.Names()
	facts := make([]staticFacts, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			description, ok := toolbox.Describe(name)
			if !ok {
				return
			}
			facts[i].provider, _ = description["provider"].(string)
			cost, _ := description["cost"].(map[string]any)
			facts[i].costKnown, facts[i].hasCost = cost["known"].(bool)
		}()
	}
	wg.Wait()
	out := make(map[string]staticFacts, len(names))
	for i, name := range names {
		out[name] = facts[i]
	}
	return out
})

// annotations maps declared effects onto MCP's hints. Every tool that writes
// files may replace an existing file at its output path (most do so whenever
// the path exists, the rest when asked to overwrite), which MCP counts as a
// destructive update; only read-only tools are non-destructive. Harnesses act
// on these hints for servers they trust, so they must not understate effects.
func annotations(e toolbox.Effects) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    e.ReadOnly,
		IdempotentHint:  e.Deterministic,
		OpenWorldHint:   boolPtr(e.Network),
		DestructiveHint: boolPtr(!e.ReadOnly),
	}
}

// facetMeta is the _meta.facet declaration: Facet's own effect vocabulary.
func facetMeta(e toolbox.Effects, costKnown, hasCost bool) map[string]any {
	meta := map[string]any{
		"may_charge":     e.MayCharge,
		"network":        e.Network,
		"external_write": e.ExternalWrite,
		"deterministic":  e.Deterministic,
		"read_only":      e.ReadOnly,
	}
	if hasCost {
		meta["cost_known"] = costKnown
	}
	return meta
}

func describeTool(capability, note string, e toolbox.Effects) string {
	text := strings.TrimSpace(capability)
	if text != "" && !strings.ContainsAny(text[len(text)-1:], ".!?") {
		text += "."
	}
	if note != "" {
		text += " " + note
	}
	return fmt.Sprintf("%s\nEffects: may_charge=%t, network=%t, external_write=%t, deterministic=%t, read_only=%t.",
		text, e.MayCharge, e.Network, e.ExternalWrite, e.Deterministic, e.ReadOnly)
}

func boolPtr(b bool) *bool { return &b }

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// topLevelCombinators are keywords several model APIs refuse at the top of a
// tool's input schema. A client forwards every tool of a server together, so
// one such schema can make the whole server unusable for it.
var topLevelCombinators = []string{"anyOf", "oneOf", "allOf", "not", "if", "then", "else"}

// inputSchema returns a tool's request schema as an MCP input schema: an
// object schema without top-level combinators. A constraint that had to be
// removed is described in the returned note instead; the tool still enforces
// it, and `describe` returns the schema unchanged.
func inputSchema(tool string) (map[string]any, string) {
	schema := map[string]any{}
	if data, err := json.Marshal(toolbox.Parameters(tool)); err == nil {
		_ = json.Unmarshal(data, &schema) // a private copy: the registry's map is shared
	}
	schema["type"] = "object"
	if _, ok := schema["properties"].(map[string]any); !ok {
		schema["properties"] = map[string]any{}
	}
	var notes []string
	generic := false
	for _, keyword := range topLevelCombinators {
		value, present := schema[keyword]
		if !present {
			continue
		}
		delete(schema, keyword)
		if note := requiredAlternatives(keyword, value); note != "" {
			notes = append(notes, note)
		} else {
			generic = true
		}
	}
	if generic {
		notes = append(notes, "Some combinations of fields are validated by the tool; `describe` shows the full request schema.")
	}
	return schema, strings.Join(notes, " ")
}

// requiredAlternatives phrases anyOf/oneOf branches that only require fields,
// such as media_probe's input or input_path.
func requiredAlternatives(keyword string, value any) string {
	branches, ok := value.([]any)
	if !ok || len(branches) == 0 || (keyword != "anyOf" && keyword != "oneOf") {
		return ""
	}
	var options []string
	for _, branch := range branches {
		object, ok := branch.(map[string]any)
		if !ok || len(object) != 1 {
			return ""
		}
		required, ok := object["required"].([]any)
		if !ok || len(required) == 0 {
			return ""
		}
		var fields []string
		for _, field := range required {
			name, ok := field.(string)
			if !ok {
				return ""
			}
			fields = append(fields, name)
		}
		options = append(options, strings.Join(fields, " + "))
	}
	if keyword == "oneOf" {
		return "Give exactly one of: " + strings.Join(options, " | ") + "."
	}
	return "Give at least one of: " + strings.Join(options, " | ") + "."
}

// runFacetTool runs one registry tool with the request's context, so a
// client's cancellation, or its hanging up, kills the run.
//
// A tool that may write runs with the allowed root as its working folder, so
// a default output (a file the tool names itself when the request names none)
// lands inside the root like every path the request names, never in the
// server's own working directory.
func (s *server) runFacetTool(name string) operation {
	return func(ctx context.Context, req *mcp.CallToolRequest) outcome {
		root := s.lazyRoot(ctx, req.Session)
		data, refused := s.facetRequest(root, name, "run", req.Params.Arguments)
		if refused != nil {
			return *refused
		}
		if !toolbox.EffectsFor(name).ReadOnly {
			dir, err := root()
			if err != nil {
				return failure(name, "run", "root_unavailable",
					"the allowed root for this tool's outputs is unavailable: "+err.Error()+
						" (facet mcp --root DIR sets the root explicitly)", nil)
			}
			ctx = toolbox.WithWorkDir(ctx, dir)
		}
		envelope := s.run(ctx, name, data)
		return outcome{envelope, envelope.OK}
	}
}

func (s *server) toolsList(_ context.Context, req *mcp.CallToolRequest) outcome {
	var in struct{}
	if err := strictDecode(req.Params.Arguments, &in); err != nil {
		return failure("", "list", "invalid_request", argumentsError(err), nil)
	}
	envelope, ok := toolbox.CLI([]string{"tools", "list"})
	return outcome{envelope, ok}
}

func (s *server) describe(_ context.Context, req *mcp.CallToolRequest) outcome {
	var in struct {
		Tool string `json:"tool"`
	}
	if err := strictDecode(req.Params.Arguments, &in); err != nil {
		return failure("", "describe", "invalid_request", argumentsError(err), nil)
	}
	tool, ok := facetToolName(in.Tool)
	if !ok {
		return unknownTool(in.Tool, "describe")
	}
	envelope, ok := toolbox.CLI([]string{"tools", "describe", tool})
	return outcome{envelope, ok}
}

func (s *server) estimate(ctx context.Context, req *mcp.CallToolRequest) outcome {
	var in struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := strictDecode(req.Params.Arguments, &in); err != nil {
		return failure("", "estimate", "invalid_request", argumentsError(err), nil)
	}
	tool, ok := facetToolName(in.Tool)
	if !ok {
		return unknownTool(in.Tool, "estimate")
	}
	data, refused := s.facetRequest(s.lazyRoot(ctx, req.Session), tool, "estimate", in.Arguments)
	if refused != nil {
		return *refused
	}
	envelope := toolbox.EstimateContext(ctx, tool, data)
	return outcome{envelope, envelope.OK}
}

// facetToolName resolves a name, including the CLI's aliases, to a registry
// tool.
func facetToolName(name string) (string, bool) {
	tool := toolbox.CanonicalName(name)
	for _, known := range toolbox.Names() {
		if known == tool {
			return tool, true
		}
	}
	return tool, false
}

func unknownTool(name, op string) outcome {
	if strings.TrimSpace(name) == "" {
		return failure("", op, "invalid_request", `"tool" must name a Facet tool; tools_list lists them`, nil)
	}
	return failure("", op, "unknown_tool", "unknown tool: "+name+"; tools_list lists Facet's tools", map[string]any{"tool": name})
}

func (s *server) capabilities(_ context.Context, req *mcp.CallToolRequest) outcome {
	var in struct{}
	if err := strictDecode(req.Params.Arguments, &in); err != nil {
		return planningFailure("capabilities", argumentsError(err))
	}
	envelope := planning.Capabilities()
	return outcome{envelope, envelope.OK}
}

func (s *server) pipelinesList(_ context.Context, req *mcp.CallToolRequest) outcome {
	var in struct{}
	if err := strictDecode(req.Params.Arguments, &in); err != nil {
		return planningFailure("pipelines_list", argumentsError(err))
	}
	envelope := planning.PipelinesList()
	return outcome{envelope, envelope.OK}
}

func (s *server) pipelineDescribe(_ context.Context, req *mcp.CallToolRequest) outcome {
	var in struct {
		Name  string `json:"name"`
		Stage string `json:"stage"`
	}
	if err := strictDecode(req.Params.Arguments, &in); err != nil {
		return planningFailure("pipeline_describe", argumentsError(err))
	}
	envelope := planning.PipelineDescribe(in.Name, in.Stage)
	return outcome{envelope, envelope.OK}
}

func (s *server) guidance(_ context.Context, req *mcp.CallToolRequest) outcome {
	var in struct {
		Path string `json:"path"`
	}
	if err := strictDecode(req.Params.Arguments, &in); err != nil {
		return planningFailure("guidance", argumentsError(err))
	}
	envelope := planning.Guidance(in.Path)
	return outcome{envelope, envelope.OK}
}

// planningFailure is a planning envelope for a request refused before it
// reached the planning package.
func planningFailure(op, message string) outcome {
	return outcome{envelope: planning.Envelope{
		OK: false, Operation: op,
		Error:    &planning.Error{Code: "invalid_request", Message: message},
		Warnings: []string{},
	}}
}
