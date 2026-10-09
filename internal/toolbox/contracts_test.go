package toolbox

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Contract assertions cover the schema keywords used by the examples without
// introducing a runtime validator or a new dependency into the toolbox.
func contractValid(schema, value any) bool {
	s, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	for _, keyword := range []string{"oneOf", "anyOf"} {
		if branches, ok := s[keyword].([]any); ok {
			matches := 0
			for _, branch := range branches {
				if contractValid(branch, value) {
					matches++
				}
			}
			if matches == 0 || (keyword == "oneOf" && matches != 1) {
				return false
			}
		}
	}
	if condition, ok := s["if"]; ok && contractValid(condition, value) {
		if then, ok := s["then"]; ok && !contractValid(then, value) {
			return false
		}
	}
	if c, ok := s["const"]; ok && !reflect.DeepEqual(c, value) {
		return false
	}
	if enums, ok := s["enum"].([]any); ok {
		found := false
		for _, v := range enums {
			found = found || reflect.DeepEqual(v, value)
		}
		if !found {
			return false
		}
	}
	kind, _ := s["type"].(string)
	validType := kind == ""
	switch v := value.(type) {
	case map[string]any:
		validType = validType || kind == "object"
		properties, _ := s["properties"].(map[string]any)
		if required, ok := s["required"].([]any); ok {
			for _, key := range required {
				if _, ok := v[key.(string)]; !ok {
					return false
				}
			}
		}
		for key, child := range v {
			if prop, ok := properties[key]; ok {
				if !contractValid(prop, child) {
					return false
				}
			} else if s["additionalProperties"] == false {
				return false
			}
		}
	case []any:
		validType = validType || kind == "array"
		if min, ok := s["minItems"].(float64); ok && float64(len(v)) < min {
			return false
		}
		for _, child := range v {
			if item, ok := s["items"]; ok && !contractValid(item, child) {
				return false
			}
		}
	case string:
		validType = validType || kind == "string"
		if min, ok := s["minLength"].(float64); ok && float64(len(v)) < min {
			return false
		}
		if pattern, ok := s["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(v) {
			return false
		}
	case float64:
		validType = validType || kind == "number" || (kind == "integer" && math.Trunc(v) == v)
		for key, bound := range s {
			n, ok := bound.(float64)
			if !ok {
				continue
			}
			if (key == "minimum" && v < n) || (key == "maximum" && v > n) || (key == "exclusiveMinimum" && v <= n) || (key == "exclusiveMaximum" && v >= n) {
				return false
			}
			if key == "multipleOf" && math.Mod(v, n) != 0 {
				return false
			}
		}
	case bool:
		validType = validType || kind == "boolean"
	case nil:
		validType = validType || kind == "null"
	}
	return validType
}

func contractJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

type rejectContractNetwork struct{ t *testing.T }

func (r rejectContractNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Error("contract estimate attempted network access")
	return nil, fmt.Errorf("network disabled by contract test")
}

