package discovery

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotDiscovered = errors.New("workdir is not a discovered project")
	ErrNotHidden     = errors.New("workdir is not hidden")
)

// HiddenEntry pairs a hidden workdir with the name it would carry if unhidden
// (from the latest scan; empty if the workdir no longer scans).
type HiddenEntry struct {
	Workdir string
	Name    string
}

// Status is a point-in-time snapshot for the HTTP layer.
type Status struct {
	Roots      []string
	Depth      int
	Exclude    []string
	Interval   time.Duration
	LastScanAt time.Time
	Discovered []Found
	Hidden     []HiddenEntry
}

type ManagerOptions struct {
	Scan          ScanOptions
	Interval      time.Duration
	Store         *Store
	TakenNames    map[string]bool
	TakenWorkdirs map[string]bool
	// OnChange receives a snapshot of the full active set after membership
	// changes. It runs outside the state lock and must not call the Manager.
	OnChange func(active []Found)
	Logger   *slog.Logger
	Now      func() time.Time
}

// Manager scans for projects and applies the persisted hidden set.
type Manager struct {
	scan          ScanOptions
	interval      time.Duration
	store         *Store
	takenNames    map[string]bool
	takenWorkdirs map[string]bool
	onChange      func([]Found)
	logger        *slog.Logger
	now           func() time.Time

	scanMu   sync.Mutex
	mutateMu sync.Mutex
	mu       sync.Mutex
	full     []Found
	lastScan time.Time
}

// NewManager constructs a discovery manager. A nil Store uses an in-memory
// hidden set, and nil callbacks and test seams receive safe defaults.
func NewManager(o ManagerOptions) *Manager {
	store := o.Store
	if store == nil {
		store = &Store{set: make(map[string]bool)}
	}
	interval := o.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		scan: ScanOptions{
			Roots:   append([]string(nil), o.Scan.Roots...),
			Depth:   o.Scan.Depth,
			Exclude: append([]string(nil), o.Scan.Exclude...),
		},
		interval:      interval,
		store:         store,
		takenNames:    cloneSet(o.TakenNames),
		takenWorkdirs: cloneSet(o.TakenWorkdirs),
		onChange:      o.OnChange,
		logger:        logger,
		now:           now,
	}
}

// Run scans immediately, then on every interval tick, until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	m.Rescan(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Rescan(ctx)
		}
	}
}

// Rescan performs one scan now and returns the names added to and removed from
// the active set. Scans and state notifications are serialized.
func (m *Manager) Rescan(ctx context.Context) (added, removed []string) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return []string{}, []string{}
		default:
		}
	}

	m.scanMu.Lock()
	defer m.scanMu.Unlock()
	found := Scan(m.scan, m.takenNames, m.takenWorkdirs)
	scannedAt := m.now()

	m.mutateMu.Lock()
	defer m.mutateMu.Unlock()
	m.mu.Lock()
	before := m.activeLocked()
	m.full = cloneFound(found)
	m.lastScan = scannedAt
	after := m.activeLocked()
	added, removed = foundDiff(before, after)
	changed := len(added) > 0 || len(removed) > 0
	m.mu.Unlock()

	if changed {
		m.notify(after)
		m.logger.Debug("discovery project set changed", "added", added, "removed", removed)
	}
	return added, removed
}

// Hide persists and removes a currently active discovered project.
func (m *Manager) Hide(workdir string) error {
	m.mutateMu.Lock()
	defer m.mutateMu.Unlock()
	m.mu.Lock()
	before := m.activeLocked()
	if !containsWorkdir(before, workdir) {
		m.mu.Unlock()
		return ErrNotDiscovered
	}
	if err := m.store.Hide(workdir); err != nil {
		m.mu.Unlock()
		return err
	}
	after := m.activeLocked()
	m.mu.Unlock()
	m.notify(after)
	return nil
}

// Unhide removes a workdir from the hidden set.
func (m *Manager) Unhide(workdir string) error {
	m.mutateMu.Lock()
	defer m.mutateMu.Unlock()
	m.mu.Lock()
	if !m.store.IsHidden(workdir) {
		m.mu.Unlock()
		return ErrNotHidden
	}
	before := m.activeLocked()
	if err := m.store.Unhide(workdir); err != nil {
		m.mu.Unlock()
		return err
	}
	after := m.activeLocked()
	m.mu.Unlock()
	if changedFound(before, after) {
		m.notify(after)
	}
	return nil
}

// Status returns a point-in-time copy of manager state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make(map[string]string, len(m.full))
	for _, found := range m.full {
		names[found.Workdir] = found.Name
	}
	hiddenWorkdirs := m.store.Hidden()
	hidden := make([]HiddenEntry, 0, len(hiddenWorkdirs))
	for _, workdir := range hiddenWorkdirs {
		hidden = append(hidden, HiddenEntry{Workdir: workdir, Name: names[workdir]})
	}
	return Status{
		Roots:      append([]string(nil), m.scan.Roots...),
		Depth:      m.scan.Depth,
		Exclude:    append([]string(nil), m.scan.Exclude...),
		Interval:   m.interval,
		LastScanAt: m.lastScan,
		Discovered: m.activeLocked(),
		Hidden:     hidden,
	}
}

func (m *Manager) activeLocked() []Found {
	hidden := make(map[string]bool)
	for _, workdir := range m.store.Hidden() {
		hidden[workdir] = true
	}
	active := make([]Found, 0, len(m.full))
	for _, found := range m.full {
		if !hidden[found.Workdir] {
			active = append(active, found)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].Name == active[j].Name {
			return active[i].Workdir < active[j].Workdir
		}
		return active[i].Name < active[j].Name
	})
	return active
}

func (m *Manager) notify(active []Found) {
	if m.onChange != nil {
		m.onChange(cloneFound(active))
	}
}

func cloneFound(found []Found) []Found {
	return append([]Found(nil), found...)
}

func containsWorkdir(found []Found, workdir string) bool {
	for _, project := range found {
		if project.Workdir == workdir {
			return true
		}
	}
	return false
}

func changedFound(before, after []Found) bool {
	added, removed := foundDiff(before, after)
	return len(added) > 0 || len(removed) > 0
}

func foundDiff(before, after []Found) (added, removed []string) {
	type identity struct {
		name    string
		workdir string
	}
	oldSet := make(map[identity]bool, len(before))
	newSet := make(map[identity]bool, len(after))
	for _, found := range before {
		oldSet[identity{name: found.Name, workdir: found.Workdir}] = true
	}
	for _, found := range after {
		key := identity{name: found.Name, workdir: found.Workdir}
		newSet[key] = true
		if !oldSet[key] {
			added = append(added, found.Name)
		}
	}
	for _, found := range before {
		if !newSet[identity{name: found.Name, workdir: found.Workdir}] {
			removed = append(removed, found.Name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
