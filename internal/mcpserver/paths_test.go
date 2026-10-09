package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kayiik/facet/internal/toolbox"
)

// testRoot is a fresh allowed root, resolved as the server resolves one.
func testRoot(t *testing.T) string {
	t.Helper()
	root, err := resolveRoot(t.TempDir())
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	return root
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// schemaKeys collects every property name in every tool's request schema.
func schemaKeys() map[string]bool {
	keys := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch s := v.(type) {
		case map[string]any:
			if props, ok := s["properties"].(map[string]any); ok {
				for k, child := range props {
					keys[k] = true
					walk(child)
				}
			}
			for _, k := range []string{"items", "anyOf", "oneOf", "allOf"} {
				walk(s[k])
			}
		case []any:
			for _, item := range s {
				walk(item)
			}
		}
	}
	for _, name := range toolbox.Names() {
		data, _ := json.Marshal(toolbox.Parameters(name))
		var schema any
		_ = json.Unmarshal(data, &schema)
		walk(schema)
	}
	return keys
}

// Every field ProjectArguments resolves is confined, and every other field
// is left alone, except the documented extras the toolbox reads as files.
func TestPathFieldsAreExactlyProjectArgumentsFields(t *testing.T) {
	root := testRoot(t)
	keys := schemaKeys()
	for _, key := range []string{"input", "output", "source", "src", "video", "path", "clips", "replacement_audio"} {
		keys[key] = true
	}
	for key := range keys {
		projected := toolbox.ProjectArguments(map[string]any{key: "media/a.mp4"}, root)
		projectSays := projected[key] != "media/a.mp4"
		confined, refusal := confineArguments(fixedRoot(root), map[string]any{key: "media/a.mp4"})
		if refusal != nil {
			t.Errorf("%s: a relative path inside the root was refused: %s", key, refusal.message())
			continue
		}
		resolved := confined[key] != "media/a.mp4"
		if want := projectSays || extraPathKeys[key]; resolved != want {
			t.Errorf("%s: resolved=%v, want %v (ProjectArguments resolves it: %v)", key, resolved, want, projectSays)
		}
		if resolved && !samePath(confined[key].(string), filepath.Join(root, "media", "a.mp4")) {
			t.Errorf("%s resolved to %v", key, confined[key])
		}
	}
	for _, key := range []string{"text", "title", "narration", "prompt", "operation", "output_format", "source_type", "query", "image_url", "start_frame", "reference_image", "segments", "voice"} {
		if isPathKey(key) {
			t.Errorf("%s is not a file argument", key)
		}
	}
}

