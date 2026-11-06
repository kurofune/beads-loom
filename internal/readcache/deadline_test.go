package readcache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/registry"
)

// A caller whose deadline passes while the compute still runs gets a query
// timeout, the error every reader maps to "slow, not down". A bare
// context.DeadlineExceeded reads as an outage to the SSE poller and as a 500
// to the HTTP layer. The compute goes on and the next caller gets its result.
func TestLoadWaiterDeadlineIsAQueryTimeout(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	release := make(chan struct{})
	slow := func(context.Context) (string, error) {
		<-release
		return "v", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := Load(ctx, c, "p", "stats", "", slow)
	if !errors.Is(err, registry.ErrQueryTimeout) {
		t.Fatalf("Load error = %v, want registry.ErrQueryTimeout", err)
	}
	if errors.Is(err, registry.ErrUnreachable) {
		t.Fatalf("Load error = %v; a slow compute is not an outage", err)
	}

	close(release)
	got, err := Load(context.Background(), c, "p", "stats", "", slow)
	if err != nil || got != "v" {
		t.Fatalf("Load after the compute finished = %q, %v; want the detached result", got, err)
	}
}

// A caller that hangs up is not a timeout: it gets its own cancellation back.
func TestLoadWaiterCancelIsNotAQueryTimeout(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	release := make(chan struct{})
	defer close(release)
	slow := func(context.Context) (string, error) {
		<-release
		return "v", nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Load(ctx, c, "p", "stats", "", slow)
	if !errors.Is(err, context.Canceled) || errors.Is(err, registry.ErrQueryTimeout) {
		t.Fatalf("Load error = %v, want context.Canceled and not a query timeout", err)
	}
}
