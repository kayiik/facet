package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kayiik/facet/internal/bundle"
)

// How each CLI registers an MCP server, per scope:
//
//	claude   user, project  `claude mcp add --scope <scope> --transport stdio facet -- <facet> mcp`
//	                        (syntax from `claude mcp add --help`; user scope is stored in
//	                        ~/.claude.json, project scope in <project>/.mcp.json)
//	codex    user, project  one [mcp_servers.facet] block, with the call limit and the
//	                        approval rules for paid tools that `codex mcp add` cannot set,
//	                        is appended to $CODEX_HOME/config.toml (~/.codex/config.toml)
//	                        or <project>/.codex/config.toml, which Codex reads for
//	                        trusted projects. A user wiring an earlier facet wire made
//	                        with `codex mcp add` keeps that registration.
//	copilot  user, project  `copilot mcp add` exists but its syntax is not shown by
//	                        `copilot mcp --help`, so one member is merged into
//	                        ~/.copilot/mcp-config.json or <project>/.github/mcp.json, the
//	                        locations `copilot mcp --help` lists. Its "tools" list only
//	                        makes the tools available; Copilot CLI still asks before
//	                        each one that is not read-only.
//	opencode user, project  `opencode mcp add` is interactive, so one member is merged into
//	                        opencode.json(c) in the global config directory or the project root
//	compa    user           the facet entry, MCP turned on, the MCP call limit and the ask
//	                        rules are written to Compa's config.json in one locked edit
//	                        (see compa.go)

// mcpStep is the MCP part of a plan.
type mcpStep struct {
	op         string // register | update | unchanged | adopt | unregister | absent | keep
	method     string
	file       string
	cli        string
	cliPath    string
	cliMissing bool
	dir        string
	argvs      [][]string
	existed    bool
	before     []byte
	after      []byte
	deleteFile bool
	mode       fs.FileMode
	keyPath    []string
	record     *MCPRecord
	warnings   []string
	// compaWhat names Compa settings an edit changes besides the server.
	compaWhat []string
}

// observed is what a CLI's configuration says about the facet server.
type observed struct {
	known   bool // the configuration could be read
	present bool
	parsed  bool // command and args were read
	command string
	args    []string
}

func (o observed) runs(command string, args []string) bool {
	return o.parsed && samePath(o.command, command) && equalStrings(o.args, args)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var (
	jsonServersKey     = []string{"mcpServers", bundle.MCPServerName}
	opencodeServersKey = []string{"mcp", bundle.MCPServerName}
)

func observeJSONServer(file string, keyPath []string) observed {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return observed{known: true}
	}
	if err != nil {
		return observed{}
	}
	root, err := parseJSONC(data)
	if err != nil || root.kind != '{' {
		return observed{}
	}
	node := root
	for _, key := range keyPath[:len(keyPath)-1] {
		m, _ := node.member(key)
		if m == nil {
			return observed{known: true}
		}
		node = m.value
	}
	m, _ := node.member(keyPath[len(keyPath)-1])
	if m == nil {
		return observed{known: true}
	}
	o := observed{known: true, present: true}
	v, err := valueOf(data, m.value)
	if err != nil {
		return o
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return o
	}
	if typ, ok := obj["type"].(string); ok && typ != "stdio" && typ != "local" {
		return o
	}
	switch command := obj["command"].(type) {
	case string:
		o.command, o.parsed = command, true
		o.args = []string{}
		if list, ok := obj["args"].([]any); ok {
			for _, item := range list {
				s, ok := item.(string)
				if !ok {
					o.parsed = false
				}
				o.args = append(o.args, s)
			}
		}
	case []any: // OpenCode: command is the whole argv
		if len(command) > 0 {
			o.parsed = true
			for i, item := range command {
				s, ok := item.(string)
				if !ok {
					o.parsed = false
				}
				if i == 0 {
					o.command = s
				} else {
					o.args = append(o.args, s)
				}
			}
			if o.args == nil {
				o.args = []string{}
			}
		}
	}
	return o
}

func observeTOMLServer(file string) observed {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return observed{known: true}
	}
	if err != nil {
		return observed{}
	}
	st := inspectTOMLServer(string(data), bundle.MCPServerName)
	o := observed{known: true, present: st.defined, parsed: st.parsed, command: st.command, args: st.args}
	if o.parsed && o.args == nil {
		o.args = []string{}
	}
	return o
}

