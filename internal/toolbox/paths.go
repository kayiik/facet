package toolbox

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Project-relative explicit file arguments must not resolve against the server's
// working directory. URLs and prose remain unchanged, including nested clips.
func ProjectArguments(args map[string]any, workspace string) map[string]any {
	var walk func(string, any) any
	walk = func(key string, value any) any {
		switch v := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			for k, item := range v {
				out[k] = walk(k, item)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, item := range v {
				out[i] = walk(key, item)
			}
			return out
		case string:
			isPath := key == "input" || key == "output" || key == "source" || key == "src" || key == "video" || key == "path" || strings.HasSuffix(key, "_path") || strings.HasSuffix(key, "_dir") || strings.HasSuffix(key, "_file")
			if !isPath || v == "" || filepath.IsAbs(v) {
				return v
			}
			if u, err := url.Parse(v); err == nil && u.Scheme != "" {
				return v
			}
			return filepath.Join(workspace, filepath.FromSlash(v))
		default:
			return value
		}
	}
	return walk("", args).(map[string]any)
}
