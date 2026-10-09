package toolbox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type colorGradeRequest struct {
	InputPath      string   `json:"input_path"`
	OutputPath     string   `json:"output_path"`
	Profile        string   `json:"profile,omitempty"`
	Intensity      *float64 `json:"intensity,omitempty"`
	LutPath        string   `json:"lut_path,omitempty"`
	CustomVF       string   `json:"custom_vf,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	Contrast       *float64 `json:"contrast,omitempty"`
	Saturation     *float64 `json:"saturation,omitempty"`
	Brightness     *float64 `json:"brightness,omitempty"`
	Gamma          *float64 `json:"gamma,omitempty"`
	Overwrite      bool     `json:"overwrite,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

var profileFilters = map[string]string{
	"cinematic_warm": "colorbalance=rs=0.06:gs=0.02:bs=-0.04:rh=0.05:gh=0.01:bh=-0.03,eq=contrast=1.05:saturation=1.08:brightness=0.01",
	"cinematic_cool": "colorbalance=rs=-0.03:gs=-0.01:bs=0.06:rh=-0.02:gh=0.01:bh=0.04,eq=contrast=1.06:saturation=0.95",
	"moody_dark":     "curves=all='0/0.04 0.25/0.22 0.5/0.47 0.75/0.73 1/0.94',eq=contrast=1.08:saturation=0.85:brightness=-0.03",
	"bright_clean":   "eq=contrast=1.04:saturation=1.05:brightness=0.02:gamma=1.02",
	"vintage_film":   "curves=all='0/0.06 0.5/0.5 1/0.95':red='0/0.02 1/1':blue='0/0.04 1/0.92',eq=contrast=0.98:saturation=0.9",
	"high_contrast":  "curves=all='0/0 0.15/0.08 0.5/0.52 0.85/0.92 1/1',eq=contrast=1.15:saturation=1.2",
	"neutral":        "eq=contrast=1.0:saturation=1.0:brightness=0.0",
	"custom":         "eq=contrast=1.0:saturation=1.0:brightness=0.0",
}

func doColorGrade(op string, data []byte) (any, []string, error) {
	return doColorGradeContext(context.Background(), op, data)
}

func doColorGradeContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r colorGradeRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if err := inputPath(r.InputPath); err != nil {
		return nil, nil, err
	}
	if op == "estimate" {
		if err := outputPath(r.OutputPath, r.Overwrite, true); err != nil {
			return nil, nil, err
		}
		res := estimateResult([]string{"color_grade_ffmpeg"})
		return res, nil, nil
	}
	if err := outputPath(r.OutputPath, r.Overwrite, false); err != nil {
		return nil, nil, err
	}

	profile := r.Profile
	if profile == "" {
		profile = "cinematic_warm"
	}

	intensity := 0.8
	if r.Intensity != nil {
		intensity = *r.Intensity
	}
	if intensity < 0 {
		intensity = 0
	}
	if intensity > 1 {
		intensity = 1
	}

	var baseFilter string
	if r.CustomVF != "" {
		baseFilter = r.CustomVF
	} else if r.LutPath != "" {
		if err := inputPath(r.LutPath); err != nil {
			return nil, nil, failure("invalid_request", "LUT file not found: "+err.Error(), map[string]any{"lut_path": r.LutPath})
		}
		lutClean := filepath.ToSlash(filepath.Clean(r.LutPath))
		lutClean = strings.ReplaceAll(lutClean, ":", "\\:")
		baseFilter = fmt.Sprintf("lut3d='%s'", lutClean)
	} else {
		filter, ok := profileFilters[profile]
		if !ok {
			return nil, nil, failure("invalid_request", "unknown color grade profile: "+profile, nil)
		}
		baseFilter = filter
	}

	// Extra adjustments
	if r.Temperature != nil {
		baseFilter += fmt.Sprintf(",colortemperature=temperature=%.1f", *r.Temperature)
	}

	eqParts := []string{}
	if r.Contrast != nil {
		eqParts = append(eqParts, fmt.Sprintf("contrast=%.3f", *r.Contrast))
	}
	if r.Saturation != nil {
		eqParts = append(eqParts, fmt.Sprintf("saturation=%.3f", *r.Saturation))
	}
	if r.Brightness != nil {
		eqParts = append(eqParts, fmt.Sprintf("brightness=%.3f", *r.Brightness))
	}
	if r.Gamma != nil {
		eqParts = append(eqParts, fmt.Sprintf("gamma=%.3f", *r.Gamma))
	}
	if len(eqParts) > 0 {
		baseFilter += ",eq=" + strings.Join(eqParts, ":")
	}

	var finalVF string
	if intensity >= 0.999 {
		finalVF = baseFilter
	} else if intensity <= 0.001 {
		finalVF = "null"
	} else {
		finalVF = fmt.Sprintf("split[orig][grade];[grade]%s[graded];[orig][graded]blend=all_mode=normal:all_opacity=%.2f", baseFilter, intensity)
	}

	timeout, err := positiveTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}

	tempOut, cleanup, err := temporaryOutput(r.OutputPath)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", r.InputPath,
		"-vf", finalVF,
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "copy",
		tempOut,
	}

	if _, err := runCommandDirContext(ctx, timeout, "", "ffmpeg", args...); err != nil {
		return nil, nil, failure("command_failed", "color grading FFmpeg process failed: "+err.Error(), nil)
	}

	p, _, probeErr := probeWithContext(ctx, tempOut, timeout)
	var outputFacts map[string]any
	var duration float64
	if probeErr == nil {
		outputFacts = p
		if format, ok := p["format"].(map[string]any); ok {
			if d, ok := format["duration"].(float64); ok {
				duration = d
			}
		}
	}

	if err := finalizeOutput(tempOut, r.OutputPath, r.Overwrite); err != nil {
		return nil, nil, err
	}

	result := map[string]any{
		"input":        r.InputPath,
		"output":       r.OutputPath,
		"profile":      profile,
		"intensity":    intensity,
		"lut_path":     r.LutPath,
		"filter_graph": finalVF,
		"duration":     duration,
	}
	// Absent rather than null when the graded file could not be probed: the
	// schema promises an object whenever the field is present.
	if outputFacts != nil {
		result["output_facts"] = outputFacts
	}
	return result, nil, nil
}