// commandSpec is a CLI's own registration command.
type commandSpec struct {
	cli    string
	file   string
	add    []string
	remove []string
	dir    string
}

func (e *env) claudeUserConfig() string {
	if dir := strings.TrimSpace(e.getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(e.home, ".claude.json")
}

func (e *env) codexUserConfig() string {
	if dir := strings.TrimSpace(e.getenv("CODEX_HOME")); dir != "" {
		return filepath.Join(dir, "config.toml")
	}
	return filepath.Join(e.home, ".codex", "config.toml")
}

func (e *env) claudeSpec(scope bundle.Scope, project string) commandSpec {
	server := append([]string{bundle.MCPServerName, "--", e.exe}, bundle.MCPServerArgs()...)
	spec := commandSpec{
		cli:    "claude",
		file:   e.claudeUserConfig(),
		add:    append([]string{"mcp", "add", "--scope", string(scope), "--transport", "stdio"}, server...),
		remove: []string{"mcp", "remove", "--scope", string(scope), bundle.MCPServerName},
		dir:    e.home,
	}
	if scope == bundle.ScopeProject {
		spec.file = filepath.Join(project, ".mcp.json")
		spec.dir = project
	}
	return spec
}

func (e *env) codexSpec() commandSpec {
	return commandSpec{
		cli:    "codex",
		file:   e.codexUserConfig(),
		add:    append([]string{"mcp", "add", bundle.MCPServerName, "--", e.exe}, bundle.MCPServerArgs()...),
		remove: []string{"mcp", "remove", bundle.MCPServerName},
		dir:    e.home,
	}
}

func readCommandConfig(cli, file string) observed {
	if cli == "codex" {
		return observeTOMLServer(file)
	}
	return observeJSONServer(file, jsonServersKey)
}

type copilotServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Tools   []string `json:"tools"`
}

type opencodeServer struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
	Enabled bool     `json:"enabled"`
}

func (e *env) planMCPInstall(t bundle.Target, scope bundle.Scope, project, root string, prev *MCPRecord) (mcpStep, []string) {
	args := bundle.MCPServerArgs()
	switch {
	case t == bundle.TargetClaude:
		return e.planCommandInstall(e.claudeSpec(scope, project), prev)
	case t == bundle.TargetCodex && scope == bundle.ScopeUser && prev != nil && prev.Method == MethodCommand:
		return e.planCommandInstall(e.codexSpec(), prev)
	case t == bundle.TargetCodex && scope == bundle.ScopeUser:
		return e.planTOMLInstall(e.codexUserConfig(), prev)
	case t == bundle.TargetCodex:
		return e.planTOMLInstall(filepath.Join(project, ".codex", "config.toml"), prev)
	case t == bundle.TargetCopilot:
		file := filepath.Join(root, "mcp-config.json")
		if scope == bundle.ScopeProject {
			file = filepath.Join(root, "mcp.json")
		}
		return e.planJSONInstall(file, jsonServersKey, copilotServer{Type: "local", Command: e.exe, Args: args, Tools: []string{"*"}}, prev, nil)
	default: // OpenCode
		dir, candidates := root, []string{"opencode.json", "opencode.jsonc", "config.json"}
		var shadows []string
		if scope == bundle.ScopeProject {
			dir, candidates = project, []string{"opencode.json", "opencode.jsonc"}
			shadows = []string{filepath.Join(root, "opencode.json"), filepath.Join(root, "opencode.jsonc")}
		}
		file := ""
		if prev != nil && prev.Method == MethodJSON {
			if _, err := os.Stat(prev.File); err == nil {
				file = prev.File
			}
		}
		for _, name := range candidates {
			path := filepath.Join(dir, name)
			if file == "" {
				if _, err := os.Stat(path); err == nil {
					file = path
				}
			}
		}
		if file == "" {
			file = filepath.Join(dir, candidates[0])
		}
		for _, name := range candidates {
			if path := filepath.Join(dir, name); !samePath(path, file) {
				shadows = append(shadows, path)
			}
		}
		command := append([]string{e.exe}, args...)
		return e.planJSONInstall(file, opencodeServersKey, opencodeServer{Type: "local", Command: command, Enabled: true}, prev, shadows)
	}
}

