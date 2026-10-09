package toolbox

import "testing"

// Chargeability must have exactly ONE source, and this tests the PROPERTY
// behaviourally rather than by scanning source.
//
// Every other test here asserts that eight named tools are chargeable. All of
// them pass if someone re-introduces a second hardcoded list with identical
// values — the duplication a single source exists to prevent, and the state
// that existed before it was collapsed. Verified: that mutant passed the whole
// suite.
//
// A source scan cannot do this reliably. I tried: "gflow_video" appears nine
// times in this package legitimately (the tool list, the provider switch, the
// alias switch, the dependency switch, its schema, its description, the
// dispatcher), and a proximity window flagged the dependency switch and the
// schema map as chargeability sets. Text cannot tell a set from a coincidence.
//
// AND THE BEHAVIOURAL APPROACH DOES NOT CATCH IT EITHER. Verified: a mutant
// making describe answer from its own identical list passes this test. Two
// sources with the same values are observationally identical on every input,
// including unknown names — they diverge only once one of them is EDITED, and
// a test cannot observe an edit that has not happened.
//
// So "exactly one source" is a property of the SOURCE TEXT, not of behaviour,
// and it is not mechanically checkable by either route I tried. Recording that
// honestly rather than shipping a test that appears to guard it: a test whose
// passing means less than its name implies is the defect this whole phase kept
// finding.
//
// What this test DOES prove is narrower and still worth having: every
// published surface agrees with MayCharge for every tool, including names no
// enumeration contains, and the cost projection agrees with chargeability. A
// duplicate that has already DRIFTED is caught here. A duplicate that has not
// drifted yet is not, and no test in this package catches it.
func TestEveryPublishedChargeabilityAnswerAgrees(t *testing.T) {
	// A name no enumeration can contain. If any surface answered from its own
	// list rather than from MayCharge, it would have to guess here — and a
	// list's answer for an unknown name is whatever its default is, which is
	// exactly how two sources drift apart on a newly added tool.
	const unknown = "a_tool_no_list_can_contain"

	if MayCharge(unknown) {
		t.Errorf("%s is not a real tool but is reported chargeable", unknown)
	}

	// The listing and describe must agree with MayCharge for EVERY tool,
	// including ones absent from any set. Agreement on the eight known names
	// proves nothing — two identical lists agree too.
	for _, name := range append(append([]string{}, names...), unknown) {
		want := MayCharge(name)
		listed, ok := summary(name)["may_charge"].(bool)
		if !ok {
			t.Errorf("%s: listing publishes no may_charge", name)
			continue
		}
		if listed != want {
			t.Errorf("%s: listing says %v, MayCharge says %v — a second source has drifted",
				name, listed, want)
		}
		described, ok := description(name)["may_charge"].(bool)
		if !ok {
			t.Errorf("%s: describe publishes no may_charge", name)
			continue
		}
		if described != want {
			t.Errorf("%s: describe says %v, MayCharge says %v — a second source has drifted",
				name, described, want)
		}
	}

	// And the cost projection must agree: a chargeable Operation has no known
	// amount. If executionFor held its own opinion, this is where it shows.
	for _, name := range names {
		exec := executionFor(name)
		if MayCharge(name) && exec.EstimatedCost != nil {
			t.Errorf("%s may charge but executionFor reports a known cost — "+
				"two sources disagree", name)
		}
		if !MayCharge(name) && exec.EstimatedCost == nil {
			t.Errorf("%s cannot charge but executionFor reports an unknown cost — "+
				"two sources disagree", name)
		}
	}
}
