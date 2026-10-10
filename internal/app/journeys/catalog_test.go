package journeys

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCatalogOperations(t *testing.T) {
	opts := catalogOptions(t)

	cat, err := LoadCatalog(opts)
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(cat.Projects) != 0 {
		t.Errorf("expected empty projects, got %d", len(cat.Projects))
	}
	if cat.DefaultRoot != opts.ProductionsRoot {
		t.Errorf("default root = %q, want the configured %q", cat.DefaultRoot, opts.ProductionsRoot)
	}
	if _, err := os.Stat(opts.CatalogPath); !os.IsNotExist(err) {
		t.Fatalf("loading created the catalog file: %v", err)
	}

	projDir := filepath.Join(t.TempDir(), "my-test-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registered, err := RegisterProject(opts, "My Test Video", projDir, "claude", []string{"explainer"})
	if err != nil {
		t.Fatalf("RegisterProject failed: %v", err)
	}
	if registered.Name != "My Test Video" || registered.ID != "my-test-video" || registered.Path != projDir {
		t.Errorf("registered = %#v", registered)
	}
	if len(registered.Packs) != 1 || registered.Packs[0] != "explainer" {
		t.Errorf("expected pack 'explainer', got %v", registered.Packs)
	}

	cat2, err := LoadCatalog(opts)
	if err != nil {
		t.Fatalf("LoadCatalog reload failed: %v", err)
	}
	if len(cat2.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(cat2.Projects))
	}
	if !cat2.Projects[0].Exists {
		t.Errorf("expected project to exist on disk")
	}

	cat2.Projects[0].Name = "Renamed"
	if err := SaveCatalog(opts, cat2); err != nil {
		t.Fatalf("SaveCatalog failed: %v", err)
	}
	cat3, err := LoadCatalog(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cat2, cat3) {
		t.Fatalf("saved catalog did not round-trip:\nsaved  %#v\nloaded %#v", cat2, cat3)
	}

	if err := os.Remove(projDir); err != nil {
		t.Fatal(err)
	}
	cat4, err := LoadCatalog(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cat4.Projects[0].Exists {
		t.Fatal("a removed project directory is still reported as existing")
	}
}

// The core reads and writes only the locations the Options name: no
// environment variable, home directory, OS application-data folder or
// temporary-directory heuristic is consulted.
func TestOptionsAreTheOnlyStateLocations(t *testing.T) {
	// Every temporary directory is made before the environment changes:
	// some platforms derive the temporary directory from it.
	envRoot := t.TempDir()
	workspace := t.TempDir()
	mustWriteTestFile(t, filepath.Join(workspace, "projects", "local", "brief.md"), "# Local\n")
	existing := t.TempDir()
	mustWriteTestFile(t, filepath.Join(existing, "brief.md"), "# Existing\n")
	opts := catalogOptions(t)
	opts.Workspace = workspace
	unrooted := Options{CatalogPath: filepath.Join(t.TempDir(), "catalog.json")}

	var envDirs []string
	for _, key := range []string{"FACET_HOME", "LOCALAPPDATA", "APPDATA", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		dir := filepath.Join(envRoot, key)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
		envDirs = append(envDirs, dir)
	}

	created, err := CreateProject(opts, NewProject{Name: "State Check"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(opts.ProductionsRoot, "state-check"); created.Path != want {
		t.Fatalf("created project at %q, want under the configured productions root %q", created.Path, want)
	}
	if _, err := OpenProject(opts, existing, ""); err != nil {
		t.Fatal(err)
	}
	if projects, err := ListProjects(opts); err != nil || len(projects) != 3 {
		t.Fatalf("ListProjects = %#v, %v", projects, err)
	}
	if _, err := os.Stat(opts.CatalogPath); err != nil {
		t.Fatalf("catalog was not written at the configured path: %v", err)
	}
	for _, dir := range envDirs {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Errorf("environment location %s was used: %v %v", dir, entries, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, ".facet")); !os.IsNotExist(err) {
		t.Errorf("a catalog was placed inside the workspace: %v", err)
	}

	// Without configured locations there is no fallback.
	if _, err := LoadCatalog(Options{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("LoadCatalog without a catalog path = %v, want ErrInvalidRequest", err)
	}
	if _, err := CreateProject(unrooted, NewProject{Name: "Nowhere"}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("CreateProject without any parent directory = %v, want ErrInvalidRequest", err)
	}
	for _, dir := range envDirs {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("environment location %s was used by a fallback: %v", dir, entries)
		}
	}
}

func TestCreateProjectRefusesExistingAndEscapingDirectory(t *testing.T) {
	opts := catalogOptions(t)
	root := t.TempDir()
	project, err := CreateProject(opts, NewProject{Name: "Existing", Slug: "existing", Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Packs) != 0 || project.Engine != "" {
		t.Fatalf("an empty request recorded packs or an engine: %#v", project)
	}
	if project.Path != filepath.Join(root, "existing") {
		t.Fatalf("project path = %q", project.Path)
	}
	// Creation makes the directory and nothing inside it.
	entries, err := os.ReadDir(project.Path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("new project directory contents = %v, %v; want an empty directory", entries, err)
	}
	if _, err := CreateProject(opts, NewProject{Name: "Existing", Slug: "existing", Dir: root}); !errors.Is(err, ErrConflict) {
		t.Fatalf("existing project folder was not refused: %v", err)
	}
	for _, slug := range []string{"../escape", "nested/folder", `nested\folder`, ".", "..", "%2e%2e", "%2e%2e%2fescape"} {
		if _, err := CreateProject(opts, NewProject{Name: "Invalid", Slug: slug, Dir: root}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("unsafe slug %q = %v, want ErrInvalidRequest", slug, err)
		}
	}
	names, err := os.ReadDir(root)
	if err != nil || len(names) != 1 || names[0].Name() != "existing" {
		t.Fatalf("parent directory after refused requests = %v, %v", names, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); !os.IsNotExist(err) {
		t.Fatalf("an escaping slug created a directory: %v", err)
	}
	cat, err := LoadCatalog(opts)
	if err != nil || len(cat.Projects) != 1 {
		t.Fatalf("catalog after refused requests = %#v, %v", cat, err)
	}
}

func TestCreateProjectUsesFolderSlugAsCatalogID(t *testing.T) {
	opts := catalogOptions(t)
	root := t.TempDir()
	project, err := CreateProject(opts, NewProject{Name: "Launch Film 2026!", Slug: "launch-film-2026", Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if project.ID != "launch-film-2026" || project.Name != "Launch Film 2026!" {
		t.Fatalf("project = %#v, want the folder slug as ID", project)
	}

	derived, err := CreateProject(opts, NewProject{Name: "Spring Promo / Cut 2"})
	if err != nil {
		t.Fatal(err)
	}
	if derived.ID != "spring-promo-cut-2" || derived.Path != filepath.Join(opts.ProductionsRoot, "spring-promo-cut-2") {
		t.Fatalf("derived project = %#v", derived)
	}
	untitled, err := CreateProject(opts, NewProject{})
	if err != nil {
		t.Fatal(err)
	}
	if untitled.Name != "Untitled Production" || untitled.ID != "untitled-production" {
		t.Fatalf("untitled project = %#v", untitled)
	}
}

func TestCreatedProjectCanBeOpenedAgain(t *testing.T) {
	opts := catalogOptions(t)
	created, err := CreateProject(opts, NewProject{Name: "Unit Test Production", Slug: "unit-test-prod", Dir: filepath.Join(t.TempDir(), "productions"), Engine: "claude", Packs: []string{"explainer"}})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(created.Path); err != nil || !info.IsDir() {
		t.Fatalf("expected created project directory: %v", err)
	}
	// 2.0 has no project initialization: no lock file or projections appear.
	if _, err := os.Stat(filepath.Join(created.Path, "facet.lock.json")); !os.IsNotExist(err) {
		t.Fatalf("creation wrote facet.lock.json: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	opened, err := OpenProject(opts, created.Path, "")
	if err != nil {
		t.Fatal(err)
	}
	if opened.ID != created.ID || opened.Name != "Unit Test Production" || opened.Engine != "claude" || !reflect.DeepEqual(opened.Packs, []string{"explainer"}) || !opened.LastOpenedAt.After(created.LastOpenedAt) {
		t.Fatalf("reopened project = %#v, created %#v", opened, created)
	}
	cat, err := LoadCatalog(opts)
	if err != nil || len(cat.Projects) != 1 {
		t.Fatalf("reopening duplicated the entry: %#v, %v", cat, err)
	}
}

func TestOpenProjectWritesNothingIntoFolder(t *testing.T) {
	project := filepath.Join(t.TempDir(), "existing-production")
	for name, contents := range map[string]string{
		"brief.md":             "# Existing Production\n",
		"artifacts/script.md":  "# Script\n\n| # | Beat |\n|---|---|\n| 1 | Intro |\n",
		"renders/final.mp4":    "video",
		"review/report.json":   `{"status":"pass"}`,
		"facet.lock.json":      `{"version":"1","engine":"opencode","packs":["explainer"]}`,
		".facet.yaml":          "paths: {}\n",
		"notes/agent-notes.md": "keep me\n",
	} {
		mustWriteTestFile(t, filepath.Join(project, filepath.FromSlash(name)), contents)
	}
	if err := os.Mkdir(filepath.Join(project, "narration"), 0o755); err != nil {
		t.Fatal(err)
	}
	ageTree(t, project)
	before := snapshotTree(t, project)

	opts := catalogOptions(t)
	opened, err := OpenProject(opts, project, "")
	if err != nil {
		t.Fatalf("OpenProject failed: %v", err)
	}
	assertTreeUnchanged(t, project, before)
	if opened.ID != "existing-production" || opened.Name != "existing-production" || opened.Path != project {
		t.Fatalf("opened project identity = %#v", opened)
	}
	if opened.Engine != "opencode" || !reflect.DeepEqual(opened.Packs, []string{"explainer"}) {
		t.Fatalf("lock metadata was not read: engine=%q packs=%v", opened.Engine, opened.Packs)
	}
	if _, err := os.Stat(opts.CatalogPath); err != nil {
		t.Fatalf("the catalog entry was not saved: %v", err)
	}

	// Reopening, listing, inspecting and resolving media are read-only too.
	if _, err := OpenProject(opts, project, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := ListProjects(opts); err != nil {
		t.Fatal(err)
	}
	details, err := GetProjectDetails(opts, opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	if details.Engine != "codex" || !details.Stages.Master || details.Name != "Existing Production" {
		t.Fatalf("opened project details = %#v", details)
	}
	if _, err := ResolveMediaRef(opts, details.VideoPath); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, project, before)
}

func TestOpenProjectRefusesCatalogInsideFolder(t *testing.T) {
	project := t.TempDir()
	mustWriteTestFile(t, filepath.Join(project, "brief.md"), "# Contained\n")
	ageTree(t, project)
	before := snapshotTree(t, project)

	for _, catalog := range []string{
		filepath.Join(project, "catalog.json"),
		filepath.Join(project, ".facet", "catalog.json"),
	} {
		opts := Options{CatalogPath: catalog}
		if _, err := OpenProject(opts, project, ""); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("OpenProject with catalog %s = %v, want ErrInvalidRequest", catalog, err)
		}
		if _, err := RegisterProject(opts, "contained", project, "", nil); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("RegisterProject with catalog %s = %v, want ErrInvalidRequest", catalog, err)
		}
	}
	assertTreeUnchanged(t, project, before)

	parent := t.TempDir()
	opts := Options{CatalogPath: filepath.Join(parent, "new-project", "catalog.json")}
	if _, err := CreateProject(opts, NewProject{Slug: "new-project", Dir: parent}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateProject with its catalog inside the new folder = %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "new-project")); !os.IsNotExist(err) {
		t.Fatalf("a refused creation left a directory: %v", err)
	}
}

func TestOpenProjectRequiresAnExistingDirectory(t *testing.T) {
	opts := catalogOptions(t)
	file := filepath.Join(t.TempDir(), "file.txt")
	mustWriteTestFile(t, file, "not a directory")
	for _, dir := range []string{filepath.Join(t.TempDir(), "missing"), file} {
		if _, err := OpenProject(opts, dir, ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("OpenProject(%q) = %v, want ErrNotFound", dir, err)
		}
	}
	if _, err := OpenProject(opts, " ", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("OpenProject without a directory = %v", err)
	}
	if _, err := RegisterProject(opts, "Missing", filepath.Join(t.TempDir(), "missing"), "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RegisterProject of a missing directory = %v", err)
	}
	if _, err := os.Stat(opts.CatalogPath); !os.IsNotExist(err) {
		t.Fatalf("refused requests wrote the catalog: %v", err)
	}
}

func TestInvalidCatalogIsNotOverwritten(t *testing.T) {
	opts := catalogOptions(t)
	mustWriteTestFile(t, opts.CatalogPath, `{"projects": [ {"id": "kept"`)
	project := t.TempDir()

	if _, err := LoadCatalog(opts); err == nil {
		t.Fatal("an unreadable catalog loaded as empty")
	}
	if _, err := RegisterProject(opts, "Project", project, "", nil); err == nil {
		t.Fatal("registration replaced an unreadable catalog")
	}
	if _, err := OpenProject(opts, project, ""); err == nil {
		t.Fatal("opening replaced an unreadable catalog")
	}
	if _, err := CreateProject(opts, NewProject{Name: "Fresh"}); err == nil {
		t.Fatal("creation replaced an unreadable catalog")
	}
	if _, err := os.Stat(filepath.Join(opts.ProductionsRoot, "fresh")); !os.IsNotExist(err) {
		t.Fatalf("creation left a directory behind an unreadable catalog: %v", err)
	}
	if _, err := ListProjects(opts); err == nil {
		t.Fatal("listing hid an unreadable catalog")
	}
	if got := mustReadTestFile(t, opts.CatalogPath); got != `{"projects": [ {"id": "kept"` {
		t.Fatalf("unreadable catalog was modified: %q", got)
	}
}

func TestCatalogProjectJourney(t *testing.T) {
	for _, withProjects := range []bool{false, true} {
		name := "catalog-only"
		if withProjects {
			name = "with-local-projects"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if withProjects {
				mustWriteTestFile(t, filepath.Join(root, "projects", "local", "brief.md"), "# Local")
			}
			// The catalog ID intentionally differs from the external folder name.
			project := filepath.Join(t.TempDir(), "external-folder")
			files := map[string]string{
				"brief.md":                     "# External Production",
				"artifacts/script.json":        `{"title":"Script","beats":[{"narration":"Hello"}]}`,
				"artifacts/edit.json":          `{"clips":[]}`,
				"narration/voice 100% #1.mp3":  "audio",
				"qa/frame #1.png":              "image",
				"review/final-frames/shot.png": "review image",
				"review/report.json":           `{"status":"passed"}`,
				"renders/final.mp4":            "video",
				"facet.lock.json":              `{"engine":"claude"}`,
			}
			for path, content := range files {
				mustWriteTestFile(t, filepath.Join(project, filepath.FromSlash(path)), content)
			}
			opts := catalogOptions(t)
			opts.Workspace = root
			registered, err := RegisterProject(opts, "Catalog Movie", project, "codex", nil)
			if err != nil {
				t.Fatal(err)
			}

			summaries, err := ListProjects(opts)
			if err != nil {
				t.Fatal(err)
			}
			var summary *ProjectSummary
			for i := range summaries {
				if summaries[i].Slug == registered.ID {
					summary = &summaries[i]
				}
			}
			if summary == nil || summary.Engine != "codex" || summary.Path != project || summary.Name != "Catalog Movie" {
				t.Fatalf("missing catalog identity/engine: %#v", summary)
			}
			if withProjects && len(summaries) != 2 {
				t.Fatalf("workspace and catalog projects were not merged: %#v", summaries)
			}

			details, err := GetProjectDetails(opts, registered.ID)
			if err != nil {
				t.Fatal(err)
			}
			if details.Engine != "codex" || details.Path != project || details.Brief != files["brief.md"] || len(details.Beats) != 1 {
				t.Fatalf("catalog details = %#v", details)
			}
			if len(details.Narration) != 1 || len(details.QAFrames) != 1 || len(details.ReviewFrames) != 1 {
				t.Fatalf("missing catalog media: %#v", details)
			}
			urls := map[string]string{
				details.BriefURL:            files["brief.md"],
				details.ScriptURL:           files["artifacts/script.json"],
				details.CompositionURL:      files["artifacts/edit.json"],
				details.Narration[0].URL:    "audio",
				details.QAFrames[0].URL:     "image",
				details.ReviewFrames[0].URL: "review image",
				details.ThumbnailURL:        "review image",
				details.VideoURL:            "video",
			}
			for mediaURL, content := range urls {
				if !strings.HasPrefix(mediaURL, "/api/media/catalog/"+registered.ID+"/") {
					t.Fatalf("media URL lacks catalog ID: %q", mediaURL)
				}
				media, err := serveMediaURL(t, opts, mediaURL)
				if err != nil {
					t.Fatalf("serve %s: %v", mediaURL, err)
				}
				if got := mustReadTestFile(t, media.Path); got != content {
					t.Errorf("serve %s = %q, want %q", mediaURL, got, content)
				}
			}
			if summary.VideoURL != details.VideoURL || summary.ThumbnailURL != details.ThumbnailURL {
				t.Fatal("summary and details media URLs differ")
			}
			// Folder-name aliases resolve to the catalog identity.
			alias, err := GetProjectDetails(opts, filepath.Base(project))
			if err != nil || alias.VideoURL != details.VideoURL || alias.Slug != registered.ID {
				t.Fatalf("catalog alias details = %#v, %v", alias, err)
			}

			mustWriteTestFile(t, filepath.Join(project, "private.env"), "not media")
			if _, err := ResolveMediaRef(opts, "catalog/"+registered.ID+"/private.env"); !errors.Is(err, ErrUnsupportedMedia) {
				t.Errorf("private file was not refused: %v", err)
			}
			if _, err := serveMediaURL(t, opts, "/api/media/catalog/"+registered.ID+"/%2e%2e/private.txt"); !errors.Is(err, ErrOutsideProject) {
				t.Errorf("encoded traversal was not refused: %v", err)
			}
			if _, err := GetProjectDetails(opts, "../external-folder"); !errors.Is(err, ErrNotFound) {
				t.Errorf("traversing slug was not refused: %v", err)
			}
			if !withProjects {
				if _, err := os.Stat(filepath.Join(root, "projects")); !os.IsNotExist(err) {
					t.Fatalf("catalog access must not create root/projects: %v", err)
				}
			}
		})
	}
}

func TestProjectEngineFromLock(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "local")
	mustWriteTestFile(t, filepath.Join(project, "facet.lock.json"), `{"engine":"opencode"}`)
	opts := catalogOptions(t)
	opts.Workspace = root

	list, err := ListProjects(opts)
	if err != nil || len(list) != 1 || list[0].Engine != "opencode" {
		t.Fatalf("local engine summary = %#v, %v", list, err)
	}
	details, err := GetProjectDetails(opts, "local")
	if err != nil || details.Engine != "opencode" {
		t.Fatalf("local engine details = %#v, %v", details, err)
	}
	if _, err := RegisterProject(opts, "External Lock", project, "", nil); err != nil {
		t.Fatal(err)
	}
	details, err = GetProjectDetails(opts, "external-lock")
	if err != nil || details.Engine != "opencode" {
		t.Fatalf("catalog lock fallback = %#v, %v", details, err)
	}
}

func TestCatalogProjectRejectsEscapedSymlinks(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	mustWriteTestFile(t, filepath.Join(outside, "secret.txt"), "outside")
	if !makeTestSymlink(t, outside, filepath.Join(project, "escaped")) {
		return
	}
	opts := catalogOptions(t)
	registered, err := RegisterProject(opts, "External", project, "codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	if media, err := ResolveMediaRef(opts, "catalog/"+registered.ID+"/escaped/secret.txt"); !errors.Is(err, ErrOutsideProject) {
		t.Fatalf("escaped catalog media = %#v, %v; want ErrOutsideProject", media, err)
	}
}

func TestCatalogMediaDistinguishesMatchingFolderNames(t *testing.T) {
	opts := catalogOptions(t)
	for _, name := range []string{"first", "second"} {
		project := filepath.Join(t.TempDir(), "same-folder")
		mustWriteTestFile(t, filepath.Join(project, "renders", "edit.mp4"), name)
		if _, err := RegisterProject(opts, name, project, "codex", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"first", "second"} {
		details, err := GetProjectDetails(opts, name)
		if err != nil {
			t.Fatal(err)
		}
		if details.Stages.Master || details.VideoURL != "" || details.PreviewVideoURL == "" {
			t.Fatalf("preview promoted to master: %#v", details)
		}
		media, err := serveMediaURL(t, opts, details.PreviewVideoURL)
		if err != nil || mustReadTestFile(t, media.Path) != name {
			t.Fatalf("preview for %s = %#v, %v", name, media, err)
		}
	}
}

func TestCatalogIDsAreUniqueAndSafe(t *testing.T) {
	opts := catalogOptions(t)
	first := filepath.Join(t.TempDir(), "Film (2026)")
	second := filepath.Join(t.TempDir(), "Film (2026)")
	percent := filepath.Join(t.TempDir(), "100%")
	for _, dir := range []string{first, second, percent} {
		mustWriteTestFile(t, filepath.Join(dir, "brief.md"), "# "+filepath.Base(dir)+"\n")
	}
	var ids []string
	for _, dir := range []string{first, second, percent} {
		project, err := OpenProject(opts, dir, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, project.ID)
	}
	if !reflect.DeepEqual(ids, []string{"film-2026", "film-2026-2", "100"}) {
		t.Fatalf("catalog IDs = %v", ids)
	}
	details, err := GetProjectDetails(opts, "film-2026-2")
	if err != nil || details.Path != second {
		t.Fatalf("second project details = %#v, %v", details, err)
	}
	projects, err := ListProjects(opts)
	if err != nil || len(projects) != 3 {
		t.Fatalf("every opened project must be listed: %#v, %v", projects, err)
	}
}

func TestCreateProjectReplacesOnlyStaleCatalogIDs(t *testing.T) {
	opts := catalogOptions(t)
	firstParent, secondParent := t.TempDir(), t.TempDir()
	first, err := CreateProject(opts, NewProject{Name: "Demo", Dir: firstParent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(opts, NewProject{Name: "Demo", Dir: secondParent}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a live catalog ID was taken over: %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondParent, "demo")); !os.IsNotExist(err) {
		t.Fatalf("a refused creation left a directory: %v", err)
	}

	if err := os.Remove(first.Path); err != nil {
		t.Fatal(err)
	}
	second, err := CreateProject(opts, NewProject{Name: "Demo", Dir: secondParent})
	if err != nil {
		t.Fatalf("a stale catalog ID blocked creation: %v", err)
	}
	cat, err := LoadCatalog(opts)
	if err != nil || len(cat.Projects) != 1 || cat.Projects[0].ID != "demo" || cat.Projects[0].Path != second.Path {
		t.Fatalf("catalog after replacing a stale entry = %#v, %v", cat, err)
	}
}

// RegisterProject returns an error when the save fails: the save is the only
// thing that makes the registration outlive the process, and a project
// reported as registered but absent from disk would vanish on restart.
func TestCatalogSaveFailureIsReported(t *testing.T) {
	root := t.TempDir()
	// A file where the catalog's parent directory must be makes the save
	// fail for a real reason rather than a simulated one.
	opts := Options{CatalogPath: filepath.Join(root, "state", "catalog.json")}
	if err := os.WriteFile(filepath.Join(root, "state"), []byte("not a directory"), 0o644); err != nil {
		t.Skipf("could not stage an unwritable catalog path: %v", err)
	}

	proj := filepath.Join(root, "demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := RegisterProject(opts, "demo", proj, "claude", nil)
	if err == nil {
		t.Fatalf("a project was registered with an unwritable catalog and reported success (got %+v); "+
			"it would vanish on restart", got)
	}
	if !strings.Contains(err.Error(), "catalog could not be saved") {
		t.Errorf("error does not say the catalog failed to save: %v", err)
	}
	// The registration happened in memory, so returning the project alongside
	// the error is honest: the caller sees what did not persist.
	if got == nil {
		t.Error("no project returned; the caller cannot tell what failed to persist")
	}
}

// The success path must stay silent.
func TestCatalogSaveSucceedsNormally(t *testing.T) {
	root := t.TempDir()
	opts := Options{CatalogPath: filepath.Join(root, "state", "catalog.json")}
	proj := filepath.Join(root, "demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterProject(opts, "demo", proj, "claude", nil); err != nil {
		t.Errorf("a normal registration failed: %v", err)
	}
	if _, err := os.Stat(opts.CatalogPath); err != nil {
		t.Errorf("catalog was not written: %v", err)
	}
}

func TestCreateProjectRemovesItsDirectoryWhenTheCatalogCannotBeSaved(t *testing.T) {
	root := t.TempDir()
	opts := Options{
		CatalogPath:     filepath.Join(root, "state", "catalog.json"),
		ProductionsRoot: filepath.Join(root, "productions"),
	}
	if err := os.WriteFile(filepath.Join(root, "state"), []byte("not a directory"), 0o644); err != nil {
		t.Skipf("could not stage an unwritable catalog path: %v", err)
	}
	project, err := CreateProject(opts, NewProject{Name: "Demo"})
	if err == nil || !strings.Contains(err.Error(), "catalog could not be saved") || project != nil {
		t.Fatalf("CreateProject with an unwritable catalog = %#v, %v", project, err)
	}
	if _, err := os.Stat(filepath.Join(opts.ProductionsRoot, "demo")); !os.IsNotExist(err) {
		t.Fatalf("the unregistered project directory was left behind: %v", err)
	}
}
