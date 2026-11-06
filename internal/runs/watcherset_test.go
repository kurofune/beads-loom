package runs

import (
	"context"
	"testing"
	"time"
)

func TestWatcherSetDynamicLifecycle(t *testing.T) {
	hub := &recordingHub{}
	mgr := newTestManager(t, hub)
	history := NewHistory(mgr.Store(), mgr)
	ws := NewWatcherSet(mgr, history, hub)

	withoutDjinn := Project{Name: "plain", Workdir: t.TempDir()}
	ws.Add(withoutDjinn)
	if len(ws.watchers) != 0 {
		t.Fatal("project without .djinn was added")
	}

	before := djinnProject(t, "before")
	ws.Add(before)
	if len(ws.watchers) != 1 {
		t.Fatalf("watchers before Run = %d, want 1", len(ws.watchers))
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ws.Run(ctx)
		close(done)
	}()
	beforeEntry := waitForWatcherStart(t, ws, "before")

	during := djinnProject(t, "during")
	ws.Add(during)
	duringEntry := waitForWatcherStart(t, ws, "during")
	ws.Add(during)
	if len(ws.watchers) != 2 {
		t.Fatalf("duplicate Add changed membership: %d", len(ws.watchers))
	}

	ws.Remove("before")
	waitForWatcherStop(t, beforeEntry)
	ws.Remove("before")

	ws.Remove("during")
	waitForWatcherStop(t, duringEntry)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatcherSet.Run did not return after all watchers exited")
	}
}

func waitForWatcherStart(t *testing.T, ws *WatcherSet, name string) *watcherEntry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ws.mu.Lock()
		entry := ws.watchers[name]
		started := entry != nil && entry.cancel != nil && entry.done != nil
		ws.mu.Unlock()
		if started {
			return entry
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("watcher %q did not start", name)
	return nil
}

func waitForWatcherStop(t *testing.T, entry *watcherEntry) {
	t.Helper()
	select {
	case <-entry.done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop after Remove")
	}
}
