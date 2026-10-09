package mcpserver

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/kayiik/facet/internal/toolbox"
)

// The path policy resolves every path argument of a tool call against one
// allowed root and holds every OUTPUT inside it. It runs in the server, before
// the tool, because once a file has been written outside the root no error can
// undo it. Inputs may be read from anywhere the operator points.
//
// Which fields hold paths is decided exactly as toolbox.ProjectArguments
// decides it, so the CLI's project resolution and the server cannot disagree.
// A relative path resolves against the root and every path is rewritten to its
// clean absolute form. An output that lands outside the root, through "..", an
// absolute location or a link in its existing part, is refused naming the root.

// extraPathKeys are fields the toolbox reads as files although
// ProjectArguments does not recognise their names: video_stitch's clips and
// source_edit's replacement_audio. Without them a clip could be read from
// anywhere, relative to the server's working directory.
var extraPathKeys = map[string]bool{"clips": true, "replacement_audio": true}

// remoteMediaKeys are the fields whose tools accept remote or inline media:
// video_compose cut sources and audio track sources. Everywhere else a path
// argument must be local, because a URL handed to FFmpeg as an input or an
// output reaches the network from a tool that declares it never does.
var remoteMediaKeys = map[string]bool{"source": true, "src": true}

var remoteMediaSchemes = map[string]bool{"http": true, "https": true, "data": true}

var pathKeys sync.Map // key -> bool, memoised projectPathKey answers

// isPathKey reports whether a field named key holds a file path.
func isPathKey(key string) bool {
	if key == "" {
		return false
	}
	if extraPathKeys[key] {
		return true
	}
	if known, ok := pathKeys.Load(key); ok {
		return known.(bool)
	}
	answer := projectPathKey(key)
	pathKeys.Store(key, answer)
	return answer
}

// projectPathKey asks toolbox.ProjectArguments whether it treats key as a
// file argument, instead of restating its rules here: a relative path under
// a path key is joined to the workspace, any other value comes back as given.
func projectPathKey(key string) bool {
	const probe = "facet-path-probe"
	projected := toolbox.ProjectArguments(map[string]any{key: probe}, "workspace")
	value, _ := projected[key].(string)
	return value != probe
}

// rootSource yields the allowed root. Calls resolve it on first use, so a
// call that neither names a path nor may write never waits on, or fails for,
// the client's roots.
type rootSource func() (string, error)

func fixedRoot(root string) rootSource { return func() (string, error) { return root, nil } }

// pathRefusal explains why a path argument was refused.
type pathRefusal struct {
	at       string // where the argument sits in the request, e.g. segments[1].input
	value    string // the value as the caller gave it
	resolved string // where it would land, when that is known
	reason   string // completes "path argument <at> (<value>) ... <root>"
	root     string // the allowed root it was checked against
	rootErr  error  // why the allowed root could not be determined
}

// code is the envelope error code: the request is at fault unless the root
// itself could not be determined.
func (r *pathRefusal) code() string {
	if r.rootErr != nil {
		return "root_unavailable"
	}
	return "invalid_request"
}

func (r *pathRefusal) message() string {
	if r.rootErr != nil {
		return fmt.Sprintf("path argument %s (%q) cannot be checked because the allowed root is unavailable: %v", r.at, r.value, r.rootErr)
	}
	return fmt.Sprintf("path argument %s (%q) %s %s", r.at, r.value, r.reason, r.root)
}

func (r *pathRefusal) details() map[string]any {
	details := map[string]any{"argument": r.at, "path": r.value}
	if r.root != "" {
		details["root"] = r.root
	}
	if r.resolved != "" {
		details["resolved"] = r.resolved
	}
	return details
}

// confineArguments returns a copy of a tool request with every path argument
// resolved inside the allowed root, or the first argument that cannot be.
func confineArguments(root rootSource, args map[string]any) (map[string]any, *pathRefusal) {
	confined, refusal := confineValue(root, "", "", args)
	if refusal != nil {
		return nil, refusal
	}
	return confined.(map[string]any), nil
}

