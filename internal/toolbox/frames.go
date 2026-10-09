package toolbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type sampleStrategy struct {
	Type       string    `json:"type"`
	Timestamps []float64 `json:"timestamps,omitempty"`
	Count      int       `json:"count,omitempty"`
	Threshold  float64   `json:"threshold,omitempty"`
}

// sampleRequest is exactly what frame_sample's request schema advertises.
type sampleRequest struct {
	Input          string `json:"input,omitempty"`
	InputPath      string `json:"input_path,omitempty"`
	OutputDir      string `json:"output_dir,omitempty"`
	Strategy       any    `json:"strategy,omitempty"`
	ImageFormat    string `json:"image_format,omitempty"`
	Format         string `json:"format,omitempty"`
	Overwrite      bool   `json:"overwrite,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

var sceneTimeRE = regexp.MustCompile(`pts_time:([0-9]+(?:\.[0-9]+)?)`)

func validateSampleStrategy(s sampleStrategy) error {
	switch s.Type {
	case "timestamps":
		if len(s.Timestamps) == 0 || s.Count != 0 || s.Threshold != 0 {
			return failure("invalid_request", "timestamps strategy requires only a non-empty timestamps list", nil)
		}
		for _, v := range s.Timestamps {
			if !finite(v) || v < 0 {
				return failure("invalid_request", "timestamps must be finite non-negative seconds", nil)
			}
		}
	case "uniform":
		if s.Count <= 0 || len(s.Timestamps) != 0 || s.Threshold != 0 {
			return failure("invalid_request", "uniform strategy requires only a positive count", nil)
		}
	case "scenes":
		if s.Count <= 0 || len(s.Timestamps) != 0 {
			return failure("invalid_request", "scenes strategy requires a positive count and optional threshold", nil)
		}
		if s.Threshold != 0 && (!finite(s.Threshold) || s.Threshold <= 0 || s.Threshold >= 1) {
			return failure("invalid_request", "scene threshold must be between 0 and 1", nil)
		}
	default:
		return failure("invalid_request", "strategy type must be timestamps, uniform, or scenes", nil)
	}
	return nil
}

func resolveSamples(s sampleStrategy, duration float64) ([]float64, []string, error) {
	if err := validateSampleStrategy(s); err != nil {
		return nil, nil, err
	}
	w := []string{}
	var ts []float64
	switch s.Type {
	case "timestamps":
		if len(s.Timestamps) == 0 {
			return nil, nil, failure("invalid_request", "timestamps strategy requires timestamps", nil)
		}
		ts = append(ts, s.Timestamps...)
	case "uniform", "scenes":
		if s.Count <= 0 {
			return nil, nil, failure("invalid_request", s.Type+" strategy requires positive count", nil)
		}
		for i := 0; i < s.Count; i++ {
			ts = append(ts, duration*float64(2*i+1)/float64(2*s.Count))
		}
	}
	for _, v := range ts {
		if !finite(v) || v < 0 || v >= duration {
			return nil, nil, failure("timestamp_out_of_bounds", "sample timestamp is outside input duration", map[string]any{"timestamp": v, "duration": duration})
		}
	}
	return ts, w, nil
}

func sceneSamples(input string, strategy sampleStrategy, duration float64, timeout time.Duration) ([]float64, []string, error) {
	return sceneSamplesContext(context.Background(), input, strategy, duration, timeout)
}

func sceneSamplesContext(ctx context.Context, input string, strategy sampleStrategy, duration float64, timeout time.Duration) ([]float64, []string, error) {
	threshold := strategy.Threshold
	if threshold == 0 {
		threshold = 0.3
	}
	if !finite(threshold) || threshold <= 0 || threshold >= 1 {
		return nil, nil, failure("invalid_request", "scene threshold must be between 0 and 1", nil)
	}
	out, err := runCommandDirContext(ctx, timeout, "", "ffmpeg", "-hide_banner", "-i", input, "-vf", fmt.Sprintf("select='gt(scene,%s)',showinfo", formatFloat(threshold)), "-an", "-f", "null", "-")
	if err != nil {
		return nil, nil, err
	}
	times := []float64{}
	seen := map[string]bool{}
	for _, match := range sceneTimeRE.FindAllStringSubmatch(string(out), -1) {
		v := parseFloat(match[1])
		key := formatFloat(v)
		if v >= 0 && v < duration && !seen[key] {
			seen[key] = true
			times = append(times, v)
		}
	}
	warnings := []string{}
	if len(times) > strategy.Count {
		selected := make([]float64, strategy.Count)
		for i := range selected {
			selected[i] = times[int(float64(i)*float64(len(times))/float64(strategy.Count))]
		}
		times = selected
	}
	if len(times) < strategy.Count {
		warnings = append(warnings, "fewer scene changes were detected; representative timestamps filled the remaining samples")
		for i := 0; len(times) < strategy.Count && i < strategy.Count*2; i++ {
			v := duration * float64(2*i+1) / float64(2*strategy.Count)
			key := formatFloat(v)
			if !seen[key] {
				seen[key] = true
				times = append(times, v)
			}
		}
	}
	sort.Float64s(times)
	return times, warnings, nil
}

func doFrameSample(op string, data []byte) (any, []string, error) {
	return doFrameSampleContext(context.Background(), op, data)
}

func doFrameSampleContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r sampleRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	input := r.Input
	if input == "" {
		input = r.InputPath
	}
	if input == "" {
		return nil, nil, failure("invalid_request", "input path is required", nil)
	}
	if strings.TrimSpace(r.OutputDir) == "" {
		return nil, nil, failure("invalid_request", "output_dir is required", nil)
	}
	imgFmt := r.ImageFormat
	if imgFmt == "" {
		imgFmt = r.Format
	}
	if imgFmt == "" {
		imgFmt = "jpg"
	}
	if imgFmt != "jpg" && imgFmt != "png" {
		return nil, nil, failure("invalid_request", "image_format must be jpg or png", nil)
	}

	strat, ok := r.Strategy.(map[string]any)
	if !ok {
		return nil, nil, failure("invalid_request", "strategy object is required for frame_sample", nil)
	}
	stratType, _ := strat["type"].(string)
	strategyObj := sampleStrategy{Type: stratType}
	if tsList, ok := strat["timestamps"].([]any); ok {
		for _, item := range tsList {
			if num, ok := item.(float64); ok {
				strategyObj.Timestamps = append(strategyObj.Timestamps, num)
			}
		}
	}
	if countVal, ok := strat["count"].(float64); ok {
		strategyObj.Count = int(countVal)
	}
	if threshVal, ok := strat["threshold"].(float64); ok {
		strategyObj.Threshold = threshVal
	}

	if err := validateSampleStrategy(strategyObj); err != nil {
		return nil, nil, err
	}

	t, err := positiveTimeout(r.TimeoutSeconds, 60)
	if err != nil {
		return nil, nil, err
	}
	if err = inputPath(input); err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		return estimateResult([]string{"validate", "ffprobe", "extract_frames"}), nil, nil
	}

	p, _, err := probeWithContext(ctx, input, t)
	if err != nil {
		return nil, nil, err
	}
	duration := p["format"].(map[string]any)["duration"].(float64)
	ts, w, err := resolveSamples(strategyObj, duration)
	if err != nil {
		return nil, nil, err
	}
	if strategyObj.Type == "scenes" && op == "run" {
		ts, w, err = sceneSamplesContext(ctx, input, strategyObj, duration, t)
		if err != nil {
			return nil, nil, err
		}
	}
	if err = os.MkdirAll(r.OutputDir, 0755); err != nil {
		return nil, nil, failure("command_failed", "evidence directory could not be created", map[string]any{"error": bounded(err.Error())})
	}
	stageDir, err := os.MkdirTemp(r.OutputDir, ".facet-frames-*")
	if err != nil {
		return nil, nil, failure("command_failed", "frame staging directory could not be created", map[string]any{"error": bounded(err.Error())})
	}
	defer os.RemoveAll(stageDir)

	samples := []map[string]any{}
	staged := make([]string, 0, len(ts))
	outputs := make([]string, 0, len(ts))
	for i, v := range ts {
		path := filepath.Join(r.OutputDir, fmt.Sprintf("frame-%04d.%s", i+1, imgFmt))
		if err = outputPath(path, r.Overwrite, false); err != nil {
			return nil, nil, err
		}
		stage := filepath.Join(stageDir, filepath.Base(path))
		args := []string{"-hide_banner", "-loglevel", "error", "-n", "-ss", formatFloat(v), "-i", input, "-frames:v", "1"}
		if imgFmt == "jpg" {
			args = append(args, "-q:v", "2")
		}
		args = append(args, stage)
		if _, err = runCommandDirContext(ctx, t, "", "ffmpeg", args...); err != nil {
			return nil, nil, err
		}
		if _, _, err = probeWithContext(ctx, stage, t); err != nil {
			return nil, nil, failure("output_validation_failed", "extracted frame could not be validated", map[string]any{"frame": i + 1, "error": err.Error()})
		}
		staged = append(staged, stage)
		outputs = append(outputs, path)
		video := firstVideo(p)
		wVal, hVal := 0, 0
		if video != nil {
			if w, ok := video["width"].(int); ok {
				wVal = w
			}
			if h, ok := video["height"].(int); ok {
				hVal = h
			}
		}
		samples = append(samples, map[string]any{"path": path, "timestamp": v, "width": wVal, "height": hVal})
	}
	if err = publishFileSet(staged, outputs, r.Overwrite); err != nil {
		return nil, nil, err
	}
	return map[string]any{"input": input, "strategy": strategyObj.Type, "resolved_timestamps": ts, "samples": samples}, w, nil
}
