package toolbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Facet supplies stateless media operations, production guidance, operation
// requirements, effects, and artifact verification to a reasoning runtime.
// Conversation, planning, sequencing, permissions, and session state remain
// the runtime's responsibility.
func TestFacetDoesNotImplementReasoningRuntime(t *testing.T) {
	banned := []struct {
		token  string
		reason string
	}{
		{"ExecutePipeline", "sequenced execution belongs to the reasoning runtime"},
		{"ExecuteWorkflow", "sequenced execution belongs to the reasoning runtime"},
		{"RunWorkflow", "sequenced execution belongs to the reasoning runtime"},
		{"planSteps", "planning belongs to the reasoning runtime"},
	}

	var files []string
	// Walk from the REPOSITORY ROOT, not this package. Scoping to "." made
	// the scan inspect only internal/toolbox while its name claimed to cover
	// Facet -- and a mutant planted in another package went undetected.
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
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
		body := stripGoComments(string(b))
		for _, bad := range banned {
			if strings.Contains(body, bad.token) {
				t.Errorf("%s contains %q: %s.\nFacet extends a reasoning runtime; it does not become one.",
					f, bad.token, bad.reason)
			}
		}
	}
}

// Provider requirements mean providers FACET'S TOOLS need -- image, video,
// voice, stock media, rendering runtimes. They never mean the model powering
// the conversation.
//
// That distinction has caused real confusion, and collapsing it would make
// Facet configure or proxy the driver's model, which is not Facet's to touch.
//
// Asserted against the canonical requirement declarations rather than prose: no
// tool may declare a requirement that names a conversation model provider.
func TestRequirementsNameToolProvidersNotConversationModels(t *testing.T) {
	// Providers that would only ever power a CONVERSATION, never a media tool.
	conversational := []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_API_KEY",
		"COPILOT_TOKEN",
		"OPENROUTER_API_KEY",
	}

	var checked int
	for _, tool := range Names() {
		for _, r := range v2RequirementsFor(tool) {
			checked++
			for _, c := range conversational {
				if strings.EqualFold(r.Name, c) {
					t.Errorf("tool %q requires %q -- that is a conversation model credential, "+
						"which belongs to the reasoning driver. Facet declares providers its "+
						"TOOLS need, never the model powering the conversation.", tool, r.Name)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no requirements were examined; the check had no input")
	}
}

// stripGoComments removes // and /* */ comments so a scan inspects CODE.
//
// Deliberately simple: it does not track string literals, so a token inside a
// quoted string still counts -- which is correct here, since a path in a string
// literal IS code referencing that path.
func stripGoComments(src string) string {
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
