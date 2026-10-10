package journeys

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocateProject(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "projects", "demo", "renders", "final.mp4"), "video")
	external := filepath.Join(t.TempDir(), "External Film")
	mustWriteTestFile(t, filepath.Join(external, "renders", "final.mp4"), "external video")
	opts := catalogOptions(t)
	opts.Workspace = root
	registered, err := RegisterProject(opts, "", external, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	for slug, want := range map[string]Location{
		"demo":                  {Slug: "demo", Dir: mustCanonical(t, filepath.Join(root, "projects", "demo")), Ref: "projects/demo"},
		registered.ID:           {Slug: registered.ID, Dir: mustCanonical(t, external), Ref: "catalog/" + registered.ID},
		filepath.Base(external): {Slug: registered.ID, Dir: mustCanonical(t, external), Ref: "catalog/" + registered.ID},
	} {
		loc, err := LocateProject(opts, slug)
		if err != nil || loc != want {
			t.Fatalf("LocateProject(%q) = %+v, %v; want %+v", slug, loc, err, want)
		}
		ref, ok := loc.MediaRef("renders/final.mp4")
		if !ok || ref != want.Ref+"/renders/final.mp4" {
			t.Fatalf("%s: MediaRef = %q, %v", slug, ref, ok)
		}
		// A path a tool reported, absolute inside the project, maps to the
		// same reference, and the reference resolves back to the file.
		if abs, ok := loc.MediaRef(filepath.Join(loc.Dir, "renders", "final.mp4")); !ok || abs != ref {
			t.Fatalf("%s: absolute MediaRef = %q, %v", slug, abs, ok)
		}
		if media, err := ResolveMediaRef(opts, ref); err != nil || media.Project != loc.Dir {
			t.Fatalf("%s: %q resolves to %+v, %v", slug, ref, media, err)
		}
		for _, outside := range []string{"../demo/renders/final.mp4", filepath.Join(filepath.Dir(loc.Dir), "x.mp4"), "C:/x.mp4", "", "."} {
			if ref, ok := loc.MediaRef(outside); ok {
				t.Fatalf("%s: MediaRef(%q) = %q, want a refusal", slug, outside, ref)
			}
		}
	}
	for _, slug := range []string{"", "..", "../demo", "missing"} {
		if _, err := LocateProject(opts, slug); !errors.Is(err, ErrNotFound) {
			t.Fatalf("LocateProject(%q) = %v, want ErrNotFound", slug, err)
		}
	}
	if got := MediaURL("/api/media/", "catalog/x/take #1.wav"); got != "/api/media/catalog/x/take%20%231.wav" {
		t.Fatalf("MediaURL = %q", got)
	}
}

func TestProjectDetailsReportTheDecision(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "demo")
	render := filepath.Join(project, "renders", "final.mp4")
	mustWriteTestFile(t, render, "rendered video")
	opts := workspaceOptions(root)

	details, err := GetProjectDetails(opts, "demo")
	if err != nil || details.Decision != nil {
		t.Fatalf("a project without a decision reported %#v, %v", details.Decision, err)
	}
	if _, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accept", Note: "Ready."}); err != nil {
		t.Fatal(err)
	}
	details, err = GetProjectDetails(opts, "demo")
	if err != nil {
		t.Fatal(err)
	}
	decision := details.Decision
	if decision == nil || decision.Decision.Decision != "accepted" || !decision.HumanApproved || decision.Artifact != "renders/final.mp4" || decision.Note != "Ready." || !decision.Current {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.Path != "projects/demo/review/decision.json" || decision.URL != "/api/media/projects/demo/review/decision.json" || decision.SHA256 != sha256Hex("rendered video") {
		t.Fatalf("decision location = %q %q %q", decision.Path, decision.URL, decision.SHA256)
	}

	// The same size with other bytes, a later time, is another file.
	later := time.Now().Add(time.Minute)
	mustWriteTestFile(t, render, "RENDERED VIDEO")
	if err := os.Chtimes(render, later, later); err != nil {
		t.Fatal(err)
	}
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Current {
		t.Fatalf("a changed render still matches its decision: %#v", details.Decision)
	}
	mustWriteTestFile(t, render, "rendered video, longer")
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Current {
		t.Fatalf("a longer render still matches its decision: %#v", details.Decision)
	}
	if err := os.Remove(render); err != nil {
		t.Fatal(err)
	}
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Current {
		t.Fatalf("a removed render still matches its decision: %#v", details.Decision)
	}

	// A record naming a file outside the project is reported, never followed.
	outside := filepath.Join(root, "outside.mp4")
	mustWriteTestFile(t, outside, "rendered video")
	mustWriteTestFile(t, filepath.Join(project, "review", "decision.json"),
		`{"decision":"accepted","human_approved":true,"artifact":"../../outside.mp4","sha256":"`+sha256Hex("rendered video")+`","size":14}`)
	if details, _ = GetProjectDetails(opts, "demo"); details.Decision == nil || details.Decision.Current {
		t.Fatalf("a decision about a file outside the project = %#v", details.Decision)
	}
	for _, record := range []string{`{"decision":"maybe","artifact":"renders/final.mp4","sha256":"` + sha256Hex("x") + `"}`, `{"decision":"accepted","artifact":"renders/final.mp4","sha256":"short"}`, `not JSON`} {
		mustWriteTestFile(t, filepath.Join(project, "review", "decision.json"), record)
		if details, _ = GetProjectDetails(opts, "demo"); details.Decision != nil {
			t.Fatalf("%s was reported as a decision: %#v", record, details.Decision)
		}
	}
}
