package compa

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kayiik/facet/internal/app/kernel"
)

func onboarded() map[string]any {
	var cfg map[string]any
	json.Unmarshal([]byte(`{
	  "agents":{"defaults":{"workspace":"","model_name":""}},
	  "channel_list":{"web":{"enabled":false,"type":"web","settings":{"ping_interval":30}},"telegram":{"enabled":false}},
	  "hooks":{"enabled":true,"defaults":{"approval_timeout_ms":60000,"interceptor_timeout_ms":5000,"observer_timeout_ms":500}},
	  "provider_instances":[{"id":"mine","provider_kind":"anthropic","adapter":"anthropic-compatible","protocol":"anthropic","endpoint":"x","state":"enabled"}],
	  "tools":{"mcp":{"enabled":true}},
	  "build_info":{"version":"2.1.1"}}`), &cfg)
	return cfg
}

func TestPatchConfigSetsOnlyWhatTheAppNeeds(t *testing.T) {
	cfg := onboarded()
	o := HomeOptions{Home: filepath.Join("h"), HookCommand: []string{"app.exe", "hook"}, ApprovalWait: 5 * time.Minute,
		Model: &Model{Endpoint: "http://127.0.0.1:9/v1/", APIKey: "k", ID: "gpt-x"}}
	ws, err := patchConfig(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	if ws != filepath.Join("h", "workspace") {
		t.Errorf("workspace = %s", ws)
	}
	web := dig(cfg, "channel_list", "web").(map[string]any)
	if web["enabled"] != true || dig(web, "settings", "streaming", "enabled") != true {
		t.Errorf("web channel = %v", web)
	}
	if _, has := dig(web, "settings").(map[string]any)["token"]; has {
		t.Error("the web token was written to config.json; it belongs in the environment")
	}
	if dig(cfg, "channel_list", "telegram") == nil {
		t.Error("another channel was dropped")
	}
	if got := dig(cfg, "hooks", "defaults", "approval_timeout_ms"); got != int64((6 * time.Minute).Milliseconds()) {
		t.Errorf("approval_timeout_ms = %v", got)
	}
	if dig(cfg, "hooks", "defaults", "interceptor_timeout_ms") != float64(5000) {
		t.Error("the person's other hook defaults changed")
	}
	proc := dig(cfg, "hooks", "processes", HookName).(map[string]any)
	if fmt.Sprint(proc["command"]) != "[app.exe hook]" || fmt.Sprint(proc["intercept"]) != "[approve_tool before_tool]" || proc["transport"] != "stdio" {
		t.Errorf("hook process = %v", proc)
	}
	instances := cfg["provider_instances"].([]any)
	if len(instances) != 2 || instances[0].(map[string]any)["id"] != "mine" {
		t.Fatalf("provider instances = %v", instances)
	}
	app := instances[1].(map[string]any)
	if app["endpoint"] != "http://127.0.0.1:9/v1" || app["auth_connection_ref"] != "credential:app" || app["adapter"] != "openai-compatible" {
		t.Errorf("app instance = %v", app)
	}
	if dig(cfg, "agents", "defaults", "model_name") != "app/gpt-x" {
		t.Errorf("model_name = %v", dig(cfg, "agents", "defaults", "model_name"))
	}
	// Patching again changes nothing and keeps one app instance.
	before, _ := json.Marshal(cfg)
	if _, err := patchConfig(cfg, o); err != nil {
		t.Fatal(err)
	}
	if after, _ := json.Marshal(cfg); string(after) != string(before) {
		t.Error("a second patch changed the config")
	}
	// Without a model, the model settings are left alone.
	cfg2 := onboarded()
	o.Model = nil
	patchConfig(cfg2, o)
	if len(cfg2["provider_instances"].([]any)) != 1 || dig(cfg2, "agents", "defaults", "model_name") != "" {
		t.Error("a patch without a model changed the model")
	}
	// A shape the App does not own is refused, not overwritten.
	bad := onboarded()
	bad["hooks"] = "off"
	if _, err := patchConfig(bad, o); err == nil {
		t.Error("a non-object hooks value was overwritten")
	}
}

func TestWriteModelKeepsTheKeyOutOfConfig(t *testing.T) {
	home := t.TempDir()
	if err := writeModel(home, Model{Endpoint: "https://api.example/v1/", APIKey: "sk-test", ID: "m1"}); err != nil {
		t.Fatal(err)
	}
	var catalog, auth map[string]any
	raw, _ := os.ReadFile(filepath.Join(home, "model_catalogs.json"))
	json.Unmarshal(raw, &catalog)
	entry := dig(catalog, "entries", Instance).(map[string]any)
	if entry["instance_id"] != Instance || entry["provider"] != "custom_openai" || entry["api_base"] != "https://api.example/v1" {
		t.Errorf("catalog entry = %v", entry)
	}
	if models := entry["models"].([]any); len(models) != 1 || models[0].(map[string]any)["id"] != "m1" {
		t.Errorf("models = %v", models)
	}
	raw, _ = os.ReadFile(filepath.Join(home, "auth.json"))
	json.Unmarshal(raw, &auth)
	if dig(auth, "credentials", Instance, "access_token") != "sk-test" {
		t.Errorf("auth = %s", raw)
	}
	// Removing the key removes the credential.
	writeModel(home, Model{Endpoint: "http://localhost:11434/v1", ID: "m1"})
	raw, _ = os.ReadFile(filepath.Join(home, "auth.json"))
	if strings.Contains(string(raw), "sk-test") {
		t.Error("an old key survived")
	}
}

func TestEnvDropsThePersonsCompaSettings(t *testing.T) {
	t.Setenv("COMPA_CONFIG", "/elsewhere/config.json")
	t.Setenv("COMPA_AGENTS_DEFAULTS_WORKSPACE", "/elsewhere")
	t.Setenv("FACET_HOME", "/their/facet")
	env := Env("/app/compa", "/app/facet", "/app/toolkit/bin", "/app/kernel", "X=1")
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, gone := range []string{"COMPA_CONFIG=", "COMPA_AGENTS_DEFAULTS_WORKSPACE=", "FACET_HOME=/their"} {
		if strings.Contains(joined, "\n"+gone) {
			t.Errorf("%s leaked into the kernel's environment", gone)
		}
	}
	for _, want := range []string{"COMPA_HOME=/app/compa", "FACET_HOME=/app/facet", "X=1", "PATH=/app/toolkit/bin" + string(os.PathListSeparator) + "/app/kernel"} {
		if !strings.Contains(joined, "\n"+want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestFramesBecomeEvents(t *testing.T) {
	tool := func(name string) kernel.Tool {
		t := kernel.Tool{Name: name}
		if rest, ok := strings.CutPrefix(name, "mcp_facet_"); ok {
			t.Facet, t.Paid = rest, rest == "sora_video"
		}
		return t
	}
	parse := func(s string) frame {
		var f frame
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	cases := []struct {
		frame string
		check func([]kernel.Event) bool
	}{
		{`{"type":"message.create","payload":{"content":"Hel","message_id":"A"}}`, func(e []kernel.Event) bool {
			return len(e) == 1 && e[0].Kind == kernel.EventMessage && e[0].Text == "Hel" && !e[0].Final && e[0].MessageID == "A"
		}},
		{`{"type":"message.update","payload":{"content":"Hello","message_id":"A","context_usage":{"used_tokens":1}}}`, func(e []kernel.Event) bool {
			return len(e) == 1 && e[0].Final && e[0].Text == "Hello"
		}},
		{`{"type":"message.create","payload":{"content":"hmm","message_id":"R","kind":"thought"}}`, func(e []kernel.Event) bool {
			return len(e) == 1 && e[0].Thought
		}},
		{`{"type":"message.create","payload":{"content":"Thinking...","placeholder":true}}`, func(e []kernel.Event) bool { return len(e) == 0 }},
		{`{"type":"message.create","payload":{"content":"","message_id":"T","kind":"tool_calls","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"mcp_facet_sora_video","arguments":"{\n  \"prompt\": \"x\"\n}"}},
			{"id":"c2","type":"function","function":{"name":"exec","arguments":"{\"command\":\"ls\"}"}}]}}`, func(e []kernel.Event) bool {
			return len(e) == 2 && e[0].Kind == kernel.EventToolCall && e[0].Tool.Facet == "sora_video" && e[0].Tool.Paid &&
				e[0].Args["prompt"] == "x" && e[1].Tool.Name == "exec" && e[1].Tool.Facet == ""
		}},
		{`{"type":"message.delete","payload":{"message_id":"A"}}`, func(e []kernel.Event) bool {
			return len(e) == 1 && e[0].Kind == kernel.EventMessageDeleted && e[0].MessageID == "A"
		}},
		{`{"type":"error","payload":{"code":"empty_content","message":"message content is empty"}}`, func(e []kernel.Event) bool {
			return len(e) == 1 && e[0].Kind == kernel.EventNotice && e[0].Detail == "empty_content"
		}},
		{`{"type":"typing.start"}`, func(e []kernel.Event) bool { return len(e) == 0 }},
	}
	for _, c := range cases {
		if got := events("s1", parse(c.frame), tool); !c.check(got) {
			t.Errorf("%s -> %+v", c.frame, got)
		}
	}
}

func TestMCPStatus(t *testing.T) {
	ok := "Configured MCP Servers:\n- `facet`\n  Enabled: yes\n  Deferred: no\n  Connected: yes\n  Active tools: 39"
	if c, seen := mcpStatus(ok); !c || !seen {
		t.Error("connected facet not recognised")
	}
	other := "Configured MCP Servers:\n- `facet`\n  Connected: no\n- `other`\n  Connected: yes"
	if c, seen := mcpStatus(other); c || !seen {
		t.Error("another server's state was taken for facet's")
	}
	if _, seen := mcpStatus("Hello"); seen {
		t.Error("an ordinary message was taken for the MCP status")
	}
}

func TestPaidShellTool(t *testing.T) {
	paid := map[string]bool{"sora_video": true}
	for cmd, want := range map[string]string{
		"facet tools run sora_video --input r.json":                        "sora_video",
		`"C:\App\toolkit\bin\facet.exe" tools run SORA_VIDEO --input '{}'`: "sora_video",
		"cd x && facet.exe  tools   run 'sora_video'":                      "sora_video",
		"facet tools run media_probe --input r.json":                       "",
		"facet tools estimate sora_video --input r.json":                   "",
		"ls": "",
	} {
		if got := paidShellTool(cmd, paid); got != want {
			t.Errorf("%q -> %q, want %q", cmd, got, want)
		}
	}
}

// relay connects to a hub as the hook process would, and returns a call
// function that sends one request and waits for its reply.
func relay(t *testing.T, h *hub) (call func(method string, params any) map[string]any, notify func(method string, params any)) {
	t.Helper()
	conn, err := net.Dial("tcp", h.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintln(conn, h.token)
	var mu sync.Mutex
	replies := map[float64]chan map[string]any{}
	go func() {
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var msg struct {
				ID     float64        `json:"id"`
				Result map[string]any `json:"result"`
			}
			json.Unmarshal(line, &msg)
			mu.Lock()
			if ch := replies[msg.ID]; ch != nil {
				ch <- msg.Result
			}
			mu.Unlock()
		}
	}()
	var next float64
	var wmu sync.Mutex
	call = func(method string, params any) map[string]any {
		mu.Lock()
		next++
		id := next
		ch := make(chan map[string]any, 1)
		replies[id] = ch
		mu.Unlock()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		wmu.Lock()
		conn.Write(append(b, '\n'))
		wmu.Unlock()
		select {
		case r := <-ch:
			return r
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: no reply", method)
			return nil
		}
	}
	notify = func(method string, params any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
		wmu.Lock()
		conn.Write(append(b, '\n'))
		wmu.Unlock()
	}
	return call, notify
}

func TestHubCarriesApprovalsAndEvents(t *testing.T) {
	events := make(chan kernel.Event, 64)
	h, err := newHub(300*time.Millisecond, []string{"sora_video"}, func(e kernel.Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	call, notify := relay(t, h)

	if r := call("hook.hello", map[string]any{"name": HookName}); r == nil {
		t.Fatal("hello not answered")
	}
	request := map[string]any{"tool": "mcp_facet_sora_video", "arguments": map[string]any{"prompt": "p"}, "origin": "web",
		"info": map[string]any{"source": "mcp:facet"}, "decision": map[string]any{"action": "ask"}}

	// Approved by the window.
	go func() {
		ev := <-events
		if ev.Kind != kernel.EventApprovalRequest || !ev.Approval.Tool.Paid || ev.Approval.Tool.Facet != "sora_video" || ev.Approval.Origin != "web" {
			t.Errorf("request event = %+v %+v", ev, ev.Approval)
		}
		h.answer(ev.Approval.ID, true, "")
	}()
	if r := call("hook.approve_tool", request); r["approved"] != true {
		t.Errorf("approve -> %v", r)
	}
	if ev := <-events; ev.Kind != kernel.EventApprovalResolved || !ev.Approval.Approved {
		t.Errorf("resolved = %+v", ev.Approval)
	}
	// Denied by the window, with its reason.
	go func() {
		ev := <-events
		h.answer(ev.Approval.ID, false, "")
	}()
	if r := call("hook.approve_tool", request); r["approved"] != false || !strings.Contains(fmt.Sprint(r["reason"]), "declined") {
		t.Errorf("deny -> %v", r)
	}
	<-events
	// No answer: denied at the deadline.
	if r := call("hook.approve_tool", request); r["approved"] != false || !strings.Contains(fmt.Sprint(r["reason"]), "no answer") {
		t.Errorf("timeout -> %v", r)
	}
	<-events
	<-events
	if err := h.answer("nope", true, ""); err != kernel.ErrUnknownApproval {
		t.Errorf("unknown id -> %v", err)
	}

	// The shell route cannot run a paid tool.
	if r := call("hook.before_tool", map[string]any{"tool": "exec", "arguments": map[string]any{"command": "facet tools run sora_video --input x.json"}}); r["action"] != "deny_tool" {
		t.Errorf("paid exec -> %v", r)
	}
	if r := call("hook.before_tool", map[string]any{"tool": "exec", "arguments": map[string]any{"command": "facet tools run media_probe --input x.json"}}); r["action"] != "continue" {
		t.Errorf("free exec -> %v", r)
	}
	if r := call("hook.before_tool", map[string]any{"tool": "mcp_facet_sora_video"}); r["action"] != "continue" {
		t.Errorf("MCP call -> %v (its approval decides)", r)
	}

	notify("hook.runtime_event", map[string]any{"kind": "agent.turn.end", "scope": map[string]any{"chat_id": "web:s1"}, "payload": map[string]any{"Status": "aborted"}})
	notify("hook.runtime_event", map[string]any{"kind": "mcp.tool.call.progress", "scope": map[string]any{"chat_id": "web:s1"},
		"payload": map[string]any{"server": "facet", "tool": "color_grade", "progress": 5.0, "message": "color_grade running for 5s"}})
	notify("hook.runtime_event", map[string]any{"kind": "agent.llm.request"})
	end, progress := <-events, <-events
	if end.Kind != kernel.EventTurnEnd || end.Status != "aborted" || end.Session != "s1" {
		t.Errorf("turn end = %+v", end)
	}
	if progress.Kind != kernel.EventProgress || progress.Tool.Facet != "color_grade" || progress.Progress != 5 || progress.Text == "" {
		t.Errorf("progress = %+v", progress)
	}
	select {
	case ev := <-events:
		t.Errorf("an unobserved event was relayed: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}

	// Closing denies whatever is still waiting.
	h.wait = time.Minute
	done := make(chan map[string]any, 1)
	go func() { done <- call("hook.approve_tool", request) }()
	<-events
	h.close()
	if r := <-done; r["approved"] != false {
		t.Errorf("close -> %v", r)
	}
}

func TestHubRefusesARelayWithoutTheToken(t *testing.T) {
	h, err := newHub(time.Second, nil, func(kernel.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	conn, err := net.Dial("tcp", h.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "wrong")
	fmt.Fprintln(conn, `{"jsonrpc":"2.0","id":1,"method":"hook.hello"}`)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if n, _ := conn.Read(make([]byte, 64)); n != 0 {
		t.Error("a relay without the token was answered")
	}
}
