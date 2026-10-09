package toolbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Shipped tool schemas (schemas/tools/*.schema.json, bundled for harnesses and
// embedded in the facet package) must describe tools that exist and say
// exactly what the live registry says. A file for a tool the registry does
// not have, a property the decoder rejects, or a constraint the registry does
// not publish is drift, and fails here.
func TestShippedToolSchemasMatchTheRegistry(t *testing.T) {
	dir := filepath.Join("..", "..", "schemas", "tools")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".schema.json") {
			t.Errorf("unexpected entry in schemas/tools: %s", name)
			continue
		}
		tool := strings.TrimSuffix(name, ".schema.json")
		registry, ok := schemas[tool]
		if !known(tool) || !ok {
			t.Errorf("schemas/tools/%s describes %q, which is not a Facet tool", name, tool)
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var shipped map[string]any
		if err := json.Unmarshal(data, &shipped); err != nil {
			t.Fatalf("schemas/tools/%s is not JSON: %v", name, err)
		}
		if shipped["$id"] != "facet/tools/"+tool {
			t.Errorf("schemas/tools/%s has $id %v, want facet/tools/%s", name, shipped["$id"], tool)
		}
		for _, key := range []string{"$schema", "$id", "title"} {
			delete(shipped, key)
		}
		got, want := withoutDescriptions(shipped), withoutDescriptions(contractJSON(t, registry))
		if !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.MarshalIndent(got, "", "  ")
			wantJSON, _ := json.MarshalIndent(want, "", "  ")
			t.Errorf("schemas/tools/%s drifted from the registry.\nshipped:\n%s\nregistry:\n%s", name, gotJSON, wantJSON)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no shipped schema was checked; the guard has no subject")
	}
}

// The registry schema advertises exactly the fields the decoder accepts, so
// the shipped copy cannot promise a field (such as a profile) the tool would
// reject, or hide one it takes.
func TestVideoStitchSchemaMatchesItsDecoder(t *testing.T) {
	properties := schemas["video_stitch"].(map[string]any)["properties"].(map[string]any)
	advertised := make([]string, 0, len(properties))
	for name := range properties {
		advertised = append(advertised, name)
	}
	sort.Strings(advertised)
	accepted := jsonFieldNames(stitchRequest{})
	if !reflect.DeepEqual(advertised, accepted) {
		t.Fatalf("video_stitch schema advertises %v but the decoder accepts %v", advertised, accepted)
	}
	for _, field := range []string{"profile", "dry_run"} {
		data := []byte(`{"operation":"validate","clips":["a.mp4"],"` + field + `":"x"}`)
		if _, _, err := doVideoStitch("estimate", data); err == nil {
			t.Errorf("video_stitch accepted %q, which nothing reads", field)
		}
	}
}

func jsonFieldNames(value any) []string {
	typ := reflect.TypeOf(value)
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// withoutDescriptions drops "description" keywords, which may differ in
// wording between the two copies, so that everything that constrains a
// request must agree. Property names under "properties" are kept even when a
// property is called "description".
func withoutDescriptions(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if key == "description" {
				continue
			}
			if properties, ok := child.(map[string]any); ok && key == "properties" {
				cleaned := make(map[string]any, len(properties))
				for name, schema := range properties {
					cleaned[name] = withoutDescriptions(schema)
				}
				out[key] = cleaned
				continue
			}
			out[key] = withoutDescriptions(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = withoutDescriptions(child)
		}
		return out
	}
	return value
}
