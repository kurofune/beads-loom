package sse

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"time"

	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/readcache"
	"github.com/kurofune/beads-loom/internal/registry"
)

// probeTimeout bounds a single hash probe. It is deliberately shorter than
// LiveInterval's multiple so a wedged server cannot pile up goroutines.
const probeTimeout = 5 * time.Second

// healthTimeout bounds a health recompute. Counts on a backlog of thousands
// take seconds cold; a bound under that would time out every cold count and
// retry it each tick, so it gets the full query budget.
const healthTimeout = registry.QueryTimeout

// Monitor owns one poller goroutine per project and caches the health each
// poller computes.
//
// It replaces phase 1's blanket registry refresh loop: counts are recomputed
// only when a project's dolt hash moves or its reachability flips, rather than
// on a timer, so /api/projects is served from cache in the steady state.
type Monitor struct {
	hub  *Hub
	opts MonitorOptions

	mu      sync.RWMutex
	pollers map[string]*poller
	cancels map[string]context.CancelFunc
	order   []string
	running bool
	runCtx  context.Context
	wg      sync.WaitGroup
}

// MonitorOptions configures a Monitor.
type MonitorOptions struct {
	// Prober supplies hashes and health. Defaults to a registry-backed prober.
	Prober Prober
	// Clock defaults to SystemClock.
	Clock Clock
	// LiveInterval defaults to LiveInterval.
	LiveInterval time.Duration
	// DownInterval defaults to DownInterval.
	DownInterval time.Duration
}

// NewMonitor builds a Monitor for the given project names.
func NewMonitor(hub *Hub, projects []string, opts MonitorOptions) *Monitor {
	if opts.Clock == nil {
		opts.Clock = SystemClock
	}
	if opts.LiveInterval <= 0 {
		opts.LiveInterval = LiveInterval
	}
	if opts.DownInterval <= 0 {
		opts.DownInterval = DownInterval
	}

	m := &Monitor{
		hub:     hub,
		opts:    opts,
		pollers: make(map[string]*poller, len(projects)),
		cancels: make(map[string]context.CancelFunc, len(projects)),
	}
	for _, name := range projects {
		m.Add(name)
	}
	return m
}

// Add starts watching a project. If Run is already active the poller starts
// immediately; otherwise it starts when Run is called. No-op if present.
func (m *Monitor) Add(project string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pollers[project]; ok {
		return
	}
	p := &poller{
		project:      project,
		prober:       m.opts.Prober,
		hub:          m.hub,
		clock:        m.opts.Clock,
		liveInterval: m.opts.LiveInterval,
		downInterval: m.opts.DownInterval,
	}
	m.pollers[project] = p
	m.order = append(m.order, project)
	if m.running {
		m.startLocked(project, p)
	}
}

// Remove stops a project's poller and drops its cached health. No-op if absent.
func (m *Monitor) Remove(project string) {
	m.mu.Lock()
	_, ok := m.pollers[project]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.pollers, project)
	for i, name := range m.order {
		if name == project {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	cancel := m.cancels[project]
	delete(m.cancels, project)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Run starts every poller and blocks until ctx is cancelled and all of them
// have exited, so the caller can be sure no goroutines outlive shutdown.
func (m *Monitor) Run(ctx context.Context) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		<-ctx.Done()
		return
	}
	m.runCtx = ctx
	m.running = true
	for _, name := range m.order {
		m.startLocked(name, m.pollers[name])
	}
	m.mu.Unlock()

	<-ctx.Done()
	m.mu.Lock()
	m.running = false
	m.runCtx = nil
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Monitor) startLocked(project string, p *poller) {
	if m.cancels[project] != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.runCtx)
	m.cancels[project] = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			if current, ok := m.pollers[project]; ok && current == p {
				delete(m.cancels, project)
			}
			m.mu.Unlock()
		}()
		p.run(ctx)
	}()
}

// Health returns the cached counts for a project, and whether any are known.
// A cache miss means "no poller result yet" and callers should fall back to
// computing the counts themselves.
func (m *Monitor) Health(project string) (queries.Health, bool) {
	m.mu.RLock()
	p, ok := m.pollers[project]
	m.mu.RUnlock()
	if !ok {
		return queries.Health{}, false
	}
	return p.cachedHealth()
}

// Snapshot returns the current health of every project. It is sent to a new SSE
// subscriber so a reconnecting UI resyncs without needing event replay.
//
// A project whose first probe has not completed is reported as pending rather
// than unreachable: "no cached health yet" and "the server is down" are
// different states, and conflating them made a healthy project flash as down
// during startup (review finding N1).
func (m *Monitor) Snapshot() []ProjectHealth {
	m.mu.RLock()
	type snapshotEntry struct {
		name string
		p    *poller
	}
	entries := make([]snapshotEntry, 0, len(m.order))
	for _, name := range m.order {
		entries = append(entries, snapshotEntry{name: name, p: m.pollers[name]})
	}
	m.mu.RUnlock()

	out := make([]ProjectHealth, 0, len(entries))
	for _, entry := range entries {
		name, p := entry.name, entry.p
		health, haveHealth := p.cachedHealth()

		item := ProjectHealth{Project: name}
		item.Writable, item.WriteReason = p.prober.Writable(name)
		switch {
		case haveHealth:
			item.Reachable = true
			item.Health = health
		case !p.probed(), p.isReachable():
			// Not checked yet, or up with counts still being read or too slow
			// to arrive. Reachability never waits on the counts.
			item.Reachable = true
			item.Pending = true
		default:
			item.Reachable = false
			item.Reason = p.prober.Reason(name)
		}
		out = append(out, item)
	}
	return out
}

