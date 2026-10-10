package journeys

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestSafeMediaName(t *testing.T) {
	for name, want := range map[string]string{
		"clip.mp4":                        "clip.mp4",
		"My Clip (final).MP4":             "My-Clip-final.mp4",
		"Interview – take #1.MOV":         "Interview-take-1.mov",
		"clip.v2.mp4":                     "clip-v2.mp4",
		"../../etc/evil.mov":              "evil.mov",
		`..\..\Windows\evil.wav`:          "evil.wav",
		"C:/Users/user/voice.m4a":         "voice.m4a",
		".hidden.png":                     "hidden.png",
		"clip:stream.mp4":                 "clip-stream.mp4",
		"a\x00b\nc.webm":                  "a-b-c.webm",
		"café crème.jpg":                  "café-crème.jpg",
		"__under_score__.ogg":             "__under_score__.ogg",
		".mp4":                            "media.mp4",
		"  -- .jpeg":                      "media.jpeg",
		"CON.mp4":                         "CON-file.mp4",
		"nul.wav":                         "nul-file.wav",
		"Lpt9.png":                        "Lpt9-file.png",
		"COM10.mp4":                       "COM10.mp4",
		strings.Repeat("a", 300) + ".aac": strings.Repeat("a", maxStem) + ".aac",
	} {
		got, err := SafeMediaName(name)
		if err != nil || got != want {
			t.Errorf("SafeMediaName(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"", "noext", "notes.txt", "page.html", "image.svg", "tool.exe", "clip.mp4.exe", "archive.mp4.zip", "folder/", "clip.mp4 x", "\xff.mp4"} {
		if got, err := SafeMediaName(name); err == nil {
			t.Errorf("SafeMediaName(%q) = %q; want a refusal", name, got)
		}
	}
	if _, err := SafeMediaName("notes.txt"); !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("an unsupported type = %v, want ErrUnsupportedMedia", err)
	}
}

// leftovers lists the import's temporary files still in dir.
func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func TestImportMediaStoresOnlyInsideAssets(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "demo")
	mustWriteTestFile(t, filepath.Join(project, "brief.md"), "# Demo\n")

	imported, err := ImportMedia(project, ImportRequest{Name: "../../escape me.MP4"}, strings.NewReader("video bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if imported.Name != "assets/escape-me.mp4" || imported.Size != 11 || imported.SHA256 != sha256Hex("video bytes") || imported.Replaced {
		t.Fatalf("imported = %+v", imported)
	}
	if got := mustReadTestFile(t, filepath.Join(project, "assets", "escape-me.mp4")); got != "video bytes" {
		t.Fatalf("stored %q", got)
	}
	for _, dir := range []string{root, filepath.Join(root, "projects")} {
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Fatalf("the import wrote beside the project in %s: %v", dir, entries)
		}
	}
	if left := leftovers(t, filepath.Join(project, "assets")); len(left) != 0 {
		t.Fatalf("temporary files were left: %v", left)
	}

	opts := workspaceOptions(root)
	details, err := GetProjectDetails(opts, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Assets) != 1 || details.Assets[0].RelativePath != "projects/demo/assets/escape-me.mp4" {
		t.Fatalf("the imported file is not listed: %#v", details.Assets)
	}
	// Every imported type is listed, the less common audio types included.
	if _, err := ImportMedia(project, ImportRequest{Name: "voice memo.m4a"}, strings.NewReader("audio")); err != nil {
		t.Fatal(err)
	}
	if details, _ = GetProjectDetails(opts, "demo"); len(details.Assets) != 2 {
		t.Fatalf("an imported .m4a is not listed: %#v", details.Assets)
	}
}

