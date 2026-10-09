package toolbox

import "testing"

// Determinism is a SEMANTIC GUARANTEE, not a performance note.
//
// Operator ruling 5 derives recoverability from it: re-execution is free
// recovery for a deterministic Operation, so durable resume is not required.
// A wrong claim here would tell a runtime that repeating expensive work is
// safe when it is not.
//
// Each declared Operation was measured — run twice, output digests compared —
// before being listed. This test pins the ones whose determinism is structural
// so a later change cannot quietly revoke them.
func TestMeasuredOperationsAreDeclaredDeterministic(t *testing.T) {
	for _, tool := range []string{
		"video_trimmer", "color_grade", "subtitle_gen", "video_compose",
		"media_probe", "audio_probe",
	} {
		if !Deterministic(tool) {
			t.Errorf("%s was measured byte-identical across runs but is not "+
				"declared deterministic", tool)
		}
	}
}

// A networked or chargeable Operation can never be deterministic: remote state
// changes between runs, and generative providers sample. The guard must hold
// even if the table is edited wrongly — that is the point of having both.
func TestNetworkedAndChargeableAreNeverDeterministic(t *testing.T) {
	for _, name := range names {
		if !Deterministic(name) {
			continue
		}
		if executionFor(name).Network {
			t.Errorf("%s is networked but declared deterministic", name)
		}
		if MayCharge(name) {
			t.Errorf("%s may charge but is declared deterministic; generative "+
				"providers sample and cannot be re-run for identical bytes", name)
		}
	}
}

// The declaration must be absent rather than false where nothing was measured.
// Claiming a guarantee nobody verified is exactly how this repo accumulated
// documentation that described behaviour the code never had.
func TestUnmeasuredOperationsAreNotClaimed(t *testing.T) {
	// piper_tts is local and plausibly deterministic, but was never measured.
	// It must not be declared until it is.
	if Deterministic("piper_tts") {
		t.Error("piper_tts is declared deterministic without having been measured")
	}
}

// Both surfaces must publish it, for the same reason may_charge must: a caller
// choosing an Operation reads the listing, one inspecting it reads describe.
func TestDeterminismPublishedInBothSurfaces(t *testing.T) {
	for _, name := range names {
		listed, okL := summary(name)["deterministic"].(bool)
		described, okD := description(name)["deterministic"].(bool)
		if !okL || !okD {
			t.Errorf("%s: deterministic missing from listing=%v describe=%v", name, okL, okD)
			continue
		}
		if listed != described {
			t.Errorf("%s: listing says %v, describe says %v", name, listed, described)
		}
	}
}
