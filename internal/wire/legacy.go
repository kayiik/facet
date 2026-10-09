package wire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Facet 1.x installed per project: a launcher and receipt under
// .facet-install/ and a copy of the core skill, extended with a
// "## This installation" section that points at run-facet, in the host CLI's
// project skill directory. Such a copy shadows the user-wide skill that
// facet wire installs, so it must be removed with the 1.1.0 installer.

// v1HostPaths are the skill roots the 1.x installer wrote, by host.
var v1HostPaths = []struct{ host, path string }{
	{"opencode", ".opencode/skills"},
	{"codex", ".agents/skills"},
	{"claude", ".claude/skills"},
	{"copilot", ".github/skills"},
	{"app", "skills"},
}

// V1Integration is one Facet 1.x project installation.
type V1Integration struct {
	// Project is the directory the 1.x installer was run for.
	Project string
	// Host is the CLI recorded by the 1.x installer, when known.
	Host string
	// Files are the receipts and marked skills that were found.
	Files []string
}

// IsV1Skill reports whether a SKILL.md is a 1.x per-project copy.
func IsV1Skill(content []byte) bool {
	return bytes.Contains(content, []byte("## This installation")) && bytes.Contains(content, []byte("run-facet"))
}

// FindV1Integrations looks for 1.x installations in each directory.
func FindV1Integrations(dirs ...string) []V1Integration {
	var found []V1Integration
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if seen[pathKey(dir)] {
			continue
		}
		seen[pathKey(dir)] = true
		v := V1Integration{Project: dir}
		for _, receipt := range []string{"installation.json", "installation.tsv"} {
			path := filepath.Join(dir, ".facet-install", receipt)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			v.Files = append(v.Files, path)
			if v.Host == "" {
				v.Host = receiptHost(receipt, data)
			}
		}
		for _, hp := range v1HostPaths {
			path := filepath.Join(dir, filepath.FromSlash(hp.path), "facet", "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil || !IsV1Skill(data) {
				continue
			}
			v.Files = append(v.Files, path)
			if v.Host == "" {
				v.Host = hp.host
			}
		}
		if len(v.Files) > 0 {
			found = append(found, v)
		}
	}
	return found
}

func receiptHost(name string, data []byte) string {
	if strings.HasSuffix(name, ".json") {
		var receipt struct {
			Host string `json:"host"`
		}
		if json.Unmarshal(bytes.TrimPrefix(data, utf8BOM), &receipt) == nil {
			return validHost(receipt.Host)
		}
		return ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "\t")
		if ok && key == "host" {
			return validHost(strings.TrimSpace(value))
		}
	}
	return ""
}

func validHost(host string) string {
	for _, hp := range v1HostPaths {
		if host == hp.host {
			return host
		}
	}
	return ""
}

// CleanupCommand is the 1.1.0 installer invocation that removes the
// installation, for goos ("windows" selects the PowerShell installer). It is
// run from the extracted 1.1.0 installer package.
func (v V1Integration) CleanupCommand(goos string) string {
	if goos == "windows" {
		cmd := `.\install.ps1 -Action uninstall`
		if v.Host != "" {
			cmd += " -Target " + v.Host
		}
		return cmd + " -ProjectDir " + psQuote(v.Project)
	}
	cmd := "bash install.sh --action uninstall"
	if v.Host != "" {
		cmd += " --target " + v.Host
	}
	return cmd + " --project " + shQuote(v.Project)
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// legacyHint explains a conflicting file that is a 1.x skill copy.
func legacyHint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || !IsV1Skill(data) {
		return ""
	}
	project := ""
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, hp := range v1HostPaths {
		suffix := "/" + hp.path + "/facet/SKILL.md"
		if strings.HasSuffix(clean, suffix) {
			project = filepath.FromSlash(strings.TrimSuffix(clean, suffix))
			break
		}
	}
	if project == "" {
		return "It is a Facet 1.x skill copy; remove it with the 1.1.0 installer's uninstall action."
	}
	return "It is a Facet 1.x installation; run `facet doctor` for the 1.1.0 uninstall command for " + project + "."
}
