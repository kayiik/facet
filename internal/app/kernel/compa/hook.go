package compa

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kayiik/facet/internal/app/kernel"
)

// The kernel starts the App's hook process itself (hooks.processes in
// config.json) and talks newline-delimited JSON-RPC 2.0 over its stdin and
// stdout. The process, RunHookRelay, only relays those lines to the App over
// a loopback connection, so the App's hub answers the kernel:
//
//	hook.hello          the handshake, answered with {}
//	hook.approve_tool   a call the policy says to ask about: the window
//	                    answers, and no answer by the deadline denies it
//	hook.before_tool    every call: an exec that would run a paid Facet tool
//	                    from the shell is denied (paid tools run only over
//	                    MCP, where they are asked about); the rest continue
//	hook.runtime_event  turn start and end, and tool progress
//
// The relay finds the hub through two environment variables, which it
// inherits from the kernel the App starts.
const (
	envHookAddr  = "FACET_APP_HOOK_ADDR"
	envHookToken = "FACET_APP_HOOK_TOKEN"
)

// RunHookRelay is the hook process. It returns when either side closes.
func RunHookRelay(stdin io.Reader, stdout io.Writer) error {
	conn, err := net.DialTimeout("tcp", os.Getenv(envHookAddr), 10*time.Second)
	if err != nil {
		return fmt.Errorf("facet-app hook: cannot reach the App: %w", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, os.Getenv(envHookToken)); err != nil {
		return err
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(conn, stdin); done <- struct{}{} }()
	go func() { io.Copy(stdout, conn); done <- struct{}{} }()
	<-done
	return nil
}

// hub is the App's side of the hook.
type hub struct {
	ln    net.Listener
	token string
	wait  time.Duration // how long the window may take to answer
	paid  map[string]bool
	emit  func(kernel.Event)

	mu      sync.Mutex
	pending map[string]chan answer
	closed  bool
}

type answer struct {
	approve bool
	reason  string
}

func newHub(wait time.Duration, paid []string, emit func(kernel.Event)) (*hub, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	h := &hub{ln: ln, token: randomToken(), wait: wait, paid: map[string]bool{}, emit: emit, pending: map[string]chan answer{}}
	for _, tool := range paid {
		h.paid[tool] = true
	}
	go h.accept()
	return h, nil
}

func randomToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *hub) env() []string {
	return []string{envHookAddr + "=" + h.ln.Addr().String(), envHookToken + "=" + h.token}
}

func (h *hub) accept() {
	for {
		c, err := h.ln.Accept()
		if err != nil {
			return
		}
		go h.serve(c)
	}
}

// close denies every pending approval and stops accepting relays.
func (h *hub) close() {
	h.mu.Lock()
	h.closed = true
	for id, ch := range h.pending {
		ch <- answer{false, "the App is stopping"}
		delete(h.pending, id)
	}
	h.mu.Unlock()
	h.ln.Close()
}

