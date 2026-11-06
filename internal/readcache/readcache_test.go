package readcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hashes is a scriptable HashFunc: one current hash per project.
type hashes struct {
	mu    sync.Mutex
	by    map[string]string
	err   error
	calls atomic.Int32
}

func (h *hashes) set(project, hash string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.by == nil {
		h.by = map[string]string{}
	}
	h.by[project] = hash
}

func (h *hashes) fn(_ context.Context, project string) (string, error) {
	h.calls.Add(1)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.by[project], h.err
}

type counter struct{ n atomic.Int32 }

func (c *counter) compute(v string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		c.n.Add(1)
		return v, nil
	}
}

func TestLoadServesTheSameHashFromCache(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	var calls counter

	for i := 0; i < 3; i++ {
		got, err := Load(context.Background(), c, "p", "stats", "", calls.compute("v1"))
		if err != nil || got != "v1" {
			t.Fatalf("Load = %q, %v", got, err)
		}
	}
	if calls.n.Load() != 1 {
		t.Errorf("computed %d times for one hash, want 1", calls.n.Load())
	}
	if h.calls.Load() != 3 {
		t.Errorf("hash read %d times, want once per Load", h.calls.Load())
	}
}

func TestLoadRecomputesWhenTheHashMoves(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	var calls counter

	if got, _ := Load(context.Background(), c, "p", "stats", "", calls.compute("old")); got != "old" {
		t.Fatalf("first = %q", got)
	}
	h.set("p", "h2")
	got, err := Load(context.Background(), c, "p", "stats", "", calls.compute("new"))
	if err != nil || got != "new" {
		t.Fatalf("after the hash moved Load = %q, %v; want a recompute", got, err)
	}
	if calls.n.Load() != 2 {
		t.Errorf("computed %d times across two hashes, want 2", calls.n.Load())
	}
}

func TestLoadKeysOnProjectEndpointAndParams(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	h.set("q", "h1")
	c := New(h.fn)
	var calls counter
	ctx := context.Background()

	keys := []struct{ project, endpoint, params, value string }{
		{"p", "issues", "status=open", "a"},
		{"p", "issues", "status=closed", "b"},
		{"p", "graph", "status=open", "c"},
		{"q", "issues", "status=open", "d"},
	}
	for _, k := range keys {
		if got, _ := Load(ctx, c, k.project, k.endpoint, k.params, calls.compute(k.value)); got != k.value {
			t.Errorf("%+v = %q", k, got)
		}
	}
	for _, k := range keys {
		if got, _ := Load(ctx, c, k.project, k.endpoint, k.params, calls.compute("wrong")); got != k.value {
			t.Errorf("cached %+v = %q, want %q", k, got, k.value)
		}
	}
	if calls.n.Load() != int32(len(keys)) {
		t.Errorf("computed %d times, want once per distinct key", calls.n.Load())
	}
}

func TestLoadDoesNotCacheErrors(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	boom := errors.New("boom")
	n := 0
	fail := func(context.Context) (string, error) { n++; return "", boom }

	for i := 0; i < 2; i++ {
		if _, err := Load(context.Background(), c, "p", "stats", "", fail); !errors.Is(err, boom) {
			t.Fatalf("Load error = %v, want boom", err)
		}
	}
	if n != 2 {
		t.Errorf("a failed compute was cached: ran %d times, want 2", n)
	}
}

func TestLoadReturnsHashErrorsWithoutComputing(t *testing.T) {
	h := &hashes{err: errors.New("unreachable")}
	c := New(h.fn)
	var calls counter
	if _, err := Load(context.Background(), c, "p", "stats", "", calls.compute("v")); err == nil {
		t.Fatal("Load succeeded although the hash could not be read")
	}
	if calls.n.Load() != 0 {
		t.Error("computed a value whose freshness could not be checked")
	}
}

func TestLoadSharesOneComputeAcrossConcurrentCallers(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	release := make(chan struct{})
	var n atomic.Int32
	slow := func(context.Context) (string, error) {
		n.Add(1)
		<-release
		return "v", nil
	}

	var wg sync.WaitGroup
	results := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _ := Load(context.Background(), c, "p", "stats", "", slow)
			results <- v
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for v := range results {
		if v != "v" {
			t.Errorf("a waiter got %q", v)
		}
	}
	if n.Load() != 1 {
		t.Errorf("computed %d times for 8 concurrent callers, want 1", n.Load())
	}
}

// Results that depend on the clock (defer dates passing, the trend's day,
// staleness) must not live forever under an unchanged hash. Past the TTL the
// cached value is still served at once and a refresh runs behind it.
func TestLoadRefreshesInTheBackgroundPastTheTTL(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	now := time.Unix(1000, 0)
	var mu sync.Mutex
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }

	var n atomic.Int32
	values := []string{"first", "second"}
	compute := func(context.Context) (string, error) {
		i := n.Add(1) - 1
		return values[i], nil
	}

	ctx := context.Background()
	if got, _ := Load(ctx, c, "p", "stats", "", compute); got != "first" {
		t.Fatalf("first = %q", got)
	}
	mu.Lock()
	now = now.Add(TTL + time.Second)
	mu.Unlock()
	if got, _ := Load(ctx, c, "p", "stats", "", compute); got != "first" {
		t.Fatalf("past the TTL Load = %q; want the cached value served at once", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := Load(ctx, c, "p", "stats", "", compute)
		if got == "second" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background refresh never replaced the expired value")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n.Load() != 2 {
		t.Errorf("computed %d times, want 2 (the fill and one refresh)", n.Load())
	}
}

func TestLoadDropsAProjectsEntriesFromAnOlderHash(t *testing.T) {
	h := &hashes{}
	h.set("p", "h1")
	c := New(h.fn)
	var calls counter
	ctx := context.Background()
	Load(ctx, c, "p", "issues", "a", calls.compute("x"))
	Load(ctx, c, "p", "issues", "b", calls.compute("y"))
	h.set("p", "h2")
	Load(ctx, c, "p", "issues", "a", calls.compute("z"))

	if got := c.size(); got != 1 {
		t.Errorf("cache holds %d entries after the hash moved, want 1: entries from h1 can never be served again", got)
	}
}

func TestNilCacheComputesEveryTime(t *testing.T) {
	var calls counter
	for i := 0; i < 2; i++ {
		if got, err := Load(context.Background(), (*Cache)(nil), "p", "stats", "", calls.compute("v")); got != "v" || err != nil {
			t.Fatalf("Load = %q, %v", got, err)
		}
	}
	if calls.n.Load() != 2 {
		t.Errorf("nil cache computed %d times, want 2", calls.n.Load())
	}
}
