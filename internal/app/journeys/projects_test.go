package journeys

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func createFixtureWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	script := "# Script\n\n| # | t | Beat | Narration |\n|---|---|---|---|\n| 1 | 0-2 | Opening | Hello. |\n"
	files := map[string]string{
		"projects/documentary-sample/brief.md":                      "# Documentary sample\n",
		"projects/documentary-sample/script.md":                     script,
		"projects/documentary-sample/narration/voice.wav":           "audio",
		"projects/documentary-sample/review/final-frames/frame.jpg": "image",
		"projects/documentary-sample/review/report.json":            `{"status":"reviewed"}`,
		"projects/documentary-sample/renders/final.mp4":             "video",
		"projects/explainer-sample/script.md":                       script,
		"projects/explainer-sample/narration/voice.wav":             "audio",
		"projects/explainer-sample/qa/frame.png":                    "image",
		"projects/explainer-sample/remotion_props.json":             `{"composition_id":"Explainer","cuts":[{"id":"one","type":"text_card","in_seconds":0,"out_seconds":2,"text":"Hello"}]}`,
		"projects/source-edit-sample/brief.md":                      "# Source edit sample\n",
	}
	for name, contents := range files {
		mustWriteTestFile(t, filepath.Join(root, filepath.FromSlash(name)), contents)
	}
	return root
}

func TestListProjectsFixtureWorkspace(t *testing.T) {
	rootDir := createFixtureWorkspace(t)
	projects, err := ListProjects(workspaceOptions(rootDir))
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(projects) < 3 {
		t.Fatalf("expected at least 3 projects, got %d", len(projects))
	}

	slugs := make(map[string]ProjectSummary)
	for _, p := range projects {
		slugs[p.Slug] = p
	}
	for _, expectedSlug := range []string{"documentary-sample", "explainer-sample", "source-edit-sample"} {
		p, ok := slugs[expectedSlug]
		if !ok {
			t.Fatalf("missing expected project slug: %s", expectedSlug)
		}
		if p.Name == "" {
			t.Fatalf("empty project name for %s", expectedSlug)
		}
	}

	cine := slugs["documentary-sample"]
	if !cine.Stages.Brief || !cine.Stages.Script || !cine.Stages.Voiceover || !cine.Stages.Review || !cine.Stages.Master {
		t.Fatalf("unexpected stages for documentary-sample: %#v", cine.Stages)
	}
	if cine.VideoURL == "" {
		t.Fatalf("expected video URL for documentary-sample")
	}
	if cine.ThumbnailURL == "" {
		t.Fatalf("expected thumbnail URL for documentary-sample")
	}
}

func TestListProjectsMissingProjectsRoot(t *testing.T) {
	root := t.TempDir()

	projects, err := ListProjects(Options{Workspace: root})
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if projects == nil || len(projects) != 0 {
		t.Fatalf("ListProjects = %#v, want non-nil empty slice", projects)
	}
	if _, err := GetProjectDetails(Options{Workspace: root}, "missing-project"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProjectDetails without a projects directory = %v, want ErrNotFound", err)
	}
	missingWorkspace := filepath.Join(root, "missing")
	if projects, err := ListProjects(Options{Workspace: missingWorkspace}); err != nil || len(projects) != 0 {
		t.Fatalf("ListProjects(missing workspace) = %#v, %v; want an empty list", projects, err)
	}
	if _, err := os.Stat(missingWorkspace); !os.IsNotExist(err) {
		t.Fatalf("listing created the missing workspace: %v", err)
	}
}

func TestListProjectsEmptyProjectsRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	projects, err := ListProjects(Options{Workspace: root})
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if projects == nil || len(projects) != 0 {
		t.Fatalf("ListProjects = %#v, want non-nil empty slice", projects)
	}
	// A host encodes the list as-is: an empty workspace must be [], not null.
	encoded, err := json.Marshal(projects)
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("encoded empty list = %s, %v; want []", encoded, err)
	}
	if projects, err := ListProjects(Options{}); err != nil || projects == nil || len(projects) != 0 {
		t.Fatalf("ListProjects without workspace or catalog = %#v, %v", projects, err)
	}
}

