// Package components installs the programs Facet's tools run beside the
// facet executable, under <runtime>/dependencies: FFmpeg, Node.js, the
// browser the renderers draw with, Remotion and HyperFrames, and Piper with a
// voice. Every download is pinned and checked against its SHA-256 before
// anything is unpacked; a supported copy already on the computer is used
// instead, and left alone.
//
// `facet components` reports them, `facet install` and `facet remove` change
// them, and `facet setup` asks which to install.
package components

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kayiik/facet/internal/toolbox"
)

// State is where a component stands on this computer.
type State string

const (
	// Installed: Facet installed it beside itself.
	Installed State = "installed"
	// System: a supported copy is on this computer already, and Facet uses it.
	System State = "system"
	// Missing: the tools that need it cannot run.
	Missing State = "missing"
	// Unavailable: Facet has nothing to install on this platform.
	Unavailable State = "unavailable"
)

// Component is something Facet's tools need, which `facet install` can add.
type Component struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enables string `json:"enables"`
	Size    string `json:"size"`
	// Default components are what `facet setup` offers to install first.
	Default bool `json:"default"`
	// Needs are the components this one runs on.
	Needs []string `json:"needs,omitempty"`
	// Version is what `facet install` installs.
	Version string `json:"version"`
	// Dir is where it is installed, under <runtime>/dependencies.
	Dir string `json:"-"`
}

// Status is a component and where it stands.
type Status struct {
	Component
	State State `json:"state"`
	// Path is the program or folder Facet's tools use.
	Path string `json:"path,omitempty"`
	// Found describes what is there, such as a version.
	Found string `json:"found,omitempty"`
	// Detail says why it is missing or where it came from.
	Detail string `json:"detail,omitempty"`
}

var platform = runtime.GOOS + "/" + runtime.GOARCH

func remotionVersion() string {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if data, err := composerFile("package.json"); err == nil && json.Unmarshal(data, &pkg) == nil {
		return pkg.Dependencies["remotion"]
	}
	return ""
}

func browserVersionHere() string {
	if v := browserVersions[platform]; v != "" {
		return v
	}
	return browserVersion
}

// All returns every component, in the order they install.
func All() []Component {
	return []Component{
		{ID: "ffmpeg", Name: "FFmpeg", Enables: "Every edit, probe, mix and render (FFmpeg and FFprobe)",
			Size: "about 120 MB", Default: true, Version: ffmpegVersion, Dir: "ffmpeg"},
		{ID: "node", Name: "Node.js", Enables: "Runs the Remotion and HyperFrames renderers",
			Size: "about 30 MB to download, 100 MB installed", Default: true, Version: nodeVersion, Dir: "node"},
		{ID: "browser", Name: "Browser", Enables: "Draws Remotion and HyperFrames frames (Chrome headless shell)",
			Size: "about 110 MB to download, 300 MB installed", Default: true, Version: browserVersionHere(), Dir: "browser"},
		{ID: "remotion", Name: "Remotion", Enables: "Animated explainers, captions, charts and titles (video_compose)",
			Size: "about 250 MB of npm packages", Default: true, Needs: []string{"node", "browser"}, Version: remotionVersion(), Dir: "remotion-composer"},
		{ID: "hyperframes", Name: "HyperFrames", Enables: "HTML and GSAP motion graphics (hyperframes_compose)",
			Size: "about 300 MB of npm packages", Default: true, Needs: []string{"node", "browser", "ffmpeg"}, Version: hyperframesVersion, Dir: "hyperframes"},
		{ID: "piper", Name: "Piper", Enables: "Offline narration with an English voice (piper_tts)",
			Size: "about 125 MB, and needs Python 3.9 or newer", Needs: []string{"ffmpeg"}, Version: piperVersion, Dir: "piper"},
	}
}

// Lookup returns the component with id.
func Lookup(id string) (Component, bool) {
	for _, c := range All() {
		if c.ID == strings.ToLower(strings.TrimSpace(id)) {
			return c, true
		}
	}
	return Component{}, false
}

// IDs lists every component id, in install order.
func IDs() []string {
	var ids []string
	for _, c := range All() {
		ids = append(ids, c.ID)
	}
	return ids
}

// Statuses reports every component.
func Statuses() []Status {
	var out []Status
	for _, c := range All() {
		out = append(out, StatusOf(c))
	}
	return out
}

