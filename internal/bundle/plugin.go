package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kayiik/facet/internal/toolbox"
)

// Plugins. A marketplace serves Facet to Claude Code, Codex and Copilot CLI
// as a plugin: the same skills, and the persona where the CLI has an agent
// format, as every other layout, plus the CLI's plugin manifest and the MCP
// server registration in its own format. The formats were read from the
// installed CLIs and checked by installing plugins into each of them, in
// throwaway homes, with a stand-in facet that recorded how it was started
// (Claude Code 2.1.278, Codex 0.160.0, Copilot CLI 1.0.93):
//
//	claude   .claude-plugin/plugin.json, .mcp.json and hooks/hooks.json. The
//	         server is named plugin:facet:facet, so its tools are
//	         mcp__plugin_facet_facet__<tool>; it starts in the project, which
//	         Claude Code also offers as its MCP root. A plugin's settings may
//	         set only `agent` and `subagentStatusLine`, so a plugin cannot add
//	         ask rules. Its PreToolUse hook runs `facet hook claude` before
//	         every paid Facet tool and every shell command, and answers "ask"
//	         for a paid tool, which Claude Code honours even for a tool the
//	         person has allowed. facet wire claude adds ask rules instead.
//	codex    .codex-plugin/plugin.json, pointing at skills/ and .mcp.json, whose
//	         server entry takes config.toml's fields: env_vars, because Codex
//	         starts MCP servers with only a short allowlist of variables;
//	         tool_timeout_sec; and tools.<tool>.approval_mode = "prompt" for
//	         every paid tool. Codex drops an entry with an invalid value.
//	copilot  plugin.json and .mcp.json. Copilot CLI starts a plugin's server in
//	         the plugin's own directory unless the entry sets cwd, and offers
//	         no MCP roots, so cwd "." keeps facet in the directory Copilot runs
//	         in. Copilot asks before every tool that is not read-only.
//
// The registration runs `facet mcp` with facet from PATH. A plugin is a static
// file: it cannot know the person's home folder or FACET_HOME, and the
// installers put <Facet home>/current/bin on PATH. When facet is missing the
// server cannot start; the facet skill then has the agent ask the person to
// install the Toolkit.

const (
	// PluginName is the plugin's name in every format and marketplace.
	PluginName = "facet"
	// PluginCommand is the program a plugin's MCP registration starts.
	PluginCommand = "facet"
	// PluginWebsite is where the Facet Toolkit is installed from.
	PluginWebsite = "https://kayiik.github.io/facet/"
	// PluginRepository is Facet's source repository.
	PluginRepository = "https://github.com/kayiik/facet"
	// PluginLicense is Facet's license, as package.json states it.
	PluginLicense = "AGPL-3.0-or-later"

	pluginAuthor      = "Facet Contributors"
	pluginDescription = "Create, edit, render, and review video with Facet's production skills and MCP tools. Needs the Facet Toolkit: " + PluginWebsite
	pluginToolkitNote = "Facet needs the Facet Toolkit, the facet command on PATH. Install it from " + PluginWebsite + " and restart Copilot CLI."
)

var pluginKeywords = []string{"video", "video-production", "mcp", "ffmpeg", "remotion", "narration", "captions"}

// pluginFile is a plugin manifest's location for each target, relative to
// the plugin root.
var pluginFile = map[Target]string{
	TargetClaude:  ".claude-plugin/plugin.json",
	TargetCodex:   ".codex-plugin/plugin.json",
	TargetCopilot: "plugin.json",
}

// PluginManifestPath returns where target t's plugin manifest sits inside a
// plugin, or "" when t has no plugin layout.
func PluginManifestPath(t Target) string { return pluginFile[t] }

type pluginPerson struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// pluginManifest holds the fields every format shares, in their order.
type pluginManifest struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	Description string       `json:"description"`
	Author      pluginPerson `json:"author"`
	Homepage    string       `json:"homepage"`
	Repository  string       `json:"repository"`
	License     string       `json:"license"`
	Keywords    []string     `json:"keywords"`
}

type copilotManifest struct {
	pluginManifest
	Skills             []string `json:"skills"`
	Agents             []string `json:"agents"`
	PostInstallMessage string   `json:"postInstallMessage"`
}

type codexManifest struct {
	pluginManifest
	Skills     string         `json:"skills"`
	MCPServers string         `json:"mcpServers"`
	Interface  codexInterface `json:"interface"`
}

// codexInterface is how Codex presents the plugin.
type codexInterface struct {
	DisplayName      string   `json:"displayName"`
	ShortDescription string   `json:"shortDescription"`
	LongDescription  string   `json:"longDescription"`
	DeveloperName    string   `json:"developerName"`
	Category         string   `json:"category"`
	Capabilities     []string `json:"capabilities"`
	WebsiteURL       string   `json:"websiteURL"`
	DefaultPrompt    []string `json:"defaultPrompt"`
}

type stdioServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type copilotServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
	Tools   []string `json:"tools"`
}

type codexServer struct {
	Command        string                   `json:"command"`
	Args           []string                 `json:"args"`
	EnvVars        []string                 `json:"env_vars"`
	ToolTimeoutSec int                      `json:"tool_timeout_sec"`
	Tools          map[string]codexApproval `json:"tools"`
}

