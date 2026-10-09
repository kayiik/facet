package toolbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type segment struct {
	Input      string  `json:"input,omitempty"`
	InputPath  string  `json:"input_path,omitempty"`
	Start      float64 `json:"start,omitempty"`
	StartSec   float64 `json:"start_seconds,omitempty"`
	End        float64 `json:"end,omitempty"`
	EndSec     float64 `json:"end_seconds,omitempty"`
	Transition string  `json:"transition,omitempty"`
	Position   string  `json:"position,omitempty"`
	FocalPoint *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"focal_point,omitempty"`
}

var framePositions = []string{"center", "top", "bottom", "left", "right", "top_left", "top_right", "bottom_left", "bottom_right"}

type target struct {
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	FPS             float64 `json:"fps"`
	Fit             string  `json:"fit"`
	VideoCodec      string  `json:"video_codec"`
	PixelFormat     string  `json:"pixel_format"`
	AudioCodec      string  `json:"audio_codec"`
	AudioSampleRate int     `json:"audio_sample_rate"`
	AudioChannels   int     `json:"audio_channels"`
}

type editRequest struct {
	Segments         []segment `json:"segments"`
	Target           target    `json:"target"`
	ReplacementAudio string    `json:"replacement_audio,omitempty"`
	Output           string    `json:"output"`
	Overwrite        bool      `json:"overwrite,omitempty"`
	TimeoutSeconds   int       `json:"timeout_seconds,omitempty"`
}

type trimmerRequest struct {
	Operation      string    `json:"operation"`
	InputPath      string    `json:"input_path"`
	OutputPath     string    `json:"output_path"`
	StartSeconds   float64   `json:"start_seconds,omitempty"`
	EndSeconds     *float64  `json:"end_seconds,omitempty"`
	SpeedFactor    float64   `json:"speed_factor,omitempty"`
	Segments       []segment `json:"segments,omitempty"`
	Codec          string    `json:"codec,omitempty"`
	TimeoutSeconds int       `json:"timeout_seconds,omitempty"`
}

func validateTarget(t target) error {
	if t.Width <= 0 || t.Height <= 0 || t.Width%2 != 0 || t.Height%2 != 0 || !finite(t.FPS) || t.FPS <= 0 {
		return failure("invalid_request", "target dimensions must be positive even values and fps positive", nil)
	}
	if t.Fit != "contain" && t.Fit != "cover" {
		return failure("invalid_request", "target fit must be contain or cover", nil)
	}
	if t.VideoCodec != "h264" || t.PixelFormat != "yuv420p" || t.AudioCodec != "aac" || t.AudioSampleRate != 48000 || t.AudioChannels != 2 {
		return failure("invalid_request", "Phase 2 target must be h264/yuv420p/AAC 48000 Hz stereo", nil)
	}
	return nil
}

func cropPlacement(s segment) (string, string) {
	x, y := "(iw-ow)/2", "(ih-oh)/2"
	switch s.Position {
	case "left", "top_left", "bottom_left":
		x = "0"
	case "right", "top_right", "bottom_right":
		x = "iw-ow"
	}
	switch s.Position {
	case "top", "top_left", "top_right":
		y = "0"
	case "bottom", "bottom_left", "bottom_right":
		y = "ih-oh"
	}
	if s.FocalPoint != nil {
		x = fmt.Sprintf("max(0\\,min(iw-ow\\,iw*%s-ow/2))", formatFloat(s.FocalPoint.X))
		y = fmt.Sprintf("max(0\\,min(ih-oh\\,ih*%s-oh/2))", formatFloat(s.FocalPoint.Y))
	}
	return x, y
}

func sourceEditRequestedOperations(r editRequest) []string {
	ops := []string{"trim", "scale_" + r.Target.Fit, "normalize", "concat", "cut"}
	for _, s := range r.Segments {
		if s.Position != "" || s.FocalPoint != nil {
			ops = append(ops, "framing")
			break
		}
	}
	if r.ReplacementAudio != "" {
		ops = append(ops, "replace_audio")
	}
	return ops
}

func doSourceEdit(op string, data []byte) (any, []string, error) {
	return doSourceEditContext(context.Background(), op, data)
}

func doSourceEditContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r editRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	tmo, err := positiveTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}
	if len(r.Segments) == 0 {
		return nil, nil, failure("invalid_timeline", "segments must not be empty", nil)
	}
	if err = validateTarget(r.Target); err != nil {
		return nil, nil, err
	}
	if err = outputPath(r.Output, r.Overwrite, op == "estimate"); err != nil {
		return nil, nil, err
	}
	total := 0.0
	probes := make([]map[string]any, len(r.Segments))
	silent := 0
	for i, s := range r.Segments {
		sIn := s.Input
		if sIn == "" {
			sIn = s.InputPath
		}
		if err = inputPath(sIn); err != nil {
			return nil, nil, err
		}
		if samePath(sIn, r.Output) {
			return nil, nil, failure("invalid_request", "output must differ from every segment input", map[string]any{"segment": i})
		}
		sStart := s.Start
		if sStart == 0 && s.StartSec > 0 {
			sStart = s.StartSec
		}
		sEnd := s.End
		if sEnd == 0 && s.EndSec > 0 {
			sEnd = s.EndSec
		}
		if !finite(sStart) || !finite(sEnd) || sStart < 0 || sEnd <= sStart {
			return nil, nil, failure("invalid_timeline", "segment times must be finite and end greater than start", map[string]any{"segment": i})
		}
		if s.Transition != "" && s.Transition != "cut" {
			return nil, nil, failure("invalid_request", "only cut transitions are supported", map[string]any{"segment": i})
		}
		if s.Position != "" && !contains(framePositions, s.Position) {
			return nil, nil, failure("invalid_request", "position is not supported", map[string]any{"segment": i, "allowed": framePositions})
		}
		if s.Position != "" && s.FocalPoint != nil {
			return nil, nil, failure("invalid_request", "segment may use position or focal_point, not both", map[string]any{"segment": i})
		}
		if s.FocalPoint != nil && (!finite(s.FocalPoint.X) || !finite(s.FocalPoint.Y) || s.FocalPoint.X < 0 || s.FocalPoint.X > 1 || s.FocalPoint.Y < 0 || s.FocalPoint.Y > 1) {
			return nil, nil, failure("invalid_request", "focal_point x and y must be finite values from 0 to 1", map[string]any{"segment": i})
		}
		total += sEnd - sStart
		if op == "estimate" {
			continue
		}
		p, _, e := probeWithContext(ctx, sIn, tmo)
		if e != nil {
			return nil, nil, e
		}
		d := p["format"].(map[string]any)["duration"].(float64)
		if sEnd > d+0.001 {
			return nil, nil, failure("invalid_timeline", "segment exceeds input duration", map[string]any{"segment": i, "duration": d})
		}
		probes[i] = p
		if !hasAudio(p) {
			silent++
		}
	}
	if r.ReplacementAudio != "" {
		if err = inputPath(r.ReplacementAudio); err != nil {
			return nil, nil, err
		}
		if samePath(r.ReplacementAudio, r.Output) {
			return nil, nil, failure("invalid_request", "output must differ from replacement audio", nil)
		}
		if op == "estimate" {
			return map[string]any{"estimated_cost": 0, "network": false, "external_write": false, "side_effect_free": true, "duration": total, "requested_operations": sourceEditRequestedOperations(r), "operations": []string{"validate_paths_and_timeline", "trim", "normalize", "concat", "replace_audio"}, "validation_scope": "request shape, paths, timeline ordering, target, framing, and cut-only transitions; stream presence and duration bounds are validated during run"}, nil, nil
		}
		replacementProbe, _, probeErr := probeWithContext(ctx, r.ReplacementAudio, tmo)
		if probeErr != nil {
			return nil, nil, probeErr
		}
		if !hasAudio(replacementProbe) {
			return nil, nil, failure("invalid_request", "replacement audio input has no audio stream", nil)
		}
	}
	if op == "estimate" {
		return map[string]any{"estimated_cost": 0, "network": false, "external_write": false, "side_effect_free": true, "duration": total, "requested_operations": sourceEditRequestedOperations(r), "operations": []string{"validate_paths_and_timeline", "trim", "normalize", "concat"}, "validation_scope": "request shape, paths, timeline ordering, target, framing, and cut-only transitions; stream presence and duration bounds are validated during run"}, nil, nil
	}
	tempOutput, cleanup, err := temporaryOutput(r.Output)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	args := []string{"-hide_banner", "-loglevel", "error", "-n"}
	for _, s := range r.Segments {
		sIn := s.Input
		if sIn == "" {
			sIn = s.InputPath
		}
		args = append(args, "-i", sIn)
	}
	replacementIndex := -1
	if r.ReplacementAudio != "" {
		replacementIndex = len(r.Segments)
		args = append(args, "-i", r.ReplacementAudio)
	}
	filters := []string{}
	concatInputs := strings.Builder{}
	for i, s := range r.Segments {
		sStart := s.Start
		if sStart == 0 && s.StartSec > 0 {
			sStart = s.StartSec
		}
		sEnd := s.End
		if sEnd == 0 && s.EndSec > 0 {
			sEnd = s.EndSec
		}
		dur := sEnd - sStart
		scale := "scale=" + strconv.Itoa(r.Target.Width) + ":" + strconv.Itoa(r.Target.Height) + ":force_original_aspect_ratio=decrease,pad=" + strconv.Itoa(r.Target.Width) + ":" + strconv.Itoa(r.Target.Height) + ":(ow-iw)/2:(oh-ih)/2"
		if r.Target.Fit == "cover" {
			x, y := cropPlacement(s)
			scale = "scale=" + strconv.Itoa(r.Target.Width) + ":" + strconv.Itoa(r.Target.Height) + ":force_original_aspect_ratio=increase,crop=" + strconv.Itoa(r.Target.Width) + ":" + strconv.Itoa(r.Target.Height) + ":" + x + ":" + y
		}
		filters = append(filters, fmt.Sprintf("[%d:v]trim=start=%s:end=%s,setpts=PTS-STARTPTS,%s,setsar=1,fps=%s,format=yuv420p[v%d]", i, formatFloat(sStart), formatFloat(sEnd), scale, formatFloat(r.Target.FPS), i))
		if hasAudio(probes[i]) {
			filters = append(filters, fmt.Sprintf("[%d:a]atrim=start=%s:end=%s,asetpts=PTS-STARTPTS,aformat=sample_rates=48000:channel_layouts=stereo[a%d]", i, formatFloat(sStart), formatFloat(sEnd), i))
		} else {
			filters = append(filters, fmt.Sprintf("anullsrc=r=48000:cl=stereo,atrim=duration=%s,asetpts=PTS-STARTPTS[a%d]", formatFloat(dur), i))
		}
		fmt.Fprintf(&concatInputs, "[v%d][a%d]", i, i)
	}
	filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=1:a=1[vcat][acat]", concatInputs.String(), len(r.Segments)))
	audioLabel := "[acat]"
	if replacementIndex >= 0 {
		// Keep concat timing unchanged while consuming the discarded source audio.
		filters = append(filters, "[acat]anullsink")
		filters = append(filters, fmt.Sprintf("[%d:a]apad,atrim=duration=%s,asetpts=PTS-STARTPTS,aformat=sample_rates=48000:channel_layouts=stereo[replacement]", replacementIndex, formatFloat(total)))
		audioLabel = "[replacement]"
	}
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[vcat]", "-map", audioLabel, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-r", formatFloat(r.Target.FPS), "-c:a", "aac", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", tempOutput)
	if _, err = runCommandDirContext(ctx, tmo, "", "ffmpeg", args...); err != nil {
		return nil, nil, err
	}
	out, w, err := probeWithContext(ctx, tempOutput, tmo)
	if err != nil {
		return nil, nil, failure("output_validation_failed", "created temporary output could not be validated", map[string]any{"error": err.Error()})
	}
	if err = finalizeOutput(tempOutput, r.Output, r.Overwrite); err != nil {
		return nil, nil, err
	}
	ops := []string{"trim", "scale_" + r.Target.Fit, "normalize", "concat", "cut"}
	for _, s := range r.Segments {
		if s.Position != "" || s.FocalPoint != nil {
			ops = append(ops, "framing")
			break
		}
	}
	if replacementIndex >= 0 {
		ops = append(ops, "replace_audio")
	}
	return map[string]any{
		"output":               r.Output,
		"duration":             out["format"].(map[string]any)["duration"],
		"realized_segments":    len(r.Segments),
		"silent_inputs_filled": silent,
		"requested_operations": sourceEditRequestedOperations(r),
		"realized_operations":  ops,
		"output_facts":         out,
	}, w, nil
}

func doVideoTrimmer(op string, data []byte) (any, []string, error) {
	return doVideoTrimmerContext(context.Background(), op, data)
}

func doVideoTrimmerContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r trimmerRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	operation := r.Operation
	if operation == "" {
		operation = "cut"
	}
	tmo, err := positiveTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		return estimateResult([]string{"video_trimmer_" + operation}), nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, tmo)
	defer cancel()

	switch operation {
	case "cut":
		if err := inputPath(r.InputPath); err != nil {
			return nil, nil, err
		}
		outPath := r.OutputPath
		if outPath == "" {
			ext := filepath.Ext(r.InputPath)
			base := strings.TrimSuffix(r.InputPath, ext)
			outPath = base + "_cut" + ext
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		codec := r.Codec
		if codec == "" {
			codec = "copy"
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.InputPath, "-ss", formatFloat(r.StartSeconds)}
		if r.EndSeconds != nil {
			args = append(args, "-to", formatFloat(*r.EndSeconds))
		}
		if codec == "copy" {
			args = append(args, "-c", "copy")
		} else {
			args = append(args, "-c:v", codec, "-c:a", "aac")
		}
		args = append(args, outPath)
		if _, err := runCommandContext(ctx, "ffmpeg", args...); err != nil {
			return nil, nil, err
		}
		result := map[string]any{
			"operation":     "cut",
			"input":         r.InputPath,
			"output":        outPath,
			"start_seconds": r.StartSeconds,
		}
		// Present only when requested: a cut without an end runs to the end
		// of the input, and null is not a number.
		if r.EndSeconds != nil {
			result["end_seconds"] = *r.EndSeconds
		}
		return result, nil, nil

	case "speed":
		if err := inputPath(r.InputPath); err != nil {
			return nil, nil, err
		}
		outPath := r.OutputPath
		if outPath == "" {
			ext := filepath.Ext(r.InputPath)
			base := strings.TrimSuffix(r.InputPath, ext)
			outPath = base + "_speed" + ext
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		factor := r.SpeedFactor
		if factor <= 0 {
			factor = 1.0
		}
		videoFilter := fmt.Sprintf("setpts=%s*PTS", formatFloat(1.0/factor))
		audioFilter := buildAtempoChain(factor)
		args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.InputPath, "-filter:v", videoFilter, "-filter:a", audioFilter, "-c:v", "libx264", "-preset", "fast", "-c:a", "aac", outPath}
		if _, err := runCommandContext(ctx, "ffmpeg", args...); err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"operation":    "speed",
			"input":        r.InputPath,
			"output":       outPath,
			"speed_factor": factor,
		}, nil, nil

	case "concat":
		if len(r.Segments) == 0 {
			return nil, nil, failure("invalid_request", "segments required for concat", nil)
		}
		outPath := r.OutputPath
		if outPath == "" {
			outPath = defaultOutput(ctx, "concat_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		tempDir, err := os.MkdirTemp(filepath.Dir(outPath), ".concat_tmp-*")
		if err != nil {
			return nil, nil, failure("command_failed", "unable to create temporary directory", nil)
		}
		defer os.RemoveAll(tempDir)

		tempFiles := []string{}
		for i, seg := range r.Segments {
			sIn := seg.Input
			if sIn == "" {
				sIn = seg.InputPath
			}
			if err := inputPath(sIn); err != nil {
				return nil, nil, err
			}
			sStart := seg.Start
			if sStart == 0 && seg.StartSec > 0 {
				sStart = seg.StartSec
			}
			sEnd := seg.End
			if sEnd == 0 && seg.EndSec > 0 {
				sEnd = seg.EndSec
			}
			if sStart > 0 || sEnd > 0 {
				tmpSeg := filepath.Join(tempDir, fmt.Sprintf("seg_%04d%s", i, filepath.Ext(sIn)))
				cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", sIn}
				if sStart > 0 {
					cmdArgs = append(cmdArgs, "-ss", formatFloat(sStart))
				}
				if sEnd > 0 {
					cmdArgs = append(cmdArgs, "-to", formatFloat(sEnd))
				}
				cmdArgs = append(cmdArgs, "-c", "copy", tmpSeg)
				if _, err := runCommandContext(ctx, "ffmpeg", cmdArgs...); err != nil {
					return nil, nil, err
				}
				tempFiles = append(tempFiles, tmpSeg)
			} else {
				tempFiles = append(tempFiles, sIn)
			}
		}

		listPath := filepath.Join(tempDir, "concat_list.txt")
		listBuilder := strings.Builder{}
		for _, tf := range tempFiles {
			abs, _ := filepath.Abs(tf)
			clean := strings.ReplaceAll(abs, `\`, `/`)
			fmt.Fprintf(&listBuilder, "file '%s'\n", clean)
		}
		if err := os.WriteFile(listPath, []byte(listBuilder.String()), 0644); err != nil {
			return nil, nil, failure("command_failed", "unable to write concat list", nil)
		}

		cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", outPath}
		if _, err := runCommandContext(ctx, "ffmpeg", cmdArgs...); err != nil {
			return nil, nil, err
		}

		return map[string]any{
			"operation":     "concat",
			"segment_count": len(r.Segments),
			"output":        outPath,
		}, nil, nil

	default:
		return nil, nil, failure("invalid_request", "unknown operation: "+operation, nil)
	}
}

func buildAtempoChain(factor float64) string {
	if factor <= 0 {
		factor = 1.0
	}
	filters := []string{}
	remaining := factor
	for remaining > 100.0 {
		filters = append(filters, "atempo=100.0")
		remaining /= 100.0
	}
	for remaining < 0.5 {
		filters = append(filters, "atempo=0.5")
		remaining /= 0.5
	}
	filters = append(filters, fmt.Sprintf("atempo=%.4f", remaining))
	return strings.Join(filters, ",")
}