func TestGetProjectDetails(t *testing.T) {
	rootDir := createFixtureWorkspace(t)
	opts := workspaceOptions(rootDir)

	cine, err := GetProjectDetails(opts, "documentary-sample")
	if err != nil {
		t.Fatalf("GetProjectDetails(documentary-sample) failed: %v", err)
	}
	if cine.Brief == "" {
		t.Fatal("expected brief to be populated")
	}
	if cine.Script == "" {
		t.Fatal("expected script to be populated")
	}
	if len(cine.Beats) == 0 {
		t.Fatal("expected beats to be parsed from script")
	}
	if len(cine.ReviewFrames) == 0 {
		t.Fatal("expected review frames")
	}
	if cine.ReviewReport == nil {
		t.Fatal("expected review report to be loaded")
	}
	if !cine.Stages.Master || cine.VideoPath == "" {
		t.Fatalf("expected master video stage: %#v, videoPath=%s", cine.Stages, cine.VideoPath)
	}
	if cine.VideoVersion == "" {
		t.Fatal("expected a master video version")
	}
	if want := filepath.Join(rootDir, "projects", "documentary-sample"); cine.Path != want {
		t.Fatalf("project path = %q, want %q", cine.Path, want)
	}

	fine, err := GetProjectDetails(opts, "explainer-sample")
	if err != nil {
		t.Fatalf("GetProjectDetails(explainer-sample) failed: %v", err)
	}
	if len(fine.Beats) == 0 {
		t.Fatal("expected beats parsed for explainer-sample")
	}
	if len(fine.Narration) == 0 {
		t.Fatal("expected narration audio files")
	}
	if len(fine.QAFrames) == 0 {
		t.Fatal("expected QA frame captures")
	}
	if fine.RemotionProps == nil {
		t.Fatal("expected remotion props loaded")
	}
	if fine.CompositionPath != "projects/explainer-sample/remotion_props.json" || fine.CompositionURL != "/api/media/projects/explainer-sample/remotion_props.json" {
		t.Fatalf("unexpected composition location: path=%q url=%q", fine.CompositionPath, fine.CompositionURL)
	}
}

func TestEvidenceURLsFollowTheConfiguredPrefix(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "projects", "prefixed", "brief.md"), "# Prefixed\n")
	mustWriteTestFile(t, filepath.Join(root, "projects", "prefixed", "narration", "take #1.wav"), "audio")

	bare, err := GetProjectDetails(Options{Workspace: root}, "prefixed")
	if err != nil {
		t.Fatal(err)
	}
	if bare.BriefPath != "projects/prefixed/brief.md" || bare.BriefURL != "" || len(bare.Narration) != 1 || bare.Narration[0].URL != "" {
		t.Fatalf("without a media prefix: path=%q url=%q narration=%#v", bare.BriefPath, bare.BriefURL, bare.Narration)
	}

	prefixed, err := GetProjectDetails(Options{Workspace: root, MediaURLPrefix: "/files"}, "prefixed")
	if err != nil {
		t.Fatal(err)
	}
	if prefixed.BriefURL != "/files/projects/prefixed/brief.md" {
		t.Fatalf("prefixed brief URL = %q", prefixed.BriefURL)
	}
	if got := prefixed.Narration[0]; got.RelativePath != "projects/prefixed/narration/take #1.wav" || got.URL != "/files/projects/prefixed/narration/take%20%231.wav" {
		t.Fatalf("prefixed narration = %#v", got)
	}
}

func TestMarkdownTableParser(t *testing.T) {
	sampleScript := `# Sample Script

| # | t | Beat | Narration |
|---|---|---|---|
| 1 | 0.0-5.0 | Intro | Hello world. |
| 2 | 5.0-10.0 | Outro | Goodbye world. |
`
	beats := parseMarkdownTableBeats(sampleScript)
	if len(beats) != 2 {
		t.Fatalf("expected 2 beats, got %d", len(beats))
	}
	if beats[0].Index != "1" || beats[0].TimeRange != "0.0-5.0" || beats[0].Title != "Intro" || beats[0].Narration != "Hello world." {
		t.Fatalf("unexpected beat 0: %#v", beats[0])
	}
	if beats[1].Index != "2" || beats[1].TimeRange != "5.0-10.0" || beats[1].Title != "Outro" || beats[1].Narration != "Goodbye world." {
		t.Fatalf("unexpected beat 1: %#v", beats[1])
	}
}

func TestProjectArtifactLayoutFallback(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "fallback-layout")
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "brief.md"), "# Artifact Brief\n")
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "script.md"), "# Artifact Script\n\n| # | Beat |\n|---|---|\n| 1 | Intro |\n")

	details, err := GetProjectDetails(workspaceOptions(root), "fallback-layout")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	if details.Brief == "" || details.Script == "" || !details.Stages.Brief || !details.Stages.Script {
		t.Fatalf("artifact fallback was not loaded: %#v", details)
	}
	if details.Name != "Artifact Brief" || len(details.Beats) != 1 {
		t.Fatalf("artifact fallback was not parsed: name=%q beats=%#v", details.Name, details.Beats)
	}
	if details.BriefPath != "projects/fallback-layout/artifacts/brief.md" || details.BriefURL != "/api/media/projects/fallback-layout/artifacts/brief.md" {
		t.Fatalf("brief fallback location was synthesized: path=%q url=%q", details.BriefPath, details.BriefURL)
	}
	if details.ScriptPath != "projects/fallback-layout/artifacts/script.md" || details.ScriptURL != "/api/media/projects/fallback-layout/artifacts/script.md" {
		t.Fatalf("script fallback location was synthesized: path=%q url=%q", details.ScriptPath, details.ScriptURL)
	}
}

