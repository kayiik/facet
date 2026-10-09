package toolbox

import "sort"

// Meta is what a tool offers to production planning: the capabilities it
// provides, whether it runs locally, online for free, or spends the operator's
// credits, the note shown before a paid call, and the bundled knowledge to read
// before using it.
type Meta struct {
	Capabilities []string `json:"capabilities"`
	Runtime      string   `json:"runtime"`
	CreditNote   string   `json:"credit_note,omitempty"`
	Knowledge    []string `json:"knowledge,omitempty"`
}

// Runtimes a tool can have.
const (
	RuntimeLocal  = "local"  // runs on this machine, no network, free
	RuntimeOnline = "online" // reaches a service, free
	RuntimePaid   = "paid"   // spends the operator's credits
)

const (
	kRemotion   = "guidance/runtimes/remotion.md"
	kModes      = "guidance/runtimes/composition-modes.md"
	kScenes     = "guidance/runtimes/scene-types.md"
	kHyperRT    = "guidance/runtimes/hyperframes.md"
	kFFmpegRT   = "guidance/runtimes/ffmpeg.md"
	kVideoGen   = "guidance/craft/video-generation-prompting.md"
	kImageGen   = "guidance/craft/image-generation.md"
	kVoice      = "guidance/craft/voice-performance.md"
	kSound      = "guidance/craft/sound-design.md"
	kCaptions   = "guidance/craft/captions.md"
	kGrade      = "guidance/craft/color-grading.md"
	kEditing    = "guidance/craft/editing.md"
	kStock      = "guidance/craft/broll-and-stock.md"
	kReview     = "guidance/stages/review.md"
	kScript     = "guidance/stages/script.md"
	kScenePlan  = "guidance/stages/scene_plan.md"
	vRemotion   = "guidance/vendor/remotion-best-practices/SKILL.md"
	vHyper      = "guidance/vendor/hyperframes/SKILL.md"
	vVideoGen   = "guidance/vendor/ai-video-gen/SKILL.md"
	vFlux       = "guidance/vendor/flux-best-practices/SKILL.md"
	vTTS        = "guidance/vendor/text-to-speech/SKILL.md"
	vElevenLabs = "guidance/vendor/elevenlabs/SKILL.md"
	vMusic      = "guidance/vendor/music/SKILL.md"
)

var toolMeta = map[string]Meta{
	"media_probe":   {Capabilities: []string{"analysis"}, Knowledge: []string{kEditing}},
	"audio_probe":   {Capabilities: []string{"analysis"}, Knowledge: []string{kSound}},
	"frame_sample":  {Capabilities: []string{"analysis", "review"}, Knowledge: []string{kReview}},
	"scene_detect":  {Capabilities: []string{"analysis"}, Knowledge: []string{kEditing, kFFmpegRT}},
	"visual_qa":     {Capabilities: []string{"review"}, Knowledge: []string{kReview}},
	"output_review": {Capabilities: []string{"review"}, Knowledge: []string{kReview}},
	"script_check":  {Capabilities: []string{"studio_checks"}, Knowledge: []string{kScript}},
	"plan_check":    {Capabilities: []string{"studio_checks"}, Knowledge: []string{kScenePlan}},

	"edge_tts":       {Capabilities: []string{"voice"}, Knowledge: []string{kVoice, vTTS}},
	"piper_tts":      {Capabilities: []string{"voice"}, Knowledge: []string{kVoice, vTTS}},
	"openai_tts":     {Capabilities: []string{"voice"}, CreditNote: "Uses your OpenAI API credits.", Knowledge: []string{kVoice, vTTS}},
	"elevenlabs_tts": {Capabilities: []string{"voice"}, CreditNote: "Uses your ElevenLabs credits.", Knowledge: []string{kVoice, vTTS, vElevenLabs}},

	"wikimedia":          {Capabilities: []string{"image_stock", "video_stock"}, Knowledge: []string{kStock}},
	"pexels_video":       {Capabilities: []string{"video_stock"}, Knowledge: []string{kStock}},
	"pixabay_video":      {Capabilities: []string{"video_stock"}, Knowledge: []string{kStock}},
	"direct_clip_search": {Capabilities: []string{"video_stock"}, Knowledge: []string{kStock}},

	"openai_image": {Capabilities: []string{"image_generation"}, CreditNote: "Uses your OpenAI API credits.", Knowledge: []string{kImageGen}},
	"flux_image":   {Capabilities: []string{"image_generation"}, CreditNote: "Uses your fal.ai credits.", Knowledge: []string{kImageGen, vFlux}},
	"gflow_image":  {Capabilities: []string{"image_generation"}, CreditNote: "Uses your Google Flow credits.", Knowledge: []string{kImageGen}},
	"kling_video":  {Capabilities: []string{"video_generation"}, CreditNote: "Uses your fal.ai credits (Kling).", Knowledge: []string{kVideoGen, vVideoGen}},
	"sora_video":   {Capabilities: []string{"video_generation"}, CreditNote: "Uses your OpenAI API credits.", Knowledge: []string{kVideoGen, vVideoGen}},
	"gflow_video":  {Capabilities: []string{"video_generation"}, CreditNote: "Uses your Google Flow credits (Veo).", Knowledge: []string{kVideoGen, vVideoGen}},

	// Selectors help choose a provider; they provide no capability themselves.
	"image_selector": {Knowledge: []string{kImageGen}},
	"video_selector": {Knowledge: []string{kVideoGen}},

	"music_library": {Capabilities: []string{"music"}, Knowledge: []string{kSound, vMusic}},
	"audio_mix":     {Capabilities: []string{"audio"}, Knowledge: []string{kSound, vMusic}},

	"subtitle_gen":        {Capabilities: []string{"captions"}, Knowledge: []string{kCaptions}},
	"ffmpeg_caption_burn": {Capabilities: []string{"captions"}, Knowledge: []string{kCaptions}},

	"video_compose":       {Capabilities: []string{"composition"}, Knowledge: []string{kRemotion, kModes, kScenes, vRemotion}},
	"hyperframes_compose": {Capabilities: []string{"composition"}, Knowledge: []string{kHyperRT, vHyper}},

	"source_edit":    {Capabilities: []string{"editing"}, Knowledge: []string{kEditing, kFFmpegRT}},
	"video_trimmer":  {Capabilities: []string{"editing"}, Knowledge: []string{kEditing, kFFmpegRT}},
	"video_stitch":   {Capabilities: []string{"editing"}, Knowledge: []string{kEditing, kFFmpegRT}},
	"silence_cutter": {Capabilities: []string{"editing"}, Knowledge: []string{kEditing, kFFmpegRT}},
	"color_grade":    {Capabilities: []string{"editing"}, Knowledge: []string{kGrade}},
}

