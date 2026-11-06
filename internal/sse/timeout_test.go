package sse

import (
	"context"
	"fmt"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/registry"
)

var slowQuery = fmt.Errorf("%w: loom: stats ready: %w", registry.ErrQueryTimeout, context.DeadlineExceeded)

// A health query that times out on a live server must not announce an outage:
// the UI would swap the backlog for the "bd dolt start" panel over a slow
// count. The next tick retries the counts.
func TestPollerHealthTimeoutIsNotAnOutage(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()

	pr.set(func(f *fakeProber) { f.healthE = slowQuery })
	if !p.Tick(ctx) {
		t.Fatal("tick reported the project unreachable over a slow health query")
	}
	for _, ev := range drain(ch) {
		if h, ok := ev.Data.(ProjectHealth); ok && !h.Reachable {
			t.Fatalf("published an outage for a slow query: %+v", h)
		}
	}

	pr.set(func(f *fakeProber) { f.healthE = nil })
	p.Tick(ctx)
	var healed bool
	for _, ev := range drain(ch) {
		if h, ok := ev.Data.(ProjectHealth); ok && h.Reachable && h.Health != nil {
			healed = true
		}
	}
	if !healed {
		t.Error("the tick after a timed-out health query did not retry and publish the counts")
	}
	if _, health := pr.counts(); health != 2 {
		t.Errorf("health computed %d times, want 2 (the timeout, then the retry)", health)
	}
}

// A timed-out health query on a project that had counts keeps them rather
// than dropping to "no health", and the snapshot still reports it reachable.
func TestSnapshotAfterHealthTimeoutStaysReachable(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	pr := &fakeProber{hash: "h1", health: queries.Health{Total: 10}}
	fresh := &fakeProber{hash: "h1", healthE: slowQuery}
	m := NewMonitor(hub, []string{"warm", "cold"}, MonitorOptions{
		Prober: routingProber{"warm": pr, "cold": fresh},
		Clock:  SystemClock,
	})
	ctx := context.Background()
	m.pollers["warm"].Tick(ctx)
	m.pollers["cold"].Tick(ctx)

	pr.set(func(f *fakeProber) { f.hash = "h2"; f.healthE = slowQuery })
	m.pollers["warm"].Tick(ctx)

	for _, got := range m.Snapshot() {
		if !got.Reachable || got.Reason != "" {
			t.Errorf("%s = %+v, want reachable after a slow health query", got.Project, got)
		}
		switch got.Project {
		case "warm":
			if h, ok := got.Health.(queries.Health); !ok || h.Total != 10 {
				t.Errorf("warm health = %+v, want the last counts kept", got.Health)
			}
		case "cold":
			if !got.Pending {
				t.Errorf("cold = %+v, want pending until its counts arrive", got)
			}
		}
	}
}

// A slow hash read is not an outage either: the tick changes nothing.
func TestPollerHashTimeoutIsNotAnOutage(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()
	p.Tick(ctx)
	drain(ch)

	pr.set(func(f *fakeProber) { f.hashErr = slowQuery })
	if !p.Tick(ctx) {
		t.Fatal("a slow hash read marked the project unreachable")
	}
	if got := drain(ch); len(got) != 0 {
		t.Errorf("a slow hash read published %v", types(got))
	}
	if _, ok := p.cachedHealth(); !ok {
		t.Error("a slow hash read dropped the cached counts")
	}
}

// A slow first hash read on a live server leaves the project reachable and
// pending: a new subscriber's snapshot must not show an outage for a server
// that answered its liveness check. The next tick that reads the hash
// computes and publishes the counts.
func TestSnapshotAfterColdHashTimeoutIsPending(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	pr := &fakeProber{hashErr: slowQuery, health: queries.Health{Total: 7}}
	m := NewMonitor(hub, []string{"cold"}, MonitorOptions{Prober: pr, Clock: SystemClock})
	ctx := context.Background()
	ch, cancel := hub.Subscribe()
	defer cancel()

	if !m.pollers["cold"].Tick(ctx) {
		t.Fatal("a slow first hash read marked the project unreachable")
	}
	snap := m.Snapshot()
	if len(snap) != 1 || !snap[0].Reachable || !snap[0].Pending || snap[0].Reason != "" {
		t.Fatalf("snapshot = %+v, want reachable and pending after a slow first hash read", snap)
	}
	for _, ev := range drain(ch) {
		if h, ok := ev.Data.(ProjectHealth); ok && !h.Reachable {
			t.Fatalf("published an outage for a slow hash read: %+v", h)
		}
	}

	pr.set(func(f *fakeProber) { f.hash = "h1"; f.hashErr = nil })
	m.pollers["cold"].Tick(ctx)
	snap = m.Snapshot()
	if h, ok := snap[0].Health.(queries.Health); !ok || h.Total != 7 || snap[0].Pending {
		t.Errorf("snapshot after the hash read = %+v, want the counts", snap[0])
	}
}
