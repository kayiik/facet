package window

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kayiik/facet/internal/app/journeys"
	"github.com/kayiik/facet/internal/app/toolkit"
)

// fakeToolsList is what `facet tools list` reports in these tests: two
// tools the window may run, a paid tool with its key missing, and a paid
// tool whose key is set but unproven.
const fakeToolsList = `{"ok":true,"operation":"list","result":{"tools":[
 {"name":"media_probe","capability":"media inspection","configured":true,"cost":{"currency":"USD","amount":0,"known":true},
  "dependencies":[{"name":"ffprobe","available":true,"path":"ffprobe","type":"binary","resolution":"satisfied"}],
  "may_charge":false,"network":false,"external_write":false},
 {"name":"output_review","capability":"technical output review","configured":true,"cost":{"currency":"USD","amount":0,"known":true},
  "dependencies":[{"name":"ffmpeg","available":true,"path":"ffmpeg","type":"binary","resolution":"satisfied"}],
  "may_charge":false,"network":false,"external_write":true},
 {"name":"sora_video","capability":"cloud AI video generation via Sora","configured":false,"cost":{"currency":"USD","amount":null,"known":false},
  "dependencies":[{"name":"OPENAI_API_KEY","available":false,"path":"","type":"env","resolution":"unsatisfied"}],
  "may_charge":true,"network":true,"external_write":true},
 {"name":"elevenlabs_tts","capability":"cloud text-to-speech synthesis","configured":true,"cost":{"currency":"USD","amount":null,"known":false},
  "dependencies":[{"name":"ELEVENLABS_API_KEY","available":true,"path":"","type":"env","resolution":"unknown"}],
  "may_charge":true,"network":true,"external_write":true}]},"warnings":[]}`

// paidToolsList declares the tools the window runs as tools that may
// charge, as a changed Toolkit could: the window must then refuse them.
var paidToolsList = strings.NewReplacer(
	`"media inspection","configured":true`, `"media inspection","may_charge":true,"configured":true`,
	`"technical output review","configured":true`, `"technical output review","may_charge":true,"configured":true`,
).Replace(strings.ReplaceAll(fakeToolsList, `"may_charge":false,`, ""))

// fakeFacet answers the Toolkit calls the window makes, the way facet does,
// and records them.
type fakeFacet struct {
	list string

	mu      sync.Mutex
	version string
	calls   []facetCall
}

type facetCall struct {
	dir  string
	args []string
}

func (f *fakeFacet) setVersion(printed string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = printed
}

func (f *fakeFacet) exec(_ context.Context, dir string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, facetCall{dir, args})
	version := f.version
	f.mu.Unlock()
	switch {
	case len(args) == 1 && args[0] == "version":
		return []byte(version), nil
	case len(args) == 2 && args[0] == "tools" && args[1] == "list":
		return []byte(f.list), nil
	case len(args) == 5 && args[0] == "tools" && args[1] == "run" && args[3] == "--input":
		var req map[string]any
		if err := json.Unmarshal([]byte(args[4]), &req); err != nil {
			return nil, err
		}
		result := map[string]any{}
		switch args[2] {
		case "media_probe":
			result = map[string]any{"input": req["input"], "sha256": "0123", "audio_streams": []any{},
				"format": map[string]any{"duration": 2.0, "format_name": "mov,mp4,m4a", "size": 10},
				"video":  map[string]any{"codec": "h264", "width": 320, "height": 240, "fps": 30.0, "pixel_format": "yuv420p"}}
		case "output_review":
			// output_review samples frames into the evidence folder.
			frame := filepath.Join(req["evidence_dir"].(string), "frame-0001.jpg")
			if err := os.WriteFile(frame, []byte("jpeg frame"), 0o644); err != nil {
				return nil, err
			}
			result = map[string]any{"execution_status": "succeeded", "review_status": "pass",
				"gates":        []any{map[string]any{"name": "profile", "status": "pass"}, map[string]any{"name": "duration", "status": "pass"}},
				"samples":      []any{map[string]any{"path": frame, "timestamp": 1.0, "width": 320, "height": 240}},
				"output_facts": map[string]any{"input": req["input"]}}
		default:
			return nil, fmt.Errorf("unexpected tool %s", args[2])
		}
		return json.Marshal(map[string]any{"ok": true, "tool": args[2], "operation": "run", "result": result, "warnings": []string{}})
	}
	return nil, fmt.Errorf("unexpected facet call %v", args)
}

// runs returns the runs of tool facet was asked for, with the folder each
// ran in and its request.
func (f *fakeFacet) runs(tool string) []facetCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []facetCall
	for _, c := range f.calls {
		if len(c.args) > 2 && c.args[1] == "run" && c.args[2] == tool {
			out = append(out, c)
		}
	}
	return out
}

