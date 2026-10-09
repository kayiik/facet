// Package hook answers agentic CLIs' tool hooks.
//
// `facet hook claude` is a Claude Code PreToolUse hook. Claude Code runs it
// before a tool runs, passes the tool call on stdin, and reads the answer
// from stdout. For a Facet tool that may charge, reached through MCP
// (mcp__<server>__<tool>, where the server is Facet's) or through the shell
// (`facet tools run <tool>` in a Bash or PowerShell command), the answer is
// "ask": Claude Code then asks the person even when the tool is otherwise
// allowed. For anything else the hook answers nothing and Claude Code's own
// permissions apply.
//
// The Claude Code plugin registers this hook (hooks/hooks.json), because a
// plugin cannot add permission rules; `facet wire claude` writes ask rules
// into Claude Code's settings instead.
//
// Claude Code runs the tool when a hook fails (an exit code other than 0 or
// 2, a crash, a timeout), so the decision depends on nothing that can fail at
// run time: only the tool declarations compiled into facet.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/kayiik/facet/internal/toolbox"
)

// maxInput bounds the hook input Claude Code sends on stdin.
const maxInput = 8 << 20

// claudeInput is the part of Claude Code's PreToolUse input the hook reads.
type claudeInput struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

type claudeOutput struct {
	HookSpecificOutput claudeDecision `json:"hookSpecificOutput"`
}

type claudeDecision struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: facet hook claude

Answers a Claude Code PreToolUse hook. Claude Code passes the tool call on
stdin. For a Facet tool that may charge, reached through MCP or through
"facet tools run" in a shell command, the answer makes Claude Code ask the
person first; for anything else there is no answer, and Claude Code's own
permissions apply. The Facet plugin for Claude Code registers this hook.
`)
}

// CLI implements `facet hook`. args excludes "hook".
func CLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		usage(stdout)
		return 0
	}
	if len(args) != 1 || args[0] != "claude" {
		usage(stderr)
		return 1
	}
	return Claude(stdin, stdout, stderr)
}

// Claude answers one Claude Code PreToolUse hook call.
func Claude(stdin io.Reader, stdout, stderr io.Writer) int {
	data, err := io.ReadAll(io.LimitReader(stdin, maxInput+1))
	if err != nil {
		fmt.Fprintf(stderr, "facet hook: cannot read the hook input: %v\n", err)
		return 1
	}
	if len(data) > maxInput {
		fmt.Fprintln(stderr, "facet hook: the hook input is larger than 8 MiB")
		return 1
	}
	var in claudeInput
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(stderr, "facet hook: the hook input is not a JSON object: %v\n", err)
		return 1
	}
	if in.HookEventName != "" && in.HookEventName != "PreToolUse" {
		return 0
	}
	paid := PaidTools(in.ToolName, in.ToolInput)
	if len(paid) == 0 {
		return 0
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(claudeOutput{HookSpecificOutput: claudeDecision{
		HookEventName: "PreToolUse", PermissionDecision: "ask", PermissionDecisionReason: reason(paid),
	}}); err != nil {
		fmt.Fprintf(stderr, "facet hook: %v\n", err)
		return 1
	}
	return 0
}

func reason(paid []string) string {
	if len(paid) == 1 {
		return "Facet's " + paid[0] + " may charge your provider account. Allow it only if you expect the cost."
	}
	return "Facet's " + strings.Join(paid[:len(paid)-1], ", ") + " and " + paid[len(paid)-1] +
		" may charge your provider account. Allow them only if you expect the cost."
}

// mcpTool splits Claude Code's MCP tool names, mcp__<server>__<tool>. For a
// server a plugin provides, <server> is plugin_<plugin>_<server>.
var mcpTool = regexp.MustCompile(`^mcp__(.+)__([a-z0-9_]+)$`)

// PaidTools returns the Facet tools that may charge which a Claude Code tool
// call would run: an MCP call to one of Facet's servers, or a Bash or
// PowerShell command that runs `facet tools run <tool>`.
func PaidTools(toolName string, toolInput json.RawMessage) []string {
	if m := mcpTool.FindStringSubmatch(toolName); m != nil {
		if facetServer(m[1]) && toolbox.MayCharge(m[2]) {
			return []string{m[2]}
		}
		return nil
	}
	switch toolName {
	case "Bash", "PowerShell":
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(toolInput, &input) != nil {
			return nil
		}
		return PaidInCommand(input.Command)
	}
	return nil
}

// facetServer reports whether an MCP server name is Facet's: "facet" as
// facet wire registers it, or a server named facet inside a plugin.
func facetServer(server string) bool {
	return server == "facet" || strings.HasSuffix(server, "_facet")
}

// facetRun finds `facet tools run <tool>` in a shell command: facet by name,
// by path, or as the npm package (with a version, as npx accepts), quoted or
// not, with or without an extension. The tool is captured as typed; Facet
// accepts aliases and any letter case, so it is canonicalized before the
// check.
var facetRun = regexp.MustCompile("(?i)(?:^|[\\s;&|(){}'\"`/\\\\])facet(?:\\.exe|\\.cmd|-cli\\.js)?(?:@[a-z0-9.^~*-]+)?['\"]?\\s+tools\\s+run\\s+['\"]?([a-z0-9_.-]+)")

// continuation joins lines a shell continues: a backslash (sh) or a backtick
// (PowerShell) before the line break.
var continuation = strings.NewReplacer("\\\r\n", " ", "\\\n", " ", "`\r\n", " ", "`\n", " ")

// PaidInCommand returns the Facet tools that may charge which a shell
// command runs through `facet tools run`.
func PaidInCommand(command string) []string {
	seen := map[string]bool{}
	for _, m := range facetRun.FindAllStringSubmatch(continuation.Replace(command), -1) {
		if tool := toolbox.CanonicalName(m[1]); toolbox.MayCharge(tool) {
			seen[tool] = true
		}
	}
	paid := make([]string, 0, len(seen))
	for tool := range seen {
		paid = append(paid, tool)
	}
	sort.Strings(paid)
	return paid
}
