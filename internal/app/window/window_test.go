package window

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kayiik/facet/internal/app/kernel"
	"github.com/kayiik/facet/internal/app/toolkit"
)

type fakeKernel struct {
	mu       sync.Mutex
	sent     []string
	cancels  []string
	answers  []string
	approval string
}

func (k *fakeKernel) Start(context.Context) error { return nil }
func (k *fakeKernel) Stop(context.Context) error  { return nil }
func (k *fakeKernel) Events() <-chan kernel.Event { return nil }
func (k *fakeKernel) Send(_ context.Context, session, text string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sent = append(k.sent, session+":"+text)
	return nil
}
func (k *fakeKernel) Cancel(_ context.Context, session string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cancels = append(k.cancels, session)
	return nil
}
func (k *fakeKernel) Answer(id string, approve bool, _ string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if id != k.approval {
		return kernel.ErrUnknownApproval
	}
	if approve {
		k.answers = append(k.answers, id+":yes")
	} else {
		k.answers = append(k.answers, id+":no")
	}
	return nil
}

type fakeHost struct {
	k     *fakeKernel
	model string
	set   []Model
}

func (h *fakeHost) Kernel() kernel.Kernel {
	if h.k == nil {
		return nil
	}
	return h.k
}
func (h *fakeHost) Model() string { return h.model }
func (h *fakeHost) SetModel(_ context.Context, m Model) error {
	h.set = append(h.set, m)
	h.model = "app/" + m.ID
	return nil
}

const testToken = "test-token"

func start(t *testing.T, host *fakeHost) (*Server, string) {
	t.Helper()
	tk := &toolkit.Runner{Exec: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		return []byte(`{"ok":true,"result":{"tools":[{"name":"media_probe"},{"name":"sora_video","may_charge":true,"network":true,"external_write":true}]}}`), nil
	}}
	s := New(host, tk, testToken)
	addr, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Serve(ctx)
	return s, "http://" + addr
}

func call(t *testing.T, method, url string, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func authed(extra ...string) map[string]string {
	h := map[string]string{"X-Facet-Token": testToken, "Content-Type": "application/json"}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = extra[i+1]
	}
	return h
}

func TestTheWindowAnswersOnlyItsOwnPage(t *testing.T) {
	host := &fakeHost{k: &fakeKernel{}}
	_, base := start(t, host)
	if code, _ := call(t, "GET", base+"/api/state", "", nil); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code, _ := call(t, "GET", base+"/api/state", "", map[string]string{"X-Facet-Token": "guess"}); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := call(t, "GET", base+"/api/state", "", authed("Host", "evil.example:80")); code != http.StatusMisdirectedRequest {
		t.Errorf("foreign Host (DNS rebinding): %d", code)
	}
	if code, _ := call(t, "POST", base+"/api/send", `{"text":"hi"}`, authed("Origin", "https://evil.example")); code != http.StatusForbidden {
		t.Errorf("cross-origin POST: %d", code)
	}
	if len(host.k.sent) != 0 {
		t.Errorf("a refused request reached the kernel: %v", host.k.sent)
	}
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "<title>Facet</title>") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("page: %d %q", resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}
}

func TestPersonActionsReachTheKernel(t *testing.T) {
	k := &fakeKernel{approval: "a1"}
	host := &fakeHost{k: k}
	s, base := start(t, host)
	if code, _ := call(t, "POST", base+"/api/send", `{"text":"Make a video"}`, authed()); code != http.StatusAccepted {
		t.Errorf("send: %d", code)
	}
	for _, text := range []string{"/approve abc123", "!stop", "   "} {
		body, _ := json.Marshal(map[string]string{"text": text})
		if code, _ := call(t, "POST", base+"/api/send", string(body), authed()); code != http.StatusBadRequest {
			t.Errorf("send %q: %d, want 400", text, code)
		}
	}
	if code, _ := call(t, "POST", base+"/api/send", `{"session":"a/b","text":"x"}`, authed()); code != http.StatusBadRequest {
		t.Errorf("bad session: %d", code)
	}
	if code, _ := call(t, "POST", base+"/api/stop", `{}`, authed()); code != http.StatusAccepted {
		t.Errorf("stop: %d", code)
	}
	if code, _ := call(t, "POST", base+"/api/answer", `{"id":"a1","approve":true}`, authed()); code != http.StatusOK {
		t.Errorf("answer: %d", code)
	}
	if code, _ := call(t, "POST", base+"/api/answer", `{"id":"nope","approve":true}`, authed()); code != http.StatusNotFound {
		t.Errorf("unknown approval: %d", code)
	}
	if strings.Join(k.sent, ",") != "main:Make a video" || strings.Join(k.cancels, ",") != "main" || strings.Join(k.answers, ",") != "a1:yes" {
		t.Errorf("kernel saw sent=%v cancels=%v answers=%v", k.sent, k.cancels, k.answers)
	}
	s.mu.Lock()
	last := s.history[len(s.history)-1]
	s.mu.Unlock()
	if last.Kind != EventUserMessage || last.Text != "Make a video" {
		t.Errorf("the person's message is not kept: %+v", last)
	}

	host.k = nil
	if code, _ := call(t, "POST", base+"/api/send", `{"text":"x"}`, authed()); code != http.StatusServiceUnavailable {
		t.Errorf("send without a kernel: %d", code)
	}
}

