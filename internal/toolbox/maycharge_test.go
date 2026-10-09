package toolbox

import (
	"encoding/json"
	"testing"
)

// Chargeability is ONE fact, declared at the Operation layer.
//
// It lived in two places: executionFor's nil-cost set here, and paidTools in
// a retired module layer. Both enumerated the same eight tools by hand, in two
// layers, and any projection of the Operation must not hold a second opinion
// about a property of the Operation.
//
// Chargeability is a per-Operation semantic effect, independent of cost
// knowledge: harness approval rules (facet wire) are derived from it.
func TestChargeabilityHasOneSource(t *testing.T) {
	// Anchored to NAMED providers, not to the set itself. A test that iterates
	// the same list it validates cannot notice the list shrinking — verified:
	// removing elevenlabs_tts from the source passed every derived assertion.
	//
	// These eight reach a billing provider. That is a fact about the world,
	// not about this file, so it is the right thing to pin.
	for _, tool := range []string{
		"gflow_video", "gflow_image", "openai_image", "flux_image",
		"kling_video", "sora_video", "openai_tts", "elevenlabs_tts",
	} {
		if !MayCharge(tool) {
			t.Errorf("%s reaches a billing provider but is not declared chargeable; "+
				"it would run without human consent and spend the operator's money", tool)
		}
	}

	// And nothing local may be declared chargeable: a false positive gates a
	// free Operation forever behind an approval it can never need.
	for _, tool := range []string{
		"media_probe", "ffprobe_local", "edge_tts", "subtitle_gen",
		"video_trimmer", "frame_sample", "output_review",
	} {
		if MayCharge(tool) {
			t.Errorf("%s is local or free but is declared chargeable", tool)
		}
	}

	for _, tool := range ChargeableTools() {
		if !MayCharge(tool) {
			t.Errorf("%s is listed chargeable but MayCharge says otherwise", tool)
		}
		// A chargeable Operation's amount is not knowable in advance, so
		// executionFor must report null rather than zero. Zero would be a
		// claim about spend nobody can make yet.
		exec := executionFor(tool)
		if exec.EstimatedCost != nil {
			t.Errorf("%s may charge but reports a known estimated cost %v",
				tool, *exec.EstimatedCost)
		}
	}
}

// may_charge and cost_known answer DIFFERENT questions, and both combinations
// ruling 4 calls legal must actually occur — otherwise the two fields are
// redundant in practice whatever the contract says.
func TestMayChargeIsIndependentOfCostKnown(t *testing.T) {
	var chargeableUnknown, freeKnown, freeNetworked int
	for _, name := range names {
		entry := summary(name)
		mayCharge, _ := entry["may_charge"].(bool)
		cost, _ := entry["cost"].(map[string]any)
		known, _ := cost["known"].(bool)
		networked, _ := entry["network"].(bool)

		switch {
		case mayCharge && !known:
			chargeableUnknown++
		case !mayCharge && known:
			freeKnown++
		}
		if !mayCharge && networked {
			freeNetworked++
		}
	}
	if chargeableUnknown == 0 {
		t.Error("no Operation is chargeable-with-unknown-amount; ruling 4 says this is the common case")
	}
	if freeKnown == 0 {
		t.Error("no Operation is free-with-known-amount")
	}
	// The case cost_known could NEVER express: reaches the network, bills
	// nothing. edge_tts proved the old gate wrong by being exactly this.
	if freeNetworked == 0 {
		t.Error("no networked Operation is free; that combination is why " +
			"chargeability cannot be inferred from cost or from network use")
	}
}

// Every configured Operation must publish may_charge in BOTH surfaces. An
// agent choosing a tool reads the listing; one inspecting a tool reads
// describe. A fact present in only one is a fact the other caller must guess.
func TestMayChargePublishedInBothSurfaces(t *testing.T) {
	for _, name := range names {
		listed, okL := summary(name)["may_charge"].(bool)
		described, okD := description(name)["may_charge"].(bool)
		if !okL {
			t.Errorf("%s: listing does not publish may_charge", name)
		}
		if !okD {
			t.Errorf("%s: describe does not publish may_charge", name)
		}
		if listed != described {
			l, _ := json.Marshal(listed)
			d, _ := json.Marshal(described)
			t.Errorf("%s: listing says %s, describe says %s", name, l, d)
		}
	}
}
