// Package compa drives a private Compa kernel (compa-kernel) as the Facet
// App's agent engine, through its gateway: the web channel carries messages
// and streamed replies, and a hook process the kernel starts carries approvals
// and turn events to the App. The App starts the kernel with its own home and
// Toolkit, so it never touches a Compa or a Facet the person installed.
package compa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/kayiik/facet/internal/app/kernel"
)

// Options configure the kernel.
type Options struct {
	HomeOptions
	// PaidTools are the Facet tools that may charge (from the Toolkit's own
	// declarations); they mark approvals and guard the shell route.
	PaidTools []string
	// Log receives the kernel's own output.
	Log io.Writer
	// ReadyTimeout bounds startup (default 2 minutes).
	ReadyTimeout time.Duration
}

// Compa is a kernel.Kernel.
type Compa struct {
	opts Options

	mu       sync.Mutex
	cmd      *exec.Cmd
	exited   chan struct{}
	port     int
	token    string // the web channel's token for this run
	hub      *hub
	sockets  map[string]*socket
	running  bool
	stopping bool

	queue  chan kernel.Event
	events chan kernel.Event
	once   sync.Once
}

var _ kernel.Kernel = (*Compa)(nil)

// New returns a kernel that is not started.
func New(opts Options) *Compa {
	if opts.ApprovalWait <= 0 {
		opts.ApprovalWait = 10 * time.Minute
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = 2 * time.Minute
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	c := &Compa{opts: opts, sockets: map[string]*socket{}, queue: make(chan kernel.Event, 1024), events: make(chan kernel.Event, 64)}
	go c.pump()
	return c
}

// pump delivers events in order without letting a slow reader block the
// kernel: it holds whatever the reader has not taken yet.
func (c *Compa) pump() {
	var held []kernel.Event
	for {
		if len(held) == 0 {
			ev, ok := <-c.queue
			if !ok {
				close(c.events)
				return
			}
			held = append(held, ev)
			continue
		}
		select {
		case ev, ok := <-c.queue:
			if !ok {
				for _, e := range held {
					c.events <- e
				}
				close(c.events)
				return
			}
			held = append(held, ev)
		case c.events <- held[0]:
			held = held[1:]
		}
	}
}

func (c *Compa) emit(ev kernel.Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	defer func() { recover() }() // after Stop closed the queue
	c.queue <- ev
}

func (c *Compa) status(status, detail string) {
	c.emit(kernel.Event{Kind: kernel.EventStatus, Status: status, Detail: detail})
}

// Events implements kernel.Kernel.
func (c *Compa) Events() <-chan kernel.Event { return c.events }

// Start implements kernel.Kernel.
func (c *Compa) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	c.status("starting", "preparing the agent's home")
	if err := Prepare(ctx, c.opts.HomeOptions); err != nil {
		c.status("failed", err.Error())
		return err
	}
	h, err := newHub(c.opts.ApprovalWait, c.opts.PaidTools, c.emit)
	if err != nil {
		c.status("failed", err.Error())
		return err
	}
	port, err := freePort()
	if err != nil {
		h.close()
		return err
	}
	token := randomToken()
	env := Env(c.opts.Home, c.opts.FacetHome, filepath.Dir(c.opts.Facet), filepath.Dir(c.opts.Kernel),
		append([]string{"COMPA_GATEWAY_HOST=127.0.0.1", fmt.Sprintf("COMPA_GATEWAY_PORT=%d", port),
			"COMPA_CHANNELS_WEB_TOKEN=" + token}, h.env()...)...)
	cmd := exec.Command(c.opts.Kernel, "gateway", "-E")
	cmd.Env, cmd.Dir = env, c.opts.Home
	cmd.Stdout, cmd.Stderr = c.opts.Log, c.opts.Log
	prepareCommand(cmd)
	c.status("starting", "starting the agent")
	if err := cmd.Start(); err != nil {
		h.close()
		c.status("failed", err.Error())
		return fmt.Errorf("starting compa-kernel: %w", err)
	}
	if err := killWithApp(cmd); err != nil {
		fmt.Fprintf(c.opts.Log, "facet-app: the agent may outlive the App: %v\n", err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()

	c.mu.Lock()
	c.cmd, c.exited, c.port, c.token, c.hub, c.running, c.stopping = cmd, exited, port, token, h, true, false
	c.mu.Unlock()

	if err := c.waitReady(ctx); err != nil {
		c.Stop(context.Background())
		c.status("failed", err.Error())
		return err
	}
	go c.supervise(exited)
	c.status("ready", "")
	return nil
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// waitReady waits for the gateway, then for Facet's MCP server: the kernel
// connects MCP servers in the background after it reports ready.
func (c *Compa) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.opts.ReadyTimeout)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/ready", c.port)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-c.exited:
			return errors.New("the agent stopped while starting; see its log")
		case <-ctx.Done():
			return errors.New("the agent did not start in time; see its log")
		case <-time.After(250 * time.Millisecond):
		}
	}
	c.status("starting", "connecting Facet's tools")
	replies := make(chan string, 8)
	s, err := dial(c.port, c.token, "facet-app-status", func(_ string, f frame) {
		if f.Type == "message.create" || f.Type == "message.update" {
			text, _ := f.Payload["content"].(string)
			select {
			case replies <- text:
			default:
			}
		}
	})
	if err != nil {
		return err
	}
	defer s.close()
	for {
		if err := s.send("/list mcp"); err != nil {
			return err
		}
		deadline := time.After(30 * time.Second)
	wait:
		for {
			select {
			case text := <-replies:
				if connected, ok := mcpStatus(text); ok {
					if connected {
						return nil
					}
					break wait
				}
			case <-deadline:
				break wait
			case <-ctx.Done():
				return errors.New("Facet's tools did not connect to the agent in time; see its log")
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("Facet's tools did not connect to the agent in time; see its log")
		case <-time.After(time.Second):
		}
	}
}

