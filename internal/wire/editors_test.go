package wire

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type server struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
}

var testServer = server{Type: "local", Command: []string{`C:\tools\facet.exe`, "mcp"}}

// insertThenRemove adds mcp.facet to doc and removes it again, returning the
// merged and the restored document.
func insertThenRemove(t *testing.T, doc string) (string, string) {
	t.Helper()
	src := []byte(doc)
	root, err := parseJSONC(src)
	if err != nil {
		t.Fatalf("parse %q: %v", doc, err)
	}
	var merged []byte
	parent, _ := root.member("mcp")
	createdParent := parent == nil
	if createdParent {
		merged, err = insertMember(src, root, "mcp", map[string]any{"facet": testServer})
	} else {
		merged, err = insertMember(src, parent.value, "facet", testServer)
	}
	if err != nil {
		t.Fatal(err)
	}
	again, err := parseJSONC(merged)
	if err != nil {
		t.Fatalf("merged document does not parse: %v\n%s", err, merged)
	}
	var decoded map[string]any
	if err := json.Unmarshal(plainJSON(merged), &decoded); err != nil {
		t.Fatalf("merged document is not JSON after stripping comments: %v\n%s", err, merged)
	}
	if dig(decoded, "mcp", "facet", "type") != "local" {
		t.Fatalf("member not inserted:\n%s", merged)
	}
	p, pi := again.member("mcp")
	m, mi := p.value.member("facet")
	want, _ := json.Marshal(testServer)
	if !nodeEquals(merged, m.value, want) {
		t.Fatalf("inserted value does not compare equal:\n%s", merged)
	}
	restored := removeMember(merged, p.value, mi)
	if createdParent {
		r, err := parseJSONC(restored)
		if err != nil {
			t.Fatal(err)
		}
		restored = removeMember(restored, r, pi)
	}
	return string(merged), string(restored)
}

func TestJSONCMemberRoundTrips(t *testing.T) {
	for name, doc := range map[string]string{
		"pretty":           "{\n  \"theme\": \"dark\",\n  \"mcp\": {\n    \"other\": {\n      \"type\": \"remote\"\n    }\n  }\n}\n",
		"crlf":             "{\r\n  \"mcp\": {\r\n    \"other\": {\"type\": \"remote\"}\r\n  }\r\n}\r\n",
		"tabs and comment": "// settings\n{\n\t\"mcp\": { // servers\n\t\t\"other\": {\"type\": \"remote\"},\n\t},\n}\n",
		"compact":          `{"mcp":{"other":{"type":"remote"}},"x":1}`,
		"no parent":        "{\n  \"theme\": \"dark\"\n}\n",
		"no parent bom":    "\ufeff{\n  \"theme\": \"dark\"\n}\n",
		"empty parent":     "{\n  \"mcp\": {}\n}\n",
		"empty root":       "{}",
		"block comment":    "{\n  /* keep */\n  \"mcp\": {\n    \"a\": 1, /* tail */\n  }\n}",
	} {
		t.Run(name, func(t *testing.T) {
			merged, restored := insertThenRemove(t, doc)
			if restored != doc {
				t.Errorf("not restored byte for byte:\n%q\nmerged:\n%s\nwant:\n%q", restored, merged, doc)
			}
			if strings.Contains(doc, "\r\n") && strings.Count(merged, "\n") != strings.Count(merged, "\r\n") {
				t.Errorf("CRLF document received bare LF line endings:\n%q", merged)
			}
		})
	}
}

func TestJSONCRejectsInvalidDocuments(t *testing.T) {
	for _, doc := range []string{"", "{", `{"a" 1}`, `{"a": tru}`, `{"a": 1} x`, "{/* open", `["x"`, `{a: 1}`, "{\"a\": \"line\nbreak\"}"} {
		if _, err := parseJSONC([]byte(doc)); err == nil {
			t.Errorf("parsed invalid document %q", doc)
		}
	}
	for _, doc := range []string{`{"a": [1, {"b": null}, "x",], "c": -1.5e3,}`, "// only\n{}\n", `{"k": "esc\"aped \\ // not a comment"}`} {
		if _, err := parseJSONC([]byte(doc)); err != nil {
			t.Errorf("rejected valid JSONC %q: %v", doc, err)
		}
	}
	if !hasComments([]byte("{ // x\n}")) || hasComments([]byte(`{"url": "http://a//b"}`)) {
		t.Error("comment detection is wrong")
	}
}

func TestReplaceValueKeepsTheRest(t *testing.T) {
	doc := []byte("{\n  \"mcp\": {\n    \"facet\": {\"type\": \"local\", \"command\": [\"old\"]},\n    \"other\": 1\n  }\n}\n")
	root, err := parseJSONC(doc)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := root.member("mcp")
	m, _ := p.value.member("facet")
	out, err := replaceValue(doc, m, testServer)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if dig(decoded, "mcp", "other") != float64(1) || dig(decoded, "mcp", "facet", "command").([]any)[0] != `C:\tools\facet.exe` {
		t.Fatalf("replaced document = %s", out)
	}
}

