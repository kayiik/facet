package wire

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kayiik/facet/internal/bundle"
	"github.com/kayiik/facet/internal/toolbox"
)

// Consent rules. Facet declares which of its tools may charge (the toolbox's
// may_charge effect) and never enforces consent itself. facet wire translates
// that declaration into each CLI's own approval setting, so the CLI asks the
// person before every paid call, whatever else they have allowed:
//
//	claude    permissions.ask in settings.json ($CLAUDE_CONFIG_DIR or ~/.claude
//	          for user scope, <project>/.claude for project scope): the MCP tool
//	          mcp__facet__<tool>, and Bash(facet tools run <tool> *) for the shell
//	          route. Claude Code evaluates ask rules before allow rules. Checked
//	          with `claude doctor`, which validates every rule (Claude Code 2.1.278).
//	codex     [mcp_servers.facet.tools.<tool>] approval_mode = "prompt", inside
//	          the MCP block itself (see tomlServerBlock); Codex 0.160.0 accepts
//	          auto|prompt|writes|approve and rejects anything else.
//	opencode  permission.facet_<tool> = "ask", added as the LAST members of the
//	          permission object because OpenCode lets the last matching rule win
//	          (OpenCode 1.18.34 validates the action).
//	copilot   Copilot CLI has no persistent per-tool setting; in its default
//	          manual mode it asks before every tool that is not read-only, which
//	          includes every paid tool.
//
// Every rule added is recorded, and --remove takes back only recorded rules
// that are still as facet wire wrote them.

// Rules methods.
const (
	RulesClaude   = "claude-permissions-ask"
	RulesOpenCode = "opencode-permission"
)

// RulesRecord is the approval rules facet wire added to a CLI's settings.
type RulesRecord struct {
	Method string `json:"method"`
	File   string `json:"file"`
	// Added are the rules facet wire added: Claude Code permission entries,
	// or OpenCode permission keys. Rules the person had already written are
	// never recorded, so they are never removed.
	Added []string `json:"added"`
	// Skipped explains why no rules could be added (the settings hold a shape
	// facet wire does not change); it is a note, not drift.
	Skipped       string `json:"skipped,omitempty"`
	CreatedFile   bool   `json:"created_file,omitempty"`
	CreatedParent bool   `json:"created_parent,omitempty"` // "permissions" or "permission"
	CreatedArray  bool   `json:"created_array,omitempty"`  // Claude Code's "ask"
}

// paidTools lists the tools whose effects may charge, derived from the
// toolbox's own declaration rather than a list kept here.
func paidTools() []string { return toolbox.PaidTools() }

// claudeRules are the Claude Code permission entries for the paid tools.
func claudeRules() []string {
	var out []string
	for _, tool := range paidTools() {
		out = append(out, "mcp__"+bundle.MCPServerName+"__"+tool, "Bash(facet tools run "+tool+" *)")
	}
	return out
}

// opencodeRules are the OpenCode permission keys for the paid tools: OpenCode
// names an MCP tool <server>_<tool>.
func opencodeRules() []string {
	var out []string
	for _, tool := range paidTools() {
		out = append(out, bundle.MCPServerName+"_"+tool)
	}
	return out
}

// rulesSupported reports whether facet wire records separate rules for t.
// Codex's rules live in the MCP block; Copilot CLI has no such setting.
// Compa's rules are written with its MCP entry, and recorded separately.
func rulesSupported(t bundle.Target) bool {
	return t == bundle.TargetClaude || t == bundle.TargetOpenCode || t == bundle.TargetCompa
}