func TestConfineArguments(t *testing.T) {
	root := testRoot(t)
	parent := filepath.Dir(root)
	inside := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	fileURL := func(path string) string {
		slashed := filepath.ToSlash(path)
		if !strings.HasPrefix(slashed, "/") {
			slashed = "/" + slashed
		}
		return (&url.URL{Scheme: "file", Path: slashed}).String()
	}

	allowed := []struct {
		name string
		args string
		want map[string]any // field -> value after confinement
	}{
		{"relative output", `{"output_path":"renders/final.mp4"}`, map[string]any{"output_path": inside("renders", "final.mp4")}},
		{"a parent segment that stays inside", `{"output_path":"a/b/../c.mp4"}`, map[string]any{"output_path": inside("a", "c.mp4")}},
		{"the root itself", `{"output_dir":"."}`, map[string]any{"output_dir": root}},
		{"an absolute path inside", `{"input_path":` + quote(inside("in.mp4")) + `}`, map[string]any{"input_path": inside("in.mp4")}},
		{"a file URL inside", `{"input":` + quote(fileURL(inside("in.mp4"))) + `}`, map[string]any{"input": inside("in.mp4")}},
		{"remote media for a composition source", `{"source":"https://example.com/a.mp4"}`, map[string]any{"source": "https://example.com/a.mp4"}},
		{"inline media for an audio track", `{"src":"data:audio/wav;base64,AAAA"}`, map[string]any{"src": "data:audio/wav;base64,AAAA"}},
		{"prose that mentions a parent directory", `{"text":"cd ../.. and look","title":"../../ explained"}`, map[string]any{"text": "cd ../.. and look", "title": "../../ explained"}},
		{"numbers keep their spelling", `{"start_seconds":1.50,"seed":9007199254740993}`, map[string]any{"start_seconds": json.Number("1.50"), "seed": json.Number("9007199254740993")}},
		{"a blank path is left for the tool to report", `{"input_path":"  "}`, map[string]any{"input_path": "  "}},
		{"clips resolve item by item", `{"clips":["a.mp4","b/c.mp4"]}`, map[string]any{"clips": []any{inside("a.mp4"), inside("b", "c.mp4")}}},
		{"an input outside the root", `{"input_path":` + quote(filepath.Join(parent, "x.mp4")) + `}`, map[string]any{"input_path": filepath.Join(parent, "x.mp4")}},
		{"a nested input outside", `{"segments":[{"input":"../../library/a.mp4"}]}`, nil},
	}
	for _, c := range allowed {
		t.Run(c.name, func(t *testing.T) {
			got, refusal := confineArguments(fixedRoot(root), decodeArgs(t, c.args))
			if refusal != nil {
				t.Fatalf("refused: %s", refusal.message())
			}
			for field, want := range c.want {
				if !equalArgument(got[field], want) {
					t.Errorf("%s = %#v, want %#v", field, got[field], want)
				}
			}
		})
	}

	// Nested values are reached through objects and arrays alike.
	got, refusal := confineArguments(fixedRoot(root), decodeArgs(t, `{"segments":[{"input":"x.mp4","start":0,"end":1}],"audio":{"narration":{"src":"voice.wav"}}}`))
	if refusal != nil {
		t.Fatalf("nested paths refused: %s", refusal.message())
	}
	segment := got["segments"].([]any)[0].(map[string]any)
	narration := got["audio"].(map[string]any)["narration"].(map[string]any)
	if !samePath(segment["input"].(string), inside("x.mp4")) || !samePath(narration["src"].(string), inside("voice.wav")) {
		t.Errorf("nested paths were not resolved: %#v", got)
	}

	refused := []struct {
		name, args, at string
	}{
		{"a leading parent segment", `{"output_path":"../escaped.mp4"}`, "output_path"},
		{"a parent segment deeper in", `{"output_path":"renders/../../deep.mp4"}`, "output_path"},
		{"an absolute output outside", `{"output_path":` + quote(filepath.Join(parent, "x.mp4")) + `}`, "output_path"},
		{"a nested output", `{"segments":[{"output":"ok.mp4"},{"output":"../../evil.mp4"}]}`, "segments[1].output"},

		{"an output in an unknown nested object", `{"edit_decisions":{"cuts":[{"output_path":"../x.mp4"}]}}`, "edit_decisions.cuts[0].output_path"},
		{"an output file URL outside", `{"output":` + quote(fileURL(filepath.Join(parent, "x.mp4"))) + `}`, "output"},
		{"a URL as an output", `{"output_path":"https://example.com/upload"}`, "output_path"},
		{"an FFmpeg protocol as an input", `{"input_path":"concat:a.mp4|b.mp4"}`, "input_path"},
		{"a remote URL as an input file", `{"input":"http://example.com/a.mp4"}`, "input"},
	}
	if runtime.GOOS == "windows" {
		refused = append(refused,
			struct{ name, args, at string }{"a drive-relative path", `{"input":"C:secret.mp4"}`, "input"},
			struct{ name, args, at string }{"an output on another drive", `{"output":"Q:\\x.mp4"}`, "output"},
			struct{ name, args, at string }{"a network share", `{"input":"\\\\attacker.invalid\\share\\x.mp4"}`, "input"},
			struct{ name, args, at string }{"a network share by file URL", `{"input":"file://attacker.invalid/share/x.mp4"}`, "input"},
			struct{ name, args, at string }{"a device name", `{"output_path":"NUL"}`, "output_path"},
			struct{ name, args, at string }{"an alternate data stream", `{"output_path":"a.mp4:hidden"}`, "output_path"},
		)
	}
	for _, c := range refused {
		t.Run("refuses "+c.name, func(t *testing.T) {
			_, refusal := confineArguments(fixedRoot(root), decodeArgs(t, c.args))
			if refusal == nil {
				t.Fatalf("allowed %s", c.args)
			}
			if refusal.at != c.at {
				t.Errorf("blamed %q, want %q", refusal.at, c.at)
			}
			if msg := refusal.message(); !strings.Contains(msg, root) || !strings.Contains(msg, c.at) {
				t.Errorf("the message must name the argument and the root: %s", msg)
			}
			if refusal.details()["root"] != root {
				t.Errorf("details must carry the root: %#v", refusal.details())
			}
			if strings.Contains(c.name, "network share") && !strings.Contains(refusal.reason, "outside the allowed root") {
				// Refused by inspecting the share would mean it was touched.
				t.Errorf("a network path must be refused before it is inspected: %s", refusal.message())
			}
		})
	}
}

