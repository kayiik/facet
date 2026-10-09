package toolbox

import (
	"math"
	"testing"
)

// A scene plan states when it ends, and the rendered video should end there.
//
// Facet never passed duration_seconds, so the composition fell back to
// `lastEnd + 1` — a full second of tail padding — and a 2s request rendered as
// 3s. Measured before the fix: 90 frames at 30fps for a plan ending at 2s.
// After: 60 frames, duration exactly 2.000000.
//
// The padding exists for a final fade that is 8 frames long, so 22 of those 30
// frames served nothing. The composition already honoured duration_seconds
// exactly; the caller's intent was simply never sent.
func TestLastCutEndDrivesTheDuration(t *testing.T) {
	cuts := []map[string]any{
		{"in_seconds": 0.0, "out_seconds": 2.0},
		{"in_seconds": 2.0, "out_seconds": 4.5},
		// Out of order on purpose: the LAST end is the largest, not the final
		// element, and a plan may list scenes in any order.
		{"in_seconds": 1.0, "out_seconds": 3.0},
	}
	if got := lastCutEnd(cuts); got != 4.5 {
		t.Errorf("lastCutEnd = %v, want 4.5", got)
	}
}

// With nothing usable to measure, report 0 so the caller leaves
// duration_seconds unset and the composition keeps its own default. Sending a
// fabricated zero would fail the render outright.
func TestNoUsableEndReportsZero(t *testing.T) {
	for _, cuts := range [][]map[string]any{
		nil,
		{},
		{{"in_seconds": 0.0}},
		{{"out_seconds": "not a number"}},
	} {
		if got := lastCutEnd(cuts); got != 0 {
			t.Errorf("lastCutEnd(%v) = %v, want 0 so the default is kept", cuts, got)
		}
	}
}

// Both input shapes must produce the same duration from the same timings.
//
// The scene path was fixed to state its own end; the cuts path was not, so the
// same tool rendered 2s from a scene plan and 3s from the equivalent cuts plan
// — 60 frames versus 90. A caller switching between the documented shapes got
// a different video for the same intent.
//
// Fixing one input shape and not the other is the same mistake as the earlier
// width/height fix, which honoured dimensions on the direct-props branch and
// silently ignored them for scenes.
func TestCutsAndScenesAgreeOnDuration(t *testing.T) {
	cuts := []map[string]any{
		{"id": "c1", "type": "text_card", "in_seconds": 0.0, "out_seconds": 2.0},
		{"id": "c2", "type": "text_card", "in_seconds": 2.0, "out_seconds": 4.5},
	}
	scenes := []map[string]any{
		{"id": "c1", "type": "text_card", "start_seconds": 0.0, "end_seconds": 2.0},
		{"id": "c2", "type": "text_card", "start_seconds": 2.0, "end_seconds": 4.5},
	}

	// The scene path derives its duration from mapped cuts.
	mapped := make([]map[string]any, 0, len(scenes))
	for _, s := range scenes {
		mapped = append(mapped, mapSceneToCut(s))
	}
	fromScenes := lastCutEnd(mapped)

	// The cuts path derives it from the cuts directly.
	fromCuts := lastCutEnd(cuts)

	if fromCuts != fromScenes {
		t.Errorf("cuts end at %v but the equivalent scenes end at %v; "+
			"the same timings must render the same length", fromCuts, fromScenes)
	}
	if fromCuts != 4.5 {
		t.Errorf("end = %v, want 4.5 — the last cut's out_seconds", fromCuts)
	}
}

// A derived duration must land on a whole frame boundary.
//
// The composition requires duration_seconds * fps to be a whole frame count
// and refuses anything else outright. Cuts matched to narration land on
// arbitrary boundaries — 8.16s of speech at 30fps is 244.8 frames — so sending
// the plan's raw end failed the render entirely:
//
//	Explainer duration_seconds * fps must be a positive safe integer frame count
//
// I introduced this with the padding fix and did not see it, because every
// test I had written used whole-second timings. It surfaced only on running
// the narrated walkthrough end to end, where the timings come from speech.
func TestDerivedDurationLandsOnAFrame(t *testing.T) {
	for _, c := range []struct {
		seconds, fps, want float64
	}{
		// The real case: 8.16s of narration at 30fps.
		{8.16, 30, 245.0 / 30},
		{4.5, 30, 4.5},          // already whole (135 frames)
		{2.0, 30, 2.0},          // already whole
		{1.0 / 3, 24, 8.0 / 24}, // 8 frames exactly
		{3.999, 25, 100.0 / 25}, // rounds up to 100 frames
	} {
		got := frameAlignedDuration(c.seconds, c.fps)
		frames := got * c.fps
		if math.Abs(frames-math.Round(frames)) > 1e-6 {
			t.Errorf("%vs at %vfps gave %v frames, not a whole count", c.seconds, c.fps, frames)
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%vs at %vfps = %v, want %v", c.seconds, c.fps, got, c.want)
		}
		// Rounding UP matters: a frame short clips the end of the video the
		// caller asked for.
		if got < c.seconds-1e-9 {
			t.Errorf("%vs was rounded DOWN to %v, clipping the last moment", c.seconds, got)
		}
	}
}

// Nothing usable in, nothing invented out.
func TestFrameAlignmentLeavesNonsenseAlone(t *testing.T) {
	for _, c := range [][2]float64{{0, 30}, {-1, 30}, {5, 0}, {5, -30}} {
		if got := frameAlignedDuration(c[0], c[1]); got != c[0] {
			t.Errorf("frameAlignedDuration(%v, %v) = %v, want it untouched", c[0], c[1], got)
		}
	}
}
