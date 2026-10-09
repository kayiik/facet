package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type silenceCutterRequest struct {
	InputPath          string  `json:"input_path,omitempty"`
	Input              string  `json:"input,omitempty"`
	OutputPath         string  `json:"output_path,omitempty"`
	Mode               string  `json:"mode,omitempty"`
	SilenceThresholdDB float64 `json:"silence_threshold_db,omitempty"`
	MinSilenceDuration float64 `json:"min_silence_duration,omitempty"`
	PaddingSeconds     float64 `json:"padding_seconds,omitempty"`
	SilenceSpeedFactor float64 `json:"silence_speed_factor,omitempty"`
	Codec              string  `json:"codec,omitempty"`
	CRF                int     `json:"crf,omitempty"`
	TimeoutSeconds     int     `json:"timeout_seconds,omitempty"`
}

type silenceInterval struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Duration float64 `json:"duration"`
}

type speechInterval struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Speed float64 `json:"speed,omitempty"`
}

var silenceStartRE = regexp.MustCompile(`silence_start:\s*([0-9]+(?:\.[0-9]+)?)`)
var silenceEndRE = regexp.MustCompile(`silence_end:\s*([0-9]+(?:\.[0-9]+)?)\s*\|\s*silence_duration:\s*([0-9]+(?:\.[0-9]+)?)`)

func doSilenceCutter(op string, data []byte) (any, []string, error) {
	return doSilenceCutterContext(context.Background(), op, data)
}

func doSilenceCutterContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r silenceCutterRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	input := r.InputPath
	if input == "" {
		input = r.Input
	}
	if input == "" {
		return nil, nil, failure("invalid_request", "input_path is required", nil)
	}
	if err := inputPath(input); err != nil {
		return nil, nil, err
	}

	mode := r.Mode
	if mode == "" {
		mode = "remove"
	}
	if mode != "remove" && mode != "speed_up" && mode != "mark" {
		return nil, nil, failure("invalid_request", "mode must be remove, speed_up, or mark", nil)
	}
	thresholdDB := r.SilenceThresholdDB
	if thresholdDB == 0 {
		thresholdDB = -35.0
	}
	minSilence := r.MinSilenceDuration
	if minSilence <= 0 {
		minSilence = 0.5
	}
	padding := r.PaddingSeconds
	if padding <= 0 {
		padding = 0.08
	}
	speedFactor := r.SilenceSpeedFactor
	if speedFactor <= 1.0 {
		speedFactor = 6.0
	}
	codec := r.Codec
	if codec == "" {
		codec = "libx264"
	}
	crf := r.CRF
	if crf == 0 {
		crf = 18
	}

	tmo, err := positiveTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		return estimateResult([]string{"silence_cutter_" + mode}), nil, nil
	}

	p, _, err := probeWithContext(ctx, input, tmo)
	if err != nil {
		return nil, nil, err
	}
	totalDur := p["format"].(map[string]any)["duration"].(float64)

	// Run silencedetect
	cmdOut, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", "-hide_banner", "-i", input, "-af", fmt.Sprintf("silencedetect=noise=%sdB:d=%s", formatFloat(thresholdDB), formatFloat(minSilence)), "-f", "null", "-")
	outStr := string(cmdOut)

	starts := silenceStartRE.FindAllStringSubmatch(outStr, -1)
	ends := silenceEndRE.FindAllStringSubmatch(outStr, -1)
	silences := []silenceInterval{}
	count := len(starts)
	if len(ends) < count {
		count = len(ends)
	}
	for i := 0; i < count; i++ {
		sVal := parseFloat(starts[i][1])
		eVal := parseFloat(ends[i][1])
		dVal := parseFloat(ends[i][2])
		silences = append(silences, silenceInterval{
			Start:    sVal,
			End:      eVal,
			Duration: dVal,
		})
	}

	// Compute speech segments
	speechSegments := []speechInterval{}
	cursor := 0.0
	for _, sil := range silences {
		sEnd := sil.Start + padding
		if sEnd > cursor {
			speechSegments = append(speechSegments, speechInterval{
				Start: cursor,
				End:   math.Min(sEnd, totalDur),
			})
		}
		cursor = math.Max(cursor, sil.End-padding)
	}
	if cursor < totalDur {
		speechSegments = append(speechSegments, speechInterval{
			Start: cursor,
			End:   totalDur,
		})
	}

	// Merge very close speech segments
	merged := []speechInterval{}
	for _, seg := range speechSegments {
		if seg.End-seg.Start < 0.01 {
			continue
		}
		if len(merged) > 0 && seg.Start-merged[len(merged)-1].End < 0.05 {
			merged[len(merged)-1].End = seg.End
		} else {
			merged = append(merged, seg)
		}
	}
	speechSegments = merged

	if mode == "mark" {
		outPath := r.OutputPath
		if outPath == "" {
			outPath = strings.TrimSuffix(input, filepath.Ext(input)) + ".silence.json"
		}
		resData := map[string]any{
			"mode":                     "mark",
			"input":                    input,
			"silences":                 silences,
			"speech_segments":          speechSegments,
			"total_duration":           totalDur,
			"silence_segments":         len(silences),
			"speech_segments_count":    len(speechSegments),
			"silence_duration_seconds": sumSilence(silences),
			"output":                   outPath,
		}
		b, _ := json.MarshalIndent(resData, "", "  ")
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return nil, nil, failure("command_failed", "unable to create output directory", map[string]any{"error": bounded(err.Error())})
		}
		if err := os.WriteFile(outPath, b, 0o644); err != nil {
			return nil, nil, failure("command_failed", "unable to write the silence map", map[string]any{"path": outPath, "error": bounded(err.Error())})
		}
		return resData, nil, nil
	}

	outPath := r.OutputPath
	if outPath == "" {
		ext := filepath.Ext(input)
		outPath = strings.TrimSuffix(input, ext) + "_cut" + ext
	}
	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}

	tempDir, err := os.MkdirTemp(filepath.Dir(outPath), ".silence_cut_tmp-*")
	if err != nil {
		return nil, nil, failure("command_failed", "unable to create temporary directory", nil)
	}
	defer os.RemoveAll(tempDir)

	if mode == "remove" {
		if len(speechSegments) == 0 {
			// No cuts, copy input
			_ = copyFile(input, outPath)
			return map[string]any{
				"mode":            "remove",
				"input":           input,
				"output":          outPath,
				"output_duration": totalDur,
			}, nil, nil
		}
		segFiles := []string{}
		for i, seg := range speechSegments {
			segPath := filepath.Join(tempDir, fmt.Sprintf("seg_%04d.mp4", i))
			args := []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", formatFloat(seg.Start), "-to", formatFloat(seg.End), "-i", input, "-c:v", codec, "-crf", strconv.Itoa(crf), "-preset", "fast", "-c:a", "aac", "-ar", "48000", "-ac", "2", segPath}
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", args...); err != nil {
				return nil, nil, err
			}
			segFiles = append(segFiles, segPath)
		}
		listPath := filepath.Join(tempDir, "concat.txt")
		b := strings.Builder{}
		for _, sf := range segFiles {
			abs, _ := filepath.Abs(sf)
			fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(abs, `\`, `/`))
		}
		_ = os.WriteFile(listPath, []byte(b.String()), 0644)
		if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", outPath); err != nil {
			return nil, nil, err
		}
	} else if mode == "speed_up" {
		timeline := []speechInterval{}
		for _, seg := range speechSegments {
			timeline = append(timeline, speechInterval{Start: seg.Start, End: seg.End, Speed: 1.0})
		}
		for _, sil := range silences {
			timeline = append(timeline, speechInterval{Start: sil.Start, End: sil.End, Speed: speedFactor})
		}
		// Render speed-up segments
		segFiles := []string{}
		for i, seg := range timeline {
			dur := seg.End - seg.Start
			if dur < 0.05 {
				continue
			}
			segPath := filepath.Join(tempDir, fmt.Sprintf("seg_%04d.mp4", i))
			var args []string
			if seg.Speed == 1.0 {
				args = []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", formatFloat(seg.Start), "-to", formatFloat(seg.End), "-i", input, "-c:v", codec, "-crf", strconv.Itoa(crf), "-preset", "fast", "-c:a", "aac", "-ar", "48000", "-ac", "2", segPath}
			} else {
				pts := 1.0 / seg.Speed
				atempo := buildAtempoChain(seg.Speed)
				args = []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", formatFloat(seg.Start), "-to", formatFloat(seg.End), "-i", input, "-filter:v", fmt.Sprintf("setpts=%s*PTS", formatFloat(pts)), "-filter:a", atempo, "-c:v", codec, "-crf", strconv.Itoa(crf), "-preset", "fast", "-c:a", "aac", "-ar", "48000", "-ac", "2", segPath}
			}
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", args...); err != nil {
				return nil, nil, err
			}
			segFiles = append(segFiles, segPath)
		}
		listPath := filepath.Join(tempDir, "concat.txt")
		b := strings.Builder{}
		for _, sf := range segFiles {
			abs, _ := filepath.Abs(sf)
			fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(abs, `\`, `/`))
		}
		_ = os.WriteFile(listPath, []byte(b.String()), 0644)
		if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", outPath); err != nil {
			return nil, nil, err
		}
	}

	silenceDur := sumSilence(silences)
	return map[string]any{
		"mode":                    mode,
		"input":                   input,
		"output":                  outPath,
		"input_duration":          totalDur,
		"silence_removed_seconds": roundFloat(silenceDur, 2),
		"silence_segments":        len(silences),
		// A count, named like mark's: speech_segments is mark's list of
		// segments, and one field must not be an array in one mode and a
		// number in another.
		"speech_segments_count": len(speechSegments),
	}, nil, nil
}

func sumSilence(silences []silenceInterval) float64 {
	total := 0.0
	for _, s := range silences {
		total += s.Duration
	}
	return total
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
