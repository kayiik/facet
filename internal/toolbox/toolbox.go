package toolbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kayiik/facet/internal/proctree"
)

const maxDiagnostic = 8192

var names = []string{
	"audio_mix",
	"audio_probe",
	"color_grade",
	"direct_clip_search",
	"edge_tts",
	"elevenlabs_tts",
	"ffmpeg_caption_burn",
	"flux_image",
	"frame_sample",
	"gflow_image",
	"gflow_video",
	"hyperframes_compose",
	"image_selector",
	"kling_video",
	"media_probe",
	"music_library",
	"openai_image",
	"openai_tts",
	"output_review",
	"pexels_video",
	"piper_tts",
	"pixabay_video",
	"plan_check",
	"scene_detect",
	"script_check",
	"silence_cutter",
	"sora_video",
	"source_edit",
	"subtitle_gen",
	"video_compose",
	"video_selector",
	"video_stitch",
	"video_trimmer",
	"visual_qa",
	"wikimedia",
}

type Execution struct {
	Provider      string   `json:"provider"`
	Network       bool     `json:"network"`
	ExternalWrite bool     `json:"external_write"`
	EstimatedCost *float64 `json:"estimated_cost"`
	ActualCost    *float64 `json:"actual_cost"`
}

type Envelope struct {
	OK        bool       `json:"ok"`
	Tool      string     `json:"tool,omitempty"`
	Operation string     `json:"operation"`
	Result    any        `json:"result,omitempty"`
	Error     *ToolError `json:"error,omitempty"`
	Warnings  []string   `json:"warnings"`
	Execution Execution  `json:"execution"`
	// Artifacts describes every file a successful run produced. It is
	// envelope-level provenance, derived centrally from the result, so a tool's
	// result schema never has to carry it.
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

type ToolError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

type toolFailure struct{ err *ToolError }

func (e *toolFailure) Error() string { return e.err.Message }

func failure(code, message string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	return &toolFailure{&ToolError{
		Code: code, Message: message, Retryable: retryableCode(code), Details: details,
	}}
}

// retryableCode says whether the SAME request could succeed if tried again.
//
// ToolError.Retryable was declared and never set anywhere, so every error told
// the host "do not retry" — including a timeout, which is the one failure a
// larger budget reliably fixes. A host that honours the flag would give up on
// a render that needed nothing but more time.
//
// Decided by code rather than per call site: there are 116 command_failed
// constructions alone, and a judgement repeated at every one of them is a
// judgement that drifts.
//
// Retryable means TRANSIENT, not "worth a second attempt by a human":
//   - a timeout may fit in a larger budget
//   - a provider returning something unusable is usually a transient upstream
//     fault rather than a wrong request
//
// Everything else is deliberately false. A malformed request, a missing
// credential, an absent dependency and an input that does not exist all fail
// identically on a retry, and telling a host otherwise invites a loop that
// burns time and, for a paid provider, money.
func retryableCode(code string) bool {
	switch code {
	case "command_timeout", "provider_response_invalid":
		return true
	}
	return false
}

func executionFor(tool string) Execution {
	provider := "local"
	network := false
	switch tool {
	case "media_probe", "audio_probe":
		provider = "ffprobe"
	case "color_grade":
		provider = "ffmpeg"
	case "direct_clip_search":
		// The tool contacts whichever configured stock services can satisfy
		// the request, so no single provider name is accurate.
		provider = "multi_stock"
		network = true
	case "edge_tts":
		provider = "microsoft_edge"
		network = true
	case "elevenlabs_tts":
		provider = "elevenlabs"
		network = true
	case "flux_image":
		provider = "fal"
		network = true
	case "image_selector":
		provider = "selector"
	case "kling_video":
		provider = "kling"
		network = true
	case "openai_image":
		provider = "openai"
		network = true
	case "openai_tts":
		provider = "openai"
		network = true
	case "pexels_video":
		provider = "pexels"
		network = true
	case "pixabay_video":
		provider = "pixabay"
		network = true
	case "piper_tts":
		provider = "piper"
	case "ffmpeg_caption_burn":
		provider = "ffmpeg"
	case "sora_video":
		provider = "openai"
		network = true
	case "subtitle_gen":
		provider = "local"
	case "video_selector":
		provider = "selector"
	case "wikimedia":
		provider = "wikimedia"
		network = true
	case "hyperframes_compose":
		// The scaffolded composition loads GSAP from a CDN, the headless
		// renderer fetches whatever the HTML references, and add_block pulls
		// from the HyperFrames registry. The tool reaches the network even
		// though it runs a local binary.
		provider = "hyperframes"
		network = true
	case "gflow_video", "gflow_image":
		provider = "google_flow"
		network = true
	}
	zero := 0.0
	e := Execution{Provider: provider, Network: network, EstimatedCost: &zero, ActualCost: &zero}
	if MayCharge(tool) {
		// A chargeable Operation's price is not knowable before it runs, so
		// the amount is null rather than zero. That is cost_known=false, a
		// SEPARATE fact from chargeability — see MayCharge.
		e.EstimatedCost, e.ActualCost = nil, nil
	}
	return e
}

// chargeableTools is the single source of truth for which Operations can
// result in a monetary charge to the operator.
//
// Chargeability is a per-Operation semantic effect, independent of whether an
// amount is known. Every surface — the listing, describe, the envelope and
// EffectsFor — derives it from this set rather than restating it.
var chargeableTools = map[string]bool{
	"gflow_video":    true,
	"gflow_image":    true,
	"openai_image":   true,
	"flux_image":     true,
	"kling_video":    true,
	"sora_video":     true,
	"openai_tts":     true,
	"elevenlabs_tts": true,
}

// MayCharge reports whether invoking this Operation may result in a monetary
// charge to the operator.
//
// Published by `facet tools list`, `describe`, and EffectsFor, which adapters
// such as the MCP server map onto their own effect declarations. Facet only
// declares it; whether a charge needs consent is the caller's policy.
//
// Independent of cost_known, which answers a different question: whether a
// numeric amount is known. Both combinations are legal and both occur here —
// every chargeable Operation has an unknown amount (may_charge=true,
// cost_known=false), and edge_tts reaches the network with a known amount of
// zero (may_charge=false, cost_known=true).
//
// Approval policy must key on THIS, never on cost_known. Verified before this
// existed: edge_tts declared cost_known=true and ran without consent while
// reaching an external service.
func MayCharge(tool string) bool { return chargeableTools[tool] }

// Deterministic reports whether the same request against the same inputs
// produces byte-identical output.
//
// Operator ruling 5 derives recoverability from this: re-execution is free
// recovery for a deterministic Operation, so durable resume is not required.
// It is therefore a semantic guarantee, not a performance note, and must only
// be claimed where it genuinely holds.
//
// MEASURED, not assumed. Each of these was run twice and its output digests
// compared:
//
//	video_trimmer   158fb763...  identical
//	color_grade     cd0ac7fb...  identical
//	subtitle_gen    658d8f1f...  identical
//	video_compose   44b52bdd...  identical (Remotion render)
//
// A networked Operation is NEVER declared deterministic: remote state can
// change between runs regardless of the request. A chargeable one never is
// either — generative providers sample.
//
// Conservative by design. An Operation absent from this set is not asserted
// to be non-deterministic; it is simply unproven, and claiming a guarantee
// nobody measured is how the false documentation in this repo got written.
var deterministicTools = map[string]bool{
	"video_trimmer": true,
	"color_grade":   true,
	"subtitle_gen":  true,
	"video_compose": true,
	"media_probe":   true,
	"audio_probe":   true,
	"scene_detect":  true,
	"frame_sample":  true,
	"script_check":  true,
}

// Deterministic reports whether this Operation is a proven deterministic
// function of its request and inputs.
//
// Published by `facet tools list`, `describe`, and EffectsFor.
func Deterministic(tool string) bool {
	if executionFor(tool).Network || MayCharge(tool) {
		// Defence in depth: a networked or chargeable Operation cannot be
		// deterministic whatever the table says, so the table cannot make one
		// so by mistake.
		return false
	}
	return deterministicTools[tool]
}

// ChargeableTools lists every Operation that may result in a monetary charge.
//
// Returned as a copy so a caller cannot mutate the source of truth, and
// exported so conformance tests can assert over the whole set rather than
// spot-checking names they happen to remember.
func ChargeableTools() []string {
	out := make([]string, 0, len(chargeableTools))
	for tool := range chargeableTools {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

// readOnlyTools only read their inputs and report facts: a run never creates,
// replaces or deletes a file and never changes remote state. Everything else
// may write, and an unrecognised tool is assumed to write.
//
// This exists because Execution.ExternalWrite was declared and never assigned,
// so every tool — including ones that demonstrably write media — reported
// external_write=false. A caller uses declared effects to decide whether
// approval is required, so a constant false routes write-performing tools
// around approval. It fails OPEN, which is why the unknown case here is true.
//
// The classification is per tool, not per request: visual_qa's probe
// operation writes nothing, but its review operation writes frames, so the
// tool as a whole is not read-only.
var readOnlyTools = map[string]bool{
	"media_probe": true, "audio_probe": true, "music_library": true,
	"image_selector": true, "video_selector": true,
	"script_check": true, "plan_check": true,
}

// externalWriteFor reports whether an operation can write outside the process.
// Estimation validates a request and never writes output, by contract.
func externalWriteFor(tool, op string) bool {
	if op != "run" {
		return false
	}
	return !readOnlyTools[tool]
}

// canonicalToolName normalises case and surrounding space. Every tool has one
// name; there are no aliases.
func canonicalToolName(tool string) string {
	return strings.ToLower(strings.TrimSpace(tool))
}

// CLI implements `facet tools`. args starts with "tools". It returns the
// envelope to print and whether the command succeeded.
//
// Help (`facet tools --help`, `facet tools help [operation]`, and `--help` or
// `-h` anywhere after an operation) succeeds with a usage payload, so the
// caller prints it like any other envelope and exits zero.
func CLI(args []string) (Envelope, bool) {
	return CLIWithProgress(args, nil)
}

// CLIWithProgress is CLI with a receiver for the milestones a run reports
// before its envelope, such as the id of a provider job it submitted. The
// facet command prints them to stderr, so stdout stays one envelope.
func CLIWithProgress(args []string, progress func(Progress)) (Envelope, bool) {
	op, tool := "", ""
	bad := func(message string) (Envelope, bool) {
		return errorEnvelope(tool, op, failure("invalid_request", message, map[string]any{"usage": toolsUsage})), false
	}
	if len(args) == 0 || args[0] != "tools" {
		return bad("usage: facet tools <list|describe|estimate|run>")
	}
	if help, ok := cliHelp(args[1:]); ok {
		return success("", "help", help, nil), true
	}
	if len(args) < 2 {
		return bad("usage: facet tools <list|describe|estimate|run>")
	}
	op = args[1]
	switch op {
	case "list":
		if len(args) != 2 {
			return bad("tools list accepts no arguments")
		}
		items := make([]any, 0, len(names))
		for _, n := range names {
			items = append(items, summary(n))
		}
		return success("", op, map[string]any{"tools": items}, nil), true
	case "describe":
		if len(args) != 3 {
			return bad("usage: facet tools describe <tool>")
		}
		tool = canonicalToolName(args[2])
		if !known(tool) {
			return bad("unknown tool: " + args[2])
		}
		return success(tool, op, description(tool), nil), true
	case "estimate", "run":
		if len(args) != 5 || args[3] != "--input" {
			return bad("usage: facet tools " + op + " <tool> --input <request.json>")
		}
		tool = canonicalToolName(args[2])
		if !known(tool) {
			return bad("unknown tool: " + args[2])
		}
		var data []byte
		rawInput := strings.TrimSpace(args[4])
		unquoted := strings.Trim(rawInput, "'`\"")
		unquoted = strings.TrimSpace(unquoted)
		if (strings.HasPrefix(unquoted, "{") && strings.HasSuffix(unquoted, "}")) ||
			(strings.HasPrefix(unquoted, "[") && strings.HasSuffix(unquoted, "]")) {
			data = []byte(unquoted)
		} else {
			var err error
			data, err = os.ReadFile(args[4])
			if err != nil {
				return errorEnvelope(tool, op, failure("input_not_found", "request input could not be read", map[string]any{"path": args[4], "error": bounded(err.Error())})), false
			}
		}
		// An interrupt or termination request cancels the run, which kills the
		// tool's whole process tree. Without this, a terminal Ctrl+C or a
		// supervisor's SIGTERM would end Facet and leave its renderer running.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var env Envelope
		if op == "run" {
			env = runEnvelope(WithProgress(ctx, progress), tool, data)
		} else {
			env = estimateEnvelope(ctx, tool, data)
		}
		return env, env.OK
	default:
		return bad("unknown tools operation: " + op)
	}
}

const toolsUsage = `usage: facet tools <operation> [arguments]

Operations:
  list                                     List every tool with its readiness, cost and effects
  describe <tool>                          Show a tool's request schema, result schema and effects
  estimate <tool> --input <request.json>   Validate a request and report cost and effects; runs nothing
  run <tool> --input <request.json>        Run a tool and print its result envelope

--input takes a path to a JSON file or an inline JSON object.
Every command prints one JSON envelope; the exit status is zero when "ok" is true.
Run "facet tools <operation> --help" for details of one operation.`

// toolOperations documents each `facet tools` operation for help output.
var toolOperations = []struct{ name, usage, summary string }{
	{"list", "facet tools list", "Lists every tool with its capability, dependencies and their resolution, " +
		"whether it is configured, its cost, and its effects (may_charge, network, external_write, deterministic)."},
	{"describe", "facet tools describe <tool>", "Shows one tool's request schema, result schema, provider, cost and effects. " +
		"Read it before building a request: input field names differ between tools."},
	{"estimate", "facet tools estimate <tool> --input <request.json>", "Validates a request without producing output: " +
		"no media is written, nothing is billed. Reports estimated cost and, for renders, expected duration."},
	{"run", "facet tools run <tool> --input <request.json>", "Runs a tool. The envelope carries the result, warnings, " +
		"execution effects, and an artifacts list describing every file the run produced (path, media type, size, sha256)."},
}

// cliHelp reports whether args asks for help and builds the help payload.
func cliHelp(args []string) (map[string]any, bool) {
	isHelp := func(arg string) bool { return arg == "-h" || arg == "--help" || arg == "-help" }
	asked := false
	for _, arg := range args {
		if isHelp(arg) {
			asked = true
			break
		}
	}
	rest := args
	if len(args) > 0 && args[0] == "help" {
		asked, rest = true, args[1:]
	}
	if !asked {
		return nil, false
	}
	payload := map[string]any{"usage": toolsUsage}
	operations := make([]any, 0, len(toolOperations))
	for _, o := range toolOperations {
		operations = append(operations, map[string]any{"name": o.name, "usage": o.usage, "summary": o.summary})
	}
	payload["operations"] = operations
	if len(rest) == 0 || isHelp(rest[0]) {
		return payload, true
	}
	for _, o := range toolOperations {
		if o.name != rest[0] {
			continue
		}
		help := map[string]any{"operation": o.name, "usage": "usage: " + o.usage, "summary": o.summary}
		// A named tool is the one the caller is about to build a request for,
		// so its request schema is the most useful thing help can add.
		if len(rest) > 1 && !isHelp(rest[1]) {
			if tool := canonicalToolName(rest[1]); known(tool) {
				help["tool"] = tool
				help["request_schema"] = schemas[tool]
			}
		}
		return help, true
	}
	return payload, true
}

func success(tool, op string, result any, warnings []string) Envelope {
	if warnings == nil {
		warnings = []string{}
	}
	e := executionFor(tool)
	if facts, ok := result.(map[string]any); ok {
		if cost, ok := facts["estimated_cost"].(float64); ok {
			e.EstimatedCost = &cost
		}
		if mock, _ := facts["mock"].(bool); mock {
			zero := 0.0
			e.EstimatedCost, e.ActualCost, e.Network = &zero, &zero, false
		}
	}
	if op == "estimate" {
		zero := 0.0
		e.ActualCost = &zero // Estimation does not generate or bill media.
	}
	e.ExternalWrite = externalWriteFor(tool, op)
	return Envelope{OK: true, Tool: tool, Operation: op, Result: result, Warnings: warnings, Execution: e}
}

func errorEnvelope(tool, op string, err error) Envelope {
	te := &ToolError{Code: "command_failed", Message: bounded(err.Error()), Details: map[string]any{}}
	var tf *toolFailure
	if errors.As(err, &tf) {
		te = tf.err
	}
	e := executionFor(tool)
	e.ExternalWrite = externalWriteFor(tool, op)
	return Envelope{OK: false, Tool: tool, Operation: op, Error: te, Warnings: []string{}, Execution: e}
}

func known(name string) bool {
	cName := canonicalToolName(name)
	for _, n := range names {
		if n == cName {
			return true
		}
	}
	return false
}

// runEnvelope runs a canonical tool and builds its envelope, including the
// descriptors of every file the run produced. RunContext and the CLI share
// it so the two surfaces cannot drift.
//
// A run that submitted a provider job and then ended without the media,
// cancelled or failed, names that job in its error, so it can be collected
// with resume_job_id instead of being paid for twice.
func runEnvelope(ctx context.Context, tool string, data []byte) Envelope {
	if err := ctx.Err(); err != nil {
		return errorEnvelope(tool, "run", failure("cancelled", err.Error(), nil))
	}
	ctx, job := withProviderJob(ctx)
	result, warnings, err := executeContext(ctx, tool, "run", data)
	if ctx.Err() != nil {
		return withProviderJobError(errorEnvelope(tool, "run", failure("cancelled", ctx.Err().Error(), nil)), job.ID())
	}
	if err != nil {
		return withProviderJobError(errorEnvelope(tool, "run", err), job.ID())
	}
	env := success(tool, "run", result, warnings)
	env.Artifacts, env.Warnings = collectArtifacts(tool, result, env.Warnings)
	return env
}

// estimateEnvelope validates a request through the tool's estimate path. It
// writes nothing and bills nothing.
func estimateEnvelope(ctx context.Context, tool string, data []byte) Envelope {
	if err := ctx.Err(); err != nil {
		return errorEnvelope(tool, "estimate", failure("cancelled", err.Error(), nil))
	}
	result, warnings, err := executeContext(ctx, tool, "estimate", data)
	if err != nil {
		return errorEnvelope(tool, "estimate", err)
	}
	return success(tool, "estimate", result, warnings)
}

// Resolution states. Operator ruling 7: Resolution is not Boolean, and the
// existence of a key, binary or process is not proof a provider is operational.
//
// Measured, which is why the third state exists:
//
//	elevenlabs_tts  ELEVENLABS_API_KEY present -> HTTP 402 paid_plan_required
//	gflow_image     binary present, `gflow status` reports HEALTHY -> CAPTCHA_FAILED
//
// Both would have been reported SATISFIED by a Boolean check, and both fail at
// the provider. UNKNOWN is the honest answer for a requirement that is present
// but unproven.
const (
	// ResolutionSatisfied: present and provably usable.
	ResolutionSatisfied = "satisfied"
	// ResolutionUnsatisfied: absent. Refusing locally is possible and names a
	// remedy the caller can act on.
	ResolutionUnsatisfied = "unsatisfied"
	// ResolutionUnknown: present, but existence does not prove operability.
	// Must NOT be reported as satisfied; execution may still proceed under
	// product or target policy, and the provider becomes the authority.
	ResolutionUnknown = "unknown"
)

// resolutionOf reports a requirement's state without claiming more than was
// checked. Published per dependency by `facet tools list` and `describe`.
//
// A binary or file whose absence is decisive resolves to satisfied or
// unsatisfied. A credential resolves to unknown when present, because holding
// a string is not holding a working account.
func resolutionOf(present bool, kind string) string {
	if !present {
		return ResolutionUnsatisfied
	}
	if kind == "env" {
		return ResolutionUnknown
	}
	return ResolutionSatisfied
}

func dependency(name string) map[string]any {
	path, err := lookPath(name)
	return map[string]any{
		"name": name, "available": err == nil, "path": path, "type": "binary",
		"resolution": resolutionOf(err == nil, "binary"),
	}
}

// composerDependency reports whether the Remotion composer can actually
// render, not merely whether its directory exists.
//
// A composer without node_modules is present and unusable: the render CLI it
// must execute lives inside those dependencies. Reporting the directory as
// available made video_compose claim it was configured while every render
// failed.
func composerDependency() map[string]any {
	dir, usable, _ := ComposerStatus()
	return map[string]any{
		"name": "remotion-composer", "available": usable, "path": dir, "type": "runtime",
		// Satisfied or unsatisfied, never unknown: usability is decided by the
		// render CLI existing on disk, which is a fact this process can check
		// rather than a credential it can only hold.
		"resolution": resolutionOf(usable, "runtime"),
	}
}

func envDependency(name string) map[string]any {
	val := os.Getenv(name)
	return map[string]any{
		"name": name, "available": val != "", "path": "", "type": "env",
		// Present means UNKNOWN, never satisfied: a key that exists may still
		// be unfunded, expired or wrong. Verified — ELEVENLABS_API_KEY is
		// present here and the provider returns 402.
		"resolution": resolutionOf(val != "", "env"),
	}
}

// envAnyDependency is a credential a tool reads under any of names, in that
// order. It is reported under the name that is set (else the first), with
// every accepted name in alternatives, exactly as the tool resolves it.
func envAnyDependency(names ...string) map[string]any {
	chosen := names[0]
	for _, name := range names {
		if os.Getenv(name) != "" {
			chosen = name
			break
		}
	}
	dep := envDependency(chosen)
	dep["alternatives"] = append([]string(nil), names...)
	return dep
}

func summary(name string) map[string]any {
	deps := []any{}
	switch name {
	case "media_probe", "audio_probe":
		deps = append(deps, dependency("ffprobe"))
	case "frame_sample", "scene_detect", "visual_qa", "output_review", "source_edit", "video_trimmer", "video_stitch", "silence_cutter", "audio_mix":
		deps = append(deps, dependency("ffmpeg"), dependency("ffprobe"))
	case "color_grade":
		deps = append(deps, dependency("ffmpeg"))
	case "video_compose":
		// The Remotion composer is a real dependency and was never declared.
		//
		// Verified on a fresh install: video_compose reported configured:true
		// listing only ffmpeg, and the next render failed with
		// dependency_missing because the composer had no node_modules. A tool
		// that cannot render must not report itself ready.
		deps = append(deps, dependency("ffmpeg"), dependency("node"), composerDependency())
	case "ffmpeg_caption_burn":
		deps = append(deps, dependency("ffmpeg"))
	case "hyperframes_compose":
		// HyperFrames runs from the pinned install under the runtime's
		// dependencies with node; there is no unpinned npx fallback.
		deps = append(deps, dependency("node"), hyperframesDependency(), dependency("ffmpeg"))
	case "music_library":
		// optional ffprobe
		deps = append(deps, dependency("ffprobe"))
	case "direct_clip_search":
		deps = append(deps, dependency("ffmpeg"), dependency("ffprobe"))
	case "pexels_video":
		deps = append(deps, envDependency("PEXELS_API_KEY"))
	case "pixabay_video":
		deps = append(deps, envDependency("PIXABAY_API_KEY"))
	case "openai_tts", "openai_image", "sora_video":
		deps = append(deps, envDependency("OPENAI_API_KEY"))
	case "elevenlabs_tts":
		deps = append(deps, envDependency("ELEVENLABS_API_KEY"))
	case "flux_image":
		deps = append(deps, envAnyDependency("FAL_KEY", "FLUX_API_KEY"))
	case "kling_video":
		deps = append(deps, envAnyDependency("FAL_KEY", "KLING_API_KEY"))
	case "piper_tts":
		deps = append(deps, dependency("piper"))
	case "gflow_video", "gflow_image":
		deps = append(deps, dependency("gflow"))
	case "edge_tts", "subtitle_gen", "wikimedia", "image_selector", "video_selector":
		// pure go / public api / selector logic
	}

	configured := dependenciesAvailable(deps)
	if name == "music_library" {
		configured = true
	} else if name == "direct_clip_search" {
		configured = true // Wikimedia always available
	} else if name == "image_selector" || name == "video_selector" {
		configured = true
	}

	// Cost, network and write behaviour belong in the LISTING, not only in
	// describe.
	//
	// The agent overlay tells an agent "unknown cost is not free" and to get
	// human consent before paid generation. Choosing which tool to use is the
	// moment that decision is made, and the listing omitted cost entirely — so
	// telling a free tool from a billing one meant 35 separate describe calls,
	// or guessing.
	//
	// Verified: `tools list` reported no cost field at all while `tools
	// describe media_probe` correctly reported amount 0, known true. The two
	// surfaces disagreed about the same tool.
	//
	// Taken from executionFor, the same source describe uses, so the two
	// cannot drift into disagreeing again.
	exec := executionFor(name)
	meta := ToolMeta(name)
	out := map[string]any{
		"name":         name,
		"capability":   capabilities[name],
		"capabilities": meta.Capabilities,
		"runtime":      meta.Runtime,
		"knowledge":    meta.Knowledge,
		"implemented":  true,
		"configured":   configured,
		"dependencies": deps,
		"cost": map[string]any{
			"currency": "USD",
			"amount":   exec.EstimatedCost,
			"known":    exec.EstimatedCost != nil,
		},
		// Chargeability is a SEPARATE fact from cost knowledge. A caller
		// deciding whether to seek human consent must read this, never
		// cost.known — see MayCharge.
		"may_charge":     MayCharge(name),
		"deterministic":  Deterministic(name),
		"network":        exec.Network,
		"external_write": externalWriteFor(name, "run"),
	}
	if meta.CreditNote != "" {
		out["credit_note"] = meta.CreditNote
	}
	return out
}

func dependenciesAvailable(deps []any) bool {
	for _, d := range deps {
		if m, ok := d.(map[string]any); ok {
			if avail, ok := m["available"].(bool); ok && !avail {
				return false
			}
		}
	}
	return true
}

var capabilities = map[string]string{
	"audio_mix":           "audio mixing",
	"audio_probe":         "audio metadata inspection",
	"color_grade":         "FFmpeg LUT and color grading tool",
	"direct_clip_search":  "stock clip search and download",
	"edge_tts":            "free keyless Microsoft Edge neural text-to-speech synthesis",
	"elevenlabs_tts":      "cloud text-to-speech synthesis",
	"flux_image":          "cloud AI image generation via FLUX",
	"frame_sample":        "review frame extraction",
	"gflow_image":         "Google Flow Imagen 4 / Nano Banana 2 image generation",
	"gflow_video":         "Google Flow Veo 3.1 cinematic video generation and 4K upsampling",
	"hyperframes_compose": "HTML/CSS/GSAP video composition",
	"image_selector":      "image provider discovery, facts, and explainable ranking",
	"kling_video":         "cloud AI video generation via Kling",
	"media_probe":         "media inspection",
	"music_library":       "local music discovery and indexing",
	"openai_image":        "cloud AI image generation via OpenAI DALL-E / GPT Image",
	"openai_tts":          "cloud text-to-speech synthesis",
	"output_review":       "technical output review",
	"pexels_video":        "stock video search and download",
	"piper_tts":           "local text-to-speech synthesis",
	"pixabay_video":       "stock video search and download",
	"ffmpeg_caption_burn": "FFmpeg subtitle and caption burning",
	"scene_detect":        "scene cut and shot boundary detection",
	"silence_cutter":      "silence detection and jump cut editing",
	"sora_video":          "cloud AI video generation via Sora",
	"source_edit":         "supplied-footage editing",
	"subtitle_gen":        "subtitle generation (SRT/VTT/JSON)",
	"video_compose":       "video composition orchestration",
	"video_selector":      "video provider discovery, facts, duration limits, and explainable ranking",
	"video_stitch":        "multi-clip assembly and transitions",
	"video_trimmer":       "video trimming, speed, and concatenation",
	"visual_qa":           "visual quality assurance and inspection",
	"wikimedia":           "Wikimedia Commons stock search and download",
	"script_check":        "script check: words against duration, the hook, caption-length lines, section shares",
	"plan_check":          "scene plan check: timeline gaps, variety, slideshow risk, narration coverage, provider feasibility",
}

func description(name string) map[string]any {
	d := summary(name)
	exec := executionFor(name)
	d["provider"] = exec.Provider
	d["request_schema"] = schemas[name]
	d["result_schema"] = resultSchemas[name]
	d["cost"] = map[string]any{"currency": "USD", "amount": exec.EstimatedCost, "known": exec.EstimatedCost != nil}
	d["may_charge"] = MayCharge(name)
	d["deterministic"] = Deterministic(name)
	d["network"] = exec.Network
	// A `run` of this tool may write; describe reports the run behaviour, which
	// is what a caller is deciding about.
	d["external_write"] = externalWriteFor(name, "run")
	return d
}

var schemas = map[string]any{
	"media_probe": map[string]any{"type": "object", "additionalProperties": false, "anyOf": []any{map[string]any{"required": []string{"input"}}, map[string]any{"required": []string{"input_path"}}}, "properties": map[string]any{
		"input": map[string]any{"type": "string", "minLength": 1}, "input_path": map[string]any{"type": "string", "minLength": 1}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 30},
	}},
	"audio_probe": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"input_path"}, "properties": map[string]any{
		"input_path": map[string]any{"type": "string", "minLength": 1}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 15},
	}},
	"frame_sample": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"output_dir", "strategy"}, "anyOf": []any{map[string]any{"required": []string{"input"}}, map[string]any{"required": []string{"input_path"}}}, "properties": map[string]any{
		"input": map[string]any{"type": "string", "minLength": 1}, "input_path": map[string]any{"type": "string", "minLength": 1}, "output_dir": map[string]any{"type": "string", "minLength": 1}, "format": map[string]any{"enum": []string{"jpg", "png"}},
		"strategy": map[string]any{"oneOf": []any{
			objectSchema([]string{"type", "timestamps"}, map[string]any{"type": map[string]any{"const": "timestamps"}, "timestamps": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "number", "minimum": 0}}}),
			objectSchema([]string{"type", "count"}, map[string]any{"type": map[string]any{"const": "uniform"}, "count": map[string]any{"type": "integer", "minimum": 1}}),
			objectSchema([]string{"type", "count"}, map[string]any{"type": map[string]any{"const": "scenes"}, "count": map[string]any{"type": "integer", "minimum": 1}, "threshold": map[string]any{"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 1}}),
		}},
		"image_format": map[string]any{"enum": []string{"jpg", "png"}, "default": "jpg"}, "overwrite": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 60},
	}},
	"scene_detect": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"input_path"}, "properties": map[string]any{
		"input_path": map[string]any{"type": "string", "minLength": 1}, "method": map[string]any{"enum": []string{"content", "threshold", "adaptive"}, "default": "content"},
		"threshold": map[string]any{"type": "number", "default": 0.3}, "min_scene_length_seconds": map[string]any{"type": "number", "default": 1.0}, "output_path": map[string]any{"type": "string"},
	}},
	"visual_qa": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operation", "input_path"}, "properties": map[string]any{
		"operation": map[string]any{"enum": []string{"review", "probe", "audio_levels"}}, "input_path": map[string]any{"type": "string", "minLength": 1},
		"timestamps": map[string]any{"type": "array", "items": map[string]any{"type": "number"}}, "output_dir": map[string]any{"type": "string"}, "expected": map[string]any{"type": "object"},
	}},
	"output_review": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"input": map[string]any{"type": "string", "minLength": 1}, "rendered_file": map[string]any{"type": "string", "minLength": 1}, "input_path": map[string]any{"type": "string", "minLength": 1}, "sample_count": map[string]any{"type": "integer", "minimum": 1},
		"profile": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"width", "height", "fps"}, "properties": map[string]any{"width": map[string]any{"type": "integer", "minimum": 1}, "height": map[string]any{"type": "integer", "minimum": 1}, "fps": map[string]any{"type": "number", "exclusiveMinimum": 0}}},
		"checks":  map[string]any{"type": "object", "additionalProperties": false, "required": []string{"duration", "video_codec", "pixel_format", "audio"}, "properties": map[string]any{"duration": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"expected", "tolerance"}, "properties": map[string]any{"expected": map[string]any{"type": "number", "exclusiveMinimum": 0}, "tolerance": map[string]any{"type": "number", "minimum": 0}}}, "video_codec": map[string]any{"type": "string", "minLength": 1}, "pixel_format": map[string]any{"type": "string", "minLength": 1}, "audio": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"required", "codec", "sample_rate", "channels"}, "properties": map[string]any{"required": map[string]any{"type": "boolean"}, "codec": map[string]any{"type": "string", "minLength": 1}, "sample_rate": map[string]any{"type": "integer", "minimum": 1}, "channels": map[string]any{"type": "integer", "minimum": 1}}}}},
		"samples": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"type": map[string]any{"const": "uniform"}, "count": map[string]any{"type": "integer", "minimum": 1}}}, "evidence_dir": map[string]any{"type": "string", "minLength": 1}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 90}, "contact_sheet": map[string]any{"type": "object", "additionalProperties": false, "description": "tile evenly spaced frames into evidence_dir/contact-sheet.jpg", "properties": map[string]any{"count": map[string]any{"type": "integer", "minimum": 4, "maximum": 24, "default": 12}, "columns": map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "default": 4}}}, "coverage": map[string]any{"type": "object", "additionalProperties": false, "description": "check narration and captions against the video, and measure integrated loudness", "properties": map[string]any{"timing_path": stringSchema(), "captions_path": stringSchema()}},
	}},
	"source_edit": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"segments", "target", "output"}, "properties": map[string]any{
		"segments":          map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"input", "start", "end"}, "properties": map[string]any{"input": map[string]any{"type": "string", "minLength": 1}, "start": map[string]any{"type": "number", "minimum": 0}, "end": map[string]any{"type": "number", "exclusiveMinimum": 0}, "transition": map[string]any{"enum": []string{"cut"}}, "position": map[string]any{"enum": framePositions}, "focal_point": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"x", "y"}, "properties": map[string]any{"x": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "y": map[string]any{"type": "number", "minimum": 0, "maximum": 1}}}}}},
		"target":            map[string]any{"type": "object", "additionalProperties": false, "required": []string{"width", "height", "fps", "fit", "video_codec", "pixel_format", "audio_codec", "audio_sample_rate", "audio_channels"}, "properties": map[string]any{"width": map[string]any{"type": "integer", "minimum": 2, "multipleOf": 2}, "height": map[string]any{"type": "integer", "minimum": 2, "multipleOf": 2}, "fps": map[string]any{"type": "number", "exclusiveMinimum": 0}, "fit": map[string]any{"enum": []string{"contain", "cover"}}, "video_codec": map[string]any{"const": "h264"}, "pixel_format": map[string]any{"const": "yuv420p"}, "audio_codec": map[string]any{"const": "aac"}, "audio_sample_rate": map[string]any{"const": 48000}, "audio_channels": map[string]any{"const": 2}}},
		"replacement_audio": map[string]any{"type": "string", "minLength": 1}, "output": map[string]any{"type": "string", "minLength": 1}, "overwrite": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 300},
	}},
	"video_trimmer": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operation", "input_path"}, "properties": map[string]any{
		"operation": map[string]any{"enum": []string{"cut", "speed", "concat"}}, "input_path": map[string]any{"type": "string"}, "output_path": map[string]any{"type": "string"},
		"start_seconds": map[string]any{"type": "number"}, "end_seconds": map[string]any{"type": "number"}, "speed_factor": map[string]any{"type": "number"}, "codec": map[string]any{"type": "string", "default": "copy"},
	}},
	"video_stitch": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operation", "clips"}, "properties": map[string]any{
		"operation":           map[string]any{"type": "string", "enum": []string{"validate", "stitch", "preview_stitch", "spatial"}},
		"clips":               map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}},
		"output_path":         map[string]any{"type": "string"},
		"transition":          map[string]any{"type": "string", "enum": []string{"cut", "crossfade", "fade"}, "default": "cut"},
		"transition_duration": map[string]any{"type": "number", "minimum": 0.1, "maximum": 5.0, "default": 0.5},
		"auto_normalize":      map[string]any{"type": "boolean", "default": false, "description": "Accepted for compatibility. stitch and preview_stitch always normalize every clip to one size, frame rate and audio format."},
		"target_resolution":   map[string]any{"type": "string", "pattern": `^\d+x\d+$`, "description": "stitch only: output size such as 1920x1080. Defaults to the first clip's size."},
		"target_fps":          map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "stitch only: output frame rate. Defaults to the first clip's rate."},
		"codec":               map[string]any{"type": "string", "default": "libx264", "description": "spatial only: video encoder."},
		"crf":                 map[string]any{"type": "integer", "minimum": 1, "maximum": 51, "default": 23},
		"preset":              map[string]any{"type": "string", "enum": []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}, "default": "medium"},
		"layout":              map[string]any{"type": "string", "enum": []string{"side_by_side", "vertical_stack", "picture_in_picture"}, "default": "side_by_side"},
		"pip_position":        map[string]any{"type": "string", "enum": []string{"top_left", "top_right", "bottom_left", "bottom_right"}, "default": "bottom_right"},
		"pip_scale":           map[string]any{"type": "number", "minimum": 0.1, "maximum": 0.5, "default": 0.3},
		"pip_margin":          map[string]any{"type": "integer", "minimum": 1, "default": 10},
		"timeout_seconds":     map[string]any{"type": "integer", "minimum": 1, "default": 300},
	}},
	"video_compose": map[string]any{"type": "object", "additionalProperties": false, "description": "Render a video. Composer props: cuts (or scenes) for the Explainer, or composition / renderer_family for another composition, with width, height, fps, duration_seconds, theme, captions, audio and overlays; the composer checks every field before rendering (guidance/runtimes/scene-types.md). timing_path lets cuts name narration lines, captions come from the narration's word timings, and music duck under the narration. edit_decisions with render_runtime remotion render the same way; composition_mode atelier with bespoke {entry, composition_id, props_path} renders the project's own Remotion composition. Audio is normalised to loudness_target (default -14 LUFS). An operation envelope (compose, burn_subtitles, overlay, encode) runs FFmpeg.", "properties": map[string]any{
		"operation": map[string]any{"enum": []string{"compose", "render", "remotion_render", "burn_subtitles", "overlay", "encode"}}, "input_path": map[string]any{"type": "string"}, "output_path": map[string]any{"type": "string"},
		"edit_decisions": map[string]any{"type": "object"}, "asset_manifest": map[string]any{"type": "object"}, "audio_path": map[string]any{"type": "string"}, "subtitle_path": map[string]any{"type": "string"},
		"output":           stringSchema(),
		"composition":      map[string]any{"enum": []string{"Explainer", "CinematicRenderer", "TalkingHead", "TitledVideo", "ProductReveal", "ProductRevealVertical", "CollageBurst", "LyricOverlay", "EndTag", "EndTagOverlay", "CaptionOverlayOnly", "HeroTitle"}},
		"composition_id":   map[string]any{"type": "string"},
		"renderer_family":  map[string]any{"enum": []string{"explainer-data", "explainer-teacher", "product-reveal", "screen-demo", "animation-first", "cinematic-trailer", "documentary-montage", "presenter"}},
		"render_runtime":   map[string]any{"enum": []string{"remotion", "hyperframes", "ffmpeg"}},
		"composition_mode": map[string]any{"enum": []string{"templated", "atelier"}},
		"bespoke":          objectSchema([]string{"entry", "composition_id"}, map[string]any{"entry": stringSchema(), "composition_id": stringSchema(), "props_path": stringSchema()}),
		"delivery_promise": map[string]any{"type": "object"},
		"theme":            map[string]any{"enum": []string{"clean-professional", "flat-motion-graphics", "minimalist-diagram", "premium-minimalist", "anime-ghibli"}},
		"themeConfig":      map[string]any{"type": "object"},
		"timing_path":      map[string]any{"type": "string", "description": "a voice tool's narration timing record"},
		"loudness_target":  map[string]any{"type": "number", "minimum": -40, "maximum": -5, "default": -14},
		"width":            map[string]any{"type": "integer", "minimum": 2, "multipleOf": 2},
		"height":           map[string]any{"type": "integer", "minimum": 2, "multipleOf": 2},
		"fps":              map[string]any{"type": "number", "exclusiveMinimum": 0, "default": 30},
		"duration_seconds": map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "omit to end at the last cut (or the narration's end when cuts name lines)"},
		"cuts":             map[string]any{"type": "array", "minItems": 1, "items": explainerCutSchema("in_seconds", "out_seconds")},
		"scenes":           map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object"}, "description": "a scene plan (start_seconds/end_seconds), or CinematicRenderer scenes with composition CinematicRenderer"},
		"audio":            explainerAudioSchema(),
		"overlays":         map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		"captions":         map[string]any{"type": "object", "description": "{timing_path or words, words_per_page, highlight_color, position: bottom|center|top, font_size}"},
		"subtitle_style":   map[string]any{"type": "object"},
		"codec":            stringSchema(), "crf": map[string]any{"type": "integer"}, "preset": stringSchema(), "profile": stringSchema(), "remotion_timeout_ms": map[string]any{"type": "integer"}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1},
		"videoSrc": stringSchema(), "titleFontSize": numberSchema(), "titleWidth": numberSchema(), "signalLineCount": numberSchema(), "soundtrack": map[string]any{"type": "object"}, "music": map[string]any{"type": "object"},
		"wordsPerPage": numberSchema(), "fontSize": numberSchema(), "highlightColor": stringSchema(), "captionColor": stringSchema(), "captionBackgroundColor": stringSchema(),
		"tagline": stringSchema(), "taglineInSeconds": numberSchema(), "taglineOutSeconds": numberSchema(), "topPx": numberSchema(), "accentColor": stringSchema(),
		"productImage": stringSchema(), "productName": stringSchema(), "price": stringSchema(), "closer": stringSchema(),
		"backgroundSrc": stringSchema(), "backgroundInSeconds": numberSchema(), "curtainStartSeconds": numberSchema(), "curtainEndSeconds": numberSchema(), "clips": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		"lyrics": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "bottomY": numberSchema(),
		"text": stringSchema(), "palette": stringSchema(), "fadeInSeconds": numberSchema(), "holdSeconds": numberSchema(), "fadeOutSeconds": numberSchema(),
		"words": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "color": stringSchema(), "backgroundColor": stringSchema(), "fontFamily": stringSchema(), "wordSeparator": stringSchema(), "position": stringSchema(),
		"title": stringSchema(), "subtitle": stringSchema(), "textColor": stringSchema(), "subtitleColor": stringSchema(), "scrimBackground": stringSchema(),
	}},
	"subtitle_gen": map[string]any{"type": "object", "additionalProperties": false, "anyOf": []any{map[string]any{"required": []string{"segments"}}, map[string]any{"required": []string{"timing_path"}}}, "properties": map[string]any{"timing_path": map[string]any{"type": "string", "description": "a voice tool's narration timing record: cues from its lines and words"}, "layout": map[string]any{"enum": []string{"horizontal", "vertical"}, "default": "horizontal", "description": "words a cue: horizontal 8, vertical 4 (unless max_words_per_cue)"},
		"segments": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"text": stringSchema(), "start": map[string]any{"type": "number", "description": "Start time in seconds"}, "end": map[string]any{"type": "number", "description": "End time in seconds"}, "words": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"word": stringSchema(), "start": map[string]any{"type": "number"}, "end": map[string]any{"type": "number"}, "startMs": map[string]any{"type": "integer"}, "endMs": map[string]any{"type": "integer"}}}}}}}, "format": map[string]any{"enum": []string{"srt", "vtt", "json"}, "default": "srt"},
		"output_path": map[string]any{"type": "string"}, "max_chars_per_line": map[string]any{"type": "integer", "default": 42}, "max_words_per_cue": map[string]any{"type": "integer", "default": 8},
		"highlight_style": map[string]any{"enum": []string{"none", "word_by_word", "karaoke"}, "default": "none"}, "corrections": map[string]any{"type": "object"},
	}},
	"ffmpeg_caption_burn": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"input_path", "output_path"}, "properties": map[string]any{
		"input_path": map[string]any{"type": "string"}, "output_path": map[string]any{"type": "string"}, "segments": map[string]any{"type": "array"}, "timing_path": map[string]any{"type": "string", "description": "a voice tool's narration timing record"},
		"srt_path": map[string]any{"type": "string"}, "words_per_page": map[string]any{"type": "integer", "default": 4}, "font_size": map[string]any{"type": "integer", "default": 52},
		"highlight_color": map[string]any{"type": "string", "default": "#22D3EE"},
	}},
	"silence_cutter": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"input_path"}, "properties": map[string]any{
		"input_path": map[string]any{"type": "string"}, "output_path": map[string]any{"type": "string"}, "mode": map[string]any{"enum": []string{"remove", "speed_up", "mark"}, "default": "remove"},
		"silence_threshold_db": map[string]any{"type": "number", "default": -35}, "min_silence_duration": map[string]any{"type": "number", "default": 0.5}, "padding_seconds": map[string]any{"type": "number", "default": 0.08},
	}},
	"hyperframes_compose": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operation"}, "properties": map[string]any{
		"operation":      map[string]any{"enum": []string{"doctor", "scaffold_workspace", "lint", "validate", "inspect", "check", "render", "render_existing", "add_block"}},
		"workspace_path": map[string]any{"type": "string"}, "output_path": map[string]any{"type": "string"}, "block_name": map[string]any{"type": "string"},
	}},
	"audio_mix": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"video", "duration", "output"}, "properties": map[string]any{
		"video": map[string]any{"type": "string", "minLength": 1}, "source": audioOperationSchema(false), "music": audioOperationSchema(true),
		"loudness": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"enabled"}, "properties": map[string]any{"enabled": map[string]any{"type": "boolean"}, "integrated_lufs": map[string]any{"type": "number", "minimum": -70, "maximum": -5}, "true_peak_db": map[string]any{"type": "number", "minimum": -9, "maximum": 0}}},
		"duration": map[string]any{"const": "video"}, "output": map[string]any{"type": "string", "minLength": 1}, "overwrite": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 300},
	}},
	"music_library": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"library_dir": map[string]any{"type": "string"},
	}},
	"direct_clip_search": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"output_dir", "queries"}, "properties": map[string]any{
		"output_dir": map[string]any{"type": "string"}, "queries": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{"query": map[string]any{"type": "string"}, "slot_id": map[string]any{"type": "string"}, "kind": map[string]any{"enum": []string{"video", "image", "any"}}}}},
		"clips_per_query": map[string]any{"type": "integer", "default": 3}, "extract_thumbnails": map[string]any{"type": "boolean", "default": true}, "skip_existing": map[string]any{"type": "boolean", "default": true},
	}},
	"pexels_video": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{
		"query": map[string]any{"type": "string"}, "orientation": map[string]any{"enum": []string{"landscape", "portrait", "square"}}, "size": map[string]any{"enum": []string{"large", "medium", "small"}},
		"min_duration": map[string]any{"type": "integer"}, "max_duration": map[string]any{"type": "integer"}, "per_page": map[string]any{"type": "integer", "default": 5}, "output_path": map[string]any{"type": "string"},
	}},
	"pixabay_video": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{
		"query": map[string]any{"type": "string"}, "video_type": map[string]any{"enum": []string{"all", "film", "animation"}, "default": "all"},
		"category": map[string]any{"type": "string"}, "per_page": map[string]any{"type": "integer", "default": 5}, "output_path": map[string]any{"type": "string"},
	}},
	"wikimedia": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{
		"query": map[string]any{"type": "string"}, "kind": map[string]any{"enum": []string{"video", "image", "any"}, "default": "video"}, "per_page": map[string]any{"type": "integer", "default": 5}, "output_path": map[string]any{"type": "string"},
	}},
	"edge_tts": map[string]any{"type": "object", "additionalProperties": false, "oneOf": textOrLines(), "properties": map[string]any{"lines": narrationLinesSchema(), "pause_seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 10, "default": 0.35}, "timing_path": map[string]any{"type": "string"},
		"text": map[string]any{"type": "string"}, "voice": map[string]any{"type": "string", "default": "en-US-ChristopherNeural"}, "rate": map[string]any{"type": "string", "default": "+0%"},
		"volume": map[string]any{"type": "string", "default": "+0%"}, "output_path": map[string]any{"type": "string"}, "timeout_seconds": map[string]any{"type": "integer"},
	}},
	"openai_tts": map[string]any{"type": "object", "additionalProperties": false, "oneOf": textOrLines(), "properties": map[string]any{"lines": narrationLinesSchema(), "pause_seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 10, "default": 0.35}, "timing_path": map[string]any{"type": "string"},
		"text": map[string]any{"type": "string"}, "voice": map[string]any{"type": "string", "default": "alloy"}, "model": map[string]any{"type": "string", "default": "gpt-4o-mini-tts"},
		"response_format": map[string]any{"enum": []string{"mp3", "opus", "aac", "flac", "wav", "pcm"}, "default": "mp3"}, "instructions": map[string]any{"type": "string"}, "speed": map[string]any{"type": "number", "default": 1.0}, "output_path": map[string]any{"type": "string"},
	}},
	"elevenlabs_tts": map[string]any{"type": "object", "additionalProperties": false, "oneOf": textOrLines(), "properties": map[string]any{"lines": narrationLinesSchema(), "pause_seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 10, "default": 0.35}, "timing_path": map[string]any{"type": "string"},
		"text": map[string]any{"type": "string"}, "voice_id": map[string]any{"type": "string"}, "model_id": map[string]any{"type": "string", "default": "eleven_multilingual_v2"},
		"stability": map[string]any{"type": "number", "default": 0.5}, "similarity_boost": map[string]any{"type": "number", "default": 0.75}, "output_format": map[string]any{"type": "string", "default": "mp3_44100_128"}, "output_path": map[string]any{"type": "string"},
	}},
	"piper_tts": map[string]any{"type": "object", "additionalProperties": false, "oneOf": textOrLines(), "properties": map[string]any{"lines": narrationLinesSchema(), "pause_seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 10, "default": 0.35}, "timing_path": map[string]any{"type": "string"},
		"text": map[string]any{"type": "string"}, "model": map[string]any{"type": "string", "default": "en_US-lessac-medium"}, "speaker_id": map[string]any{"type": "integer", "default": 0},
		"length_scale": map[string]any{"type": "number", "default": 1.0}, "sentence_silence": map[string]any{"type": "number", "default": 0.3}, "output_path": map[string]any{"type": "string"},
	}},
	"openai_image": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"prompt"}, "properties": map[string]any{
		"prompt": map[string]any{"type": "string", "minLength": 1}, "model": map[string]any{"type": "string", "default": "dall-e-3"},
		"size": map[string]any{"type": "string", "default": "1024x1024"}, "aspect_ratio": map[string]any{"enum": []string{"1:1", "16:9", "9:16", "4:3", "3:4"}},
		"quality": map[string]any{"enum": []string{"standard", "hd"}, "default": "standard"}, "style": map[string]any{"enum": []string{"vivid", "natural"}, "default": "vivid"},
		"output_path": map[string]any{"type": "string"}, "mock": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 120},
	}},
	"flux_image": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"prompt"}, "properties": map[string]any{
		"prompt": map[string]any{"type": "string", "minLength": 1}, "model": map[string]any{"type": "string", "default": "fal-ai/flux-pro"},
		"aspect_ratio": map[string]any{"enum": []string{"1:1", "16:9", "9:16", "4:3", "3:4", "21:9"}, "default": "16:9"}, "image_size": map[string]any{"type": "string"},
		"num_images": map[string]any{"type": "integer", "minimum": 1, "default": 1}, "guidance_scale": map[string]any{"type": "number"}, "seed": map[string]any{"type": "integer"},
		"output_path": map[string]any{"type": "string"}, "mock": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 120},
	}},
	"kling_video": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"prompt"}, "properties": map[string]any{
		"prompt": map[string]any{"type": "string", "minLength": 1}, "model": map[string]any{"type": "string", "default": "fal-ai/kling-video"},
		"duration": map[string]any{"type": "number", "default": 5}, "aspect_ratio": map[string]any{"enum": []string{"16:9", "9:16", "1:1"}, "default": "16:9"},
		"mode": map[string]any{"enum": []string{"std", "pro"}, "default": "std"}, "image_url": map[string]any{"type": "string"},
		"output_path": map[string]any{"type": "string"}, "mock": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 300},
		"resume_job_id": resumeJobIDSchema,
	}},
	"sora_video": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"prompt"}, "properties": map[string]any{
		"prompt": map[string]any{"type": "string", "minLength": 1}, "model": map[string]any{"type": "string", "default": "sora-2"},
		"duration": map[string]any{"type": "number", "minimum": 1, "maximum": 20, "default": 5}, "aspect_ratio": map[string]any{"enum": []string{"16:9", "9:16", "1:1"}, "default": "16:9"},
		"resolution":  map[string]any{"enum": []string{"720p", "1080p"}, "default": "720p"},
		"output_path": map[string]any{"type": "string"}, "mock": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 300},
		"resume_job_id": resumeJobIDSchema,
	}},
	"gflow_video": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"prompt"}, "properties": map[string]any{
		"prompt": map[string]any{"type": "string", "minLength": 1}, "model": map[string]any{"const": "veo-3.1", "default": "veo-3.1"},
		"duration": map[string]any{"enum": []int{4, 6, 8, 10}, "default": 6}, "aspect_ratio": map[string]any{"enum": []string{"landscape", "portrait", "square"}, "default": "landscape"},
		"resolution": map[string]any{"enum": []string{"720p", "1080p", "4k"}, "default": "1080p"}, "start_frame": map[string]any{"type": "string"}, "end_frame": map[string]any{"type": "string"},
		"output_path": map[string]any{"type": "string"}, "mock": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 300},
	}},
	"gflow_image": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"prompt"}, "properties": map[string]any{
		"prompt": map[string]any{"type": "string", "minLength": 1}, "model": map[string]any{"enum": []string{"narwhal", "harbor_seal", "gem_pix_2"}, "default": "narwhal"},
		"aspect_ratio": map[string]any{"enum": []string{"landscape", "portrait", "square", "4:3", "3:4"}, "default": "landscape"}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 4, "default": 1},
		"reference_image": map[string]any{"type": "string"}, "output_path": map[string]any{"type": "string"}, "mock": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 180},
	}},
	"color_grade": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"input_path", "output_path"}, "properties": map[string]any{
		"input_path": map[string]any{"type": "string", "minLength": 1}, "output_path": map[string]any{"type": "string", "minLength": 1},
		"profile":   map[string]any{"enum": []string{"cinematic_warm", "cinematic_cool", "moody_dark", "bright_clean", "vintage_film", "high_contrast", "neutral", "custom"}, "default": "cinematic_warm"},
		"intensity": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "default": 0.8}, "lut_path": map[string]any{"type": "string"}, "custom_vf": map[string]any{"type": "string"},
		"temperature": map[string]any{"type": "number"}, "contrast": map[string]any{"type": "number"}, "saturation": map[string]any{"type": "number"}, "brightness": map[string]any{"type": "number"}, "gamma": map[string]any{"type": "number"},
		"overwrite": map[string]any{"type": "boolean", "default": false}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "default": 300},
	}},
	"image_selector": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"prompt": map[string]any{"type": "string"}, "aspect_ratio": map[string]any{"type": "string"}, "style": map[string]any{"type": "string"}, "scene_type": map[string]any{"type": "string"},
		"budget_tier": map[string]any{"enum": []string{"free", "budget", "standard", "premium"}}, "max_cost": map[string]any{"type": "number", "minimum": 0},
		"preferred_provider": map[string]any{"type": "string"}, "allowed_providers": map[string]any{"type": "array", "items": stringSchema()},
		"generation_mode": map[string]any{"enum": []string{"create", "edit", "stock", "any"}}, "require_text_rendering": map[string]any{"type": "boolean"}, "require_vector": map[string]any{"type": "boolean"},
	}},
	"video_selector": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"prompt": map[string]any{"type": "string"}, "aspect_ratio": map[string]any{"type": "string"}, "duration": map[string]any{"type": "number", "minimum": 0.1},
		"style": map[string]any{"type": "string"}, "intent": map[string]any{"type": "string"}, "budget_tier": map[string]any{"enum": []string{"free", "budget", "standard", "premium"}},
		"max_cost": map[string]any{"type": "number", "minimum": 0}, "preferred_provider": map[string]any{"type": "string"}, "allowed_providers": map[string]any{"type": "array", "items": stringSchema()},
		"source_type": map[string]any{"enum": []string{"text_to_video", "image_to_video", "stock", "any"}}, "require_audio": map[string]any{"type": "boolean"},
	}},
	"script_check": map[string]any{"type": "object", "additionalProperties": false, "anyOf": []any{map[string]any{"required": []string{"script_path"}}, map[string]any{"required": []string{"script"}}}, "properties": map[string]any{
		"script_path": stringSchema(), "script": map[string]any{"type": "object", "description": "a script record (schemas/artifacts/script.schema.json)"},
		"duration_seconds": map[string]any{"type": "number", "exclusiveMinimum": 0}, "words_per_minute": map[string]any{"type": "number", "minimum": 60, "maximum": 260, "default": 150},
		"hook_seconds": map[string]any{"type": "number", "exclusiveMinimum": 0, "default": 5}, "max_line_chars": map[string]any{"type": "integer", "minimum": 10, "default": 42},
		"structure": map[string]any{"type": "object", "description": "the pipeline structure's beats: {beats: [{id, share}]}"},
	}},
	"plan_check": map[string]any{"type": "object", "additionalProperties": false, "anyOf": []any{map[string]any{"required": []string{"scene_plan_path"}}, map[string]any{"required": []string{"scene_plan"}}}, "properties": map[string]any{
		"scene_plan_path": stringSchema(), "scene_plan": map[string]any{"type": "object", "description": "a scene_plan record"}, "timing_path": stringSchema(),
		"delivery_promise": map[string]any{"enum": []string{"motion_led", "source_led", "data_explainer", "teacher_explainer", "screen_demo", "avatar_presenter", "hybrid", "localization"}},
		"duration_seconds": map[string]any{"type": "number", "exclusiveMinimum": 0},
	}},
}

