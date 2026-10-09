package toolbox

import (
	"encoding/json"
	"testing"
)

// The tool LISTING must carry cost, because choosing a tool is when the cost
// decision is made.
//
// The agent overlay tells an agent "unknown cost is not free" and to obtain
// human consent before paid generation. The listing omitted cost entirely, so
// telling a free tool from a billing one meant 35 separate describe calls, or
// guessing — and guessing wrong means spending someone's money without asking.
//
// Verified: `tools list` reported no cost field at all while `tools describe
// media_probe` reported amount 0, known true. Two surfaces, same tool,
// different answers.
func TestListingCarriesCostAndEffects(t *testing.T) {
	entry := summary("media_probe")

	cost, ok := entry["cost"].(map[string]any)
	if !ok {
		t.Fatal("a listed tool carries no cost; an agent cannot tell free from paid")
	}
	if known, _ := cost["known"].(bool); !known {
		t.Error("media_probe runs ffprobe locally and its cost is known")
	}
	for _, field := range []string{"network", "external_write"} {
		if _, present := entry[field]; !present {
			t.Errorf("a listed tool does not report %q", field)
		}
	}
}

// A tool whose cost genuinely is not knowable must say so rather than
// reporting zero. Collapsing unknown into free is how spend gets hidden.
func TestUnknownCostIsNotReportedAsFree(t *testing.T) {
	for _, name := range []string{"elevenlabs_tts", "gflow_video", "gflow_image"} {
		cost, ok := summary(name)["cost"].(map[string]any)
		if !ok {
			t.Fatalf("%s carries no cost", name)
		}
		if known, _ := cost["known"].(bool); known {
			t.Errorf("%s claims a known cost; a paid provider's price is not known in advance", name)
		}
		// null is the honest answer for a price that is not knowable; a
		// number here would be a claim about spend nobody can make yet.
		//
		// Checked as SERIALISED JSON rather than against Go nil: the value is
		// a typed *float64, which is not untyped nil even when the pointer is,
		// and what a host receives is the JSON.
		raw, err := json.Marshal(cost["amount"])
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "null" {
			t.Errorf("%s serialises amount as %s for an unknown cost; null is the honest answer", name, raw)
		}
	}
}

// The listing and describe must never disagree. They are the same facts, and
// an agent that reads one and acts on the other needs them identical.
func TestListingAndDescribeAgree(t *testing.T) {
	for _, name := range []string{"media_probe", "edge_tts", "video_compose", "elevenlabs_tts"} {
		listed := summary(name)
		described := description(name)
		for _, field := range []string{"cost", "network", "external_write"} {
			l, _ := json.Marshal(listed[field])
			d, _ := json.Marshal(described[field])
			if string(l) != string(d) {
				t.Errorf("%s.%s: listing says %s, describe says %s", name, field, l, d)
			}
		}
	}
}
