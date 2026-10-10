package journeys

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMediaEnforcesExtensionAllowlist(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"projects/demo/.env":                 "TEST_ONLY_ENV_VALUE",
		"projects/demo/source.go":            "package denied",
		"projects/demo/extensionless":        "extensionless denied",
		"projects/demo/archive.bin":          "unknown denied",
		"projects/demo/artifacts/brief.json": `{"title":"Allowed artifact"}`,
		"projects/demo/artifacts/script.md":  "# Allowed artifact\n",
		"projects/demo/assets/frame.png":     "png-data",
		"projects/demo/renders/final.mp4":    "mp4-data",
	}
	for path, contents := range files {
		mustWriteTestFile(t, filepath.Join(root, filepath.FromSlash(path)), contents)
	}
	opts := Options{Workspace: root}

	for _, ref := range []string{
		"projects/demo/.env",
		"projects/demo/source.go",
		"projects/demo/extensionless",
		"projects/demo/archive.bin",
	} {
		t.Run("deny_"+strings.ReplaceAll(filepath.Base(ref), ".", "_"), func(t *testing.T) {
			media, err := ResolveMediaRef(opts, ref)
			if !errors.Is(err, ErrUnsupportedMedia) {
				t.Fatalf("ResolveMediaRef(%s) = %#v, %v; want ErrUnsupportedMedia", ref, media, err)
			}
			if media.Path != "" || media.ContentType != "" {
				t.Fatalf("denied file %s was described: %#v", ref, media)
			}
		})
	}

	allowed := map[string]string{
		"projects/demo/artifacts/brief.json": "application/json",
		"projects/demo/artifacts/script.md":  "text/markdown; charset=utf-8",
		"projects/demo/assets/frame.png":     "image/png",
		"projects/demo/renders/final.mp4":    "video/mp4",
	}
	for ref, wantContentType := range allowed {
		t.Run("allow_"+strings.ReplaceAll(filepath.Base(ref), ".", "_"), func(t *testing.T) {
			media, err := ResolveMediaRef(opts, ref)
			if err != nil {
				t.Fatalf("ResolveMediaRef(%s): %v", ref, err)
			}
			if media.ContentType != wantContentType {
				t.Fatalf("%s content type = %q, want %q", ref, media.ContentType, wantContentType)
			}
			if got := mustReadTestFile(t, media.Path); got != files[ref] || media.Size != int64(len(files[ref])) {
				t.Fatalf("%s resolved to %q (%d bytes)", ref, got, media.Size)
			}
			if want := strings.TrimPrefix(ref, "projects/demo/"); media.Name != want {
				t.Fatalf("%s name = %q, want %q", ref, media.Name, want)
			}
			if media.Project != mustCanonical(t, filepath.Join(root, "projects", "demo")) {
				t.Fatalf("%s project = %q", ref, media.Project)
			}
		})
	}
}

func TestZeroByteVideoIsNotServed(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "projects", "empty-video", "renders", "final.mp4"), "")
	if _, err := ResolveMediaRef(Options{Workspace: root}, "projects/empty-video/renders/final.mp4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("zero-byte video = %v, want ErrNotFound", err)
	}
}

func TestMediaOutsideProjectsRejected(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "projects", "demo", "asset.txt"), "inside")
	mustWriteTestFile(t, filepath.Join(root, "outside.txt"), "outside")
	mustWriteTestFile(t, filepath.Join(root, "web", "index.html"), "<html>")
	opts := Options{Workspace: root}

	if _, err := ResolveMediaRef(opts, "projects/demo/asset.txt"); err != nil {
		t.Fatalf("project media was refused: %v", err)
	}
	for _, ref := range []string{"outside.txt", "web/index.html", "projects/../outside.txt", "projects/demo/../../outside.txt"} {
		if media, err := ResolveMediaRef(opts, ref); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("outside media %q = %#v, %v; want ErrOutsideProject", ref, media, err)
		}
	}
}

func TestMediaProjectsDirectoryItselfIsNotServed(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "projects", "demo", "frames.png", "frame.png"), "image")
	opts := Options{Workspace: root}
	for _, ref := range []string{"projects", "projects/demo", "projects/demo/frames.png"} {
		if media, err := ResolveMediaRef(opts, ref); err == nil {
			t.Fatalf("directory %q was served: %#v", ref, media)
		}
	}
	if _, err := ResolveMediaRef(opts, "projects/demo/frames.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a directory with a media name = %v, want ErrNotFound", err)
	}
}

