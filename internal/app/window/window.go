// Package window serves the Facet App's window: a local page in the person's
// browser, on 127.0.0.1 only. The page reaches the App through a small API:
// server-sent events carry what the agent engine reports (with a replay, so a
// reloaded page shows the conversation), and POST requests carry what the
// person does: send a message, stop the turn, answer an approval, choose the
// model. The screens that need no model use the rest of the API (projects.go):
// projects and their media, technical review and the person's decision, and
// the Toolkit's readiness. Every API call needs the token of this run, which
// only the URL the App opens carries; the Host and Origin headers must be the
// App's own, so another site cannot drive it through the browser.
package window

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kayiik/facet/internal/app/journeys"
	"github.com/kayiik/facet/internal/app/kernel"
	"github.com/kayiik/facet/internal/app/toolkit"
)

//go:embed web
var webFiles embed.FS

// Model is what the person chooses on the setup screen.
type Model struct {
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"api_key"`
	ID       string `json:"model"`
}

// Host is what the window needs from the App.
type Host interface {
	// Kernel is the running agent engine, or nil while none runs.
	Kernel() kernel.Kernel
	// Model returns the configured model ("" when none).
	Model() string
	// SetModel saves the model and restarts the engine with it.
	SetModel(ctx context.Context, m Model) error
}

// Server is the window.
type Server struct {
	// Projects configures the project screens; set it before Serve.
	Projects Projects

	host    Host
	toolkit *toolkit.Runner
	token   string
	addr    string

	mu       sync.Mutex
	history  []kernel.Event
	clients  map[chan kernel.Event]struct{}
	status   kernel.Event
	pending  map[string]kernel.Event
	listener net.Listener
}

const historyLimit = 4000

// New returns a window for host. token authorizes API calls.
func New(host Host, tk *toolkit.Runner, token string) *Server {
	return &Server{host: host, toolkit: tk, token: token, clients: map[chan kernel.Event]struct{}{}, pending: map[string]kernel.Event{}}
}

// Listen binds 127.0.0.1 on a free port.
func (s *Server) Listen() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	s.listener = ln
	s.addr = ln.Addr().String()
	return s.addr, nil
}

// URL is the address the App opens; it carries the token.
func (s *Server) URL() string { return "http://" + s.addr + "/#t=" + s.token }

// Serve serves until ctx ends.
func (s *Server) Serve(ctx context.Context) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	err := srv.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Publish records an engine event and passes it to every open page.
func (s *Server) Publish(ev kernel.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Kind {
	case kernel.EventStatus:
		s.status = ev
	case kernel.EventApprovalRequest:
		s.pending[ev.Approval.ID] = ev
	case kernel.EventApprovalResolved:
		delete(s.pending, ev.Approval.ID)
	}
	s.history = append(s.history, ev)
	if len(s.history) > historyLimit {
		s.history = append([]kernel.Event(nil), s.history[len(s.history)-historyLimit:]...)
	}
	for ch := range s.clients {
		select {
		case ch <- ev:
		default:
			// A page that cannot keep up reconnects and replays.
			close(ch)
			delete(s.clients, ch)
		}
	}
}

// Handler returns the window's routes.
func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(webFiles, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /api/events", s.api(s.events))
	mux.HandleFunc("GET /api/state", s.api(s.state))
	mux.HandleFunc("GET /api/tools", s.api(s.tools))
	mux.HandleFunc("POST /api/send", s.api(s.send))
	mux.HandleFunc("POST /api/stop", s.api(s.stop))
	mux.HandleFunc("POST /api/answer", s.api(s.answer))
	mux.HandleFunc("POST /api/model", s.api(s.model))
	mux.HandleFunc("GET /api/projects", s.api(s.listProjects))
	mux.HandleFunc("POST /api/projects", s.api(s.createProject))
	mux.HandleFunc("POST /api/projects/open", s.api(s.openProject))
	mux.HandleFunc("GET /api/projects/{slug}", s.api(s.projectDetails))
	mux.HandleFunc("POST /api/projects/{slug}/media", s.api(s.importMedia))
	mux.HandleFunc("GET "+mediaRoute+"{ref...}", s.api(s.media))
	mux.HandleFunc("POST /api/probe", s.api(s.probe))
	mux.HandleFunc("POST /api/review", s.api(s.review))
	mux.HandleFunc("POST /api/decision", s.api(s.decision))
	mux.HandleFunc("GET /api/readiness", s.api(s.readiness))
	return s.guard(mux)
}

// guard refuses requests not addressed to this window: a page on another
// site can make the browser send requests here, but not with this Host
// (DNS rebinding) or, for writes, this Origin.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.ownHost(r.Host) {
			http.Error(w, "not this window", http.StatusMisdirectedRequest)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ownHost(host string) bool {
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1:"+port || host == "localhost:"+port
}

// api requires the run's token, as a header or, for what the browser loads
// itself and cannot give headers (event streams, and the media its video,
// audio and image elements load), as the t query parameter.
func (s *Server) api(h func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Facet-Token")
		if token == "" && (r.URL.Path == "/api/events" || strings.HasPrefix(r.URL.Path, mediaRoute)) {
			token = r.URL.Query().Get("t")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
			http.Error(w, "this window needs the address the Facet App opened", http.StatusUnauthorized)
			return
		}
		if err := h(w, r); err != nil {
			writeJSON(w, statusOf(err), map[string]any{"error": err.Error()})
		}
	}
}

// statusOf maps an error onto the response status the page shows it with.
func statusOf(err error) int {
	var bad badRequest
	switch {
	case errors.As(err, &bad), errors.Is(err, journeys.ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, kernel.ErrNotRunning):
		return http.StatusServiceUnavailable
	case errors.Is(err, kernel.ErrUnknownApproval), errors.Is(err, journeys.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, journeys.ErrOutsideProject), errors.Is(err, toolkit.ErrNotDirect):
		return http.StatusForbidden
	case errors.Is(err, journeys.ErrUnsupportedMedia):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, journeys.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, journeys.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

type badRequest struct{ msg string }

func (b badRequest) Error() string { return b.msg }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badRequest{"the request is not valid JSON: " + err.Error()}
	}
	return nil
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming is not supported")
	}
	ch := make(chan kernel.Event, 512)
	s.mu.Lock()
	replay := append([]kernel.Event(nil), s.history...)
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if _, open := s.clients[ch]; open {
			delete(s.clients, ch)
			close(ch)
		}
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	send := func(ev kernel.Event) error {
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "data: %s\n\n", data)
		return err
	}
	for _, ev := range replay {
		if err := send(ev); err != nil {
			return nil
		}
	}
	fmt.Fprint(w, ": replayed\n\n")
	flusher.Flush()
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case ev, open := <-ch:
			if !open {
				return nil
			}
			if send(ev) != nil {
				return nil
			}
			flusher.Flush()
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) error {
	s.mu.Lock()
	pending := make([]*kernel.Approval, 0, len(s.pending))
	for _, ev := range s.pending {
		pending = append(pending, ev.Approval)
	}
	status := s.status
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"status": status.Status, "detail": status.Detail, "model": s.host.Model(), "pending": pending})
	return nil
}

// tools lists the Toolkit's tools with their effects, and whether the window
// may run each itself (R1).
func (s *Server) tools(w http.ResponseWriter, r *http.Request) error {
	effects, err := s.toolkit.Effects(r.Context())
	if err != nil {
		return err
	}
	type row struct {
		toolkit.Effects
		Name   string `json:"name"`
		Direct bool   `json:"direct"`
	}
	rows := make([]row, 0, len(effects))
	for name, e := range effects {
		rows = append(rows, row{Effects: e, Name: name, Direct: e.Direct()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": rows})
	return nil
}

type sessionRequest struct {
	Session string `json:"session"`
	Text    string `json:"text"`
}

func (req *sessionRequest) validate(needText bool) error {
	req.Session = strings.TrimSpace(req.Session)
	if req.Session == "" {
		req.Session = "main"
	}
	if len(req.Session) > 64 || strings.ContainsAny(req.Session, "/?#&= ") {
		return badRequest{"the session name is not valid"}
	}
	if needText && strings.TrimSpace(req.Text) == "" {
		return badRequest{"the message is empty"}
	}
	return nil
}

func (s *Server) running() (kernel.Kernel, error) {
	if k := s.host.Kernel(); k != nil {
		return k, nil
	}
	return nil, kernel.ErrNotRunning
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) error {
	var req sessionRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := req.validate(true); err != nil {
		return err
	}
	// A message starting with / or ! is a command to the engine; the window
	// offers Stop for the one command a person needs, and refuses the rest
	// so a message can never approve a call or change the engine.
	if t := strings.TrimSpace(req.Text); strings.HasPrefix(t, "/") || strings.HasPrefix(t, "!") {
		return badRequest{"messages cannot start with / or !"}
	}
	k, err := s.running()
	if err != nil {
		return err
	}
	if err := k.Send(r.Context(), req.Session, req.Text); err != nil {
		return err
	}
	// The engine does not echo the person's message; the window keeps it,
	// so a reloaded page shows the whole conversation.
	s.Publish(kernel.Event{Kind: EventUserMessage, At: time.Now(), Session: req.Session, Text: req.Text})
	writeJSON(w, http.StatusAccepted, map[string]any{"session": req.Session})
	return nil
}

// EventUserMessage is a message the person sent, recorded by the window.
const EventUserMessage kernel.EventKind = "user_message"

func (s *Server) stop(w http.ResponseWriter, r *http.Request) error {
	var req sessionRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := req.validate(false); err != nil {
		return err
	}
	k, err := s.running()
	if err != nil {
		return err
	}
	if err := k.Cancel(r.Context(), req.Session); err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"session": req.Session})
	return nil
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ID      string `json:"id"`
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	k, err := s.running()
	if err != nil {
		return err
	}
	if err := k.Answer(req.ID, req.Approve, req.Reason); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID, "approved": req.Approve})
	return nil
}

func (s *Server) model(w http.ResponseWriter, r *http.Request) error {
	var m Model
	if err := decode(r, &m); err != nil {
		return err
	}
	m.Endpoint, m.ID, m.APIKey = strings.TrimSpace(m.Endpoint), strings.TrimSpace(m.ID), strings.TrimSpace(m.APIKey)
	if !strings.HasPrefix(m.Endpoint, "https://") && !strings.HasPrefix(m.Endpoint, "http://127.0.0.1") && !strings.HasPrefix(m.Endpoint, "http://localhost") {
		return badRequest{"the endpoint must be an https:// address, or http:// on this computer"}
	}
	if m.ID == "" || len(m.ID) > 200 {
		return badRequest{"name the model"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.host.SetModel(ctx, m); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": s.host.Model()})
	return nil
}