// resultSchemas describe each tool's successful result exactly: every
// property a result can carry is declared and nothing else is allowed. They
// are audited against the real results by TestRunResultsConformToSchemas,
// which runs every tool offline and validates what it returns.
var resultSchemas = map[string]any{
	"script_check": objectSchema([]string{"operation", "words", "spoken_seconds", "fit", "hook", "sections", "findings"}, map[string]any{"operation": stringSchema(), "words": integerSchema(), "words_per_minute": numberSchema(), "spoken_seconds": numberSchema(), "target_seconds": numberSchema(), "fit": stringSchema(), "hook": map[string]any{"type": "object"}, "sections": map[string]any{"type": "array"}, "findings": map[string]any{"type": "array"}}),
	"plan_check":   objectSchema([]string{"operation", "scenes", "motion_share", "slideshow_risk", "findings"}, map[string]any{"operation": stringSchema(), "scenes": integerSchema(), "scene_types": integerSchema(), "motion_share": numberSchema(), "slideshow_risk": stringSchema(), "ends_at": numberSchema(), "coverage": map[string]any{"type": "object"}, "unavailable": map[string]any{"type": "array"}, "findings": map[string]any{"type": "array"}}),
	"media_probe": objectSchema([]string{"input", "sha256", "format", "video_streams", "audio_streams", "warnings"}, map[string]any{
		"input": stringSchema(), "sha256": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}, "format": map[string]any{"type": "object", "required": []string{"duration", "format_name", "size", "bit_rate"}}, "video": map[string]any{"type": "object"}, "video_streams": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"index", "codec", "width", "height", "pixel_format", "fps", "reported_frame_rate", "rotation"}}}, "audio": map[string]any{"type": "array"}, "audio_streams": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"index", "codec", "sample_rate", "channels", "channel_layout"}}}, "warnings": stringArraySchema(),
	}),
	"audio_probe": objectSchema([]string{"file", "duration_seconds", "format_name", "format_long_name", "size_bytes", "bit_rate", "stream_count"}, map[string]any{
		"file": stringSchema(), "duration_seconds": numberSchema(), "format_name": stringSchema(), "format_long_name": stringSchema(), "size_bytes": integerSchema(), "bit_rate": integerSchema(), "stream_count": integerSchema(), "audio": map[string]any{"type": "object"},
	}),
	"frame_sample": objectSchema([]string{"input", "strategy", "resolved_timestamps", "samples"}, map[string]any{
		"input": stringSchema(), "strategy": enumSchema("timestamps", "uniform", "scenes"), "resolved_timestamps": numberArraySchema(), "samples": objectArraySchema([]string{"path", "timestamp", "width", "height"}, map[string]any{"path": stringSchema(), "timestamp": numberSchema(), "width": integerSchema(), "height": integerSchema()}),
	}),
	"scene_detect": objectSchema([]string{"scene_count", "scenes", "method"}, map[string]any{
		"scene_count": integerSchema(),
		"scenes":      objectArraySchema([]string{"index", "start_seconds", "end_seconds", "duration_seconds"}, map[string]any{"index": integerSchema(), "start_seconds": numberSchema(), "end_seconds": numberSchema(), "duration_seconds": numberSchema()}),
		"method":      stringSchema(),
		"output":      describedSchema(stringSchema(), "the scene list JSON file written when output_path was given; empty otherwise"),
	}),
	// One object for all three operations; each property names the operation
	// that produces it.
	"visual_qa": objectSchema([]string{"operation", "input"}, map[string]any{
		"operation": enumSchema("review", "probe", "audio_levels"), "input": stringSchema(),
		"frame_count": describedSchema(integerSchema(), "review"),
		"frames":      describedSchema(objectArraySchema([]string{"timestamp", "path"}, map[string]any{"timestamp": numberSchema(), "path": stringSchema()}), "review: the extracted frames"),
		"duration":    describedSchema(numberSchema(), "probe"), "file_size_mb": describedSchema(numberSchema(), "probe"), "has_audio": describedSchema(booleanSchema(), "probe"),
		"width": describedSchema(integerSchema(), "probe"), "height": describedSchema(integerSchema(), "probe"), "pixel_format": describedSchema(stringSchema(), "probe"),
		"video_codec": describedSchema(stringSchema(), "probe"), "fps": describedSchema(numberSchema(), "probe"), "audio_codec": describedSchema(stringSchema(), "probe"),
		"sample_rate": describedSchema(integerSchema(), "probe"), "channels": describedSchema(integerSchema(), "probe"),
		"validation_issues": describedSchema(stringArraySchema(), "probe"), "validation_passed": describedSchema(booleanSchema(), "probe"),
		"levels": describedSchema(objectArraySchema([]string{"timestamp"}, map[string]any{"timestamp": numberSchema(), "mean_volume_db": stringSchema(), "max_volume_db": stringSchema(), "error": stringSchema()}), "audio_levels"),
	}),
	"output_review": objectSchema([]string{"execution_status", "review_status", "gates", "samples", "volume", "output_facts"}, map[string]any{
		"execution_status": map[string]any{"const": "succeeded"}, "review_status": enumSchema("pass", "warn", "fail"), "gates": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"name", "status"}}}, "samples": map[string]any{"type": "array"}, "volume": map[string]any{"type": "object"}, "output_facts": map[string]any{"type": "object"}, "contact_sheet": map[string]any{"type": "object"}, "coverage": map[string]any{"type": "object"},
	}),
	"source_edit": mediaOutputResultSchema([]string{"realized_segments", "silent_inputs_filled"}),
	"video_trimmer": objectSchema([]string{"operation", "output"}, map[string]any{
		"operation": enumSchema("cut", "speed", "concat"), "input": stringSchema(), "output": stringSchema(),
		"start_seconds": describedSchema(numberSchema(), "cut"), "end_seconds": describedSchema(numberSchema(), "cut, when requested"),
		"speed_factor": describedSchema(numberSchema(), "speed"), "segment_count": describedSchema(integerSchema(), "concat"),
	}),
	"video_stitch": objectSchema([]string{"operation", "clip_count"}, map[string]any{
		"operation": enumSchema("validate", "stitch", "preview_stitch", "spatial"), "clip_count": integerSchema(),
		"compatible": describedSchema(booleanSchema(), "validate"), "total_duration": describedSchema(numberSchema(), "validate"),
		"mismatches": describedSchema(objectArraySchema([]string{"clip_index", "clip_path", "differences"}, map[string]any{"clip_index": integerSchema(), "clip_path": stringSchema(), "differences": stringArraySchema()}), "validate"),
		"transition": describedSchema(stringSchema(), "stitch and preview_stitch"), "transition_duration": describedSchema(numberSchema(), "stitch and preview_stitch"),
		"duration": describedSchema(numberSchema(), "stitch and preview_stitch"), "layout": describedSchema(stringSchema(), "spatial"),
		"output": describedSchema(stringSchema(), "every operation except validate"),
	}),
	"video_compose": objectSchema([]string{"operation", "output"}, map[string]any{
		"operation": enumSchema("compose", "remotion_render", "burn_subtitles", "overlay", "encode"), "output": stringSchema(),
		"composition_id": describedSchema(stringSchema(), "remotion_render"), "output_facts": describedSchema(map[string]any{"type": "object"}, "remotion_render: media_probe facts of the published file"),
		"input": describedSchema(stringSchema(), "burn_subtitles, overlay and encode"), "subtitles": describedSchema(stringSchema(), "burn_subtitles"),
		"overlay_count": describedSchema(integerSchema(), "overlay"), "codec": describedSchema(stringSchema(), "encode"),
		"cut_count": describedSchema(integerSchema(), "compose"), "has_subtitles": describedSchema(booleanSchema(), "compose"), "has_mixed_audio": describedSchema(booleanSchema(), "compose"),
	}),
	"subtitle_gen": objectSchema([]string{"format", "cue_count", "output"}, map[string]any{
		"format": enumSchema("srt", "vtt", "json"), "cue_count": integerSchema(), "output": stringSchema(),
	}),
	"ffmpeg_caption_burn": objectSchema([]string{"method", "output", "caption_count", "word_count"}, map[string]any{
		"method": map[string]any{"const": "ffmpeg"}, "output": stringSchema(),
		"caption_count": describedSchema(integerSchema(), "captions burned in"), "word_count": describedSchema(integerSchema(), "words across all captions"),
	}),
	"silence_cutter": objectSchema([]string{"mode", "input", "output"}, map[string]any{
		"mode": enumSchema("remove", "speed_up", "mark"), "input": stringSchema(),
		"output":                  describedSchema(stringSchema(), "the edited video, or for mark the silence map JSON"),
		"input_duration":          describedSchema(numberSchema(), "remove and speed_up"),
		"output_duration":         describedSchema(numberSchema(), "remove, when no silence was found and the input was copied"),
		"silence_removed_seconds": describedSchema(numberSchema(), "remove and speed_up"),
		"silence_segments":        integerSchema(), "speech_segments_count": integerSchema(),
		"silences":                 describedSchema(objectArraySchema([]string{"start", "end", "duration"}, map[string]any{"start": numberSchema(), "end": numberSchema(), "duration": numberSchema()}), "mark"),
		"speech_segments":          describedSchema(objectArraySchema([]string{"start", "end"}, map[string]any{"start": numberSchema(), "end": numberSchema(), "speed": numberSchema()}), "mark"),
		"total_duration":           describedSchema(numberSchema(), "mark"),
		"silence_duration_seconds": describedSchema(numberSchema(), "mark"),
	}),
	"hyperframes_compose": objectSchema([]string{"operation"}, map[string]any{
		"operation":     enumSchema("doctor", "scaffold_workspace", "lint", "validate", "inspect", "check", "render", "render_existing", "add_block"),
		"runtime_check": describedSchema(map[string]any{"type": "object"}, "doctor: which runtime pieces resolved, and where"),
		"workspace":     describedSchema(stringSchema(), "scaffold_workspace, render and render_existing"),
		"cut_count":     describedSchema(integerSchema(), "scaffold_workspace"),
		"files":         describedSchema(stringArraySchema(), "scaffold_workspace: the files written"),
		"report":        describedSchema(map[string]any{}, "lint, validate, inspect, check and add_block: the HyperFrames CLI's own JSON report"),
		"raw":           describedSchema(stringSchema(), "lint, validate, inspect, check and add_block: CLI output that was not JSON"),
		"block_name":    describedSchema(stringSchema(), "add_block"),
		"output":        describedSchema(stringSchema(), "render and render_existing: the rendered video"),
	}),
	"audio_mix": mediaOutputResultSchema([]string{"loudnorm"}),
	"music_library": objectSchema([]string{"library_dir", "exists", "track_count", "tracks"}, map[string]any{
		"library_dir": stringSchema(), "exists": booleanSchema(), "track_count": integerSchema(), "total_duration_seconds": numberSchema(),
		"tracks": objectArraySchema([]string{"name", "path", "size_bytes", "duration_seconds"}, map[string]any{"name": stringSchema(), "path": stringSchema(), "size_bytes": integerSchema(), "duration_seconds": numberSchema()}),
	}),
	"direct_clip_search": objectSchema([]string{"output_dir", "clips_downloaded", "total_clips", "clips"}, map[string]any{
		"output_dir": stringSchema(), "clips_downloaded": integerSchema(), "total_clips": integerSchema(),
		"per_source_counts": map[string]any{"type": "object"}, "queries_run": integerSchema(),
		"clips": objectArraySchema([]string{"clip_id", "source", "query", "path"}, map[string]any{"clip_id": stringSchema(), "source": stringSchema(), "query": stringSchema(), "slot_id": stringSchema(), "path": stringSchema(), "thumbnail": stringSchema(), "duration": numberSchema()}),
	}),
	"pexels_video": objectSchema([]string{"provider", "video_id", "query", "output"}, map[string]any{
		"provider": stringSchema(), "video_id": integerSchema(), "user": stringSchema(), "duration_seconds": numberSchema(), "width": integerSchema(), "height": integerSchema(), "fps": numberSchema(), "quality": stringSchema(),
		"query": stringSchema(), "output": stringSchema(), "total_results": integerSchema(), "results_returned": integerSchema(), "license": stringSchema(), "pexels_url": stringSchema(),
	}),
	"pixabay_video": objectSchema([]string{"provider", "video_id", "query", "output"}, map[string]any{
		"provider": stringSchema(), "video_id": integerSchema(), "user": stringSchema(), "tags": stringSchema(), "duration_seconds": numberSchema(), "width": integerSchema(), "height": integerSchema(),
		"query": stringSchema(), "output": stringSchema(), "total_results": integerSchema(), "results_returned": integerSchema(), "license": stringSchema(), "page_url": stringSchema(),
	}),
	"wikimedia": objectSchema([]string{"provider", "source_id", "query", "output"}, map[string]any{
		"provider": stringSchema(), "source_id": stringSchema(), "duration_seconds": numberSchema(), "width": integerSchema(), "height": integerSchema(),
		"query": stringSchema(), "output": stringSchema(), "license": stringSchema(), "download_url": stringSchema(),
	}),
	"edge_tts": objectSchema([]string{"output", "voice", "format", "provider", "size_bytes"}, map[string]any{
		"output": stringSchema(), "voice": stringSchema(), "format": stringSchema(), "provider": stringSchema(), "size_bytes": integerSchema(),
		"duration_seconds": describedSchema(numberSchema(), "measured from the written file; absent when it could not be probed"),
		"words":            describedSchema(objectArraySchema([]string{"word", "start", "end"}, map[string]any{"word": stringSchema(), "start": numberSchema(), "end": numberSchema()}), "the service's word timings in seconds"),
		"timing_path":      describedSchema(stringSchema(), "with lines: the narration_timing record"),
		"line_count":       describedSchema(integerSchema(), "with lines: how many lines were voiced"),
	}),
	"openai_tts":     ttsResultSchema("voice"),
	"elevenlabs_tts": ttsResultSchema("voice_id"),
	"piper_tts":      ttsResultSchema("speaker_id"),
	"openai_image":   objectSchema([]string{"provider", "model", "prompt", "output"}, map[string]any{"provider": stringSchema(), "model": stringSchema(), "prompt": stringSchema(), "size": stringSchema(), "quality": stringSchema(), "output": stringSchema(), "mock": booleanSchema(), "url": stringSchema()}),
	"flux_image":     objectSchema([]string{"provider", "model", "prompt", "output"}, map[string]any{"provider": stringSchema(), "model": stringSchema(), "prompt": stringSchema(), "aspect_ratio": stringSchema(), "output": stringSchema(), "mock": booleanSchema(), "url": stringSchema(), "seed": integerSchema()}),
	"kling_video":    objectSchema([]string{"provider", "model", "prompt", "output"}, map[string]any{"provider": stringSchema(), "model": stringSchema(), "prompt": stringSchema(), "duration": numberSchema(), "aspect_ratio": stringSchema(), "mode": stringSchema(), "output": stringSchema(), "mock": booleanSchema(), "video_url": stringSchema(), "provider_job_id": stringSchema(), "resumed": booleanSchema()}),
	"sora_video":     objectSchema([]string{"provider", "model", "prompt", "output"}, map[string]any{"provider": stringSchema(), "model": stringSchema(), "prompt": stringSchema(), "duration": numberSchema(), "aspect_ratio": stringSchema(), "resolution": stringSchema(), "output": stringSchema(), "mock": booleanSchema(), "video_url": stringSchema(), "provider_job_id": stringSchema(), "resumed": booleanSchema()}),
	"gflow_video":    gflowResultSchema(true),
	"gflow_image":    gflowResultSchema(false),
	"color_grade":    objectSchema([]string{"input", "output", "profile", "intensity", "filter_graph"}, map[string]any{"input": stringSchema(), "output": stringSchema(), "profile": stringSchema(), "intensity": numberSchema(), "lut_path": stringSchema(), "filter_graph": stringSchema(), "duration": numberSchema(), "output_facts": map[string]any{"type": "object"}}),
	"image_selector": objectSchema([]string{"selected_recommendation", "rationale", "candidates", "total_candidates", "configured_candidates"}, map[string]any{"selected_recommendation": stringSchema(), "rationale": stringSchema(), "candidates": map[string]any{"type": "array"}, "total_candidates": integerSchema(), "configured_candidates": integerSchema(), "requested_aspect_ratio": stringSchema(), "requested_style": stringSchema()}),
	"video_selector": objectSchema([]string{"selected_recommendation", "rationale", "candidates", "total_candidates", "configured_candidates"}, map[string]any{"selected_recommendation": stringSchema(), "rationale": stringSchema(), "candidates": map[string]any{"type": "array"}, "total_candidates": integerSchema(), "configured_candidates": integerSchema(), "requested_duration": numberSchema(), "requested_aspect_ratio": stringSchema()}),
}