func TestResolveMediaRejectsPathEscapes(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	mustWriteTestFile(t, filepath.Join(project, "renders", "frame.png"), "inside")
	mustWriteTestFile(t, filepath.Join(parent, "outside.png"), "outside")
	other := filepath.Join(t.TempDir(), "other.png")
	mustWriteTestFile(t, other, "other")

	for _, name := range []string{
		"../outside.png",
		"renders/../../outside.png",
		"renders/../frame.png",
		`..\outside.png`,
		`renders\..\..\outside.png`,
		filepath.Join(parent, "outside.png"),
		other,
		filepath.Join(project, "..", "outside.png"),
		"/outside.png",
		"C:/Windows/win.ini",
		`C:\outside.png`,
		"c:outside.png",
		"D:frame.png",
		"renders/frame.png\x00.png",
	} {
		t.Run(strings.NewReplacer("/", "_", `\`, "_", ":", "_", "\x00", "_").Replace(name), func(t *testing.T) {
			if media, err := ResolveMedia(project, name); !errors.Is(err, ErrOutsideProject) {
				t.Fatalf("ResolveMedia(%q) = %#v, %v; want ErrOutsideProject", name, media, err)
			}
		})
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{`\\server\share\outside.png`, `\outside.png`, "renders/frame.png:stream.png", "renders/NUL", "CON"} {
			if media, err := ResolveMedia(project, name); !errors.Is(err, ErrOutsideProject) {
				t.Fatalf("ResolveMedia(%q) = %#v, %v; want ErrOutsideProject", name, media, err)
			}
		}
	}

	want := mustCanonical(t, filepath.Join(project, "renders", "frame.png"))
	for _, name := range []string{"renders/frame.png", "./renders/frame.png", filepath.Join("renders", "frame.png"), filepath.Join(project, "renders", "frame.png")} {
		media, err := ResolveMedia(project, name)
		if err != nil {
			t.Fatalf("ResolveMedia(%q): %v", name, err)
		}
		if media.Path != want || media.Name != "renders/frame.png" {
			t.Fatalf("ResolveMedia(%q) = %#v", name, media)
		}
	}
	for _, name := range []string{"", " ", ".", "renders/.."} {
		if _, err := ResolveMedia(project, name); err == nil {
			t.Fatalf("ResolveMedia(%q) accepted a non-file", name)
		}
	}
	if _, err := ResolveMedia(project, "renders/missing.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing media = %v, want ErrNotFound", err)
	}
	if _, err := ResolveMedia(filepath.Join(parent, "missing-project"), "renders/frame.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project = %v, want ErrNotFound", err)
	}
}

func TestResolveMediaRejectsLinksLeavingTheProject(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	mustWriteTestFile(t, filepath.Join(outside, "secret.png"), "outside")
	mustWriteTestFile(t, filepath.Join(project, "renders", "inside.png"), "inside")
	if !makeTestSymlink(t, filepath.Join(outside, "secret.png"), filepath.Join(project, "renders", "leak.png")) ||
		!makeTestSymlink(t, outside, filepath.Join(project, "assets")) ||
		!makeTestSymlink(t, filepath.Join(project, "renders", "inside.png"), filepath.Join(project, "renders", "alias.png")) {
		return
	}
	for _, name := range []string{"renders/leak.png", "assets/secret.png"} {
		if media, err := ResolveMedia(project, name); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("ResolveMedia(%q) = %#v, %v; want ErrOutsideProject", name, media, err)
		}
	}
	// A link that stays inside the project is followed to its target.
	media, err := ResolveMedia(project, "renders/alias.png")
	if err != nil || media.Path != mustCanonical(t, filepath.Join(project, "renders", "inside.png")) || media.Name != "renders/alias.png" {
		t.Fatalf("inside link = %#v, %v", media, err)
	}
}

func TestResolveMediaRefRejectsMalformedReferences(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "projects", "demo", "renders", "frame.png"), "image")
	opts := catalogOptions(t)
	opts.Workspace = root
	if _, err := ResolveMediaRef(opts, "projects/demo/renders/frame.png"); err != nil {
		t.Fatalf("valid reference refused: %v", err)
	}
	for _, ref := range []string{
		"",
		"/projects/demo/renders/frame.png",
		"projects//demo/renders/frame.png",
		"projects/demo//renders/frame.png",
		"projects/demo/renders/frame.png/",
		"projects/./demo/renders/frame.png",
		"projects/demo/../demo/renders/frame.png",
		`projects\demo\renders\frame.png`,
		"projects/demo",
		"other/demo/renders/frame.png",
		"projects/%2e%2e/demo/renders/frame.png",
		"projects/missing/renders/frame.png",
		"catalog/unknown/renders/frame.png",
		"C:/projects/demo/renders/frame.png",
		"projects/demo/renders/frame.png\x00",
	} {
		if media, err := ResolveMediaRef(opts, ref); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("ResolveMediaRef(%q) = %#v, %v; want ErrOutsideProject", ref, media, err)
		}
	}

	for _, ref := range []string{"projects/demo/renders/frame.png", "catalog/demo/renders/frame.png"} {
		if _, err := ResolveMediaRef(Options{}, ref); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("ResolveMediaRef(%q) without workspace or catalog = %v", ref, err)
		}
	}
}
