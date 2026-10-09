package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Narration with timing. Every voice tool accepts `lines` instead of `text`:
// each line is synthesised on its own through the tool's normal path,
// measured, and the clips are joined with pauses into one narration file. The
// narration_timing record says where every line and word falls, so captions,
// scene timing and music ducking come from the synthesis itself.

type narrationLine struct {
	ID    string   `json:"id"`
	Text  string   `json:"text"`
	Pause *float64 `json:"pause_after_seconds,omitempty"`
}

// voiceTools maps each voice tool to the file extension of its normal output.
var voiceTools = map[string]string{
	"edge_tts": ".mp3", "piper_tts": ".wav", "openai_tts": ".mp3", "elevenlabs_tts": ".mp3",
}

// trimTail cuts a line's trailing silence down to 0.12 s.
const trimTail = "areverse,silenceremove=start_periods=1:start_threshold=-50dB:start_silence=0.12,areverse"

// narrationRequested reports whether a voice tool request uses `lines`.
func narrationRequested(tool string, data []byte) bool {
	if _, ok := voiceTools[tool]; !ok {
		return false
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(data, &probe) != nil {
		return false
	}
	_, ok := probe["lines"]
	return ok
}

type narrationPlan struct {
	lines      []narrationLine
	pauses     []float64
	output     string
	timingPath string
	base       map[string]json.RawMessage
	ext        string
}

func planNarration(ctx context.Context, tool string, data []byte) (narrationPlan, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return narrationPlan{}, failure("invalid_request", "request must be a JSON object", nil)
	}
	if _, ok := raw["text"]; ok {
		return narrationPlan{}, failure("invalid_request", "give text or lines, not both", nil)
	}
	var p narrationPlan
	if err := json.Unmarshal(raw["lines"], &p.lines); err != nil {
		return p, failure("invalid_request", "lines must be an array of {id, text, pause_after_seconds}", nil)
	}
	if len(p.lines) == 0 || len(p.lines) > 400 {
		return p, failure("invalid_request", "lines must hold 1 to 400 lines", nil)
	}
	pause := 0.35
	if v, ok := raw["pause_seconds"]; ok {
		if err := json.Unmarshal(v, &pause); err != nil || pause < 0 || pause > 10 || !finite(pause) {
			return p, failure("invalid_request", "pause_seconds must be between 0 and 10", nil)
		}
	}
	seen := map[string]bool{}
	for i := range p.lines {
		l := &p.lines[i]
		if strings.TrimSpace(l.ID) == "" {
			l.ID = fmt.Sprintf("l%d", i+1)
		}
		if seen[l.ID] {
			return p, failure("invalid_request", fmt.Sprintf("line id %q repeats", l.ID), nil)
		}
		seen[l.ID] = true
		if strings.TrimSpace(l.Text) == "" {
			return p, failure("invalid_request", fmt.Sprintf("line %s has no text", l.ID), nil)
		}
		gap := pause
		if l.Pause != nil {
			if *l.Pause < 0 || *l.Pause > 10 || !finite(*l.Pause) {
				return p, failure("invalid_request", fmt.Sprintf("line %s pause_after_seconds must be between 0 and 10", l.ID), nil)
			}
			gap = *l.Pause
		}
		if i == len(p.lines)-1 {
			gap = 0
		}
		p.pauses = append(p.pauses, gap)
	}
	for _, key := range []string{"output_path", "output"} {
		if v, ok := raw[key]; ok && p.output == "" {
			_ = json.Unmarshal(v, &p.output)
		}
	}
	if v, ok := raw["timing_path"]; ok {
		_ = json.Unmarshal(v, &p.timingPath)
	}
	p.ext = voiceTools[tool]
	if p.output == "" {
		p.output = defaultOutput(ctx, "narration"+p.ext)
	}
	if e := strings.ToLower(filepath.Ext(p.output)); e != "" {
		p.ext = e
	}
	if p.timingPath == "" {
		p.timingPath = strings.TrimSuffix(p.output, filepath.Ext(p.output)) + ".timing.json"
	}
	for _, key := range []string{"lines", "pause_seconds", "timing_path", "output_path", "output"} {
		delete(raw, key)
	}
	p.base = raw
	return p, nil
}

// lineRequest is the tool's own single-text request for one line.
func (p narrationPlan) lineRequest(text, output string) []byte {
	req := make(map[string]json.RawMessage, len(p.base)+2)
	for k, v := range p.base {
		req[k] = v
	}
	req["text"], _ = json.Marshal(text)
	req["output_path"], _ = json.Marshal(output)
	data, _ := json.Marshal(req)
	return data
}

