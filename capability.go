// Package facet contains the production knowledge every Facet host shares: the
// guide, role agents, pipelines, stage guides, stances, craft and vendor
// knowledge, styles and record schemas. It is compiled into the binary, so a
// projection or a read never depends on the working directory.
package facet

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Assets holds every bundled file. Vendor knowledge keeps its own folder
// layout, so guidance is embedded with all: to include every file in it.
//
//go:embed skills agents pipelines all:guidance styles schemas
var Assets embed.FS

// GuidanceRoots are the top-level folders the guidance reader serves.
var GuidanceRoots = []string{"skills", "agents", "pipelines", "guidance", "styles", "schemas"}

var guidanceExtensions = map[string]bool{".md": true, ".json": true, ".yaml": true}

// Guidance returns one bundled file by its path, for example
// "guidance/stages/script.md" or "pipelines/animated-explainer.yaml".
func Guidance(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimPrefix(name, "/")
	if !fs.ValidPath(name) || !guidanceExtensions[path.Ext(name)] || !underRoot(name) {
		return "", fmt.Errorf("%q is not a bundled Facet guidance file; paths start with %s and end in .md, .json or .yaml", name, strings.Join(GuidanceRoots, "/, ")+"/")
	}
	data, err := Assets.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("no bundled Facet guidance file %q", name)
	}
	return string(data), nil
}

// GuidanceFiles lists every bundled guidance file under prefix ("" for all),
// sorted.
func GuidanceFiles(prefix string) []string {
	prefix = strings.Trim(strings.ReplaceAll(prefix, "\\", "/"), "/")
	var out []string
	for _, root := range GuidanceRoots {
		_ = fs.WalkDir(Assets, root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !guidanceExtensions[path.Ext(name)] {
				return nil
			}
			if prefix == "" || name == prefix || strings.HasPrefix(name, prefix+"/") {
				out = append(out, name)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

func underRoot(name string) bool {
	for _, root := range GuidanceRoots {
		if strings.HasPrefix(name, root+"/") {
			return true
		}
	}
	return false
}
