package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPort        = 8080
	defaultAddr        = ":8080"
	defaultDurationIn  = 500
	defaultDurationOut = 500
	maxJSONBodyBytes   = 1 << 20
)

type showRequest struct {
	Name        string `json:"name"`
	Party       string `json:"party"`
	Title       string `json:"title"`
	DurationIn  int    `json:"duration_in"`
	DurationOut int    `json:"duration_out"`
}

type hideRequest struct {
	DurationOut int `json:"duration_out"`
}

type overlayMessage struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Party       string `json:"party,omitempty"`
	Title       string `json:"title,omitempty"`
	DurationIn  int    `json:"duration_in,omitempty"`
	DurationOut int    `json:"duration_out,omitempty"`
}

type subscriber interface {
	send(eventName string, payload overlayMessage)
}

type hub struct {
	mu          sync.RWMutex
	subscribers map[subscriber]struct{}
}

func newHub() *hub {
	return &hub{subscribers: make(map[subscriber]struct{})}
}

func (h *hub) register(s subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[s] = struct{}{}
}

func (h *hub) unregister(s subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers, s)
}

func (h *hub) broadcast(eventName string, payload overlayMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subscribers {
		s.send(eventName, payload)
	}
}

type app struct {
	hub             *hub
	router          *http.ServeMux
	template        *template.Template
	stateMu         sync.Mutex
	lastDurationOut int
}

func newApp(h *hub) http.Handler {
	a := &app{
		hub:             h,
		router:          http.NewServeMux(),
		lastDurationOut: defaultDurationOut,
	}
	a.loadTemplate()
	a.routes()
	return a
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.router.ServeHTTP(w, r)
}

func (a *app) routes() {
	a.router.HandleFunc("GET /", a.handleOverlay)
	a.router.HandleFunc("GET /events", a.handleEvents)
	a.router.HandleFunc("POST /api/show", a.handleShow)
	a.router.HandleFunc("POST /api/hide", a.handleHide)
	a.router.Handle("GET /assets/logos/", http.StripPrefix("/assets/logos/", http.FileServer(http.Dir(filepath.Join("assets", "logos")))))
}

func (a *app) loadTemplate() {
	path := filepath.Join("templates", "overlay.html")
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		// Keeps tests and first-run diagnostics usable even if the template file is missing.
		tmpl = template.Must(template.New("overlay.html").Parse(`<!doctype html><html><head><title>Lower Third</title></head><body>Lower Third</body></html>`))
	}
	a.template = tmpl
}

func (a *app) handleOverlay(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		notFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.template.Execute(w, map[string]any{"Title": "Lower Third"}); err != nil {
		http.Error(w, "failed to render overlay", http.StatusInternalServerError)
	}
}

func (a *app) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}

	client := newSSEClient(w, flusher)
	a.hub.register(client)
	defer a.hub.unregister(client)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case msg := <-client.messages:
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (a *app) handleShow(w http.ResponseWriter, r *http.Request) {
	var req showRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	msg, err := normalizeShow(req)
	if err != nil {
		badRequest(w, err)
		return
	}

	a.stateMu.Lock()
	a.lastDurationOut = msg.DurationOut
	a.stateMu.Unlock()

	a.hub.broadcast("show", msg)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "shown"})
}

func (a *app) handleHide(w http.ResponseWriter, r *http.Request) {
	var req hideRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			badRequest(w, err)
			return
		}
	}

	a.stateMu.Lock()
	durationOut := a.lastDurationOut
	a.stateMu.Unlock()

	if req.DurationOut != 0 {
		durationOut = req.DurationOut
	}
	if durationOut < 0 {
		badRequest(w, errors.New("duration_out must be zero or a positive integer"))
		return
	}
	if durationOut == 0 {
		durationOut = defaultDurationOut
	}

	msg := overlayMessage{Type: "hide", DurationOut: durationOut}
	a.hub.broadcast("hide", msg)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "hidden"})
}

func normalizeShow(req showRequest) (overlayMessage, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return overlayMessage{}, errors.New("name is required")
	}
	if req.DurationIn < 0 {
		return overlayMessage{}, errors.New("duration_in must be zero or a positive integer")
	}
	if req.DurationOut < 0 {
		return overlayMessage{}, errors.New("duration_out must be zero or a positive integer")
	}

	durationIn := req.DurationIn
	if durationIn == 0 {
		durationIn = defaultDurationIn
	}
	durationOut := req.DurationOut
	if durationOut == 0 {
		durationOut = defaultDurationOut
	}

	return overlayMessage{
		Type:        "show",
		Name:        name,
		Party:       sanitizeParty(req.Party),
		Title:       strings.TrimSpace(req.Title),
		DurationIn:  durationIn,
		DurationOut: durationOut,
	}, nil
}

func sanitizeParty(party string) string {
	party = strings.ToLower(strings.TrimSpace(party))
	party = strings.TrimSuffix(party, ".png")
	var b strings.Builder
	for _, r := range party {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func badRequest(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

type sseClient struct {
	messages chan string
}

func newSSEClient(_ http.ResponseWriter, _ http.Flusher) *sseClient {
	return &sseClient{messages: make(chan string, 16)}
}

func (c *sseClient) send(eventName string, payload overlayMessage) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, data)
	select {
	case c.messages <- msg:
	default:
		// Drop an event instead of blocking API requests if an OBS/browser client is slow.
	}
}

func listenAddrFromArgs(args []string) (string, error) {
	flags := flag.NewFlagSet("obs-lowerthird", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	port := flags.Int("port", defaultPort, "HTTP-port att lyssna på")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() > 0 {
		positionalPort, err := strconv.Atoi(flags.Arg(0))
		if err != nil {
			return "", fmt.Errorf("invalid port %q: %w", flags.Arg(0), err)
		}
		*port = positionalPort
	}
	if *port < 1 || *port > 65535 {
		return "", fmt.Errorf("port must be between 1 and 65535, got %d", *port)
	}
	return fmt.Sprintf(":%d", *port), nil
}

func main() {
	addr, err := listenAddrFromArgs(os.Args[1:])
	if err != nil {
		log.Fatalf("invalid command line arguments: %v", err)
	}

	h := newHub()
	server := &http.Server{
		Addr:              addr,
		Handler:           newApp(h),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("OBS lower third server listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