func doNarrationContext(ctx context.Context, tool, op string, data []byte) (any, []string, error) {
	p, err := planNarration(ctx, tool, data)
	if err != nil {
		return nil, nil, err
	}
	if op == "estimate" {
		res, warnings, err := executeContext(ctx, tool, "estimate", p.lineRequest(p.lines[0].Text, filepath.Join(filepath.Dir(p.output), "line-1"+p.ext)))
		if err != nil {
			return nil, warnings, err
		}
		return map[string]any{"output": p.output, "timing_path": p.timingPath, "line_count": len(p.lines), "line_estimate": res}, warnings, nil
	}
	if err := outputPath(p.output, true, false); err != nil {
		return nil, nil, err
	}
	if err := outputPath(p.timingPath, true, false); err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(p.output), ".facet-narration-")
	if err != nil {
		return nil, nil, failure("output_failed", "cannot create a working folder beside the output: "+err.Error(), nil)
	}
	defer os.RemoveAll(tmp)

	var warnings []string
	var last map[string]any
	clips := make([]string, len(p.lines))
	durations := make([]float64, len(p.lines))
	lineWords := make([][]timedWord, len(p.lines))
	textLength := 0
	for i, l := range p.lines {
		voiced := filepath.Join(tmp, fmt.Sprintf("voice-%03d%s", i+1, p.ext))
		res, w, err := executeContext(ctx, tool, "run", p.lineRequest(l.Text, voiced))
		warnings = append(warnings, w...)
		if err != nil {
			return nil, warnings, lineFailure(l.ID, err)
		}
		last, _ = res.(map[string]any)
		// Measure decoded samples, not the container's estimate: the join
		// works on decoded audio, so every later line stays exact. The voice's
		// own trailing silence (Edge leaves most of a second) is trimmed to a
		// short tail, so the pause between lines is the one asked for; the
		// start is kept, so the voice's word timings still hold.
		clips[i] = filepath.Join(tmp, fmt.Sprintf("line-%03d.wav", i+1))
		if out, err := runCommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", voiced, "-af", trimTail, "-ar", "44100", "-ac", "1", "-c:a", "pcm_s16le", clips[i]); err != nil {
			return nil, warnings, failure("command_failed", fmt.Sprintf("line %s could not be decoded: %s", l.ID, bounded(string(out)+err.Error())), nil)
		}
		durations[i], err = probeDurationContext(ctx, clips[i], 20*time.Second)
		if err != nil || durations[i] <= 0 {
			return nil, warnings, failure("probe_failed", fmt.Sprintf("line %s could not be measured", l.ID), nil)
		}
		lineWords[i] = providerWords(last, l.Text, durations[i])
		textLength += len(l.Text)
	}
	if err := joinNarration(ctx, clips, p.pauses, p.output); err != nil {
		return nil, warnings, err
	}
	total, err := probeDurationContext(ctx, p.output, 20*time.Second)
	if err != nil {
		return nil, warnings, failure("probe_failed", "the joined narration could not be measured", nil)
	}
	record := narrationRecord(tool, p, durations, lineWords, total)
	body, _ := json.MarshalIndent(record, "", "  ")
	if err := os.WriteFile(p.timingPath, append(body, '\n'), 0o644); err != nil {
		return nil, warnings, failure("output_failed", "failed to write the timing record: "+err.Error(), nil)
	}
	result := map[string]any{}
	for k, v := range last {
		if k != "words" {
			result[k] = v
		}
	}
	result["output"] = p.output
	result["timing_path"] = p.timingPath
	result["line_count"] = len(p.lines)
	for _, k := range []string{"duration_seconds", "audio_duration_seconds"} {
		if _, ok := result[k]; ok {
			result[k] = total
		}
	}
	if _, ok := result["text_length"]; ok {
		result["text_length"] = textLength
	}
	if info, err := os.Stat(p.output); err == nil {
		if _, ok := result["size_bytes"]; ok {
			result["size_bytes"] = int(info.Size())
		}
	}
	result["format"] = strings.TrimPrefix(p.ext, ".")
	return result, warnings, nil
}

func lineFailure(id string, err error) error {
	if tf, ok := err.(*toolFailure); ok {
		e := *tf.err
		e.Message = fmt.Sprintf("line %s: %s", id, e.Message)
		return &toolFailure{&e}
	}
	return failure("tts_failed", fmt.Sprintf("line %s: %v", id, err), nil)
}

// joinNarration joins the clips with silence after each, re-encoding to the
// output's format.
func joinNarration(ctx context.Context, clips []string, pauses []float64, output string) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	for _, c := range clips {
		args = append(args, "-i", c)
	}
	var graph strings.Builder
	for i := range clips {
		fmt.Fprintf(&graph, "[%d:a]aresample=44100,aformat=sample_fmts=fltp:channel_layouts=mono,apad=pad_dur=%.3f[a%d];", i, pauses[i], i)
	}
	for i := range clips {
		fmt.Fprintf(&graph, "[a%d]", i)
	}
	fmt.Fprintf(&graph, "concat=n=%d:v=0:a=1[out]", len(clips))
	args = append(args, "-filter_complex", graph.String(), "-map", "[out]")
	args = append(args, audioCodecFor(output)...)
	temp, cleanup, err := temporaryOutput(output)
	if err != nil {
		return err
	}
	defer cleanup()
	args = append(args, temp)
	if out, err := runCommandContext(ctx, "ffmpeg", args...); err != nil {
		return failure("command_failed", "joining the narration failed: "+bounded(string(out)+err.Error()), nil)
	}
	return finalizeOutput(temp, output, true)
}

func audioCodecFor(output string) []string {
	switch strings.ToLower(filepath.Ext(output)) {
	case ".wav":
		return []string{"-c:a", "pcm_s16le"}
	case ".m4a", ".aac":
		return []string{"-c:a", "aac", "-b:a", "192k"}
	case ".flac":
		return []string{"-c:a", "flac"}
	case ".ogg", ".opus":
		return []string{"-c:a", "libopus", "-b:a", "128k"}
	default:
		return []string{"-c:a", "libmp3lame", "-q:a", "2"}
	}
}
