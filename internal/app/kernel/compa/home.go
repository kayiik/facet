package compa

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
)

// The App keeps a private Compa home (COMPA_HOME), so the kernel never reads
// or writes a Compa the person installed themselves. Prepare brings that home
// to the state the App needs, and is safe to run on every start:
//
//   - `compa-kernel onboard` creates it the first time;
//   - `facet wire compa --exe` wires the App's own Toolkit in: the skills, the
//     facet MCP server, the MCP call limit and the ask rules for paid tools;
//   - config.json gets the web channel (its token comes from the environment
//     at each start, never the file), the hook process that carries approvals
//     to the window, and the model the person chose;
//   - model_catalogs.json and auth.json describe that model and its key;
//   - AGENT.md gives the agent its identity.
//
// Compa rejects unknown keys and rewrites the whole file when it saves, so
// the App sets only keys Compa defines and writes the decoded object back.

// Model is an OpenAI-compatible model the agent uses.
type Model struct {
	Endpoint string `json:"endpoint"` // base URL, such as https://api.openai.com/v1
	APIKey   string `json:"-"`
	ID       string `json:"id"` // the model's id at the endpoint
}

// Instance is the provider instance the App configures.
const Instance = "app"

// HookName is the hook process's name in config.json.
const HookName = "facet-app"

// observed are the runtime events the hook relays to the window.
var observed = []string{"agent.turn.start", "agent.turn.end", "mcp.tool.call.progress"}

// HomeOptions say what Prepare sets up.
type HomeOptions struct {
	Kernel      string   // compa-kernel executable
	Facet       string   // the App's facet executable
	Home        string   // COMPA_HOME
	FacetHome   string   // FACET_HOME
	HookCommand []string // starts the hook relay
	Agent       string   // AGENT.md content; empty leaves it alone
	Model       *Model   // nil leaves the model as configured
	// ApprovalWait is how long the window may take to answer an approval;
	// the kernel's hook limit is set a minute beyond it.
	ApprovalWait time.Duration
}

func (o HomeOptions) config() string { return filepath.Join(o.Home, "config.json") }

