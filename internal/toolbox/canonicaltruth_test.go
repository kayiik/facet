package toolbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE PRODUCT TRUTH, MANY PROJECTIONS.
//
// The release manifest's central invariant: canonical Operation truth is
// declared once and every release shape READS it. A release-mode-specific
// effects, cost, or determinism table is the duplicated-truth defect this
// codebase has already paid for twice -- once in chargeability, once in
// determinism -- and a second release pipeline is exactly where it reappears,
// because the shapes look independent.
//
// This asserts the property structurally rather than trusting review: the
// canonical accessors must have exactly one definition in the tree.
func TestCanonicalTruthHasOneDefinition(t *testing.T) {
	// Each of these answers a product question that must have ONE answer
	// regardless of whether Facet ships standalone, as a module, or as a CLI
	// bundle.
	canonical := []string{
		"func MayCharge(",
		"func Deterministic(",
		"func executionFor(",
		"func resolutionOf(",
	}

	var goFiles []string
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// Vendored and generated trees are not product truth.
			if name := info.Name(); name == "node_modules" || name == ".git" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(goFiles) == 0 {
		// A scan that inspects nothing and a scan that finds nothing produce
		// the same silence.
		t.Fatal("scanned zero Go files; the walk found nothing to check")
	}

	for _, sig := range canonical {
		var found []string
		for _, f := range goFiles {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if strings.Contains(string(b), sig) {
				found = append(found, f)
			}
		}
		switch len(found) {
		case 0:
			t.Errorf("canonical accessor %q has NO definition; product truth is missing", sig)
		case 1:
			// exactly one source of truth
		default:
			t.Errorf("canonical accessor %q is defined in %d places: %v\n"+
				"a second definition is a release-mode-specific truth table, "+
				"which drifts the first time one shape changes", sig, len(found), found)
		}
	}
}

// A projection READS canonical truth; it never restates it.
//
// v2ops.go is the module projection. If it ever grows its own chargeability or
// determinism table rather than calling the canonical accessor, the two layers
// can disagree and only one of them is right.
func TestModuleProjectionDerivesRatherThanRestates(t *testing.T) {
	src, err := os.ReadFile("v2ops.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	// The projection must CALL the canonical accessors.
	for _, call := range []string{"MayCharge(n)", "Deterministic(n)", "executionFor(n)"} {
		if !strings.Contains(body, call) {
			t.Errorf("the v2 projection no longer calls %s; it may be restating "+
				"product truth instead of deriving it", call)
		}
	}

	// And it must not carry its own map of tool -> effect. A literal map from
	// tool names to booleans in the projection layer is the shape of a second
	// truth table.
	for _, banned := range []string{"chargeableTools", "deterministicTools"} {
		// Referencing the canonical map is fine; DEFINING one here is not.
		if strings.Contains(body, "var "+banned) || strings.Contains(body, banned+" = map[") {
			t.Errorf("the v2 projection defines %q; canonical effect truth belongs "+
				"in exactly one place", banned)
		}
	}
}
