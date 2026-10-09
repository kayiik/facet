package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/kayiik/facet/internal/facethome"
)

// RegistrySchema identifies the wiring registry format.
const RegistrySchema = "kayiik.facet.wiring/v1"

// Registry is wiring.json in Facet's home folder: what facet wire installed,
// where, and how the MCP server was registered. It is the ownership record:
// facet wire only replaces or removes what this registry says it wrote, and
// only while the content still matches the recorded digest.
type Registry struct {
	Schema  string    `json:"schema"`
	Wirings []*Wiring `json:"wirings"`
}

// Wiring is one CLI wired at one scope (and, for project scope, one project).
type Wiring struct {
	CLI     string `json:"cli"`
	Scope   string `json:"scope"`
	Project string `json:"project,omitempty"`
	// Root is the CLI's install root that received the skills and agent.
	Root         string `json:"root"`
	FacetVersion string `json:"facet_version"`
	// Executable is the facet executable registered as the MCP server.
	Executable string `json:"executable"`
	// ExplicitExecutable is set when --exe chose Executable: it is not
	// drift when the stable executable is another one, and --refresh keeps
	// it.
	ExplicitExecutable bool   `json:"explicit_executable,omitempty"`
	WiredAt            string `json:"wired_at"`
	// Pending marks a record saved before its changes were applied; a run
	// interrupted mid-way leaves it set, and both old and new digests are
	// then accepted as Facet's.
	Pending bool        `json:"pending,omitempty"`
	Files   []OwnedFile `json:"files"`
	// Dirs are directories facet wire created; --remove deletes them when
	// they are empty.
	Dirs []string   `json:"dirs,omitempty"`
	MCP  *MCPRecord `json:"mcp,omitempty"`
	// Rules are the approval rules facet wire added to the CLI's settings so
	// it asks before every tool that may charge (see rules.go).
	Rules *RulesRecord `json:"rules,omitempty"`
}

// OwnedFile is one installed file and the digest facet wire wrote.
type OwnedFile struct {
	Path   string   `json:"path"`
	Kind   string   `json:"kind"`
	Digest string   `json:"digest"`
	Alt    []string `json:"alt_digests,omitempty"`
}

func (f OwnedFile) owns(digest string) bool {
	if digest == f.Digest {
		return true
	}
	for _, alt := range f.Alt {
		if digest == alt {
			return true
		}
	}
	return false
}

// MCP registration methods.
const (
	// MethodCommand registers through the CLI's own non-interactive command.
	MethodCommand = "command"
	// MethodJSON merges one member into the CLI's JSON or JSONC config file.
	MethodJSON = "json-merge"
	// MethodTOML appends one table to the CLI's TOML config file.
	MethodTOML = "toml-append"
	// MethodCompa edits Compa's config.json: the server entry, MCP turned
	// on, the MCP call limit, and the ask rules, in one locked write.
	MethodCompa = "compa-config"
)

// MCPRecord is how the MCP server was registered, with enough detail to
// detect drift and to reverse exactly what was done.
type MCPRecord struct {
	Method  string   `json:"method"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// File is the configuration file holding the registration. For the
	// command method it is where the CLI is known to store it, read only to
	// detect drift.
	File string `json:"file,omitempty"`

	// Command method.
	CLI        string   `json:"cli,omitempty"`
	AddArgv    []string `json:"add_argv,omitempty"`
	RemoveArgv []string `json:"remove_argv,omitempty"`
	Dir        string   `json:"dir,omitempty"`

	// JSON merge: the member at KeyPath holds Value.
	KeyPath []string        `json:"key_path,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	// TOML append: the exact text appended.
	Block string `json:"block,omitempty"`

	CreatedFile   bool `json:"created_file,omitempty"`
	CreatedParent bool `json:"created_parent,omitempty"`

	// Compa: what facet wire changed besides the server entry, so --remove
	// restores each value only while it is still the one facet wire set.
	Compa *CompaChanges `json:"compa,omitempty"`
}

