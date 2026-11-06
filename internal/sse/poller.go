package sse

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/registry"
)

// Poll cadences. LiveInterval is the ARCHITECTURE-specified 1s hash poll;
// DownInterval backs a dead server off so a project that has been down for an
// hour is not hammered once a second.
const (
	LiveInterval = 1 * time.Second
	DownInterval = 15 * time.Second
)

// Prober is what a poller needs from the outside world for one project. Both
// methods must respect ctx and must not block indefinitely.
type Prober interface {
	// Hash returns the project's current dolt_hashof_db().
	Hash(ctx context.Context, project string) (string, error)
	// Health recomputes the project's issue counts.
	Health(ctx context.Context, project string) (queries.Health, error)
	// Generation returns the project's write generation, which Loom bumps on
	// every write it performs. It lets a local write invalidate immediately
	// instead of waiting for the next hash poll.
	Generation(project string) uint64
	// Reason returns the current human-readable unreachability reason.
	Reason(project string) string
	// Writable reports whether writes to the project are allowed and, when
	// they are not, why.
	Writable(project string) (bool, string)
}

// poller watches one project. One goroutine per project, with its own state and
// its own timeouts, so a hung or dead server can never delay another project.
type poller struct {
	project string
	prober  Prober
	hub     *Hub
	clock   Clock

	liveInterval time.Duration
	downInterval time.Duration

	// state, owned by the poller goroutine except where noted.
	started bool
	// everProbed is set once any probe has completed. Guarded by mu.
	everProbed bool
	// reachable says whether the server is up, independent of whether its
	// counts are known. Written under mu so Snapshot can read it.
	reachable  bool
	lastHash   string
	lastGen    uint64
	lastHealth queries.Health
	haveHealth bool
	// healthDue is set when the counts timed out on a live server; the next
	// tick recomputes them even if nothing moved. Guarded by mu.
	healthDue bool
	writable  bool
	writeWhy  string

	mu sync.Mutex // guards the snapshot readers below use
}

// Tick performs exactly one iteration of the state machine and reports whether
// the project is currently reachable.
//
// It is exported through Monitor only for tests; keeping the whole decision in
// one clock-free method is what makes the state machine testable without
// sleeping.
func (p *poller) Tick(ctx context.Context) bool {
	writeChanged := p.observeWritable()
	hash, err := p.prober.Hash(ctx, p.project)
	if errors.Is(err, registry.ErrQueryTimeout) {
		// The server answered its liveness check; only this read was slow.
		// Reachability does not wait for counts: a project that was down, or
		// never checked, is announced up with its counts pending. lastHash is
		// left empty so the first hash read that lands invalidates and
		// computes them.
		if !p.reachable {
			p.started = true
			p.lastHash = ""
			p.markReachable()
			p.setHealthDue()
			p.publishUp()
		} else if writeChanged {
			p.publishUp()
		}
		return true
	}
	if err != nil {
		p.onUnreachable(err, writeChanged)
		return false
	}

	gen := p.prober.Generation(p.project)
	recovered := !p.reachable
	changed := p.started && (hash != p.lastHash || gen != p.lastGen)

	p.started = true
	p.markReachable()
	p.lastHash = hash
	p.lastGen = gen

	switch {
	case recovered:
		// First successful probe, or the server came back. Announce it with
		// the counts, or pending when they are slow, before telling clients
		// to refetch everything for this project.
		p.publishHealth(ctx, true)
		p.hub.Publish(TypeDBInvalidated, DBInvalidated{Project: p.project})
	case changed:
		// The data moved: invalidate, then recompute counts. Counts are only
		// recomputed here and on reachability transitions, which is what keeps
		// this from being a second polling loop over the database.
		p.hub.Publish(TypeDBInvalidated, DBInvalidated{Project: p.project})
		p.publishHealth(ctx, false)
	case writeChanged, p.isHealthDue():
		// The registry re-derives write state on its own refresh, without the
		// data moving, so nothing else would tell clients. A count that timed
		// out last tick is retried here too.
		p.publishHealth(ctx, writeChanged)
	}
	return true
}

