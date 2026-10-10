package components

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	facet "github.com/kayiik/facet"
	"github.com/kayiik/facet/internal/toolbox"
)

// fakeRuntime lays out <runtime>/bin/facet in a temporary folder and makes
// the toolbox resolve its runtime there, with nothing on PATH.
func fakeRuntime(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "bin", "facet")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(toolbox.UseExecutable(exe))
	t.Setenv("PATH", t.TempDir())
	for _, name := range []string{"REMOTION_BROWSER_EXECUTABLE", "PUPPETEER_EXECUTABLE_PATH", "CHROME_PATH", "FACET_REMOTION_COMPOSER", "FACET_HYPERFRAMES"} {
		t.Setenv(name, "")
	}
	return root
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func zipOf(t *testing.T, files map[string]string, modes map[string]fs.FileMode) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		mode := modes[name]
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(files[name]))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serve answers each path with its body.
func serve(t *testing.T, bodies map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func allPins() map[string][]download {
	all := map[string][]download{"voice": voicePins}
	for component, pins := range map[string]map[string][]download{"node": nodePins, "ffmpeg": ffmpegPins, "browser": browserPins} {
		for p, ds := range pins {
			all[component+" "+p] = ds
		}
	}
	return all
}

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Every pin is an https download with a SHA-256 and a name of its own, and
// the downloaded programs cover all six platforms facet is built for.
func TestPinsAreCompleteAndChecked(t *testing.T) {
	names := map[string]string{}
	for owner, ds := range allPins() {
		for _, d := range ds {
			if !strings.HasPrefix(d.URL, "https://") || !hexDigest.MatchString(d.SHA256) || d.Name == "" {
				t.Errorf("%s: pin %+v lacks an https URL, a SHA-256 or a name", owner, d)
			}
			if other, ok := names[d.Name]; ok && other != d.SHA256 {
				t.Errorf("%s: two pins share the name %s", owner, d.Name)
			}
			names[d.Name] = d.SHA256
		}
	}
	for _, p := range []string{"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		for component, pins := range map[string]map[string][]download{"node": nodePins, "ffmpeg": ffmpegPins, "browser": browserPins} {
			if len(pins[p]) == 0 {
				t.Errorf("no %s pin for %s", component, p)
			}
		}
		for _, d := range ffmpegPins[p] {
			for _, dest := range d.Files {
				if !strings.HasPrefix(dest, "bin/") {
					t.Errorf("ffmpeg %s puts %s outside bin/", p, dest)
				}
			}
		}
	}
}

// The HyperFrames files pin the version Facet reports, in the package and
// its lockfile alike.
func TestHyperFramesPinMatchesItsFiles(t *testing.T) {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
		AllowScripts map[string]bool   `json:"allowScripts"`
	}
	data, _ := hyperframesFiles.ReadFile("hyperframes/package.json")
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Dependencies["hyperframes"] != hyperframesVersion {
		t.Fatalf("package.json pins hyperframes %q (%v), want %s", pkg.Dependencies["hyperframes"], err, hyperframesVersion)
	}
	var lock struct {
		Packages map[string]struct {
			Version          string `json:"version"`
			HasInstallScript bool   `json:"hasInstallScript"`
		} `json:"packages"`
	}
	data, _ = hyperframesFiles.ReadFile("hyperframes/package-lock.json")
	if err := json.Unmarshal(data, &lock); err != nil || lock.Packages["node_modules/hyperframes"].Version != hyperframesVersion {
		t.Fatalf("the lockfile does not pin hyperframes %s: %v", hyperframesVersion, err)
	}
	// Every package with an install script is approved by name and version,
	// so a future npm that blocks unreviewed scripts installs the same tree.
	for name, p := range lock.Packages {
		if p.HasInstallScript {
			key := path.Base(name)
			if scope := path.Base(path.Dir(name)); strings.HasPrefix(scope, "@") {
				key = scope + "/" + key
			}
			if !pkg.AllowScripts[key+"@"+p.Version] {
				t.Errorf("%s@%s runs an install script that allowScripts does not approve", key, p.Version)
			}
		}
	}
}