// StatusOf reports one component, resolved exactly as Facet's tools resolve
// what they run.
func StatusOf(c Component) Status {
	s := Status{Component: c}
	if !supported(c.ID) {
		s.State, s.Detail = Unavailable, "Facet has no "+c.Name+" for "+platform
		return s
	}
	switch c.ID {
	case "ffmpeg":
		ffmpeg, errA := toolbox.ResolveProgram("ffmpeg")
		_, errB := toolbox.ResolveProgram("ffprobe")
		switch {
		case errA != nil || errB != nil:
			s.State, s.Detail = Missing, "ffmpeg and ffprobe are not both on this computer"
		default:
			s.State, s.Path, s.Found = origin(c, ffmpeg), ffmpeg, ffmpegVersionOf(ffmpeg)
		}
	case "node":
		node, err := toolbox.ResolveProgram("node")
		if err != nil {
			s.State, s.Detail = Missing, "no Node.js on this computer"
			break
		}
		version := strings.TrimPrefix(firstLine(node, "--version"), "v")
		s.Path, s.Found = node, version
		if major(version) < nodeMinimumMajor {
			s.State, s.Detail = Missing, fmt.Sprintf("Node.js %s is older than %d", version, nodeMinimumMajor)
			break
		}
		s.State = origin(c, node)
	case "browser":
		if browser := toolbox.PrivateBrowser(); browser != "" {
			s.State, s.Path = Installed, browser
		} else if browser := toolbox.BrowserExecutable(); browser != "" {
			s.State, s.Path, s.Detail = System, browser, "Remotion and HyperFrames draw with it"
		} else {
			s.State, s.Detail = Missing, "no headless shell or Chrome found"
		}
		if s.State == Installed {
			s.Found = receiptVersion(c)
		}
	case "remotion":
		dir, usable, _ := toolbox.ComposerStatus()
		switch {
		case usable:
			s.State, s.Path, s.Found = origin(c, dir), dir, packageVersion(filepath.Join(dir, "node_modules", "remotion"))
		case dir != "":
			s.State, s.Path, s.Detail = Missing, dir, "the composer's npm packages are not installed"
		default:
			s.State, s.Detail = Missing, "no Remotion composer beside this facet"
		}
	case "hyperframes":
		entry, err := toolbox.HyperFramesEntry()
		if err != nil {
			s.State, s.Detail = Missing, "HyperFrames is not installed"
			break
		}
		pkg := filepath.Dir(filepath.Dir(entry))
		s.State, s.Path, s.Found = origin(c, entry), entry, packageVersion(pkg)
	case "piper":
		piper, err := toolbox.ResolveProgram("piper")
		if err != nil {
			s.State, s.Detail = Missing, "Piper is not installed"
			if python, _ := findPython(context.Background()); len(python) == 0 {
				s.Detail += "; installing it needs Python 3.9 or newer"
			}
			break
		}
		s.State, s.Path = origin(c, piper), piper
	}
	return s
}

// origin tells a copy Facet installed from one already on the computer.
func origin(c Component, path string) State {
	dir := toolbox.RuntimeDependency(c.Dir)
	if dir == "" {
		return System
	}
	if rel, err := filepath.Rel(dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Installed
	}
	return System
}

func supported(id string) bool {
	switch id {
	case "node":
		return nodePins[platform] != nil
	case "ffmpeg":
		return ffmpegPins[platform] != nil
	case "browser":
		return browserPins[platform] != nil
	case "piper":
		// piper-tts publishes no wheels for Windows on Arm.
		return platform != "windows/arm64"
	}
	return true
}

func firstLine(program string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, program, args...).Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

var leadingNumber = regexp.MustCompile(`^\d+`)

// ffmpegVersionOf returns the version `ffmpeg -version` prints first.
func ffmpegVersionOf(ffmpeg string) string {
	fields := strings.Fields(firstLine(ffmpeg, "-version"))
	if len(fields) >= 3 && fields[1] == "version" {
		return fields[2]
	}
	return strings.Join(fields, " ")
}

func major(version string) int {
	n, _ := strconv.Atoi(leadingNumber.FindString(version))
	return n
}

func packageVersion(dir string) string {
	var pkg struct {
		Version string `json:"version"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

// ForDependency names the component that provides a tool dependency, as
// `facet tools list` reports it, or "" when none does (a credential, or a
// program Facet does not install).
func ForDependency(name string) string {
	switch name {
	case "ffmpeg", "ffprobe":
		return "ffmpeg"
	case "node":
		return "node"
	case "remotion-composer":
		return "remotion"
	case "hyperframes":
		return "hyperframes"
	case "piper":
		return "piper"
	}
	return ""
}

// Minimal drops every component another one in ids needs, since installing
// that one installs its needs too.
func Minimal(ids []string) []string {
	needed := map[string]bool{}
	for _, id := range ids {
		if c, ok := Lookup(id); ok {
			for _, n := range c.Needs {
				needed[n] = true
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if !needed[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