func (e *env) planCommandInstall(spec commandSpec, prev *MCPRecord) (mcpStep, []string) {
	args := bundle.MCPServerArgs()
	step := mcpStep{method: MethodCommand, file: spec.file, cli: spec.cli, dir: spec.dir}
	step.record = &MCPRecord{Method: MethodCommand, Name: bundle.MCPServerName, Command: e.exe, Args: args,
		File: spec.file, CLI: spec.cli, AddArgv: spec.add, RemoveArgv: spec.remove, Dir: spec.dir}
	o := readCommandConfig(spec.cli, spec.file)
	var problems []string
	switch {
	case o.present && o.runs(e.exe, args):
		step.op = "adopt"
		if prev != nil && prev.Method == MethodCommand {
			step.op = "unchanged"
		}
	case o.present && prev != nil && prev.Method == MethodCommand && o.runs(prev.Command, prev.Args):
		step.op = "update"
		step.argvs = [][]string{spec.remove, spec.add}
	case o.present:
		problems = append(problems, fmt.Sprintf("an MCP server named %q is already configured in %s and does not match what facet wire registered; remove it with `%s`, then rerun",
			bundle.MCPServerName, spec.file, commandLine(append([]string{spec.cli}, spec.remove...))))
	default:
		step.op = "register"
		step.argvs = [][]string{spec.add}
		if !o.known {
			step.warnings = append(step.warnings, fmt.Sprintf("could not read %s; %s reports an existing registration itself", spec.file, spec.cli))
		}
	}
	if len(step.argvs) > 0 {
		path, err := exec.LookPath(spec.cli)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s was not found on PATH; facet wire registers the MCP server with `%s`",
				spec.cli, commandLine(append([]string{spec.cli}, spec.add...))))
		}
		step.cliPath = path
	}
	return step, problems
}

func (e *env) planJSONInstall(file string, keyPath []string, value any, prev *MCPRecord, shadows []string) (mcpStep, []string) {
	step := mcpStep{method: MethodJSON, file: file, keyPath: keyPath, mode: 0o644}
	want, err := json.Marshal(value)
	if err != nil {
		return step, []string{err.Error()}
	}
	command, args := e.exe, bundle.MCPServerArgs()
	rec := &MCPRecord{Method: MethodJSON, Name: bundle.MCPServerName, Command: command, Args: args, File: file, KeyPath: keyPath, Value: want}
	step.record = rec
	key := strings.Join(keyPath, ".")
	var problems []string
	if prev != nil && prev.Method == MethodJSON && samePath(prev.File, file) {
		rec.CreatedFile, rec.CreatedParent = prev.CreatedFile, prev.CreatedParent
	}
	data, err := os.ReadFile(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc, err := renderJSONDocument(keyPath, value)
		if err != nil {
			return step, []string{err.Error()}
		}
		step.op, step.after = "register", doc
		rec.CreatedFile, rec.CreatedParent = true, true
	case err != nil:
		return step, []string{err.Error()}
	default:
		step.existed, step.before = true, data
		if info, err := os.Stat(file); err == nil {
			step.mode = info.Mode().Perm()
		}
		root, err := parseJSONC(data)
		if err != nil {
			return step, []string{fmt.Sprintf("cannot parse %s (%v); fix it, then rerun", file, err)}
		}
		if root.kind != '{' {
			return step, []string{fmt.Sprintf("%s does not hold a JSON object", file)}
		}
		parent, _ := root.member(keyPath[0])
		switch {
		case parent == nil:
			after, err := insertMember(data, root, keyPath[0], map[string]any{keyPath[1]: value})
			if err != nil {
				return step, []string{err.Error()}
			}
			step.op, step.after = "register", after
			rec.CreatedParent = true
		case parent.value.kind != '{':
			problems = append(problems, fmt.Sprintf("%q in %s is not an object; fix it, then rerun", keyPath[0], file))
		default:
			m, _ := parent.value.member(keyPath[1])
			switch {
			case m == nil:
				after, err := insertMember(data, parent.value, keyPath[1], value)
				if err != nil {
					return step, []string{err.Error()}
				}
				step.op, step.after = "register", after
			case nodeEquals(data, m.value, want):
				step.op = "adopt"
				if prev != nil && prev.Method == MethodJSON {
					step.op = "unchanged"
				}
			case prev != nil && prev.Method == MethodJSON && samePath(prev.File, file) && nodeEquals(data, m.value, prev.Value):
				after, err := replaceValue(data, m, value)
				if err != nil {
					return step, []string{err.Error()}
				}
				step.op, step.after = "update", after
			default:
				problems = append(problems, fmt.Sprintf("%s already defines %s and it does not match what facet wire registered; remove it, then rerun", file, key))
			}
		}
	}
	for _, other := range shadows {
		o := observeJSONServer(other, keyPath)
		if o.present && !o.runs(command, args) {
			problems = append(problems, fmt.Sprintf("%s also defines %s; the CLI merges its configuration files, so one would shadow the other; remove it, then rerun", other, key))
		}
	}
	return step, problems
}