func (e *env) claudeSettings(scope bundle.Scope, project string) string {
	if scope == bundle.ScopeProject {
		return filepath.Join(project, ".claude", "settings.json")
	}
	if dir := strings.TrimSpace(e.getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	return filepath.Join(e.home, ".claude", "settings.json")
}

// rulesStep is the consent part of a plan. Its edit is computed when it is
// applied, on the file as it is then: for OpenCode the MCP registration has
// just changed the same file. Compa's edit is computed with its MCP entry,
// and record is what it leaves.
type rulesStep struct {
	method   string
	file     string
	want     []string
	missing  []string // rules not yet in place when planned
	op       string   // add | unchanged | skip | remove | none
	prev     *RulesRecord
	warnings []string
	record   *RulesRecord
}

func (e *env) planRules(t bundle.Target, scope bundle.Scope, project string, mcp *mcpStep, prev *RulesRecord) rulesStep {
	step := rulesStep{op: "none", prev: prev}
	switch t {
	case bundle.TargetClaude:
		step.method, step.file, step.want = RulesClaude, e.claudeSettings(scope, project), claudeRules()
	case bundle.TargetOpenCode:
		step.method, step.file, step.want = RulesOpenCode, mcp.file, opencodeRules()
	default:
		return step
	}
	present, taken, skipped, err := readRules(step.method, step.file)
	switch {
	case err != nil:
		step.op = "skip"
		step.warnings = append(step.warnings, fmt.Sprintf("cannot read %s to add the ask rules for paid tools: %v", step.file, err))
		return step
	case skipped != "":
		step.op = "skip"
		step.warnings = append(step.warnings, skipped)
		return step
	}
	for _, rule := range step.want {
		switch {
		case taken[rule] != "":
			step.warnings = append(step.warnings, takenWarning(step.file, rule, taken[rule]))
		case !present[rule]:
			step.missing = append(step.missing, rule)
		}
	}
	step.op = "unchanged"
	if len(step.missing) > 0 || prev == nil || !samePath(prev.File, step.file) {
		step.op = "add"
	}
	return step
}

func takenWarning(file, rule, action string) string {
	return fmt.Sprintf("permission.%s in %s is already %q; it is left as you set it, so OpenCode will not ask before %s",
		rule, file, action, strings.TrimPrefix(rule, bundle.MCPServerName+"_"))
}

// readRules reports which rules a settings file already holds, the OpenCode
// rules a person set to another action, or why rules cannot be added to it.
// A missing file holds none.
func readRules(method, file string) (map[string]bool, map[string]string, string, error) {
	present, taken := map[string]bool{}, map[string]string{}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return present, taken, "", nil
	}
	if err != nil {
		return nil, nil, "", err
	}
	root, err := parseJSONC(data)
	if err != nil {
		return nil, nil, "", fmt.Errorf("cannot parse it (%v)", err)
	}
	if root.kind != '{' {
		return nil, nil, fmt.Sprintf("%s does not hold a JSON object; no ask rules were added for paid tools", file), nil
	}
	switch method {
	case RulesCompa:
		return readCompaRules(data, file)
	case RulesClaude:
		perms, _ := root.member("permissions")
		if perms == nil {
			return present, taken, "", nil
		}
		if perms.value.kind != '{' {
			return nil, nil, fmt.Sprintf("\"permissions\" in %s is not an object; no ask rules were added for paid tools", file), nil
		}
		ask, _ := perms.value.member("ask")
		if ask == nil {
			return present, taken, "", nil
		}
		if ask.value.kind != '[' {
			return nil, nil, fmt.Sprintf("\"permissions.ask\" in %s is not an array; no ask rules were added for paid tools", file), nil
		}
		for _, el := range ask.value.elems {
			if s, ok := stringValue(data, el.value); ok {
				present[s] = true
			}
		}
	case RulesOpenCode:
		perm, _ := root.member("permission")
		if perm == nil {
			return present, taken, "", nil
		}
		if perm.value.kind != '{' {
			return nil, nil, fmt.Sprintf("\"permission\" in %s applies one action to every tool; no ask rules were added for paid tools", file), nil
		}
		for _, m := range perm.value.members {
			s, ok := stringValue(data, m.value)
			switch {
			case ok && s == "ask":
				present[m.key] = true
			case ok:
				taken[m.key] = s
			default:
				taken[m.key] = string(data[m.value.start:m.value.end])
			}
		}
	}
	return present, taken, "", nil
}

