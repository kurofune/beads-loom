// Package readcache keeps the results of Loom's heavy reads, keyed by the
// project's dolt_hashof_db().
//
// The hash moves on every data change, committed or not, so a result computed
// under a hash is exact for as long as the hash holds. A request reads the
// hash (one cheap statement, or a manifest stat for an embedded store) and
// gets the stored result when it matches. On a backlog of thousands of issues
// that turns a multi-second read into a millisecond one.
//
// Some results also depend on the clock: a defer date passes, the trend's day
// turns over, an issue crosses the staleness line. Those can change while the
// hash does not, so an entry older than TTL is still served at once and a
// refresh runs behind it.
package readcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kurofune/beads-loom/internal/registry"
)

// TTL is how long a result may be served under an unchanged hash before a
// background refresh replaces it.
const TTL = time.Minute

// maxEntries bounds memory. Filters make /issues keys open-ended, and one
// project's entries from an older hash are dropped as soon as a newer one is
// stored, so the bound only bites on a large spread of distinct filters.
const maxEntries = 512

// StatsEndpoint is the key every reader of a project's stats shares, so the
// SSE poller and GET /stats fill and serve one entry.
const StatsEndpoint = "stats"

// HashFunc returns the project's current dolt_hashof_db().
type HashFunc func(ctx context.Context, project string) (string, error)

// Cache holds results per (project, endpoint, params) under the hash they were
// computed for. A nil *Cache caches nothing.
type Cache struct {
	hash HashFunc
	now  func() time.Time

	mu      sync.Mutex
	entries map[key]*entry
	flights map[flightKey]*flight
}

type key struct{ project, endpoint, params string }

type flightKey struct {
	key
	hash string
}

type entry struct {
	hash       string
	value      any
	at         time.Time
	refreshing bool
}

// flight is one compute in progress; callers asking for the same key and hash
// wait for it instead of starting their own.
type flight struct {
	done  chan struct{}
	value any
	err   error
}

// New returns a cache that checks freshness with hash.
func New(hash HashFunc) *Cache {
	return &Cache{
		hash:    hash,
		now:     time.Now,
		entries: map[key]*entry{},
		flights: map[flightKey]*flight{},
	}
}

// Load returns the result for (project, endpoint, params) under the project's
// current hash, running compute only when no result for that hash is stored.
// params must capture every input that changes the result. A failed compute is
// returned and not stored.
//
// compute runs detached from the caller's cancellation: a result someone else
// is waiting on, or that the next request will want, is worth finishing when
// the first caller hangs up. Its own query deadline still bounds it.
//
// A caller whose deadline passes while it waits gets an error wrapping
// registry.ErrQueryTimeout, the same as a query that ran out of time on a live
// server. The compute starts its own deadline after the caller's, so it
// classifies its outcome later than the caller gives up; and the hash read
// that opened this call has just shown the server answers.
func Load[T any](ctx context.Context, c *Cache, project, endpoint, params string, compute func(context.Context) (T, error)) (T, error) {
	var zero T
	if c == nil {
		return compute(ctx)
	}
	hash, err := c.hash(ctx, project)
	if err != nil {
		return zero, err
	}
	k := key{project, endpoint, params}
	run := func(ctx context.Context) (any, error) { return compute(ctx) }

	c.mu.Lock()
	if e, ok := c.entries[k]; ok && e.hash == hash {
		if c.now().Sub(e.at) >= TTL && !e.refreshing {
			e.refreshing = true
			go c.refresh(context.WithoutCancel(ctx), k, hash, run)
		}
		v := e.value
		c.mu.Unlock()
		return v.(T), nil
	}
	f := c.flightLocked(context.WithoutCancel(ctx), k, hash, run)
	c.mu.Unlock()

	select {
	case <-f.done:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, fmt.Errorf("%w: %s: %s still computing: %w", registry.ErrQueryTimeout, project, endpoint, ctx.Err())
		}
		return zero, ctx.Err()
	}
	if f.err != nil {
		return zero, f.err
	}
	return f.value.(T), nil
}

// flightLocked joins the compute for (k, hash), starting it if none runs.
// c.mu must be held.
func (c *Cache) flightLocked(ctx context.Context, k key, hash string, run func(context.Context) (any, error)) *flight {
	fk := flightKey{k, hash}
	if f, ok := c.flights[fk]; ok {
		return f
	}
	f := &flight{done: make(chan struct{})}
	c.flights[fk] = f
	go func() {
		f.value, f.err = run(ctx)
		c.mu.Lock()
		delete(c.flights, fk)
		if f.err == nil {
			c.storeLocked(k, hash, f.value)
		}
		c.mu.Unlock()
		close(f.done)
	}()
	return f
}

// refresh recomputes an expired entry. On failure the old value stays and the
// next request past the TTL tries again.
func (c *Cache) refresh(ctx context.Context, k key, hash string, run func(context.Context) (any, error)) {
	c.mu.Lock()
	f := c.flightLocked(ctx, k, hash, run)
	c.mu.Unlock()
	<-f.done

	if f.err != nil {
		c.mu.Lock()
		if e, ok := c.entries[k]; ok && e.hash == hash {
			e.refreshing = false
		}
		c.mu.Unlock()
	}
}

// storeLocked records value for k under hash, dropping the project's entries
// from any other hash (they can never be served again) and the oldest entry
// when the cache is full. c.mu must be held.
func (c *Cache) storeLocked(k key, hash string, value any) {
	for other, e := range c.entries {
		if other.project == k.project && e.hash != hash {
			delete(c.entries, other)
		}
	}
	if _, exists := c.entries[k]; !exists && len(c.entries) >= maxEntries {
		var oldest key
		var oldestAt time.Time
		first := true
		for other, e := range c.entries {
			if first || e.at.Before(oldestAt) {
				oldest, oldestAt, first = other, e.at, false
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[k] = &entry{hash: hash, value: value, at: c.now()}
}

func (c *Cache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