// On Windows, even inspecting a UNC path connects, and authenticates, to its
// host, so a share outside the root is refused without being touched.
func TestNetworkPathsAreRefusedUntouched(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC paths only reach the network on Windows")
	}
	root := testRoot(t)
	var touched []string
	previous := lstat
	lstat = func(name string) (os.FileInfo, error) {
		touched = append(touched, name)
		return previous(name)
	}
	t.Cleanup(func() { lstat = previous })
	for _, value := range []string{`\\attacker.invalid\share\x.mp4`, `//attacker.invalid/share/x.mp4`, "file://attacker.invalid/share/x.mp4"} {
		if _, refusal := confineArguments(fixedRoot(root), map[string]any{"input": value}); refusal == nil {
			t.Errorf("%s was allowed", value)
		}
	}
	for _, path := range touched {
		if strings.HasPrefix(path, `\\`) {
			t.Errorf("the policy inspected the network path %s", path)
		}
	}
}

func decodeArgs(t *testing.T, raw string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var args map[string]any
	if err := decoder.Decode(&args); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return args
}

func quote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func equalArgument(got, want any) bool {
	switch w := want.(type) {
	case string:
		g, ok := got.(string)
		if !ok {
			return false
		}
		if filepath.IsAbs(w) {
			return samePath(g, w)
		}
		return g == w
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !equalArgument(g[i], w[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(got, want)
	}
}

// A link inside the root that leads outside it is followed, not trusted.
func TestLinksOutOfTheRootAreRefused(t *testing.T) {
	base := t.TempDir()
	root, err := resolveRoot(mkdir(t, base, "root"))
	if err != nil {
		t.Fatal(err)
	}
	outside := mkdir(t, base, "outside")
	insideTarget := mkdir(t, root, "renders")

	link := func(name, target string) bool {
		path := filepath.Join(root, name)
		if err := os.Symlink(target, path); err == nil {
			return true
		}
		if runtime.GOOS != "windows" {
			return false
		}
		// Symbolic links need a privilege on Windows; a junction does not,
		// and EvalSymlinks will not traverse one, so it is the case that
		// matters most there.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "cmd", "/c", "mklink", "/J", path, target).Run() == nil
	}
	if !link("escape", outside) || !link("renders-link", insideTarget) {
		t.Skip("this system cannot create links in a temporary directory")
	}

	for _, value := range []string{"escape/out.mp4", "escape", "escape/new/deeper.mp4"} {
		if _, refusal := confineArguments(fixedRoot(root), map[string]any{"output_path": value}); refusal == nil {
			t.Errorf("%s leaves the root through a link and was allowed", value)
		} else if !strings.Contains(refusal.resolved, filepath.Base(outside)) {
			t.Errorf("%s: the refusal should show where the link leads, got %q", value, refusal.resolved)
		}
	}
	got, refusal := confineArguments(fixedRoot(root), map[string]any{"output_path": "renders-link/out.mp4"})
	if refusal != nil {
		t.Fatalf("a link that stays inside the root was refused: %s", refusal.message())
	}
	if !samePath(got["output_path"].(string), filepath.Join(root, "renders-link", "out.mp4")) {
		t.Errorf("the tool must receive the checked path, got %v", got["output_path"])
	}

	if runtime.GOOS != "windows" {
		// A dangling link is written through: where it points decides.
		if err := os.Symlink(filepath.Join(outside, "later.mp4"), filepath.Join(root, "dangling-out")); err == nil {
			if _, refusal := confineArguments(fixedRoot(root), map[string]any{"output_path": "dangling-out"}); refusal == nil {
				t.Error("a dangling link to outside the root was allowed")
			}
		}
		if err := os.Symlink(filepath.Join(insideTarget, "later.mp4"), filepath.Join(root, "dangling-in")); err == nil {
			if _, refusal := confineArguments(fixedRoot(root), map[string]any{"output_path": "dangling-in"}); refusal != nil {
				t.Errorf("a dangling link inside the root was refused: %s", refusal.message())
			}
		}
	}
}

func mkdir(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFileURLPath(t *testing.T) {
	type c struct{ in, want string }
	var good []c
	var bad []string
	if runtime.GOOS == "windows" {
		good = []c{
			{"file:///C:/Users/user/a%20b.mp4", `C:\Users\user\a b.mp4`},
			{"file://localhost/D:/x/../y", `D:\y`},
			{"FILE:///c:/x", `c:\x`},
			{"file://server/share/x.mp4", `\\server\share\x.mp4`},
		}
		bad = []string{"file:///x.mp4", "file:relative/x", "file:///C:/x?y=1", "file:///C:/x#f", "http://x/y", "file://"}
	} else {
		good = []c{
			{"file:///home/user/a%20b.mp4", "/home/user/a b.mp4"},
			{"file://localhost/tmp/x/../y", "/tmp/y"},
		}
		bad = []string{"file://server/share/x", "file:relative/x", "file:///x?y=1", "http://x/y", "file://"}
	}
	for _, g := range good {
		got, err := fileURLPath(g.in)
		if err != nil || got != g.want {
			t.Errorf("fileURLPath(%q) = %q, %v; want %q", g.in, got, err, g.want)
		}
	}
	for _, b := range bad {
		if got, err := fileURLPath(b); err == nil {
			t.Errorf("fileURLPath(%q) = %q, want an error", b, got)
		}
	}
}

func TestResolveRootNeedsAnExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveRoot(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing root was accepted")
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRoot(file); err == nil {
		t.Error("a file was accepted as a root")
	}
	if _, err := newServer(context.Background(), Options{Root: filepath.Join(dir, "missing")}); err == nil {
		t.Error("the server started with a root that does not exist")
	}
}

func TestInputSchemasSuitEveryClient(t *testing.T) {
	for _, name := range toolbox.Names() {
		before, _ := json.Marshal(toolbox.Parameters(name))
		schema, note := inputSchema(name)
		after, _ := json.Marshal(toolbox.Parameters(name))
		if string(before) != string(after) {
			t.Fatalf("%s: building the input schema changed the registry's schema", name)
		}
		if schema["type"] != "object" {
			t.Errorf("%s: input schema type = %v", name, schema["type"])
		}
		if _, ok := schema["properties"].(map[string]any); !ok {
			t.Errorf("%s: input schema has no properties object", name)
		}
		for _, keyword := range topLevelCombinators {
			if _, ok := schema[keyword]; ok {
				t.Errorf("%s: input schema keeps top-level %s", name, keyword)
			}
		}
		var original map[string]any
		_ = json.Unmarshal(before, &original)
		for _, keyword := range topLevelCombinators {
			if _, ok := original[keyword]; ok && note == "" {
				t.Errorf("%s: a removed %s constraint is not described", name, keyword)
			}
		}
	}
	_, note := inputSchema("media_probe")
	if !strings.Contains(note, "input | input_path") {
		t.Errorf("media_probe must say it takes input or input_path, got %q", note)
	}
}
