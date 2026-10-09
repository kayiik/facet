package toolbox

import "sort"

// Operation catalogue consumed by internal/routes.
//
// DERIVED, never a second table. Every field here reads the same canonical
// source the rest of the toolbox reads: MayCharge, Deterministic, executionFor
// and summary's dependency list. A second authoritative effects table is the
// duplicated-constant defect this repo has already paid for twice — in
// chargeability and in determinism — and a projection is exactly where it
// would reappear, because the two layers look independent.
//
// The canonical tool NAME is the Operation ID.

// V2Effects is an Operation's declared effects.
//
// may_charge and cost_known are INDEPENDENT and neither is inferred from the
// other. Five Facet tools are networked and free (edge_tts, pexels_video,
// pixabay_video, wikimedia, direct_clip_search): cost known, amount zero, no
// charge. Eight may charge and their amount is unknowable before the call.
type V2Effects struct {
	Network       bool `json:"network"`
	ExternalWrite bool `json:"external_writes"`
	MayCharge     bool `json:"may_charge"`
	CostKnown     bool `json:"cost_known"`
	Deterministic bool `json:"deterministic"`
}

// V2Requirement is one thing an Operation needs, with how badly it needs it.
type V2Requirement struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Strength is "mandatory" or "preferred". A preferred requirement that is
	// unsatisfied degrades the Operation rather than blocking it.
	Strength string `json:"strength"`
	// Resolution is SATISFIED, UNSATISFIED or UNKNOWN — never collapsed to a
	// boolean. A present credential resolves UNKNOWN because holding a key is
	// not proof the provider will serve you.
	Resolution string `json:"resolution"`
}

// V2Operation is the semantic unit: what this Operation MEANS, as opposed to
// what the capability surface exposes.
type V2Operation struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Effects      V2Effects       `json:"effects"`
	Requirements []V2Requirement `json:"requirements"`
	// Produces names the artifact kinds the Operation emits, which routes use
	// to bind one Operation's output to the next one's input.
	Produces []string `json:"produces,omitempty"`
	// Implementations are the interchangeable ways this Operation runs. More
	// than one is a multi-Implementation Operation, NOT a Composite: a
	// Composite would require a typed child-Operation graph, and selecting
	// between renderers is not that.
	Implementations []string `json:"implementations,omitempty"`
}

// preferredRequirements are dependencies whose absence degrades an Operation
// instead of blocking it.
//
// These were previously expressed as hardcoded overrides — music_library set
// configured=true after declaring ffprobe, and direct_clip_search did the same
// — written before there was a vocabulary for strength. The override WAS the
// strength; this gives it a name.
var preferredRequirements = map[string]map[string]bool{
	"music_library":      {"ffprobe": true},
	"direct_clip_search": {"ffmpeg": true, "ffprobe": true},
}

// operationImplementations records where one Operation has several
// interchangeable runners. Both are local and neither bills, so the choice
// changes no declared effect — which is why it is an Implementation list
// rather than separate Operations.
var operationImplementations = map[string][]string{
	"video_compose": {"remotion", "ffmpeg"},
}

// operationProduces maps an Operation to the artifact kinds it emits.
//
// Only Operations that actually write a file appear. A read-only Operation
// produces nothing, and declaring a kind it never emits would be the
// declared-but-never-produced defect from the other direction.
var operationProduces = map[string][]string{
	"video_compose":       {"render_video"},
	"hyperframes_compose": {"render_video"},
	"ffmpeg_caption_burn": {"render_video"},
	"video_trimmer":       {"render_video"},
	"video_stitch":        {"render_video"},
	"silence_cutter":      {"render_video"},
	"source_edit":         {"render_video"},
	"color_grade":         {"render_video"},
	"frame_sample":        {"frame"},
	"scene_detect":        {"frame"},
	"visual_qa":           {"frame"},
	"output_review":       {"frame"},
	"edge_tts":            {"narration"},
	"openai_tts":          {"narration"},
	"elevenlabs_tts":      {"narration"},
	"piper_tts":           {"narration"},
	"audio_mix":           {"render_video"},
	"music_library":       {"narration"},
	"subtitle_gen":        {"captions"},
	"openai_image":        {"image"},
	"flux_image":          {"image"},
	"gflow_image":         {"image"},
	"image_selector":      {"image"},
	"sora_video":          {"render_video"},
	"kling_video":         {"render_video"},
	"gflow_video":         {"render_video"},
	"pexels_video":        {"render_video"},
	"pixabay_video":       {"render_video"},
	"wikimedia":           {"image", "render_video"},
	"direct_clip_search":  {"render_video"},
	"video_selector":      {"render_video"},
}

// V2Operations projects every public tool as an Operation.
//
// Sorted so the catalogue is byte-stable: an unordered map would make two
// identical builds produce different bytes and defeat digest comparison.
func V2Operations() []V2Operation {
	names := Names()
	sort.Strings(names)

	out := make([]V2Operation, 0, len(names))
	for _, n := range names {
		exec := executionFor(n)
		out = append(out, V2Operation{
			ID:    n,
			Title: capabilities[n],
			Effects: V2Effects{
				Network:       exec.Network,
				ExternalWrite: externalWriteFor(n, "run"),
				MayCharge:     MayCharge(n),
				// CostKnown asks whether a NUMERIC AMOUNT is known, never
				// whether money may be spent. executionFor supplies a non-nil
				// estimate exactly when the amount is known.
				CostKnown:     exec.EstimatedCost != nil,
				Deterministic: Deterministic(n),
			},
			Requirements:    v2RequirementsFor(n),
			Produces:        operationProduces[n],
			Implementations: operationImplementations[n],
		})
	}
	return out
}

// v2RequirementsFor derives requirements from the SAME dependency list the
// tool listing publishes, so the two can never disagree.
func v2RequirementsFor(tool string) []V2Requirement {
	raw, _ := summary(tool)["dependencies"].([]any)
	out := make([]V2Requirement, 0, len(raw))
	pref := preferredRequirements[tool]
	for _, d := range raw {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		typ, _ := m["type"].(string)
		res, _ := m["resolution"].(string)
		strength := "mandatory"
		if pref[name] {
			strength = "preferred"
		}
		out = append(out, V2Requirement{
			Name: name, Kind: typ, Strength: strength, Resolution: res,
		})
	}
	return out
}