// confineValue walks a request as ProjectArguments does: an object's members
// are judged by their own names and an array's items inherit the array's.
func confineValue(root rootSource, key, at string, value any) (any, *pathRefusal) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for _, name := range sortedKeys(v) {
			item, refusal := confineValue(root, name, joinField(at, name), v[name])
			if refusal != nil {
				return nil, refusal
			}
			out[name] = item
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			confined, refusal := confineValue(root, key, fmt.Sprintf("%s[%d]", at, i), item)
			if refusal != nil {
				return nil, refusal
			}
			out[i] = confined
		}
		return out, nil
	case string:
		if !isPathKey(key) {
			return v, nil
		}
		return confinePath(root, key, at, v)
	default:
		return value, nil
	}
}

func joinField(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// confinePath resolves one path argument. It returns what the tool receives:
// the clean absolute path that was checked, or a remote media URL as given.
func confinePath(rootOf rootSource, key, at, value string) (string, *pathRefusal) {
	if strings.TrimSpace(value) == "" {
		return value, nil // the tool reports a missing path in its own words
	}
	root, err := rootOf()
	if err != nil {
		return "", &pathRefusal{at: at, value: value, rootErr: err}
	}
	refuse := func(resolved, reason string) (string, *pathRefusal) {
		return "", &pathRefusal{at: at, value: value, resolved: resolved, reason: reason, root: root}
	}
	var local string
	switch {
	case filepath.IsAbs(value):
		local = value
	case filepath.VolumeName(value) != "":
		// "C:file" names the current directory of drive C, which no root
		// can vouch for.
		return refuse("", "is relative to a drive's current directory instead of the allowed root")
	default:
		scheme, isURL := urlScheme(value)
		switch {
		case !isURL:
			local = filepath.Join(root, filepath.FromSlash(value))
		case scheme == "file":
			p, err := fileURLPath(value)
			if err != nil {
				return refuse("", "is not a usable local file URL ("+err.Error()+"); give a path inside the allowed root")
			}
			local = p
		case remoteMediaKeys[key] && remoteMediaSchemes[scheme]:
			return value, nil
		default:
			return refuse("", "is a URL, but this field takes a local path inside the allowed root")
		}
	}
	local = filepath.Clean(local)
	if strings.HasPrefix(local, `\\`) || strings.HasPrefix(local, "//") {
		// A network path is refused without being touched: on Windows even
		// inspecting a UNC path connects, and authenticates, to its host.
		return refuse(local, "is a network path, outside the allowed root; copy the file to this machine first")
	}
	if !isOutputKey(key) {
		// Inputs may be read from anywhere the operator points: footage, a
		// music library or a brand folder often live outside the project.
		// Only what a tool writes is held inside the allowed root.
		return local, nil
	}
	if !strings.EqualFold(filepath.VolumeName(local), filepath.VolumeName(root)) {
		// Another drive or a network share is outside the root, and is
		// refused without touching it: on Windows even inspecting a UNC
		// path connects, and authenticates, to its host.
		return refuse(local, "resolves to "+local+", outside the allowed root")
	}
	real, err := realPath(local)
	if err != nil {
		return refuse(local, "cannot be checked ("+err.Error()+") against the allowed root")
	}
	if reason := outside(root, real); reason != "" {
		return refuse(real, reason)
	}
	return local, nil
}

// outputKeys name the fields a tool writes to. Every other path field is an
// input, which may point anywhere; an output must land inside the allowed root.
var outputKeys = map[string]bool{"evidence_dir": true, "workspace_path": true, "timing_path": true}

func isOutputKey(key string) bool {
	k := strings.ToLower(key)
	return strings.HasPrefix(k, "output") || outputKeys[k]
}

// outside explains why path is not inside root, or returns "".
func outside(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "resolves to " + path + ", outside the allowed root"
	}
	if !filepath.IsLocal(rel) {
		return "names a reserved device or stream rather than a file inside the allowed root"
	}
	return ""
}