func TestGetProjectDetailsRejectsTraversalAndNonChildren(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "projects", "valid-project", "brief.md"), "# Valid\n")
	mustWriteTestFile(t, filepath.Join(root, "projects", "plain-file"), "not a directory")
	mustWriteTestFile(t, filepath.Join(root, "projects", "parent", "nested", "brief.md"), "# Nested\n")
	mustWriteTestFile(t, filepath.Join(root, "outside", "brief.md"), "# Outside\n")
	opts := Options{Workspace: root}

	if _, err := GetProjectDetails(opts, "valid-project"); err != nil {
		t.Fatalf("valid direct child was rejected: %v", err)
	}
	for _, slug := range []string{
		"",
		".",
		"..",
		"../outside",
		`..\outside`,
		"parent/nested",
		`parent\nested`,
		"/outside",
		`C:\outside`,
		"%2e%2e",
		"%2e%2e%2foutside",
		"..%2Foutside",
		"%252e%252e%252foutside",
		"missing-project",
		"plain-file",
	} {
		t.Run(strings.ReplaceAll(slug, "/", "_"), func(t *testing.T) {
			if _, err := GetProjectDetails(opts, slug); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetProjectDetails(%q) = %v, want ErrNotFound", slug, err)
			}
		})
	}
}

func TestProjectJSONArtifactsAndCompositionLocations(t *testing.T) {
	root := t.TempDir()
	opts := workspaceOptions(root)
	project := filepath.Join(root, "projects", "json-layout")
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "brief.json"), `{"title":"JSON Brief","audience":"Editors"}`)
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "script.json"), `{"name":"JSON Script","scenes":[{"id":"opening","start":0,"end":2.5,"title":"Open","visual":"A real frame","narration":"A real line."}]}`)
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "explainer_props.json"), `{"cuts":[{"type":"hero_title","in_seconds":0,"out_seconds":2.5,"text":"Open"}]}`)

	details, err := GetProjectDetails(opts, "json-layout")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	if details.Name != "JSON Brief" || !strings.Contains(details.Brief, "\n  \"title\": \"JSON Brief\"") || !strings.HasSuffix(details.Brief, "\n") {
		t.Fatalf("brief JSON was not exposed readably: name=%q brief=%q", details.Name, details.Brief)
	}
	if !strings.Contains(details.Script, "\n  \"name\": \"JSON Script\"") || len(details.Beats) != 1 {
		t.Fatalf("script JSON was not exposed or parsed: script=%q beats=%#v", details.Script, details.Beats)
	}
	beat := details.Beats[0]
	if beat.Index != "opening" || beat.TimeRange != "0s - 2.5s" || beat.Title != "Open" || beat.Visual != "A real frame" || beat.Narration != "A real line." {
		t.Fatalf("unexpected JSON script beat: %#v", beat)
	}
	if details.BriefPath != "projects/json-layout/artifacts/brief.json" || details.BriefURL != "/api/media/projects/json-layout/artifacts/brief.json" {
		t.Fatalf("unexpected brief location: path=%q url=%q", details.BriefPath, details.BriefURL)
	}
	if details.ScriptPath != "projects/json-layout/artifacts/script.json" || details.ScriptURL != "/api/media/projects/json-layout/artifacts/script.json" {
		t.Fatalf("unexpected script location: path=%q url=%q", details.ScriptPath, details.ScriptURL)
	}
	if details.CompositionPath != "projects/json-layout/artifacts/explainer_props.json" || details.CompositionURL != "/api/media/projects/json-layout/artifacts/explainer_props.json" || details.RemotionProps == nil {
		t.Fatalf("current props artifact was not resolved: path=%q url=%q props=%#v", details.CompositionPath, details.CompositionURL, details.RemotionProps)
	}

	editProject := filepath.Join(root, "projects", "source-edit")
	mustWriteTestFile(t, filepath.Join(editProject, "artifacts", "edit.json"), `{"segments":[{"start":1,"end":2}],"output":"projects/source-edit/renders/edit.mp4"}`)
	edit, err := GetProjectDetails(opts, "source-edit")
	if err != nil {
		t.Fatalf("GetProjectDetails source-edit failed: %v", err)
	}
	if !edit.Stages.Composition || edit.RemotionProps != nil || edit.CompositionPath != "projects/source-edit/artifacts/edit.json" || edit.CompositionURL != "/api/media/projects/source-edit/artifacts/edit.json" {
		t.Fatalf("edit evidence was not exposed truthfully: %#v", edit)
	}
}

func TestNestedReviewStatusIsNormalized(t *testing.T) {
	root := t.TempDir()
	opts := Options{Workspace: root}
	project := filepath.Join(root, "projects", "failed-review")
	mustWriteTestFile(t, filepath.Join(project, "review", "report.json"), `{"ok":true,"result":{"review_status":"fail","gates":[{"name":"audio","status":"fail"}]}}`)

	details, err := GetProjectDetails(opts, "failed-review")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	report, ok := details.ReviewReport.(map[string]any)
	if !ok || report["status"] != "fail" {
		t.Fatalf("nested review failure was not normalized: %#v", details.ReviewReport)
	}
	if _, nested := report["result"]; nested {
		t.Fatalf("review result envelope was not removed: %#v", report)
	}

	mustWriteTestFile(t, filepath.Join(root, "projects", "unknown-review", "review", "report.json"), `{"result":{"gates":[]}}`)
	unknown, err := GetProjectDetails(opts, "unknown-review")
	if err != nil {
		t.Fatalf("GetProjectDetails unknown review failed: %v", err)
	}
	unknownReport, ok := unknown.ReviewReport.(map[string]any)
	if !ok || unknownReport["status"] != "unknown" {
		t.Fatalf("missing review status was treated as pass: %#v", unknown.ReviewReport)
	}

	mustWriteTestFile(t, filepath.Join(root, "projects", "frames-only", "qa", "frame.png"), "image")
	framesOnly, err := GetProjectDetails(opts, "frames-only")
	if err != nil {
		t.Fatalf("GetProjectDetails frames-only review failed: %v", err)
	}
	if framesOnly.ReviewReport != nil || !framesOnly.Stages.Review {
		t.Fatalf("frames-only evidence synthesized a report or hid review evidence: report=%#v stages=%#v", framesOnly.ReviewReport, framesOnly.Stages)
	}
}