// supervise reports a kernel that stops on its own.
func (c *Compa) supervise(exited chan struct{}) {
	<-exited
	c.mu.Lock()
	stopping := c.stopping
	c.running = false
	c.mu.Unlock()
	if !stopping {
		c.status("failed", "the agent stopped unexpectedly; see its log")
	}
}

func (c *Compa) socketFor(session string) (*socket, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil, kernel.ErrNotRunning
	}
	if s := c.sockets[session]; s != nil {
		select {
		case <-s.done:
			delete(c.sockets, session)
		default:
			return s, nil
		}
	}
	s, err := dial(c.port, c.token, session, c.onFrame)
	if err != nil {
		return nil, err
	}
	c.sockets[session] = s
	return s, nil
}

func (c *Compa) onFrame(session string, f frame) {
	c.mu.Lock()
	h := c.hub
	c.mu.Unlock()
	tool := func(name string) kernel.Tool { return kernel.Tool{Name: name} }
	if h != nil {
		tool = h.facetTool
	}
	for _, ev := range events(session, f, tool) {
		c.emit(ev)
	}
}

// Send implements kernel.Kernel.
func (c *Compa) Send(ctx context.Context, session, text string) error {
	s, err := c.socketFor(session)
	if err != nil {
		return err
	}
	return s.send(text)
}

// Cancel implements kernel.Kernel: /stop aborts the session's turn, and the
// kernel cancels the tool calls it runs (Facet stops their processes).
func (c *Compa) Cancel(ctx context.Context, session string) error {
	return c.Send(ctx, session, "/stop")
}

// Answer implements kernel.Kernel.
func (c *Compa) Answer(id string, approve bool, reason string) error {
	c.mu.Lock()
	h := c.hub
	c.mu.Unlock()
	if h == nil {
		return kernel.ErrUnknownApproval
	}
	return h.answer(id, approve, reason)
}

// Stop implements kernel.Kernel: pending approvals are denied, the gateway is
// asked to shut down, and killed if it does not.
func (c *Compa) Stop(ctx context.Context) error {
	c.mu.Lock()
	cmd, exited, h, port := c.cmd, c.exited, c.hub, c.port
	sockets := c.sockets
	c.sockets = map[string]*socket{}
	c.stopping = true
	c.mu.Unlock()
	if h != nil {
		h.close()
	}
	for _, s := range sockets {
		s.close()
	}
	if cmd != nil {
		c.shutdown(port)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
	}
	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
	c.status("stopped", "")
	c.once.Do(func() { close(c.queue) })
	return nil
}

// shutdown asks the gateway to stop, with the token it wrote to .compa.pid.
func (c *Compa) shutdown(port int) {
	raw, err := os.ReadFile(filepath.Join(c.opts.Home, ".compa.pid"))
	if err != nil {
		return
	}
	var pid struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &pid) != nil || pid.Token == "" {
		return
	}
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/shutdown", port), nil)
	req.Header.Set("Authorization", "Bearer "+pid.Token)
	client := http.Client{Timeout: 5 * time.Second}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}