// fixture is a window with the project screens configured: the App's data
// folder holds the agent's workspace with one project, demo, and outside is
// a folder beside it that belongs to no project.
type fixture struct {
	base      string
	facet     *fakeFacet
	data      string
	workspace string
	demo      string
	outside   string
}

const uploadLimit = 64

func startProjects(t *testing.T, list string) *fixture {
	t.Helper()
	data := t.TempDir()
	workspace := filepath.Join(data, "compa", "workspace")
	demo := filepath.Join(workspace, "projects", "demo")
	for name, contents := range map[string]string{
		"brief.md":          "# Demo film\n",
		"script.md":         "# Script\n\n| # | t | Beat | Narration |\n|---|---|---|---|\n| 1 | 0-2 | Opening | Hello. |\n",
		"assets/clip.mp4":   "clip bytes",
		"assets/still.png":  "png bytes",
		"renders/final.mp4": "rendered video",
		"secret.env":        "KEY=not media",
	} {
		writeFile(t, filepath.Join(demo, filepath.FromSlash(name)), contents)
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.png"), "outside every project")

	facet := &fakeFacet{list: list, version: "facet v0.0.1-test\n"}
	tk := &toolkit.Runner{Exec: facet.exec}
	previous := journeys.Toolkit
	journeys.Toolkit = tk
	t.Cleanup(func() { journeys.Toolkit = previous })

	s := New(&fakeHost{k: &fakeKernel{}}, tk, testToken)
	s.Projects = Projects{
		Options: journeys.Options{
			Workspace:       workspace,
			CatalogPath:     filepath.Join(data, "catalog.json"),
			ProductionsRoot: filepath.Join(workspace, "projects"),
		},
		DataDir:   data,
		MaxUpload: uploadLimit,
	}
	addr, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Serve(ctx)
	return &fixture{base: "http://" + addr, facet: facet, data: data, workspace: workspace, demo: demo, outside: outside}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// noRedirects shows a redirect as it is: the router answers a path with ".."
// with one, which must never be taken for the file.
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// do sends a request with the body as given and returns the response.
func do(t *testing.T, method, target string, body io.Reader, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, data
}

// jsonCall sends v as JSON, with the token, and decodes the answer.
func jsonCall(t *testing.T, method, target string, v any) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if v != nil {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	code, _, data := do(t, method, target, body, authed())
	var out map[string]any
	json.Unmarshal(data, &out)
	return code, out
}

func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, key := range path {
		obj, _ := cur.(map[string]any)
		cur = obj[key]
	}
	return cur
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestProjectRoutesNeedTheToken(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	open, _ := json.Marshal(map[string]string{"path": f.outside})
	routes := []struct{ method, path, body string }{
		{"GET", "/api/projects", ""},
		{"POST", "/api/projects", `{"name":"Sneaky"}`},
		{"POST", "/api/projects/open", string(open)},
		{"GET", "/api/projects/demo", ""},
		{"POST", "/api/projects/demo/media?name=sneaky.mp4", "video"},
		{"GET", "/api/media/projects/demo/assets/clip.mp4", ""},
		{"POST", "/api/probe", `{"ref":"projects/demo/assets/clip.mp4"}`},
		{"POST", "/api/review", `{"ref":"projects/demo/renders/final.mp4","width":320,"height":240,"fps":30,"duration":2}`},
		{"POST", "/api/decision", `{"ref":"projects/demo/renders/final.mp4","decision":"accept"}`},
		{"GET", "/api/readiness", ""},
	}
	for _, route := range routes {
		for _, headers := range []map[string]string{{}, {"X-Facet-Token": "guess"}} {
			if code, _, body := do(t, route.method, f.base+route.path, strings.NewReader(route.body), headers); code != http.StatusUnauthorized {
				t.Errorf("%s %s with %v: %d %s, want 401", route.method, route.path, headers, code, body)
			}
		}
	}
	// Only media takes the token in its address, and only this run's.
	if code, _, _ := do(t, "GET", f.base+"/api/media/projects/demo/assets/clip.mp4?t=guess", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("media with a wrong token in its address: %d", code)
	}
	if code, _, _ := do(t, "GET", f.base+"/api/projects?t="+testToken, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("the project list took the token from its address: %d", code)
	}
	// Nothing was done.
	if names := entries(t, filepath.Join(f.workspace, "projects")); strings.Join(names, ",") != "demo" {
		t.Errorf("projects after refused requests: %v", names)
	}
	if _, err := os.Stat(filepath.Join(f.data, "catalog.json")); !os.IsNotExist(err) {
		t.Errorf("a refused request wrote the catalog: %v", err)
	}
	if names := entries(t, filepath.Join(f.demo, "assets")); len(names) != 2 {
		t.Errorf("a refused upload wrote into the project: %v", names)
	}
	if names := entries(t, filepath.Join(f.demo, "review")); len(names) != 0 {
		t.Errorf("a refused review or decision wrote into the project: %v", names)
	}
	if len(f.facet.runs("media_probe"))+len(f.facet.runs("output_review")) != 0 {
		t.Errorf("a refused request ran a tool")
	}
}

func TestProjectRoutesAnswerOnlyThisWindow(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	evil := map[string]string{"X-Facet-Token": testToken, "Origin": "https://evil.example"}
	rebound := map[string]string{"X-Facet-Token": testToken, "Host": "evil.example:80"}
	for _, route := range []struct{ method, path, body string }{
		{"POST", "/api/projects", `{"name":"Cross"}`},
		{"POST", "/api/projects/open", `{"path":"` + strings.ReplaceAll(f.outside, `\`, `\\`) + `"}`},
		{"POST", "/api/projects/demo/media?name=cross.mp4", "video"},
		{"POST", "/api/probe", `{"ref":"projects/demo/assets/clip.mp4"}`},
		{"POST", "/api/review", `{"ref":"projects/demo/renders/final.mp4","width":320,"height":240,"fps":30,"duration":2}`},
		{"POST", "/api/decision", `{"ref":"projects/demo/renders/final.mp4","decision":"accept"}`},
		{"GET", "/api/media/projects/demo/assets/clip.mp4", ""},
		{"GET", "/api/projects/demo", ""},
	} {
		if code, _, _ := do(t, route.method, f.base+route.path, strings.NewReader(route.body), evil); code != http.StatusForbidden {
			t.Errorf("%s %s from another site: %d, want 403", route.method, route.path, code)
		}
		if code, _, _ := do(t, route.method, f.base+route.path, strings.NewReader(route.body), rebound); code != http.StatusMisdirectedRequest {
			t.Errorf("%s %s for another host (DNS rebinding): %d, want 421", route.method, route.path, code)
		}
	}
	if code, _, _ := do(t, "GET", f.base+"/api/media/projects/demo/assets/clip.mp4?t="+testToken, nil, map[string]string{"Host": "evil.example:80"}); code != http.StatusMisdirectedRequest {
		t.Errorf("media for another host: %d", code)
	}
	if names := entries(t, filepath.Join(f.workspace, "projects")); strings.Join(names, ",") != "demo" {
		t.Errorf("projects after refused requests: %v", names)
	}
	if names := entries(t, filepath.Join(f.demo, "assets")); len(names) != 2 {
		t.Errorf("a refused upload wrote into the project: %v", names)
	}
	if len(f.facet.runs("media_probe"))+len(f.facet.runs("output_review")) != 0 {
		t.Errorf("a refused request ran a tool")
	}
}

func TestProjectsListCreateAndOpen(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	code, out := jsonCall(t, "GET", f.base+"/api/projects", nil)
	projects, _ := out["projects"].([]any)
	if code != http.StatusOK || len(projects) != 1 || get(projects[0].(map[string]any), "slug") != "demo" || get(projects[0].(map[string]any), "in_workspace") != true {
		t.Fatalf("list: %d %v", code, out)
	}
	if out["productions_root"] != filepath.Join(f.workspace, "projects") {
		t.Errorf("productions root = %v", out["productions_root"])
	}

	// Only the name is taken: the folder goes in the productions root
	// whatever else the request says.
	code, out = jsonCall(t, "POST", f.base+"/api/projects", map[string]string{"name": "Spring Launch!", "dir": f.outside, "slug": "../escape"})
	if code != http.StatusCreated || get(out, "project", "id") != "spring-launch" || get(out, "project", "path") != filepath.Join(f.workspace, "projects", "spring-launch") {
		t.Fatalf("create: %d %v", code, out)
	}
	if info, err := os.Stat(filepath.Join(f.workspace, "projects", "spring-launch")); err != nil || !info.IsDir() {
		t.Fatalf("the project folder was not made: %v", err)
	}
	if names := entries(t, f.outside); strings.Join(names, ",") != "secret.png" {
		t.Fatalf("creation wrote where the request said: %v", names)
	}
	for name, want := range map[string]int{"   ": http.StatusBadRequest, strings.Repeat("x", maxProjectName+1): http.StatusBadRequest, "Spring Launch": http.StatusConflict} {
		if code, out := jsonCall(t, "POST", f.base+"/api/projects", map[string]string{"name": name}); code != want {
			t.Errorf("create %q: %d %v, want %d", name, code, out, want)
		}
	}

	// Opening reads a folder anywhere and writes nothing into it.
	external := filepath.Join(f.outside, "External Film")
	writeFile(t, filepath.Join(external, "brief.md"), "# External\n")
	code, out = jsonCall(t, "POST", f.base+"/api/projects/open", map[string]string{"path": `"` + external + `"`})
	if code != http.StatusOK || get(out, "project", "id") != "external-film" {
		t.Fatalf("open: %d %v", code, out)
	}
	if names := entries(t, external); strings.Join(names, ",") != "brief.md" {
		t.Fatalf("opening wrote into the folder: %v", names)
	}
	_, out = jsonCall(t, "GET", f.base+"/api/projects", nil)
	byslug := map[string]map[string]any{}
	for _, raw := range out["projects"].([]any) {
		p := raw.(map[string]any)
		byslug[p["slug"].(string)] = p
	}
	if len(byslug) != 3 || byslug["external-film"]["in_workspace"] != false || byslug["external-film"]["name"] != "External Film" || byslug["spring-launch"]["in_workspace"] != true {
		t.Fatalf("list after create and open: %v", byslug)
	}

	drive := "/"
	if runtime.GOOS == "windows" {
		drive = filepath.VolumeName(f.outside) + `\`
	}
	file := filepath.Join(f.outside, "secret.png")
	for path, want := range map[string]int{
		"":                                  http.StatusBadRequest,
		"relative/folder":                   http.StatusBadRequest,
		`\\?\` + external:                   http.StatusBadRequest,
		external + "/../External Film":      http.StatusForbidden,
		f.outside + `\..\..`:                http.StatusForbidden,
		drive:                               http.StatusForbidden,
		f.data:                              http.StatusForbidden,
		filepath.Join(f.data, "compa"):      http.StatusForbidden,
		f.demo:                              http.StatusForbidden,
		filepath.Join(f.outside, "missing"): http.StatusNotFound,
		file:                                http.StatusNotFound,
		// A folder that holds the App's data, and so its catalog.
		filepath.Dir(f.data): http.StatusBadRequest,
	} {
		if code, out := jsonCall(t, "POST", f.base+"/api/projects/open", map[string]string{"path": path}); code != want {
			t.Errorf("open %q: %d %v, want %d", path, code, out, want)
		}
	}
	cat, err := journeys.LoadCatalog(journeys.Options{CatalogPath: filepath.Join(f.data, "catalog.json")})
	if err != nil || len(cat.Projects) != 2 {
		t.Fatalf("catalog after refused opens: %+v, %v", cat, err)
	}
}

func TestProjectDetailsShowTheEvidence(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	code, out := jsonCall(t, "GET", f.base+"/api/projects/demo", nil)
	if code != http.StatusOK {
		t.Fatalf("details: %d %v", code, out)
	}
	beats, _ := get(out, "project", "beats").([]any)
	if get(out, "project", "name") != "Demo film" || len(beats) != 1 || get(out, "ref") != "projects/demo" || get(out, "in_workspace") != true || get(out, "max_upload") != float64(uploadLimit) {
		t.Fatalf("details: %v", out)
	}
	renders, _ := out["renders"].([]any)
	if len(renders) != 1 || get(renders[0].(map[string]any), "url") != "/api/media/projects/demo/renders/final.mp4" {
		t.Fatalf("renders: %v", out["renders"])
	}
	if assets, _ := get(out, "project", "assets").([]any); len(assets) != 2 {
		t.Fatalf("assets: %v", assets)
	}
	if out["review"] != nil || get(out, "project", "decision") != nil {
		t.Fatalf("evidence that does not exist was reported: %v", out)
	}
	for _, slug := range []string{"missing", "%252e%252e", "..%5Cdemo"} {
		if code, _ := jsonCall(t, "GET", f.base+"/api/projects/"+slug, nil); code != http.StatusNotFound {
			t.Errorf("details of %q: %d, want 404", slug, code)
		}
	}
}

func TestMediaServesOnlyProjectFiles(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	clip := f.base + "/api/media/projects/demo/assets/clip.mp4"
	code, header, body := do(t, "GET", clip+"?t="+testToken, nil, nil)
	if code != http.StatusOK || string(body) != "clip bytes" || header.Get("Content-Type") != "video/mp4" || header.Get("Cache-Control") != "no-store" || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("media: %d %q %v", code, body, header)
	}
	if code, _, body := do(t, "GET", clip, nil, map[string]string{"X-Facet-Token": testToken, "Range": "bytes=0-3"}); code != http.StatusPartialContent || string(body) != "clip" {
		t.Errorf("a range of the media: %d %q", code, body)
	}

	absolute := url.PathEscape(filepath.Join(f.outside, "secret.png"))
	for path, want := range map[string]int{
		"projects/demo/secret.env":                  http.StatusUnsupportedMediaType,
		"projects/demo/assets/missing.png":          http.StatusNotFound,
		"projects/demo":                             http.StatusForbidden,
		"projects/missing/assets/clip.mp4":          http.StatusForbidden,
		"catalog/unknown/assets/clip.mp4":           http.StatusForbidden,
		"other/demo/assets/clip.mp4":                http.StatusForbidden,
		`projects/demo/..%5C..%5C..%5Csecret.png`:   http.StatusForbidden,
		"projects/demo/%252e%252e/%252e%252e/x.png": http.StatusNotFound,
		"projects/demo/../../../../secret.png":      0,
		"projects/demo/%2e%2e/%2e%2e/secret.png":    0,
		"projects/%2e%2e/%2e%2e/%2e%2e/secret.png":  0,
		absolute: 0,
		strings.ReplaceAll(absolute, "%5C", "%2F"): 0,
	} {
		code, _, body := do(t, "GET", f.base+"/api/media/"+path+"?t="+testToken, nil, nil)
		if (want != 0 && code != want) || code == http.StatusOK || bytes.Contains(body, []byte("outside every project")) || bytes.Contains(body, []byte("not media")) {
			t.Errorf("media %s: %d %q, want %d (and never the file)", path, code, body, want)
		}
	}
}

// link makes link lead to the folder target: a junction on Windows, which
// needs no privilege, and a symbolic link elsewhere.
func link(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("cannot make a junction: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make a link: %v", err)
	}
}

func TestLinksOutOfAProjectAreRefused(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	link(t, f.outside, filepath.Join(f.demo, "linked"))
	if code, _, body := do(t, "GET", f.base+"/api/media/projects/demo/linked/secret.png?t="+testToken, nil, nil); code != http.StatusForbidden {
		t.Errorf("media through a link out of the project: %d %q", code, body)
	}
	if code, out := jsonCall(t, "POST", f.base+"/api/probe", map[string]string{"ref": "projects/demo/linked/secret.png"}); code != http.StatusForbidden {
		t.Errorf("inspecting through a link out of the project: %d %v", code, out)
	}

	other := filepath.Join(f.workspace, "projects", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, f.outside, filepath.Join(other, "assets"))
	code, _, body := do(t, "POST", f.base+"/api/projects/other/media?name=clip.mp4", strings.NewReader("video"), authed("Content-Type", "application/octet-stream"))
	if code != http.StatusForbidden {
		t.Errorf("an upload into assets that lead out of the project: %d %s", code, body)
	}
	if names := entries(t, f.outside); strings.Join(names, ",") != "secret.png" {
		t.Errorf("a refused upload wrote outside the project: %v", names)
	}
	if len(f.facet.runs("media_probe")) != 0 {
		t.Errorf("media_probe ran on a file outside the project")
	}
}

func TestImportMediaIntoAProject(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	upload := func(slug, query, body string, chunked bool) (int, map[string]any) {
		t.Helper()
		var reader io.Reader = strings.NewReader(body)
		if chunked {
			// An unknown length: the limit holds while the file arrives.
			reader = io.MultiReader(strings.NewReader(body))
		}
		code, _, data := do(t, "POST", f.base+"/api/projects/"+slug+"/media?"+query, reader, authed("Content-Type", "application/octet-stream"))
		var out map[string]any
		json.Unmarshal(data, &out)
		return code, out
	}
	assets := filepath.Join(f.demo, "assets")
	read := func(name string) string {
		data, _ := os.ReadFile(filepath.Join(assets, name))
		return string(data)
	}

	code, out := upload("demo", "name="+url.QueryEscape(`..\..\My Clip!.MOV`), "new clip", false)
	if code != http.StatusCreated || get(out, "imported", "name") != "assets/My-Clip.mov" || get(out, "url") != "/api/media/projects/demo/assets/My-Clip.mov" || get(out, "imported", "sha256") != sha256Hex("new clip") {
		t.Fatalf("upload: %d %v", code, out)
	}
	if read("My-Clip.mov") != "new clip" {
		t.Fatalf("the upload is not in the project's assets")
	}
	if code, _, body := do(t, "GET", f.base+out["url"].(string)+"?t="+testToken, nil, nil); code != http.StatusOK || string(body) != "new clip" {
		t.Errorf("the imported file is not served: %d %q", code, body)
	}

	// An existing file is replaced only by a clear choice.
	if code, out := upload("demo", "name=My-Clip.mov", "other clip", false); code != http.StatusConflict {
		t.Errorf("an existing file was overwritten: %d %v", code, out)
	}
	if read("My-Clip.mov") != "new clip" {
		t.Fatalf("a refused upload changed the file")
	}
	if code, out := upload("demo", "name=My-Clip.mov&if_exists=keep", "other clip", false); code != http.StatusCreated || get(out, "imported", "name") != "assets/My-Clip-2.mov" {
		t.Errorf("keep both: %d %v", code, out)
	}
	if code, out := upload("demo", "name=My-Clip.mov&if_exists=replace", "third clip", false); code != http.StatusCreated || get(out, "imported", "replaced") != true || read("My-Clip.mov") != "third clip" {
		t.Errorf("replace: %d %v", code, out)
	}
	if code, out := upload("demo", "name=My-Clip.mov&if_exists=always", "x", false); code != http.StatusBadRequest {
		t.Errorf("an unknown choice: %d %v", code, out)
	}

	// The size limit holds whether the length is announced or not.
	big := strings.Repeat("v", uploadLimit+1)
	for _, chunked := range []bool{false, true} {
		if code, out := upload("demo", "name=big.mp4", big, chunked); code != http.StatusRequestEntityTooLarge {
			t.Errorf("a file over the limit (chunked %v): %d %v", chunked, code, out)
		}
	}
	if code, out := upload("demo", "name=exact.mp4", big[:uploadLimit], true); code != http.StatusCreated {
		t.Errorf("a file at the limit: %d %v", code, out)
	}

	for query, want := range map[string]int{"name=run.exe": http.StatusUnsupportedMediaType, "name=page.html": http.StatusUnsupportedMediaType, "": http.StatusUnsupportedMediaType} {
		if code, out := upload("demo", query, "x", false); code != want {
			t.Errorf("upload %q: %d %v, want %d", query, code, out, want)
		}
	}
	for _, slug := range []string{"missing", "%252e%252e", "..%5C..%5Cdemo"} {
		if code, out := upload(slug, "name=x.mp4", "x", false); code != http.StatusNotFound {
			t.Errorf("upload into %q: %d %v", slug, code, out)
		}
	}
	names := entries(t, assets)
	if strings.Join(names, ",") != "My-Clip-2.mov,My-Clip.mov,clip.mp4,exact.mp4,still.png" {
		t.Errorf("assets after the uploads: %v", names)
	}
	if names := entries(t, f.outside); strings.Join(names, ",") != "secret.png" {
		t.Errorf("an upload wrote outside the project: %v", names)
	}
	if names := entries(t, filepath.Join(f.workspace, "projects")); strings.Join(names, ",") != "demo" {
		t.Errorf("an upload wrote beside the project: %v", names)
	}
}

func TestProbeRunsOnlyWhatTheWindowMayRun(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	code, out := jsonCall(t, "POST", f.base+"/api/probe", map[string]string{"ref": "projects/demo/assets/clip.mp4"})
	if code != http.StatusOK || out["ok"] != true || get(out, "result", "video", "width") != float64(320) {
		t.Fatalf("probe: %d %v", code, out)
	}
	runs := f.facet.runs("media_probe")
	if len(runs) != 1 || runs[0].dir != canonical(t, f.demo) || runs[0].args[4] != `{"input":`+jsonString(canonical(t, filepath.Join(f.demo, "assets", "clip.mp4")))+`}` {
		t.Fatalf("media_probe ran as %+v", runs)
	}
	for ref, want := range map[string]int{
		"projects/demo/brief.md":               http.StatusUnsupportedMediaType,
		"projects/demo/../../outside.png":      http.StatusForbidden,
		"projects/demo/assets/missing.mp4":     http.StatusNotFound,
		filepath.Join(f.outside, "secret.png"): http.StatusForbidden,
	} {
		if code, out := jsonCall(t, "POST", f.base+"/api/probe", map[string]string{"ref": ref}); code != want {
			t.Errorf("probe %q: %d %v, want %d", ref, code, out, want)
		}
	}
	if len(f.facet.runs("media_probe")) != 1 {
		t.Errorf("a refused probe ran media_probe")
	}

	// A Toolkit that declares the tool may charge: the window refuses it.
	paid := startProjects(t, paidToolsList)
	if code, out := jsonCall(t, "POST", paid.base+"/api/probe", map[string]string{"ref": "projects/demo/assets/clip.mp4"}); code != http.StatusForbidden {
		t.Errorf("a paid media_probe: %d %v, want 403", code, out)
	}
	if runs := paid.facet.runs("media_probe"); len(runs) != 0 {
		t.Errorf("a paid tool ran from the window: %+v", runs)
	}
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func TestReviewAndDecision(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	render := "projects/demo/renders/final.mp4"
	review := map[string]any{"ref": render, "width": 320, "height": 240, "fps": 30, "duration": 2, "audio": true}
	code, out := jsonCall(t, "POST", f.base+"/api/review", review)
	samples, _ := get(out, "review", "samples").([]any)
	if code != http.StatusOK || get(out, "envelope", "ok") != true || get(out, "review", "file") != "renders/final.mp4" || len(samples) != 1 {
		t.Fatalf("review: %d %v", code, out)
	}
	frame := get(samples[0].(map[string]any), "url").(string)
	if frame != "/api/media/projects/demo/review/frame-0001.jpg" {
		t.Fatalf("frame url = %s", frame)
	}
	if code, header, body := do(t, "GET", f.base+frame+"?t="+testToken, nil, nil); code != http.StatusOK || header.Get("Content-Type") != "image/jpeg" || string(body) != "jpeg frame" {
		t.Errorf("the sampled frame: %d %v %q", code, header, body)
	}
	var sent map[string]any
	runs := f.facet.runs("output_review")
	if len(runs) != 1 || json.Unmarshal([]byte(runs[0].args[4]), &sent) != nil || get(sent, "profile", "width") != float64(320) || get(sent, "checks", "duration", "expected") != float64(2) {
		t.Fatalf("output_review ran as %+v", runs)
	}

	_, details := jsonCall(t, "GET", f.base+"/api/projects/demo", nil)
	if get(details, "project", "review_report", "status") != "pass" || len(get(details, "review", "samples").([]any)) != 1 {
		t.Fatalf("details after the review: %v", details)
	}

	for name, refused := range map[string]struct {
		body map[string]any
		want int
	}{
		"without expectations": {map[string]any{"ref": render}, http.StatusBadRequest},
		"outside the project":  {map[string]any{"ref": "projects/demo/../../x.mp4", "width": 1, "height": 1, "fps": 1, "duration": 1}, http.StatusForbidden},
		"of a missing render":  {map[string]any{"ref": "projects/demo/renders/missing.mp4", "width": 1, "height": 1, "fps": 1, "duration": 1}, http.StatusNotFound},
	} {
		if code, out := jsonCall(t, "POST", f.base+"/api/review", refused.body); code != refused.want {
			t.Errorf("review %s: %d %v, want %d", name, code, out, refused.want)
		}
	}
	if len(f.facet.runs("output_review")) != 1 {
		t.Errorf("a refused review ran output_review")
	}

	if code, out := jsonCall(t, "POST", f.base+"/api/decision", map[string]string{"ref": render, "decision": "reject"}); code != http.StatusBadRequest {
		t.Errorf("a rejection without a note: %d %v", code, out)
	}
	if code, out := jsonCall(t, "POST", f.base+"/api/decision", map[string]string{"ref": render}); code != http.StatusBadRequest {
		t.Errorf("a decision never made: %d %v", code, out)
	}
	if code, out := jsonCall(t, "POST", f.base+"/api/decision", map[string]string{"ref": "catalog/none/renders/final.mp4", "decision": "accept"}); code != http.StatusForbidden {
		t.Errorf("a decision outside every project: %d %v", code, out)
	}
	code, out = jsonCall(t, "POST", f.base+"/api/decision", map[string]string{"ref": render, "decision": "accept", "note": "Ready to publish."})
	if code != http.StatusOK || get(out, "decision", "sha256") != sha256Hex("rendered video") || get(out, "decision", "decision") != "accepted" {
		t.Fatalf("decision: %d %v", code, out)
	}
	_, details = jsonCall(t, "GET", f.base+"/api/projects/demo", nil)
	if get(details, "project", "decision", "current") != true || get(details, "project", "decision", "note") != "Ready to publish." {
		t.Fatalf("details after the decision: %v", get(details, "project", "decision"))
	}

	// A Toolkit that declares output_review may charge: the window refuses it.
	paid := startProjects(t, paidToolsList)
	if code, out := jsonCall(t, "POST", paid.base+"/api/review", review); code != http.StatusForbidden {
		t.Errorf("a paid output_review: %d %v, want 403", code, out)
	}
	if runs := paid.facet.runs("output_review"); len(runs) != 0 {
		t.Errorf("a paid tool ran from the window: %+v", runs)
	}
}

func TestReadinessReportsWhatEachToolNeeds(t *testing.T) {
	f := startProjects(t, fakeToolsList)
	code, out := jsonCall(t, "GET", f.base+"/api/readiness", nil)
	if code != http.StatusOK || out["version"] != "0.0.1-test" {
		t.Fatalf("readiness: %d %v", code, out)
	}
	tools := map[string]map[string]any{}
	var order []string
	for _, raw := range out["tools"].([]any) {
		tool := raw.(map[string]any)
		tools[tool["name"].(string)] = tool
		order = append(order, tool["name"].(string))
	}
	if strings.Join(order, ",") != "elevenlabs_tts,media_probe,output_review,sora_video" {
		t.Errorf("tools are not sorted: %v", order)
	}
	sora, probe, tts := tools["sora_video"], tools["media_probe"], tools["elevenlabs_tts"]
	missing, _ := sora["missing"].([]any)
	if sora["configured"] != false || len(missing) != 1 || get(missing[0].(map[string]any), "name") != "OPENAI_API_KEY" || get(missing[0].(map[string]any), "type") != "env" || sora["may_charge"] != true || sora["direct"] != false {
		t.Errorf("sora_video = %v", sora)
	}
	if probe["configured"] != true || len(probe["missing"].([]any)) != 0 || probe["direct"] != true || probe["capability"] != "media inspection" {
		t.Errorf("media_probe = %v", probe)
	}
	if unverified, _ := tts["unverified"].([]any); tts["configured"] != true || len(unverified) != 1 || unverified[0] != "ELEVENLABS_API_KEY" {
		t.Errorf("elevenlabs_tts = %v", tts)
	}
	// Readiness is read again each time; a version facet cannot tell is
	// reported, and the tools are still shown.
	f.facet.setVersion("no version")
	code, out = jsonCall(t, "GET", f.base+"/api/readiness", nil)
	if code != http.StatusOK || out["version"] != nil || out["version_error"] == nil || len(out["tools"].([]any)) != 4 {
		t.Errorf("readiness without a version: %d %v", code, out)
	}
}

// TestScreensWithARealToolkit drives the screens' routes against a real facet
// (FACET_APP_FACET) and FFmpeg: create a project, import a clip, inspect it,
// review a render and record the decision.
func TestScreensWithARealToolkit(t *testing.T) {
	path := os.Getenv("FACET_APP_FACET")
	if path == "" {
		t.Skip("FACET_APP_FACET is not set")
	}
	for _, program := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not on PATH", program)
		}
	}
	f := startProjects(t, fakeToolsList)
	real := toolkit.New(path)
	journeys.Toolkit = real
	s := New(&fakeHost{k: &fakeKernel{}}, real, testToken)
	s.Projects = Projects{Options: journeys.Options{
		Workspace: f.workspace, CatalogPath: filepath.Join(f.data, "catalog.json"), ProductionsRoot: filepath.Join(f.workspace, "projects"),
	}, DataDir: f.data}
	addr, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	go s.Serve(ctx)
	base := "http://" + addr

	clip := filepath.Join(f.outside, "clip.mp4")
	render := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", clip)
	if output, err := render.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make the clip: %v\n%s", err, output)
	}
	clipBytes, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}

	code, out := jsonCall(t, "POST", base+"/api/projects", map[string]string{"name": "Real Toolkit"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	project := get(out, "project", "path").(string)
	code, _, body := do(t, "POST", base+"/api/projects/real-toolkit/media?name=clip.mp4", bytes.NewReader(clipBytes), authed("Content-Type", "application/octet-stream"))
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %s", code, body)
	}
	code, out = jsonCall(t, "POST", base+"/api/probe", map[string]string{"ref": "projects/real-toolkit/assets/clip.mp4"})
	if code != http.StatusOK || out["ok"] != true || get(out, "result", "video", "width") != float64(320) || get(out, "result", "sha256") != sha256Hex(string(clipBytes)) {
		t.Fatalf("probe: %d %v", code, out)
	}

	// The agent renders into renders/; here the clip stands in for it.
	writeFile(t, filepath.Join(project, "renders", "final.mp4"), string(clipBytes))
	code, out = jsonCall(t, "POST", base+"/api/review", map[string]any{"ref": "projects/real-toolkit/renders/final.mp4",
		"width": 320, "height": 240, "fps": 30, "duration": 2, "duration_tolerance": 0.25, "audio": true})
	status := get(out, "envelope", "result", "review_status")
	samples, _ := get(out, "review", "samples").([]any)
	if code != http.StatusOK || get(out, "envelope", "ok") != true || (status != "pass" && status != "warn") || len(samples) == 0 {
		t.Fatalf("review: %d %v", code, out)
	}
	frame := get(samples[0].(map[string]any), "url").(string)
	if code, header, _ := do(t, "GET", base+frame+"?t="+testToken, nil, nil); code != http.StatusOK || header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("a sampled frame: %d %v", code, header)
	}
	code, out = jsonCall(t, "POST", base+"/api/decision", map[string]string{"ref": "projects/real-toolkit/renders/final.mp4", "decision": "accept"})
	if code != http.StatusOK || get(out, "decision", "sha256") != sha256Hex(string(clipBytes)) {
		t.Fatalf("decision: %d %v", code, out)
	}
	code, out = jsonCall(t, "GET", base+"/api/readiness", nil)
	if code != http.StatusOK || out["version"] == nil || out["version"] == "" {
		t.Fatalf("readiness: %d %v", code, out)
	}
	for _, raw := range out["tools"].([]any) {
		if tool := raw.(map[string]any); tool["name"] == "media_probe" && (tool["configured"] != true || tool["direct"] != true) {
			t.Errorf("media_probe = %v", tool)
		}
	}
}
