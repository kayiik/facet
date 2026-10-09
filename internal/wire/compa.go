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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kayiik/facet/internal/bundle"
)

// Compa (compa-kernel, the agent engine of the Compa app) is wired through
// its config.json, $COMPA_CONFIG or $COMPA_HOME/config.json (~/.compa):
//
//	skills      <workspace>/skills/<name>/. Compa's agent reads files only inside
//	            its workspace (agents.defaults.workspace, default
//	            $COMPA_HOME/workspace), so the global $COMPA_HOME/skills folder,
//	            whose skills it would list but could not open, is not used.
//	MCP server  tools.mcp.servers.facet = {enabled, command, args, type: stdio,
//	            trusted}: the entry `compa-kernel mcp add --trusted facet --
//	            <facet> mcp` writes. tools.mcp.enabled is turned on.
//	time limit  tools.mcp.call_timeout_seconds = 600 when it is unset. Compa
//	            stops an MCP call after 300 s by default, before Facet's own
//	            limit for a paid call could report the provider job. Facet
//	            sets the global key; a per-server key would be narrower.
//	ask rules   one {tool: mcp_facet_<tool>, source: mcp:facet, action: ask}
//	            per paid tool, first in tools.approval.rules, because the first
//	            matching rule wins. Compa allows MCP tools by default.
//
// The file is strict JSON: a comment or an unknown key stops Compa loading
// it. Compa rewrites all of it on every save, in its own layout, and its
// writers lock <config>.flock. facet wire edits it under the same lock in one
// write, keeps the rest of the text as it is, compares values rather than
// text to detect drift, and has compa-kernel check the result when it can
// find it, restoring the file if Compa rejects it.

const (
	// RulesCompa is the method of the ask rules in Compa's tools.approval.
	RulesCompa = "compa-approval"
	// compaCallTimeout is the MCP call limit set when Compa has none, the
	// same as Codex's tool_timeout_sec.
	compaCallTimeout = 600
	compaSource      = "mcp:" + bundle.MCPServerName
	compaLockWait    = 10 * time.Second
)