func TestOnlyNonEmptyStableRenderIsMaster(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		contents    string
		wantMaster  bool
		wantPreview bool
	}{
		{name: "final", file: "final.mp4", contents: "video", wantMaster: true},
		{name: "video", file: "video.mp4", contents: "video", wantMaster: true},
		{name: "zero byte final", file: "final.mp4"},
		{name: "edit preview", file: "edit.mp4", contents: "preview", wantPreview: true},
		{name: "vo montage preview", file: "montage_vo.mp4", contents: "preview", wantPreview: true},
		{name: "montage preview", file: "montage.mp4", contents: "preview", wantPreview: true},
		{name: "arbitrary preview", file: "draft.mp4", contents: "preview", wantPreview: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			opts := workspaceOptions(root)
			mustWriteTestFile(t, filepath.Join(root, "projects", "video", "renders", tt.file), tt.contents)
			details, err := GetProjectDetails(opts, "video")
			if err != nil {
				t.Fatalf("GetProjectDetails failed: %v", err)
			}
			wantPath := "projects/video/renders/" + tt.file
			wantURL := "/api/media/" + wantPath
			if details.Stages.Master != tt.wantMaster || (details.VideoURL != "") != tt.wantMaster {
				t.Fatalf("master state = %v video=%q, want master=%v", details.Stages.Master, details.VideoURL, tt.wantMaster)
			}
			if tt.wantMaster && (details.VideoPath != wantPath || details.VideoURL != wantURL || details.VideoVersion == "") {
				t.Fatalf("master location/version = %q %q %q, want %q %q and version", details.VideoPath, details.VideoURL, details.VideoVersion, wantPath, wantURL)
			}
			if (details.PreviewVideoURL != "") != tt.wantPreview {
				t.Fatalf("preview=%q, want preview=%v", details.PreviewVideoURL, tt.wantPreview)
			}
			if tt.wantPreview && (details.PreviewVideoPath != wantPath || details.PreviewVideoURL != wantURL || details.VideoVersion == "") {
				t.Fatalf("preview location/version = %q %q %q, want %q %q and version", details.PreviewVideoPath, details.PreviewVideoURL, details.VideoVersion, wantPath, wantURL)
			}
			projects, err := ListProjects(opts)
			if err != nil || len(projects) != 1 {
				t.Fatalf("ListProjects = %#v, %v", projects, err)
			}
			if projects[0].Stages.Master != tt.wantMaster || (projects[0].VideoURL != "") != tt.wantMaster || (projects[0].PreviewVideoURL != "") != tt.wantPreview {
				t.Fatalf("summary video state = %#v, want master=%v preview=%v", projects[0], tt.wantMaster, tt.wantPreview)
			}
			if tt.contents != "" && !details.Stages.Composition {
				t.Fatal("non-empty preview did not mark composition stage")
			}
			if !reflect.DeepEqual(projects[0].Stages, details.Stages) {
				t.Fatalf("summary/detail stages differ: summary=%#v detail=%#v", projects[0].Stages, details.Stages)
			}
		})
	}
}

func TestPreviewPriorityAndStableMasterSuppressesPreview(t *testing.T) {
	root := t.TempDir()
	opts := Options{Workspace: root}
	renders := filepath.Join(root, "projects", "priority", "renders")
	for _, name := range []string{"draft.mp4", "montage.mp4", "montage_vo.mp4", "edit.mp4"} {
		mustWriteTestFile(t, filepath.Join(renders, name), name)
	}
	details, err := GetProjectDetails(opts, "priority")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	if details.PreviewVideoPath != "projects/priority/renders/edit.mp4" || details.Stages.Master || details.VideoPath != "" {
		t.Fatalf("preview priority claimed a master or selected the wrong preview: %#v", details)
	}

	mustWriteTestFile(t, filepath.Join(renders, "video.mp4"), "stable")
	details, err = GetProjectDetails(opts, "priority")
	if err != nil {
		t.Fatalf("GetProjectDetails with master failed: %v", err)
	}
	if !details.Stages.Master || details.VideoPath != "projects/priority/renders/video.mp4" || details.PreviewVideoPath != "" || details.PreviewVideoURL != "" {
		t.Fatalf("stable master did not suppress preview fallback: %#v", details)
	}
}

