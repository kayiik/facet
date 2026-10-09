package toolbox

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSourceEditAudioFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	voiced := filepath.Join(dir, "voiced.mp4")
	silent := filepath.Join(dir, "silent.mp4")
	long := filepath.Join(dir, "long.wav")
	short := filepath.Join(dir, "short.wav")
	ffmpeg(t, "-v", "error", "-f", "lavfi", "-i", "color=c=red:size=64x48:rate=25:duration=1.5", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1.5", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", voiced)
	ffmpeg(t, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:size=64x48:rate=25:duration=1.5", "-c:v", "libx264", "-pix_fmt", "yuv420p", silent)
	ffmpeg(t, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=3", long)
	ffmpeg(t, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=0.6", short)

	for _, tc := range []struct {
		name        string
		first       string
		replacement string
		tones       [2]float64
		silentCount int
	}{
		{"replacement_trimmed", voiced, long, [2]float64{880, 880}, 1},
		{"replacement_padded", voiced, short, [2]float64{880, 0}, 1},
		{"original_and_silence", voiced, "", [2]float64{440, 0}, 1},
		{"silent_sources_replaced", silent, long, [2]float64{880, 880}, 2},
		{"silent_sources_preserved", silent, "", [2]float64{0, 0}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(dir, tc.name+".mp4")
			r := editRequest{
				Segments:         []segment{{Input: tc.first, Start: 0.2, End: 1.2}, {Input: silent, Start: 0.2, End: 1.2}},
				Target:           target{Width: 64, Height: 48, FPS: 25, Fit: "contain", VideoCodec: "h264", PixelFormat: "yuv420p", AudioCodec: "aac", AudioSampleRate: 48000, AudioChannels: 2},
				ReplacementAudio: tc.replacement, Output: output, TimeoutSeconds: 30,
			}
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			result, _, err := doSourceEdit("run", data)
			if err != nil {
				t.Fatalf("source_edit: %#v", errorEnvelope("source_edit", "run", err).Error)
			}
			facts := result.(map[string]any)
			if facts["silent_inputs_filled"] != tc.silentCount || facts["realized_segments"] != 2 {
				t.Fatalf("unexpected edit facts: %#v", facts)
			}
			if contains(facts["realized_operations"].([]string), "replace_audio") != (tc.replacement != "") {
				t.Fatalf("unexpected realized operations: %v", facts["realized_operations"])
			}

			// Check each stream, not just container duration (which can hide a truncated track).
			out, err := runCommand(30*time.Second, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,duration", "-of", "json", output)
			if err != nil {
				t.Fatal(err)
			}
			var p struct {
				Streams []struct {
					Type     string `json:"codec_type"`
					Duration string `json:"duration"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(out, &p); err != nil {
				t.Fatal(err)
			}
			seen := map[string]int{}
			for _, s := range p.Streams {
				seen[s.Type]++
				d, err := strconv.ParseFloat(s.Duration, 64)
				if err != nil || math.Abs(d-2) > 0.08 {
					t.Fatalf("%s duration = %q, want 2s (+/-80ms)", s.Type, s.Duration)
				}
				t.Logf("%s duration: %.3fs", s.Type, d)
			}
			if seen["video"] != 1 || seen["audio"] != 1 || len(p.Streams) != 2 {
				t.Fatalf("unexpected streams: %+v", p.Streams)
			}
			pcm, err := runCommand(30*time.Second, "ffmpeg", "-v", "error", "-i", output, "-map", "0:a:0", "-f", "f32le", "-acodec", "pcm_f32le", "-ac", "1", "-ar", "8000", "pipe:1")
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(float64(len(pcm))/4/8000-2) > 0.08 {
				t.Fatalf("decoded audio length = %d bytes, want about 2s", len(pcm))
			}
			// Measure tone energy away from cuts and AAC boundaries; phase is irrelevant.
			for window, want := range tc.tones {
				start := window*8000 + 2400
				const n = 1600
				var energy float64
				var re, im [2]float64
				for i := 0; i < n; i++ {
					x := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[(start+i)*4:])))
					energy += x * x
					for j, hz := range []float64{440, 880} {
						angle := 2 * math.Pi * hz * float64(i) / 8000
						re[j] += x * math.Cos(angle)
						im[j] += x * math.Sin(angle)
					}
				}
				rms := math.Sqrt(energy / n)
				original := 2 * math.Hypot(re[0], im[0]) / n
				replacement := 2 * math.Hypot(re[1], im[1]) / n
				t.Logf("segment %d: RMS=%.5f, 440Hz=%.5f, 880Hz=%.5f", window, rms, original, replacement)
				if want == 0 {
					if rms > 0.001 {
						t.Fatalf("segment %d should be silent, RMS=%f", window, rms)
					}
				} else {
					desired, unwanted := original, replacement
					if want == 880 {
						desired, unwanted = replacement, original
					}
					if desired < 0.03 || unwanted > desired/20 {
						t.Fatalf("segment %d: want %.0fHz, desired=%f unwanted=%f", window, want, desired, unwanted)
					}
				}
			}
		})
	}
}

func TestSourceEditReplacementEstimateNoEffects(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.mp4")
	replacement := filepath.Join(dir, "replacement.wav")
	for _, path := range []string{input, replacement} {
		if err := os.WriteFile(path, []byte("shape-only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", "") // Estimate must not invoke FFmpeg or FFprobe.
	output := filepath.Join(dir, "not-created", "output.mp4")
	r := editRequest{
		Segments:         []segment{{Input: input, Start: 0.2, End: 1.2}},
		Target:           target{Width: 64, Height: 48, FPS: 25, Fit: "contain", VideoCodec: "h264", PixelFormat: "yuv420p", AudioCodec: "aac", AudioSampleRate: 48000, AudioChannels: 2},
		ReplacementAudio: replacement, Output: output,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := doSourceEdit("estimate", data)
	if err != nil {
		t.Fatal(err)
	}
	facts := result.(map[string]any)
	if facts["duration"] != 1.0 || facts["side_effect_free"] != true || facts["network"] != false || !contains(facts["requested_operations"].([]string), "replace_audio") {
		t.Fatalf("unexpected estimate: %#v", facts)
	}
	if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
		t.Fatalf("estimate created output directory or stat failed: %v", err)
	}
}
