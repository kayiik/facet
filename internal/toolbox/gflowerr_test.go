package toolbox

import (
	"strings"
	"testing"
)

// A provider failure must say what the PROVIDER said.
//
// gflow's stderr was discarded entirely because it may carry credentials, so
// every failure read identically: "gflow CLI failed; check provider
// authentication and availability". True, unactionable, and the same sentence
// a quota exhaustion, a network outage and an expired token would produce.
//
// Verified against the real CLI, which reported:
//
//	Error: generation failed (500): generate images error: CAPTCHA_FAILED:
//	Cannot access contents of the page...
//
// An operator reading the generic message would check their API key. The
// actual cause was a browser-extension permission in gflow's own auth.
func TestProviderErrorLineIsReported(t *testing.T) {
	got := providerErrorLine(
		"some progress noise\nError: generation failed (500): CAPTCHA_FAILED: cannot access page\n")
	if !strings.Contains(got, "CAPTCHA_FAILED") {
		t.Errorf("the provider's own reason was not extracted: %q", got)
	}
}

// The reason stderr was discarded stands: a message is not worth a leaked
// credential. Anything token-shaped is dropped rather than relayed.
func TestSecretsAreNeverRelayed(t *testing.T) {
	for _, line := range []string{
		"Error: request failed with api_key=sk-abcdef0123456789",
		"Error: authorization header rejected",
		"Error: signed url https://x/y?X-Goog-Signature=deadbeef expired",
		"Error: bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9 rejected",
		"Error: token=AKIAIOSFODNN7EXAMPLE is not valid",
		// A long unbroken base64-ish run is a token, not prose.
		"Error: " + strings.Repeat("aB3", 20),
	} {
		if got := providerErrorLine(line + "\n"); got != "" {
			t.Errorf("a line that may carry a secret was relayed: %q -> %q", line, got)
		}
	}
}

// Ordinary prose must survive, or the filter costs the message it was added to
// deliver.
func TestOrdinaryProseSurvivesTheFilter(t *testing.T) {
	for _, line := range []string{
		"Error: generation failed (500): CAPTCHA_FAILED: cannot access page",
		"Error: quota exceeded for this project",
		"Error: model narwhal is not available in your region",
	} {
		if got := providerErrorLine(line + "\n"); got == "" {
			t.Errorf("a safe message was dropped: %q", line)
		}
	}
}

// Nothing to report is reported as nothing rather than as noise.
func TestNoErrorLineReportsNothing(t *testing.T) {
	for _, stderr := range []string{"", "downloading...\n", "warning: slow network\n"} {
		if got := providerErrorLine(stderr); got != "" {
			t.Errorf("non-error output was reported as a failure reason: %q", got)
		}
	}
}

// A chatty CLI must not be able to exhaust memory through stderr.
func TestStderrCaptureIsBounded(t *testing.T) {
	var b boundedBuffer
	chunk := strings.Repeat("x", 4096)
	for i := 0; i < 100; i++ {
		n, err := b.Write([]byte(chunk))
		if err != nil {
			t.Fatal(err)
		}
		// The writer must always be told its whole write succeeded, or it
		// retries and the command stalls.
		if n != len(chunk) {
			t.Fatalf("short write reported: %d of %d", n, len(chunk))
		}
	}
	if len(b.String()) > providerStderrLimit {
		t.Errorf("captured %d bytes, past the %d limit", len(b.String()), providerStderrLimit)
	}
}
