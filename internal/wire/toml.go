package wire

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kayiik/facet/internal/bundle"
	"github.com/kayiik/facet/internal/toolbox"
)

// Minimal TOML support for one job: find, add, and remove a single
// [mcp_servers.<name>] table without rewriting anything else in the file.

// tomlString renders s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlServerBlock renders the table that registers the MCP server: its
// command; the environment variables Codex must forward, because it starts
// MCP servers with only a short allowlist (PATH, the home and temporary
// folders) and a provider key the person exported would otherwise never reach
// the tool; the call limit (bundle.CodexToolTimeoutSec); and an approval_mode
// = "prompt" table for each tool that may charge, so Codex asks before every
// paid call.
func tomlServerBlock(name, command string, args []string) string {
	quote := func(list []string) string {
		quoted := make([]string, len(list))
		for i, a := range list {
			quoted[i] = tomlString(a)
		}
		return strings.Join(quoted, ", ")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Facet MCP server, managed by facet wire\n[mcp_servers.%s]\ncommand = %s\nargs = [%s]\nenv_vars = [%s]\ntool_timeout_sec = %d\n",
		name, tomlString(command), quote(args), quote(toolbox.EnvVars()), bundle.CodexToolTimeoutSec)
	for _, tool := range paidTools() {
		fmt.Fprintf(&b, "\n[mcp_servers.%s.tools.%s]\napproval_mode = \"prompt\"\n", name, tool)
	}
	return b.String()
}

// tomlLine is one logical line with its comment removed.
type tomlLine struct {
	text string // trimmed, comment removed
}

// tomlLines splits content into logical lines, skipping the bodies of
// multi-line strings so their contents are never mistaken for keys.
func tomlLines(content string) []tomlLine {
	var out []tomlLine
	inMulti := ""
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, "\r")
		if inMulti != "" {
			if strings.Count(line, inMulti)%2 == 1 {
				inMulti = ""
			}
			continue
		}
		text := strings.TrimSpace(stripTOMLComment(line))
		for _, delim := range []string{`"""`, `'''`} {
			if strings.Count(text, delim)%2 == 1 {
				inMulti = delim
			}
		}
		out = append(out, tomlLine{text: text})
	}
	return out
}

// stripTOMLComment removes a trailing # comment outside quotes.
func stripTOMLComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == '"' && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && c == '#':
			return line[:i]
		}
	}
	return line
}

// splitTOMLKey splits a dotted key into its unquoted parts.
func splitTOMLKey(key string) []string {
	var parts []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == '"' && c == '\\' && i+1 < len(key):
			i++
			cur.WriteByte(key[i])
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && c == '.':
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		case quote == 0 && (c == ' ' || c == '\t'):
		default:
			cur.WriteByte(c)
		}
	}
	return append(parts, strings.TrimSpace(cur.String()))
}

// splitTOMLAssignment returns the key and value of a key = value line.
func splitTOMLAssignment(text string) (string, string, bool) {
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && c == '=':
			return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:]), true
		}
	}
	return "", "", false
}

func hasPathPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// tomlServerState describes how a TOML config defines mcp_servers.<name>.
type tomlServerState struct {
	defined bool // any key or table at or below mcp_servers.<name>
	// inlineParent is set when mcp_servers itself is assigned a value, so a
	// [mcp_servers.<name>] table cannot be appended.
	inlineParent bool
	command      string
	args         []string
	parsed       bool // command and args were read from a [mcp_servers.<name>] table
}

// inspectTOMLServer reports how content defines mcp_servers.<name>.
func inspectTOMLServer(content, name string) tomlServerState {
	var st tomlServerState
	want := []string{"mcp_servers", name}
	var table []string
	lines := tomlLines(content)
	for i := 0; i < len(lines); i++ {
		text := lines[i].text
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") {
			header := strings.TrimSpace(strings.Trim(text, "[]"))
			table = splitTOMLKey(header)
			if hasPathPrefix(table, want) {
				st.defined = true
			}
			continue
		}
		key, value, ok := splitTOMLAssignment(text)
		if !ok {
			continue
		}
		full := append(append([]string(nil), table...), splitTOMLKey(key)...)
		if len(full) == 1 && full[0] == "mcp_servers" {
			st.inlineParent = true
		}
		if !hasPathPrefix(full, want) {
			continue
		}
		st.defined = true
		if len(table) == 2 && hasPathPrefix(table, want) {
			// Collect a value that may span lines (arrays).
			for strings.Count(value, "[") > strings.Count(value, "]") && i+1 < len(lines) {
				i++
				value += " " + lines[i].text
			}
			switch strings.Join(splitTOMLKey(key), ".") {
			case "command":
				if s, ok := parseTOMLString(value); ok {
					st.command = s
					st.parsed = true
				}
			case "args":
				if list, ok := parseTOMLStringArray(value); ok {
					st.args = list
				}
			}
		}
	}
	return st
}

// parseTOMLString decodes a basic or literal single-line string.
func parseTOMLString(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' && !strings.HasPrefix(v, "'''") {
		return v[1 : len(v)-1], true
	}
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' || strings.HasPrefix(v, `"""`) {
		return "", false
	}
	body := v[1 : len(v)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(body) {
			return "", false
		}
		i++
		switch body[i] {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'u', 'U':
			n := 4
			if body[i] == 'U' {
				n = 8
			}
			if i+1+n > len(body) {
				return "", false
			}
			code, err := strconv.ParseUint(body[i+1:i+1+n], 16, 32)
			if err != nil || !utf8.ValidRune(rune(code)) {
				return "", false
			}
			b.WriteRune(rune(code))
			i += n
		default:
			return "", false
		}
	}
	return b.String(), true
}

// parseTOMLStringArray decodes a single-line array of strings.
func parseTOMLStringArray(v string) ([]string, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 2 || v[0] != '[' || v[len(v)-1] != ']' {
		return nil, false
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	out := []string{}
	for inner != "" {
		var item string
		var rest string
		switch inner[0] {
		case '"':
			end := 1
			for end < len(inner) && inner[end] != '"' {
				if inner[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(inner) {
				return nil, false
			}
			item, rest = inner[:end+1], inner[end+1:]
		case '\'':
			end := strings.IndexByte(inner[1:], '\'')
			if end < 0 {
				return nil, false
			}
			item, rest = inner[:end+2], inner[end+2:]
		default:
			return nil, false
		}
		s, ok := parseTOMLString(item)
		if !ok {
			return nil, false
		}
		out = append(out, s)
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, ",") {
			rest = strings.TrimSpace(rest[1:])
		} else if rest != "" {
			return nil, false
		}
		inner = rest
	}
	return out, true
}
