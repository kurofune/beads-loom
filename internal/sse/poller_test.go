package sse

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/queries"
)

// fakeProber is a scriptable Prober.
type fakeProber struct {
	mu sync.Mutex

	hash    string
	hashErr error
	gen     uint64
	health  queries.Health
	healthE error
	reason  string
	// readOnly is the write-refusal reason; empty means writable.
	readOnly string

	hashCalls   int
	healthCalls int
}

func (f *fakeProber) Hash(ctx context.Context, project string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hashCalls++
	return f.hash, f.hashErr
}

func (f *fakeProber) Health(ctx context.Context, project string) (queries.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthCalls++
	return f.health, f.healthE
}

func (f *fakeProber) Generation(project string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen
}

func (f *fakeProber) Reason(project string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reason
}

func (f *fakeProber) Writable(project string) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readOnly == "", f.readOnly
}

func (f *fakeProber) set(fn func(*fakeProber)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeProber) counts() (hash, health int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hashCalls, f.healthCalls
}

// newTestPoller wires a poller to a hub and returns everything a test needs.
func newTestPoller(t *testing.T) (*poller, *fakeProber, <-chan Event, *Hub) {
	t.Helper()
	hub := NewHub()
	t.Cleanup(hub.Close)

	ch, cancel := hub.Subscribe()
	t.Cleanup(cancel)

	pr := &fakeProber{hash: "h1", health: queries.Health{Ready: 5, Open: 13, Total: 22}}
	p := &poller{
		project:      "loom",
		prober:       pr,
		hub:          hub,
		clock:        SystemClock,
		liveInterval: LiveInterval,
		downInterval: DownInterval,
	}
	return p, pr, ch, hub
}

