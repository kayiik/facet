package hook

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kayiik/facet/internal/toolbox"
)

func TestPaidToolsThroughMCP(t *testing.T) {
	for name, want := range map[string][]string{
		"mcp__plugin_facet_facet__sora_video":   {"sora_video"},  // the plugin's server
		"mcp__facet__kling_video":               {"kling_video"}, // facet wire's server
		"mcp__plugin_studio_facet__gflow_image": {"gflow_image"}, // a facet server in another plugin
		"mcp__plugin_facet_facet__media_probe":  nil,             // free
		"mcp__plugin_facet_facet__edge_tts":     nil,
		"mcp__other__sora_video":                nil, // not Facet's server
		"mcp__notfacet__sora_video":             nil,
		"sora_video":                            nil,
		"Read":                                  nil,
	} {
		if got := PaidTools(name, json.RawMessage(`{"prompt":"x","mock":true}`)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
}

func TestEveryPaidToolAsksThroughMCPAndTheShell(t *testing.T) {
	for _, tool := range toolbox.PaidTools() {
		if got := PaidTools("mcp__plugin_facet_facet__"+tool, nil); !reflect.DeepEqual(got, []string{tool}) {
			t.Errorf("MCP %s: got %v", tool, got)
		}
		input, _ := json.Marshal(map[string]string{"command": "facet tools run " + tool + " --input request.json"})
		for _, shell := range []string{"Bash", "PowerShell"} {
			if got := PaidTools(shell, input); !reflect.DeepEqual(got, []string{tool}) {
				t.Errorf("%s %s: got %v", shell, tool, got)
			}
		}
	}
	for _, tool := range toolbox.Names() {
		if toolbox.MayCharge(tool) {
			continue
		}
		if got := PaidInCommand("facet tools run " + tool + " --input r.json"); len(got) != 0 {
			t.Errorf("free tool %s asks: %v", tool, got)
		}
	}
}

func TestPaidInCommand(t *testing.T) {
	for command, want := range map[string][]string{
		"facet tools run sora_video --input request.json":                                                       {"sora_video"},
		"cd project && facet tools run kling_video --input '{\"prompt\":\"x\"}'":                                {"kling_video"},
		"facet tools run veo --input r.json":                                                                    nil,            // no aliases: an unknown tool runs nothing
		"facet tools run SORA_VIDEO --input r.json":                                                             {"sora_video"}, // any case
		"facet tools run 'elevenlabs_tts' --input r.json":                                                       {"elevenlabs_tts"},
		"/home/a/.local/bin/facet tools run openai_image --input r.json":                                        {"openai_image"},
		`& "C:\Users\test user\AppData\Local\Programs\Facet\bin\facet.exe" tools run flux_image --input r.json`: {"flux_image"},
		"npx @kayiik/facet tools run openai_tts --input r.json":                                                 {"openai_tts"},
		"npx @kayiik/facet@latest tools run openai_tts --input r.json":                                          {"openai_tts"},
		"facet tools \\\n  run sora_video --input r.json":                                                       {"sora_video"}, // sh continuation
		"facet tools `\r\n  run sora_video --input r.json":                                                      {"sora_video"}, // PowerShell continuation
		"facet tools run sora_video --input a.json; facet tools run kling_video --input b":                      {"kling_video", "sora_video"},
		"facet tools run media_probe --input r.json":                                                            nil,
		"facet tools estimate sora_video --input r.json":                                                        nil, // an estimate charges nothing
		"facet tools describe sora_video":                                                                       nil,
		"myfacet tools run sora_video --input r.json":                                                           nil,
		"facet mcp": nil,
		"":          nil,
	} {
		got := PaidInCommand(command)
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v, want %v", command, got, want)
		}
	}
}

func run(t *testing.T, args []string, input string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := CLI(args, strings.NewReader(input), &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestClaudeAnswersAskForPaidTools(t *testing.T) {
	out, errOut, code := run(t, []string{"claude"}, `{"session_id":"s","hook_event_name":"PreToolUse","permission_mode":"default",
		"tool_name":"mcp__plugin_facet_facet__sora_video","tool_input":{"prompt":"a red ball","mock":true}}`)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var answer claudeOutput
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		t.Fatalf("answer %q: %v", out, err)
	}
	d := answer.HookSpecificOutput
	if d.HookEventName != "PreToolUse" || d.PermissionDecision != "ask" || !strings.Contains(d.PermissionDecisionReason, "sora_video may charge") {
		t.Errorf("answer = %+v", d)
	}

	out, _, code = run(t, []string{"claude"}, `{"hook_event_name":"PreToolUse","tool_name":"Bash",
		"tool_input":{"command":"facet tools run kling_video --input r.json && facet tools run sora_video --input s.json"}}`)
	if code != 0 || !strings.Contains(out, `"permissionDecision":"ask"`) || !strings.Contains(out, "kling_video and sora_video may charge") {
		t.Errorf("exit %d, answer %q", code, out)
	}
}

func TestClaudeAnswersNothingOtherwise(t *testing.T) {
	for _, input := range []string{
		`{"hook_event_name":"PreToolUse","tool_name":"mcp__plugin_facet_facet__media_probe","tool_input":{}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":"not an object"}`,
		`{"hook_event_name":"PostToolUse","tool_name":"mcp__plugin_facet_facet__sora_video","tool_input":{}}`,
	} {
		if out, errOut, code := run(t, []string{"claude"}, input); code != 0 || out != "" || errOut != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", input, code, out, errOut)
		}
	}
}

func TestClaudeReportsBadInput(t *testing.T) {
	if out, errOut, code := run(t, []string{"claude"}, "not json"); code != 1 || out != "" || !strings.Contains(errOut, "not a JSON object") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, errOut, code := run(t, []string{"claude"}, strings.Repeat(" ", maxInput+1)); code != 1 || !strings.Contains(errOut, "larger than") {
		t.Errorf("oversized input: exit %d, stderr %q", code, errOut)
	}
}

func TestCLIUsage(t *testing.T) {
	if out, _, code := run(t, []string{"--help"}, ""); code != 0 || !strings.Contains(out, "facet hook claude") {
		t.Errorf("help: exit %d, %q", code, out)
	}
	for _, args := range [][]string{nil, {"codex"}, {"claude", "extra"}} {
		if _, errOut, code := run(t, args, ""); code != 1 || !strings.Contains(errOut, "Usage") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}
