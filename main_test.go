package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShowBroadcastsSSEEventWithDefaultsAndOptionalTitle(t *testing.T) {
	hub := newHub()
	app := newApp(hub)

	recorder := &sseRecorder{events: make(chan string, 1)}
	hub.register(recorder)
	defer hub.unregister(recorder)

	payload := `{"name":"Förnamn Efternamn","party":"s"}`
	req := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	app.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d with body %q", w.Code, w.Body.String())
	}

	event := waitForEvent(t, recorder.events)
	if !strings.HasPrefix(event, "event: show\n") {
		t.Fatalf("expected show SSE event, got %q", event)
	}
	var msg overlayMessage
	decodeSSEData(t, event, &msg)

	if msg.Type != "show" || msg.Name != "Förnamn Efternamn" || msg.Party != "s" {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if msg.Title != "" {
		t.Fatalf("expected empty title when omitted, got %q", msg.Title)
	}
	if msg.DurationIn != defaultDurationIn || msg.DurationOut != defaultDurationOut {
		t.Fatalf("expected default durations %d/%d, got %d/%d", defaultDurationIn, defaultDurationOut, msg.DurationIn, msg.DurationOut)
	}
}

func TestHideBroadcastsSSEEventWithRequestedDuration(t *testing.T) {
	hub := newHub()
	app := newApp(hub)

	recorder := &sseRecorder{events: make(chan string, 1)}
	hub.register(recorder)
	defer hub.unregister(recorder)

	payload := `{"duration_out":750}`
	req := httptest.NewRequest(http.MethodPost, "/api/hide", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	app.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d with body %q", w.Code, w.Body.String())
	}
	event := waitForEvent(t, recorder.events)
	if !strings.HasPrefix(event, "event: hide\n") {
		t.Fatalf("expected hide SSE event, got %q", event)
	}
	var msg overlayMessage
	decodeSSEData(t, event, &msg)
	if msg.Type != "hide" || msg.DurationOut != 750 {
		t.Fatalf("unexpected hide message: %+v", msg)
	}
}

func TestShowRejectsInvalidRequests(t *testing.T) {
	hub := newHub()
	app := newApp(hub)

	cases := []string{
		`{}`,
		`{"name":""}`,
		`{"name":"Valid","duration_in":-1}`,
		`{"name":"Valid","duration_out":-1}`,
	}
	for _, payload := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/show", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		app.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("payload %s: expected status 400, got %d", payload, w.Code)
		}
	}
}

func TestOverlayAndStaticAssetsRoutesExist(t *testing.T) {
	hub := newHub()
	app := newApp(hub)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected overlay status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Lower Third") {
		t.Fatalf("expected overlay HTML content, got %q", w.Body.String())
	}
}

func TestListenAddrFromArgsUsesDefaultPort(t *testing.T) {
	addr, err := listenAddrFromArgs(nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if addr != ":8080" {
		t.Fatalf("expected default addr :8080, got %q", addr)
	}
}

func TestListenAddrFromArgsUsesPortFlag(t *testing.T) {
	addr, err := listenAddrFromArgs([]string{"-port", "9090"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if addr != ":9090" {
		t.Fatalf("expected addr :9090, got %q", addr)
	}
}

func TestListenAddrFromArgsRejectsInvalidPort(t *testing.T) {
	_, err := listenAddrFromArgs([]string{"-port", "70000"})
	if err == nil {
		t.Fatal("expected invalid port to return an error")
	}
}

type sseRecorder struct {
	events chan string
}

func (r *sseRecorder) send(eventName string, payload overlayMessage) {
	data, _ := json.Marshal(payload)
	r.events <- "event: " + eventName + "\n" + "data: " + string(data) + "\n\n"
}

func waitForEvent(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE event")
		return ""
	}
}

func decodeSSEData(t *testing.T, event string, target any) {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		if strings.HasPrefix(line, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), target); err != nil {
				t.Fatalf("failed to decode SSE data: %v", err)
			}
			return
		}
	}
	t.Fatalf("no SSE data line found in %q", event)
}
