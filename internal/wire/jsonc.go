package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// A minimal JSON-with-comments reader that records byte offsets, so a
// configuration file can be changed by inserting or deleting exactly one
// member while every other byte -- comments, ordering, formatting -- stays as
// the user wrote it. It accepts JSON plus // and /* */ comments and trailing
// commas, the dialect of OpenCode's opencode.jsonc.

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type jnode struct {
	kind    byte // '{', '[', '"', or 'v' for numbers and literals
	start   int  // offset of the first byte of the value
	end     int  // offset one past the last byte
	members []jmember
	elems   []jelem // array elements, in order
}

type jmember struct {
	key      string
	keyStart int // offset of the key's opening quote
	value    *jnode
	comma    int // offset of the comma after the value, or -1
}

// jelem is one array element and the comma after it, if any.
type jelem struct {
	value *jnode
	comma int // offset of the comma after the value, or -1
}

func (n *jnode) member(key string) (*jmember, int) {
	if n == nil || n.kind != '{' {
		return nil, -1
	}
	for i := range n.members {
		if n.members[i].key == key {
			return &n.members[i], i
		}
	}
	return nil, -1
}

type jparser struct {
	src []byte
	pos int
}

func parseJSONC(src []byte) (*jnode, error) {
	p := &jparser{src: src}
	if bytes.HasPrefix(src, utf8BOM) {
		p.pos = len(utf8BOM)
	}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.pos >= len(src) {
		return nil, errors.New("the file is empty")
	}
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.pos != len(src) {
		return nil, p.errorf("unexpected content after the top-level value")
	}
	return n, nil
}

func (p *jparser) errorf(format string, args ...any) error {
	line := 1 + bytes.Count(p.src[:min(p.pos, len(p.src))], []byte("\n"))
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
}

// skip consumes whitespace and comments.
func (p *jparser) skip() error {
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		case c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '*':
			end := bytes.Index(p.src[p.pos+2:], []byte("*/"))
			if end < 0 {
				return p.errorf("unterminated comment")
			}
			p.pos += 2 + end + 2
		default:
			return nil
		}
	}
	return nil
}

func (p *jparser) value(depth int) (*jnode, error) {
	if depth > 200 {
		return nil, p.errorf("nesting too deep")
	}
	if p.pos >= len(p.src) {
		return nil, p.errorf("unexpected end of file")
	}
	switch c := p.src[p.pos]; c {
	case '{':
		return p.object(depth)
	case '[':
		return p.array(depth)
	case '"':
		start := p.pos
		if _, err := p.str(); err != nil {
			return nil, err
		}
		return &jnode{kind: '"', start: start, end: p.pos}, nil
	default:
		start := p.pos
		for p.pos < len(p.src) && strings.IndexByte("+-.0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", p.src[p.pos]) >= 0 {
			p.pos++
		}
		if p.pos == start || !json.Valid(p.src[start:p.pos]) {
			p.pos = start
			return nil, p.errorf("unexpected character %q", c)
		}
		return &jnode{kind: 'v', start: start, end: p.pos}, nil
	}
}

// str consumes a string and returns its decoded value.
func (p *jparser) str() (string, error) {
	start := p.pos
	p.pos++
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '\\':
			p.pos += 2
		case '"':
			p.pos++
			var s string
			if err := json.Unmarshal(p.src[start:p.pos], &s); err != nil {
				p.pos = start
				return "", p.errorf("invalid string")
			}
			return s, nil
		case '\n':
			p.pos = start
			return "", p.errorf("unterminated string")
		default:
			p.pos++
		}
	}
	p.pos = start
	return "", p.errorf("unterminated string")
}

func (p *jparser) object(depth int) (*jnode, error) {
	n := &jnode{kind: '{', start: p.pos}
	p.pos++
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos >= len(p.src) {
			return nil, p.errorf("unterminated object")
		}
		if p.src[p.pos] == '}' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		if p.src[p.pos] != '"' {
			return nil, p.errorf("expected a quoted member name")
		}
		keyStart := p.pos
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos >= len(p.src) || p.src[p.pos] != ':' {
			return nil, p.errorf("expected ':' after %q", key)
		}
		p.pos++
		if err := p.skip(); err != nil {
			return nil, err
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		m := jmember{key: key, keyStart: keyStart, value: v, comma: -1}
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			m.comma = p.pos
			p.pos++
			n.members = append(n.members, m)
			continue
		}
		n.members = append(n.members, m)
		if p.pos < len(p.src) && p.src[p.pos] == '}' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		return nil, p.errorf("expected ',' or '}'")
	}
}