// markReachable records the server as up.
func (p *poller) markReachable() {
	p.mu.Lock()
	p.everProbed = true
	p.reachable = true
	p.mu.Unlock()
}

func (p *poller) setHealthDue() {
	p.mu.Lock()
	p.healthDue = true
	p.mu.Unlock()
}

// publishUp announces the project reachable without recomputing its counts:
// the cached ones when there are any, otherwise pending.
func (p *poller) publishUp() {
	health, have := p.cachedHealth()
	frame := ProjectHealth{
		Project:     p.project,
		Reachable:   true,
		Writable:    p.writable,
		WriteReason: p.writeWhy,
	}
	if have {
		frame.Health = health
	} else {
		frame.Pending = true
	}
	p.hub.Publish(TypeProjectHealth, frame)
}

func (p *poller) isHealthDue() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthDue
}

// observeWritable reads the project's write state and reports whether it moved
// since the last tick. The first tick always publishes, so it reports false.
func (p *poller) observeWritable() bool {
	writable, why := p.prober.Writable(p.project)
	changed := p.started && (writable != p.writable || why != p.writeWhy)
	p.writable, p.writeWhy = writable, why
	return changed
}

// onUnreachable records a failed probe, publishing only on the transition, or
// when the write state moved, so a long outage does not spam the stream.
func (p *poller) onUnreachable(err error, writeChanged bool) {
	wasReachable := p.reachable || !p.started
	p.started = true
	p.lastHash = ""

	p.mu.Lock()
	p.everProbed = true
	p.reachable = false
	p.haveHealth = false
	p.healthDue = false
	p.mu.Unlock()

	if !wasReachable && !writeChanged {
		return
	}
	reason := p.prober.Reason(p.project)
	if reason == "" && err != nil {
		reason = err.Error()
	}
	p.hub.Publish(TypeProjectHealth, ProjectHealth{
		Project:     p.project,
		Reachable:   false,
		Reason:      reason,
		Health:      nil,
		Writable:    p.writable,
		WriteReason: p.writeWhy,
	})
}

// publishHealth recomputes and publishes the project's counts. When announce
// is set, slow counts still publish a reachable frame — cached counts or
// pending — because reachability or write state moved and clients must hear
// it whether or not the counts arrive.
func (p *poller) publishHealth(ctx context.Context, announce bool) {
	health, err := p.prober.Health(ctx, p.project)
	if errors.Is(err, registry.ErrQueryTimeout) {
		// Slow counts on a live server: keep whatever counts are cached and
		// try again next tick, without telling clients the project is down.
		p.setHealthDue()
		if announce {
			p.publishUp()
		}
		return
	}
	if err != nil {
		p.onUnreachable(err, false)
		return
	}

	p.mu.Lock()
	p.lastHealth = health
	p.haveHealth = true
	p.healthDue = false
	p.mu.Unlock()

	p.hub.Publish(TypeProjectHealth, ProjectHealth{
		Project:     p.project,
		Reachable:   true,
		Health:      health,
		Writable:    p.writable,
		WriteReason: p.writeWhy,
	})
}

// isReachable reports the server's reachability, for readers outside the
// poller goroutine.
func (p *poller) isReachable() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reachable
}

// probed reports whether this poller has completed at least one probe.
func (p *poller) probed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.everProbed
}

// cachedHealth returns the last computed counts, if any.
func (p *poller) cachedHealth() (queries.Health, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastHealth, p.haveHealth
}

// interval is how long to wait before the next probe.
func (p *poller) interval() time.Duration {
	if p.reachable {
		return p.liveInterval
	}
	return p.downInterval
}

// run polls until ctx is cancelled. It probes immediately, then waits the
// interval appropriate to the current state.
func (p *poller) run(ctx context.Context) {
	p.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.clock.After(p.interval()):
		}
		if ctx.Err() != nil {
			return
		}
		p.Tick(ctx)
	}
}