func TestZeroByteMediaIsIgnored(t *testing.T) {
	root := t.TempDir()
	opts := Options{Workspace: root}
	project := filepath.Join(root, "projects", "empty-media")
	for _, path := range []string{
		filepath.Join(project, "narration", "voice.ogg"),
		filepath.Join(project, "qa", "frame.png"),
		filepath.Join(project, "review", "final-frames", "review.jpg"),
		filepath.Join(project, "assets", "raw", "thumbnail.webp"),
		filepath.Join(project, "renders", "draft.mp4"),
	} {
		mustWriteTestFile(t, path, "")
	}

	details, err := GetProjectDetails(opts, "empty-media")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	if details.Stages.Voiceover || details.Stages.Review || details.Stages.Composition || len(details.Narration) != 0 || len(details.QAFrames) != 0 || len(details.ReviewFrames) != 0 || details.ThumbnailPath != "" || details.PreviewVideoPath != "" {
		t.Fatalf("zero-byte media was exposed as evidence: %#v", details)
	}

	mustWriteTestFile(t, filepath.Join(project, "assets", "audio", "voice.ogg"), "audio")
	mustWriteTestFile(t, filepath.Join(project, "qa", "real.png"), "image")
	details, err = GetProjectDetails(opts, "empty-media")
	if err != nil {
		t.Fatalf("GetProjectDetails with media failed: %v", err)
	}
	if !details.Stages.Voiceover || len(details.Narration) != 1 || details.Narration[0].Name != "voice.ogg" {
		t.Fatalf("non-empty OGG evidence was not exposed: %#v", details.Narration)
	}
	if !details.Stages.Review || len(details.QAFrames) != 1 || details.ThumbnailPath != "projects/empty-media/qa/real.png" {
		t.Fatalf("non-empty image evidence was not exposed: frames=%#v thumbnail=%q", details.QAFrames, details.ThumbnailPath)
	}
}

func TestProjectSummaryAndDetailsShareEvidenceSemantics(t *testing.T) {
	root := t.TempDir()
	opts := workspaceOptions(root)
	project := filepath.Join(root, "projects", "consistent")
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "brief.json"), `{"name":"Consistent Project"}`)
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "script.json"), `{"beats":[{"title":"Beat one","narration":"Truth"}]}`)
	mustWriteTestFile(t, filepath.Join(project, "narration", "voice.ogg"), "audio")
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "explainer_props.json"), `{"cuts":[]}`)
	mustWriteTestFile(t, filepath.Join(project, "review", "report.json"), `{"status":"pass","gates":[]}`)
	mustWriteTestFile(t, filepath.Join(project, "renders", "edit.mp4"), "preview")

	details, err := GetProjectDetails(opts, "consistent")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	projects, err := ListProjects(opts)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = %#v, %v", projects, err)
	}
	summary := projects[0]
	if !reflect.DeepEqual(summary.Stages, details.Stages) {
		t.Fatalf("summary/detail stages differ: summary=%#v detail=%#v", summary.Stages, details.Stages)
	}
	if !details.Stages.Brief || !details.Stages.Script || !details.Stages.Voiceover || !details.Stages.Composition || !details.Stages.Review || details.Stages.Master {
		t.Fatalf("unexpected shared evidence stages: %#v", details.Stages)
	}
	if summary.Name != details.Name || summary.BriefPath != details.BriefPath || summary.BriefURL != details.BriefURL || summary.ScriptPath != details.ScriptPath || summary.ScriptURL != details.ScriptURL || summary.CompositionPath != details.CompositionPath || summary.CompositionURL != details.CompositionURL || summary.PreviewVideoPath != details.PreviewVideoPath || summary.PreviewVideoURL != details.PreviewVideoURL || summary.VideoVersion != details.VideoVersion {
		t.Fatalf("summary/detail evidence locations differ: summary=%#v detail=%#v", summary, details)
	}
	if details.ReviewReport == nil || len(details.Narration) != 1 || len(details.Beats) != 1 {
		t.Fatalf("valid detail evidence was not returned: %#v", details)
	}

	invalidProject := filepath.Join(root, "projects", "invalid-review")
	mustWriteTestFile(t, filepath.Join(invalidProject, "review", "report.json"), `{not JSON}`)
	invalidDetails, err := GetProjectDetails(opts, "invalid-review")
	if err != nil {
		t.Fatalf("GetProjectDetails invalid-review failed: %v", err)
	}
	if invalidDetails.Stages.Review || invalidDetails.ReviewReport != nil {
		t.Fatalf("invalid review JSON was exposed as valid evidence: %#v", invalidDetails)
	}
}

