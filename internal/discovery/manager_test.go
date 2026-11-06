package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestManagerLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	alpha := filepath.Join(root, "alpha")
	zeta := filepath.Join(root, "zeta")
	makeWorkspace(t, alpha)
	makeWorkspace(t, zeta)
	store, err := NewStore(filepath.Join(t.TempDir(), "hidden.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)

	var callbacksMu sync.Mutex
	var callbacks [][]Found
	manager := NewManager(ManagerOptions{
		Scan:     ScanOptions{Roots: []string{root}, Depth: 2, Exclude: []string{"ignored"}},
		Interval: 2 * time.Minute,
		Store:    store,
		Now:      func() time.Time { return now },
		OnChange: func(active []Found) {
			callbacksMu.Lock()
			defer callbacksMu.Unlock()
			callbacks = append(callbacks, cloneFound(active))
		},
	})

	operations := []struct {
		name        string
		run         func() (added, removed []string, err error)
		wantAdded   []string
		wantRemoved []string
		wantActive  []string
		wantCalls   int
	}{
		{
			name: "initial scan fires callback",
			run: func() ([]string, []string, error) {
				added, removed := manager.Rescan(context.Background())
				return added, removed, nil
			},
			wantAdded:  []string{"alpha", "zeta"},
			wantActive: []string{"alpha", "zeta"},
			wantCalls:  1,
		},
		{
			name: "unchanged scan does not fire callback",
			run: func() ([]string, []string, error) {
				added, removed := manager.Rescan(context.Background())
				return added, removed, nil
			},
			wantActive: []string{"alpha", "zeta"},
			wantCalls:  1,
		},
		{
			name: "hide removes active project and fires",
			run: func() ([]string, []string, error) {
				return nil, nil, manager.Hide(alpha)
			},
			wantActive: []string{"zeta"},
			wantCalls:  2,
		},
		{
			name: "unhide restores active project and fires",
			run: func() ([]string, []string, error) {
				return nil, nil, manager.Unhide(alpha)
			},
			wantActive: []string{"alpha", "zeta"},
			wantCalls:  3,
		},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			added, removed, err := operation.run()
			if err != nil {
				t.Fatalf("operation: %v", err)
			}
			if !reflect.DeepEqual(added, operation.wantAdded) {
				t.Errorf("added = %v, want %v", added, operation.wantAdded)
			}
			if !reflect.DeepEqual(removed, operation.wantRemoved) {
				t.Errorf("removed = %v, want %v", removed, operation.wantRemoved)
			}
			status := manager.Status()
			if got := foundNames(status.Discovered); !reflect.DeepEqual(got, operation.wantActive) {
				t.Errorf("active = %v, want %v", got, operation.wantActive)
			}
			callbacksMu.Lock()
			gotCalls := len(callbacks)
			callbacksMu.Unlock()
			if gotCalls != operation.wantCalls {
				t.Errorf("callback count = %d, want %d", gotCalls, operation.wantCalls)
			}
		})
	}

	status := manager.Status()
	if !reflect.DeepEqual(status.Roots, []string{root}) || status.Depth != 2 ||
		!reflect.DeepEqual(status.Exclude, []string{"ignored"}) || status.Interval != 2*time.Minute {
		t.Errorf("Status scan configuration = %+v", status)
	}
	if !status.LastScanAt.Equal(now) {
		t.Errorf("LastScanAt = %s, want %s", status.LastScanAt, now)
	}
	if len(status.Hidden) != 0 {
		t.Errorf("Hidden = %v, want empty", status.Hidden)
	}
	if err := manager.Hide(filepath.Join(root, "missing")); !errors.Is(err, ErrNotDiscovered) {
		t.Errorf("Hide missing error = %v, want ErrNotDiscovered", err)
	}
	if err := manager.Unhide(filepath.Join(root, "missing")); !errors.Is(err, ErrNotHidden) {
		t.Errorf("Unhide missing error = %v, want ErrNotHidden", err)
	}
}

func TestManagerRescanReportsAddedAndRemoved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	makeWorkspace(t, alpha)
	manager := NewManager(ManagerOptions{Scan: ScanOptions{Roots: []string{root}, Depth: 1}})
	manager.Rescan(context.Background())

	makeWorkspace(t, beta)
	added, removed := manager.Rescan(context.Background())
	if !reflect.DeepEqual(added, []string{"beta"}) || len(removed) != 0 {
		t.Errorf("after add: added=%v removed=%v", added, removed)
	}
	if err := os.Remove(filepath.Join(alpha, ".beads", "metadata.json")); err != nil {
		t.Fatal(err)
	}
	added, removed = manager.Rescan(context.Background())
	if len(added) != 0 || !reflect.DeepEqual(removed, []string{"alpha"}) {
		t.Errorf("after remove: added=%v removed=%v", added, removed)
	}
}

func TestManagerStatusIncludesHiddenAndStaleHidden(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	project := filepath.Join(root, "project")
	makeWorkspace(t, project)
	store, err := NewStore(filepath.Join(t.TempDir(), "hidden.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerOptions{Scan: ScanOptions{Roots: []string{root}, Depth: 1}, Store: store})
	manager.Rescan(context.Background())
	if err := manager.Hide(project); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if want := []HiddenEntry{{Workdir: project, Name: "project"}}; !reflect.DeepEqual(status.Hidden, want) {
		t.Errorf("Hidden = %#v, want %#v", status.Hidden, want)
	}
	if len(status.Discovered) != 0 {
		t.Errorf("Discovered = %v, want empty", status.Discovered)
	}

	if err := os.Remove(filepath.Join(project, ".beads", "metadata.json")); err != nil {
		t.Fatal(err)
	}
	manager.Rescan(context.Background())
	status = manager.Status()
	if want := []HiddenEntry{{Workdir: project, Name: ""}}; !reflect.DeepEqual(status.Hidden, want) {
		t.Errorf("stale Hidden = %#v, want %#v", status.Hidden, want)
	}
}

func TestManagerRunScansImmediately(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	makeWorkspace(t, filepath.Join(root, "project"))
	changed := make(chan []Found, 1)
	manager := NewManager(ManagerOptions{
		Scan:     ScanOptions{Roots: []string{root}, Depth: 1},
		Interval: time.Hour,
		OnChange: func(active []Found) { changed <- active },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()

	select {
	case active := <-changed:
		if got := foundNames(active); !reflect.DeepEqual(got, []string{"project"}) {
			t.Errorf("initial callback = %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not scan immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after cancellation")
	}
}

func TestManagerConcurrentAccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	project := filepath.Join(root, "project")
	makeWorkspace(t, project)
	var callbackMu sync.Mutex
	manager := NewManager(ManagerOptions{
		Scan: ScanOptions{Roots: []string{root}, Depth: 1},
		OnChange: func(active []Found) {
			callbackMu.Lock()
			_ = len(active)
			callbackMu.Unlock()
		},
	})
	manager.Rescan(context.Background())

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 40; iteration++ {
				switch (worker + iteration) % 4 {
				case 0:
					manager.Rescan(context.Background())
				case 1:
					_ = manager.Status()
				case 2:
					_ = manager.Hide(project)
				case 3:
					_ = manager.Unhide(project)
				}
			}
		}(worker)
	}
	wg.Wait()
}

func foundNames(found []Found) []string {
	names := make([]string, len(found))
	for i, project := range found {
		names[i] = project.Name
	}
	return names
}
