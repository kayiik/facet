package toolbox

import (
	"strings"
	"testing"
)

// A wrong-type rejection must name the FIELD and the type it wants.
//
// It said only "a field has the wrong type" followed by Go's raw error:
//
//	json: cannot unmarshal object into Go struct field
//	stitchRequest.clips of type string
//
// Both facts a caller needs — "clips" and "string" — were there, wrapped in
// internal struct naming that reads as implementation noise. Verified across
// five tools whose requests I got wrong while sweeping the tool surface: every
// one reported the same unhelpful sentence.
func TestWrongTypeNamesTheFieldAndType(t *testing.T) {
	got := wrongTypeMessage(
		"json: cannot unmarshal object into Go struct field stitchRequest.clips of type string")

	for _, want := range []string{`"clips"`, "string", "object"} {
		if !strings.Contains(got, want) {
			t.Errorf("the message does not mention %s: %q", want, got)
		}
	}
	// The internal struct name is noise a caller cannot act on.
	if strings.Contains(got, "stitchRequest") {
		t.Errorf("an internal struct name leaked to the caller: %q", got)
	}
}

// An internal Go type name tells a caller nothing. "toolbox.stockQueryItem"
// becomes "an object", which at least says what shape to send.
func TestInternalTypeNamesBecomeShapes(t *testing.T) {
	for in, want := range map[string]string{
		"toolbox.stockQueryItem": "an object",
		"toolbox.target":         "an object",
		"map[string]any":         "an object",
		"[]string":               "an array of string",
		"string":                 "string",
		"bool":                   "bool",
		"float64":                "number",
		"int":                    "number",
	} {
		if got := describeType(in); got != want {
			t.Errorf("describeType(%q) = %q, want %q", in, got, want)
		}
	}
}

// An unfamiliar error shape must not lose information: fall back to the raw
// text rather than discarding what cannot be parsed.
func TestUnparseableTypeErrorKeepsTheDetail(t *testing.T) {
	raw := "json: something entirely different"
	got := wrongTypeMessage(raw)
	if !strings.Contains(got, "something entirely different") {
		t.Errorf("detail was discarded: %q", got)
	}
}
