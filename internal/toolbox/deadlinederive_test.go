package toolbox

import (
	"testing"
	"time"
)

// The estimate's "will this fit" threshold was once the literal 55: correct
// when the safety margin was 5s (60-5), stale the moment the margin dropped to
// 1s. Nothing failed, because a stale NUMBER stays plausible in a way a stale
// LIST does not — the estimate simply warned about renders that had four spare
// seconds.
//
// This is the same duplicated-constant defect already fixed for chargeability and
// determinism. It survived here because the duplicate was a literal in an
// expression rather than a second list.
func TestMCPLimitThresholdIsDerivedNotHardcoded(t *testing.T) {
	want := ShortestMCPCallLimit - MCPCallMargin
	if FitsShortestMCPCallLimit != want {
		t.Errorf("threshold %v, want %v (limit %v less margin %v)",
			FitsShortestMCPCallLimit, want, ShortestMCPCallLimit, MCPCallMargin)
	}

	// The value the stale literal had. Asserting it is WRONG is what proves the
	// fix moved behaviour: with margin at 1s, 55 reserves 5s and warns about a
	// render that fits.
	if FitsShortestMCPCallLimit == 55*time.Second {
		t.Error("threshold is still 55s, the value from when the margin was 5s")
	}
	if got := FitsShortestMCPCallLimit.Seconds(); got != 59 {
		t.Errorf("threshold %.0fs, want 59s", got)
	}
}

// A render between the old and new threshold is the behavioural difference: it
// was reported as NOT fitting, and it does fit.
func TestRenderInTheStaleGapIsNotSentToTheShell(t *testing.T) {
	// Find a frame count whose pessimistic estimate lands in (55s, 59s].
	var frames int
	for f := 1; f < 4000; f++ {
		max := (renderFixedSeconds + float64(f)*renderSecondsPerFrame) * renderLoadFactor
		if max > 55 && max <= 59 {
			frames = f
			break
		}
	}
	if frames == 0 {
		t.Skip("no frame count lands in the stale gap at this reference size")
	}

	got := estimateRender([]string{"video_compose_remotion_render"}, frames, 1280, 720)
	preferShell, ok := got["prefer_shell"].(bool)
	if !ok {
		t.Fatal("prefer_shell missing or not a bool")
	}
	if preferShell {
		max := got["estimated_duration_seconds_max"]
		t.Errorf("a %d-frame render estimated at %v s max is sent to the shell, "+
			"though it fits the %v limit with room to spare", frames, max, ShortestMCPCallLimit)
	}
}

// The margin must stay small enough to be worth reserving. A margin approaching
// the limit would make the threshold meaningless.
func TestMarginIsASmallFractionOfTheLimit(t *testing.T) {
	if MCPCallMargin >= ShortestMCPCallLimit/10 {
		t.Errorf("margin %v is %.0f%% of the %v limit; too much to reserve for returning a result",
			MCPCallMargin,
			100*MCPCallMargin.Seconds()/ShortestMCPCallLimit.Seconds(),
			ShortestMCPCallLimit)
	}
}

// The estimate speaks in harness-neutral terms. The retired host protocol's
// field and advice must not come back.
func TestEstimateNamesNoRetiredHostProtocol(t *testing.T) {
	got := estimateRender([]string{"video_compose_remotion_render"}, 450, 1280, 720)
	if _, present := got["exceeds_default_host_deadline"]; present {
		t.Error("the estimate still reports exceeds_default_host_deadline")
	}
	for _, msg := range []string{timeoutMessage("node", 7*time.Second), timeoutMessage("node", 0)} {
		for _, retired := range []string{"deadline_ms", "async"} {
			if containsWord(msg, retired) {
				t.Errorf("the timeout message still advises %q: %q", retired, msg)
			}
		}
	}
}

func containsWord(s, word string) bool {
	for i := 0; i+len(word) <= len(s); i++ {
		if s[i:i+len(word)] != word {
			continue
		}
		before := i == 0 || !isWordByte(s[i-1])
		after := i+len(word) == len(s) || !isWordByte(s[i+len(word)])
		if before && after {
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
