package journeys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withDecisionKey sets a decision key for one test.
func withDecisionKey(t *testing.T, key string) {
	t.Helper()
	SetDecisionKey([]byte(key))
	t.Cleanup(func() { SetDecisionKey(nil) })
}

// The agent can write files in a workspace project, so only a record this
// App sealed is reported as the person's decision.
func TestOnlySealedDecisionsAreThePersons(t *testing.T) {
	withDecisionKey(t, "0123456789abcdef0123456789abcdef")
	root := t.TempDir()
	project := filepath.Join(root, "projects", "demo")
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "rendered video")
	opts := workspaceOptions(root)

	record, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "reject", Note: "The ending is cut."})
	if err != nil {
		t.Fatal(err)
	}
	if record.Seal == "" {
		t.Fatal("a decision recorded with a key is not sealed")
	}
	details, err := GetProjectDetails(opts, "demo")
	if err != nil || details.Decision == nil || !details.Decision.Sealed || !details.Decision.Current {
		t.Fatalf("the recorded decision = %#v, %v", details.Decision, err)
	}

	// The agent turns the rejection into an acceptance, keeping the seal.
	path := filepath.Join(project, "review", "decision.json")
	forged := strings.Replace(mustReadTestFile(t, path), `"decision": "rejected"`, `"decision": "accepted"`, 1)
	forged = strings.Replace(forged, `"human_approved": false`, `"human_approved": true`, 1)
	mustWriteTestFile(t, path, forged)
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Sealed {
		t.Fatalf("an edited decision passes as sealed: %#v", details.Decision)
	}

	// A record written from scratch, without the key.
	var d Decision
	if err := json.Unmarshal([]byte(forged), &d); err != nil {
		t.Fatal(err)
	}
	d.Seal = strings.Repeat("0", 64)
	data, _ := json.MarshalIndent(d, "", "  ")
	mustWriteTestFile(t, path, string(data))
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Sealed {
		t.Fatalf("a record with a made-up seal passes as sealed: %#v", details.Decision)
	}

	// Without the App's key nothing is sealed, not even a genuine record.
	if _, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	SetDecisionKey([]byte("another key, another App........"))
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Sealed {
		t.Fatalf("a record sealed with another key passes: %#v", details.Decision)
	}
	SetDecisionKey(nil)
	if record, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accept"}); err != nil || record.Seal != "" {
		t.Fatalf("without a key: %#v, %v", record, err)
	}
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Sealed {
		t.Fatalf("an unsealed record passes as sealed: %#v", details.Decision)
	}
}

// "Unchanged since the decision" compares fresh bytes: a file rewritten with
// the same size and modification time is another file.
func TestDecisionCurrentIgnoresSizeAndTime(t *testing.T) {
	withDecisionKey(t, "0123456789abcdef0123456789abcdef")
	root := t.TempDir()
	project := filepath.Join(root, "projects", "demo")
	render := filepath.Join(project, "renders", "final.mp4")
	mustWriteTestFile(t, render, "rendered video")
	opts := workspaceOptions(root)
	if _, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(render)
	if err != nil {
		t.Fatal(err)
	}
	if details, _ := GetProjectDetails(opts, "demo"); details.Decision == nil || !details.Decision.Current {
		t.Fatalf("the decided render is not current: %#v", details.Decision)
	}
	mustWriteTestFile(t, render, "RENDERED VIDEO")
	if err := os.Chtimes(render, time.Now(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if details, _ := GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Current {
		t.Fatalf("a same-size, same-time rewrite passes as the decided bytes: %#v", details.Decision)
	}
}
