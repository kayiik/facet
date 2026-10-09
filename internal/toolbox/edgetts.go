package toolbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kolonist/edgetts"
)

// edgeTTSRequest has no pitch field on purpose. The synthesis library
// (github.com/kolonist/edgetts) accepts only voice, rate and volume, and
// always sends a default pitch, so a requested pitch could only ever be
// accepted and silently ignored. Leaving it out makes such a request fail as
// an unknown field instead.
type edgeTTSRequest struct {
	Text           string `json:"text"`
	Voice          string `json:"voice,omitempty"`
	Rate           string `json:"rate,omitempty"`
	Volume         string `json:"volume,omitempty"`
	OutputPath     string `json:"output_path,omitempty"`
	Output         string `json:"output,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// edgeAdjustment is the only form the service accepts for rate and volume.
var edgeAdjustment = regexp.MustCompile(`^[+-]\d+%$`)

// edgeSynthesize returns MP3 bytes for text and the service's word timings. A
// variable so tests can run the tool end to end without reaching the service.
var edgeSynthesize = synthesizeEdgeTTSContext

func doEdgeTTS(op string, data []byte) (any, []string, error) {
	return doEdgeTTSContext(context.Background(), op, data)
}

func doEdgeTTSContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r edgeTTSRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Text) == "" {
		return nil, nil, failure("invalid_request", "text is required", nil)
	}

	voice := r.Voice
	if voice == "" {
		voice = "en-US-ChristopherNeural"
	}
	rate := r.Rate
	if rate == "" {
		rate = "+0%"
	}
	volume := r.Volume
	if volume == "" {
		volume = "+0%"
	}
	if !edgeAdjustment.MatchString(rate) || !edgeAdjustment.MatchString(volume) {
		return nil, nil, failure("invalid_request", "rate and volume must look like +10% or -20%", map[string]any{"rate": rate, "volume": volume})
	}
	timeout, err := positiveTimeout(r.TimeoutSeconds, 30)
	if err != nil {
		return nil, nil, err
	}

	outPath := r.OutputPath
	if outPath == "" {
		outPath = r.Output
	}
	if outPath == "" {
		outPath = defaultOutput(ctx, "edge_tts.mp3")
	}

	if op == "estimate" {
		res := estimateResult([]string{"edge_tts_synthesize"})
		res["voice"] = voice
		res["output"] = outPath
		res["estimated_cost"] = 0.0
		res["network"] = true
		return res, nil, nil
	}

	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}

	audioBytes, words, err := edgeSynthesize(ctx, r.Text, voice, rate, volume, timeout)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, nil, failure("command_timeout", timeoutMessage("edge-tts synthesis", timeout), nil)
		}
		return nil, nil, failure("tts_failed", fmt.Sprintf("edge-tts synthesis failed: %v", err), nil)
	}

	if err := os.WriteFile(outPath, audioBytes, 0o644); err != nil {
		return nil, nil, failure("output_failed", fmt.Sprintf("failed to write output: %v", err), nil)
	}

	res := map[string]any{
		"output":     outPath,
		"voice":      voice,
		"size_bytes": len(audioBytes),
		"format":     "mp3",
		"provider":   "microsoft_edge",
	}
	if len(words) > 0 {
		// The service's own word timings, in seconds from the start of the
		// clip: exact caption timing without listening to the audio again.
		timed := make([]map[string]any, 0, len(words))
		for _, w := range words {
			text := strings.TrimSpace(w.Text)
			if text == "" {
				continue
			}
			start := float64(w.Offset) / 1000
			timed = append(timed, map[string]any{"word": text, "start": start, "end": start + float64(w.Duration)/1000})
		}
		res["words"] = timed
	}
	// Measured from the written file, the same way the other speech tools
	// measure theirs. Optional: the audio is delivered even where ffprobe is
	// unavailable.
	if dur, err := probeDurationContext(ctx, outPath, 5*time.Second); err == nil && dur > 0 {
		res["duration_seconds"] = dur
	}
	return res, nil, nil
}

func synthesizeEdgeTTSContext(parent context.Context, text, voice, rate, volume string, timeout time.Duration) ([]byte, []edgetts.SpeechMetadata, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	client := edgetts.New(edgetts.Args{Voice: voice, Rate: rate, Volume: volume})
	type outcome struct {
		audio []byte
		words []edgetts.SpeechMetadata
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		speaker := client.Speak(text)
		audio, err := speaker.GetSound(ctx, edgetts.OutputFormatMp3)
		var words []edgetts.SpeechMetadata
		if err == nil {
			// Word timings are a bonus: a service that sends none still
			// delivers the audio.
			words, _ = speaker.GetMetadata()
		}
		done <- outcome{audio, words, err}
	}()
	select {
	case o := <-done:
		return o.audio, o.words, o.err
	case <-ctx.Done():
		// The library honours the context only while connecting; a service
		// that stalls after the handshake would hold the call forever. The
		// reader is abandoned and exits when the connection closes.
		return nil, nil, ctx.Err()
	}
}