// ttsResultSchema is the shared result of the providers that synthesize a
// file through a model: identity, format, input length and the measured
// duration of what was written.
func ttsResultSchema(voiceField string) map[string]any {
	properties := map[string]any{
		"provider": stringSchema(), "model": stringSchema(), "format": stringSchema(), "output": stringSchema(),
		"text_length":            describedSchema(integerSchema(), "bytes of input text"),
		"audio_duration_seconds": describedSchema(numberSchema(), "measured from the written file; 0 when it could not be probed"),
		"timing_path":            describedSchema(stringSchema(), "with lines: the narration_timing record"),
		"line_count":             describedSchema(integerSchema(), "with lines: how many lines were voiced"),
	}
	if voiceField == "speaker_id" {
		properties[voiceField] = integerSchema()
	} else {
		properties[voiceField] = stringSchema()
	}
	return objectSchema([]string{"provider", "model", voiceField, "format", "text_length", "audio_duration_seconds", "output"}, properties)
}

func gflowResultSchema(video bool) map[string]any {
	properties := map[string]any{"provider": stringSchema(), "model": stringSchema(), "prompt": stringSchema(), "aspect_ratio": stringSchema(), "output": stringSchema(), "mock": map[string]any{"type": "boolean"},
		"outputs": map[string]any{"type": "array", "minItems": 1, "items": objectSchema([]string{"id", "type", "mime_type", "output", "source_file", "sha256"}, map[string]any{
			"id": stringSchema(), "type": map[string]any{"enum": []string{"image", "video"}}, "mime_type": stringSchema(), "output": stringSchema(), "source_file": stringSchema(), "sha256": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
		})}}
	if video {
		properties["duration"], properties["resolution"] = map[string]any{"type": "number"}, stringSchema()
	}
	schema := objectSchema([]string{"provider", "model", "prompt", "output", "mock"}, properties)
	schema["if"] = map[string]any{"properties": map[string]any{"mock": map[string]any{"const": false}}}
	schema["then"] = map[string]any{"required": []string{"outputs"}}
	return schema
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}
}
func stringSchema() map[string]any  { return map[string]any{"type": "string"} }
func integerSchema() map[string]any { return map[string]any{"type": "integer"} }
func numberSchema() map[string]any  { return map[string]any{"type": "number"} }
func booleanSchema() map[string]any { return map[string]any{"type": "boolean"} }

