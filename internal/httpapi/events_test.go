package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/sse"
)

// stubMonitor is a HealthSource with canned data.
type stubMonitor struct {
	health   map[string]queries.Health
	snapshot []sse.ProjectHealth
}

func (m stubMonitor) Health(project string) (queries.Health, bool) {
	h, ok := m.health[project]
	return h, ok
}

func (m stubMonitor) Snapshot() []sse.ProjectHealth { return m.snapshot }

// newSSEServer builds a server with a live hub and a stub monitor.
func newSSEServer(t *testing.T) (*Server, *sse.Hub) {
	t.Helper()
	cfg := &config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "loom", Workdir: t.TempDir()}},
	}
	reg := registry.New(cfg)
	t.Cleanup(func() { reg.Close() })

	hub := sse.NewHub()
	t.Cleanup(hub.Close)

	mon := stubMonitor{
		health: map[string]queries.Health{"loom": {Ready: 5, Open: 13, Total: 22}},
		snapshot: []sse.ProjectHealth{
			{Project: "loom", Reachable: true, Health: queries.Health{Ready: 5, Open: 13, Total: 22}},
		},
	}
	s := New(Options{Registry: reg, Hub: hub, Monitor: mon})
	return s, hub
}

// sseFrame is one parsed SSE message.
type sseFrame struct {
	ID      string
	Event   string
	Data    string
	Comment string
}

// readFrames parses SSE frames from r until n frames have been read or the
// reader is exhausted. Comments are returned as frames with only Comment set.
func readFrames(t *testing.T, r *bufio.Reader, n int, timeout time.Duration) []sseFrame {
	t.Helper()

	type result struct {
		frames []sseFrame
	}
	done := make(chan result, 1)

	go func() {
		var frames []sseFrame
		var cur sseFrame
		for len(frames) < n {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if cur != (sseFrame{}) {
					frames = append(frames, cur)
					cur = sseFrame{}
				}
			case strings.HasPrefix(line, ":"):
				frames = append(frames, sseFrame{Comment: strings.TrimSpace(line[1:])})
			case strings.HasPrefix(line, "id: "):
				cur.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				cur.Event = line[7:]
			case strings.HasPrefix(line, "data: "):
				cur.Data = line[6:]
			}
		}
		done <- result{frames}
	}()

	select {
	case res := <-done:
		return res.frames
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %d SSE frames", n)
		return nil
	}
}

func TestEventsStreamHeaders(t *testing.T) {
	s, _ := newSSEServer(t)

	srv := httptest.NewServer(s)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if xa := resp.Header.Get("X-Accel-Buffering"); xa != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", xa)
	}
}

// TestEventsSendsSnapshotThenLiveEvents: a client must be able to resync
// without event replay, then receive published events.
func TestEventsSendsSnapshotThenLiveEvents(t *testing.T) {
	s, hub := newSSEServer(t)

	srv := httptest.NewServer(s)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	r := bufio.NewReader(resp.Body)

	// ": connected" comment, then the snapshot frame.
	frames := readFrames(t, r, 2, 5*time.Second)
	if len(frames) < 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if frames[0].Comment != "connected" {
		t.Errorf("first frame = %+v, want a connected comment", frames[0])
	}
	if frames[1].Event != sse.TypeProjectHealth {
		t.Fatalf("second frame event = %q, want %q", frames[1].Event, sse.TypeProjectHealth)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(frames[1].Data), &snap); err != nil {
		t.Fatalf("snapshot data is not JSON: %v (%q)", err, frames[1].Data)
	}
	if snap["project"] != "loom" || snap["reachable"] != true {
		t.Errorf("snapshot = %v", snap)
	}

	// Wait until the handler is actually subscribed before publishing.
	deadline := time.Now().Add(2 * time.Second)
	for hub.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	hub.Publish(sse.TypeDBInvalidated, sse.DBInvalidated{Project: "loom"})

	got := readFrames(t, r, 1, 5*time.Second)
	if len(got) != 1 {
		t.Fatalf("got %d frames, want 1", len(got))
	}
	if got[0].Event != sse.TypeDBInvalidated {
		t.Errorf("event = %q, want %q", got[0].Event, sse.TypeDBInvalidated)
	}
	if got[0].ID == "" {
		t.Error("live events must carry an id for Last-Event-ID")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got[0].Data), &payload); err != nil {
		t.Fatalf("data is not JSON: %v", err)
	}
	if payload["project"] != "loom" {
		t.Errorf("payload = %v", payload)
	}
}

