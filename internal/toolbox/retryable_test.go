package toolbox

import (
	"strings"
	"testing"
	"time"
)

// ToolError.Retryable was declared and never set anywhere, so EVERY error told
// the host "do not retry" — including a timeout, which is the one failure a
// larger budget reliably fixes. A host honouring the flag would abandon a
// render that needed nothing but more time.
//
// The tenth instance of a field accepted and ignored.
func TestTransientFailuresAreRetryable(t *testing.T) {
	for _, code := range []string{"command_timeout", "provider_response_invalid"} {
		err := failure(code, "boom", nil)
		var tf *toolFailure
		if !asToolFailure(err, &tf) {
			t.Fatalf("failure(%q) is not a toolFailure", code)
		}
		if !tf.err.Retryable {
			t.Errorf("%s is not retryable; a host will not try again even though a "+
				"larger budget or a second call would succeed", code)
		}
	}
}

// Retryable means TRANSIENT, not "worth another go". A malformed request, a
// missing credential, an absent dependency and a nonexistent input all fail
// identically on a retry, and claiming otherwise invites a loop that burns
// time and — for a paid provider — money.
func TestPermanentFailuresAreNotRetryable(t *testing.T) {
	for _, code := range []string{
		"invalid_request", "credentials_missing", "dependency_missing",
		"input_not_found", "unconfigured", "command_failed",
		"output_validation_failed", "invalid_timeline",
	} {
		err := failure(code, "boom", nil)
		var tf *toolFailure
		if !asToolFailure(err, &tf) {
			t.Fatalf("failure(%q) is not a toolFailure", code)
		}
		if tf.err.Retryable {
			t.Errorf("%s is marked retryable; retrying it fails the same way", code)
		}
	}
}

// A timeout message must name the work and the fix. "node was cancelled or
// timed out" named the binary and suggested nothing, so an agent could not
// tell an impossible render from one given four seconds too few.
func TestTimeoutMessageNamesTheBudgetAndTheFix(t *testing.T) {
	got := timeoutMessage("node", 7*time.Second)
	for _, want := range []string{"7s", "timeout_seconds", "facet tools run"} {
		if !strings.Contains(got, want) {
			t.Errorf("the timeout message does not mention %q: %q", want, got)
		}
	}

	// With no known budget it must still say what to change rather than
	// inventing a duration.
	blank := timeoutMessage("node", 0)
	if strings.Contains(blank, "0s") {
		t.Errorf("an unknown budget was reported as a duration: %q", blank)
	}
	if !strings.Contains(blank, "timeout_seconds") {
		t.Errorf("the fallback message suggests no fix: %q", blank)
	}
}

func asToolFailure(err error, out **toolFailure) bool {
	tf, ok := err.(*toolFailure)
	if ok {
		*out = tf
	}
	return ok
}