// The composer facet writes is exactly what composer-manifest.json allows,
// with its package files.
func TestEmbeddedComposerIsTheAllowlist(t *testing.T) {
	var manifest struct {
		AllowedSourcePaths []string `json:"allowedSourcePaths"`
	}
	data, err := composerFile("composer-manifest.json")
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatalf("composer-manifest.json: %v", err)
	}
	want := append([]string{"composer-manifest.json", "package-lock.json", "package.json", "tsconfig.json"}, manifest.AllowedSourcePaths...)
	var got []string
	fs.WalkDir(facet.Composer, "remotion-composer", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			got = append(got, strings.TrimPrefix(name, "remotion-composer/"))
		}
		return err
	})
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("embedded composer:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if remotionVersion() == "" {
		t.Fatal("the composer names no remotion version")
	}
}

func TestFetchChecksTheDigest(t *testing.T) {
	body := []byte("the pinned bytes")
	srv := serve(t, map[string][]byte{"/file.zip": body})
	dir := t.TempDir()
	good := download{Name: "file.zip", URL: srv.URL + "/file.zip", SHA256: sha(body)}
	if got, err := fetch(context.Background(), good, dir, Options{}); err != nil || filepath.Base(got) != "file.zip" {
		t.Fatalf("fetch = %q, %v", got, err)
	}
	bad := good
	bad.SHA256 = sha([]byte("other bytes"))
	os.Remove(filepath.Join(dir, "file.zip"))
	_, err := fetch(context.Background(), bad, dir, Options{})
	var mismatch *checksumError
	if !errors.As(err, &mismatch) {
		t.Fatalf("a wrong digest was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "file.zip")); err == nil {
		t.Fatal("a download that failed its checksum was kept")
	}
}

func TestFetchFromAFolder(t *testing.T) {
	body := []byte("offline bytes")
	from := t.TempDir()
	os.WriteFile(filepath.Join(from, "pinned.zip"), body, 0o644)
	d := download{Name: "pinned.zip", URL: "https://example.invalid/pinned.zip", SHA256: sha(body)}
	if _, err := fetch(context.Background(), d, t.TempDir(), Options{From: from, Offline: true}); err != nil {
		t.Fatalf("a matching file in --from was refused: %v", err)
	}
	os.WriteFile(filepath.Join(from, "pinned.zip"), []byte("tampered"), 0o644)
	if _, err := fetch(context.Background(), d, t.TempDir(), Options{From: from, Offline: true}); err == nil {
		t.Fatal("a tampered file in --from was accepted")
	}
	missing := d
	missing.Name = "absent.zip"
	if _, err := fetch(context.Background(), missing, t.TempDir(), Options{From: from, Offline: true}); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("an offline install downloaded or failed unclearly: %v", err)
	}
}

func TestUnzipStripsMapsAndStaysInside(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "a.zip")
	os.WriteFile(archive, zipOf(t, map[string]string{
		"top/bin/tool":     "program",
		"top/doc/readme":   "text",
		"top/../../escape": "outside",
	}, map[string]fs.FileMode{"top/bin/tool": 0o755}), 0o644)
	dir := t.TempDir()
	if err := unpack(archive, dir, download{Name: "a.zip", Strip: 1}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bin/tool", "doc/readme"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Fatal("an archive path left the folder")
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(dir, "bin", "tool")); info == nil || info.Mode()&0o111 == 0 {
			t.Error("the program lost its execute bit")
		}
	}
	mapped := t.TempDir()
	if err := unpack(archive, mapped, download{Name: "a.zip", Strip: 1, Files: map[string]string{"bin/tool": "bin/renamed"}}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(mapped, "bin"))
	if len(entries) != 1 || entries[0].Name() != "renamed" {
		t.Fatalf("Files took %v", entries)
	}
	if _, err := os.Stat(filepath.Join(mapped, "doc")); err == nil {
		t.Fatal("Files took a path it does not name")
	}
	if err := unpack(archive, t.TempDir(), download{Name: "a.zip", Strip: 1, Files: map[string]string{"bin/absent": "bin/absent"}}); err == nil {
		t.Fatal("an archive without a named file unpacked")
	}
}

