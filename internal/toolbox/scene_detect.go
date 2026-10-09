package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type sceneDetectRequest struct {
	Input                 string  `json:"input,omitempty"`
	InputPath             string  `json:"input_path,omitempty"`
	Method                string  `json:"method,omitempty"`
	Threshold             float64 `json:"threshold,omitempty"`
	MinSceneLengthSeconds float64 `json:"min_scene_length_seconds,omitempty"`
	OutputPath            string  `json:"output_path,omitempty"`
	TimeoutSeconds        int     `json:"timeout_seconds,omitempty"`
}

func doSceneDetect(op string, data []byte) (any, []string, error) {
	return doSceneDetectContext(context.Background(), op, data)
}

func doSceneDetectContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r sceneDetectRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	input := r.Input
	if input == "" {
		input = r.InputPath
	}
	if input == "" {
		return nil, nil, failure("invalid_request", "input_path is required", nil)
	}
	if err := inputPath(input); err != nil {
		return nil, nil, err
	}

	threshold := r.Threshold
	if threshold == 0 {
		threshold = 0.3
	}
	minLen := r.MinSceneLengthSeconds
	if minLen <= 0 {
		minLen = 1.0
	}
	t, err := positiveTimeout(r.TimeoutSeconds, 120)
	if err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		return estimateResult([]string{"validate_input", "ffprobe_duration", "ffmpeg_scene_detect"}), nil, nil
	}

	p, _, err := probeWithContext(ctx, input, t)
	if err != nil {
		return nil, nil, err
	}
	duration, _ := p["format"].(map[string]any)["duration"].(float64)
	if duration <= 0 {
		return nil, nil, failure("input_probe_failed", "unable to determine input duration", nil)
	}

	out, err := runCommandDirContext(ctx, t, "", "ffmpeg", "-hide_banner", "-i", input, "-vf", fmt.Sprintf("select='gt(scene,%s)',showinfo", formatFloat(threshold)), "-an", "-f", "null", "-")
	if err != nil {
		return nil, nil, err
	}

	changePoints := []float64{0.0}
	rawMatches := sceneTimeRE.FindAllStringSubmatch(string(out), -1)
	var rawTimes []float64
	for _, m := range rawMatches {
		v := parseFloat(m[1])
		if v > 0 && v < duration {
			rawTimes = append(rawTimes, v)
		}
	}
	sort.Float64s(rawTimes)

	for _, ts := range rawTimes {
		last := changePoints[len(changePoints)-1]
		if ts-last >= minLen {
			changePoints = append(changePoints, ts)
		}
	}
	if duration-changePoints[len(changePoints)-1] >= 0.05 {
		changePoints = append(changePoints, duration)
	} else {
		changePoints[len(changePoints)-1] = duration
	}

	scenes := []map[string]any{}
	for i := 0; i < len(changePoints)-1; i++ {
		startS := changePoints[i]
		endS := changePoints[i+1]
		scenes = append(scenes, map[string]any{
			"index":            i,
			"start_seconds":    roundFloat(startS, 3),
			"end_seconds":      roundFloat(endS, 3),
			"duration_seconds": roundFloat(endS-startS, 3),
		})
	}

	if r.OutputPath != "" {
		if err := os.MkdirAll(filepath.Dir(r.OutputPath), 0755); err != nil {
			return nil, nil, failure("command_failed", "unable to create output directory", map[string]any{"error": err.Error()})
		}
		sceneData, _ := json.MarshalIndent(map[string]any{"scenes": scenes}, "", "  ")
		if err := os.WriteFile(r.OutputPath, sceneData, 0644); err != nil {
			return nil, nil, failure("command_failed", "unable to write scene output json", map[string]any{"error": err.Error()})
		}
	}

	return map[string]any{
		"scene_count": len(scenes),
		"scenes":      scenes,
		"method":      "ffmpeg",
		"output":      r.OutputPath,
	}, nil, nil
}

func roundFloat(val float64, precision int) float64 {
	ratio := 1.0
	for i := 0; i < precision; i++ {
		ratio *= 10.0
	}
	return float64(int64(val*ratio+0.5)) / ratio
}