func (p *jparser) array(depth int) (*jnode, error) {
	n := &jnode{kind: '[', start: p.pos}
	p.pos++
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos >= len(p.src) {
			return nil, p.errorf("unterminated array")
		}
		if p.src[p.pos] == ']' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if err := p.skip(); err != nil {
			return nil, err
		}
		e := jelem{value: v, comma: -1}
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			e.comma = p.pos
			p.pos++
			n.elems = append(n.elems, e)
			continue
		}
		n.elems = append(n.elems, e)
		if p.pos < len(p.src) && p.src[p.pos] == ']' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		return nil, p.errorf("expected ',' or ']'")
	}
}

// plainJSON strips a byte-order mark, comments, and trailing commas from a
// JSONC fragment.
func plainJSON(src []byte) []byte {
	src = bytes.TrimPrefix(src, utf8BOM)
	var out bytes.Buffer
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				j = len(src) - 1
			}
			out.Write(src[i : j+1])
			i = j
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i--
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return out.Bytes()
			}
			i += 2 + end + 1
		case c == ',':
			j := i + 1
			for j < len(src) {
				if src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r' {
					j++
					continue
				}
				if src[j] == '/' && j+1 < len(src) && src[j+1] == '/' {
					for j < len(src) && src[j] != '\n' {
						j++
					}
					continue
				}
				if src[j] == '/' && j+1 < len(src) && src[j+1] == '*' {
					end := bytes.Index(src[j+2:], []byte("*/"))
					if end < 0 {
						break
					}
					j += 2 + end + 2
					continue
				}
				break
			}
			if j < len(src) && (src[j] == '}' || src[j] == ']') {
				continue // trailing comma
			}
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}

// hasComments reports whether a JSONC document contains comments.
func hasComments(src []byte) bool {
	return !bytes.Equal(bytes.TrimSpace(plainJSONKeepCommas(src)), bytes.TrimSpace(src))
}

func plainJSONKeepCommas(src []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				j = len(src) - 1
			}
			out.Write(src[i : j+1])
			i = j
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i--
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return out.Bytes()
			}
			i += 2 + end + 1
		default:
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}