func TestImportMediaReplacesOnlyWhenAsked(t *testing.T) {
	project := t.TempDir()
	clip := filepath.Join(project, "assets", "clip.mp4")
	if _, err := ImportMedia(project, ImportRequest{Name: "clip.mp4"}, strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportMedia(project, ImportRequest{Name: "clip.mp4"}, strings.NewReader("second")); !errors.Is(err, ErrConflict) {
		t.Fatalf("an existing file was not protected: %v", err)
	}
	if got := mustReadTestFile(t, clip); got != "first" {
		t.Fatalf("a refused import changed the file: %q", got)
	}
	for _, want := range []string{"assets/clip-2.mp4", "assets/clip-3.mp4"} {
		kept, err := ImportMedia(project, ImportRequest{Name: "clip.mp4", IfExists: "keep"}, strings.NewReader(want))
		if err != nil || kept.Name != want || kept.Replaced {
			t.Fatalf("keep both = %+v, %v; want %s", kept, err, want)
		}
		if got := mustReadTestFile(t, filepath.Join(project, filepath.FromSlash(want))); got != want {
			t.Fatalf("%s holds %q", want, got)
		}
	}
	replaced, err := ImportMedia(project, ImportRequest{Name: "clip.mp4", IfExists: "replace"}, strings.NewReader("third"))
	if err != nil || replaced.Name != "assets/clip.mp4" || !replaced.Replaced {
		t.Fatalf("replace = %+v, %v", replaced, err)
	}
	if got := mustReadTestFile(t, clip); got != "third" {
		t.Fatalf("replaced file holds %q", got)
	}
	// Replacing a name nothing has yet simply stores the file.
	if fresh, err := ImportMedia(project, ImportRequest{Name: "fresh.png", IfExists: "replace"}, strings.NewReader("png")); err != nil || fresh.Replaced {
		t.Fatalf("replace without an existing file = %+v, %v", fresh, err)
	}
	// A directory is never replaced.
	if err := os.Mkdir(filepath.Join(project, "assets", "folder.mp4"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportMedia(project, ImportRequest{Name: "folder.mp4", IfExists: "replace"}, strings.NewReader("x")); !errors.Is(err, ErrConflict) {
		t.Fatalf("a directory was replaced: %v", err)
	}
	if _, err := ImportMedia(project, ImportRequest{Name: "clip.mp4", IfExists: "overwrite"}, strings.NewReader("x")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("an unknown choice was accepted: %v", err)
	}
	if left := leftovers(t, filepath.Join(project, "assets")); len(left) != 0 {
		t.Fatalf("temporary files were left: %v", left)
	}
}

func TestImportMediaLimitsAndRefusals(t *testing.T) {
	project := t.TempDir()
	assets := filepath.Join(project, "assets")
	if _, err := ImportMedia(project, ImportRequest{Name: "big.mp4", MaxBytes: 4}, strings.NewReader("12345")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a file over the limit = %v, want ErrTooLarge", err)
	}
	if exact, err := ImportMedia(project, ImportRequest{Name: "exact.mp4", MaxBytes: 4}, strings.NewReader("1234")); err != nil || exact.Size != 4 {
		t.Fatalf("a file at the limit = %+v, %v", exact, err)
	}
	if _, err := ImportMedia(project, ImportRequest{Name: "empty.mp4"}, strings.NewReader("")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("an empty file = %v, want ErrInvalidRequest", err)
	}
	if _, err := ImportMedia(project, ImportRequest{Name: "cut.mp4"}, io.MultiReader(strings.NewReader("part"), iotest.ErrReader(errors.New("connection lost")))); err == nil {
		t.Fatal("a file that did not arrive whole was stored")
	}
	if _, err := ImportMedia(project, ImportRequest{Name: "run.exe"}, strings.NewReader("MZ")); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("an executable = %v, want ErrUnsupportedMedia", err)
	}
	if _, err := ImportMedia(project, ImportRequest{Name: "x.mp4", MaxBytes: -1}, strings.NewReader("x")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a negative limit = %v", err)
	}
	if _, err := ImportMedia(filepath.Join(project, "missing"), ImportRequest{Name: "x.mp4"}, strings.NewReader("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing project = %v, want ErrNotFound", err)
	}
	entries, err := os.ReadDir(assets)
	if err != nil || len(entries) != 1 || entries[0].Name() != "exact.mp4" {
		t.Fatalf("refused imports left files: %v, %v", entries, err)
	}

	file := t.TempDir()
	mustWriteTestFile(t, filepath.Join(file, "assets"), "not a folder")
	if _, err := ImportMedia(file, ImportRequest{Name: "x.mp4"}, strings.NewReader("x")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("assets as a file = %v, want ErrInvalidRequest", err)
	}
}

func TestImportMediaRefusesAssetsLeavingTheProject(t *testing.T) {
	outside := t.TempDir()
	t.Run("junction", func(t *testing.T) {
		project := t.TempDir()
		makeTestJunction(t, outside, filepath.Join(project, "assets"))
		if _, err := ImportMedia(project, ImportRequest{Name: "x.mp4"}, strings.NewReader("x")); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("import through a junction = %v, want ErrOutsideProject", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		project := t.TempDir()
		if !makeTestSymlink(t, outside, filepath.Join(project, "assets")) {
			return
		}
		if _, err := ImportMedia(project, ImportRequest{Name: "x.mp4"}, strings.NewReader("x")); !errors.Is(err, ErrOutsideProject) {
			t.Fatalf("import through a link = %v, want ErrOutsideProject", err)
		}
	})
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("an import wrote outside the project: %v, %v", entries, err)
	}
}