// --- registry-backed prober ------------------------------------------------

// RegistryProber implements Prober against the live registry.
type RegistryProber struct {
	reg *registry.Registry
	// cache holds results keyed by the hash this prober reads. Health fills
	// its stats entry, which GET /stats then serves.
	cache *readcache.Cache

	mu sync.Mutex
	// manifests caches, per embedded project, the hash read the last time its
	// manifest had a given size and mtime.
	manifests map[string]manifestHash
}

// manifestHash is one embedded project's last confirmed hash and the manifest
// stat it was read under.
type manifestHash struct {
	size    int64
	modTime time.Time
	hash    string
}

// NewRegistryProber returns a Prober backed by reg.
func NewRegistryProber(reg *registry.Registry) *RegistryProber {
	p := &RegistryProber{reg: reg, manifests: map[string]manifestHash{}}
	p.cache = readcache.New(p.Hash)
	return p
}

// Cache is the result cache keyed by this prober's hash reads.
func (p *RegistryProber) Cache() *readcache.Cache { return p.cache }

// Hash reads dolt_hashof_db(), which changes on any data change including
// uncommitted working-set edits — verified against bd 1.3.2, where each bd
// write moves it on a server and in an embedded store.
//
// An embedded project is opened only when its manifest moved. bd rewrites
// .dolt/noms/manifest on every write, so an unchanged size and mtime return
// the last hash without opening the store; a changed one is confirmed by
// dolt_hashof_db(). The stat is taken before the query, so a write landing
// between the two moves the manifest again and is read on the next tick.
func (p *RegistryProber) Hash(ctx context.Context, project string) (string, error) {
	manifest := ""
	if proj, err := p.reg.Get(project); err == nil && proj.Reachable {
		manifest = proj.EmbeddedManifest()
	}
	var st os.FileInfo
	if manifest != "" {
		st, _ = os.Stat(manifest)
	}
	if st == nil {
		p.forget(project)
		return p.queryHash(ctx, project)
	}

	p.mu.Lock()
	cached, ok := p.manifests[project]
	p.mu.Unlock()
	if ok && cached.size == st.Size() && cached.modTime.Equal(st.ModTime()) {
		return cached.hash, nil
	}

	hash, err := p.queryHash(ctx, project)
	if err != nil {
		p.forget(project)
		return "", err
	}
	p.mu.Lock()
	p.manifests[project] = manifestHash{size: st.Size(), modTime: st.ModTime(), hash: hash}
	p.mu.Unlock()
	return hash, nil
}

func (p *RegistryProber) forget(project string) {
	p.mu.Lock()
	delete(p.manifests, project)
	p.mu.Unlock()
}

func (p *RegistryProber) queryHash(ctx context.Context, project string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var hash string
	err := p.reg.Do(ctx, project, func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT dolt_hashof_db()").Scan(&hash)
	})
	if err != nil {
		return "", err
	}
	return hash, nil
}

// Health returns the project's issue counts, from the shared stats entry when
// the hash has not moved since it was computed.
func (p *RegistryProber) Health(ctx context.Context, project string) (queries.Health, error) {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	stats, err := readcache.Load(ctx, p.cache, project, readcache.StatsEndpoint, "",
		func(ctx context.Context) (*queries.Stats, error) {
			var stats *queries.Stats
			err := p.reg.Do(ctx, project, func(ctx context.Context, db *sql.DB) error {
				var err error
				stats, err = queries.StatsFor(ctx, db)
				return err
			})
			return stats, err
		})
	if err != nil {
		return queries.Health{}, err
	}
	return stats.Health(), nil
}

// Generation returns the registry's write generation for the project.
func (p *RegistryProber) Generation(project string) uint64 {
	gen, _ := p.reg.Generation(project)
	return gen
}

// Writable returns the registry's write state. An unknown project counts as
// writable: the write routes answer for it on their own.
func (p *RegistryProber) Writable(project string) (bool, string) {
	proj, err := p.reg.Get(project)
	if err != nil {
		return true, ""
	}
	return proj.Writable, proj.WriteReason
}

// Reason returns the registry's current unreachability reason.
func (p *RegistryProber) Reason(project string) string {
	proj, err := p.reg.Get(project)
	if err != nil {
		return err.Error()
	}
	return proj.Reason
}
