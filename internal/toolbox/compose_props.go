package toolbox

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// rendererFamilies maps a production's renderer_family to the composer
// composition that renders templated work.
var rendererFamilies = map[string]string{
	"explainer-data": "Explainer", "explainer-teacher": "Explainer", "product-reveal": "Explainer",
	"screen-demo": "Explainer", "animation-first": "Explainer",
	"cinematic-trailer": "CinematicRenderer", "documentary-montage": "CinematicRenderer",
	"presenter": "TalkingHead",
}

var composerCompositions = map[string]bool{
	"Explainer": true, "CinematicRenderer": true, "TalkingHead": true, "TitledVideo": true,
	"ProductReveal": true, "ProductRevealVertical": true, "CollageBurst": true, "LyricOverlay": true,
	"EndTag": true, "EndTagOverlay": true, "CaptionOverlayOnly": true, "HeroTitle": true,
}

// facetOnlyKeys are request fields video_compose reads itself; they never
// reach the composer, whose contract refuses unknown props.
var facetOnlyKeys = []string{
	"output", "output_path", "timeout_seconds", "timing_path", "renderer_family", "render_runtime",
	"composition_mode", "composition", "composition_id", "loudness_target", "audio_path", "bespoke",
	"delivery_promise", "version", "metadata",
}

type narrationTiming struct {
	Duration float64 `json:"duration_seconds"`
	File     string  `json:"file"`
	Lines    []struct {
		ID    string  `json:"id"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	} `json:"lines"`
	Words []struct {
		Word  string  `json:"word"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Line  string  `json:"line"`
	} `json:"words"`
}

func loadTiming(path string) (*narrationTiming, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, failure("input_not_found", "timing_path cannot be read: "+err.Error(), map[string]any{"path": path})
	}
	var t narrationTiming
	if err := json.Unmarshal(data, &t); err != nil || len(t.Lines) == 0 {
		return nil, failure("invalid_request", "timing_path is not a narration timing record (a voice tool's lines output)", map[string]any{"path": path})
	}
	return &t, nil
}

// composerPlan is a request turned into composer props.
type composerPlan struct {
	props       map[string]any
	composition string
	loudness    float64
}

// prepareComposerProps turns a video_compose request (direct props, or edit
// decisions) into the composer's props: it picks the composition from
// renderer_family, times cuts given as narration lines, builds word captions
// and music ducking from the narration timing record, and converts sound
// effect cues. Everything else passes through for the composer's contract to
// check.
func prepareComposerProps(raw map[string]any) (composerPlan, error) {
	plan := composerPlan{composition: "Explainer", loudness: -14}
	str := func(key string) string { s, _ := raw[key].(string); return strings.TrimSpace(s) }
	if family := str("renderer_family"); family != "" {
		comp, ok := rendererFamilies[family]
		if !ok {
			return plan, failure("invalid_request", "renderer_family must be one of "+strings.Join(sortedMapKeys(rendererFamilies), ", "), nil)
		}
		plan.composition = comp
	}
	for _, key := range []string{"composition", "composition_id"} {
		if c := str(key); c != "" {
			if !composerCompositions[c] {
				return plan, failure("invalid_request", "composition must be one of "+strings.Join(sortedBoolKeys(composerCompositions), ", "), nil)
			}
			plan.composition = c
		}
	}
	if v, ok := raw["loudness_target"].(float64); ok {
		if v > -5 || v < -40 {
			return plan, failure("invalid_request", "loudness_target must be between -40 and -5 LUFS", nil)
		}
		plan.loudness = v
	}
	props := map[string]any{}
	for k, v := range raw {
		props[k] = v
	}
	for _, k := range facetOnlyKeys {
		delete(props, k)
	}
	var timing *narrationTiming
	if p := str("timing_path"); p != "" {
		t, err := loadTiming(p)
		if err != nil {
			return plan, err
		}
		timing = t
	}
	if cuts, ok := props["cuts"].([]any); ok {
		if err := timeCuts(cuts, timing); err != nil {
			return plan, err
		}
		if _, given := props["duration_seconds"]; !given && timing != nil {
			last := 0.0
			for _, c := range cuts {
				if m, ok := c.(map[string]any); ok {
					if v, ok := m["out_seconds"].(float64); ok && v > last {
						last = v
					}
				}
			}
			fps := 30.0
			if v, ok := props["fps"].(float64); ok && v > 0 {
				fps = v
			}
			props["duration_seconds"] = frameAlignedDuration(last, fps)
		}
	}
	if c, ok := props["captions"].(map[string]any); ok {
		captions, err := composerCaptions(c, timing)
		if err != nil {
			return plan, err
		}
		props["captions"] = captions
	}
	if a, ok := props["audio"].(map[string]any); ok {
		audio, err := composerAudio(a, timing)
		if err != nil {
			return plan, err
		}
		props["audio"] = audio
	}
	plan.props = props
	return plan, nil
}