func TestModelSetup(t *testing.T) {
	host := &fakeHost{k: &fakeKernel{}}
	_, base := start(t, host)
	for _, body := range []string{`{"endpoint":"http://api.example/v1","model":"m"}`, `{"endpoint":"https://api.example/v1","model":""}`, `not json`} {
		if code, _ := call(t, "POST", base+"/api/model", body, authed()); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, code)
		}
	}
	code, out := call(t, "POST", base+"/api/model", `{"endpoint":" https://api.example/v1 ","model":"gpt-x","api_key":"k"}`, authed())
	if code != http.StatusOK || out["model"] != "app/gpt-x" || len(host.set) != 1 || host.set[0].Endpoint != "https://api.example/v1" || host.set[0].APIKey != "k" {
		t.Errorf("setup: %d %v %+v", code, out, host.set)
	}
	if code, _ := call(t, "POST", base+"/api/model", `{"endpoint":"http://127.0.0.1:11434/v1","model":"llama"}`, authed()); code != http.StatusOK {
		t.Errorf("a local endpoint was refused: %d", code)
	}
	code, out = call(t, "GET", base+"/api/tools", "", authed())
	tools, _ := out["tools"].([]any)
	if code != http.StatusOK || len(tools) != 2 {
		t.Fatalf("tools: %d %v", code, out)
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if (tool["name"] == "sora_video") == (tool["direct"] == true) {
			t.Errorf("%v", tool)
		}
	}
}

func TestEventsReplayThenStream(t *testing.T) {
	host := &fakeHost{k: &fakeKernel{}}
	s, base := start(t, host)
	s.Publish(kernel.Event{Kind: kernel.EventStatus, Status: "ready"})
	s.Publish(kernel.Event{Kind: kernel.EventApprovalRequest, Approval: &kernel.Approval{ID: "a1", Tool: kernel.Tool{Name: "mcp_facet_sora_video", Facet: "sora_video", Paid: true}}})

	_, state := call(t, "GET", base+"/api/state", "", authed())
	if state["status"] != "ready" || len(state["pending"].([]any)) != 1 {
		t.Errorf("state = %v", state)
	}
	req, _ := http.NewRequest("GET", base+"/api/events?t="+testToken, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 16)
	go func() {
		r := bufio.NewReader(resp.Body)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				close(lines)
				return
			}
			if strings.HasPrefix(line, "data: ") {
				lines <- strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	next := func() kernel.Event {
		select {
		case line := <-lines:
			var ev kernel.Event
			json.Unmarshal([]byte(line), &ev)
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
			return kernel.Event{}
		}
	}
	if ev := next(); ev.Kind != kernel.EventStatus {
		t.Errorf("replay 1 = %+v", ev)
	}
	if ev := next(); ev.Kind != kernel.EventApprovalRequest || !ev.Approval.Tool.Paid {
		t.Errorf("replay 2 = %+v", ev)
	}
	s.Publish(kernel.Event{Kind: kernel.EventMessage, Text: "live"})
	if ev := next(); ev.Text != "live" {
		t.Errorf("live = %+v", ev)
	}
	s.Publish(kernel.Event{Kind: kernel.EventApprovalResolved, Approval: &kernel.Approval{ID: "a1", Approved: true}})
	next()
	_, state = call(t, "GET", base+"/api/state", "", authed())
	if len(state["pending"].([]any)) != 0 {
		t.Errorf("a resolved approval is still pending: %v", state)
	}
}