func TestVideoVersionChangesWhenSamePathIsReplaced(t *testing.T) {
	root := t.TempDir()
	opts := Options{Workspace: root}
	videoPath := filepath.Join(root, "projects", "versioned", "renders", "final.mp4")
	mustWriteTestFile(t, videoPath, "first")
	firstTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(videoPath, firstTime, firstTime); err != nil {
		t.Fatalf("Chtimes first video: %v", err)
	}
	first, err := GetProjectDetails(opts, "versioned")
	if err != nil {
		t.Fatalf("GetProjectDetails first video failed: %v", err)
	}

	mustWriteTestFile(t, videoPath, "later")
	secondTime := firstTime.Add(time.Second)
	if err := os.Chtimes(videoPath, secondTime, secondTime); err != nil {
		t.Fatalf("Chtimes replacement video: %v", err)
	}
	second, err := GetProjectDetails(opts, "versioned")
	if err != nil {
		t.Fatalf("GetProjectDetails replacement video failed: %v", err)
	}
	if first.VideoPath != second.VideoPath || first.VideoURL != second.VideoURL || first.VideoVersion == "" || second.VideoVersion == "" || first.VideoVersion == second.VideoVersion {
		t.Fatalf("same-path replacement was not versioned: first=%q second=%q path=%q url=%q", first.VideoVersion, second.VideoVersion, second.VideoPath, second.VideoURL)
	}
	projects, err := ListProjects(opts)
	if err != nil || len(projects) != 1 || projects[0].VideoVersion != second.VideoVersion {
		t.Fatalf("summary version does not match detail: projects=%#v err=%v detail=%q", projects, err, second.VideoVersion)
	}
}

func TestProjectEvidenceAcceptsSafeRegularFiles(t *testing.T) {
	root := t.TempDir()
	opts := workspaceOptions(root)
	project := filepath.Join(root, "projects", "safe-evidence")
	briefPath := filepath.Join(project, "brief.md")
	mustWriteTestFile(t, briefPath, "# Safe Evidence\n")
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "script.json"), `{"beats":[{"title":"Safe beat","narration":"Safe narration"}]}`)
	mustWriteTestFile(t, filepath.Join(project, "artifacts", "safe_props.json"), `{"cuts":[]}`)
	mustWriteTestFile(t, filepath.Join(project, "narration", "voice.wav"), "audio")
	mustWriteTestFile(t, filepath.Join(project, "qa", "qa.png"), "qa frame")
	mustWriteTestFile(t, filepath.Join(project, "review", "final-frames", "review.jpg"), "review frame")
	mustWriteTestFile(t, filepath.Join(project, "review", "report.json"), `{"status":"pass"}`)
	mustWriteTestFile(t, filepath.Join(project, "renders", "final.mp4"), "video")
	mustWriteTestFile(t, filepath.Join(project, "assets", "raw", "thumbnail.webp"), "thumbnail")

	details, err := GetProjectDetails(opts, "safe-evidence")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	if details.Name != "Safe Evidence" || details.Brief == "" || details.Script == "" || details.RemotionProps == nil || details.ReviewReport == nil {
		t.Fatalf("safe artifact evidence was not loaded: %#v", details)
	}
	if len(details.Beats) != 1 || len(details.Narration) != 1 || len(details.QAFrames) != 1 || len(details.ReviewFrames) != 1 {
		t.Fatalf("safe structured and media evidence was not loaded: %#v", details)
	}
	if !details.Stages.Brief || !details.Stages.Script || !details.Stages.Voiceover || !details.Stages.Composition || !details.Stages.Review || !details.Stages.Master {
		t.Fatalf("safe evidence stages = %#v", details.Stages)
	}
	if details.VideoPath != "projects/safe-evidence/renders/final.mp4" || details.VideoURL != "/api/media/projects/safe-evidence/renders/final.mp4" || details.VideoVersion == "" {
		t.Fatalf("safe video evidence = path %q, URL %q, version %q", details.VideoPath, details.VideoURL, details.VideoVersion)
	}
	if details.ThumbnailPath != "projects/safe-evidence/review/final-frames/review.jpg" || details.ThumbnailURL != "/api/media/projects/safe-evidence/review/final-frames/review.jpg" {
		t.Fatalf("safe thumbnail evidence = path %q, URL %q", details.ThumbnailPath, details.ThumbnailURL)
	}
	media, err := ResolveMediaRef(opts, details.BriefPath)
	if err != nil {
		t.Fatalf("safe brief reference was not resolved: %v", err)
	}
	if media.Path != mustCanonical(t, briefPath) || media.Name != "brief.md" || media.ContentType != "text/markdown; charset=utf-8" {
		t.Fatalf("safe brief media = %#v", media)
	}
}