func TestTOMLInspection(t *testing.T) {
	cases := []struct {
		name, doc       string
		defined, inline bool
		command         string
		args            []string
	}{
		{"absent", "model = \"o3\"\n[mcp_servers.other]\ncommand = \"x\"\n", false, false, "", nil},
		{"table", "[mcp_servers.facet]\ncommand = \"C:\\\\tools\\\\facet.exe\" # comment\nargs = [\"mcp\"]\n", true, false, `C:\tools\facet.exe`, []string{"mcp"}},
		{"literal", "[mcp_servers.\"facet\"]\ncommand = 'C:\\tools\\facet.exe'\nargs = [\n  'mcp',\n]\n", true, false, `C:\tools\facet.exe`, []string{"mcp"}},
		{"crlf", "[mcp_servers.facet]\r\ncommand = \"f\"\r\nargs = [\"mcp\"]\r\n", true, false, "f", []string{"mcp"}},
		{"subtable", "[mcp_servers.facet.env]\nA = \"1\"\n", true, false, "", nil},
		{"dotted key", "[mcp_servers]\nfacet.command = \"f\"\n", true, false, "", nil},
		{"inline parent", "mcp_servers = { other = { command = \"x\" } }\n", false, true, "", nil},
		{"inside multiline string", "notes = \"\"\"\n[mcp_servers.facet]\n\"\"\"\n", false, false, "", nil},
		{"commented out", "# [mcp_servers.facet]\n", false, false, "", nil},
	}
	for _, c := range cases {
		st := inspectTOMLServer(c.doc, "facet")
		if st.defined != c.defined || st.inlineParent != c.inline || st.command != c.command || strings.Join(st.args, ",") != strings.Join(c.args, ",") {
			t.Errorf("%s: %+v", c.name, st)
		}
	}
	for in, want := range map[string]string{`"a\"b"`: `a"b`, `"\u00e9\t"`: "é\t", `'raw\n'`: `raw\n`, `"C:\\x"`: `C:\x`} {
		if got, ok := parseTOMLString(in); !ok || got != want {
			t.Errorf("parseTOMLString(%s) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{`"open`, `"\q"`, `"\u12"`, `x`} {
		if _, ok := parseTOMLString(bad); ok {
			t.Errorf("parseTOMLString accepted %s", bad)
		}
	}
	if got := tomlString("a\"b\\c\n"); got != `"a\"b\\c\n"` {
		t.Errorf("tomlString = %s", got)
	}
}

func TestTOMLAppendKeepsCRLFAndRestores(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.toml")
	original := "model = \"o3\"\r\n"
	if err := os.WriteFile(file, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &env{exe: `C:\tools\facet.exe`}
	step, problems := e.planTOMLInstall(file, nil)
	if len(problems) != 0 || step.op != "register" {
		t.Fatalf("plan = %+v, %v", step, problems)
	}
	if bytes.Count(step.after, []byte("\n")) != bytes.Count(step.after, []byte("\r\n")) {
		t.Fatalf("appended block does not use the file's CRLF line endings: %q", step.after)
	}
	if err := os.WriteFile(file, step.after, 0o644); err != nil {
		t.Fatal(err)
	}
	if st := inspectTOMLServer(string(step.after), "facet"); !st.parsed || st.command != e.exe {
		t.Fatalf("appended table = %+v", st)
	}
	again, _ := e.planTOMLInstall(file, step.record)
	if again.op != "unchanged" {
		t.Fatalf("second plan = %s", again.op)
	}
	removal := e.planMCPRemove(step.record)
	if removal.op != "unregister" || string(removal.after) != original {
		t.Fatalf("removal = %s %q", removal.op, removal.after)
	}
}

func TestFindV1Integrations(t *testing.T) {
	root := t.TempDir()
	marked := []byte("## This installation\n- Invoke Facet through `run-facet.sh`.\n")
	write := func(rel string, data []byte) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/.facet-install/installation.tsv", []byte("version\t1.1.0\nhost\tcodex\n"))
	write("a/.agents/skills/facet/SKILL.md", marked)
	write("b/.github/skills/facet/SKILL.md", marked)
	write("c/.claude/skills/facet/SKILL.md", []byte("# A current skill without the 1.x section\n"))
	found := FindV1Integrations(filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c"), filepath.Join(root, "a"))
	if len(found) != 2 {
		t.Fatalf("found = %+v", found)
	}
	if found[0].Host != "codex" || len(found[0].Files) != 2 || found[1].Host != "copilot" || len(found[1].Files) != 1 {
		t.Fatalf("found = %+v", found)
	}
	if got := found[1].CleanupCommand("linux"); got != "bash install.sh --action uninstall --target copilot --project '"+filepath.Join(root, "b")+"'" {
		t.Errorf("unix cleanup = %s", got)
	}
	if got := (V1Integration{Project: `C:\it's`}).CleanupCommand("windows"); got != `.\install.ps1 -Action uninstall -ProjectDir 'C:\it''s'` {
		t.Errorf("windows cleanup = %s", got)
	}
	if !IsV1Skill(marked) || IsV1Skill([]byte("## This installation")) {
		t.Error("IsV1Skill needs both markers")
	}
}
