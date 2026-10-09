package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type wordTimestamp struct {
	Word    string  `json:"word"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	StartMS int     `json:"startMs,omitempty"`
	EndMS   int     `json:"endMs,omitempty"`
}

type transcriptSegment struct {
	Text  string          `json:"text,omitempty"`
	Start float64         `json:"start"`
	End   float64         `json:"end"`
	Words []wordTimestamp `json:"words,omitempty"`
}

type subtitleGenRequest struct {
	Segments        []transcriptSegment `json:"segments"`
	TimingPath      string              `json:"timing_path,omitempty"`
	Layout          string              `json:"layout,omitempty"`
	Format          string              `json:"format,omitempty"`
	OutputPath      string              `json:"output_path,omitempty"`
	MaxCharsPerLine int                 `json:"max_chars_per_line,omitempty"`
	MaxWordsPerCue  int                 `json:"max_words_per_cue,omitempty"`
	HighlightStyle  string              `json:"highlight_style,omitempty"`
	Corrections     map[string]string   `json:"corrections,omitempty"`
	TimeoutSeconds  int                 `json:"timeout_seconds,omitempty"`
}

type captionBurnRequest struct {
	InputPath      string              `json:"input_path"`
	OutputPath     string              `json:"output_path"`
	Segments       []transcriptSegment `json:"segments,omitempty"`
	TimingPath     string              `json:"timing_path,omitempty"`
	SRTPath        string              `json:"srt_path,omitempty"`
	WordsPerPage   int                 `json:"words_per_page,omitempty"`
	FontSize       int                 `json:"font_size,omitempty"`
	HighlightColor string              `json:"highlight_color,omitempty"`
	Corrections    map[string]string   `json:"corrections,omitempty"`
	Overlays       []any               `json:"overlays,omitempty"`
	TimeoutSeconds int                 `json:"timeout_seconds,omitempty"`
}

type cue struct {
	Index int             `json:"index"`
	Start float64         `json:"start"`
	End   float64         `json:"end"`
	Text  string          `json:"text"`
	Words []wordTimestamp `json:"words,omitempty"`
}

func doSubtitleGen(op string, data []byte) (any, []string, error) {
	return doSubtitleGenContext(context.Background(), op, data)
}

func doSubtitleGenContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r subtitleGenRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if len(r.Segments) == 0 && strings.TrimSpace(r.TimingPath) != "" {
		segments, err := timingSegments(r.TimingPath)
		if err != nil {
			return nil, nil, err
		}
		r.Segments = segments
	}
	if len(r.Segments) == 0 {
		return nil, nil, failure("invalid_request", "segments, or a narration timing record (timing_path), is required", nil)
	}
	fmtType := strings.ToLower(r.Format)
	if fmtType == "" {
		fmtType = "srt"
	}
	if fmtType != "srt" && fmtType != "vtt" && fmtType != "json" {
		return nil, nil, failure("invalid_request", "format must be srt, vtt, or json", nil)
	}
	maxWords := r.MaxWordsPerCue
	if maxWords <= 0 {
		maxWords = 8
		switch strings.ToLower(r.Layout) {
		case "vertical":
			maxWords = 4
		case "", "horizontal":
		default:
			return nil, nil, failure("invalid_request", "layout must be horizontal or vertical", nil)
		}
	}
	maxChars := r.MaxCharsPerLine
	if maxChars <= 0 {
		maxChars = 42
	}
	highlight := r.HighlightStyle
	if highlight == "" {
		highlight = "none"
	}

	if op == "estimate" {
		return estimateResult([]string{"parse_segments", "format_cues", "write_subtitles"}), nil, nil
	}

	segments := applyWordCorrections(r.Segments, r.Corrections)
	cues := buildSubtitleCues(segments, maxWords, maxChars)

	var content string
	ext := "." + fmtType
	switch fmtType {
	case "srt":
		content = renderSRT(cues, highlight)
	case "vtt":
		content = renderVTT(cues, highlight)
	case "json":
		b, _ := json.MarshalIndent(map[string]any{"cues": cues, "highlight_style": highlight}, "", "  ")
		content = string(b)
		ext = ".caption.json"
	}

	outPath := r.OutputPath
	if outPath == "" {
		outPath = defaultOutput(ctx, "subtitles"+ext)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return nil, nil, failure("command_failed", "unable to create output directory", nil)
	}
	if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
		return nil, nil, failure("command_failed", "unable to write subtitle file", nil)
	}

	return map[string]any{
		"format":    fmtType,
		"cue_count": len(cues),
		"output":    outPath,
	}, nil, nil
}

func doFFmpegCaptionBurn(op string, data []byte) (any, []string, error) {
	return doFFmpegCaptionBurnContext(context.Background(), op, data)
}

func doFFmpegCaptionBurnContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r captionBurnRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if len(r.Segments) == 0 && r.SRTPath == "" && strings.TrimSpace(r.TimingPath) != "" {
		segments, err := timingSegments(r.TimingPath)
		if err != nil {
			return nil, nil, err
		}
		r.Segments = segments
	}
	if err := inputPath(r.InputPath); err != nil {
		return nil, nil, err
	}
	if r.OutputPath == "" {
		r.OutputPath = defaultOutput(ctx, "captioned_output.mp4")
	}
	if err := outputPath(r.OutputPath, true, false); err != nil {
		return nil, nil, err
	}
	tmo, err := positiveTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		return estimateResult([]string{"prepare_captions", "render_captions"}), nil, nil
	}

	words := []wordTimestamp{}
	if len(r.Segments) > 0 {
		segs := applyWordCorrections(r.Segments, r.Corrections)
		for _, s := range segs {
			if len(s.Words) > 0 {
				words = append(words, s.Words...)
			} else if s.Text != "" {
				wParts := strings.Fields(s.Text)
				dur := s.End - s.Start
				perWord := dur / float64(len(wParts))
				for i, wp := range wParts {
					words = append(words, wordTimestamp{
						Word:  wp,
						Start: s.Start + float64(i)*perWord,
						End:   s.Start + float64(i+1)*perWord,
					})
				}
			}
		}
	} else if r.SRTPath != "" {
		// Read from SRT
		srtData, srtErr := os.ReadFile(r.SRTPath)
		if srtErr != nil {
			return nil, nil, failure("input_not_found", "unable to read srt file", nil)
		}
		words = parseSRTWords(string(srtData))
	} else {
		return nil, nil, failure("invalid_request", "either segments or srt_path is required", nil)
	}

	// Group into pages
	pageSize := r.WordsPerPage
	if pageSize <= 0 {
		pageSize = 4
	}
	tmpSRT := filepath.Join(filepath.Dir(r.OutputPath), fmt.Sprintf(".captions_%d.srt", time.Now().UnixNano()))
	srtLines := []string{}
	idx := 1
	for i := 0; i < len(words); i += pageSize {
		endIdx := i + pageSize
		if endIdx > len(words) {
			endIdx = len(words)
		}
		page := words[i:endIdx]
		txtParts := []string{}
		for _, w := range page {
			txtParts = append(txtParts, w.Word)
		}
		srtLines = append(srtLines, strconv.Itoa(idx))
		srtLines = append(srtLines, fmt.Sprintf("%s --> %s", formatSRTTime(page[0].Start), formatSRTTime(page[len(page)-1].End)))
		srtLines = append(srtLines, strings.Join(txtParts, " "))
		srtLines = append(srtLines, "")
		idx++
	}
	if err := os.WriteFile(tmpSRT, []byte(strings.Join(srtLines, "\n")), 0644); err != nil {
		return nil, nil, failure("command_failed", "unable to write temporary caption srt", nil)
	}
	defer os.Remove(tmpSRT)

	srtEscaped := strings.ReplaceAll(filepath.ToSlash(tmpSRT), ":", `\:`)
	fontSize := r.FontSize
	if fontSize <= 0 {
		fontSize = 24
	}
	vf := fmt.Sprintf("subtitles='%s':force_style='FontName=Segoe UI,FontSize=%d,Bold=1,PrimaryColour=&H00FFFFFF,OutlineColour=&H00000000,Outline=3,Shadow=2,Alignment=2,MarginV=100'", srtEscaped, fontSize)
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.InputPath, "-vf", vf, "-c:v", "libx264", "-preset", "fast", "-crf", "18", "-pix_fmt", "yuv420p", "-c:a", "copy", r.OutputPath}
	if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", args...); err != nil {
		return nil, nil, err
	}

	return map[string]any{
		"method": "ffmpeg",
		"output": r.OutputPath,
		// Captions are pages of up to words_per_page words; both counts are
		// reported because they answer different questions.
		"caption_count": idx - 1,
		"word_count":    len(words),
	}, nil, nil
}

func applyWordCorrections(segments []transcriptSegment, corrections map[string]string) []transcriptSegment {
	if len(corrections) == 0 {
		return segments
	}
	corrLower := map[string]string{}
	for k, v := range corrections {
		corrLower[strings.ToLower(k)] = v
	}
	res := make([]transcriptSegment, len(segments))
	for i, s := range segments {
		sCopy := s
		if len(s.Words) > 0 {
			wList := make([]wordTimestamp, len(s.Words))
			for j, w := range s.Words {
				wCopy := w
				clean := strings.TrimRight(strings.ToLower(w.Word), ".,!?;:'\"")
				if rep, ok := corrLower[clean]; ok {
					trailing := w.Word[len(clean):]
					wCopy.Word = rep + trailing
				}
				wList[j] = wCopy
			}
			sCopy.Words = wList
			parts := []string{}
			for _, w := range wList {
				parts = append(parts, w.Word)
			}
			sCopy.Text = strings.Join(parts, " ")
		}
		res[i] = sCopy
	}
	return res
}

func buildSubtitleCues(segments []transcriptSegment, maxWords, maxChars int) []cue {
	allWords := []wordTimestamp{}
	for _, s := range segments {
		if len(s.Words) > 0 {
			allWords = append(allWords, s.Words...)
		} else if s.Text != "" {
			parts := strings.Fields(s.Text)
			dur := s.End - s.Start
			perWord := dur / float64(len(parts))
			for i, p := range parts {
				allWords = append(allWords, wordTimestamp{
					Word:  p,
					Start: s.Start + float64(i)*perWord,
					End:   s.Start + float64(i+1)*perWord,
				})
			}
		}
	}
	if len(allWords) == 0 {
		return nil
	}

	cues := []cue{}
	buf := []wordTimestamp{}
	bufText := ""

	for _, w := range allWords {
		wordText := strings.TrimSpace(w.Word)
		candidate := wordText
		if bufText != "" {
			candidate = bufText + " " + wordText
		}
		if len(buf) > 0 && (len(buf) >= maxWords || len(candidate) > maxChars) {
			cues = append(cues, cue{
				Index: len(cues) + 1,
				Start: buf[0].Start,
				End:   buf[len(buf)-1].End,
				Text:  bufText,
				Words: buf,
			})
			buf = []wordTimestamp{}
			bufText = ""
		}
		buf = append(buf, w)
		if bufText == "" {
			bufText = wordText
		} else {
			bufText = bufText + " " + wordText
		}
	}
	if len(buf) > 0 {
		cues = append(cues, cue{
			Index: len(cues) + 1,
			Start: buf[0].Start,
			End:   buf[len(buf)-1].End,
			Text:  bufText,
			Words: buf,
		})
	}
	return cues
}

func formatSRTTime(seconds float64) string {
	totalMS := int(seconds*1000.0 + 0.5)
	if totalMS < 0 {
		totalMS = 0
	}
	h := totalMS / 3600000
	m := (totalMS % 3600000) / 60000
	s := (totalMS % 60000) / 1000
	ms := totalMS % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func formatVTTTime(seconds float64) string {
	totalMS := int(seconds*1000.0 + 0.5)
	if totalMS < 0 {
		totalMS = 0
	}
	h := totalMS / 3600000
	m := (totalMS % 3600000) / 60000
	s := (totalMS % 60000) / 1000
	ms := totalMS % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

func renderSRT(cues []cue, highlightStyle string) string {
	lines := []string{}
	if highlightStyle == "word_by_word" {
		idx := 1
		for _, c := range cues {
			for _, w := range c.Words {
				lines = append(lines, strconv.Itoa(idx))
				lines = append(lines, fmt.Sprintf("%s --> %s", formatSRTTime(w.Start), formatSRTTime(w.End)))
				lines = append(lines, w.Word)
				lines = append(lines, "")
				idx++
			}
		}
	} else if highlightStyle == "karaoke" {
		for _, c := range cues {
			if len(c.Words) == 0 {
				lines = append(lines, strconv.Itoa(c.Index))
				lines = append(lines, fmt.Sprintf("%s --> %s", formatSRTTime(c.Start), formatSRTTime(c.End)))
				lines = append(lines, c.Text)
				lines = append(lines, "")
				continue
			}
			for wi, w := range c.Words {
				lines = append(lines, strconv.Itoa(c.Index*100+wi))
				lines = append(lines, fmt.Sprintf("%s --> %s", formatSRTTime(w.Start), formatSRTTime(w.End)))
				parts := []string{}
				for wj, wOther := range c.Words {
					if wj == wi {
						parts = append(parts, "<b>"+wOther.Word+"</b>")
					} else {
						parts = append(parts, wOther.Word)
					}
				}
				lines = append(lines, strings.Join(parts, " "))
				lines = append(lines, "")
			}
		}
	} else {
		for _, c := range cues {
			lines = append(lines, strconv.Itoa(c.Index))
			lines = append(lines, fmt.Sprintf("%s --> %s", formatSRTTime(c.Start), formatSRTTime(c.End)))
			lines = append(lines, c.Text)
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func renderVTT(cues []cue, highlightStyle string) string {
	lines := []string{"WEBVTT", ""}
	for _, c := range cues {
		lines = append(lines, fmt.Sprintf("%s --> %s", formatVTTTime(c.Start), formatVTTTime(c.End)))
		lines = append(lines, c.Text)
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func parseSRTWords(content string) []wordTimestamp {
	words := []wordTimestamp{}
	blocks := strings.Split(strings.TrimSpace(content), "\n\n")
	for _, b := range blocks {
		lines := strings.Split(strings.TrimSpace(b), "\n")
		if len(lines) < 3 {
			continue
		}
		timeLine := lines[1]
		parts := strings.Split(timeLine, "-->")
		if len(parts) != 2 {
			continue
		}
		startS := parseSRTTimestamp(strings.TrimSpace(parts[0]))
		endS := parseSRTTimestamp(strings.TrimSpace(parts[1]))
		txt := strings.Join(lines[2:], " ")
		wList := strings.Fields(txt)
		if len(wList) > 0 {
			dur := endS - startS
			perWord := dur / float64(len(wList))
			for i, w := range wList {
				words = append(words, wordTimestamp{
					Word:  w,
					Start: startS + float64(i)*perWord,
					End:   startS + float64(i+1)*perWord,
				})
			}
		}
	}
	return words
}

func parseSRTTimestamp(s string) float64 {
	// 00:00:00,000
	s = strings.ReplaceAll(s, ",", ".")
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	h := parseFloat(parts[0])
	m := parseFloat(parts[1])
	sec := parseFloat(parts[2])
	return h*3600 + m*60 + sec
}