func TestProjectEvidenceIgnoresEscapedFileSymlinks(t *testing.T) {
	root := t.TempDir()
	opts := workspaceOptions(root)
	project := filepath.Join(root, "projects", "file-escape")
	otherProject := filepath.Join(root, "projects", "other-project")
	outside := t.TempDir()
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	targets := map[string]string{
		filepath.Join(project, "brief.md"):                        filepath.Join(outside, "brief.md"),
		filepath.Join(project, "script.json"):                     filepath.Join(otherProject, "script.json"),
		filepath.Join(project, "remotion_props.json"):             filepath.Join(outside, "props.json"),
		filepath.Join(project, "artifacts", "edit.json"):          filepath.Join(otherProject, "edit.json"),
		filepath.Join(project, "review", "report.json"):           filepath.Join(outside, "report.json"),
		filepath.Join(project, "narration", "voice.wav"):          filepath.Join(outside, "voice.wav"),
		filepath.Join(project, "qa", "frame.png"):                 filepath.Join(otherProject, "frame.png"),
		filepath.Join(project, "renders", "final.mp4"):            filepath.Join(outside, "final.mp4"),
		filepath.Join(project, "assets", "raw", "thumbnail.webp"): filepath.Join(otherProject, "thumbnail.webp"),
	}
	contents := map[string]string{
		filepath.Join(outside, "brief.md"):            "# Leaked Brief\n",
		filepath.Join(otherProject, "script.json"):    `{"title":"Leaked Script","beats":[{"title":"Leak"}]}`,
		filepath.Join(outside, "props.json"):          `{"cuts":[{"text":"Leak"}]}`,
		filepath.Join(otherProject, "edit.json"):      `{"segments":[{"name":"Leak"}]}`,
		filepath.Join(outside, "report.json"):         `{"status":"pass","secret":"leak"}`,
		filepath.Join(outside, "voice.wav"):           "leaked audio",
		filepath.Join(otherProject, "frame.png"):      "leaked frame",
		filepath.Join(outside, "final.mp4"):           "leaked video",
		filepath.Join(otherProject, "thumbnail.webp"): "leaked thumbnail",
	}
	for path, content := range contents {
		mustWriteTestFile(t, path, content)
	}
	for link, target := range targets {
		if !makeTestSymlink(t, target, link) {
			return
		}
	}

	details, err := GetProjectDetails(opts, "file-escape")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	if details.Brief != "" || details.Script != "" || details.RemotionProps != nil || details.ReviewReport != nil {
		t.Fatalf("escaped inline artifact was exposed: %#v", details)
	}
	if len(details.Beats) != 0 || len(details.Narration) != 0 || len(details.QAFrames) != 0 || len(details.ReviewFrames) != 0 {
		t.Fatalf("escaped structured or media artifact was exposed: %#v", details)
	}
	if details.BriefPath != "" || details.BriefURL != "" || details.ScriptPath != "" || details.ScriptURL != "" || details.CompositionPath != "" || details.CompositionURL != "" || details.VideoPath != "" || details.VideoURL != "" || details.PreviewVideoPath != "" || details.PreviewVideoURL != "" || details.ThumbnailPath != "" || details.ThumbnailURL != "" {
		t.Fatalf("escaped artifact location or URL was exposed: %#v", details)
	}
	if details.Stages != (StageStatuses{}) {
		t.Fatalf("escaped files affected stages: %#v", details.Stages)
	}
	for link := range targets {
		rel, err := filepath.Rel(project, link)
		if err != nil {
			t.Fatal(err)
		}
		ref := "projects/file-escape/" + filepath.ToSlash(rel)
		if media, err := ResolveMediaRef(opts, ref); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("escaped file reference %q = %#v, %v; want ErrOutsideProject", ref, media, err)
		}
	}

	projects, err := ListProjects(opts)
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	for _, summary := range projects {
		if summary.Slug == "file-escape" && (summary.Stages != (StageStatuses{}) || summary.BriefURL != "" || summary.ScriptURL != "" || summary.CompositionURL != "" || summary.VideoURL != "" || summary.PreviewVideoURL != "" || summary.ThumbnailURL != "") {
			t.Fatalf("escaped file evidence reached project summary: %#v", summary)
		}
	}
}

func TestProjectEvidenceIgnoresEscapedDirectorySymlinks(t *testing.T) {
	root := t.TempDir()
	opts := Options{Workspace: root}
	project := filepath.Join(root, "projects", "directory-escape")
	otherProject := filepath.Join(root, "projects", "other-project")
	outside := t.TempDir()
	mustWriteTestFile(t, filepath.Join(project, "brief.md"), "# Directory Escape\n")
	mustWriteTestFile(t, filepath.Join(outside, "artifacts", "script.json"), `{"title":"Leaked Script"}`)
	mustWriteTestFile(t, filepath.Join(outside, "artifacts", "leaked_props.json"), `{"cuts":[{"text":"Leak"}]}`)
	mustWriteTestFile(t, filepath.Join(outside, "narration", "voice.wav"), "leaked audio")
	mustWriteTestFile(t, filepath.Join(otherProject, "qa", "frame.png"), "leaked frame")
	mustWriteTestFile(t, filepath.Join(otherProject, "review", "report.json"), `{"status":"pass"}`)
	mustWriteTestFile(t, filepath.Join(otherProject, "review", "final-frames", "review.jpg"), "leaked review frame")
	mustWriteTestFile(t, filepath.Join(outside, "renders", "final.mp4"), "leaked video")

	for link, target := range map[string]string{
		filepath.Join(project, "artifacts"): filepath.Join(outside, "artifacts"),
		filepath.Join(project, "narration"): filepath.Join(outside, "narration"),
		filepath.Join(project, "qa"):        filepath.Join(otherProject, "qa"),
		filepath.Join(project, "review"):    filepath.Join(otherProject, "review"),
		filepath.Join(project, "renders"):   filepath.Join(outside, "renders"),
	} {
		if !makeTestSymlink(t, target, link) {
			return
		}
	}

	details, err := GetProjectDetails(opts, "directory-escape")
	if err != nil {
		t.Fatalf("GetProjectDetails failed: %v", err)
	}
	if details.Brief == "" || !details.Stages.Brief {
		t.Fatalf("safe regular artifact stopped working: %#v", details)
	}
	if details.Script != "" || details.RemotionProps != nil || details.ReviewReport != nil || details.Stages.Voiceover || details.Stages.Review || details.Stages.Composition || details.Stages.Master || len(details.Narration) != 0 || len(details.QAFrames) != 0 || len(details.ReviewFrames) != 0 || details.VideoURL != "" || details.PreviewVideoURL != "" || details.ThumbnailURL != "" {
		t.Fatalf("escaped directory evidence was exposed: %#v", details)
	}
	for _, ref := range []string{
		"projects/directory-escape/artifacts/script.json",
		"projects/directory-escape/artifacts/leaked_props.json",
		"projects/directory-escape/narration/voice.wav",
		"projects/directory-escape/qa/frame.png",
		"projects/directory-escape/review/report.json",
		"projects/directory-escape/renders/final.mp4",
	} {
		if media, err := ResolveMediaRef(opts, ref); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("escaped directory reference %q = %#v, %v; want ErrOutsideProject", ref, media, err)
		}
	}
}