// CompaChanges are the settings facet wire changed in Compa's config.json
// next to the facet server entry.
type CompaChanges struct {
	// EnabledSet: tools.mcp.enabled was false (or absent, EnabledInserted)
	// and facet wire set it to true.
	EnabledSet      bool `json:"enabled_set,omitempty"`
	EnabledInserted bool `json:"enabled_inserted,omitempty"`
	// TimeoutSet: tools.mcp.call_timeout_seconds was 0 or absent
	// (TimeoutInserted) and facet wire set it to Timeout.
	TimeoutSet      bool `json:"timeout_set,omitempty"`
	TimeoutInserted bool `json:"timeout_inserted,omitempty"`
	Timeout         int  `json:"timeout,omitempty"`
	// CreatedServers: tools.mcp.servers did not exist.
	CreatedServers bool `json:"created_servers,omitempty"`
}

// RegistryPath returns wiring.json in Facet's home folder for the user whose
// home directory is home: ~/.facet/wiring.json, or $FACET_HOME/wiring.json.
func RegistryPath(home string) string {
	return filepath.Join(facethome.For(home), "wiring.json")
}

// LoadRegistry reads the registry. A missing registry is empty; an unreadable
// one is an error, because ownership cannot be decided without it.
func LoadRegistry(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Registry{Schema: RegistrySchema}, nil
	}
	if err != nil {
		return nil, err
	}
	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("%s does not decode (%v); move it aside only if you know what it recorded", path, err)
	}
	if reg.Schema != RegistrySchema {
		return nil, fmt.Errorf("%s has schema %q, want %q", path, reg.Schema, RegistrySchema)
	}
	var kept []*Wiring
	for _, w := range reg.Wirings {
		if w != nil {
			kept = append(kept, w)
		}
	}
	reg.Wirings = kept
	return &reg, nil
}

// Save writes the registry atomically.
func (r *Registry) Save(path string) error {
	r.Schema = RegistrySchema
	sort.SliceStable(r.Wirings, func(i, j int) bool {
		a, b := r.Wirings[i], r.Wirings[j]
		if a.CLI != b.CLI {
			return a.CLI < b.CLI
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.Project < b.Project
	})
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, append(raw, '\n'), 0o644)
}

// find returns the wiring for cli at scope and project, or nil.
func (r *Registry) find(cli, scope, project string) *Wiring {
	for _, w := range r.Wirings {
		if w.CLI == cli && w.Scope == scope && samePath(w.Project, project) {
			return w
		}
	}
	return nil
}

// put replaces or adds w.
func (r *Registry) put(w *Wiring) {
	for i, existing := range r.Wirings {
		if existing.CLI == w.CLI && existing.Scope == w.Scope && samePath(existing.Project, w.Project) {
			r.Wirings[i] = w
			return
		}
	}
	r.Wirings = append(r.Wirings, w)
}

// drop removes the wiring for cli at scope and project.
func (r *Registry) drop(cli, scope, project string) {
	var kept []*Wiring
	for _, w := range r.Wirings {
		if !(w.CLI == cli && w.Scope == scope && samePath(w.Project, project)) {
			kept = append(kept, w)
		}
	}
	r.Wirings = kept
}

// label names a wiring for people.
func (w *Wiring) label() string {
	if w.Scope == "project" {
		return fmt.Sprintf("%s (project %s)", w.CLI, w.Project)
	}
	return fmt.Sprintf("%s (%s)", w.CLI, w.Scope)
}

// pathKey normalises a path for comparison: Windows paths are
// case-insensitive.
func pathKey(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func samePath(a, b string) bool { return pathKey(a) == pathKey(b) }

// writeFileAtomic writes content to a temporary file beside path and renames
// it into place, so readers see the old or the new file, never a mix.
func writeFileAtomic(path string, content []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".facet-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