func (e *env) planTOMLInstall(file string, prev *MCPRecord) (mcpStep, []string) {
	args := bundle.MCPServerArgs()
	block := tomlServerBlock(bundle.MCPServerName, e.exe, args)
	step := mcpStep{method: MethodTOML, file: file, mode: 0o644}
	rec := &MCPRecord{Method: MethodTOML, Name: bundle.MCPServerName, Command: e.exe, Args: args, File: file}
	step.record = rec
	data, err := os.ReadFile(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		step.op, step.after = "register", []byte(block)
		rec.Block, rec.CreatedFile = block, true
		return step, nil
	case err != nil:
		return step, []string{err.Error()}
	}
	step.existed, step.before = true, data
	if info, err := os.Stat(file); err == nil {
		step.mode = info.Mode().Perm()
	}
	content := string(data)
	// Keep the file's own line endings so removal restores it exactly.
	nl := newline(data)
	block = strings.ReplaceAll(block, "\n", nl)
	if prev != nil && prev.Method == MethodTOML && samePath(prev.File, file) && prev.Block != "" && strings.Contains(content, prev.Block) {
		rec.CreatedFile = prev.CreatedFile
		core := strings.TrimLeft(prev.Block, "\r\n")
		sep := prev.Block[:len(prev.Block)-len(core)]
		if core == block {
			step.op, rec.Block = "unchanged", prev.Block
			return step, nil
		}
		step.op = "update"
		rec.Block = sep + block
		step.after = []byte(strings.Replace(content, prev.Block, rec.Block, 1))
		return step, nil
	}
	if strings.Contains(content, block) {
		step.op, rec.Block = "adopt", block
		return step, nil
	}
	st := inspectTOMLServer(content, bundle.MCPServerName)
	switch {
	case st.inlineParent:
		return step, []string{fmt.Sprintf("%s assigns mcp_servers inline, so facet wire cannot add a table to it; add the facet server there yourself", file)}
	case st.defined:
		return step, []string{fmt.Sprintf("%s already defines mcp_servers.%s and it does not match what facet wire registered; remove it, then rerun", file, bundle.MCPServerName)}
	}
	sep := ""
	switch {
	case content == "":
	case strings.HasSuffix(content, "\n"):
		sep = nl
	default:
		sep = nl + nl
	}
	rec.Block = sep + block
	step.op, step.after = "register", []byte(content+rec.Block)
	return step, nil
}

