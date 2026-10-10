package compa

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kayiik/facet/internal/app/kernel"
)

// The kernel's web channel is a WebSocket at /web/ws on its gateway, one
// session per socket (?session_id=), authenticated with the channel token.
// The client sends {"type":"message.send","payload":{"content":...}}; a
// command such as /stop is just content. The kernel sends message.create and
// message.update (the whole text so far; the last update carries
// context_usage), message.delete, typing.start/stop and error frames. A
// payload kind of "tool_calls" lists the tools the agent calls.

type frame struct {
	Type      string         `json:"type"`
	ID        string         `json:"id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type socket struct {
	conn    *websocket.Conn
	session string
	wmu     sync.Mutex
	seq     int
	done    chan struct{}
}

func dial(port int, token, session string, onFrame func(string, frame)) (*socket, error) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	u := fmt.Sprintf("ws://127.0.0.1:%d/web/ws?session_id=%s", port, url.QueryEscape(session))
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := d.Dial(u, h)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("connecting to the agent: %v (%s)", err, resp.Status)
		}
		return nil, fmt.Errorf("connecting to the agent: %w", err)
	}
	s := &socket{conn: conn, session: session, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			var f frame
			if err := conn.ReadJSON(&f); err != nil {
				return
			}
			onFrame(session, f)
		}
	}()
	return s, nil
}

func (s *socket) send(content string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.seq++
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteJSON(frame{Type: "message.send", ID: fmt.Sprintf("app-%d", s.seq), Payload: map[string]any{"content": content}})
}

func (s *socket) close() {
	s.conn.Close()
	<-s.done
}

// events maps one frame to the events it reports.
func events(session string, f frame, tool func(string) kernel.Tool) []kernel.Event {
	p := f.Payload
	str := func(key string) string { s, _ := p[key].(string); return s }
	switch f.Type {
	case "message.create", "message.update":
		if placeholder, _ := p["placeholder"].(bool); placeholder {
			return nil
		}
		ev := kernel.Event{Kind: kernel.EventMessage, Session: session, MessageID: str("message_id"), Text: str("content")}
		switch str("kind") {
		case "tool_calls":
			calls, _ := p["tool_calls"].([]any)
			var out []kernel.Event
			for _, c := range calls {
				call, _ := c.(map[string]any)
				fn, _ := call["function"].(map[string]any)
				name, _ := fn["name"].(string)
				t := tool(name)
				var args map[string]any
				if raw, _ := fn["arguments"].(string); raw != "" {
					// Arguments are pretty-printed, and cut short when long.
					_ = json.Unmarshal([]byte(raw), &args)
				}
				out = append(out, kernel.Event{Kind: kernel.EventToolCall, Session: session, MessageID: str("message_id"), Tool: &t, Args: args})
			}
			return out
		case "thought":
			ev.Thought = true
		}
		_, ev.Final = p["context_usage"]
		return []kernel.Event{ev}
	case "message.delete":
		return []kernel.Event{{Kind: kernel.EventMessageDeleted, Session: session, MessageID: str("message_id")}}
	case "error":
		return []kernel.Event{{Kind: kernel.EventNotice, Session: session, Text: str("message"), Detail: str("code")}}
	}
	return nil
}

// mcpStatus reads the kernel's /list mcp reply: whether the facet server is
// connected, and how many tools it has.
func mcpStatus(text string) (connected bool, ok bool) {
	if !strings.Contains(text, "Configured MCP Servers") {
		return false, false
	}
	i := strings.Index(text, "`facet`")
	if i < 0 {
		return false, true
	}
	rest := text[i:]
	if j := strings.Index(rest[1:], "\n- `"); j >= 0 {
		rest = rest[:j+1]
	}
	return strings.Contains(rest, "Connected: yes"), true
}