// types extracts the event types from a batch, for readable assertions.
func types(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPollerFirstTickAnnouncesProject: the very first successful probe is a
// transition into reachable, so clients get health and an invalidation.
func TestPollerFirstTickAnnouncesProject(t *testing.T) {
	p, _, ch, _ := newTestPoller(t)
	ctx := context.Background()

	if !p.Tick(ctx) {
		t.Fatal("first tick should report reachable")
	}
	got := types(drain(ch))
	want := []string{TypeProjectHealth, TypeDBInvalidated}
	if !equalStrings(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// TestPollerQuietWhenNothingChanges is the property that makes a 1s poll
// acceptable: an idle project produces no traffic at all.
func TestPollerQuietWhenNothingChanges(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()

	p.Tick(ctx)
	drain(ch)

	for i := 0; i < 10; i++ {
		p.Tick(ctx)
	}
	if got := drain(ch); len(got) != 0 {
		t.Errorf("an idle project published %d events: %v", len(got), types(got))
	}

	// Health must not be recomputed on every tick either.
	_, healthCalls := pr.counts()
	if healthCalls != 1 {
		t.Errorf("health recomputed %d times, want 1 (only on the transition)", healthCalls)
	}
}

// TestPollerPublishesOnHashChange: a changed dolt hash invalidates, then
// refreshes counts.
func TestPollerPublishesOnHashChange(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()

	p.Tick(ctx)
	drain(ch)

	pr.set(func(f *fakeProber) {
		f.hash = "h2"
		f.health = queries.Health{Ready: 6, Open: 14, Total: 23}
	})
	p.Tick(ctx)

	got := drain(ch)
	if !equalStrings(types(got), []string{TypeDBInvalidated, TypeProjectHealth}) {
		t.Fatalf("events = %v, want [db-invalidated project-health]", types(got))
	}
	health, ok := got[1].Data.(ProjectHealth)
	if !ok {
		t.Fatalf("unexpected payload %#v", got[1].Data)
	}
	if h, ok := health.Health.(queries.Health); !ok || h.Ready != 6 {
		t.Errorf("published health = %#v, want the recomputed counts", health.Health)
	}

	// And it goes quiet again.
	p.Tick(ctx)
	if got := drain(ch); len(got) != 0 {
		t.Errorf("published %v after the hash settled", types(got))
	}
}

// TestPollerPublishesOnGenerationChange: a write made through Loom invalidates
// immediately, without waiting for the hash to be re-read. This is the
// Registry.Generation hook.
func TestPollerPublishesOnGenerationChange(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()

	p.Tick(ctx)
	drain(ch)

	// Hash unchanged, generation bumped by a local write.
	pr.set(func(f *fakeProber) { f.gen = 1 })
	p.Tick(ctx)

	if got := types(drain(ch)); !equalStrings(got, []string{TypeDBInvalidated, TypeProjectHealth}) {
		t.Errorf("events = %v, want a generation bump to invalidate", got)
	}
}

// TestPollerUnreachableTransition: going down publishes once, with a reason,
// and then stays quiet.
func TestPollerUnreachableTransition(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()

	p.Tick(ctx)
	drain(ch)

	pr.set(func(f *fakeProber) {
		f.hashErr = errors.New("dial tcp: connection refused")
		f.reason = "cannot connect to dolt server at 127.0.0.1:55933"
	})
	if p.Tick(ctx) {
		t.Fatal("tick should report unreachable")
	}

	got := drain(ch)
	if !equalStrings(types(got), []string{TypeProjectHealth}) {
		t.Fatalf("events = %v, want a single project-health", types(got))
	}
	payload := got[0].Data.(ProjectHealth)
	if payload.Reachable {
		t.Error("payload should report reachable=false")
	}
	if payload.Reason != "cannot connect to dolt server at 127.0.0.1:55933" {
		t.Errorf("reason = %q", payload.Reason)
	}
	if payload.Health != nil {
		t.Errorf("health = %#v, want nil while unreachable", payload.Health)
	}

	// A prolonged outage must not spam the stream.
	for i := 0; i < 5; i++ {
		p.Tick(ctx)
	}
	if got := drain(ch); len(got) != 0 {
		t.Errorf("a continuing outage published %d more events", len(got))
	}

	// The cached health must be invalidated, not left stale.
	if _, ok := p.cachedHealth(); ok {
		t.Error("cached health should be dropped while the project is down")
	}
}

// TestPollerRecovery: coming back publishes health and forces a refetch.
func TestPollerRecovery(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()

	p.Tick(ctx)
	pr.set(func(f *fakeProber) { f.hashErr = errors.New("down") })
	p.Tick(ctx)
	drain(ch)

	pr.set(func(f *fakeProber) {
		f.hashErr = nil
		f.hash = "h9"
		f.reason = ""
		f.health = queries.Health{Ready: 7, Open: 15, Total: 24}
	})
	if !p.Tick(ctx) {
		t.Fatal("tick should report reachable again")
	}

	got := drain(ch)
	if !equalStrings(types(got), []string{TypeProjectHealth, TypeDBInvalidated}) {
		t.Fatalf("events = %v, want [project-health db-invalidated] on recovery", types(got))
	}
	if payload := got[0].Data.(ProjectHealth); !payload.Reachable {
		t.Error("recovery payload should report reachable=true")
	}
	health, ok := p.cachedHealth()
	if !ok || health.Ready != 7 {
		t.Errorf("cached health = %+v (ok=%v), want the recomputed counts", health, ok)
	}
}

// TestPollerHealthFailureMarksUnreachable: if the hash succeeds but the health
// query fails, the project is treated as down rather than reported with stale
// counts.
func TestPollerHealthFailureMarksUnreachable(t *testing.T) {
	p, pr, ch, _ := newTestPoller(t)
	ctx := context.Background()

	pr.set(func(f *fakeProber) {
		f.healthE = errors.New("table missing")
		f.reason = "health query failed"
	})
	p.Tick(ctx)

	got := drain(ch)
	if len(got) == 0 {
		t.Fatal("expected an event")
	}
	payload := got[0].Data.(ProjectHealth)
	if payload.Reachable {
		t.Error("a failed health query should mark the project unreachable")
	}
	if _, ok := p.cachedHealth(); ok {
		t.Error("no health should be cached")
	}
}

func TestPollerInterval(t *testing.T) {
	p, _, _, _ := newTestPoller(t)

	p.reachable = true
	if got := p.interval(); got != LiveInterval {
		t.Errorf("reachable interval = %s, want %s", got, LiveInterval)
	}
	p.reachable = false
	if got := p.interval(); got != DownInterval {
		t.Errorf("unreachable interval = %s, want %s", got, DownInterval)
	}
}

// TestPollerRunUsesBackoffWhenDown drives the real loop with a fake clock and
// asserts the cadence switches to the backoff interval after a failure.
func TestPollerRunUsesBackoffWhenDown(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	ch, cancel := hub.Subscribe()
	defer cancel()

	clock := NewFakeClock(time.Unix(0, 0))
	pr := &fakeProber{hash: "h1", health: queries.Health{Total: 1}}
	p := &poller{
		project: "loom", prober: pr, hub: hub, clock: clock,
		liveInterval: LiveInterval, downInterval: DownInterval,
	}

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx)
	}()

	// First tick happens immediately, then the loop sleeps.
	if !clock.WaitForSleeper(2 * time.Second) {
		t.Fatal("poller never slept after its first tick")
	}
	hashes, _ := pr.counts()
	if hashes != 1 {
		t.Fatalf("hash probes = %d, want 1 before any time passes", hashes)
	}

	// A sub-interval advance must not wake it.
	clock.Advance(LiveInterval / 2)
	time.Sleep(20 * time.Millisecond)
	if hashes, _ := pr.counts(); hashes != 1 {
		t.Errorf("poller probed early: %d probes", hashes)
	}

	// Crossing the live interval wakes it.
	pr.set(func(f *fakeProber) { f.hashErr = errors.New("down"); f.reason = "down" })
	clock.Advance(LiveInterval)
	if !clock.WaitForSleeper(2 * time.Second) {
		t.Fatal("poller did not sleep again after the second tick")
	}
	if hashes, _ := pr.counts(); hashes != 2 {
		t.Errorf("hash probes = %d, want 2", hashes)
	}

	// Now down: the live interval must NOT be enough to wake it.
	clock.Advance(LiveInterval)
	time.Sleep(20 * time.Millisecond)
	if hashes, _ := pr.counts(); hashes != 2 {
		t.Errorf("poller used the live interval while down: %d probes", hashes)
	}

	// The backoff interval does.
	clock.Advance(DownInterval)
	if !clock.WaitForSleeper(2 * time.Second) {
		t.Fatal("poller did not resume after the backoff interval")
	}
	if hashes, _ := pr.counts(); hashes != 3 {
		t.Errorf("hash probes = %d, want 3 after the backoff elapsed", hashes)
	}

	stop()
	clock.Advance(DownInterval)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not exit on context cancellation")
	}
	drain(ch)
}