// applyRules adds the missing rules and returns the record. createdFile says
// the MCP registration created the same file in this run.
func (e *env) applyRules(step *rulesStep, createdFile bool) (*RulesRecord, error) {
	if step.op == "none" {
		return nil, nil
	}
	rec := &RulesRecord{Method: step.method, File: step.file}
	if step.prev != nil && samePath(step.prev.File, step.file) {
		rec.CreatedFile, rec.CreatedParent, rec.CreatedArray = step.prev.CreatedFile, step.prev.CreatedParent, step.prev.CreatedArray
	}
	rec.CreatedFile = rec.CreatedFile || createdFile
	if step.op == "skip" {
		rec.Skipped = strings.Join(step.warnings, "; ")
		return rec, nil
	}
	data, err := os.ReadFile(step.file)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	present, taken, skipped, err := readRules(step.method, step.file)
	if err != nil {
		return nil, err
	}
	if skipped != "" {
		rec.Skipped = skipped
		return rec, nil
	}
	var missing []string
	for _, rule := range step.want {
		if !present[rule] && taken[rule] == "" {
			missing = append(missing, rule)
		}
	}
	// Rules facet wire added before stay recorded as its own.
	added := map[string]bool{}
	if step.prev != nil && samePath(step.prev.File, step.file) {
		for _, rule := range step.prev.Added {
			if present[rule] {
				added[rule] = true
			}
		}
	}
	for _, rule := range missing {
		added[rule] = true
	}
	for _, rule := range step.want {
		if added[rule] {
			rec.Added = append(rec.Added, rule)
		}
	}
	if len(missing) == 0 {
		return rec, nil
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(step.file); err == nil {
		mode = info.Mode().Perm()
	}
	var after []byte
	switch step.method {
	case RulesClaude:
		after, err = addClaudeRules(data, existed, missing, rec)
	case RulesOpenCode:
		after, err = addOpenCodeRules(data, existed, missing, rec)
	}
	if err != nil {
		return nil, err
	}
	if !existed {
		rec.CreatedFile = true
		if err := os.MkdirAll(filepath.Dir(step.file), 0o755); err != nil {
			return nil, err
		}
	}
	if err := writeFileAtomic(step.file, after, mode); err != nil {
		return nil, err
	}
	return rec, nil
}

func addClaudeRules(data []byte, existed bool, missing []string, rec *RulesRecord) ([]byte, error) {
	if !existed {
		rec.CreatedParent, rec.CreatedArray = true, true
		return renderJSONDocument([]string{"permissions", "ask"}, missing)
	}
	root, err := parseJSONC(data)
	if err != nil {
		return nil, err
	}
	perms, _ := root.member("permissions")
	if perms == nil {
		rec.CreatedParent, rec.CreatedArray = true, true
		return insertMember(data, root, "permissions", map[string]any{"ask": missing})
	}
	ask, _ := perms.value.member("ask")
	if ask == nil {
		rec.CreatedArray = true
		return insertMember(data, perms.value, "ask", missing)
	}
	return appendArrayStrings(data, ask.value, missing)
}

func addOpenCodeRules(data []byte, existed bool, missing []string, rec *RulesRecord) ([]byte, error) {
	if !existed {
		data = []byte("{}\n")
	}
	for _, key := range missing {
		root, err := parseJSONC(data)
		if err != nil {
			return nil, err
		}
		perm, _ := root.member("permission")
		if perm == nil {
			rec.CreatedParent = true
			data, err = insertMember(data, root, "permission", map[string]any{key: "ask"})
		} else {
			// A key the person set to another action is theirs; it is
			// left as it is (readRules counted only "ask" as present).
			if m, _ := perm.value.member(key); m != nil {
				continue
			}
			data, err = appendMember(data, perm.value, key, "ask")
		}
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

// removeRules takes back the recorded rules that are still as facet wire
// wrote them. It returns warnings for anything left in place.
func removeRules(rec *RulesRecord) ([]string, error) {
	if rec == nil || len(rec.Added) == 0 && !rec.CreatedParent {
		return nil, nil
	}
	data, err := os.ReadFile(rec.File)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(rec.File); err == nil {
		mode = info.Mode().Perm()
	}
	var warnings []string
	switch rec.Method {
	case RulesClaude:
		data, warnings, err = removeClaudeRules(data, rec)
	case RulesOpenCode:
		data, warnings, err = removeOpenCodeRules(data, rec)
	default:
		return nil, nil
	}
	if err != nil {
		return warnings, err
	}
	if rec.CreatedFile {
		if root, err := parseJSONC(data); err == nil && root.kind == '{' && len(root.members) == 0 && !hasComments(data) {
			return warnings, os.Remove(rec.File)
		}
	}
	return warnings, writeFileAtomic(rec.File, data, mode)
}

func removeClaudeRules(data []byte, rec *RulesRecord) ([]byte, []string, error) {
	ours := map[string]bool{}
	for _, rule := range rec.Added {
		ours[rule] = true
	}
next:
	for {
		root, err := parseJSONC(data)
		if err != nil {
			return data, nil, fmt.Errorf("cannot parse %s (%v); remove the ask rules for paid tools yourself", rec.File, err)
		}
		perms, permsIndex := root.member("permissions")
		if perms == nil || perms.value.kind != '{' {
			return data, nil, nil
		}
		if ask, askIndex := perms.value.member("ask"); ask != nil && ask.value.kind == '[' {
			for i, el := range ask.value.elems {
				if s, ok := stringValue(data, el.value); ok && ours[s] {
					data = removeArrayElem(data, ask.value, i)
					delete(ours, s)
					continue next
				}
			}
			if rec.CreatedArray && len(ask.value.elems) == 0 && !hasComments(data[ask.value.start:ask.value.end]) {
				data = removeMember(data, perms.value, askIndex)
				continue next
			}
		}
		if rec.CreatedParent && len(perms.value.members) == 0 && !hasComments(data[perms.value.start:perms.value.end]) {
			data = removeMember(data, root, permsIndex)
		}
		return data, nil, nil
	}
}

func removeOpenCodeRules(data []byte, rec *RulesRecord) ([]byte, []string, error) {
	var warnings []string
	for _, key := range rec.Added {
		root, err := parseJSONC(data)
		if err != nil {
			return data, warnings, fmt.Errorf("cannot parse %s (%v); remove the ask rules for paid tools yourself", rec.File, err)
		}
		perm, _ := root.member("permission")
		if perm == nil || perm.value.kind != '{' {
			break
		}
		m, index := perm.value.member(key)
		if m == nil {
			continue
		}
		if s, ok := stringValue(data, m.value); !ok || s != "ask" {
			warnings = append(warnings, fmt.Sprintf("permission.%s in %s changed after facet wire added it; it is left in place", key, rec.File))
			continue
		}
		data = removeMember(data, perm.value, index)
	}
	if rec.CreatedParent {
		if root, err := parseJSONC(data); err == nil {
			if perm, index := root.member("permission"); perm != nil && perm.value.kind == '{' && len(perm.value.members) == 0 &&
				!hasComments(data[perm.value.start:perm.value.end]) {
				data = removeMember(data, root, index)
			}
		}
	}
	return data, warnings, nil
}

// rulesState is ok when every recorded rule is in place, missing when one is
// gone, unknown when the settings cannot be read, skipped when the settings
// did not allow rules, absent when a CLI that takes rules has none recorded,
// and none when the CLI keeps no separate rules.
func rulesState(cli string, rec *RulesRecord) string {
	t, err := bundle.ParseTargets(cli)
	if err != nil || len(t) != 1 || !rulesSupported(t[0]) {
		return "none"
	}
	switch {
	case rec == nil:
		return "absent"
	case rec.Skipped != "":
		return "skipped"
	}
	present, _, skipped, err := readRules(rec.Method, rec.File)
	switch {
	case err != nil:
		return "unknown"
	case skipped != "":
		return "missing"
	}
	for _, rule := range rec.Added {
		if !present[rule] {
			return "missing"
		}
	}
	return "ok"
}
