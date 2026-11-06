package sse

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest/fakedolt"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/readcache"
	"github.com/kurofune/beads-loom/internal/registry"
)

// RegistryProber.Health on a live server whose cold counts outlast the
// caller's deadline reports a query timeout, which the poller treats as slow
// counts, not the outage a bare context.DeadlineExceeded would publish.
//
// The counts are made slow by holding every query connection, so the stats
// compute waits for one. The hash is stubbed: it is not what this checks.
func TestRegistryProberHealthWaiterDeadlineIsAQueryTimeout(t *testing.T) {
	port := fakedolt.Start(t, "loom")
	reg := registry.New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "a", Workdir: fakedolt.Workspace(t, port, "loom")}},
	})
	t.Cleanup(func() { reg.Close() })
	if err := reg.RefreshProject(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	err := reg.Do(context.Background(), "a", func(ctx context.Context, db *sql.DB) error {
		for i := 0; i < db.Stats().MaxOpenConnections; i++ {
			conn, err := db.Conn(ctx)
			if err != nil {
				return err
			}
			t.Cleanup(func() { conn.Close() })
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	prober := NewRegistryProber(reg)
	prober.cache = readcache.New(func(context.Context, string) (string, error) { return "h1", nil })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = prober.Health(ctx, "a")
	if !errors.Is(err, registry.ErrQueryTimeout) || errors.Is(err, registry.ErrUnreachable) {
		t.Fatalf("Health error = %v, want registry.ErrQueryTimeout only", err)
	}

	hub := NewHub()
	t.Cleanup(hub.Close)
	ch, unsubscribe := hub.Subscribe()
	t.Cleanup(unsubscribe)
	p := &poller{project: "a", prober: failingHealth{prober, err}, hub: hub, started: true, reachable: true}
	p.publishHealth(context.Background(), false)
	for _, ev := range drain(ch) {
		if h, ok := ev.Data.(ProjectHealth); ok && !h.Reachable {
			t.Fatalf("published an outage for counts still computing: %+v", h)
		}
	}
	if got, _ := reg.Get("a"); !got.Reachable {
		t.Errorf("project went unreachable over slow counts: %q", got.Reason)
	}
}

// failingHealth replays one Health error through the real prober's other
// methods.
type failingHealth struct {
	*RegistryProber
	err error
}

func (f failingHealth) Health(context.Context, string) (queries.Health, error) {
	return queries.Health{}, f.err
}