func TestUntarKeepsLinksInside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("archives with links are unpacked on Linux and macOS only")
	}
	build := func(link string) string {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "node/lib/cli.js", Mode: 0o755, Size: 2, Typeflag: tar.TypeReg})
		tw.Write([]byte("js"))
		tw.WriteHeader(&tar.Header{Name: "node/bin/npm", Linkname: link, Typeflag: tar.TypeSymlink})
		tw.Close()
		gz.Close()
		file := filepath.Join(t.TempDir(), "n.tar.gz")
		os.WriteFile(file, buf.Bytes(), 0o644)
		return file
	}
	dir := t.TempDir()
	if err := unpack(build("../lib/cli.js"), dir, download{Name: "n.tar.gz", Strip: 1}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "bin", "npm")); err != nil || string(data) != "js" {
		t.Fatalf("the link does not lead to its target: %q, %v", data, err)
	}
	for _, link := range []string{"../../../etc/passwd", "/etc/passwd"} {
		if err := unpack(build(link), t.TempDir(), download{Name: "n.tar.gz", Strip: 1}); err == nil {
			t.Errorf("a link to %s was unpacked", link)
		}
	}
}

// facet install fetches, checks, unpacks and records a component, which
// Facet's tools then find; facet remove takes it away again.
func TestInstallAndRemoveTheBrowser(t *testing.T) {
	root := fakeRuntime(t)
	exe := "chrome-headless-shell"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	archive := zipOf(t, map[string]string{"chrome-headless-shell-test/" + exe: "browser", "chrome-headless-shell-test/lib.so": "lib"},
		map[string]fs.FileMode{"chrome-headless-shell-test/" + exe: 0o755})
	srv := serve(t, map[string][]byte{"/browser.zip": archive})
	saved := browserPins[platform]
	browserPins[platform] = []download{{Name: "browser-test.zip", URL: srv.URL + "/browser.zip", SHA256: sha(archive), Strip: 1}}
	t.Cleanup(func() { browserPins[platform] = saved })

	c, _ := Lookup("browser")
	if s := StatusOf(c); s.State == Installed {
		t.Fatalf("browser is installed before install: %+v", s)
	}
	// Forced: a desktop Chrome or Edge on the computer running the test
	// would otherwise count as the browser already being there.
	var log bytes.Buffer
	if err := Install(context.Background(), []string{"browser"}, Options{Force: true, Log: &log}); err != nil {
		t.Fatalf("install: %v\n%s", err, log.String())
	}
	s := StatusOf(c)
	want := filepath.Join(root, "dependencies", "browser", exe)
	if s.State != Installed || !sameFile(s.Path, want) || s.Found != c.Version {
		t.Fatalf("after install: %+v", s)
	}
	if !sameFile(toolbox.BrowserExecutable(), want) {
		t.Fatalf("a Remotion render would use %q", toolbox.BrowserExecutable())
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, "dependencies", ".install-*"))
	if len(leftovers) != 0 {
		t.Fatalf("install left %v", leftovers)
	}
	// A second install does nothing; a forced one replaces it in place.
	if err := Install(context.Background(), []string{"browser"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), []string{"browser"}, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if !sameFile(StatusOf(c).Path, want) {
		t.Fatal("a forced reinstall lost the browser")
	}
	if err := Remove([]string{"browser"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if s := StatusOf(c); s.State == Installed {
		t.Fatalf("after remove: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(root, "dependencies", "browser")); err == nil {
		t.Fatal("remove left the folder")
	}
}

func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// What facet install did not make, it neither replaces nor removes.
func TestInstallLeavesForeignFoldersAlone(t *testing.T) {
	root := fakeRuntime(t)
	foreign := filepath.Join(root, "dependencies", "browser")
	os.MkdirAll(foreign, 0o755)
	os.WriteFile(filepath.Join(foreign, "mine.txt"), []byte("the person's"), 0o644)
	if err := Remove([]string{"browser"}, Options{}); err == nil {
		t.Fatal("remove deleted a folder facet install did not make")
	}
	if err := replaceable(foreign, false); err == nil {
		t.Fatal("install would replace a folder facet install did not make")
	}
	if err := replaceable(foreign, true); err != nil {
		t.Fatalf("--force cannot replace it: %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "mine.txt")); err != nil {
		t.Fatal("the person's file is gone")
	}
}

// Installing a component installs the missing ones it runs on first.
func TestPlanAddsMissingNeedsInOrder(t *testing.T) {
	fakeRuntime(t)
	plan, err := Plan([]string{"remotion"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range plan {
		ids = append(ids, s.ID)
	}
	if len(ids) == 0 || ids[len(ids)-1] != "remotion" || !strings.Contains(strings.Join(ids, " "), "node") {
		t.Fatalf("plan for remotion = %v; want node first and remotion last", ids)
	}
	if _, err := Plan([]string{"nonsense"}, false); err == nil {
		t.Fatal("an unknown component was planned")
	}
}

func TestDependenciesNameTheirComponents(t *testing.T) {
	for dep, want := range map[string]string{"ffprobe": "ffmpeg", "node": "node", "remotion-composer": "remotion", "hyperframes": "hyperframes", "piper": "piper", "OPENAI_API_KEY": "", "gflow": ""} {
		if got := ForDependency(dep); got != want {
			t.Errorf("ForDependency(%q) = %q, want %q", dep, got, want)
		}
	}
	if got := strings.Join(Minimal([]string{"node", "ffmpeg", "remotion", "node"}), " "); got != "ffmpeg remotion" {
		t.Errorf("Minimal = %q", got)
	}
}

func TestFlagsMayFollowTheComponents(t *testing.T) {
	got := strings.Join(flagsFirst([]string{"browser", "--from", "C:/pins", "node", "--force", "--from=x", "--", "--odd"}, "from"), " ")
	if got != "--from C:/pins --force --from=x browser node --odd" {
		t.Fatalf("flagsFirst = %q", got)
	}
}

func TestCLI(t *testing.T) {
	fakeRuntime(t)
	var out, errOut bytes.Buffer
	if code := CLI([]string{"components", "--json"}, nil, &out, &errOut, "test"); code != 0 {
		t.Fatalf("components --json exited %d: %s", code, errOut.String())
	}
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Components []Status `json:"components"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil || !env.OK || len(env.Result.Components) != len(All()) {
		t.Fatalf("components --json: %v\n%s", err, out.String())
	}
	out.Reset()
	if code := CLI([]string{"components"}, nil, &out, &errOut, "test"); code != 0 || !strings.Contains(out.String(), "facet install") {
		t.Fatalf("components did not say how to install what is missing:\n%s", out.String())
	}
	errOut.Reset()
	if code := CLI([]string{"install", "nonsense"}, nil, &out, &errOut, "test"); code == 0 || !strings.Contains(errOut.String(), "unknown component") {
		t.Fatalf("install nonsense: %d %s", code, errOut.String())
	}
	errOut.Reset()
	if code := CLI([]string{"setup"}, strings.NewReader(""), &out, &errOut, "test"); code == 0 || !strings.Contains(errOut.String(), "--yes") {
		t.Fatalf("setup without a terminal: %d %s", code, errOut.String())
	}
}
