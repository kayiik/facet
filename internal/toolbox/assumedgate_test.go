package toolbox

import "testing"

// A gate whose expectation was taken FROM THE FILE compares the file against
// itself and cannot fail. Reporting that as "pass" made a review look far
// stronger than it was.
//
// This is how a real defect survived: a 2s request rendered 3s and the
// duration gate passed, because Expected had been defaulted to the measured
// 3s. Four gates default this way — profile, duration, video_codec,
// pixel_format — so "6/6 pass" on a bare review actually meant two gates
// verified something and four agreed with themselves.
//
// The defaults are not wrong; without a stated expectation there is nothing
// else to compare against. Reporting them as verification was.
func TestAssumedMarksASelfComparingGate(t *testing.T) {
	got := assumed(gate("duration", true, ""))
	if got["status"] != "assumed" {
		t.Errorf("status = %v, want assumed", got["status"])
	}
	msg, _ := got["message"].(string)
	if msg == "" {
		t.Error("an assumed gate carries no explanation of what it did not check")
	}
}

// A gate that genuinely failed must stay failed. Marking a failure as assumed
// would hide a real defect behind a softer word — the opposite of the point.
func TestAssumedNeverSoftensAFailure(t *testing.T) {
	got := assumed(gate("duration", false, "duration outside tolerance"))
	if got["status"] != "fail" {
		t.Errorf("a failing gate became %v; a failure is never an assumption", got["status"])
	}
	if got["message"] != "duration outside tolerance" {
		t.Errorf("the failure reason was replaced: %v", got["message"])
	}
}