// TestMonitorIsolatesProjects: one dead project must not stop another's poller.
func TestMonitorIsolatesProjects(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	ch, cancel := hub.Subscribe()
	defer cancel()

	up := &fakeProber{hash: "up1", health: queries.Health{Total: 10}}
	down := &fakeProber{hashErr: errors.New("refused"), reason: "refused"}

	m := NewMonitor(hub, []string{"up", "down"}, MonitorOptions{
		Prober: routingProber{"up": up, "down": down},
		Clock:  SystemClock,
	})

	ctx := context.Background()
	for _, name := range m.order {
		m.pollers[name].Tick(ctx)
	}

	if h, ok := m.Health("up"); !ok || h.Total != 10 {
		t.Errorf("healthy project health = %+v (ok=%v)", h, ok)
	}
	if _, ok := m.Health("down"); ok {
		t.Error("dead project should have no cached health")
	}
	if _, ok := m.Health("nonexistent"); ok {
		t.Error("unknown project should report no health")
	}

	events := drain(ch)
	var sawUp, sawDown bool
	for _, e := range events {
		p, ok := e.Data.(ProjectHealth)
		if !ok {
			continue
		}
		if p.Project == "up" && p.Reachable {
			sawUp = true
		}
		if p.Project == "down" && !p.Reachable {
			sawDown = true
		}
	}
	if !sawUp || !sawDown {
		t.Errorf("expected health for both projects, got %v", types(events))
	}

	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot has %d entries, want 2", len(snap))
	}
	if !snap[0].Reachable || snap[1].Reachable {
		t.Errorf("snapshot = %+v", snap)
	}
	if snap[1].Reason != "refused" {
		t.Errorf("unreachable snapshot reason = %q", snap[1].Reason)
	}
}

// routingProber dispatches to a per-project fake.
type routingProber map[string]*fakeProber

func (r routingProber) Hash(ctx context.Context, project string) (string, error) {
	return r[project].Hash(ctx, project)
}
func (r routingProber) Health(ctx context.Context, project string) (queries.Health, error) {
	return r[project].Health(ctx, project)
}
func (r routingProber) Generation(project string) uint64 { return r[project].Generation(project) }
func (r routingProber) Reason(project string) string     { return r[project].Reason(project) }
func (r routingProber) Writable(project string) (bool, string) {
	return r[project].Writable(project)
}

// TestMonitorRunExitsCleanly guards against leaking poller goroutines.
func TestMonitorRunExitsCleanly(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	clock := NewFakeClock(time.Unix(0, 0))
	m := NewMonitor(hub, []string{"a", "b", "c"}, MonitorOptions{
		Prober: routingProber{
			"a": {hash: "1"}, "b": {hash: "2"}, "c": {hashErr: errors.New("down")},
		},
		Clock: clock,
	})

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Run(ctx)
	}()

	// Wait for all three to reach their first sleep.
	for i := 0; i < 3; i++ {
		if !clock.WaitForSleeper(2 * time.Second) {
			t.Fatalf("only %d pollers started", i)
		}
	}

	stop()
	clock.Advance(DownInterval)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Monitor.Run did not return after cancellation")
	}
}

