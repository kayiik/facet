package toolbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kolonist/edgetts"
)

func TestKolonistEdgeTTS_Synthesis(t *testing.T) {
	if testing.Short() {
		t.Skip("live Microsoft Edge TTS integration; run without -short explicitly")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Test ListVoices
	voices, err := edgetts.ListVoices(ctx)
	if err != nil {
		t.Logf("ListVoices returned error: %v", err)
	} else {
		t.Logf("ListVoices succeeded, found %d voices", len(voices))
		if len(voices) > 0 {
			t.Logf("Sample voice: %s (%s, %s)", voices[0].Name, voices[0].Locale, voices[0].Gender)
		}
	}

	// 2. Test Speech Synthesis to MP3 file
	dir := t.TempDir()
	mp3Path := filepath.Join(dir, "test_synthesis.mp3")

	args := edgetts.Args{
		Voice: "en-US-ChristopherNeural",
		Rate:  "+0%",
	}
	ttsClient := edgetts.New(args)
	text := "Hello! This is a test synthesis using the kolonist edgetts library."

	speaker := ttsClient.Speak(text)

	err = speaker.SaveToFile(ctx, mp3Path, edgetts.OutputFormatMp3)
	if err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	info, err := os.Stat(mp3Path)
	if err != nil {
		t.Fatalf("Failed to stat generated MP3: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("Generated MP3 file is empty (0 bytes)")
	}
	t.Logf("Successfully generated MP3 file: %s (size: %d bytes)", mp3Path, info.Size())

	// 3. Test Metadata retrieval
	meta, err := speaker.GetMetadata()
	if err != nil {
		t.Logf("GetMetadata returned error: %v", err)
	} else {
		t.Logf("Retrieved %d word boundary metadata entries", len(meta))
		for i, w := range meta {
			if i < 4 || i >= len(meta)-2 {
				t.Logf("  Word %d: %q (offset: %dms, duration: %dms)", i, w.Text, w.Offset, w.Duration)
			} else if i == 4 {
				t.Logf("  ...")
			}
		}
	}

	// 4. Test GetSound (in-memory buffer)
	speakerBuf := ttsClient.Speak("Short audio buffer test.")
	soundBytes, err := speakerBuf.GetSound(ctx, edgetts.OutputFormatMp3)
	if err != nil {
		t.Fatalf("GetSound failed: %v", err)
	}
	if len(soundBytes) == 0 {
		t.Fatalf("GetSound returned empty byte buffer")
	}
	t.Logf("GetSound succeeded, received %d bytes", len(soundBytes))

	// 5. Probe with doAudioProbe
	if apRes, _, probeErr := doAudioProbe("run", []byte(`{"input_path":"`+filepath.ToSlash(mp3Path)+`"}`)); probeErr == nil {
		if apMap, ok := apRes.(map[string]any); ok {
			t.Logf("Audio Probe: duration=%.2fs, format=%v, size=%v bytes, audio details=%#v",
				apMap["duration_seconds"], apMap["format_name"], apMap["size_bytes"], apMap["audio"])
		}
	}
}