func (h *hub) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReaderSize(c, 1<<20)
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	first, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(first) != h.token {
		return
	}
	c.SetReadDeadline(time.Time{})
	var wmu sync.Mutex
	reply := func(id json.RawMessage, result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		wmu.Lock()
		defer wmu.Unlock()
		c.Write(append(b, '\n'))
	}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var msg rpcMessage
			if json.Unmarshal(line, &msg) == nil && msg.Method != "" {
				if msg.ID != nil {
					// Requests are answered concurrently: an approval
					// waits for the window, and must not hold up others.
					go func() { reply(msg.ID, h.handle(msg)) }()
				} else {
					h.handle(msg)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// handle answers one message from the kernel; the result is ignored for a
// notification.
func (h *hub) handle(msg rpcMessage) any {
	switch msg.Method {
	case "hook.hello":
		return map[string]any{}
	case "hook.approve_tool":
		var p struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
			Origin    string         `json:"origin"`
			Info      struct {
				Source string `json:"source"`
			} `json:"info"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return map[string]any{"approved": false, "reason": "the App could not read the request"}
		}
		ok, reason := h.approve(p.Tool, p.Arguments, p.Origin, p.Info.Source)
		if ok {
			return map[string]any{"approved": true}
		}
		return map[string]any{"approved": false, "reason": reason}
	case "hook.before_tool":
		var p struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(msg.Params, &p) == nil && p.Tool == "exec" {
			command, _ := p.Arguments["command"].(string)
			if tool := paidShellTool(command, h.paid); tool != "" {
				return map[string]any{"action": "deny_tool", "reason": fmt.Sprintf(
					"%s may charge, so the Facet App runs it only through the facet MCP tool mcp_facet_%s, where the person is asked first", tool, tool)}
			}
		}
		return map[string]any{"action": "continue"}
	case "hook.runtime_event":
		h.event(msg.Params)
		return nil
	}
	// Methods the App did not ask for: let the kernel carry on.
	return map[string]any{"action": "continue"}
}

// shellRun matches the facet command running a tool, however it is quoted
// or named.
var shellRun = regexp.MustCompile(`(?i)facet(?:\.exe)?["']?\s+tools\s+run\s+["']?([a-z0-9_]+)`)

// paidShellTool returns the paid Facet tool a shell command runs, or "".
func paidShellTool(command string, paid map[string]bool) string {
	for _, m := range shellRun.FindAllStringSubmatch(command, -1) {
		if paid[strings.ToLower(m[1])] {
			return strings.ToLower(m[1])
		}
	}
	return ""
}

// facetTool describes a tool the kernel names.
func (h *hub) facetTool(name string) kernel.Tool {
	t := kernel.Tool{Name: name}
	if rest, ok := strings.CutPrefix(name, "mcp_facet_"); ok {
		t.Facet = rest
		t.Paid = h.paid[rest]
	}
	return t
}

// approve asks the window and waits for its answer, the deadline, or Stop.
func (h *hub) approve(tool string, args map[string]any, origin, source string) (bool, string) {
	id := randomToken()[:12]
	ch := make(chan answer, 1)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false, "the App is stopping"
	}
	h.pending[id] = ch
	h.mu.Unlock()

	req := kernel.Approval{ID: id, Tool: h.facetTool(tool), Args: args, Origin: origin, Source: source, Deadline: time.Now().Add(h.wait)}
	h.emit(kernel.Event{Kind: kernel.EventApprovalRequest, Approval: &req})
	var a answer
	select {
	case a = <-ch:
	case <-time.After(h.wait):
		a = answer{false, fmt.Sprintf("no answer in the Facet App within %s", h.wait)}
	}
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()
	req.Approved, req.Reason = a.approve, a.reason
	h.emit(kernel.Event{Kind: kernel.EventApprovalResolved, Approval: &req})
	return a.approve, a.reason
}

// answer resolves a pending approval.
func (h *hub) answer(id string, approve bool, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.pending[id]
	if !ok {
		return kernel.ErrUnknownApproval
	}
	if !approve && reason == "" {
		reason = "the person declined in the Facet App"
	}
	ch <- answer{approve, reason}
	delete(h.pending, id)
	return nil
}

// event relays one runtime event.
func (h *hub) event(raw json.RawMessage) {
	var ev struct {
		Kind  string `json:"kind"`
		Scope struct {
			ChatID string `json:"chat_id"`
		} `json:"scope"`
		Payload map[string]any `json:"payload"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	session := strings.TrimPrefix(ev.Scope.ChatID, "web:")
	out := kernel.Event{Session: session}
	switch ev.Kind {
	case "agent.turn.start":
		out.Kind = kernel.EventTurnStart
	case "agent.turn.end":
		out.Kind = kernel.EventTurnEnd
		out.Status, _ = ev.Payload["Status"].(string)
	case "mcp.tool.call.progress":
		out.Kind = kernel.EventProgress
		server, _ := ev.Payload["server"].(string)
		tool, _ := ev.Payload["tool"].(string)
		t := h.facetTool("mcp_" + server + "_" + tool)
		out.Tool = &t
		out.Progress, _ = ev.Payload["progress"].(float64)
		out.Text, _ = ev.Payload["message"].(string)
	default:
		return
	}
	h.emit(out)
}