func TestGuidanceExamplesThroughCLI(t *testing.T) {
	// Estimates must work with no external executables or provider credentials.
	t.Setenv("PATH", t.TempDir())
	for _, key := range []string{"OPENAI_API_KEY", "FAL_KEY", "ELEVENLABS_API_KEY"} {
		t.Setenv(key, "")
	}
	oldTransport := http.DefaultTransport
	http.DefaultTransport = rejectContractNetwork{t}
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	for _, file := range []string{"assets/source.mp4", "renders/final.mp4", "narration/voice.mp3"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("estimate-only fixture, not media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "skills", "facet", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The guide is the one skill every host loads: it must stay readable.
	if lines := len(strings.Split(strings.TrimSpace(string(data)), "\n")); lines > 140 {
		t.Errorf("the guide has %d lines; keep it concise and move detail to stage guides", lines)
	}
	for _, term := range []string{"capabilities", "pipelines_list", "pipeline_describe", "guidance", "facet tools run"} {
		if !strings.Contains(string(data), term) {
			t.Errorf("the guide never mentions %s", term)
		}
	}
	inline := regexp.MustCompile(`facet tools (?:run|estimate) (\w+) --input '(\{[^\n]+\})'`)
	for _, example := range inline.FindAllStringSubmatch(string(data), -1) {
		var req any
		if err := json.Unmarshal([]byte(example[2]), &req); err != nil {
			t.Fatal(err)
		}
		if !contractValid(contractJSON(t, schemas[example[1]]), req) {
			t.Errorf("the guide's %s example does not match the advertised schema", example[1])
		}
		if env, ok := CLI([]string{"tools", "estimate", example[1], "--input", example[2]}); !ok {
			t.Errorf("the guide's %s example failed: %+v", example[1], env.Error)
		}
	}
	if _, err := os.Stat("artifacts"); !os.IsNotExist(err) {
		t.Fatal("estimate created artifacts")
	}
}

func TestAllToolSchemaContracts(t *testing.T) {
	for _, name := range Names() {
		env, ok := CLI([]string{"tools", "describe", name})
		if !ok {
			t.Fatalf("describe %s failed", name)
		}
		desc := contractJSON(t, env.Result).(map[string]any)
		for _, key := range []string{"request_schema", "result_schema"} {
			schema, ok := desc[key].(map[string]any)
			if !ok || schema["type"] != "object" || schema["properties"] == nil {
				t.Errorf("%s: incomplete %s", name, key)
			}
		}
	}
	// Keep the repaired request schemas aligned with the actual Go contracts.
	for name, request := range map[string]any{"media_probe": probeRequest{}, "video_compose": composeRequest{}, "gflow_image": gflowImageRequest{}, "gflow_video": gflowVideoRequest{}} {
		properties := schemas[name].(map[string]any)["properties"].(map[string]any)
		typ := reflect.TypeOf(request)
		for i := 0; i < typ.NumField(); i++ {
			field := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
			if field != "-" && properties[field] == nil {
				t.Errorf("%s does not advertise %s", name, field)
			}
		}
	}
}

func TestRejectedRequestContracts(t *testing.T) {
	for _, tc := range []struct{ tool, request string }{
		{"media_probe", `{"file_path":"source.mp4"}`},
		{"frame_sample", `{"video_path":"source.mp4","output_dir":"frames","interval_seconds":2}`},
		{"frame_sample", `{"input":"source.mp4","output_dir":"frames","strategy":{"type":"uniform"}}`},
		{"frame_sample", `{"input":"source.mp4","output_dir":"frames","strategy":{"type":"uniform","count":2,"threshold":0.3}}`},
		{"gflow_video", `{"prompt":"test","duration":5}`},
		{"gflow_image", `{"prompt":"test","count":5}`},
	} {
		var req any
		_ = json.Unmarshal([]byte(tc.request), &req)
		if contractValid(contractJSON(t, schemas[tc.tool]), req) {
			t.Errorf("schema accepted invalid %s: %s", tc.tool, tc.request)
		}
		if env, ok := CLI([]string{"tools", "estimate", tc.tool, "--input", tc.request}); ok || env.Error.Code != "invalid_request" {
			t.Errorf("CLI accepted invalid %s: %+v", tc.tool, env)
		}
	}
	if _, ok := CLI([]string{"tools", "describe", "gflow"}); ok {
		t.Fatal("generic gflow tool must not be advertised")
	}
}

func TestGFlowDiscoveryCostAndResultContracts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, name := range []string{"gflow_image", "gflow_video"} {
		desc := contractJSON(t, description(name)).(map[string]any)
		cost := desc["cost"].(map[string]any)
		if desc["configured"] != false || cost["amount"] != nil || cost["known"] != false {
			t.Fatalf("misleading discovery: %v", desc)
		}
		env, ok := CLI([]string{"tools", "estimate", name, "--input", `{"prompt":"test"}`})
		if !ok || env.Execution.EstimatedCost != nil || env.Result.(map[string]any)["estimated_cost"] != nil {
			t.Fatalf("unknown cost not preserved: %+v", env)
		}
		for _, mock := range []bool{false, true} {
			result := map[string]any{"provider": "google_flow", "model": "test", "prompt": "test", "output": "out.png", "mock": mock}
			if !mock {
				result["outputs"] = []gflowOutput{{ID: "media-1", Type: "image", MIMEType: "image/png", Output: "out.png", SourceFile: "source.png", SHA256: strings.Repeat("a", 64)}}
			}
			if !contractValid(contractJSON(t, resultSchemas[name]), contractJSON(t, result)) {
				t.Fatalf("schema rejects provider output: %v", result)
			}
			if !mock {
				delete(result, "outputs")
				if contractValid(contractJSON(t, resultSchemas[name]), contractJSON(t, result)) {
					t.Fatal("real provider output needs provenance")
				}
			}
		}
	}
	binary := "gflow"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, binary), []byte("discovery-only executable fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gflow_image", "gflow_video"} {
		if summary(name)["configured"] != true {
			t.Errorf("%s must discover the binary on PATH", name)
		}
	}
}

func TestMissingCredentialsThroughCLI(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "FAL_KEY", "FLUX_API_KEY", "KLING_API_KEY"} {
		t.Setenv(key, "")
	}
	for _, tool := range []string{"openai_image", "flux_image", "kling_video", "sora_video"} {
		output := filepath.Join(t.TempDir(), "new", "output.mp4")
		data, _ := json.Marshal(map[string]any{"prompt": "test", "output_path": output})
		env, ok := CLI([]string{"tools", "run", tool, "--input", string(data)})
		if ok || env.Error == nil || env.Error.Code != "credentials_missing" {
			t.Errorf("%s missing credentials must fail: %+v", tool, env)
		}
		if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
			t.Errorf("%s created output without credentials", tool)
		}
	}
}