func TestMonitorDynamicMembership(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	prober := &blockingProber{
		started: make(chan string, 4),
		exited:  make(chan string, 4),
	}
	m := NewMonitor(hub, []string{"before"}, MonitorOptions{Prober: prober})

	if got := m.Snapshot(); len(got) != 1 || got[0].Project != "before" {
		t.Fatalf("pre-Run snapshot = %+v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()
	waitProject(t, prober.started, "before")

	m.Add("during")
	waitProject(t, prober.started, "during")
	if got := m.Snapshot(); len(got) != 2 || got[0].Project != "before" || got[1].Project != "during" {
		t.Fatalf("dynamic snapshot = %+v", got)
	}

	m.Remove("before")
	waitProject(t, prober.exited, "before")
	if _, ok := m.Health("before"); ok {
		t.Fatal("removed project retained cached health")
	}
	if got := m.Snapshot(); len(got) != 1 || got[0].Project != "during" {
		t.Fatalf("snapshot after removal = %+v", got)
	}

	m.Remove("during")
	waitProject(t, prober.exited, "during")
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Monitor.Run did not drain dynamically added pollers")
	}
}

type blockingProber struct {
	started chan string
	exited  chan string
}

func (p *blockingProber) Hash(ctx context.Context, project string) (string, error) {
	p.started <- project
	<-ctx.Done()
	p.exited <- project
	return "", ctx.Err()
}

func (*blockingProber) Health(context.Context, string) (queries.Health, error) {
	return queries.Health{}, nil
}
func (*blockingProber) Generation(string) uint64 { return 0 }
func (*blockingProber) Reason(string) string     { return "stopped" }
func (*blockingProber) Writable(string) (bool, string) {
	return true, ""
}

func waitProject(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("project = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for project %q", want)
	}
}

func TestFakeClock(t *testing.T) {
	c := NewFakeClock(time.Unix(1000, 0))
	if got := c.Now().Unix(); got != 1000 {
		t.Errorf("Now = %d", got)
	}

	ch := c.After(5 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired before its deadline")
	default:
	}

	c.Advance(2 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}

	c.Advance(3 * time.Second)
	select {
	case at := <-ch:
		if at.Unix() != 1005 {
			t.Errorf("fired at %d, want 1005", at.Unix())
		}
	case <-time.After(time.Second):
		t.Fatal("did not fire at its deadline")
	}

	if c.Sleepers() != 0 {
		t.Errorf("Sleepers = %d, want 0 after firing", c.Sleepers())
	}
}

// TestN1SnapshotDistinguishesPendingFromUnreachable is the regression test for
// review finding N1: a client connecting before the first probe completes used
// to be told the project was down.
func TestN1SnapshotDistinguishesPendingFromUnreachable(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	up := &fakeProber{hash: "h1", health: queries.Health{Total: 10}}
	down := &fakeProber{hashErr: errors.New("refused"), reason: "connection refused"}
	pending := &fakeProber{hash: "h2", health: queries.Health{Total: 5}}

	m := NewMonitor(hub, []string{"up", "down", "pending"}, MonitorOptions{
		Prober: routingProber{"up": up, "down": down, "pending": pending},
		Clock:  SystemClock,
	})

	ctx := context.Background()
	// Tick only two of the three; "pending" has never been probed.
	m.pollers["up"].Tick(ctx)
	m.pollers["down"].Tick(ctx)

	snap := m.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot has %d entries", len(snap))
	}
	byName := map[string]ProjectHealth{}
	for _, p := range snap {
		byName[p.Project] = p
	}

	if got := byName["up"]; !got.Reachable || got.Pending || got.Health == nil {
		t.Errorf("healthy project = %+v", got)
	}
	if got := byName["down"]; got.Reachable || got.Pending || got.Reason == "" {
		t.Errorf("unreachable project = %+v", got)
	}

	// The point of the fix: not-yet-probed is neither healthy-with-counts nor
	// down. It must not claim the project is unreachable.
	got := byName["pending"]
	if !got.Pending {
		t.Errorf("unprobed project = %+v, want pending=true", got)
	}
	if !got.Reachable {
		t.Error("an unprobed project must not be reported as unreachable")
	}
	if got.Reason != "" {
		t.Errorf("unprobed project carries a reason %q it cannot know", got.Reason)
	}
	if got.Health != nil {
		t.Errorf("unprobed project reports health %v it has not computed", got.Health)
	}

	// Once probed, it becomes a normal healthy entry.
	m.pollers["pending"].Tick(ctx)
	for _, p := range m.Snapshot() {
		if p.Project != "pending" {
			continue
		}
		if p.Pending || !p.Reachable || p.Health == nil {
			t.Errorf("after probing = %+v", p)
		}
	}
}

