package toolbox

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type timedWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// providerWords returns a line's word timings: the voice's own when it gave
// them (Edge does), otherwise the line's words spread over its measured
// duration in proportion to their length.
func providerWords(result map[string]any, text string, duration float64) []timedWord {
	if raw, ok := result["words"].([]map[string]any); ok && len(raw) > 0 {
		out := make([]timedWord, 0, len(raw))
		for _, w := range raw {
			word, _ := w["word"].(string)
			start, _ := w["start"].(float64)
			end, _ := w["end"].(float64)
			if strings.TrimSpace(word) == "" || end < start {
				continue
			}
			if end > duration {
				end = duration
			}
			out = append(out, timedWord{Word: word, Start: start, End: end})
		}
		if len(out) > 0 {
			return punctuate(out, text)
		}
	}
	return spreadWords(text, 0, duration)
}

// punctuate gives a voice's words the punctuation of the line they came from:
// Edge reports "day" for "day." and "blue" for "blue?". Each word takes the
// next matching token of the line; a word with no match nearby keeps its own
// spelling.
func punctuate(words []timedWord, text string) []timedWord {
	tokens := strings.Fields(text)
	next := 0
	for i := range words {
		key := wordKey(words[i].Word)
		if key == "" {
			continue
		}
		for k := next; k < len(tokens) && k < next+4; k++ {
			if wordKey(tokens[k]) == key {
				words[i].Word = tokens[k]
				next = k + 1
				break
			}
		}
	}
	return words
}

// wordKey is a word's letters and digits, lower-cased.
func wordKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// spreadWords places text's words between start and end, each taking time in
// proportion to its length (plus one character for the gap after it).
func spreadWords(text string, start, end float64) []timedWord {
	words := strings.Fields(text)
	if len(words) == 0 || end <= start {
		return nil
	}
	weights := make([]float64, len(words))
	total := 0.0
	for i, w := range words {
		weights[i] = float64(utf8.RuneCountInString(w) + 1)
		total += weights[i]
	}
	out := make([]timedWord, len(words))
	at := start
	for i, w := range words {
		span := (end - start) * weights[i] / total
		out[i] = timedWord{Word: w, Start: round3(at), End: round3(at + span)}
		at += span
	}
	out[len(out)-1].End = round3(end)
	return out
}

func round3(v float64) float64 { return float64(int64(v*1000+0.5)) / 1000 }

// timingSegments turns a narration timing record into subtitle segments: one
// per line, carrying its words.
func timingSegments(path string) ([]transcriptSegment, error) {
	t, err := loadTiming(path)
	if err != nil {
		return nil, err
	}
	segments := make([]transcriptSegment, 0, len(t.Lines))
	for _, l := range t.Lines {
		seg := transcriptSegment{Start: l.Start, End: l.End}
		var texts []string
		for _, w := range t.Words {
			if w.Start >= l.Start-0.001 && w.End <= l.End+0.001 {
				seg.Words = append(seg.Words, wordTimestamp{Word: w.Word, Start: w.Start, End: w.End})
				texts = append(texts, w.Word)
			}
		}
		seg.Text = strings.Join(texts, " ")
		segments = append(segments, seg)
	}
	return segments, nil
}

// textOrLines is the request rule every voice tool shares: one text, or
// lines voiced one by one with a timing record.
func textOrLines() []any {
	return []any{
		map[string]any{"required": []string{"text"}},
		map[string]any{"required": []string{"lines"}},
	}
}

func narrationLinesSchema() map[string]any {
	return map[string]any{
		"type": "array", "minItems": 1, "maxItems": 400,
		"description": "Voice these lines one by one, joined with pauses, and write a narration_timing record (timing_path) with every line's and word's start and end.",
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"text"},
			"properties": map[string]any{
				"id":                  map[string]any{"type": "string"},
				"text":                map[string]any{"type": "string", "minLength": 1},
				"pause_after_seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 10},
			},
		},
	}
}

// narrationRecord builds the narration_timing record: every line and word in
// seconds from the start of the joined narration.
func narrationRecord(tool string, p narrationPlan, durations []float64, words [][]timedWord, total float64) map[string]any {
	voice := ""
	for _, key := range []string{"voice", "voice_id", "model"} {
		if v, ok := p.base[key]; ok {
			voice = strings.Trim(string(v), `"`)
			break
		}
	}
	lines := make([]map[string]any, 0, len(p.lines))
	allWords := make([]map[string]any, 0)
	at := 0.0
	for i, l := range p.lines {
		start, end := at, at+durations[i]
		lines = append(lines, map[string]any{"id": l.ID, "text": l.Text, "start": round3(start), "end": round3(end)})
		for _, w := range words[i] {
			allWords = append(allWords, map[string]any{
				"word": w.Word, "start": round3(start + w.Start), "end": round3(start + w.End), "line": l.ID,
			})
		}
		at = end + p.pauses[i]
	}
	return map[string]any{
		"voice":            map[string]any{"tool": tool, "voice": voice},
		"duration_seconds": round3(total),
		"file":             p.output,
		"lines":            lines,
		"words":            allWords,
		"source":           "synthesis",
	}
}