// Prepare brings the private home up to date.
func Prepare(ctx context.Context, o HomeOptions) error {
	if err := os.MkdirAll(o.Home, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(o.config()); errors.Is(err, fs.ErrNotExist) {
		if out, err := run(ctx, o, o.Kernel, "onboard"); err != nil {
			return fmt.Errorf("compa-kernel onboard: %v: %s", err, out)
		}
	}
	if out, err := run(ctx, o, o.Facet, "wire", "compa", "--exe", o.Facet); err != nil {
		return fmt.Errorf("facet wire compa: %v: %s", err, out)
	}
	cfg, err := readConfig(o.config())
	if err != nil {
		return err
	}
	workspace, err := patchConfig(cfg, o)
	if err != nil {
		return err
	}
	if err := writeConfig(o.config(), cfg); err != nil {
		return err
	}
	if o.Model != nil {
		if err := writeModel(o.Home, *o.Model); err != nil {
			return err
		}
	}
	if o.Agent != "" {
		path := filepath.Join(workspace, "AGENT.md")
		if old, err := os.ReadFile(path); err != nil || string(old) != o.Agent {
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(o.Agent), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// Workspace returns the kernel's workspace: where the agent works, where its
// skills are, and the root Facet's MCP server keeps files inside.
func Workspace(home string) (string, error) {
	cfg, err := readConfig(filepath.Join(home, "config.json"))
	if err != nil {
		return "", err
	}
	return workspaceOf(cfg, home), nil
}

func workspaceOf(cfg map[string]any, home string) string {
	if ws, _ := dig(cfg, "agents", "defaults", "workspace").(string); ws != "" {
		if strings.HasPrefix(ws, "~") {
			if userHome, err := os.UserHomeDir(); err == nil {
				ws = filepath.Join(userHome, strings.TrimPrefix(strings.TrimPrefix(ws, "~"), string(filepath.Separator)))
			}
		}
		if !filepath.IsAbs(ws) {
			ws = filepath.Join(home, ws)
		}
		return filepath.Clean(ws)
	}
	return filepath.Join(home, "workspace")
}

// ConfiguredModel returns the model config.json names, or "" when none.
func ConfiguredModel(home string) string {
	cfg, err := readConfig(filepath.Join(home, "config.json"))
	if err != nil {
		return ""
	}
	name, _ := dig(cfg, "agents", "defaults", "model_name").(string)
	return name
}

// run runs a command against the private home only: every inherited COMPA_
// setting is dropped, so the person's own Compa configuration never applies.
func run(ctx context.Context, o HomeOptions, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = Env(o.Home, o.FacetHome, filepath.Dir(o.Facet), filepath.Dir(o.Kernel),
		// facet wire compa checks the result with compa-kernel, which loads a
		// config whose web channel takes its token from the environment.
		"COMPA_CHANNELS_WEB_TOKEN=prepare")
	cmd.Dir = o.Home
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Env is the environment the App runs the kernel and its own Facet with:
// the person's environment without any COMPA_ or FACET_HOME setting, the
// private homes, and the App's Toolkit first on PATH, so the shell route runs
// the App's facet and never another one.
func Env(home, facetHome, facetBin, kernelDir string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "COMPA_") || upper == "FACET_HOME" || upper == "PATH" {
			continue
		}
		env = append(env, kv)
	}
	path := facetBin + string(os.PathListSeparator) + kernelDir + string(os.PathListSeparator) + os.Getenv("PATH")
	env = append(env, "COMPA_HOME="+home, "FACET_HOME="+facetHome, "PATH="+path)
	return append(env, extra...)
}

func readConfig(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %v", path, err)
	}
	return cfg, nil
}

func writeConfig(path string, cfg map[string]any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return writeAtomic(path, data, 0o600)
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func dig(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[k]
	}
	return cur
}

// object returns m[key] as an object, creating it when absent.
func object(m map[string]any, key string) (map[string]any, error) {
	switch v := m[key].(type) {
	case map[string]any:
		return v, nil
	case nil:
		obj := map[string]any{}
		m[key] = obj
		return obj, nil
	default:
		return nil, fmt.Errorf("%q in config.json is not an object", key)
	}
}

// patchConfig sets what the App needs and returns the workspace.
func patchConfig(cfg map[string]any, o HomeOptions) (string, error) {
	channels, err := object(cfg, "channel_list")
	if err != nil {
		return "", err
	}
	// A channel entry replaces Compa's default entry entirely, so streaming
	// is set explicitly. The token comes from COMPA_CHANNELS_WEB_TOKEN.
	channels["web"] = map[string]any{"enabled": true, "type": "web", "settings": map[string]any{
		"streaming": map[string]any{"enabled": true}, "max_connections": 16}}

	hooks, err := object(cfg, "hooks")
	if err != nil {
		return "", err
	}
	hooks["enabled"] = true
	defaults, err := object(hooks, "defaults")
	if err != nil {
		return "", err
	}
	wait := o.ApprovalWait
	if wait <= 0 {
		wait = 10 * time.Minute
	}
	defaults["approval_timeout_ms"] = (wait + time.Minute).Milliseconds()
	processes, err := object(hooks, "processes")
	if err != nil {
		return "", err
	}
	command := make([]any, len(o.HookCommand))
	for i, part := range o.HookCommand {
		command[i] = part
	}
	observe := make([]any, len(observed))
	for i, kind := range observed {
		observe[i] = kind
	}
	processes[HookName] = map[string]any{"enabled": true, "priority": 0, "transport": "stdio",
		"command": command, "observe": observe, "intercept": []any{"approve_tool", "before_tool"}}

	if m := o.Model; m != nil {
		instance := map[string]any{"id": Instance, "provider_kind": "custom_openai", "adapter": "openai-compatible",
			"protocol": "openai", "endpoint": strings.TrimRight(m.Endpoint, "/"), "state": "enabled",
			"runtime": map[string]any{"streaming": true}}
		if m.APIKey != "" {
			instance["auth_connection_ref"] = "credential:" + Instance
		}
		var instances []any
		if list, ok := cfg["provider_instances"].([]any); ok {
			for _, item := range list {
				if obj, ok := item.(map[string]any); ok && obj["id"] == Instance {
					continue
				}
				instances = append(instances, item)
			}
		}
		cfg["provider_instances"] = append(instances, instance)
		agents, err := object(cfg, "agents")
		if err != nil {
			return "", err
		}
		agentDefaults, err := object(agents, "defaults")
		if err != nil {
			return "", err
		}
		agentDefaults["model_name"] = Instance + "/" + m.ID
	}
	return workspaceOf(cfg, o.Home), nil
}

// writeModel writes the model catalog entry and the API key.
func writeModel(home string, m Model) error {
	catalogPath := filepath.Join(home, "model_catalogs.json")
	catalog := map[string]any{}
	if raw, err := os.ReadFile(catalogPath); err == nil {
		_ = json.Unmarshal(raw, &catalog)
	}
	entries, _ := catalog["entries"].(map[string]any)
	if entries == nil {
		entries = map[string]any{}
		catalog["entries"] = entries
	}
	entries[Instance] = map[string]any{"id": Instance, "instance_id": Instance, "provider": "custom_openai",
		"api_base": strings.TrimRight(m.Endpoint, "/"), "models": []any{map[string]any{"id": m.ID}},
		"fetched_at": time.Now().UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(catalogPath, append(data, '\n'), 0o600); err != nil {
		return err
	}

	authPath := filepath.Join(home, "auth.json")
	auth := map[string]any{}
	if raw, err := os.ReadFile(authPath); err == nil {
		_ = json.Unmarshal(raw, &auth)
	}
	creds, _ := auth["credentials"].(map[string]any)
	if creds == nil {
		creds = map[string]any{}
		auth["credentials"] = creds
	}
	if m.APIKey == "" {
		delete(creds, Instance)
	} else {
		creds[Instance] = map[string]any{"access_token": m.APIKey, "provider": "custom_openai", "auth_method": "api_key"}
	}
	data, err = json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(authPath, append(data, '\n'), 0o600)
}
