package runs

import (
	"context"
	"sort"
	"sync"
)

// WatcherSet manages one Watcher per djinn project, with dynamic membership.
type WatcherSet struct {
	mgr     *Manager
	history *History
	hub     Publisher

	mu       sync.Mutex
	watchers map[string]*watcherEntry
	running  bool
	runCtx   context.Context
	wg       sync.WaitGroup
}

type watcherEntry struct {
	watcher *Watcher
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewWatcherSet returns an empty dynamic watcher set.
func NewWatcherSet(mgr *Manager, history *History, hub Publisher) *WatcherSet {
	return &WatcherSet{
		mgr:      mgr,
		history:  history,
		hub:      hub,
		watchers: make(map[string]*watcherEntry),
	}
}

// Run blocks until ctx is cancelled and every watcher has exited.
func (ws *WatcherSet) Run(ctx context.Context) {
	ws.mu.Lock()
	if ws.running {
		ws.mu.Unlock()
		<-ctx.Done()
		return
	}
	ws.running = true
	ws.runCtx = ctx
	for name, entry := range ws.watchers {
		ws.startLocked(name, entry)
	}
	ws.mu.Unlock()

	<-ctx.Done()
	ws.mu.Lock()
	ws.running = false
	ws.runCtx = nil
	ws.mu.Unlock()
	ws.wg.Wait()
}

// Add starts a watcher for p if p.IsDjinnProject() and none is running. No-op otherwise.
func (ws *WatcherSet) Add(p Project) {
	if !p.IsDjinnProject() {
		return
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if _, ok := ws.watchers[p.Name]; ok {
		return
	}
	entry := &watcherEntry{watcher: NewWatcher(p, ws.mgr, ws.history, ws.hub)}
	ws.watchers[p.Name] = entry
	if ws.running {
		ws.startLocked(p.Name, entry)
	}
}

// Remove stops the named project's watcher. No-op if absent.
func (ws *WatcherSet) Remove(name string) {
	ws.mu.Lock()
	entry, ok := ws.watchers[name]
	if !ok {
		ws.mu.Unlock()
		return
	}
	delete(ws.watchers, name)
	cancel := entry.cancel
	ws.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (ws *WatcherSet) startLocked(name string, entry *watcherEntry) {
	if entry.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ws.runCtx)
	entry.cancel = cancel
	entry.done = make(chan struct{})
	ws.wg.Add(1)
	go func() {
		defer ws.wg.Done()
		defer close(entry.done)
		defer func() {
			ws.mu.Lock()
			if current, ok := ws.watchers[name]; ok && current == entry {
				entry.cancel = nil
			}
			ws.mu.Unlock()
		}()
		entry.watcher.Run(ctx)
	}()
}

// Names returns the projects being watched, sorted.
func (ws *WatcherSet) Names() []string {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	out := make([]string, 0, len(ws.watchers))
	for name := range ws.watchers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
