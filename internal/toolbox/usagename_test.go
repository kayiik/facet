package toolbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A usage string names the binary the reader must type. If it names a binary
// that does not exist, the message is worse than absent: it is confident and
// wrong, and the reader follows it.
//
// Three usage strings named the donor binary instead of Facet. They survived
// because nothing ever dereferences a usage string -- the same
// reason a stale $id and a dead provider name survived here. This one had a
// deadline attached: Release C bundles instruct an agentic CLI on how to invoke
// Facet, so a stale name would have shipped into every target bundle telling
// the agent to run a command that fails.
func TestUserFacingUsageNamesTheRealBinary(t *testing.T) {
	donorBinary := "video" + "kit"
	dead := []string{donorBinary + " tools", donorBinary + " module", "usage: " + donorBinary}

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
		t.Fatal("scanned zero Go files")
	}

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		body := string(b)
		for _, d := range dead {
			if strings.Contains(body, d) {
				t.Errorf("%s contains %q -- a usage string naming a binary this "+
					"product does not ship. Release C bundles tell an agent how to "+
					"invoke Facet; a stale name here ships a failing command.", f, d)
			}
		}
	}
}
