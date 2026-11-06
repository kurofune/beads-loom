package sse

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

var errDown = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")

// takeOutage ticks p into a published outage and drains the stream.
func takeOutage(t *testing.T, p *poller, pr *fakeProber, ch <-chan Event) {
	t.Helper()
	ctx := context.Background()
	p.Tick(ctx)
	pr.set(func(f *fakeProber) { f.hashErr = errDown; f.reason = "connection refused" })
	if p.Tick(ctx) {
		t.Fatal("a refused connection left the project reachable")
	}
	var down bool
	for _, ev := range drain(ch) {
		if h, ok := ev.Data.(ProjectHealth); ok && !h.Reachable {
			down = true
		}
	}
	if !down {
		t.Fatal("the outage published no reachable=false frame")
	}
}

// A server that comes back while its counts are slow is announced up before
// clients are told to refetch: the db-invalidated alone does not refresh the
// project list, so without the frame an open browser keeps the "bd dolt
// start" panel over a live server.
func TestPollerRecoveryWithSlowCountsAnnouncesReachableFirst(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	takeOutage(t, p, pr, ch)

	pr.set(func(f *fakeProber) { f.hashErr = nil; f.hash = "h2"; f.healthE = slowQuery })
	if !p.Tick(context.Background()) {
		t.Fatal("a recovered server with slow counts reported unreachable")
	}
	got := drain(ch)
	if want := []string{TypeProjectHealth, TypeDBInvalidated}; !equalStrings(types(got), want) {
		t.Fatalf("events = %v, want %v", types(got), want)
	}
	h := got[0].Data.(ProjectHealth)
	if !h.Reachable || !h.Pending || h.Reason != "" || h.Health != nil {
		t.Errorf("recovery frame = %+v, want reachable and pending with no counts", h)
	}
}

// A hash read that times out after an outage proves the server answered its
// liveness check, so the project is announced up and pending, and a new
// subscriber's snapshot agrees.
func TestPollerRecoveryWithSlowHashAnnouncesReachable(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	pr := &fakeProber{hash: "h1", health: queries.Health{Total: 3}}
	m := NewMonitor(hub, []string{"loom"}, MonitorOptions{Prober: pr, Clock: SystemClock})
	ch, cancel := hub.Subscribe()
	defer cancel()
	p := m.pollers["loom"]
	takeOutage(t, p, pr, ch)

	pr.set(func(f *fakeProber) { f.hashErr = slowQuery })
	if !p.Tick(context.Background()) {
		t.Fatal("a slow hash read after an outage reported unreachable")
	}
	got := drain(ch)
	if want := []string{TypeProjectHealth}; !equalStrings(types(got), want) {
		t.Fatalf("events = %v, want %v", types(got), want)
	}
	if h := got[0].Data.(ProjectHealth); !h.Reachable || !h.Pending || h.Reason != "" {
		t.Errorf("recovery frame = %+v, want reachable and pending", h)
	}
	snap := m.Snapshot()
	if len(snap) != 1 || !snap[0].Reachable || !snap[0].Pending || snap[0].Reason != "" {
		t.Errorf("snapshot = %+v, want reachable and pending", snap)
	}

	// A further slow hash read publishes nothing new.
	p.Tick(context.Background())
	if got := drain(ch); len(got) != 0 {
		t.Errorf("a second slow hash read published %v", types(got))
	}

	// The first hash read that lands invalidates and delivers the counts.
	pr.set(func(f *fakeProber) { f.hashErr = nil; f.hash = "h2" })
	p.Tick(context.Background())
	var invalidated, counted bool
	for _, ev := range drain(ch) {
		switch d := ev.Data.(type) {
		case DBInvalidated:
			invalidated = true
		case ProjectHealth:
			counted = counted || (d.Reachable && d.Health != nil)
		}
	}
	if !invalidated || !counted {
		t.Errorf("hash read after recovery: invalidated=%v counted=%v, want both", invalidated, counted)
	}
}

// blockingHealth holds every Health call until release is closed.
type blockingHealth struct {
	*fakeProber
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingHealth) Health(ctx context.Context, project string) (queries.Health, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.fakeProber.Health(ctx, project)
}

// While the first health read is still running after a successful hash read,
// the project is up with its counts pending, never unreachable.
func TestSnapshotDuringFirstHealthReadIsPending(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	pr := &blockingHealth{
		fakeProber: &fakeProber{hash: "h1", health: queries.Health{Total: 4}},
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	m := NewMonitor(hub, []string{"loom"}, MonitorOptions{Prober: pr, Clock: SystemClock})
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.pollers["loom"].Tick(context.Background())
	}()
	<-pr.entered

	snap := m.Snapshot()
	close(pr.release)
	<-done
	if len(snap) != 1 || !snap[0].Reachable || !snap[0].Pending || snap[0].Reason != "" {
		t.Errorf("snapshot mid-read = %+v, want reachable and pending", snap)
	}
}

// The same holds for a server that recovers from an outage: mid-read, its
// snapshot is up and pending, not the outage it just left.
func TestSnapshotDuringRecoveryHealthReadIsPending(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	fake := &fakeProber{hash: "h1", hashErr: errDown, health: queries.Health{Total: 4}}
	pr := &blockingHealth{fakeProber: fake, entered: make(chan struct{}), release: make(chan struct{})}
	m := NewMonitor(hub, []string{"loom"}, MonitorOptions{Prober: pr, Clock: SystemClock})
	p := m.pollers["loom"]
	p.Tick(context.Background())
	if snap := m.Snapshot(); snap[0].Reachable {
		t.Fatalf("snapshot during the outage = %+v, want unreachable", snap[0])
	}

	fake.set(func(f *fakeProber) { f.hashErr = nil })
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Tick(context.Background())
	}()
	<-pr.entered
	snap := m.Snapshot()
	close(pr.release)
	<-done
	if !snap[0].Reachable || !snap[0].Pending || snap[0].Reason != "" {
		t.Errorf("snapshot mid-recovery = %+v, want reachable and pending", snap[0])
	}
}