func (e *env) planMCPRemove(rec *MCPRecord) mcpStep {
	step := mcpStep{method: rec.Method, file: rec.File, cli: rec.CLI, dir: rec.Dir, keyPath: rec.KeyPath, mode: 0o644}
	switch rec.Method {
	case MethodCommand:
		o := readCommandConfig(rec.CLI, rec.File)
		switch {
		case o.known && !o.present:
			step.op = "absent"
		case !o.known || o.runs(rec.Command, rec.Args):
			step.op = "unregister"
			step.argvs = [][]string{rec.RemoveArgv}
			path, err := exec.LookPath(rec.CLI)
			if err != nil {
				step.op, step.argvs, step.cliMissing = "keep", nil, true
				step.warnings = append(step.warnings, fmt.Sprintf("%s was not found on PATH; unregister the MCP server with `%s`, then rerun facet wire --remove",
					rec.CLI, commandLine(append([]string{rec.CLI}, rec.RemoveArgv...))))
			}
			step.cliPath = path
		default:
			step.op = "keep"
			step.warnings = append(step.warnings, fmt.Sprintf("the %q MCP server in %s changed after facet wire registered it; it is left in place", rec.Name, rec.File))
		}
	case MethodJSON:
		e.planJSONRemove(&step, rec)
	case MethodTOML:
		data, err := os.ReadFile(rec.File)
		if errors.Is(err, fs.ErrNotExist) {
			step.op = "absent"
			break
		}
		if err != nil {
			step.op = "keep"
			step.warnings = append(step.warnings, err.Error())
			break
		}
		content := string(data)
		step.existed, step.before = true, data
		if info, err := os.Stat(rec.File); err == nil {
			step.mode = info.Mode().Perm()
		}
		switch {
		case rec.Block != "" && strings.Contains(content, rec.Block):
			step.op = "unregister"
			after := strings.Replace(content, rec.Block, "", 1)
			if rec.CreatedFile && strings.TrimSpace(after) == "" {
				step.deleteFile = true
			} else {
				step.after = []byte(after)
			}
		case inspectTOMLServer(content, rec.Name).defined:
			step.op = "keep"
			step.warnings = append(step.warnings, fmt.Sprintf("the %q MCP server in %s changed after facet wire registered it; it is left in place", rec.Name, rec.File))
		default:
			step.op = "absent"
		}
	}
	return step
}

func (e *env) planJSONRemove(step *mcpStep, rec *MCPRecord) {
	data, err := os.ReadFile(rec.File)
	if errors.Is(err, fs.ErrNotExist) {
		step.op = "absent"
		return
	}
	if err != nil {
		step.op = "keep"
		step.warnings = append(step.warnings, err.Error())
		return
	}
	step.existed, step.before = true, data
	if info, err := os.Stat(rec.File); err == nil {
		step.mode = info.Mode().Perm()
	}
	root, err := parseJSONC(data)
	if err != nil || root.kind != '{' || len(rec.KeyPath) != 2 {
		step.op = "keep"
		step.warnings = append(step.warnings, fmt.Sprintf("cannot parse %s; remove %s yourself", rec.File, strings.Join(rec.KeyPath, ".")))
		return
	}
	parent, parentIndex := root.member(rec.KeyPath[0])
	if parent == nil || parent.value.kind != '{' {
		step.op = "absent"
		return
	}
	m, index := parent.value.member(rec.KeyPath[1])
	if m == nil {
		step.op = "absent"
		return
	}
	if !nodeEquals(data, m.value, rec.Value) {
		step.op = "keep"
		step.warnings = append(step.warnings, fmt.Sprintf("%s in %s changed after facet wire registered it; it is left in place", strings.Join(rec.KeyPath, "."), rec.File))
		return
	}
	after := removeMember(data, parent.value, index)
	if rec.CreatedParent {
		if again, err := parseJSONC(after); err == nil {
			if p, i := again.member(rec.KeyPath[0]); p != nil && i == parentIndex && p.value.kind == '{' && len(p.value.members) == 0 && !hasComments(after[p.value.start:p.value.end]) {
				after = removeMember(after, again, i)
			}
		}
	}
	step.op = "unregister"
	if rec.CreatedFile {
		if again, err := parseJSONC(after); err == nil && again.kind == '{' && len(again.members) == 0 && !hasComments(after) {
			step.deleteFile = true
			return
		}
	}
	step.after = after
}

