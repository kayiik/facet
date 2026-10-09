package toolbox

import (
	"context"
	"path/filepath"
)

// workDirKey carries the folder a call's default outputs are written to.
type workDirKey struct{}

// WithWorkDir returns a context whose tool calls write their default outputs
// (the file a tool names itself when a request names none) inside dir rather
// than in the process's working directory.
//
// The MCP server sets it to the call's allowed root: it confines every path a
// request names, and without this a default output would still land wherever
// the server happened to be started, outside that root. The CLI sets nothing,
// so a person's default outputs stay in their working directory.
func WithWorkDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workDirKey{}, dir)
}

// defaultOutput places a tool's own default output name in the call's working
// folder, or leaves it relative to the process's working directory when the
// call has none.
func defaultOutput(ctx context.Context, name string) string {
	if ctx == nil || name == "" || filepath.IsAbs(name) {
		return name
	}
	if dir, ok := ctx.Value(workDirKey{}).(string); ok && dir != "" {
		return filepath.Join(dir, name)
	}
	return name
}

// workDirOf returns the call's working folder, or "" when it has none.
func workDirOf(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	dir, _ := ctx.Value(workDirKey{}).(string)
	return dir
}
