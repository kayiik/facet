package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

// script_check measures a script against the time it has: word count and
// spoken length at a pace, when the hook lands, caption-length lines, and each
// section's share of the duration against the chosen structure's beats.

type checkFinding struct {
	Severity string `json:"severity"`
	Issue    string `json:"issue"`
	Fix      string `json:"fix"`
}

type scriptCheckRequest struct {
	ScriptPath      string          `json:"script_path,omitempty"`
	Script          json.RawMessage `json:"script,omitempty"`
	DurationSeconds float64         `json:"duration_seconds,omitempty"`
	WordsPerMinute  float64         `json:"words_per_minute,omitempty"`
	HookSeconds     float64         `json:"hook_seconds,omitempty"`
	MaxLineChars    int             `json:"max_line_chars,omitempty"`
	Structure       *struct {
		Beats []struct {
			ID    string  `json:"id"`
			Share float64 `json:"share"`
		} `json:"beats"`
	} `json:"structure,omitempty"`
}

type scriptDoc struct {
	Title    string  `json:"title"`
	Duration float64 `json:"total_duration_seconds"`
	Sections []struct {
		ID    string  `json:"id"`
		Beat  string  `json:"beat"`
		Text  string  `json:"text"`
		Start float64 `json:"start_seconds"`
		End   float64 `json:"end_seconds"`
	} `json:"sections"`
}

var sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)

// loadRecord reads a record given inline or by path.
func loadRecord(inline json.RawMessage, path, name string, dst any) error {
	data := []byte(inline)
	if len(data) == 0 || string(data) == "null" {
		if strings.TrimSpace(path) == "" {
			return failure("invalid_request", fmt.Sprintf("give %s or %s_path", name, name), nil)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return failure("input_not_found", fmt.Sprintf("%s_path cannot be read: %v", name, err), nil)
		}
		data = body
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return failure("invalid_request", fmt.Sprintf("%s is not a %s record: %v", name, name, err), nil)
	}
	return nil
}

func doScriptCheckContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r scriptCheckRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	var doc scriptDoc
	if err := loadRecord(r.Script, r.ScriptPath, "script", &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Sections) == 0 {
		return nil, nil, failure("invalid_request", "the script has no sections", nil)
	}
	wpm := r.WordsPerMinute
	if wpm == 0 {
		wpm = 150
	}
	hook := r.HookSeconds
	if hook == 0 {
		hook = 5
	}
	maxChars := r.MaxLineChars
	if maxChars == 0 {
		maxChars = 42
	}
	if wpm < 60 || wpm > 260 || hook <= 0 || maxChars < 10 {
		return nil, nil, failure("invalid_request", "words_per_minute must be 60–260, hook_seconds positive, max_line_chars at least 10", nil)
	}
	target := r.DurationSeconds
	if target == 0 {
		target = doc.Duration
	}
	if op == "estimate" {
		return estimateResult([]string{"script_check"}), nil, nil
	}
	var findings []checkFinding
	total := 0
	var all []string
	sectionWords := make([]int, len(doc.Sections))
	for i, s := range doc.Sections {
		words := len(strings.Fields(s.Text))
		sectionWords[i] = words
		total += words
		all = append(all, s.Text)
	}
	spoken := float64(total) / wpm * 60
	fit := "unknown"
	if target > 0 {
		ratio := spoken / target
		switch {
		case ratio > 1.1:
			fit = "too_long"
			findings = append(findings, checkFinding{"critical", fmt.Sprintf("%d words take about %.1f s at %.0f words a minute; the video has %.1f s", total, spoken, wpm, target), fmt.Sprintf("cut about %d words", int(float64(total)-target/60*wpm))})
		case ratio < 0.75:
			fit = "too_short"
			findings = append(findings, checkFinding{"suggestion", fmt.Sprintf("%d words fill only about %.1f of %.1f s", total, spoken, target), "add substance, or plan visual beats with music for the rest"})
		default:
			fit = "ok"
		}
	}
	text := strings.Join(all, " ")
	first := text
	if loc := sentenceEnd.FindStringIndex(text); loc != nil {
		first = text[:loc[0]+1]
	}
	hookWords := len(strings.Fields(first))
	hookAt := float64(hookWords) / wpm * 60
	if hookAt > hook {
		findings = append(findings, checkFinding{"critical", fmt.Sprintf("the first sentence takes about %.1f s; the hook should land within %.1f s", hookAt, hook), "open with a shorter, sharper first sentence"})
	}
	var longLines []string
	for _, s := range doc.Sections {
		for _, sentence := range sentenceEnd.Split(s.Text, -1) {
			if words := strings.Fields(sentence); len(words) > 0 && len(strings.Join(words, " ")) > maxChars*2 {
				longLines = append(longLines, s.ID)
				break
			}
		}
	}
	if len(longLines) > 0 {
		findings = append(findings, checkFinding{"suggestion", "sentences longer than two caption lines in sections " + strings.Join(longLines, ", "), "split long sentences so captions and the voice can breathe"})
	}
	shares := make([]map[string]any, 0, len(doc.Sections))
	for i, s := range doc.Sections {
		share := 0.0
		if total > 0 {
			share = float64(sectionWords[i]) / float64(total)
		}
		entry := map[string]any{"id": s.ID, "words": sectionWords[i], "share": math.Round(share*100) / 100}
		if r.Structure != nil {
			beat := s.Beat
			if beat == "" {
				beat = s.ID
			}
			for _, b := range r.Structure.Beats {
				if b.ID == beat {
					entry["beat_share"] = b.Share
					if math.Abs(share-b.Share) > 0.1 {
						findings = append(findings, checkFinding{"suggestion", fmt.Sprintf("section %s takes %.0f%% of the words; its beat plans %.0f%%", s.ID, share*100, b.Share*100), "rebalance the section against the structure"})
					}
				}
			}
		}
		shares = append(shares, entry)
	}
	if findings == nil {
		findings = []checkFinding{}
	}
	return map[string]any{
		"operation": "script_check", "words": total, "words_per_minute": wpm,
		"spoken_seconds": math.Round(spoken*10) / 10, "target_seconds": target, "fit": fit,
		"hook":     map[string]any{"text": strings.TrimSpace(first), "seconds": math.Round(hookAt*10) / 10, "within": hookAt <= hook},
		"sections": shares, "findings": findings,
	}, nil, nil
}
