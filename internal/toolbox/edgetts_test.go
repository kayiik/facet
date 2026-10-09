package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kolonist/edgetts"
)

// The synthesis library cannot apply a pitch, so a pitch must be refused as
// an unknown field rather than accepted and silently ignored.
func TestEdgeTTSRejectsPitch(t *testing.T) {
	properties := schemas["edge_tts"].(map[string]any)["properties"].(map[string]any)
	if _, advertised := properties["pitch"]; advertised {
		t.Fatal("edge_tts still advertises pitch, which the synthesis library cannot apply")
	}
	for _, op := range []string{"estimate", "run"} {
		_, _, err := doEdgeTTS(op, []byte(`{"text":"hello","pitch":"+10Hz"}`))
		var tf *toolFailure
		if !asToolFailure(err, &tf) || tf.err.Code != "invalid_request" || !strings.Contains(tf.err.Message, "pitch") {
			t.Fatalf("%s accepted pitch: %v", op, err)
		}
	}
	if err := ValidateRequestShape("edge_tts", []byte(`{"text":"hello","pitch":"+10Hz"}`)); err == nil {
		t.Fatal("the request schema accepts pitch")
	}
}

// duration_seconds is measured from the written file. It used to be read from
// a key media_probe never produces, so it was never reported at all.
func TestEdgeTTSReportsMeasuredDuration(t *testing.T) {
	requireFFmpeg(t)
	fakeEdgeSynthesis(t)
	output := filepath.Join(t.TempDir(), "voice.wav")
	request, _ := json.Marshal(map[string]any{"text": "Hello there", "output_path": output})
	result, _, err := doEdgeTTSContext(context.Background(), "run", request)
	if err != nil {
		t.Fatal(err)
	}
	duration, ok := result.(map[string]any)["duration_seconds"].(float64)
	if !ok || math.Abs(duration-0.5) > 0.05 {
		t.Fatalf("duration_seconds = %#v, want about 0.5 measured from the file", result.(map[string]any)["duration_seconds"])
	}
	assertResultConforms(t, "edge_tts", result)
}

func TestEdgeTTSValidatesAdjustmentsBeforeCalling(t *testing.T) {
	called := false
	previous := edgeSynthesize
	edgeSynthesize = func(context.Context, string, string, string, string, time.Duration) ([]byte, []edgetts.SpeechMetadata, error) {
		called = true
		return nil, nil, errors.New("must not be called")
	}
	t.Cleanup(func() { edgeSynthesize = previous })
	for _, body := range []string{`{"text":"hi","rate":"fast"}`, `{"text":"hi","volume":"10"}`} {
		for _, op := range []string{"estimate", "run"} {
			if _, _, err := doEdgeTTS(op, []byte(body)); err == nil {
				t.Fatalf("%s accepted %s", op, body)
			}
		}
	}
	if called {
		t.Fatal("an invalid request reached the synthesis service")
	}
}

// A synthesis that outlives its budget is a timeout, not a provider failure,
// and the caller gets control back when the budget ends.
func TestEdgeTTSTimeoutIsReported(t *testing.T) {
	previous := edgeSynthesize
	edgeSynthesize = func(ctx context.Context, _, _, _, _ string, timeout time.Duration) ([]byte, []edgetts.SpeechMetadata, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	t.Cleanup(func() { edgeSynthesize = previous })
	output := filepath.Join(t.TempDir(), "voice.mp3")
	request, _ := json.Marshal(map[string]any{"text": "hi", "output_path": output, "timeout_seconds": 1})
	start := time.Now()
	_, _, err := doEdgeTTSContext(context.Background(), "run", request)
	var tf *toolFailure
	if !asToolFailure(err, &tf) || tf.err.Code != "command_timeout" || !tf.err.Retryable {
		t.Fatalf("timeout reported as %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the timeout did not return promptly")
	}
}