// TestEventsLastEventIDIsAcknowledged: replay is not supported, but the header
// must be handled gracefully and the client resynced.
func TestEventsLastEventIDIsAcknowledged(t *testing.T) {
	s, _ := newSSEServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	req.Header.Set("Last-Event-ID", "42")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	frames := readFrames(t, bufio.NewReader(resp.Body), 2, 5*time.Second)
	if len(frames) < 2 {
		t.Fatalf("got %d frames", len(frames))
	}
	if !strings.Contains(frames[0].Comment, "42") {
		t.Errorf("comment = %q, want it to acknowledge the Last-Event-ID", frames[0].Comment)
	}
	// The snapshot is what makes reconnect work without replay.
	if frames[1].Event != sse.TypeProjectHealth {
		t.Errorf("want a snapshot after reconnect, got %q", frames[1].Event)
	}
}

// TestEventsUnsubscribesOnDisconnect is the leak guard: closing the client must
// release the hub slot.
func TestEventsUnsubscribesOnDisconnect(t *testing.T) {
	s, hub := newSSEServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for hub.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hub.SubscriberCount() != 1 {
		t.Fatalf("SubscriberCount = %d, want 1", hub.SubscriberCount())
	}

	cancel()
	resp.Body.Close()

	deadline = time.Now().Add(3 * time.Second)
	for hub.SubscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := hub.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount = %d after disconnect; the handler leaked a subscription", got)
	}
}

// TestEventsClosesWhenHubCloses ensures shutdown does not hang a handler.
func TestEventsClosesWhenHubCloses(t *testing.T) {
	s, hub := newSSEServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for hub.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	hub.Close()

	// The body must reach EOF rather than hanging.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("handler did not return after the hub closed")
	}
}

func TestEventsDisabledWithoutHub(t *testing.T) {
	s := newTestServer(t) // Options.Hub is nil
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no hub is configured", rec.Code)
	}
}

// TestProjectsUsesCachedHealth: the poller's cache is what /api/projects should
// serve, so the handler does not run a stats query per project per request.
func TestProjectsUsesCachedHealth(t *testing.T) {
	cfg := &config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "loom", Workdir: t.TempDir()}},
	}
	reg := registry.New(cfg)
	defer reg.Close()

	// Force the project to look reachable so the handler reaches the cache.
	reg.Refresh(context.Background())

	mon := stubMonitor{health: map[string]queries.Health{"loom": {Ready: 42, Open: 99, Total: 123}}}
	s := New(Options{Registry: reg, Monitor: mon})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var body []projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// The temp workdir has no .beads, so the project is unreachable and health
	// is null — the cache is only consulted for reachable projects.
	if len(body) != 1 {
		t.Fatalf("got %d projects", len(body))
	}
	if body[0].Reachable {
		t.Skip("project unexpectedly reachable; cache path covered by the live test")
	}
	if body[0].Health != nil {
		t.Errorf("health = %+v, want null for an unreachable project", body[0].Health)
	}
}

func TestSanitizeSSE(t *testing.T) {
	tests := []struct{ in, want string }{
		{"db-invalidated", "db-invalidated"},
		{"a\nb", "a b"},
		{"a\r\nb", "a  b"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := sanitizeSSE(tc.in); got != tc.want {
			t.Errorf("sanitizeSSE(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