// compaServer is the facet entry, in the key order Compa writes.
type compaServer struct {
	Enabled bool     `json:"enabled"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Type    string   `json:"type"`
	Trusted bool     `json:"trusted"`
}

// compaRule is one ask rule, in the key order Compa writes.
type compaRule struct {
	Tool   string `json:"tool"`
	Source string `json:"source"`
	Action string `json:"action"`
}

// compaToolName is the name Compa gives an MCP tool: mcp_<server>_<tool>.
func compaToolName(tool string) string { return "mcp_" + bundle.MCPServerName + "_" + tool }

// compaRules are the Compa tool names of the paid tools.
func compaRules() []string {
	var out []string
	for _, tool := range paidTools() {
		out = append(out, compaToolName(tool))
	}
	return out
}

func compaRuleFor(name string) compaRule {
	return compaRule{Tool: name, Source: compaSource, Action: "ask"}
}

func (e *env) absolute(p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.cwd, p)
	}
	return filepath.Clean(p)
}

// compaHome is $COMPA_HOME, else ~/.compa.
func (e *env) compaHome() string {
	if dir := strings.TrimSpace(e.getenv("COMPA_HOME")); dir != "" {
		return e.absolute(dir)
	}
	return filepath.Join(e.home, ".compa")
}

// compaConfigPath is $COMPA_CONFIG, else config.json in Compa's home.
func (e *env) compaConfigPath() string {
	if file := strings.TrimSpace(e.getenv("COMPA_CONFIG")); file != "" {
		return e.absolute(file)
	}
	return filepath.Join(e.compaHome(), "config.json")
}

// compaDoc is the part of config.json facet wire reads.
type compaDoc struct {
	Agents struct {
		Defaults struct {
			Workspace string `json:"workspace"`
		} `json:"defaults"`
	} `json:"agents"`
	BuildInfo struct {
		Version string `json:"version"`
	} `json:"build_info"`
}

// compaWorkspace is the folder Compa's agent works in, whose skills/ folder
// receives the skills: COMPA_AGENTS_DEFAULTS_WORKSPACE, else
// agents.defaults.workspace (a leading ~ is the home directory), else
// $COMPA_HOME/workspace.
func (e *env) compaWorkspace() string {
	if dir := strings.TrimSpace(e.getenv("COMPA_AGENTS_DEFAULTS_WORKSPACE")); dir != "" {
		return e.absolute(dir)
	}
	if data, err := os.ReadFile(e.compaConfigPath()); err == nil {
		var doc compaDoc
		if json.Unmarshal(data, &doc) == nil {
			if ws := strings.TrimSpace(doc.Agents.Defaults.Workspace); ws != "" {
				if ws == "~" || strings.HasPrefix(ws, "~/") || strings.HasPrefix(ws, `~\`) {
					ws = filepath.Join(e.home, ws[1:])
				}
				if !filepath.IsAbs(ws) {
					ws = filepath.Join(e.compaHome(), ws)
				}
				return filepath.Clean(ws)
			}
		}
	}
	return filepath.Join(e.compaHome(), "workspace")
}

// compaReady reports why Compa's configuration cannot be wired, or "".
func (e *env) compaReady() string {
	file := e.compaConfigPath()
	data, err := os.ReadFile(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("Compa is not set up: %s does not exist; open Compa once (or run `compa-kernel onboard`), then rerun", file)
	case err != nil:
		return err.Error()
	case !json.Valid(data):
		return fmt.Sprintf("%s is not valid JSON, so Compa cannot load it either; fix it, then rerun", file)
	}
	var doc compaDoc
	_ = json.Unmarshal(data, &doc)
	if v := doc.BuildInfo.Version; v != "" && !versionAtLeast(v, 0, 0) {
		return fmt.Sprintf("%s was written by Compa %s; facet wire supports Compa 0.0.1 or later", file, v)
	}
	return ""
}

// versionAtLeast compares a "major.minor[.patch][-pre]" version.
func versionAtLeast(v string, major, minor int) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(strings.SplitN(parts[1], "-", 2)[0])
	if err1 != nil || err2 != nil {
		return false
	}
	return maj > major || maj == major && min >= minor
}

// compaGlob matches Compa's rule patterns: case-sensitive, where * is any
// run of characters and ? one character.
func compaGlob(pattern, s string) bool {
	p, r := []rune(pattern), []rune(s)
	var match func(i, j int) bool
	match = func(i, j int) bool {
		for i < len(p) {
			switch p[i] {
			case '*':
				for k := j; k <= len(r); k++ {
					if match(i+1, k) {
						return true
					}
				}
				return false
			case '?':
				if j >= len(r) {
					return false
				}
			default:
				if j >= len(r) || r[j] != p[i] {
					return false
				}
			}
			i, j = i+1, j+1
		}
		return j == len(r)
	}
	return match(0, 0)
}

// compaRuleState classifies Compa's existing rules for one facet tool:
// present (facet's own ask rule is there), taken (the person wrote a rule
// for exactly this tool, or one that denies or hides it), or neither.
func compaRuleState(rules []any, name string) (present bool, taken string) {
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tool, _ := rule["tool"].(string)
		source, _ := rule["source"].(string)
		action, _ := rule["action"].(string)
		conditional := nonEmpty(rule["origin"]) || nonEmpty(rule["hints"])
		if tool != "" && !compaGlob(tool, name) || source != "" && !compaGlob(source, compaSource) {
			continue
		}
		switch {
		case tool == name && source == compaSource && action == "ask" && !conditional && len(rule) == 3:
			return true, ""
		case tool == name:
			return false, action
		case !conditional && (action == "deny" || action == "hide"):
			return false, action
		}
	}
	return false, ""
}

func nonEmpty(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case []any:
		return len(v) > 0
	case string:
		return v != ""
	}
	return true
}

// compaPath walks an object path and returns the member at its end (nil if
// absent) and its parent object, or an error naming what is not an object.
func compaPath(root *jnode, file string, keys ...string) (*jmember, *jnode, error) {
	node := root
	for i, key := range keys {
		m, _ := node.member(key)
		if m == nil {
			return nil, node, nil
		}
		if i == len(keys)-1 {
			return m, node, nil
		}
		if m.value.kind != '{' {
			return nil, nil, fmt.Errorf("%s in %s is not an object", strings.Join(keys[:i+1], "."), file)
		}
		node = m.value
	}
	return nil, node, nil
}

// compaEdit is a computed change to config.json.
type compaEdit struct {
	data     []byte
	file     string
	serverOp string // register | update | unchanged | adopt | absent | unregister | keep
	changes  CompaChanges
	what     []string // what else changed, for people
	added    []string // ask rules facet wire owns after the edit
	missing  []string // ask rules added by this edit
	rulesOff string   // why no ask rules can be added
	warnings []string
}

// edit replaces data with the result of f, parsing it first.
func (c *compaEdit) edit(f func(root *jnode) ([]byte, error)) error {
	root, err := parseJSONC(c.data)
	if err != nil {
		return err
	}
	if root.kind != '{' {
		return fmt.Errorf("%s does not hold a JSON object", c.file)
	}
	out, err := f(root)
	if err != nil {
		return err
	}
	c.data = out
	return nil
}

// planCompaInstall computes the whole edit of Compa's config.json and the
// ask rules it adds.
func (e *env) planCompaInstall(prev *MCPRecord, prevRules *RulesRecord) (mcpStep, rulesStep, []string) {
	file := e.compaConfigPath()
	args := bundle.MCPServerArgs()
	step := mcpStep{method: MethodCompa, file: file, mode: 0o600}
	rules := rulesStep{method: RulesCompa, file: file, want: compaRules(), op: "none", prev: prevRules}
	value, err := json.Marshal(compaServer{Enabled: true, Command: e.exe, Args: args, Type: "stdio", Trusted: true})
	if err != nil {
		return step, rules, []string{err.Error()}
	}
	rec := &MCPRecord{Method: MethodCompa, Name: bundle.MCPServerName, Command: e.exe, Args: args, File: file,
		KeyPath: []string{"tools", "mcp", "servers", bundle.MCPServerName}, Value: value, Compa: &CompaChanges{}}
	step.record = rec
	if problem := e.compaReady(); problem != "" {
		return step, rules, []string{problem}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return step, rules, []string{err.Error()}
	}
	if info, err := os.Stat(file); err == nil {
		step.mode = info.Mode().Perm()
	}
	step.existed, step.before = true, data
	var doc compaDoc
	_ = json.Unmarshal(data, &doc)
	if prev != nil && prev.Method == MethodCompa && samePath(prev.File, file) && prev.Compa != nil {
		*rec.Compa = *prev.Compa
	}
	ours := prev != nil && prev.Method == MethodCompa && samePath(prev.File, file)
	c := &compaEdit{data: data, file: file, changes: *rec.Compa}
	if err := c.installServer(value, ours, prev); err != nil {
		return step, rules, []string{err.Error()}
	}
	if err := c.turnOn(); err != nil {
		return step, rules, []string{err.Error()}
	}
	if err := c.limitCalls(); err != nil {
		return step, rules, []string{err.Error()}
	}
	var prevAdded []string
	if prevRules != nil && prevRules.Method == RulesCompa && samePath(prevRules.File, file) {
		prevAdded = prevRules.Added
	}
	if err := c.addRules(rules.want, prevAdded); err != nil {
		return step, rules, []string{err.Error()}
	}
	*rec.Compa = c.changes
	step.after = c.data
	step.compaWhat = c.what
	step.warnings = append(step.warnings, c.warnings...)
	step.op = c.serverOp
	if step.op == "unchanged" {
		if !bytes.Equal(step.after, step.before) && len(c.what) > 0 {
			step.op = "update"
		} else if !ours {
			step.op = "adopt"
		}
	}

	rules.record = &RulesRecord{Method: RulesCompa, File: file, Added: c.added}
	switch {
	case c.rulesOff != "":
		rules.op = "skip"
		rules.warnings = append(rules.warnings, c.rulesOff)
		rules.record = &RulesRecord{Method: RulesCompa, File: file, Skipped: c.rulesOff}
	case len(c.missing) > 0 || prevRules == nil || !samePath(prevRules.File, file):
		rules.op = "add"
		rules.missing = c.missing
	default:
		rules.op = "unchanged"
	}
	return step, rules, nil
}

// installServer adds or updates tools.mcp.servers.facet.
func (c *compaEdit) installServer(value []byte, ours bool, prev *MCPRecord) error {
	root, err := parseJSONC(c.data)
	if err != nil || root.kind != '{' {
		return fmt.Errorf("cannot parse %s", c.file)
	}
	mcp, _, err := compaPath(root, c.file, "tools", "mcp")
	if err != nil {
		return err
	}
	if mcp == nil || mcp.value.kind != '{' {
		return fmt.Errorf("%s has no tools.mcp object; open Compa once so it writes its whole configuration, then rerun", c.file)
	}
	var server compaServer
	_ = json.Unmarshal(value, &server)
	servers, _ := mcp.value.member("servers")
	switch {
	case servers == nil:
		c.serverOp, c.changes.CreatedServers = "register", true
		return c.edit(func(root *jnode) ([]byte, error) {
			mcp, _, _ := compaPath(root, c.file, "tools", "mcp")
			return appendMember(c.data, mcp.value, "servers", map[string]any{bundle.MCPServerName: server})
		})
	case servers.value.kind != '{':
		return fmt.Errorf("tools.mcp.servers in %s is not an object", c.file)
	}
	m, _ := servers.value.member(bundle.MCPServerName)
	switch {
	case m == nil:
		c.serverOp = "register"
		return c.edit(func(root *jnode) ([]byte, error) {
			servers, _, _ := compaPath(root, c.file, "tools", "mcp", "servers")
			return appendMember(c.data, servers.value, bundle.MCPServerName, server)
		})
	case nodeEquals(c.data, m.value, value):
		c.serverOp = "unchanged"
		return nil
	case ours && nodeEquals(c.data, m.value, prev.Value):
		c.serverOp = "update"
		return c.edit(func(root *jnode) ([]byte, error) {
			m, _, _ := compaPath(root, c.file, "tools", "mcp", "servers", bundle.MCPServerName)
			return replaceValue(c.data, m, server)
		})
	}
	return fmt.Errorf("an MCP server named %q is already configured in %s and does not match what facet wire registers; remove it with `compa-kernel mcp remove %s`, then rerun",
		bundle.MCPServerName, c.file, bundle.MCPServerName)
}

// turnOn sets tools.mcp.enabled to true.
func (c *compaEdit) turnOn() error {
	root, _ := parseJSONC(c.data)
	m, _, err := compaPath(root, c.file, "tools", "mcp", "enabled")
	if err != nil {
		return err
	}
	switch {
	case m == nil:
		c.changes.EnabledSet, c.changes.EnabledInserted = true, true
		c.what = append(c.what, "turn MCP on")
		return c.edit(func(root *jnode) ([]byte, error) {
			_, mcp, _ := compaPath(root, c.file, "tools", "mcp", "enabled")
			return insertMember(c.data, mcp, "enabled", true)
		})
	case m.value.kind == 'v' && string(c.data[m.value.start:m.value.end]) == "true":
		return nil
	case m.value.kind == 'v' && string(c.data[m.value.start:m.value.end]) == "false":
		c.changes.EnabledSet, c.changes.EnabledInserted = true, false
		c.what = append(c.what, "turn MCP on")
		return c.edit(func(root *jnode) ([]byte, error) {
			m, _, _ := compaPath(root, c.file, "tools", "mcp", "enabled")
			return replaceValue(c.data, m, true)
		})
	}
	return fmt.Errorf("tools.mcp.enabled in %s is not true or false", c.file)
}

// limitCalls sets tools.mcp.call_timeout_seconds when it is unset.
func (c *compaEdit) limitCalls() error {
	root, _ := parseJSONC(c.data)
	m, _, err := compaPath(root, c.file, "tools", "mcp", "call_timeout_seconds")
	if err != nil {
		return err
	}
	if m == nil {
		c.changes.TimeoutSet, c.changes.TimeoutInserted, c.changes.Timeout = true, true, compaCallTimeout
		c.what = append(c.what, fmt.Sprintf("MCP call limit %d s", compaCallTimeout))
		return c.edit(func(root *jnode) ([]byte, error) {
			_, mcp, _ := compaPath(root, c.file, "tools", "mcp", "call_timeout_seconds")
			return appendMember(c.data, mcp, "call_timeout_seconds", compaCallTimeout)
		})
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(c.data[m.value.start:m.value.end])))
	switch {
	case err != nil:
		return fmt.Errorf("tools.mcp.call_timeout_seconds in %s is not a whole number", c.file)
	case n == compaCallTimeout:
		return nil // set by facet wire before, or chosen alike; recorded as it was
	case n <= 0:
		c.changes.TimeoutSet, c.changes.TimeoutInserted, c.changes.Timeout = true, false, compaCallTimeout
		c.what = append(c.what, fmt.Sprintf("MCP call limit %d s", compaCallTimeout))
		return c.edit(func(root *jnode) ([]byte, error) {
			m, _, _ := compaPath(root, c.file, "tools", "mcp", "call_timeout_seconds")
			return replaceValue(c.data, m, compaCallTimeout)
		})
	}
	// The person's own limit is theirs.
	c.changes.TimeoutSet, c.changes.TimeoutInserted, c.changes.Timeout = false, false, 0
	if n < compaCallTimeout {
		c.warnings = append(c.warnings, fmt.Sprintf("tools.mcp.call_timeout_seconds in %s is %d, so Compa stops a Facet call after %d s; a long render or a paid generation may be cut off; raise it to %d or more", c.file, n, n, compaCallTimeout))
	}
	return nil
}

// addRules puts the missing ask rules first in tools.approval.rules.
func (c *compaEdit) addRules(want, prevAdded []string) error {
	root, _ := parseJSONC(c.data)
	m, _, err := compaPath(root, c.file, "tools", "approval", "rules")
	if err != nil || m == nil || m.value.kind != '[' {
		c.rulesOff = fmt.Sprintf("tools.approval.rules in %s is not a list; open Compa's settings once so it writes the rules out, then rerun facet wire to add the ask rules for paid tools", c.file)
		return nil
	}
	v, err := valueOf(c.data, m.value)
	if err != nil {
		return err
	}
	existing, _ := v.([]any)
	ownedBefore := map[string]bool{}
	for _, name := range prevAdded {
		ownedBefore[name] = true
	}
	var add []any
	for _, name := range want {
		present, taken := compaRuleState(existing, name)
		switch {
		case present:
			if ownedBefore[name] {
				c.added = append(c.added, name)
			}
		case taken != "":
			c.warnings = append(c.warnings, fmt.Sprintf("a rule in %s already decides %s (%q); it is left as you set it, so Compa will not ask before it", c.file, name, taken))
		default:
			add = append(add, compaRuleFor(name))
			c.added = append(c.added, name)
			c.missing = append(c.missing, name)
		}
	}
	sort.Strings(c.added)
	if len(add) == 0 {
		return nil
	}
	return c.edit(func(root *jnode) ([]byte, error) {
		m, _, _ := compaPath(root, c.file, "tools", "approval", "rules")
		return prependArrayValues(c.data, m.value, add)
	})
}

// planCompaRemove computes the edit that takes back what facet wire set in
// Compa's config.json. rec or rules may be nil.
func (e *env) planCompaRemove(rec *MCPRecord, rules *RulesRecord) mcpStep {
	file := ""
	switch {
	case rec != nil:
		file = rec.File
	case rules != nil:
		file = rules.File
	}
	step := mcpStep{method: MethodCompa, file: file, mode: 0o600, op: "absent", record: rec}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return step
	}
	if err != nil {
		step.op = "keep"
		step.warnings = append(step.warnings, err.Error())
		return step
	}
	if info, err := os.Stat(file); err == nil {
		step.mode = info.Mode().Perm()
	}
	step.existed, step.before = true, data
	if _, err := parseJSONC(data); err != nil || !json.Valid(data) {
		step.op = "keep"
		step.warnings = append(step.warnings, fmt.Sprintf("cannot parse %s; remove the facet MCP server (`compa-kernel mcp remove %s`) and the ask rules for mcp_facet_ tools yourself", file, bundle.MCPServerName))
		return step
	}
	c := &compaEdit{data: data, file: file}
	if rules != nil && rules.Method == RulesCompa {
		c.removeRules(rules.Added)
	}
	if rec != nil {
		changes := CompaChanges{}
		if rec.Compa != nil {
			changes = *rec.Compa
		}
		c.removeServer(rec, changes)
		c.restoreLimit(changes)
		c.turnOff(changes)
	}
	step.op = c.serverOp
	if step.op == "" {
		step.op = "absent"
	}
	step.after = c.data
	step.warnings = append(step.warnings, c.warnings...)
	return step
}

func (c *compaEdit) removeRules(names []string) {
	for _, name := range names {
		root, err := parseJSONC(c.data)
		if err != nil {
			return
		}
		m, _, err := compaPath(root, c.file, "tools", "approval", "rules")
		if err != nil || m == nil || m.value.kind != '[' {
			return
		}
		want, _ := json.Marshal(compaRuleFor(name))
		found := false
		for i, el := range m.value.elems {
			if nodeEquals(c.data, el.value, want) {
				c.data = removeArrayElem(c.data, m.value, i)
				found = true
				break
			}
		}
		if !found {
			c.warnings = append(c.warnings, fmt.Sprintf("the ask rule for %s in %s changed or is gone; nothing of it was removed", name, c.file))
		}
	}
}

func (c *compaEdit) removeServer(rec *MCPRecord, changes CompaChanges) {
	root, _ := parseJSONC(c.data)
	m, servers, err := compaPath(root, c.file, "tools", "mcp", "servers", rec.Name)
	switch {
	case err != nil || m == nil:
		c.serverOp = "absent"
		return
	case !nodeEquals(c.data, m.value, rec.Value):
		c.serverOp = "keep"
		c.warnings = append(c.warnings, fmt.Sprintf("tools.mcp.servers.%s in %s changed after facet wire registered it; it is left in place", rec.Name, c.file))
		return
	}
	_, index := servers.member(rec.Name)
	c.data = removeMember(c.data, servers, index)
	c.serverOp = "unregister"
	if !changes.CreatedServers {
		return
	}
	root, _ = parseJSONC(c.data)
	if s, mcp, _ := compaPath(root, c.file, "tools", "mcp", "servers"); s != nil && s.value.kind == '{' && len(s.value.members) == 0 {
		_, i := mcp.member("servers")
		c.data = removeMember(c.data, mcp, i)
	}
}

func (c *compaEdit) restoreLimit(changes CompaChanges) {
	if !changes.TimeoutSet {
		return
	}
	root, _ := parseJSONC(c.data)
	m, mcp, err := compaPath(root, c.file, "tools", "mcp", "call_timeout_seconds")
	if err != nil || m == nil || strings.TrimSpace(string(c.data[m.value.start:m.value.end])) != strconv.Itoa(changes.Timeout) {
		return // the person changed it since; it is theirs now
	}
	if changes.TimeoutInserted {
		_, i := mcp.member("call_timeout_seconds")
		c.data = removeMember(c.data, mcp, i)
		return
	}
	if out, err := replaceValue(c.data, m, 0); err == nil {
		c.data = out
	}
}

// turnOff restores tools.mcp.enabled when facet wire turned it on and no
// other server needs it.
func (c *compaEdit) turnOff(changes CompaChanges) {
	if !changes.EnabledSet {
		return
	}
	root, _ := parseJSONC(c.data)
	if s, _, _ := compaPath(root, c.file, "tools", "mcp", "servers"); s != nil && s.value.kind == '{' && len(s.value.members) > 0 {
		return
	}
	m, mcp, err := compaPath(root, c.file, "tools", "mcp", "enabled")
	if err != nil || m == nil || string(c.data[m.value.start:m.value.end]) != "true" {
		return
	}
	if changes.EnabledInserted {
		_, i := mcp.member("enabled")
		c.data = removeMember(c.data, mcp, i)
		return
	}
	if out, err := replaceValue(c.data, m, false); err == nil {
		c.data = out
	}
}

// applyCompa writes a planned edit of Compa's config.json under Compa's own
// lock, refusing if the file changed since it was planned, and restores the
// previous text if compa-kernel rejects the result.
func (e *env) applyCompa(step *mcpStep) error {
	if !step.existed || bytes.Equal(step.before, step.after) {
		return nil
	}
	if !json.Valid(step.after) {
		return fmt.Errorf("the edited %s would not be valid JSON; nothing was changed", step.file)
	}
	unlock, err := lockFile(step.file+".flock", compaLockWait)
	if err != nil {
		return fmt.Errorf("Compa is writing %s (cannot take %s.flock: %v); try again", step.file, step.file, err)
	}
	defer unlock()
	current, err := os.ReadFile(step.file)
	if err != nil || !bytes.Equal(current, step.before) {
		return fmt.Errorf("%s changed while facet wire was running, perhaps saved by Compa; nothing was changed, rerun", step.file)
	}
	if err := writeFileAtomic(step.file, step.after, step.mode); err != nil {
		return err
	}
	if err := e.checkCompaConfig(step.file); err != nil {
		if rerr := writeFileAtomic(step.file, step.before, step.mode); rerr != nil {
			return fmt.Errorf("compa-kernel rejected the edited %s (%v), and restoring it failed: %v", step.file, err, rerr)
		}
		return fmt.Errorf("compa-kernel rejected the edited %s, so it was restored: %v", step.file, err)
	}
	return nil
}

// compaKernel finds compa-kernel to check an edited configuration: on PATH,
// or where Compa's Windows installer puts it.
func (e *env) compaKernel() string {
	if path, err := exec.LookPath("compa-kernel"); err == nil {
		return path
	}
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(e.getenv("LOCALAPPDATA")); local != "" {
			path := filepath.Join(local, "Programs", "Compa", "compa-kernel.exe")
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path
			}
		}
	}
	return ""
}

// checkCompaConfig has compa-kernel load the configuration strictly, as it
// does before it starts: `compa-kernel mcp list` exits non-zero on any
// problem and starts no server. Without compa-kernel nothing is checked.
func (e *env) checkCompaConfig(file string) error {
	kernel := e.compaKernel()
	if kernel == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, kernel, "mcp", "list", "--no-color")
	cmd.Env = append(os.Environ(), "COMPA_CONFIG="+file, "COMPA_HOME="+e.compaHome())
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("`compa-kernel mcp list` did not finish within %s", commandTimeout)
	}
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 2000 {
			detail = detail[:2000] + "..."
		}
		return fmt.Errorf("`compa-kernel mcp list` failed (%v): %s", err, detail)
	}
	return nil
}

// compaMCPState reports the facet server in Compa's configuration.
func compaMCPState(rec *MCPRecord) string {
	data, err := os.ReadFile(rec.File)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing"
	}
	if err != nil {
		return "unknown"
	}
	root, err := parseJSONC(data)
	if err != nil || root.kind != '{' {
		return "unknown"
	}
	m, _, err := compaPath(root, rec.File, "tools", "mcp", "servers", rec.Name)
	switch {
	case err != nil:
		return "unknown"
	case m == nil:
		return "missing"
	case !nodeEquals(data, m.value, rec.Value):
		return "modified"
	}
	// Compa loads no MCP server while MCP is off.
	if on, _, _ := compaPath(root, rec.File, "tools", "mcp", "enabled"); on == nil || string(data[on.value.start:on.value.end]) != "true" {
		return "modified"
	}
	return "ok"
}

// readCompaRules reports which of facet's ask rules Compa's configuration
// holds, the tools whose rule the person decided, or why rules cannot be
// added.
func readCompaRules(data []byte, file string) (map[string]bool, map[string]string, string, error) {
	present, taken := map[string]bool{}, map[string]string{}
	root, err := parseJSONC(data)
	if err != nil {
		return nil, nil, "", fmt.Errorf("cannot parse it (%v)", err)
	}
	m, _, err := compaPath(root, file, "tools", "approval", "rules")
	if err != nil || m == nil || m.value.kind != '[' {
		return nil, nil, fmt.Sprintf("tools.approval.rules in %s is not a list", file), nil
	}
	v, err := valueOf(data, m.value)
	if err != nil {
		return nil, nil, "", err
	}
	rules, _ := v.([]any)
	for _, name := range compaRules() {
		ok, action := compaRuleState(rules, name)
		switch {
		case ok:
			present[name] = true
		case action != "":
			taken[name] = action
		}
	}
	return present, taken, "", nil
}

// compaSetUp reports whether Compa's configuration exists, so that "all"
// includes Compa only where it is set up.
func (e *env) compaSetUp() bool {
	info, err := os.Stat(e.compaConfigPath())
	return err == nil && info.Mode().IsRegular()
}

// prependArrayValues returns src with values inserted before the first
// element of arr, in the array's own layout, so that a reader where the first
// matching rule wins reads them first. removeArrayElem deletes each again.
func prependArrayValues(src []byte, arr *jnode, values []any) ([]byte, error) {
	if len(values) == 0 {
		return src, nil
	}
	nl := newline(src)
	unit := indentUnit(src)
	if len(arr.elems) == 0 {
		arrIndent, _ := lineIndent(src, arr.start)
		indent := arrIndent + unit
		var out bytes.Buffer
		out.Write(src[:arr.start+1])
		for i, v := range values {
			rendered, err := marshalIndent(v, indent, unit)
			if err != nil {
				return nil, err
			}
			out.WriteString(nl + indent + string(bytes.ReplaceAll(rendered, []byte("\n"), []byte(nl))))
			if i < len(values)-1 {
				out.WriteByte(',')
			}
		}
		if len(bytes.TrimSpace(src[arr.start+1:arr.end-1])) == 0 {
			out.WriteString(nl + arrIndent)
			out.Write(src[arr.end-1:])
		} else {
			out.Write(src[arr.start+1:])
		}
		return out.Bytes(), nil
	}
	first := arr.elems[0]
	indent, alone := lineIndent(src, first.value.start)
	var insert strings.Builder
	at := first.value.start
	if alone {
		at -= len(indent)
		for _, v := range values {
			rendered, err := marshalIndent(v, indent, unit)
			if err != nil {
				return nil, err
			}
			insert.WriteString(indent + string(bytes.ReplaceAll(rendered, []byte("\n"), []byte(nl))) + "," + nl)
		}
	} else {
		for _, v := range values {
			text, err := jsonText(v)
			if err != nil {
				return nil, err
			}
			insert.WriteString(text + ", ")
		}
	}
	var out bytes.Buffer
	out.Write(src[:at])
	out.WriteString(insert.String())
	out.Write(src[at:])
	return out.Bytes(), nil
}