// TestN1ProbedFlagSetEvenOnFailure: a failed probe still counts as probed, so a
// genuinely down project reports unreachable rather than pending forever.
func TestN1ProbedFlagSetOnFailure(t *testing.T) {
	p, pr, _, _ := newTestPoller(t)
	pr.set(func(f *fakeProber) { f.hashErr = errors.New("down"); f.reason = "down" })

	if p.probed() {
		t.Fatal("poller reports probed before any tick")
	}
	p.Tick(context.Background())
	if !p.probed() {
		t.Error("a failed probe should still mark the poller as probed")
	}
}

// onlyHealth asserts the batch is exactly one project-health event and returns
// its payload.
func onlyHealth(t *testing.T, events []Event) ProjectHealth {
	t.Helper()
	if got := types(events); !equalStrings(got, []string{TypeProjectHealth}) {
		t.Fatalf("events = %v, want a single project-health", got)
	}
	return events[0].Data.(ProjectHealth)
}

// TestPollerPublishesWriteStateTransitions: the registry re-derives write state
// on its own refresh, with no hash change, so the poller must publish the
// transition itself or the UI keeps the stale state.
func TestPollerPublishesWriteStateTransitions(t *testing.T) {
	const why = "Loom reads \"a\" but bd writes to \"b\""
	t.Run("reachable", func(t *testing.T) {
		p, pr, ch, _ := newTestPoller(t)
		ctx := context.Background()
		p.Tick(ctx)
		if h := drain(ch)[0].Data.(ProjectHealth); !h.Writable || h.WriteReason != "" {
			t.Fatalf("first event = %+v, want writable", h)
		}

		pr.set(func(f *fakeProber) { f.readOnly = why })
		p.Tick(ctx)
		if h := onlyHealth(t, drain(ch)); h.Writable || h.WriteReason != why || !h.Reachable || h.Health == nil {
			t.Fatalf("after split: %+v, want reachable, counts, read-only with the reason", h)
		}

		p.Tick(ctx)
		if got := drain(ch); len(got) != 0 {
			t.Fatalf("unchanged write state published %v", types(got))
		}

		pr.set(func(f *fakeProber) { f.readOnly = "" })
		p.Tick(ctx)
		if h := onlyHealth(t, drain(ch)); !h.Writable || h.WriteReason != "" {
			t.Fatalf("after repair: %+v, want writable and no reason", h)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		p, pr, ch, _ := newTestPoller(t)
		ctx := context.Background()
		pr.set(func(f *fakeProber) { f.hashErr = errors.New("down"); f.reason = "down" })
		p.Tick(ctx)
		drain(ch)

		pr.set(func(f *fakeProber) { f.readOnly = why })
		p.Tick(ctx)
		if h := onlyHealth(t, drain(ch)); h.Writable || h.WriteReason != why || h.Reachable {
			t.Fatalf("after split: %+v, want unreachable and read-only with the reason", h)
		}

		p.Tick(ctx)
		if got := drain(ch); len(got) != 0 {
			t.Fatalf("an unchanged outage published %v", types(got))
		}

		pr.set(func(f *fakeProber) { f.readOnly = "" })
		p.Tick(ctx)
		if h := onlyHealth(t, drain(ch)); !h.Writable || h.Reachable {
			t.Fatalf("after repair: %+v, want unreachable and writable", h)
		}
	})
}

// TestSnapshotCarriesWriteState: a connecting client learns the write state
// from the snapshot, not only from a later transition.
func TestSnapshotCarriesWriteState(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	ro := &fakeProber{hash: "h1", readOnly: "split"}
	rw := &fakeProber{hash: "h2"}
	m := NewMonitor(hub, []string{"ro", "rw"}, MonitorOptions{
		Prober: routingProber{"ro": ro, "rw": rw},
		Clock:  SystemClock,
	})
	m.pollers["ro"].Tick(context.Background())

	for _, h := range m.Snapshot() {
		wantWritable := h.Project == "rw"
		if h.Writable != wantWritable || (wantWritable != (h.WriteReason == "")) {
			t.Errorf("snapshot %s = %+v, want writable=%v", h.Project, h, wantWritable)
		}
	}
}