func TestProjectRootSymlinkCannotAliasAnotherProject(t *testing.T) {
	root := t.TempDir()
	opts := Options{Workspace: root}
	realProject := filepath.Join(root, "projects", "real-project")
	aliasProject := filepath.Join(root, "projects", "alias-project")
	outsideProject := filepath.Join(t.TempDir(), "outside-project")
	outsideAlias := filepath.Join(root, "projects", "outside-alias")
	mustWriteTestFile(t, filepath.Join(realProject, "brief.md"), "# Real Project\n")
	mustWriteTestFile(t, filepath.Join(outsideProject, "brief.md"), "# Outside Project\n")
	if !makeTestSymlink(t, realProject, aliasProject) || !makeTestSymlink(t, outsideProject, outsideAlias) {
		return
	}

	for _, slug := range []string{"alias-project", "outside-alias"} {
		if _, err := GetProjectDetails(opts, slug); err == nil {
			t.Fatalf("linked project root %q was accepted", slug)
		}
	}
	projects, err := ListProjects(opts)
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	for _, project := range projects {
		if project.Slug == "alias-project" || project.Slug == "outside-alias" {
			t.Fatalf("linked project root was listed: %#v", project)
		}
	}
	for _, ref := range []string{"projects/alias-project/brief.md", "projects/outside-alias/brief.md"} {
		if media, err := ResolveMediaRef(opts, ref); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("linked project root reference %q = %#v, %v; want ErrOutsideProject", ref, media, err)
		}
	}
	if _, err := ResolveMediaRef(opts, "projects/real-project/brief.md"); err != nil {
		t.Fatalf("real project reference was refused: %v", err)
	}
}

func TestProjectsDirectoryLinkLeavingWorkspaceIsRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWriteTestFile(t, filepath.Join(outside, "leaked", "brief.md"), "# Leaked\n")
	if !makeTestSymlink(t, outside, filepath.Join(root, "projects")) {
		return
	}
	opts := Options{Workspace: root}
	if projects, err := ListProjects(opts); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("ListProjects through an escaping projects link = %#v, %v; want ErrOutsideProject", projects, err)
	}
	if _, err := GetProjectDetails(opts, "leaked"); err == nil {
		t.Fatal("project behind an escaping projects link was opened")
	}
	if _, err := ResolveMediaRef(opts, "projects/leaked/brief.md"); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("media behind an escaping projects link = %v, want ErrOutsideProject", err)
	}
}

func TestSlugToTitle(t *testing.T) {
	if got := slugToTitle("documentary-sample"); got != "Documentary Sample" {
		t.Errorf("slugToTitle failed: got %q", got)
	}
	if got := slugToTitle("source-edit-sample"); got != "Source Edit Sample" {
		t.Errorf("slugToTitle failed: got %q", got)
	}
}

func TestExtractBeatsFromRemotionProps(t *testing.T) {
	rawJSON := `{
		"cuts": [
			{
				"type": "intro",
				"in_seconds": 0.0,
				"out_seconds": 3.5,
				"title": "Welcome",
				"text": "Hello all"
			},
			{
				"type": "scene",
				"in_seconds": 3.5,
				"out_seconds": 8.0,
				"text": "Main content"
			}
		]
	}`
	var props any
	if err := json.Unmarshal([]byte(rawJSON), &props); err != nil {
		t.Fatalf("unmarshal props failed: %v", err)
	}

	beats := extractBeatsFromRemotionProps(props)
	if len(beats) != 2 {
		t.Fatalf("expected 2 beats, got %d", len(beats))
	}
	if beats[0].Title != "Welcome" || beats[0].Type != "intro" {
		t.Errorf("unexpected beat 0: %#v", beats[0])
	}
	if beats[1].Title != "Main content" || beats[1].Type != "scene" {
		t.Errorf("unexpected beat 1: %#v", beats[1])
	}
}
