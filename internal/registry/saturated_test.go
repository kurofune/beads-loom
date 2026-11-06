package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest/fakedolt"
)

// saturatedLiveProject registers project "a" against a live server, refreshes
// it reachable, then holds every connection its query pool allows, the state
// eight slow reads leave behind.
func saturatedLiveProject(t *testing.T) *Registry {
	t.Helper()
	port := fakedolt.Start(t, "loom")
	r := New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "a", Workdir: fakedolt.Workspace(t, port, "loom")}},
	})
	t.Cleanup(func() { r.Close() })
	if err := r.RefreshProject(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get("a"); !got.Reachable {
		t.Fatalf("live server refreshed unreachable: %q", got.Reason)
	}

	err := r.Do(context.Background(), "a", func(ctx context.Context, db *sql.DB) error {
		max := db.Stats().MaxOpenConnections
		for i := 0; i < max; i++ {
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
	return r
}

// A timed-out query on a live server whose pool is fully occupied is a slow
// query. A ping through that pool waits for a connection that never frees, so
// the liveness check must not go through it.
func TestDoTimeoutWithSaturatedPoolLeavesLiveServerReachable(t *testing.T) {
	r := saturatedLiveProject(t)

	start := time.Now()
	err := r.Do(context.Background(), "a", func(context.Context, *sql.DB) error {
		return fmt.Errorf("stats ready: %w", context.DeadlineExceeded)
	})
	if !errors.Is(err, ErrQueryTimeout) || errors.Is(err, ErrUnreachable) {
		t.Fatalf("Do error = %v, want ErrQueryTimeout only", err)
	}
	if got, _ := r.Get("a"); !got.Reachable || got.Reason != "" {
		t.Fatalf("project = reachable %v reason %q; a full pool is not a dead server", got.Reachable, got.Reason)
	}
	if elapsed := time.Since(start); elapsed >= dialTimeout {
		t.Errorf("classification took %s; it waited on the full pool", elapsed)
	}
}

// A refresh while every query connection is held still finds the server up.
func TestRefreshWithSaturatedPoolLeavesLiveServerReachable(t *testing.T) {
	r := saturatedLiveProject(t)

	if err := r.RefreshProject(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get("a"); !got.Reachable || got.Reason != "" {
		t.Fatalf("project = reachable %v reason %q; a full pool is not a dead server", got.Reachable, got.Reason)
	}
}
