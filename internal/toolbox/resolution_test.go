package toolbox

import "testing"

// Resolution is not Boolean (operator ruling 7). The existence of a key,
// binary or process is not proof a provider is operational.
//
// Measured before this existed, and the reason the third state is needed:
//
//	elevenlabs_tts  key present, `configured` true -> HTTP 402 at run
//	gflow_image     binary present, status HEALTHY -> CAPTCHA_FAILED at run
//
// A Boolean check called both SATISFIED. Both fail at the provider.
func TestCredentialPresenceIsUnknownNotSatisfied(t *testing.T) {
	dep := envDependency("PATH") // certain to be present
	if got := dep["resolution"]; got != ResolutionUnknown {
		t.Errorf("a present credential resolved %v; holding a string is not "+
			"holding a working account", got)
	}
	if avail, _ := dep["available"].(bool); !avail {
		t.Error("a present env var reported unavailable")
	}
}

// An absent requirement is decisive: it can be refused locally with a remedy
// the caller can act on, which is strictly better than a provider error.
func TestAbsentRequirementIsUnsatisfied(t *testing.T) {
	dep := envDependency("FACET_DEFINITELY_NOT_SET_ANYWHERE")
	if got := dep["resolution"]; got != ResolutionUnsatisfied {
		t.Errorf("an absent credential resolved %v, want unsatisfied", got)
	}
}

// A binary or runtime this process can inspect is decidable, so it never
// resolves UNKNOWN — that state is for things only the provider can confirm.
func TestInspectableRequirementsAreDecided(t *testing.T) {
	for _, kind := range []string{"binary", "runtime"} {
		if got := resolutionOf(true, kind); got != ResolutionSatisfied {
			t.Errorf("present %s resolved %v, want satisfied", kind, got)
		}
		if got := resolutionOf(false, kind); got != ResolutionUnsatisfied {
			t.Errorf("absent %s resolved %v, want unsatisfied", kind, got)
		}
	}
}

// Every declared requirement must carry a resolution. A dependency without one
// forces a caller back to the Boolean it replaced — which is how the composer
// dependency was missed when the other two constructors were updated.
func TestEveryRequirementCarriesAResolution(t *testing.T) {
	for _, name := range names {
		deps, _ := summary(name)["dependencies"].([]any)
		for _, d := range deps {
			m, ok := d.(map[string]any)
			if !ok {
				continue
			}
			res, present := m["resolution"]
			if !present || res == nil {
				t.Errorf("%s: dependency %v carries no resolution", name, m["name"])
				continue
			}
			switch res {
			case ResolutionSatisfied, ResolutionUnsatisfied, ResolutionUnknown:
			default:
				t.Errorf("%s: dependency %v has unknown resolution state %v",
					name, m["name"], res)
			}
		}
	}
}
