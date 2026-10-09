package toolbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type probeRequest struct {
	Input          string `json:"input,omitempty"`
	InputPath      string `json:"input_path,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type ffprobe struct {
	Format struct {
		Duration       string `json:"duration"`
		FormatName     string `json:"format_name"`
		FormatLongName string `json:"format_long_name"`
		Size           string `json:"size"`
		BitRate        string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		Index         int               `json:"index"`
		CodecType     string            `json:"codec_type"`
		CodecName     string            `json:"codec_name"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		PixFmt        string            `json:"pix_fmt"`
		AvgFrameRate  string            `json:"avg_frame_rate"`
		RFrameRate    string            `json:"r_frame_rate"`
		SampleRate    string            `json:"sample_rate"`
		Channels      int               `json:"channels"`
		ChannelLayout string            `json:"channel_layout"`
		BitRate       string            `json:"bit_rate"`
		Tags          map[string]string `json:"tags"`
		SideData      []struct {
			Rotation int `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
}

func parseFPS(s string) float64 {
	p := strings.Split(s, "/")
	if len(p) == 2 {
		d := parseFloat(p[1])
		if d != 0 {
			return parseFloat(p[0]) / d
		}
	}
	return parseFloat(s)
}

func probe(path string, timeout time.Duration) (map[string]any, []string, error) {
	return probeWithContext(context.Background(), path, timeout)
}

func probeWithContext(parent context.Context, path string, timeout time.Duration) (map[string]any, []string, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return probeContext(ctx, path)
}

func probeDuration(path string, timeout time.Duration) (float64, error) {
	return probeDurationContext(context.Background(), path, timeout)
}

func probeDurationContext(ctx context.Context, path string, timeout time.Duration) (float64, error) {
	p, _, err := probeWithContext(ctx, path, timeout)
	if err != nil {
		return 0, err
	}
	fmtMap, ok := p["format"].(map[string]any)
	if !ok {
		return 0, failure("input_probe_failed", "format information missing", nil)
	}
	dur, ok := fmtMap["duration"].(float64)
	if !ok {
		return 0, failure("input_probe_failed", "duration information missing", nil)
	}
	return dur, nil
}

func probeContext(ctx context.Context, path string) (map[string]any, []string, error) {
	if err := inputPath(path); err != nil {
		return nil, nil, err
	}
	out, err := runCommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", path)
	if err != nil {
		var tf *toolFailure
		if errors.As(err, &tf) && tf.err.Code == "command_failed" {
			tf.err.Code = "input_probe_failed"
			tf.err.Message = "ffprobe could not inspect input"
		}
		return nil, nil, err
	}
	var raw ffprobe
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, nil, failure("input_probe_failed", "ffprobe returned invalid JSON", map[string]any{"error": bounded(err.Error())})
	}
	duration := parseFloat(raw.Format.Duration)
	warnings := []string{}
	// Initialized EMPTY, not nil. A nil slice marshals to JSON `null`, and
	// media_probe's own result_schema declares video_streams and audio_streams
	// as REQUIRED arrays -- so a video-only file made the tool violate its own
	// published contract on a completely legitimate input.
	//
	// Found by a target CLI reading the schema and the result together during
	// Release C bundle testing, which is the whole point of that surface: the
	// agent compared what the tool ADVERTISED against what it RETURNED, and
	// nothing in this repo had done that.
	//
	// Same defect the v2 envelope work already fixed one layer up -- a null
	// collection forces every consumer into a nil check the contract says is
	// unnecessary -- surviving inside a tool result where that fix did not
	// reach.
	videos := []map[string]any{}
	audios := []map[string]any{}
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			rotation := 0
			for _, sd := range s.SideData {
				if sd.Rotation != 0 {
					rotation = sd.Rotation
				}
			}
			if r := s.Tags["rotate"]; r != "" {
				rotation = int(parseFloat(r))
			}
			videos = append(videos, map[string]any{
				"index":               s.Index,
				"codec":               s.CodecName,
				"width":               s.Width,
				"height":              s.Height,
				"pixel_format":        s.PixFmt,
				"fps":                 parseFPS(s.AvgFrameRate),
				"reported_frame_rate": parseFPS(s.RFrameRate),
				"rotation":            rotation,
			})
		case "audio":
			bitrate := int64(parseFloat(s.BitRate))
			aMap := map[string]any{
				"index":          s.Index,
				"codec":          s.CodecName,
				"sample_rate":    int(parseFloat(s.SampleRate)),
				"channels":       s.Channels,
				"channel_layout": s.ChannelLayout,
			}
			if bitrate > 0 {
				aMap["bit_rate"] = bitrate
			}
			audios = append(audios, aMap)
		}
	}
	if len(videos) == 0 {
		warnings = append(warnings, "no video stream found")
	}
	if len(audios) == 0 {
		warnings = append(warnings, "no audio stream found")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	h := sha256.New()
	buf := make([]byte, 128*1024)
	for {
		if err = ctx.Err(); err != nil {
			_ = f.Close()
			return nil, nil, failure("command_timeout", "media hashing timed out", nil)
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			return nil, nil, failure("command_failed", "media hashing failed", map[string]any{"error": bounded(readErr.Error())})
		}
	}
	_ = f.Close()
	result := map[string]any{
		"input":  path,
		"sha256": hex.EncodeToString(h.Sum(nil)),
		"format": map[string]any{
			"duration":         duration,
			"format_name":      raw.Format.FormatName,
			"format_long_name": raw.Format.FormatLongName,
			"size":             int64(parseFloat(raw.Format.Size)),
			"bit_rate":         int64(parseFloat(raw.Format.BitRate)),
		},
		"video_streams": videos,
		"audio_streams": audios,
		"warnings":      warnings,
	}
	if len(videos) > 0 {
		result["video"] = videos[0]
	}
	result["audio"] = audios
	return result, warnings, nil
}

func doMediaProbe(op string, data []byte) (any, []string, error) {
	return doMediaProbeContext(context.Background(), op, data)
}

func doMediaProbeContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r probeRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	targetPath := r.Input
	if targetPath == "" {
		targetPath = r.InputPath
	}
	if targetPath == "" {
		return nil, nil, failure("invalid_request", "input path is required", nil)
	}
	t, err := positiveTimeout(r.TimeoutSeconds, 30)
	if err != nil {
		return nil, nil, err
	}
	if err := inputPath(targetPath); err != nil {
		return nil, nil, err
	}
	if op == "estimate" {
		ext := strings.ToLower(filepath.Ext(targetPath))
		if ext == "" {
			return nil, nil, failure("invalid_request", "input must have a media file extension", nil)
		}
		return estimateResult([]string{"validate_extension", "ffprobe", "sha256"}), []string{"estimate validates request shape and basic file eligibility; media decoding and stream validation occur only during run"}, nil
	}
	return probeWithContext(ctx, targetPath, t)
}

func doAudioProbe(op string, data []byte) (any, []string, error) {
	return doAudioProbeContext(context.Background(), op, data)
}

func doAudioProbeContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r probeRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	targetPath := r.InputPath
	if targetPath == "" {
		targetPath = r.Input
	}
	if targetPath == "" {
		return nil, nil, failure("invalid_request", "input_path is required", nil)
	}
	t, err := positiveTimeout(r.TimeoutSeconds, 15)
	if err != nil {
		return nil, nil, err
	}
	if err := inputPath(targetPath); err != nil {
		return nil, nil, err
	}
	if op == "estimate" {
		return estimateResult([]string{"validate_path", "ffprobe"}), []string{"audio_probe inspects audio container and stream properties"}, nil
	}
	p, warnings, err := probeWithContext(ctx, targetPath, t)
	if err != nil {
		return nil, nil, err
	}
	fmtInfo, _ := p["format"].(map[string]any)
	audios, _ := p["audio_streams"].([]map[string]any)
	res := map[string]any{
		"file":             targetPath,
		"duration_seconds": fmtInfo["duration"],
		"format_name":      fmtInfo["format_name"],
		"format_long_name": fmtInfo["format_long_name"],
		"size_bytes":       fmtInfo["size"],
		"bit_rate":         fmtInfo["bit_rate"],
		"stream_count":     len(audios),
	}
	if len(audios) > 0 {
		a := audios[0]
		audioData := map[string]any{
			"codec":          a["codec"],
			"sample_rate":    a["sample_rate"],
			"channels":       a["channels"],
			"channel_layout": a["channel_layout"],
		}
		if br, ok := a["bit_rate"]; ok {
			audioData["bit_rate"] = br
		}
		res["audio"] = audioData
	}
	return res, warnings, nil
}
