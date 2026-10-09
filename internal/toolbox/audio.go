package toolbox

import (
	"context"
	"fmt"
	"strings"
)

type audioOps struct {
	GainDB  float64 `json:"gain_db,omitempty"`
	FadeIn  float64 `json:"fade_in,omitempty"`
	FadeOut float64 `json:"fade_out,omitempty"`
}

type ducking struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold,omitempty"`
	Ratio     float64 `json:"ratio,omitempty"`
}

type musicOps struct {
	Input   string   `json:"input"`
	GainDB  float64  `json:"gain_db,omitempty"`
	FadeIn  float64  `json:"fade_in,omitempty"`
	FadeOut float64  `json:"fade_out,omitempty"`
	Ducking *ducking `json:"ducking,omitempty"`
}

type loudness struct {
	Enabled        bool    `json:"enabled"`
	IntegratedLUFS float64 `json:"integrated_lufs,omitempty"`
	TruePeakDB     float64 `json:"true_peak_db,omitempty"`
}

type mixRequest struct {
	Video          string    `json:"video"`
	Source         *audioOps `json:"source,omitempty"`
	Music          *musicOps `json:"music,omitempty"`
	Loudness       *loudness `json:"loudness,omitempty"`
	Duration       string    `json:"duration"`
	Output         string    `json:"output"`
	Overwrite      bool      `json:"overwrite,omitempty"`
	TimeoutSeconds int       `json:"timeout_seconds,omitempty"`
}

func validateFades(o audioOps, d float64) error {
	if !finite(o.GainDB) || !finite(o.FadeIn) || !finite(o.FadeOut) || o.FadeIn < 0 || o.FadeOut < 0 || o.FadeIn > d || o.FadeOut > d || o.FadeIn+o.FadeOut > d {
		return failure("invalid_request", "audio gain/fades must be finite, non-negative, and combined fades must fit duration", nil)
	}
	return nil
}

func audioChain(label string, o audioOps, d float64) string {
	parts := []string{fmt.Sprintf("volume=%sdB", formatFloat(o.GainDB))}
	if o.FadeIn > 0 {
		parts = append(parts, fmt.Sprintf("afade=t=in:st=0:d=%s", formatFloat(o.FadeIn)))
	}
	if o.FadeOut > 0 {
		parts = append(parts, fmt.Sprintf("afade=t=out:st=%s:d=%s", formatFloat(d-o.FadeOut), formatFloat(o.FadeOut)))
	}
	return label + strings.Join(parts, ",")
}

func requestedAudioOperations(r mixRequest) []string {
	ops := []string{}
	if r.Source != nil {
		if r.Source.GainDB != 0 {
			ops = append(ops, "source_gain")
		}
		if r.Source.FadeIn > 0 || r.Source.FadeOut > 0 {
			ops = append(ops, "source_fades")
		}
	}
	if r.Music != nil {
		ops = append(ops, "music_mix")
		if r.Music.GainDB != 0 {
			ops = append(ops, "music_gain")
		}
		if r.Music.FadeIn > 0 || r.Music.FadeOut > 0 {
			ops = append(ops, "music_fades")
		}
		if r.Music.Ducking != nil && r.Music.Ducking.Enabled {
			ops = append(ops, "duck")
		}
	}
	if r.Loudness != nil && r.Loudness.Enabled {
		ops = append(ops, "loudnorm")
	}
	return ops
}

func validateAudioValues(r mixRequest, duration float64, checkDuration bool) error {
	validate := func(o audioOps) error {
		if !finite(o.GainDB) || !finite(o.FadeIn) || !finite(o.FadeOut) || o.FadeIn < 0 || o.FadeOut < 0 {
			return failure("invalid_request", "audio gain and fades must be finite and fades non-negative", nil)
		}
		if checkDuration {
			return validateFades(o, duration)
		}
		return nil
	}
	if r.Source != nil {
		if err := validate(*r.Source); err != nil {
			return err
		}
	}
	if r.Music != nil {
		if err := validate(audioOps{r.Music.GainDB, r.Music.FadeIn, r.Music.FadeOut}); err != nil {
			return err
		}
		if d := r.Music.Ducking; d != nil && d.Enabled && (!finite(d.Threshold) || !finite(d.Ratio) || d.Threshold <= 0 || d.Threshold > 1 || d.Ratio <= 1 || d.Ratio > 20) {
			return failure("invalid_request", "enabled ducking requires threshold in (0,1] and ratio in (1,20]", nil)
		}
	}
	if r.Loudness != nil && r.Loudness.Enabled && (!finite(r.Loudness.IntegratedLUFS) || !finite(r.Loudness.TruePeakDB) || r.Loudness.IntegratedLUFS < -70 || r.Loudness.IntegratedLUFS > -5 || r.Loudness.TruePeakDB < -9 || r.Loudness.TruePeakDB > 0) {
		return failure("invalid_request", "enabled loudness requires integrated_lufs from -70 to -5 and true_peak_db from -9 to 0", nil)
	}
	return nil
}

func doAudioMix(op string, data []byte) (any, []string, error) {
	return doAudioMixContext(context.Background(), op, data)
}

func doAudioMixContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r mixRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	tmo, err := positiveTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}
	if err = inputPath(r.Video); err != nil {
		return nil, nil, err
	}
	if r.Source == nil && r.Music == nil && r.Loudness == nil {
		return nil, nil, failure("invalid_request", "at least one source, music, or loudness operation is required", nil)
	}
	if (r.Source == nil || (r.Source.GainDB == 0 && r.Source.FadeIn == 0 && r.Source.FadeOut == 0)) && r.Music == nil && (r.Loudness == nil || !r.Loudness.Enabled) {
		return nil, nil, failure("invalid_request", "audio_mix request contains no enabled or non-zero operation", nil)
	}
	if r.Duration != "video" {
		return nil, nil, failure("invalid_request", "duration must be video", nil)
	}
	if err = outputPath(r.Output, r.Overwrite, op == "estimate"); err != nil {
		return nil, nil, err
	}
	if samePath(r.Video, r.Output) {
		return nil, nil, failure("invalid_request", "output must differ from video input", nil)
	}
	requested := requestedAudioOperations(r)
	if r.Music != nil {
		if err = inputPath(r.Music.Input); err != nil {
			return nil, nil, err
		}
		if samePath(r.Music.Input, r.Output) {
			return nil, nil, failure("invalid_request", "output must differ from music input", nil)
		}
	}
	if err = validateAudioValues(r, 0, false); err != nil {
		return nil, nil, err
	}
	if op == "estimate" {
		return map[string]any{"estimated_cost": 0, "network": false, "external_write": false, "side_effect_free": true, "requested_operations": requested, "operations": []string{"validate_paths_and_audio_parameters", "ffprobe", "mix"}, "validation_scope": "request shape, paths, gains, fades, ducking, and loudness ranges; stream presence and fade fit against decoded duration are validated during run"}, nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, tmo)
	defer cancel()
	p, _, err := probeContext(ctx, r.Video)
	if err != nil {
		return nil, nil, err
	}
	d := p["format"].(map[string]any)["duration"].(float64)
	sourceAudio := hasAudio(p)
	if r.Source != nil && !sourceAudio {
		return nil, nil, failure("invalid_request", "source operations require video source audio", nil)
	}
	if err = validateAudioValues(r, d, true); err != nil {
		return nil, nil, err
	}
	if r.Music != nil {
		musicProbe, _, probeErr := probeContext(ctx, r.Music.Input)
		if probeErr != nil {
			return nil, nil, probeErr
		}
		if !hasAudio(musicProbe) {
			return nil, nil, failure("invalid_request", "music input has no audio stream", nil)
		}
		if r.Music.Ducking != nil && r.Music.Ducking.Enabled && !sourceAudio {
			return nil, nil, failure("invalid_request", "ducking requires source audio", nil)
		}
	}
	tempOutput, cleanup, err := temporaryOutput(r.Output)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	args := []string{"-hide_banner", "-loglevel", "info", "-n", "-i", r.Video}
	if r.Music != nil {
		args = append(args, "-stream_loop", "-1", "-i", r.Music.Input)
	}
	filters := []string{}
	ops := append([]string(nil), requested...)
	current := "[0:a]"
	if !sourceAudio {
		filters = append(filters, fmt.Sprintf("anullsrc=r=48000:cl=stereo,atrim=duration=%s[src]", formatFloat(d)))
		current = "[src]"
	}
	if r.Source != nil {
		filters = append(filters, audioChain(current, *r.Source, d)+"[source]")
		current = "[source]"
	}
	if r.Music != nil {
		filters = append(filters, audioChain("[1:a]", audioOps{r.Music.GainDB, r.Music.FadeIn, r.Music.FadeOut}, d)+",atrim=duration="+formatFloat(d)+"[music]")
		music := "[music]"
		if r.Music.Ducking != nil && r.Music.Ducking.Enabled {
			th := r.Music.Ducking.Threshold
			ratio := r.Music.Ducking.Ratio
			filters = append(filters, fmt.Sprintf("%sasplit=2[source_mix][source_key]", current))
			current = "[source_mix]"
			filters = append(filters, fmt.Sprintf("[music][source_key]sidechaincompress=threshold=%s:ratio=%s[ducked]", formatFloat(th), formatFloat(ratio)))
			music = "[ducked]"
		}
		filters = append(filters, fmt.Sprintf("%s%samix=inputs=2:duration=first:normalize=0[mixed]", current, music))
		current = "[mixed]"
	}
	if r.Loudness != nil && r.Loudness.Enabled {
		filters = append(filters, fmt.Sprintf("%sloudnorm=I=%s:TP=%s:LRA=11:print_format=json[outa]", current, formatFloat(r.Loudness.IntegratedLUFS), formatFloat(r.Loudness.TruePeakDB)))
		current = "[outa]"
	}
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "0:v:0", "-map", current, "-c:v", "copy", "-c:a", "aac", "-ar", "48000", "-ac", "2", "-t", formatFloat(d), tempOutput)
	diag, err := runCommandContext(ctx, "ffmpeg", args...)
	if err != nil {
		return nil, nil, err
	}
	out, w, err := probeContext(ctx, tempOutput)
	if err != nil {
		return nil, nil, failure("output_validation_failed", "mixed temporary output could not be validated", map[string]any{"error": err.Error()})
	}
	if err = finalizeOutput(tempOutput, r.Output, r.Overwrite); err != nil {
		return nil, nil, err
	}
	facts := parseLoudnorm(string(diag))
	return map[string]any{
		"output":               r.Output,
		"duration":             out["format"].(map[string]any)["duration"],
		"requested_operations": requested,
		"realized_operations":  ops,
		"loudnorm":             facts,
		"output_facts":         out,
	}, w, nil
}
