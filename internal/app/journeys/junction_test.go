package journeys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// filepath.EvalSymlinks does not follow Windows junctions and mount points,
// and creating a junction needs no privilege, so containment that relied on
// it alone let a project's review/ junction receive decisions outside the
// project. Junctions are refused wherever they would lead.
func TestJunctionsCannotLeadOutOfTheProject(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "demo")
	mustWriteTestFile(t, filepath.Join(project, "brief.md"), "# Demo\n")
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "rendered video")
	outside := t.TempDir()
	mustWriteTestFile(t, filepath.Join(outside, "assets", "secret.png"), "outside image")
	mustWriteTestFile(t, filepath.Join(outside, "qa", "frame.png"), "outside frame")
	mustWriteTestFile(t, filepath.Join(outside, "narration", "voice.wav"), "outside audio")
	reviewTarget := t.TempDir()
	for link, target := range map[string]string{
		filepath.Join(project, "assets"):                 filepath.Join(outside, "assets"),
		filepath.Join(project, "qa"):                     filepath.Join(outside, "qa"),
		filepath.Join(project, "narration"):              filepath.Join(outside, "narration"),
		filepath.Join(project, "review"):                 reviewTarget,
		filepath.Join(root, "projects", "alias"):         project,
		filepath.Join(root, "projects", "outside-alias"): outside,
	} {
		makeTestJunction(t, target, link)
	}
	opts := workspaceOptions(root)

	details, err := GetProjectDetails(opts, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if details.Brief == "" || len(details.Assets) != 0 || len(details.QAFrames) != 0 || len(details.Narration) != 0 || details.Stages.Voiceover || details.Stages.Review || details.ThumbnailURL != "" {
		t.Fatalf("evidence behind a junction was exposed: %#v", details)
	}
	for _, name := range []string{"assets/secret.png", "qa/frame.png", "narration/voice.wav"} {
		if media, err := ResolveMedia(project, name); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("ResolveMedia(%q) = %#v, %v; want ErrOutsideProject", name, media, err)
		}
		if media, err := ResolveMediaRef(opts, "projects/demo/"+name); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("ResolveMediaRef(%q) = %#v, %v; want ErrOutsideProject", name, media, err)
		}
	}
	for _, slug := range []string{"alias", "outside-alias"} {
		if _, err := GetProjectDetails(opts, slug); !errors.Is(err, ErrNotFound) {
			t.Fatalf("junction project %q = %v, want ErrNotFound", slug, err)
		}
		if _, err := ResolveMediaRef(opts, "projects/"+slug+"/brief.md"); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("junction project media %q = %v, want ErrOutsideProject", slug, err)
		}
	}
	projects, err := ListProjects(opts)
	if err != nil || len(projects) != 1 || projects[0].Slug != "demo" {
		t.Fatalf("junction projects were listed: %#v, %v", projects, err)
	}

	calls := stubTool(t, passingEnvelope())
	if _, err := RecordDecision(project, DecisionRequest{Path: "renders/final.mp4", Decision: "accept"}); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("decision through a review junction = %v, want ErrOutsideProject", err)
	}
	if _, err := ReviewRender(context.Background(), project, ReviewRequest{Path: "renders/final.mp4", Width: 1, Height: 1, FPS: 1, Duration: 1}); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("review through a review junction = %v, want ErrOutsideProject", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused review reached the tool %d times", len(*calls))
	}
	if entries, err := os.ReadDir(reviewTarget); err != nil || len(entries) != 0 {
		t.Fatalf("review records were written through a junction: %v, %v", entries, err)
	}
}
