// Package sse carries Loom's live-update channel: a generic publish/subscribe
// hub plus the pollers that decide when to publish.
//
// The hub is deliberately untyped in its payload (`Publish(type string, payload
// any)`) so phase 4 can put run events on the same stream without changing it.
package sse

import (
	"sync"
	"sync/atomic"
)

// Event types published today. Phase 4 adds run-* events alongside these.
const (
	// TypeDBInvalidated tells clients a project's data changed and any cached
	// query for it should be refetched.
	TypeDBInvalidated = "db-invalidated"
	// TypeProjectHealth carries a project's reachability, counts and write
	// state, published on every reachability or write-state transition and
	// whenever the counts change.
	TypeProjectHealth = "project-health"
	// TypeProjectsChanged announces that the project set changed (discovery).
	TypeProjectsChanged = "projects-changed"
)

// SubscriberBuffer is how many events a slow client may fall behind before the
// hub starts discarding. Loom's events are invalidation hints rather than a
// ledger, so dropping is safe: a client that misses one still refetches on the
// next event it does receive.
const SubscriberBuffer = 64

// Event is one message on the stream.
type Event struct {
	// ID is a hub-wide monotonic sequence number, sent as the SSE id field.
	ID uint64
	// Type becomes the SSE event field.
	Type string
	// Data becomes the SSE data field, JSON-encoded by the handler.
	Data any
}

// DBInvalidated is the payload of a db-invalidated event.
type DBInvalidated struct {
	Project string `json:"project"`
}

// ProjectsChanged is the payload of a projects-changed event.
type ProjectsChanged struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// ProjectHealth is the payload of a project-health event.
type ProjectHealth struct {
	Project   string `json:"project"`
	Reachable bool   `json:"reachable"`
	// Reason explains an unreachable project; empty when reachable.
	Reason string `json:"reason,omitempty"`
	// Health is nil when the project is unreachable.
	Health any `json:"health"`
	// Writable is false when bd writes to a database other than the one Loom
	// reads; WriteReason then says why. Both mirror registry.Project.
	Writable    bool   `json:"writable"`
	WriteReason string `json:"writeReason,omitempty"`
	// Pending marks a project whose first probe has not completed. Without it a
	// client connecting during startup would paint a live project as down until
	// the next event (review finding N1).
	Pending bool `json:"pending,omitempty"`
}

// Hub fans events out to every subscriber.
//
// Publish never blocks: a subscriber that is not keeping up loses its oldest
// buffered events rather than stalling the poller that produced them.
type Hub struct {
	mu     sync.RWMutex
	subs   map[uint64]*subscriber
	nextID uint64
	closed bool

	seq     atomic.Uint64
	dropped atomic.Uint64
}

type subscriber struct {
	id uint64
	ch chan Event
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[uint64]*subscriber)}
}

// Subscribe registers a new listener and returns its channel plus a cancel
// function. Cancel is idempotent and must be called to release the slot.
//
// The channel is closed when the subscription ends, so a reader can range over
// it; readers must not assume every published event arrives (see drop policy).
func (h *Hub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}

	h.nextID++
	s := &subscriber{id: h.nextID, ch: make(chan Event, SubscriberBuffer)}
	h.subs[s.id] = s

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if _, ok := h.subs[s.id]; ok {
				delete(h.subs, s.id)
				// Safe to close under the write lock: Publish sends while
				// holding the read lock, so a send can never overlap this.
				close(s.ch)
			}
		})
	}
	return s.ch, cancel
}

// Publish fans an event out to every current subscriber. It never blocks and is
// safe to call from many goroutines.
func (h *Hub) Publish(typ string, payload any) {
	ev := Event{ID: h.seq.Add(1), Type: typ, Data: payload}

	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	for _, s := range h.subs {
		h.send(s, ev)
	}
}

// send delivers one event, dropping the subscriber's oldest buffered event to
// make room when the buffer is full.
func (h *Hub) send(s *subscriber, ev Event) {
	select {
	case s.ch <- ev:
		return
	default:
	}

	// Full: discard the oldest, then retry. The second send can still fail if a
	// concurrent Publish refilled the slot, which is fine — the point is to
	// never block, not to guarantee delivery.
	select {
	case <-s.ch:
		h.dropped.Add(1)
	default:
	}
	select {
	case s.ch <- ev:
	default:
		h.dropped.Add(1)
	}
}

// SubscriberCount returns how many listeners are attached.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Dropped returns how many events have been discarded because a subscriber was
// too slow. Useful as a health signal; non-zero is not necessarily a problem.
func (h *Hub) Dropped() uint64 { return h.dropped.Load() }

// LastID returns the most recently assigned event id.
func (h *Hub) LastID() uint64 { return h.seq.Load() }

// Close ends every subscription. Publish becomes a no-op afterwards.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, s := range h.subs {
		delete(h.subs, id)
		close(s.ch)
	}
}