// timeCuts gives every cut that names narration `lines` its in/out times: from
// its first line's start to the next cut's start (or its last line's end for
// the final cut), so the picture never goes blank between lines.
func timeCuts(cuts []any, timing *narrationTiming) error {
	lineAt := map[string][2]float64{}
	if timing != nil {
		for _, l := range timing.Lines {
			lineAt[l.ID] = [2]float64{l.Start, l.End}
		}
	}
	var timed []int
	for i, c := range cuts {
		cut, ok := c.(map[string]any)
		if !ok {
			continue
		}
		ids, ok := cut["lines"].([]any)
		if !ok {
			continue
		}
		if timing == nil {
			return failure("invalid_request", fmt.Sprintf("cuts[%d] names narration lines but the request has no timing_path", i), nil)
		}
		if len(ids) == 0 {
			return failure("invalid_request", fmt.Sprintf("cuts[%d].lines is empty", i), nil)
		}
		start, end := math.Inf(1), 0.0
		for _, v := range ids {
			id, _ := v.(string)
			span, ok := lineAt[id]
			if !ok {
				return failure("invalid_request", fmt.Sprintf("cuts[%d].lines names %q, which the timing record does not have", i, id), nil)
			}
			start, end = math.Min(start, span[0]), math.Max(end, span[1])
		}
		cut["in_seconds"], cut["out_seconds"] = round3(start), round3(end)
		delete(cut, "lines")
		timed = append(timed, i)
	}
	if len(timed) == 0 {
		return nil
	}
	// Close gaps: each line-timed cut runs until the next cut starts; the
	// first starts at 0 and the last holds a short tail.
	for k, i := range timed {
		cut := cuts[i].(map[string]any)
		if k == 0 && i == 0 {
			cut["in_seconds"] = 0.0
		}
		if i+1 < len(cuts) {
			if next, ok := cuts[i+1].(map[string]any); ok {
				if ns, ok := next["in_seconds"].(float64); ok {
					if ns > cut["out_seconds"].(float64) {
						cut["out_seconds"] = ns
					}
				}
			}
		} else {
			cut["out_seconds"] = round3(math.Max(cut["out_seconds"].(float64)+0.6, timing.Duration))
		}
	}
	return nil
}

func composerCaptions(c map[string]any, timing *narrationTiming) (map[string]any, error) {
	out := map[string]any{}
	if words, ok := c["words"]; ok {
		out["words"] = words
	} else {
		if p, ok := c["timing_path"].(string); ok && strings.TrimSpace(p) != "" {
			t, err := loadTiming(p)
			if err != nil {
				return nil, err
			}
			timing = t
		}
		if timing == nil || len(timing.Words) == 0 {
			return nil, failure("invalid_request", "captions need words, or a narration timing record (timing_path) with words", nil)
		}
		// A caption page ends with a sentence or a narration line, so one page
		// never runs from one thought into the next.
		words := make([]map[string]any, 0, len(timing.Words))
		for i, w := range timing.Words {
			word := map[string]any{"word": w.Word, "startMs": math.Round(w.Start * 1000), "endMs": math.Round(w.End * 1000)}
			lineEnds := i+1 < len(timing.Words) && timing.Words[i+1].Line != w.Line
			if lineEnds || strings.ContainsAny(lastRune(w.Word), ".?!") {
				word["pageBreakAfter"] = true
			}
			words = append(words, word)
		}
		out["words"] = words
	}
	rename := map[string]string{"words_per_page": "wordsPerPage", "highlight_color": "highlightColor", "font_size": "fontSize"}
	for k, v := range c {
		switch {
		case k == "words" || k == "timing_path":
		case rename[k] != "":
			out[rename[k]] = v
		default:
			out[k] = v
		}
	}
	return out, nil
}

func composerAudio(a map[string]any, timing *narrationTiming) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	if n, ok := a["narration"].(string); ok {
		out["narration"] = map[string]any{"src": n}
	}
	if m, ok := a["music"].(map[string]any); ok {
		music := map[string]any{}
		rename := map[string]string{"offset_seconds": "offsetSeconds", "fade_in_seconds": "fadeInSeconds", "fade_out_seconds": "fadeOutSeconds"}
		duck := false
		level := 0.25
		for k, v := range m {
			switch {
			case k == "duck_under_narration":
				duck, _ = v.(bool)
			case k == "duck_level":
				if f, ok := v.(float64); ok {
					level = f
				}
			case rename[k] != "":
				music[rename[k]] = v
			default:
				music[k] = v
			}
		}
		if duck {
			if timing == nil {
				return nil, failure("invalid_request", "audio.music.duck_under_narration needs the narration timing record (timing_path)", nil)
			}
			music["duck"] = map[string]any{"ranges": speakingRanges(timing), "level": level}
		}
		out["music"] = music
	}
	if cues, ok := a["sfx"].([]any); ok {
		sfx := make([]any, 0, len(cues))
		for i, c := range cues {
			cue, ok := c.(map[string]any)
			if !ok {
				return nil, failure("invalid_request", fmt.Sprintf("audio.sfx[%d] must be an object", i), nil)
			}
			item := map[string]any{"src": cue["src"]}
			if at, ok := cue["at_seconds"]; ok {
				item["atSeconds"] = at
			} else {
				item["atSeconds"] = cue["atSeconds"]
			}
			if g, ok := cue["gain_db"].(float64); ok {
				item["volume"] = math.Min(1, math.Pow(10, g/20))
			} else if v, ok := cue["volume"]; ok {
				item["volume"] = v
			}
			sfx = append(sfx, item)
		}
		out["sfx"] = sfx
	}
	return out, nil
}

// speakingRanges merges the narration's lines into spans of speech; pauses
// shorter than 0.4 s don't lift the music back up.
func speakingRanges(t *narrationTiming) []any {
	spans := make([][2]float64, 0, len(t.Lines))
	for _, l := range t.Lines {
		spans = append(spans, [2]float64{l.Start, l.End})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	var merged [][2]float64
	for _, s := range spans {
		if n := len(merged); n > 0 && s[0]-merged[n-1][1] < 0.4 {
			merged[n-1][1] = math.Max(merged[n-1][1], s[1])
			continue
		}
		merged = append(merged, s)
	}
	out := make([]any, 0, len(merged))
	for _, s := range merged {
		out = append(out, []any{round3(s[0]), round3(s[1])})
	}
	return out
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// lastRune is s's last character, ignoring closing quotes and brackets.
func lastRune(s string) string {
	s = strings.TrimRight(s, "\"')]\u201d\u2019")
	if s == "" {
		return ""
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return string(r)
}