type codexApproval struct {
	ApprovalMode string `json:"approval_mode"`
}

// Claude Code's hook registration, hooks/hooks.json: a description and a map
// from hook event to matchers. A matcher made only of letters, digits, `_`
// and `|` is a list of exact tool names.
type claudeHooks struct {
	Description string                         `json:"description"`
	Hooks       map[string][]claudeHookMatcher `json:"hooks"`
}

type claudeHookMatcher struct {
	Matcher string       `json:"matcher"`
	Hooks   []claudeHook `json:"hooks"`
}

type claudeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// ClaudeHookCommand is the command the Claude Code plugin's hook runs: the
// shell form, which every Claude Code version runs, with facet from PATH
// like the plugin's MCP registration.
const ClaudeHookCommand = PluginCommand + " hook claude"

// claudeHookTimeout bounds one hook run, in seconds. Claude Code runs the
// tool when the hook times out, so the bound only limits a hang.
const claudeHookTimeout = 30

// ClaudeHookMatcher returns the tools the Claude Code plugin's hook runs
// for: every paid Facet tool, under the plugin's server name and under the
// name facet wire gives the server, and the two shell tools, where `facet
// tools run` would reach a paid tool without MCP.
func ClaudeHookMatcher() string {
	var names []string
	for _, prefix := range []string{"mcp__plugin_" + PluginName + "_" + MCPServerName + "__", "mcp__" + MCPServerName + "__"} {
		for _, tool := range toolbox.PaidTools() {
			names = append(names, prefix+tool)
		}
	}
	return strings.Join(append(names, "Bash", "PowerShell"), "|")
}

func claudeHookFile() ([]byte, error) {
	return renderJSON(claudeHooks{
		Description: "Ask before every Facet tool that may charge, through MCP or the shell.",
		Hooks: map[string][]claudeHookMatcher{"PreToolUse": {{
			Matcher: ClaudeHookMatcher(),
			Hooks:   []claudeHook{{Type: "command", Command: ClaudeHookCommand, Timeout: claudeHookTimeout}},
		}}},
	})
}

// pluginFiles returns target t's plugin manifest and MCP registration, with
// paths relative to the plugin root.
func pluginFiles(t Target, version string) ([]File, error) {
	common := pluginManifest{
		Name: PluginName, Version: version, Description: pluginDescription,
		Author:   pluginPerson{Name: pluginAuthor, URL: PluginRepository},
		Homepage: PluginWebsite, Repository: PluginRepository, License: PluginLicense,
		Keywords: pluginKeywords,
	}
	var manifest, server any
	switch t {
	case TargetClaude:
		manifest = common
		server = stdioServer{Command: PluginCommand, Args: MCPServerArgs()}
	case TargetCopilot:
		manifest = copilotManifest{pluginManifest: common, Skills: []string{"skills/"}, Agents: []string{"agents/"}, PostInstallMessage: pluginToolkitNote}
		server = copilotServer{Type: "local", Command: PluginCommand, Args: MCPServerArgs(), Cwd: ".", Tools: []string{"*"}}
	case TargetCodex:
		manifest = codexManifest{pluginManifest: common, Skills: "./skills/", MCPServers: "./.mcp.json", Interface: codexInterface{
			DisplayName:      "Facet",
			ShortDescription: "Create, edit, render, and review video",
			LongDescription: "Facet's production skills and MCP tools: inspect media, edit, narrate, caption, compose with Remotion or HyperFrames, " +
				"generate with paid providers after you approve each call, and review the result. Needs the Facet Toolkit, the facet command on PATH: " + PluginWebsite,
			DeveloperName: pluginAuthor,
			Category:      "Productivity",
			Capabilities:  []string{"Interactive", "Read", "Write"},
			WebsiteURL:    PluginWebsite,
			DefaultPrompt: []string{"Make a narrated explainer video from my notes", "Cut the silences out of this recording", "Review this video for technical problems"},
		}}
		approvals := map[string]codexApproval{}
		for _, tool := range toolbox.PaidTools() {
			approvals[tool] = codexApproval{ApprovalMode: "prompt"}
		}
		server = codexServer{Command: PluginCommand, Args: MCPServerArgs(), EnvVars: toolbox.EnvVars(), ToolTimeoutSec: CodexToolTimeoutSec, Tools: approvals}
	default:
		return nil, fmt.Errorf("%s has no plugin layout; plugins exist for claude, codex, and copilot", t)
	}
	manifestData, err := renderJSON(manifest)
	if err != nil {
		return nil, err
	}
	serverData, err := renderJSON(map[string]any{"mcpServers": map[string]any{MCPServerName: server}})
	if err != nil {
		return nil, err
	}
	files := []File{
		{Path: pluginFile[t], Kind: KindPlugin, Name: PluginName, Content: manifestData},
		{Path: ".mcp.json", Kind: KindMCP, Name: MCPServerName, Content: serverData},
	}
	if t == TargetClaude {
		hooks, err := claudeHookFile()
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: "hooks/hooks.json", Kind: KindHook, Name: PluginName, Content: hooks})
	}
	return files, nil
}

// renderJSON encodes v with two-space indentation, a final newline, and no
// HTML escaping.
func renderJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
