package toolbox

import (
	"strings"
	"testing"
)

// execution.provider attributes work to a service, binary, or the local
// machine. Dead product identities must not survive as provider names.
func TestNoToolNamesADeadProject(t *testing.T) {
	// Names that referred to something real once and no longer do. A provider
	// that names nothing a host can act on is worse than "local", because it
	// looks like an attribution.
	dead := []string{"open" + "montage"}
	for _, tool := range Names() {
		p := providerOf(tool)
		for _, d := range dead {
			if strings.EqualFold(p, d) {
				t.Errorf("tool %q declares provider %q, a project name that no longer refers to anything", tool, d)
			}
		}
	}
}

// A local tool must not claim an external provider, and a networked tool must
// not claim to be local. This is the property that would have caught the
// original defect: subtitle_gen was network=false with a provider that read as
// a service.
func TestProviderAgreesWithNetwork(t *testing.T) {
	for _, tool := range Names() {
		p := providerOf(tool)
		net := networkOf(tool)
		if p == "" {
			t.Errorf("tool %q declares no provider at all", tool)
			continue
		}
		if !net && p != "local" && !isLocalBinary(p) {
			t.Errorf("tool %q does no network access but names provider %q, "+
				"which reads as an external service", tool, p)
		}
		if net && p == "local" {
			t.Errorf("tool %q performs network access but claims provider \"local\"", tool)
		}
	}
}

// isLocalBinary lists providers that name a program on this machine rather than
// a remote service. These are honest for a non-networked tool.
func isLocalBinary(p string) bool {
	switch p {
	// Each of these is a program invoked on THIS machine. piper was added
	// after this test first ran red on it: piper_tts shells out to a piper
	// binary on PATH (tts.go:331) and reaches no service, so the provider was
	// honest and the allowlist was incomplete. Recorded because the test
	// finding a TRUE NEGATIVE on its first run is evidence it inspects the
	// real declaration rather than a list written to match it.
	case "ffmpeg", "ffprobe", "remotion", "hyperframes", "facet",
		"selector", "gflow", "whisper", "piper":
		return true
	}
	return false
}

func providerOf(tool string) string { return executionFor(tool).Provider }
func networkOf(tool string) bool    { return executionFor(tool).Network }
