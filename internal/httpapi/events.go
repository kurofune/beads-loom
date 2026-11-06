package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kurofune/beads-loom/internal/sse"
)

// heartbeatInterval keeps the connection alive through proxies and lets a
// client notice a dead server. It is sent as an SSE comment, which browsers
// ignore.
const heartbeatInterval = 15 * time.Second

// handleEvents streams live updates.
//
// Reconnect handling is deliberately simple: the hub keeps no history, so
// Last-Event-ID cannot be honoured as replay. Instead every new subscription
// immediately receives a project-health snapshot, which is enough for the UI to
// resync — and it refetches on the next db-invalidated anyway.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Tell nginx and friends not to buffer the stream.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, cancel := s.hub.Subscribe()
	defer cancel()

	ctx := r.Context()

	// Opening comment so the client sees bytes immediately and treats the
	// connection as established.
	if lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); lastID != "" {
		fmt.Fprintf(w, ": reconnected (Last-Event-ID %s; no replay, sending a snapshot)\n\n", sanitizeSSE(lastID))
	} else {
		fmt.Fprint(w, ": connected\n\n")
	}
	flusher.Flush()

	// Snapshot so a fresh or reconnecting client starts from a known state.
	if s.monitor != nil {
		for _, snap := range s.monitor.Snapshot() {
			if !s.writeEvent(w, flusher, sse.Event{Type: sse.TypeProjectHealth, Data: snap}) {
				return
			}
		}
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, open := <-ch:
			if !open {
				// The hub shut down.
				return
			}
			if !s.writeEvent(w, flusher, ev) {
				return
			}

		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeEvent emits one SSE frame and flushes. It reports whether the write
// succeeded; a failure means the client is gone and the handler should return.
func (s *Server) writeEvent(w http.ResponseWriter, flusher http.Flusher, ev sse.Event) bool {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		// A payload we cannot encode is a bug on our side; drop the event
		// rather than killing the client's stream.
		s.log.Error("cannot encode SSE payload", "type", ev.Type, "err", err)
		return true
	}

	var b strings.Builder
	if ev.ID != 0 {
		fmt.Fprintf(&b, "id: %d\n", ev.ID)
	}
	fmt.Fprintf(&b, "event: %s\n", sanitizeSSE(ev.Type))
	// JSON never contains a raw newline, so a single data line is always valid.
	fmt.Fprintf(&b, "data: %s\n\n", data)

	if _, err := w.Write([]byte(b.String())); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// sanitizeSSE strips characters that would break the line-oriented SSE framing.
func sanitizeSSE(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}