// ToolMeta returns a tool's planning metadata. The runtime is derived from the
// tool's declared effects so it can never disagree with may_charge or network.
func ToolMeta(tool string) Meta {
	m := toolMeta[tool]
	out := Meta{
		Capabilities: append([]string(nil), m.Capabilities...),
		CreditNote:   m.CreditNote,
		Knowledge:    append([]string(nil), m.Knowledge...),
		Runtime:      RuntimeLocal,
	}
	switch {
	case MayCharge(tool):
		out.Runtime = RuntimePaid
	case executionFor(tool).Network:
		out.Runtime = RuntimeOnline
	}
	return out
}

// CapabilityTool is one provider of a capability in the preflight menu.
type CapabilityTool struct {
	Tool       string   `json:"tool"`
	Provider   string   `json:"provider"`
	Runtime    string   `json:"runtime"`
	Configured bool     `json:"configured"`
	Missing    []string `json:"missing,omitempty"`
	CreditNote string   `json:"credit_note,omitempty"`
}

// CapabilityReport is one line of the preflight menu: how many of a
// capability's tools are ready, listed free first.
type CapabilityReport struct {
	Capability string           `json:"capability"`
	Configured int              `json:"configured"`
	Total      int              `json:"total"`
	Tools      []CapabilityTool `json:"tools"`
}

// Capabilities reports every capability in order, each with its tools and
// whether they are ready on this machine. A capability with no tool yet is
// reported with total 0, so a pipeline that needs it sees it is unavailable.
func Capabilities(order []string) []CapabilityReport {
	byCap := map[string][]CapabilityTool{}
	for _, name := range names {
		meta := ToolMeta(name)
		s := summary(name)
		configured, _ := s["configured"].(bool)
		var missing []string
		if deps, ok := s["dependencies"].([]any); ok {
			for _, d := range deps {
				if m, ok := d.(map[string]any); ok {
					if avail, _ := m["available"].(bool); !avail {
						if n, _ := m["name"].(string); n != "" {
							missing = append(missing, n)
						}
					}
				}
			}
		}
		entry := CapabilityTool{
			Tool: name, Provider: executionFor(name).Provider, Runtime: meta.Runtime,
			Configured: configured, Missing: missing, CreditNote: meta.CreditNote,
		}
		for _, c := range meta.Capabilities {
			byCap[c] = append(byCap[c], entry)
		}
	}
	rank := map[string]int{RuntimeLocal: 0, RuntimeOnline: 1, RuntimePaid: 2}
	out := make([]CapabilityReport, 0, len(order))
	for _, c := range order {
		tools := byCap[c]
		sort.SliceStable(tools, func(i, j int) bool {
			if rank[tools[i].Runtime] != rank[tools[j].Runtime] {
				return rank[tools[i].Runtime] < rank[tools[j].Runtime]
			}
			return tools[i].Tool < tools[j].Tool
		})
		ready := 0
		for _, t := range tools {
			if t.Configured {
				ready++
			}
		}
		if tools == nil {
			tools = []CapabilityTool{}
		}
		out = append(out, CapabilityReport{Capability: c, Configured: ready, Total: len(tools), Tools: tools})
	}
	return out
}