// urlScheme reports the lower-cased scheme of a value that starts like a URL
// ("scheme:..."). A Windows drive letter is a volume, never a scheme.
func urlScheme(value string) (string, bool) {
	i := strings.IndexByte(value, ':')
	if i < 1 {
		return "", false
	}
	if runtime.GOOS == "windows" && i == 1 {
		return "", false
	}
	for j := 0; j < i; j++ {
		c := value[j]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case j > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return "", false
		}
	}
	return strings.ToLower(value[:i]), true
}

// fileURLPath converts a file URL to an absolute local path: file:///C:/x on
// Windows, file:///x elsewhere, with an optional "localhost" host. A Windows
// UNC host becomes \\host\share.
func fileURLPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Scheme, "file") {
		return "", errors.New("not a file URL")
	}
	if u.Opaque != "" {
		return "", errors.New("a file URL must be absolute")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("a file URL must not carry credentials, a query or a fragment")
	}
	p := u.Path
	if p == "" {
		return "", errors.New("empty path")
	}
	if host := u.Host; host != "" && !strings.EqualFold(host, "localhost") {
		if runtime.GOOS != "windows" {
			return "", fmt.Errorf("remote host %q", host)
		}
		return filepath.Clean(`\\` + host + filepath.FromSlash(p)), nil
	}
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' && isLetter(p[1]) {
		p = p[1:] // /C:/x -> C:/x
	}
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) {
		return "", errors.New("not an absolute path")
	}
	return filepath.Clean(p), nil
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// resolveRoot turns a directory into the allowed root: absolute, with its
// links followed, and required to exist as a directory.
func resolveRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	real, err := realPath(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", real)
	}
	return real, nil
}

const maxLinks = 40

// lstat inspects one path element. A variable so tests can observe which
// paths the policy touches.
var lstat = os.Lstat

// realPath returns where an absolute, clean path lands: links in its existing
// part are followed, including Windows junctions, which
// filepath.EvalSymlinks refuses to traverse. The part that does not exist
// yet is appended as given; it cannot contain a link.
func realPath(path string) (string, error) {
	base, missing, err := followLinks(path, 0)
	if err != nil {
		return "", err
	}
	// The existing part is now free of links; EvalSymlinks normalises it
	// (case and short names on Windows).
	if normal, err := filepath.EvalSymlinks(base); err == nil {
		base = normal
	}
	return filepath.Join(append([]string{base}, missing...)...), nil
}

// followLinks walks path element by element as the filesystem would. It
// returns the real location of the longest existing prefix and the elements
// after it, none of which exist.
func followLinks(path string, depth int) (string, []string, error) {
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	elements := strings.Split(path[len(volume):], string(filepath.Separator))
	for i, element := range elements {
		if element == "" {
			continue
		}
		next := filepath.Join(current, element)
		info, err := lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return current, nonEmpty(elements[i:]), nil
		}
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			target, linkErr := os.Readlink(next)
			if linkErr == nil {
				if depth >= maxLinks {
					return "", nil, errors.New("too many levels of links")
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(current, target)
				}
				real, missing, err := followLinks(filepath.Clean(target), depth+1)
				if err != nil {
					return "", nil, err
				}
				if len(missing) > 0 {
					// A link to something that does not exist yet.
					return real, append(missing, nonEmpty(elements[i+1:])...), nil
				}
				current = real
				continue
			}
			if info.Mode()&fs.ModeSymlink != 0 {
				return "", nil, linkErr
			}
			// A reparse point that is not a link, such as a cloud
			// placeholder, is an ordinary entry.
		}
		current = next
	}
	return current, nil, nil
}

func nonEmpty(elements []string) []string {
	var out []string
	for _, e := range elements {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}