// enumSchema is a string limited to values.
func enumSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

// objectArraySchema is an array whose items are closed objects.
func objectArraySchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": objectSchema(required, properties)}
}

// describedSchema returns a copy of schema carrying a description.
func describedSchema(schema map[string]any, description string) map[string]any {
	out := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		out[key] = value
	}
	out["description"] = description
	return out
}
func nonBlankStringSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1}
}

// explainerCutSchema describes a composer cut for agents. The composer's own
// contract checks every field before rendering, so this schema lists the
// common fields and scene types and lets scene-specific fields through.
func explainerCutSchema(start, end string) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": true,
		"description": "One scene. Give in/out seconds, or lines (narration line ids, with a top-level timing_path). Scene types and their fields: guidance/runtimes/scene-types.md.",
		"properties": map[string]any{
			"id":     stringSchema(),
			"type":   map[string]any{"enum": []string{"text_card", "hero_title", "section_title", "callout", "stat_card", "stat_reveal", "kpi_grid", "progress_bar", "comparison", "bar_chart", "line_chart", "pie_chart", "terminal_scene", "screenshot_scene", "anime_scene", "provider_chip", "image", "video"}},
			start:    map[string]any{"type": "number", "minimum": 0},
			end:      map[string]any{"type": "number", "exclusiveMinimum": 0},
			"lines":  map[string]any{"type": "array", "minItems": 1, "items": stringSchema(), "description": "narration line ids this scene covers"},
			"source": stringSchema(), "media_kind": map[string]any{"enum": []string{"image", "video"}},
			"animation":           map[string]any{"enum": []string{"zoom-in", "zoom-out", "pan-left", "pan-right", "ken-burns", "ken-burns-slow-zoom", "parallax", "static", "none", "drift-up", "drift-down"}},
			"transition":          map[string]any{"enum": []string{"cut", "fade", "slide", "wipe", "flip", "clock-wipe", "iris"}},
			"transition_duration": map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 3},
		},
	}
}

func explainerAudioSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "minProperties": 1, "properties": map[string]any{
		"narration": map[string]any{"description": "the narration file, or {src, volume}"},
		"music":     map[string]any{"type": "object", "description": "{src, volume, loop, offset_seconds, fade_in_seconds, fade_out_seconds, duck_under_narration (needs timing_path), duck_level}"},
		"sfx":       map[string]any{"type": "array", "items": objectSchema([]string{"src", "at_seconds"}, map[string]any{"src": stringSchema(), "at_seconds": map[string]any{"type": "number", "minimum": 0}, "gain_db": numberSchema()})},
	}}
}

func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": stringSchema()}
}
func numberArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "number"}}
}
func mediaOutputResultSchema(extra []string) map[string]any {
	required := []string{"output", "duration", "requested_operations", "realized_operations", "output_facts"}
	required = append(required, extra...)
	properties := map[string]any{"output": stringSchema(), "duration": map[string]any{"type": "number"}, "requested_operations": stringArraySchema(), "realized_operations": stringArraySchema(), "output_facts": map[string]any{"type": "object"}}
	for _, name := range extra {
		if name == "loudnorm" {
			properties[name] = map[string]any{"type": "object"}
		} else {
			properties[name] = map[string]any{"type": "integer"}
		}
	}
	return objectSchema(required, properties)
}

