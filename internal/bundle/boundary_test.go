package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kayiik.module/v2 IS RELEASE B'S HOST BOUNDARY, NOT A PRODUCT-WIDE CONTRACT.
//
// The distinction is not "structured schemas versus prose" -- `facet tools
// describe` already returns request_schema, result_schema, may_charge and
// cost.known over plain CLI, so describe/estimate/consent/run/verify is Release
// C's loop too. That discipline is a product property carried by every
// projection.
//
// What v2 buys is that a PROGRAM, not an agent, must decide whether an
// invocation may proceed BEFORE Facet is asked: approval routing, grants, and
// no-weakening conformance all run without executing the tool. A host cannot
// read help text to do that; an agent can.
//
// So the CLI-bundle projection must not acquire a v2 dependency. If it ever
// does, either the boundary has moved or someone has confused a product
// property with a contract one -- and the manifest's claim that A and C contain
// zero references becomes stale prose rather than a fact.
func TestBundleProjectionDoesNotDependOnModuleV2(t *testing.T) {
	banned := []string{
		"kayiik.module/v2",
		"modproto",
		"ContractVersion",
	}

	var files []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A scan that inspects nothing and a scan that finds nothing are the same
	// silence.
	if len(files) == 0 {
		t.Fatal("scanned zero Go files in the bundle package")
	}

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		body := stripComments(string(raw))
		for _, b := range banned {
			if strings.Contains(body, b) {
				t.Errorf("%s references %q -- the CLI-bundle projection must not depend on "+
					"Release B's host contract. Per-call schemas already come from "+
					"`facet tools describe`, which needs no v2.", f, b)
			}
		}
	}
}

// stripComments removes // and /* */ so the scan inspects CODE.
//
// The prose above deliberately names the contract it forbids; a scan that
// cannot tell documentation from a dependency would punish the comment that
// makes the rule legible.
func stripComments(src string) string {
	var out strings.Builder
	inLine, inBlock := false, false
	for i := 0; i < len(src); i++ {
		if inLine {
			if src[i] == '\n' {
				inLine = false
				out.WriteByte(src[i])
			}
			continue
		}
		if inBlock {
			if i+1 < len(src) && src[i] == '*' && src[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			inLine = true
			i++
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			inBlock = true
			i++
			continue
		}
		out.WriteByte(src[i])
	}
	return out.String()
}