// valueOf decodes the JSONC value of n.
func valueOf(src []byte, n *jnode) (any, error) {
	var v any
	if err := json.Unmarshal(plainJSON(src[n.start:n.end]), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// sameJSON reports whether two JSON documents hold the same value.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func nodeEquals(src []byte, n *jnode, want []byte) bool {
	got, err := valueOf(src, n)
	if err != nil {
		return false
	}
	raw, err := json.Marshal(got)
	if err != nil {
		return false
	}
	return sameJSON(raw, want)
}

// marshalIndent renders v without HTML escaping.
func marshalIndent(v any, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// lineIndent returns the whitespace before off on its line and whether only
// whitespace precedes off there.
func lineIndent(src []byte, off int) (string, bool) {
	i := off
	for i > 0 && (src[i-1] == ' ' || src[i-1] == '\t') {
		i--
	}
	alone := i == 0 || src[i-1] == '\n' || (i == len(utf8BOM) && bytes.HasPrefix(src, utf8BOM))
	return string(src[i:off]), alone
}

// indentUnit guesses the document's indentation step.
func indentUnit(src []byte) string {
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || len(trimmed) == len(line) {
			continue
		}
		ws := line[:len(line)-len(trimmed)]
		if strings.HasPrefix(ws, "\t") {
			return "\t"
		}
		if len(ws) <= 8 {
			return ws
		}
	}
	return "  "
}

// newline returns the line ending a document uses.
func newline(src []byte) string {
	if bytes.Contains(src, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// insertMember returns src with "key": value added as the first member of
// obj. Nothing else in src changes, and removeMember deletes exactly the
// inserted text again.
func insertMember(src []byte, obj *jnode, key string, value any) ([]byte, error) {
	unit := indentUnit(src)
	nl := newline(src)
	objIndent, _ := lineIndent(src, obj.start)
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return nil, err
	}
	at := obj.start + 1
	var insert string
	if len(obj.members) > 0 {
		first := obj.members[0]
		indent, alone := lineIndent(src, first.keyStart)
		if alone {
			// A line of its own, just above the first member's line, so
			// comments on the opening line stay where they are.
			rendered, err := marshalIndent(value, indent, unit)
			if err != nil {
				return nil, err
			}
			rendered = bytes.ReplaceAll(rendered, []byte("\n"), []byte(nl))
			at = first.keyStart - len(indent)
			insert = indent + string(keyJSON) + ": " + string(rendered) + "," + nl
		} else {
			rendered, err := marshalIndent(value, "", "")
			if err != nil {
				return nil, err
			}
			at = first.keyStart
			insert = string(keyJSON) + ": " + string(bytes.ReplaceAll(rendered, []byte("\n"), nil)) + ","
		}
		var out bytes.Buffer
		out.Write(src[:at])
		out.WriteString(insert)
		out.Write(src[at:])
		return out.Bytes(), nil
	}
	indent := objIndent + unit
	rendered, err := marshalIndent(value, indent, unit)
	if err != nil {
		return nil, err
	}
	rendered = bytes.ReplaceAll(rendered, []byte("\n"), []byte(nl))
	insert = nl + indent + string(keyJSON) + ": " + string(rendered) + nl + objIndent
	var out bytes.Buffer
	out.Write(src[:obj.start+1])
	out.WriteString(insert)
	if len(bytes.TrimSpace(src[obj.start+1:obj.end-1])) == 0 {
		// Whitespace-only objects ({} or { }) take the new layout.
		out.Write(src[obj.end-1:])
	} else {
		out.Write(src[obj.start+1:])
	}
	return out.Bytes(), nil
}

// replaceValue returns src with the value of m replaced by value.
func replaceValue(src []byte, m *jmember, value any) ([]byte, error) {
	indent, _ := lineIndent(src, m.keyStart)
	rendered, err := marshalIndent(value, indent, indentUnit(src))
	if err != nil {
		return nil, err
	}
	rendered = bytes.ReplaceAll(rendered, []byte("\n"), []byte(newline(src)))
	var out bytes.Buffer
	out.Write(src[:m.value.start])
	out.Write(rendered)
	out.Write(src[m.value.end:])
	return out.Bytes(), nil
}

// removeMember returns src without member i of obj, keeping comments and the
// other members' text. An object left with nothing but whitespace collapses
// to {}.
func removeMember(src []byte, obj *jnode, i int) []byte {
	if len(obj.members) == 1 {
		m := obj.members[0]
		before := src[obj.start+1 : m.keyStart]
		after := src[m.value.end : obj.end-1]
		if m.comma >= 0 {
			after = src[m.comma+1 : obj.end-1]
		}
		if len(bytes.TrimSpace(before)) == 0 && len(bytes.TrimSpace(after)) == 0 {
			out := append([]byte(nil), src[:obj.start+1]...)
			return append(out, src[obj.end-1:]...)
		}
	}
	m := obj.members[i]
	type span struct{ start, end int }
	var cuts []span
	end := m.value.end
	if m.comma >= 0 {
		end = m.comma + 1
	} else if i > 0 && obj.members[i-1].comma >= 0 {
		c := obj.members[i-1].comma
		cuts = append(cuts, span{c, c + 1})
	}
	start, end := wholeLines(src, m.keyStart, end)
	cuts = append(cuts, span{start, end})
	out := append([]byte(nil), src...)
	for j := len(cuts) - 1; j >= 0; j-- {
		out = append(out[:cuts[j].start], out[cuts[j].end:]...)
	}
	return out
}

// wholeLines widens [start, end) to whole lines when only whitespace
// surrounds it on its first and last line.
func wholeLines(src []byte, start, end int) (int, int) {
	ls := start
	for ls > 0 && (src[ls-1] == ' ' || src[ls-1] == '\t') {
		ls--
	}
	le := end
	for le < len(src) && (src[le] == ' ' || src[le] == '\t' || src[le] == '\r') {
		le++
	}
	if (ls == 0 || src[ls-1] == '\n') && (le == len(src) || src[le] == '\n') {
		if le < len(src) {
			le++
		}
		return ls, le
	}
	return start, end
}

// renderJSONDocument renders a new document holding value at keyPath.
func renderJSONDocument(keyPath []string, value any) ([]byte, error) {
	var doc any = value
	for i := len(keyPath) - 1; i >= 0; i-- {
		doc = map[string]any{keyPath[i]: doc}
	}
	out, err := marshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// stringValue decodes n when it is a JSON string.
func stringValue(src []byte, n *jnode) (string, bool) {
	if n == nil || n.kind != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(src[n.start:n.end], &s); err != nil {
		return "", false
	}
	return s, true
}

// jsonText renders v as compact JSON without HTML escaping.
func jsonText(v any) (string, error) {
	out, err := marshalIndent(v, "", "")
	if err != nil {
		return "", err
	}
	return string(bytes.ReplaceAll(out, []byte("\n"), nil)), nil
}

// appendArrayStrings returns src with values added as string elements after
// the last element of arr, in the array's own layout: one per line when its
// elements sit on lines of their own, inline otherwise. Nothing else in src
// changes, and removeArrayElem deletes exactly the added text again.
func appendArrayStrings(src []byte, arr *jnode, values []string) ([]byte, error) {
	if len(values) == 0 {
		return src, nil
	}
	nl := newline(src)
	quoted := make([]string, len(values))
	for i, v := range values {
		q, err := jsonText(v)
		if err != nil {
			return nil, err
		}
		quoted[i] = q
	}
	var out bytes.Buffer
	if len(arr.elems) == 0 {
		arrIndent, _ := lineIndent(src, arr.start)
		indent := arrIndent + indentUnit(src)
		out.Write(src[:arr.start+1])
		for i, q := range quoted {
			out.WriteString(nl + indent + q)
			if i < len(quoted)-1 {
				out.WriteByte(',')
			}
		}
		if len(bytes.TrimSpace(src[arr.start+1:arr.end-1])) == 0 {
			// Whitespace-only arrays ([] or [ ]) take the new layout.
			out.WriteString(nl + arrIndent)
			out.Write(src[arr.end-1:])
		} else {
			out.Write(src[arr.start+1:])
		}
		return out.Bytes(), nil
	}
	last := arr.elems[len(arr.elems)-1]
	indent, alone := lineIndent(src, last.value.start)
	at := last.value.end
	if last.comma >= 0 {
		at = last.comma + 1
	}
	out.Write(src[:at])
	for _, q := range quoted {
		switch {
		case alone && last.comma >= 0:
			out.WriteString(nl + indent + q + ",")
		case alone:
			out.WriteString("," + nl + indent + q)
		case last.comma >= 0:
			out.WriteString(" " + q + ",")
		default:
			out.WriteString(", " + q)
		}
	}
	out.Write(src[at:])
	return out.Bytes(), nil
}

// removeArrayElem returns src without element i of arr, keeping comments and
// the other elements' text. An array left with nothing but whitespace
// collapses to [].
func removeArrayElem(src []byte, arr *jnode, i int) []byte {
	if len(arr.elems) == 1 {
		e := arr.elems[0]
		before := src[arr.start+1 : e.value.start]
		after := src[e.value.end : arr.end-1]
		if e.comma >= 0 {
			after = src[e.comma+1 : arr.end-1]
		}
		if len(bytes.TrimSpace(before)) == 0 && len(bytes.TrimSpace(after)) == 0 {
			out := append([]byte(nil), src[:arr.start+1]...)
			return append(out, src[arr.end-1:]...)
		}
	}
	e := arr.elems[i]
	cut := func(start, end int) []byte {
		out := append([]byte(nil), src[:start]...)
		return append(out, src[end:]...)
	}
	if _, alone := lineIndent(src, e.value.start); alone {
		end := e.value.end
		if e.comma >= 0 {
			end = e.comma + 1
		}
		start, end := wholeLines(src, e.value.start, end)
		if e.comma < 0 && i > 0 && arr.elems[i-1].comma >= 0 {
			c := arr.elems[i-1].comma
			out := cut(start, end)
			return append(out[:c], out[c+1:]...)
		}
		return cut(start, end)
	}
	if e.comma >= 0 {
		end := e.comma + 1
		for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
			end++
		}
		return cut(e.value.start, end)
	}
	if i > 0 && arr.elems[i-1].comma >= 0 {
		return cut(arr.elems[i-1].comma, e.value.end)
	}
	return cut(e.value.start, e.value.end)
}

// appendMember returns src with "key": value added as the last member of
// obj: for a reader that lets the last matching rule win, it decides.
func appendMember(src []byte, obj *jnode, key string, value any) ([]byte, error) {
	if len(obj.members) == 0 {
		return insertMember(src, obj, key, value)
	}
	keyJSON, err := jsonText(key)
	if err != nil {
		return nil, err
	}
	nl := newline(src)
	last := obj.members[len(obj.members)-1]
	indent, alone := lineIndent(src, last.keyStart)
	var rendered string
	if alone {
		out, err := marshalIndent(value, indent, indentUnit(src))
		if err != nil {
			return nil, err
		}
		rendered = string(bytes.ReplaceAll(out, []byte("\n"), []byte(nl)))
	} else if rendered, err = jsonText(value); err != nil {
		return nil, err
	}
	at := last.value.end
	var insert string
	switch {
	case alone && last.comma >= 0:
		at = last.comma + 1
		insert = nl + indent + keyJSON + ": " + rendered + ","
	case alone:
		insert = "," + nl + indent + keyJSON + ": " + rendered
	case last.comma >= 0:
		at = last.comma + 1
		insert = " " + keyJSON + ": " + rendered + ","
	default:
		insert = ", " + keyJSON + ": " + rendered
	}
	var out bytes.Buffer
	out.Write(src[:at])
	out.WriteString(insert)
	out.Write(src[at:])
	return out.Bytes(), nil
}