func audioOperationSchema(music bool) map[string]any {
	properties := map[string]any{"gain_db": map[string]any{"type": "number"}, "fade_in": map[string]any{"type": "number", "minimum": 0}, "fade_out": map[string]any{"type": "number", "minimum": 0}}
	if music {
		properties["input"] = map[string]any{"type": "string", "minLength": 1}
		properties["ducking"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"enabled"}, "properties": map[string]any{"enabled": map[string]any{"type": "boolean"}, "threshold": map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 1}, "ratio": map[string]any{"type": "number", "exclusiveMinimum": 1, "maximum": 20}}}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
}

func execute(tool, op string, data []byte) (any, []string, error) {
	return executeContext(context.Background(), tool, op, data)
}

func executeContext(ctx context.Context, tool, op string, data []byte) (any, []string, error) {
	if narrationRequested(tool, data) {
		return doNarrationContext(ctx, tool, op, data)
	}
	switch tool {
	case "media_probe":
		return doMediaProbeContext(ctx, op, data)
	case "audio_probe":
		return doAudioProbeContext(ctx, op, data)
	case "frame_sample":
		return doFrameSampleContext(ctx, op, data)
	case "scene_detect":
		return doSceneDetectContext(ctx, op, data)
	case "visual_qa":
		return doVisualQAContext(ctx, op, data)
	case "output_review":
		return doOutputReviewContext(ctx, op, data)
	case "source_edit":
		return doSourceEditContext(ctx, op, data)
	case "video_trimmer":
		return doVideoTrimmerContext(ctx, op, data)
	case "video_stitch":
		return doVideoStitchContext(ctx, op, data)
	case "video_compose":
		return doVideoComposeContext(ctx, op, data)
	case "subtitle_gen":
		return doSubtitleGenContext(ctx, op, data)
	case "ffmpeg_caption_burn":
		return doFFmpegCaptionBurnContext(ctx, op, data)
	case "silence_cutter":
		return doSilenceCutterContext(ctx, op, data)
	case "hyperframes_compose":
		return doHyperFramesComposeContext(ctx, op, data)
	case "audio_mix":
		return doAudioMixContext(ctx, op, data)
	case "music_library":
		return doMusicLibraryContext(ctx, op, data)
	case "direct_clip_search":
		return doDirectClipSearchContext(ctx, op, data)
	case "pexels_video":
		return doPexelsVideoContext(ctx, op, data)
	case "pixabay_video":
		return doPixabayVideoContext(ctx, op, data)
	case "wikimedia":
		return doWikimediaContext(ctx, op, data)
	case "edge_tts":
		return doEdgeTTSContext(ctx, op, data)
	case "openai_tts":
		return doOpenAITTSContext(ctx, op, data)
	case "elevenlabs_tts":
		return doElevenLabsTTSContext(ctx, op, data)
	case "piper_tts":
		return doPiperTTSContext(ctx, op, data)
	case "openai_image":
		return doOpenAIImageContext(ctx, op, data)
	case "flux_image":
		return doFluxImageContext(ctx, op, data)
	case "kling_video":
		return doKlingVideoContext(ctx, op, data)
	case "sora_video":
		return doSoraVideoContext(ctx, op, data)
	case "gflow_video":
		return doGFlowVideoContext(ctx, op, data)
	case "gflow_image":
		return doGFlowImageContext(ctx, op, data)
	case "color_grade":
		return doColorGradeContext(ctx, op, data)
	case "image_selector":
		return doImageSelector(op, data)
	case "video_selector":
		return doVideoSelector(op, data)
	case "script_check":
		return doScriptCheckContext(ctx, op, data)
	case "plan_check":
		return doPlanCheckContext(ctx, op, data)
	}
	return nil, nil, failure("invalid_request", "unknown tool: "+tool, nil)
}

func decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return failure("invalid_request", decodeMessage(err, dst),
			map[string]any{"error": bounded(err.Error())})
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return failure("invalid_request", "request must contain one JSON object", nil)
	}
	return nil
}

// decodeMessage says which of three different problems occurred.
//
// "invalid request JSON" covered all of them, and for two it was actively
// wrong: a request with an unknown field is perfectly valid JSON that this tool
// does not accept, and a type mismatch is valid JSON with a wrong value. An
// agent told its JSON is invalid re-serializes correct JSON, fails again, and
// concludes the module is broken — which was observed: one abandoned Facet
// after this error and fell back to raw ffmpeg, producing a blank video it
// reported as a success.
// It also NAMES the fields the tool does accept. Telling a caller to go run
// `tools describe` costs a round trip and assumes it can; the accepted names
// are already in the destination struct, and one of them is usually the one
// meant. This matters because the input field is not spelled the same across
// tools — media_probe takes `input`, video_trimmer takes `input_path`,
// audio_mix takes `source` — so a caller that learned one tool guesses wrong
// on the next. Observed: two consecutive wrong guesses on a tool I own.
func decodeMessage(err error, dst any) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "unknown field"):
		field := text
		if i := strings.Index(text, "unknown field "); i >= 0 {
			field = strings.TrimSpace(text[i+len("unknown field "):])
		}
		msg := "this tool does not accept the field " + field
		if accepted := acceptedFields(dst); accepted != "" {
			msg += "; it accepts " + accepted
		}
		// The field list is the likely fix; describe stays as the authority
		// on types, enums and which combinations are valid.
		return msg + " (see `facet tools describe <tool>` for the full schema)"
	case strings.Contains(text, "cannot unmarshal"):
		return wrongTypeMessage(text)
	}
	return "request body is not valid JSON"
}

// wrongTypeMessage names the FIELD and the type it wants.
//
// Go's own error carries both but wraps them in internal struct naming:
//
//	json: cannot unmarshal object into Go struct field
//	stitchRequest.clips of type string
//
// A caller does not know what a stitchRequest is, and the sentence reads as
// implementation noise around the two words that matter — "clips" and
// "string". Verified across five tools whose requests I got wrong: every one
// reported "a field has the wrong type" and left the caller to parse the rest.
//
// Falls back to the raw text when the shape is unfamiliar rather than
// discarding information it cannot parse.
func wrongTypeMessage(text string) string {
	const fieldMarker = "Go struct field "
	const typeMarker = " of type "
	i := strings.Index(text, fieldMarker)
	j := strings.Index(text, typeMarker)
	if i < 0 || j < 0 || j < i {
		return "a field has the wrong type: " + bounded(text)
	}
	field := text[i+len(fieldMarker) : j]
	// Strip the internal struct name: "stitchRequest.clips" -> "clips".
	if dot := strings.LastIndex(field, "."); dot >= 0 {
		field = field[dot+1:]
	}
	want := describeType(strings.TrimSpace(text[j+len(typeMarker):]))

	got := ""
	if k := strings.Index(text, "cannot unmarshal "); k >= 0 {
		rest := text[k+len("cannot unmarshal "):]
		if sp := strings.Index(rest, " "); sp > 0 {
			got = rest[:sp]
		}
	}
	if field == "" || want == "" {
		return "a field has the wrong type: " + bounded(text)
	}
	if got != "" {
		return fmt.Sprintf("field %q must be %s, but %s was given; "+
			"run `facet tools describe <tool>` for its request schema", field, want, got)
	}
	return fmt.Sprintf("field %q must be %s; "+
		"run `facet tools describe <tool>` for its request schema", field, want)
}

// describeType turns a Go type name into something a caller can act on.
//
// "toolbox.stockQueryItem" names an internal struct and tells a caller
// nothing; "an object" at least says what shape to send, and the describe
// pointer beside it carries the detail. Primitive names pass through, because
// "string" and "number" mean exactly what they say.
func describeType(goType string) string {
	switch goType {
	case "string", "bool":
		return goType
	case "int", "int64", "float64":
		return "number"
	}
	if strings.HasPrefix(goType, "[]") {
		return "an array of " + describeType(strings.TrimPrefix(goType, "[]"))
	}
	if strings.HasPrefix(goType, "map[") {
		return "an object"
	}
	// A package-qualified name is an internal struct; say what shape it is.
	if strings.Contains(goType, ".") {
		return "an object"
	}
	return goType
}

// acceptedFields lists the JSON names a request struct accepts, so a rejection
// carries the answer rather than only the complaint.
func acceptedFields(dst any) string {
	t := reflect.TypeOf(dst)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return ""
	}
	var names []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, ", ")
}

func positiveTimeout(v int, fallback int) (time.Duration, error) {
	if v < 0 {
		return 0, failure("invalid_request", "timeout_seconds must be positive", nil)
	}
	if v == 0 {
		v = fallback
	}
	return time.Duration(v) * time.Second, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func inputPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return failure("invalid_request", "input path is required", nil)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return failure("input_not_found", "input does not exist", map[string]any{"path": path})
	}
	if err != nil {
		return failure("invalid_request", "input cannot be accessed", map[string]any{"path": path, "error": bounded(err.Error())})
	}
	if info.IsDir() {
		return failure("invalid_request", "input must be a file", map[string]any{"path": path})
	}
	return nil
}

func outputPath(path string, overwrite bool, estimate bool) error {
	if strings.TrimSpace(path) == "" {
		return failure("invalid_request", "output path is required", nil)
	}
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return failure("output_conflict", "output already exists", map[string]any{"path": path})
		}
	}
	if !estimate {
		parent := filepath.Dir(path)
		if parent == "." {
			return nil
		}
		if err := os.MkdirAll(parent, 0755); err != nil {
			return failure("command_failed", "output directory could not be created", map[string]any{"error": bounded(err.Error())})
		}
	}
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func temporaryOutput(path string) (string, func(), error) {
	ext := filepath.Ext(path)
	file, err := os.CreateTemp(filepath.Dir(path), ".facet-*"+ext)
	if err != nil {
		return "", nil, failure("command_failed", "temporary output could not be created", map[string]any{"error": bounded(err.Error())})
	}
	temp := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(temp)
		return "", nil, failure("command_failed", "temporary output could not be closed", map[string]any{"error": bounded(err.Error())})
	}
	if err := os.Remove(temp); err != nil {
		return "", nil, failure("command_failed", "temporary output could not be prepared", map[string]any{"error": bounded(err.Error())})
	}
	return temp, func() { _ = os.Remove(temp) }, nil
}

