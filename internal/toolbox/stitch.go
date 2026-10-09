package toolbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// stitchRequest is exactly what video_stitch's request schema advertises, in
// the registry and in schemas/tools/video_stitch.schema.json alike.
//
// There is no dry_run: it was accepted and never read, so a request asking
// for "what would be done without executing" executed and wrote files.
// `facet tools estimate` is the side-effect-free path.
type stitchRequest struct {
	Operation          string   `json:"operation"`
	Clips              []string `json:"clips,omitempty"`
	OutputPath         string   `json:"output_path,omitempty"`
	Transition         string   `json:"transition,omitempty"`
	TransitionDuration float64  `json:"transition_duration,omitempty"`
	AutoNormalize      bool     `json:"auto_normalize,omitempty"`
	TargetResolution   string   `json:"target_resolution,omitempty"`
	TargetFPS          int      `json:"target_fps,omitempty"`
	Codec              string   `json:"codec,omitempty"`
	CRF                int      `json:"crf,omitempty"`
	Preset             string   `json:"preset,omitempty"`
	Layout             string   `json:"layout,omitempty"`
	PipPosition        string   `json:"pip_position,omitempty"`
	PipScale           float64  `json:"pip_scale,omitempty"`
	PipMargin          int      `json:"pip_margin,omitempty"`
	TimeoutSeconds     int      `json:"timeout_seconds,omitempty"`
}

func doVideoStitch(op string, data []byte) (any, []string, error) {
	return doVideoStitchContext(context.Background(), op, data)
}

func doVideoStitchContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r stitchRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	operation := r.Operation
	if operation == "" {
		operation = "stitch"
	}
	tmo, err := positiveTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		return estimateResult([]string{"video_stitch_" + operation}), nil, nil
	}

	if len(r.Clips) == 0 {
		return nil, nil, failure("invalid_request", "clips list is required", nil)
	}

	probes := make([]map[string]any, len(r.Clips))
	for i, clip := range r.Clips {
		if err := inputPath(clip); err != nil {
			return nil, nil, err
		}
		p, _, err := probeWithContext(ctx, clip, tmo)
		if err != nil {
			return nil, nil, err
		}
		probes[i] = p
	}

	switch operation {
	case "validate":
		reference := probes[0]
		mismatches := []map[string]any{}
		refV := firstVideo(reference)
		refAuds, _ := reference["audio_streams"].([]map[string]any)
		for i := 1; i < len(probes); i++ {
			curV := firstVideo(probes[i])
			curAuds, _ := probes[i]["audio_streams"].([]map[string]any)
			diffs := []string{}
			if refV != nil && curV != nil {
				if refV["width"] != curV["width"] || refV["height"] != curV["height"] {
					diffs = append(diffs, fmt.Sprintf("resolution mismatch: %vx%v vs %vx%v", refV["width"], refV["height"], curV["width"], curV["height"]))
				}
				if refV["fps"] != curV["fps"] {
					diffs = append(diffs, fmt.Sprintf("fps mismatch: %v vs %v", refV["fps"], curV["fps"]))
				}
				if refV["codec"] != curV["codec"] {
					diffs = append(diffs, fmt.Sprintf("video codec mismatch: %v vs %v", refV["codec"], curV["codec"]))
				}
			}
			if len(refAuds) > 0 && len(curAuds) > 0 {
				if refAuds[0]["sample_rate"] != curAuds[0]["sample_rate"] || refAuds[0]["channels"] != curAuds[0]["channels"] {
					diffs = append(diffs, fmt.Sprintf("audio format mismatch: %vHz/%vch vs %vHz/%vch", refAuds[0]["sample_rate"], refAuds[0]["channels"], curAuds[0]["sample_rate"], curAuds[0]["channels"]))
				}
			} else if len(refAuds) != len(curAuds) {
				diffs = append(diffs, "audio stream presence mismatch")
			}
			if len(diffs) > 0 {
				mismatches = append(mismatches, map[string]any{
					"clip_index":  i,
					"clip_path":   r.Clips[i],
					"differences": diffs,
				})
			}
		}
		totalDur := 0.0
		for _, p := range probes {
			totalDur += p["format"].(map[string]any)["duration"].(float64)
		}
		return map[string]any{
			"operation":      "validate",
			"clip_count":     len(r.Clips),
			"compatible":     len(mismatches) == 0,
			"total_duration": totalDur,
			"mismatches":     mismatches,
		}, nil, nil

	case "stitch", "preview_stitch":
		outPath := r.OutputPath
		if outPath == "" {
			outPath = defaultOutput(ctx, "stitched_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		targetW, targetH := 1920, 1080
		targetFPS := 30.0
		crf := r.CRF
		if crf == 0 {
			crf = 23
		}
		preset := r.Preset
		if preset == "" {
			preset = "medium"
		}
		if operation == "preview_stitch" {
			targetW, targetH = 640, 360
			targetFPS = 24.0
			crf = 30
			preset = "ultrafast"
		} else if r.TargetResolution != "" {
			parts := strings.Split(r.TargetResolution, "x")
			if len(parts) == 2 {
				targetW, _ = strconv.Atoi(parts[0])
				targetH, _ = strconv.Atoi(parts[1])
			}
		} else if refV := firstVideo(probes[0]); refV != nil {
			targetW = refV["width"].(int)
			targetH = refV["height"].(int)
			targetFPS = refV["fps"].(float64)
		}
		if r.TargetFPS > 0 {
			targetFPS = float64(r.TargetFPS)
		}

		tempDir, err := os.MkdirTemp(filepath.Dir(outPath), ".stitch_tmp-*")
		if err != nil {
			return nil, nil, failure("command_failed", "unable to create temporary staging directory", nil)
		}
		defer os.RemoveAll(tempDir)

		normalizedClips := make([]string, len(r.Clips))
		for i, clip := range r.Clips {
			normFile := filepath.Join(tempDir, fmt.Sprintf("norm_%04d.mp4", i))
			p := probes[i]
			vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%s,format=yuv420p", targetW, targetH, targetW, targetH, formatFloat(targetFPS))
			var args []string
			if hasAudio(p) {
				args = []string{"-hide_banner", "-loglevel", "error", "-y", "-i", clip, "-vf", vf, "-c:v", "libx264", "-crf", strconv.Itoa(crf), "-preset", preset, "-c:a", "aac", "-ar", "48000", "-ac", "2", normFile}
			} else {
				dur := p["format"].(map[string]any)["duration"].(float64)
				args = []string{"-hide_banner", "-loglevel", "error", "-y", "-i", clip, "-f", "lavfi", "-t", formatFloat(dur), "-i", "anullsrc=r=48000:cl=stereo", "-vf", vf, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-crf", strconv.Itoa(crf), "-preset", preset, "-c:a", "aac", "-ar", "48000", "-ac", "2", normFile}
			}
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", args...); err != nil {
				return nil, nil, err
			}
			normalizedClips[i] = normFile
		}

		trans := r.Transition
		if trans == "" {
			trans = "cut"
		}
		transDur := r.TransitionDuration
		if transDur <= 0 {
			transDur = 0.5
		}

		if trans == "cut" {
			listPath := filepath.Join(tempDir, "concat_list.txt")
			listBuilder := strings.Builder{}
			for _, nc := range normalizedClips {
				abs, _ := filepath.Abs(nc)
				clean := strings.ReplaceAll(abs, `\`, `/`)
				fmt.Fprintf(&listBuilder, "file '%s'\n", clean)
			}
			if err := os.WriteFile(listPath, []byte(listBuilder.String()), 0644); err != nil {
				return nil, nil, failure("command_failed", "unable to write concat list", nil)
			}
			cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", outPath}
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", cmdArgs...); err != nil {
				return nil, nil, err
			}
		} else {
			// Crossfade / fade transitions using xfade + acrossfade
			var transName string
			if trans == "fade" {
				transName = "fadeblack"
			} else {
				transName = "fade"
			}
			cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y"}
			for _, nc := range normalizedClips {
				cmdArgs = append(cmdArgs, "-i", nc)
			}
			filters := []string{}
			cumOffset := 0.0
			for i := 0; i < len(normalizedClips)-1; i++ {
				dur := probes[i]["format"].(map[string]any)["duration"].(float64)
				offset := cumOffset + dur - transDur
				if offset < 0 {
					offset = 0
				}
				vIn1 := "[0:v]"
				aIn1 := "[0:a]"
				if i > 0 {
					vIn1 = fmt.Sprintf("[vfade%d]", i-1)
					aIn1 = fmt.Sprintf("[afade%d]", i-1)
				}
				vIn2 := fmt.Sprintf("[%d:v]", i+1)
				aIn2 := fmt.Sprintf("[%d:a]", i+1)
				vOut := fmt.Sprintf("[vfade%d]", i)
				aOut := fmt.Sprintf("[afade%d]", i)
				if i == len(normalizedClips)-2 {
					vOut = "[vout]"
					aOut = "[aout]"
				}
				filters = append(filters, fmt.Sprintf("%s%sxfade=transition=%s:duration=%s:offset=%s%s", vIn1, vIn2, transName, formatFloat(transDur), formatFloat(offset), vOut))
				filters = append(filters, fmt.Sprintf("%s%sacrossfade=d=%s%s", aIn1, aIn2, formatFloat(transDur), aOut))
				cumOffset = offset
			}
			cmdArgs = append(cmdArgs, "-filter_complex", strings.Join(filters, ";"), "-map", "[vout]", "-map", "[aout]", "-c:v", "libx264", "-crf", strconv.Itoa(crf), "-preset", preset, "-c:a", "aac", outPath)
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", cmdArgs...); err != nil {
				return nil, nil, err
			}
		}

		outProbe, _, _ := probeWithContext(ctx, outPath, tmo)
		outDur := 0.0
		if outProbe != nil {
			if fmtM, ok := outProbe["format"].(map[string]any); ok {
				outDur, _ = fmtM["duration"].(float64)
			}
		}
		return map[string]any{
			"operation":           operation,
			"clip_count":          len(r.Clips),
			"transition":          trans,
			"transition_duration": transDur,
			"output":              outPath,
			"duration":            outDur,
		}, nil, nil

	case "spatial":
		if len(r.Clips) < 2 {
			return nil, nil, failure("invalid_request", "spatial layout requires at least 2 clips", nil)
		}
		outPath := r.OutputPath
		if outPath == "" {
			outPath = defaultOutput(ctx, "spatial_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		layout := r.Layout
		if layout == "" {
			layout = "side_by_side"
		}
		codec := r.Codec
		if codec == "" {
			codec = "libx264"
		}
		crf := r.CRF
		if crf == 0 {
			crf = 23
		}

		switch layout {
		case "side_by_side":
			filterComplex := "[0:v]scale=-2:480[left];[1:v]scale=-2:480[right];[left][right]hstack=inputs=2[v];[0:a][1:a]amix=inputs=2:duration=shortest[a]"
			cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.Clips[0], "-i", r.Clips[1], "-filter_complex", filterComplex, "-map", "[v]", "-map", "[a]", "-c:v", codec, "-crf", strconv.Itoa(crf), "-c:a", "aac", "-shortest", outPath}
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", cmdArgs...); err != nil {
				return nil, nil, err
			}
		case "vertical_stack":
			filterComplex := "[0:v]scale=540:-2[top];[1:v]scale=540:-2[bottom];[top][bottom]vstack=inputs=2[v];[0:a][1:a]amix=inputs=2:duration=shortest[a]"
			cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.Clips[0], "-i", r.Clips[1], "-filter_complex", filterComplex, "-map", "[v]", "-map", "[a]", "-c:v", codec, "-crf", strconv.Itoa(crf), "-c:a", "aac", "-shortest", outPath}
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", cmdArgs...); err != nil {
				return nil, nil, err
			}
		case "picture_in_picture":
			scale := r.PipScale
			if scale <= 0 {
				scale = 0.3
			}
			margin := r.PipMargin
			if margin <= 0 {
				margin = 10
			}
			pos := "main_w-overlay_w-" + strconv.Itoa(margin) + ":main_h-overlay_h-" + strconv.Itoa(margin)
			switch r.PipPosition {
			case "top_left":
				pos = strconv.Itoa(margin) + ":" + strconv.Itoa(margin)
			case "top_right":
				pos = "main_w-overlay_w-" + strconv.Itoa(margin) + ":" + strconv.Itoa(margin)
			case "bottom_left":
				pos = strconv.Itoa(margin) + ":main_h-overlay_h-" + strconv.Itoa(margin)
			}
			filterComplex := fmt.Sprintf("[1:v]scale=iw*%s:ih*%s[pip];[0:v][pip]overlay=%s:shortest=1[v]", formatFloat(scale), formatFloat(scale), pos)
			cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.Clips[0], "-i", r.Clips[1], "-filter_complex", filterComplex, "-map", "[v]", "-map", "0:a?", "-c:v", codec, "-crf", strconv.Itoa(crf), "-c:a", "aac", "-shortest", outPath}
			if _, err := runCommandDirContext(ctx, tmo, "", "ffmpeg", cmdArgs...); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, failure("invalid_request", "unknown layout: "+layout, nil)
		}

		return map[string]any{
			"operation":  "spatial",
			"layout":     layout,
			"clip_count": len(r.Clips),
			"output":     outPath,
		}, nil, nil

	default:
		return nil, nil, failure("invalid_request", "unknown operation: "+operation, nil)
	}
}
