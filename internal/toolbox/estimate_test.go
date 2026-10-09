package toolbox

import "testing"

// An estimate said nothing about TIME. It reported cost — which is zero for a
// local render and therefore says nothing — while the decision a caller
// actually faces is whether the render fits an MCP client's call limit or
// should run from a shell.
//
// A 15-second explainer takes ~43 seconds to render and, under load, does NOT
// fit the shortest default MCP call limit (60 seconds). Nothing in the
// estimate revealed that.
func TestRenderEstimateReportsDuration(t *testing.T) {
	// 450 frames at 1280x720: measured 42.7s idle.
	got := estimateRender([]string{"video_compose_remotion_render"}, 450, 1280, 720)

	secs, ok := got["estimated_duration_seconds"].(float64)
	if !ok {
		t.Fatal("an estimate for a render reports no duration")
	}
	if secs < 35 || secs > 50 {
		t.Errorf("predicted %vs for a render measured at 42.7s", secs)
	}

	// The pessimistic bound must cover the contended case: the same render
	// took 170.9s while other work was running.
	max, ok := got["estimated_duration_seconds_max"].(float64)
	if !ok {
		t.Fatal("no pessimistic bound; a caller cannot choose a safe deadline")
	}
	if max < 170 {
		t.Errorf("max %vs does not cover the 170.9s measured under load", max)
	}

	if preferShell, _ := got["prefer_shell"].(bool); !preferShell {
		t.Error("a 43s render (171s under load) was not sent to the shell route")
	}
}

// A short render must NOT be predicted as instant. A per-frame-only model
// said 1.4s for a render that took 16.5s, because at small sizes the fixed
// bundling and browser startup dominates entirely.
func TestShortRenderIsNotPredictedInstant(t *testing.T) {
	// 60 frames at 640x360: measured 16.5s through the module.
	got := estimateRender([]string{"video_compose_remotion_render"}, 60, 640, 360)
	secs, _ := got["estimated_duration_seconds"].(float64)
	if secs < renderFixedSeconds {
		t.Errorf("predicted %vs, below the %vs of fixed setup every render pays",
			secs, renderFixedSeconds)
	}
	max, _ := got["estimated_duration_seconds_max"].(float64)
	if max < 16.5 {
		t.Errorf("max %vs does not cover the 16.5s actually measured", max)
	}
	// It fits the shortest MCP call limit, so it must stay an MCP call.
	if preferShell, _ := got["prefer_shell"].(bool); preferShell {
		t.Error("a render that fits the shortest MCP call limit was sent to the shell route")
	}
}

// Cost is separate from duration and must stay honest: a local render is free,
// and a duration estimate must not be mistaken for a price.
func TestDurationEstimateDoesNotDisturbCost(t *testing.T) {
	got := estimateRender([]string{"video_compose_remotion_render"}, 450, 1280, 720)
	if c, _ := got["estimated_cost"].(float64); c != 0 {
		t.Errorf("estimated_cost = %v for a local render", c)
	}
	if sfe, _ := got["side_effect_free"].(bool); !sfe {
		t.Error("an estimate reported side effects")
	}
}

// A request that names no usable shape gets no fabricated duration.
func TestUnknownShapeReportsNoDuration(t *testing.T) {
	for _, c := range [][3]int{{0, 1280, 720}, {450, 0, 720}, {450, 1280, 0}} {
		got := estimateRender([]string{"x"}, c[0], c[1], c[2])
		if _, present := got["estimated_duration_seconds"]; present {
			t.Errorf("a duration was invented for shape %v", c)
		}
	}
}