// applyMCP carries out an MCP step.
func (e *env) applyMCP(step *mcpStep) error {
	if step.method == MethodCompa {
		// One edit holds the server, the settings and the ask rules; it is
		// written whenever it changes anything.
		return e.applyCompa(step)
	}
	switch step.op {
	case "register", "update", "unregister":
	default:
		return nil
	}
	if step.method == MethodCommand {
		for _, argv := range step.argvs {
			if err := runCommand(step.cliPath, argv, step.dir); err != nil {
				return err
			}
		}
		if step.op != "unregister" && step.record != nil {
			if o := readCommandConfig(step.cli, step.file); o.known && !o.runs(step.record.Command, step.record.Args) {
				step.warnings = append(step.warnings, fmt.Sprintf("%s reported success, but %s does not show the registration; check with `%s mcp list`", step.cli, step.file, step.cli))
			}
		}
		return nil
	}
	// File edits: refuse if the file changed since it was planned.
	current, err := os.ReadFile(step.file)
	switch {
	case step.existed && (err != nil || !bytes.Equal(current, step.before)):
		return fmt.Errorf("%s changed while facet wire was running; rerun", step.file)
	case !step.existed && err == nil:
		return fmt.Errorf("%s appeared while facet wire was running; rerun", step.file)
	}
	if step.deleteFile {
		return os.Remove(step.file)
	}
	if err := os.MkdirAll(filepath.Dir(step.file), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(step.file, step.after, step.mode)
}

// runCommand runs a CLI with a hard timeout and no standard input.
func runCommand(path string, argv []string, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, argv...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	shown := commandLine(append([]string{filepath.Base(path)}, argv...))
	if ctx.Err() != nil {
		return fmt.Errorf("`%s` did not finish within %s", shown, commandTimeout)
	}
	if err != nil {
		detail := strings.TrimSpace(out.String())
		if len(detail) > 2000 {
			detail = detail[:2000] + "..."
		}
		return fmt.Errorf("`%s` failed (%v): %s", shown, err, detail)
	}
	return nil
}

// printMCP describes an MCP step.
func (e *env) printMCP(step mcpStep, line func(verb, target string)) {
	name := bundle.MCPServerName
	key := strings.Join(step.keyPath, ".")
	in := ""
	if step.method == MethodCommand && step.dir != "" && !samePath(step.dir, e.home) {
		in = " (in " + step.dir + ")"
	}
	switch step.op {
	case "register", "update", "unregister":
		switch step.method {
		case MethodCommand:
			for _, argv := range step.argvs {
				line("run", commandLine(append([]string{step.cli}, argv...))+in)
			}
		case MethodJSON:
			switch {
			case step.deleteFile:
				line("delete", step.file)
			case !step.existed:
				line("create", fmt.Sprintf("%s (%s)", step.file, key))
			case step.op == "unregister":
				line("edit", fmt.Sprintf("%s (remove %s)", step.file, key))
			case step.op == "update":
				line("edit", fmt.Sprintf("%s (update %s)", step.file, key))
			default:
				line("edit", fmt.Sprintf("%s (add %s)", step.file, key))
			}
		case MethodTOML:
			block := fmt.Sprintf("[mcp_servers.%s], asking before each of %d paid tools", name, len(paidTools()))
			switch {
			case step.deleteFile:
				line("delete", step.file)
			case !step.existed:
				line("create", fmt.Sprintf("%s (%s)", step.file, block))
			case step.op == "unregister":
				line("edit", fmt.Sprintf("%s (remove [mcp_servers.%s])", step.file, name))
			case step.op == "update":
				line("edit", fmt.Sprintf("%s (update %s)", step.file, block))
			default:
				line("edit", fmt.Sprintf("%s (append %s)", step.file, block))
			}
		case MethodCompa:
			what := ""
			if len(step.compaWhat) > 0 {
				what = "; " + strings.Join(step.compaWhat, ", ")
			}
			switch step.op {
			case "unregister":
				line("edit", fmt.Sprintf("%s (remove tools.mcp.servers.%s and what facet wire set)", step.file, name))
			case "update":
				line("edit", fmt.Sprintf("%s (update tools.mcp.servers.%s%s)", step.file, name, what))
			default:
				line("edit", fmt.Sprintf("%s (add tools.mcp.servers.%s%s)", step.file, name, what))
			}
		}
	case "unchanged":
		line("unchanged", fmt.Sprintf("MCP server %q in %s", name, step.file))
	case "adopt":
		line("adopt", fmt.Sprintf("MCP server %q already registered identically in %s", name, step.file))
	case "absent":
		line("absent", fmt.Sprintf("MCP server %q is not registered in %s", name, step.file))
	case "keep":
		line("keep", fmt.Sprintf("MCP server %q in %s", name, step.file))
	}
}
