//go:build unix

package sse

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest/embeddedbd"
	"github.com/kurofune/beads-loom/internal/registry"
)

func TestMain(m *testing.M) { os.Exit(embeddedbd.Main(m)) }

// waitFor returns the first event of typ for project within d, or false.
func waitFor(ch <-chan Event, typ, project string, d time.Duration) (Event, bool) {
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return Event{}, false
			}
			if ev.Type != typ {
				continue
			}
			if inv, ok := ev.Data.(DBInvalidated); ok && inv.Project == project {
				return ev, true
			}
		case <-deadline:
			return Event{}, false
		}
	}
}

// An external `bd create` in an embedded workspace reaches SSE clients as a
// db-invalidated event within 3s, at the production 1s poll cadence: the
// manifest stat sees the write and dolt_hashof_db() confirms it.
func TestEmbeddedExternalWriteEmitsChange(t *testing.T) {
	w := embeddedbd.Clone(t)
	reg := registry.New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: w.Workdir}}})
	t.Cleanup(func() { _ = reg.Close() })
	reg.Refresh(context.Background())
	if p, _ := reg.Get("fx"); !p.Reachable || !p.Embedded {
		t.Fatalf("fixture project = %+v; want reachable embedded", p)
	}

	hub := NewHub()
	t.Cleanup(hub.Close)
	ch, cancelSub := hub.Subscribe()
	t.Cleanup(cancelSub)

	prober := NewRegistryProber(reg)
	mon := NewMonitor(hub, []string{"fx"}, MonitorOptions{Prober: prober})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { mon.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	if _, ok := waitFor(ch, TypeDBInvalidated, "fx", 10*time.Second); !ok {
		t.Fatal("no initial db-invalidated after the first probe")
	}

	if ev, ok := waitFor(ch, TypeDBInvalidated, "fx", 2500*time.Millisecond); ok {
		t.Fatalf("db-invalidated %+v with no write", ev)
	}

	w.Create(t, "written outside loom")
	wrote := time.Now()
	if _, ok := waitFor(ch, TypeDBInvalidated, "fx", 3*time.Second); !ok {
		t.Fatalf("no db-invalidated within 3s of an external bd create (waited %s)", time.Since(wrote).Round(time.Millisecond))
	}
}

// Ticks over an unchanged manifest do not open the store: the cached hash is
// returned, so an idle embedded project costs a stat per second.
func TestEmbeddedIdleTicksSkipTheStore(t *testing.T) {
	w := embeddedbd.Clone(t)
	reg := registry.New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: w.Workdir}}})
	t.Cleanup(func() { _ = reg.Close() })
	reg.Refresh(context.Background())

	prober := NewRegistryProber(reg)
	first, err := prober.Hash(context.Background(), "fx")
	if err != nil {
		t.Fatal(err)
	}

	held := holdExclusive(t, filepath.Join(filepath.Dir(w.ManifestPath()), "LOCK"))
	start := time.Now()
	for i := 0; i < 10; i++ {
		got, err := prober.Hash(context.Background(), "fx")
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("tick %d: hash %q, want cached %q", i, got, first)
		}
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("10 idle ticks took %s; they opened the store", elapsed)
	}
	held()
}

// holdExclusive takes bd's side of the store lock, so any open of the store
// during the hold waits out the shared-lock timeout and shows up as latency.
func holdExclusive(t *testing.T, path string) (release func()) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