func finalizeOutput(temp, output string, overwrite bool) error {
	if !overwrite {
		if err := os.Rename(temp, output); err != nil {
			return failure("command_failed", "temporary output could not be finalized", map[string]any{"error": bounded(err.Error())})
		}
		return nil
	}
	backupFile, err := os.CreateTemp(filepath.Dir(output), ".facet-backup-*")
	if err != nil {
		return failure("command_failed", "replacement backup could not be prepared", map[string]any{"error": bounded(err.Error())})
	}
	backup := backupFile.Name()
	if err = backupFile.Close(); err != nil {
		_ = os.Remove(backup)
		return failure("command_failed", "replacement backup could not be closed", map[string]any{"error": bounded(err.Error())})
	}
	if err = os.Remove(backup); err != nil {
		return failure("command_failed", "replacement backup could not be reserved", map[string]any{"error": bounded(err.Error())})
	}
	hadOutput := false
	if _, err = os.Stat(output); err == nil {
		hadOutput = true
		if err = os.Rename(output, backup); err != nil {
			return failure("command_failed", "existing output could not be preserved", map[string]any{"error": bounded(err.Error())})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return failure("command_failed", "existing output could not be inspected", map[string]any{"error": bounded(err.Error())})
	}
	if err := os.Rename(temp, output); err != nil {
		if hadOutput {
			if rollbackErr := os.Rename(backup, output); rollbackErr != nil {
				return failure("command_failed", "temporary output could not be finalized and preserved output rollback failed", map[string]any{"error": bounded(err.Error()), "rollback_error": bounded(rollbackErr.Error()), "backup": backup})
			}
		}
		return failure("command_failed", "temporary output could not be finalized; preserved output was restored", map[string]any{"error": bounded(err.Error())})
	}
	if hadOutput {
		_ = os.Remove(backup)
	}
	return nil
}

func publishFileSet(staged, outputs []string, overwrite bool) error {
	backups := make([]string, len(outputs))
	published := 0
	rollback := func() {
		for i := 0; i < published; i++ {
			_ = os.Remove(outputs[i])
		}
		for i, backup := range backups {
			if backup != "" {
				_ = os.Rename(backup, outputs[i])
			}
		}
	}
	if overwrite {
		for i, output := range outputs {
			if _, err := os.Stat(output); err == nil {
				backupFile, createErr := os.CreateTemp(filepath.Dir(output), ".facet-frame-backup-*")
				if createErr != nil {
					rollback()
					return failure("command_failed", "frame backup could not be prepared", map[string]any{"path": output, "error": bounded(createErr.Error())})
				}
				backup := backupFile.Name()
				closeErr := backupFile.Close()
				removeErr := os.Remove(backup)
				if closeErr != nil || removeErr != nil {
					rollback()
					return failure("command_failed", "frame backup could not be reserved", map[string]any{"path": output})
				}
				if err = os.Rename(output, backup); err != nil {
					rollback()
					return failure("command_failed", "existing frame set could not be preserved", map[string]any{"path": output, "error": bounded(err.Error())})
				}
				backups[i] = backup
			} else if !errors.Is(err, os.ErrNotExist) {
				rollback()
				return failure("command_failed", "existing frame could not be inspected", map[string]any{"path": output, "error": bounded(err.Error())})
			}
		}
	}
	for i := range staged {
		if err := os.Rename(staged[i], outputs[i]); err != nil {
			rollback()
			return failure("command_failed", "frame set could not be published", map[string]any{"path": outputs[i], "error": bounded(err.Error())})
		}
		published++
	}
	for _, backup := range backups {
		if backup != "" {
			_ = os.Remove(backup)
		}
	}
	return nil
}

func bounded(s string) string {
	if len(s) <= maxDiagnostic {
		return s
	}
	return s[len(s)-maxDiagnostic:]
}

func runCommand(timeout time.Duration, program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runCommandContext(ctx, program, args...)
}

// newCommand prepares every subprocess the toolbox starts.
//
// Cancelling ctx, or its deadline passing, kills the program AND everything
// it started: node's headless browser, a wrapper's ffmpeg. os/exec alone
// kills only the direct child, which left renderers running after their
// render had been abandoned. When the command finishes, anything it left
// running is killed too, and Wait gives up after WaitDelay on pipes a
// descendant still holds. See internal/proctree.
func newCommand(ctx context.Context, path string, args ...string) *proctree.Cmd {
	return proctree.CommandContext(ctx, path, args...)
}

// exitedCleanly reports a command whose own process succeeded but whose
// output pipes stayed open past WaitDelay because a descendant still held
// them. The program finished its work; the straggler has been killed with
// the rest of its tree, so the run is not a failure.
func exitedCleanly(cmd *proctree.Cmd, err error) bool {
	return errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success()
}

// timeoutMessage says what ran out of time and what to do about it.
//
// "node was cancelled or timed out" names the binary rather than the work and
// suggests nothing. An agent reading it cannot tell whether the render was
// impossible or merely given four seconds too few — and the honest answer is
// usually the latter. The other budget that can run out is the MCP client's
// own limit on one call, which Facet cannot raise; the shell route
// (facet tools run) has no such limit.
func timeoutMessage(program string, budget time.Duration) string {
	if budget > 0 {
		return fmt.Sprintf(
			"%s did not finish within %s; raise timeout_seconds, or run the call "+
				"with facet tools run from a shell if the MCP client's own call "+
				"limit is the smaller budget", program, budget)
	}
	return program + " did not finish in the time allowed; raise timeout_seconds, " +
		"or run the call with facet tools run from a shell if the MCP client's " +
		"own call limit is the smaller budget"
}

func runCommandContext(ctx context.Context, program string, args ...string) ([]byte, error) {
	resolved, err := lookPath(program)
	if err != nil {
		return nil, failure("dependency_missing", program+" is not available", nil)
	}
	// Recorded before the run so the message can name the budget that expired.
	var budget time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	cmd := newCommand(ctx, resolved, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, failure("command_timeout", timeoutMessage(program, budget.Round(time.Second)), map[string]any{"stderr": bounded(stderr.String())})
	}
	if err != nil && !exitedCleanly(cmd, err) {
		return nil, failure("command_failed", program+" failed", map[string]any{"stderr": bounded(stderr.String()), "error": bounded(err.Error())})
	}
	return append(stdout.Bytes(), stderr.Bytes()...), nil
}

func runCommandDirContext(parent context.Context, timeout time.Duration, dir, program string, args ...string) ([]byte, error) {
	stdout, stderr, err := runCommandDirOutput(parent, timeout, dir, program, args...)
	if err != nil {
		return stdout, err
	}
	return append(stdout, stderr...), nil
}

// runCommandDirOutput is runCommandDirContext with standard output and
// standard error kept apart, for programs whose stdout is a JSON report that
// diagnostics on stderr would otherwise corrupt. On a timeout the bounded
// stdout seen so far is returned with the error.
func runCommandDirOutput(parent context.Context, timeout time.Duration, dir, program string, args ...string) ([]byte, []byte, error) {
	return runCommandDirOutputEnv(parent, timeout, dir, nil, program, args...)
}

// runCommandDirOutputEnv is runCommandDirOutput with the program's
// environment; nil inherits this process's.
func runCommandDirOutputEnv(parent context.Context, timeout time.Duration, dir string, env []string, program string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	resolved, err := lookPath(program)
	if err != nil {
		return nil, nil, failure("dependency_missing", program+" is not available", nil)
	}
	cmd := newCommand(ctx, resolved, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return []byte(bounded(stdout.String())), nil, failure("command_timeout", timeoutMessage(program, timeout), map[string]any{"stderr": bounded(stderr.String())})
	}
	if err != nil && !exitedCleanly(cmd, err) {
		return nil, nil, failure("command_failed", program+" failed", map[string]any{"stderr": bounded(stderr.String()), "error": bounded(err.Error()), "output": bounded(stdout.String())})
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func parseFloat(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

func estimateResult(ops []string) map[string]any {
	return map[string]any{"estimated_cost": 0.0, "network": false, "external_write": false, "side_effect_free": true, "operations": ops}
}

// A Remotion render costs a large FIXED setup plus a per-frame cost. Both are
// measured, not assumed:
//
//	 30 frames  640x360    5.0s
//	120 frames 1280x720   16.5s
//	450 frames 1280x720   42.7s
//
// Fitting the two 720p points gives ~79ms per 720p frame and ~7s of fixed
// bundling and browser startup. A per-frame-only model predicted 1.4s for a
// render that actually took 16.5s, because at small sizes the setup dominates
// completely — which is precisely the case where a caller most needs to know
// the answer is "seconds, not instant".
const renderFixedSeconds = 7.0
const renderSecondsPerFrame = 0.0794
const referencePixels = 1280 * 720

// renderLoadFactor is how much slower the same render runs under contention.
// The 450-frame render took 42.7s idle and 170.9s while other work was
// running, so a caller choosing a deadline needs the pessimistic figure.
const renderLoadFactor = 4.0

// ShortestMCPCallLimit is the shortest default limit that an MCP client facet
// wire supports puts on a single tool call: Codex stops an MCP call after
// mcp_servers.<id>.tool_timeout_sec, 60 seconds unless configured.
//
// Facet does not own this number and cannot enforce it. It is needed here
// because an estimate must say whether a call should leave MCP BEFORE any call
// is made: a call that may outlast the client's limit belongs on the shell
// route (facet tools run), which no MCP limit applies to.
const ShortestMCPCallLimit = 60 * time.Second

// MCPCallMargin is reserved from that limit so Facet can still return a result
// after a tool gives up. A tool returning exactly at the limit is abandoned
// before its result or error reaches the client.
//
// Measured: a whole invocation completes in ~0.1s and sha256 over a 200MB
// artifact takes 0.15s, so one second is roughly 5x the worst case observed.
const MCPCallMargin = time.Second

// FitsShortestMCPCallLimit is the longest render that still answers inside the
// shortest default MCP call limit.
//
// DERIVED, never written as a literal. An earlier threshold was hardcoded 55,
// correct when the margin was 5s; the margin later dropped to 1s and the
// literal did not follow, so the estimate warned about renders that had four
// spare seconds. That is the duplicated-constant defect this codebase fixed in
// chargeability and determinism, surviving in the estimate because a stale
// number stays plausible in a way a stale list does not.
var FitsShortestMCPCallLimit = ShortestMCPCallLimit - MCPCallMargin

// estimateRender adds an expected wall-clock duration to an estimate.
//
// Without it a caller cannot tell that a 15-second explainer takes 43 seconds
// to render, and may not finish inside an MCP client's 60-second call limit.
// prefer_shell turns that into the decision the caller faces: run the render
// as an MCP call, or from a shell with facet tools run.
func estimateRender(ops []string, frames int, width, height int) map[string]any {
	out := estimateResult(ops)
	if frames <= 0 || width <= 0 || height <= 0 {
		return out
	}
	scale := float64(width*height) / float64(referencePixels)
	expected := renderFixedSeconds + float64(frames)*renderSecondsPerFrame*scale
	out["estimated_duration_seconds"] = roundFloat(expected, 1)
	out["estimated_duration_seconds_max"] = roundFloat(expected*renderLoadFactor, 1)
	// True means: run it from a shell (facet tools run), not as an MCP call.
	out["prefer_shell"] = expected*renderLoadFactor > FitsShortestMCPCallLimit.Seconds()
	return out
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

func parseLoudnorm(s string) map[string]any {
	start := strings.LastIndex(s, "{\n")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		var m map[string]any
		if json.Unmarshal([]byte(s[start:end+1]), &m) == nil {
			return m
		}
	}
	return map[string]any{}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// Names returns a defensive copy of the exact public tool catalog.
func Names() []string { out := append([]string(nil), names...); sort.Strings(out); return out }

// CanonicalName resolves a tool name, including the hyphenated and short
// spellings the CLI accepts, to the identity reported by envelopes and
// listings. A name that is not a Facet tool is returned normalized but
// unchanged.
func CanonicalName(tool string) string { return canonicalToolName(tool) }

// Description returns the tool's human-readable capability description.
func Description(tool string) string {
	tool = canonicalToolName(tool)
	if desc, ok := capabilities[tool]; ok {
		return desc
	}
	return tool
}

// Parameters returns the JSON Schema for the tool's input parameters.
func Parameters(tool string) map[string]any {
	tool = canonicalToolName(tool)
	if s, ok := schemas[tool]; ok {
		if m, ok := s.(map[string]any); ok {
			return m
		}
	}
	return map[string]any{"type": "object"}
}

// ValidateRequest validates a concrete request through the canonical estimate
// path. Estimates execute no production work, but they do enforce semantic
// requirements such as real input files and operation-specific constraints.
func ValidateRequest(tool string, data []byte) error {
	tool = canonicalToolName(tool)
	if !known(tool) {
		return fmt.Errorf("unknown canonical operation %q", tool)
	}
	_, _, err := execute(tool, "estimate", data)
	return err
}

// ValidateRequestShape validates a request against the canonical JSON schema
// without requiring intermediate files to exist yet.
func ValidateRequestShape(tool string, data []byte) error {
	tool = canonicalToolName(tool)
	schema, ok := schemas[tool]
	if !ok {
		return fmt.Errorf("unknown canonical operation %q", tool)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if reason := schemaMismatch(schema, value, "$"); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

func schemaMismatch(rawSchema, value any, path string) string {
	schema, ok := rawSchema.(map[string]any)
	if !ok {
		return ""
	}
	if branches, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, branch := range branches {
			if schemaMismatch(branch, value, path) == "" {
				matched = true
				break
			}
		}
		if !matched {
			return path + " does not satisfy any allowed request shape"
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, branch := range branches {
			if schemaMismatch(branch, value, path) == "" {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Sprintf("%s matches %d oneOf request shapes", path, matches)
		}
	}
	if condition, ok := schema["if"]; ok && schemaMismatch(condition, value, path) == "" {
		if thenSchema, ok := schema["then"]; ok {
			if reason := schemaMismatch(thenSchema, value, path); reason != "" {
				return reason
			}
		}
	}
	if expected, ok := schema["const"]; ok && !schemaValueEqual(expected, value) {
		return fmt.Sprintf("%s must equal %v", path, expected)
	}
	if values, ok := schema["enum"].([]string); ok {
		text, _ := value.(string)
		if !contains(values, text) {
			return fmt.Sprintf("%s must be one of %v", path, values)
		}
	}
	if _, hasType := schema["type"]; !hasType {
		if required := schemaStrings(schema["required"]); len(required) != 0 {
			if _, ok := value.(map[string]any); !ok {
				return path + " must be an object"
			}
		}
	}
	if schema["type"] == "object" {
		if _, ok := value.(map[string]any); !ok {
			return path + " must be an object"
		}
	}
	// Object keywords apply to every object value whether or not the schema
	// names a type, as in JSON Schema: an if/then condition such as
	// {"properties": {"mock": {"const": false}}} has no type and must still
	// look at the property.
	if object, ok := value.(map[string]any); ok {
		for _, name := range schemaStrings(schema["required"]) {
			if _, exists := object[name]; !exists {
				return path + "." + name + " is required"
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(object))
		for name := range object {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			childSchema, exists := properties[name]
			if !exists {
				if schema["additionalProperties"] == false {
					return path + "." + name + " is not allowed"
				}
				continue
			}
			if reason := schemaMismatch(childSchema, object[name], path+"."+name); reason != "" {
				return reason
			}
		}
	}
	switch schema["type"] {
	case "array":
		items, ok := value.([]any)
		if !ok {
			return path + " must be an array"
		}
		if min, ok := numberValue(schema["minItems"]); ok && float64(len(items)) < min {
			return fmt.Sprintf("%s must contain at least %.0f items", path, min)
		}
		if itemSchema, ok := schema["items"]; ok {
			for i, item := range items {
				if reason := schemaMismatch(itemSchema, item, fmt.Sprintf("%s[%d]", path, i)); reason != "" {
					return reason
				}
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return path + " must be a string"
		}
		if min, ok := numberValue(schema["minLength"]); ok && float64(len(text)) < min {
			return fmt.Sprintf("%s must contain at least %.0f characters", path, min)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Sprintf("%s has an invalid pattern in its schema: %v", path, err)
			}
			if !re.MatchString(text) {
				return fmt.Sprintf("%s must match %s", path, pattern)
			}
		}
	case "integer":
		number, ok := numberValue(value)
		if !ok || math.Trunc(number) != number {
			return path + " must be an integer"
		}
		if reason := numericMismatch(schema, number, path); reason != "" {
			return reason
		}
	case "number":
		number, ok := numberValue(value)
		if !ok {
			return path + " must be a number"
		}
		if reason := numericMismatch(schema, number, path); reason != "" {
			return reason
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return path + " must be a boolean"
		}
	}
	return ""
}

func numericMismatch(schema map[string]any, number float64, path string) string {
	if minimum, ok := numberValue(schema["minimum"]); ok && number < minimum {
		return fmt.Sprintf("%s must be at least %v", path, minimum)
	}
	if minimum, ok := numberValue(schema["exclusiveMinimum"]); ok && number <= minimum {
		return fmt.Sprintf("%s must be greater than %v", path, minimum)
	}
	if maximum, ok := numberValue(schema["maximum"]); ok && number > maximum {
		return fmt.Sprintf("%s must be at most %v", path, maximum)
	}
	if maximum, ok := numberValue(schema["exclusiveMaximum"]); ok && number >= maximum {
		return fmt.Sprintf("%s must be less than %v", path, maximum)
	}
	if multiple, ok := numberValue(schema["multipleOf"]); ok && math.Mod(number, multiple) != 0 {
		return fmt.Sprintf("%s must be a multiple of %v", path, multiple)
	}
	return ""
}

func schemaStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case float64:
		return number, true
	}
	return 0, false
}

func schemaValueEqual(left, right any) bool {
	if l, ok := numberValue(left); ok {
		r, rok := numberValue(right)
		return rok && l == r
	}
	return reflect.DeepEqual(left, right)
}

// Run executes a tool operation in 'run' mode with raw JSON input.
func Run(tool string, data []byte) Envelope {
	return RunContext(context.Background(), tool, data)
}

// RunContext runs a tool with raw JSON input.
//
// Cancelling ctx stops the run: network requests are abandoned and every
// subprocess the tool started is killed with its whole process tree. A
// successful envelope lists every file the run produced in Artifacts.
func RunContext(ctx context.Context, tool string, data []byte) Envelope {
	tool = canonicalToolName(tool)
	if err := ctx.Err(); err != nil {
		return errorEnvelope(tool, "run", failure("cancelled", err.Error(), nil))
	}
	if !known(tool) {
		return errorEnvelope(tool, "run", failure("unknown_tool", "unknown tool: "+tool, nil))
	}
	return runEnvelope(ctx, tool, data)
}
