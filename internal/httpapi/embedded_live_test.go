package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest/embeddedbd"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/sse"
)

func TestMain(m *testing.M) { os.Exit(embeddedbd.Main(m)) }

// embeddedFixture is an embedded workspace holding an epic with two children,
// the second blocked by the first, and a status change that writes events.
type embeddedFixture struct {
	w                   *embeddedbd.Workspace
	epic, first, second string
	server              *Server
	reg                 *registry.Registry
	allIDs              []string
}

func newEmbeddedFixture(t *testing.T) *embeddedFixture {
	t.Helper()
	w := embeddedbd.Clone(t)
	f := &embeddedFixture{w: w}
	f.epic = w.Create(t, "Embedded epic", "--type", "epic")
	f.first = w.Create(t, "First child", "--parent", f.epic)
	f.second = w.Create(t, "Second child", "--parent", f.epic)
	w.Run(t, "dep", "add", f.second, f.first)
	w.Run(t, "update", f.first, "--status", "in_progress")
	f.allIDs = []string{f.epic, f.first, f.second}

	f.reg = registry.New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: w.Workdir}}})
	t.Cleanup(func() { _ = f.reg.Close() })
	f.reg.Refresh(context.Background())
	if p, _ := f.reg.Get("fx"); !p.Reachable || !p.Embedded {
		t.Fatalf("fixture project = %+v; want reachable embedded", p)
	}
	f.server = New(Options{Registry: f.reg})
	return f
}

func (f *embeddedFixture) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.Bytes()
}

// sweep calls every read endpoint and checks each answers 200 with the
// fixture's issues in it.
func (f *embeddedFixture) sweep(t *testing.T) {
	t.Helper()
	base := "/api/projects/fx"

	code, body := f.get(t, base+"/issues")
	if code != http.StatusOK {
		t.Fatalf("GET /issues = %d: %s", code, body)
	}
	var list queries.IssueList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, is := range list.Issues {
		got[is.ID] = is.Title
	}
	for _, id := range f.allIDs {
		if _, ok := got[id]; !ok {
			t.Fatalf("GET /issues lacks %s: %s", id, body)
		}
	}

	code, body = f.get(t, base+"/issues/"+f.first)
	if code != http.StatusOK {
		t.Fatalf("GET /issues/%s = %d: %s", f.first, code, body)
	}
	var detail queries.IssueDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != f.first || detail.Title != "First child" || detail.Status != "in_progress" {
		t.Fatalf("detail = %+v", detail.Issue)
	}
	if len(detail.Events) == 0 {
		t.Fatalf("GET /issues/%s carries no events: %s", f.first, body)
	}
	if detail.Deps.Parent == nil || detail.Deps.Parent.ID != f.epic {
		t.Fatalf("detail parent = %+v, want %s", detail.Deps.Parent, f.epic)
	}

	code, body = f.get(t, base+"/stats")
	if code != http.StatusOK {
		t.Fatalf("GET /stats = %d: %s", code, body)
	}
	var stats queries.Stats
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.ByStatus["in_progress"] < 1 || stats.ByStatus["open"] < 2 {
		t.Fatalf("stats.byStatus = %v, want the fixture's 1 in_progress and 2+ open", stats.ByStatus)
	}

	code, body = f.get(t, base+"/graph?epic="+f.epic)
	if code != http.StatusOK {
		t.Fatalf("GET /graph = %d: %s", code, body)
	}
	for _, id := range []string{f.first, f.second} {
		if !strings.Contains(string(body), `"`+id+`"`) {
			t.Fatalf("GET /graph lacks %s: %s", id, body)
		}
	}

	code, body = f.get(t, base+"/initiatives")
	if code != http.StatusOK {
		t.Fatalf("GET /initiatives = %d: %s", code, body)
	}
	if !strings.Contains(string(body), `"`+f.epic+`"`) {
		t.Fatalf("GET /initiatives lacks %s: %s", f.epic, body)
	}
}

// Every read endpoint answers from an embedded workspace that `bd create`
// wrote, with no server and no conversion.
func TestEmbeddedReadEndpoints(t *testing.T) {
	newEmbeddedFixture(t).sweep(t)
}

// tickCounter counts the poller's probes.
type tickCounter struct {
	*sse.RegistryProber
	ticks atomic.Int64
}

func (c *tickCounter) Hash(ctx context.Context, project string) (string, error) {
	defer c.ticks.Add(1)
	return c.RegistryProber.Hash(ctx, project)
}

func runMonitor(t *testing.T, reg *registry.Registry, interval time.Duration) *tickCounter {
	t.Helper()
	counter := &tickCounter{RegistryProber: sse.NewRegistryProber(reg)}
	hub := sse.NewHub()
	mon := sse.NewMonitor(hub, []string{"fx"}, sse.MonitorOptions{Prober: counter, LiveInterval: interval})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { mon.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; hub.Close() })
	return counter
}

// Loom's reads leave .beads/ byte-identical: the five endpoints plus ten
// poller ticks change, add and remove no file.
func TestEmbeddedReadsLeaveBeadsUntouched(t *testing.T) {
	f := newEmbeddedFixture(t)
	before := f.w.HashBeads(t)

	f.sweep(t)
	counter := runMonitor(t, f.reg, 20*time.Millisecond)
	deadline := time.Now().Add(10 * time.Second)
	for counter.ticks.Load() < 10 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d poller ticks in 10s", counter.ticks.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	after := f.w.HashBeads(t)
	for path, sum := range before {
		if after[path] != sum {
			t.Errorf("%s changed: %s -> %q", path, sum, after[path])
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s appeared", path)
		}
	}
	if len(before) < 5 {
		t.Fatalf("hashed only %d entries under .beads; the fixture is not a bd workspace", len(before))
	}
}

// Twenty bd processes write the embedded store while Loom serves GET /issues
// in a loop and the poller ticks. bd never fails, every GET answers 200 or the
// busy-store 503 with Retry-After (never 500), and every id bd printed is in a
// later GET /issues.
func TestEmbeddedConcurrentWrites(t *testing.T) {
	f := newEmbeddedFixture(t)
	runMonitor(t, f.reg, 50*time.Millisecond)

	stop := make(chan struct{})
	var reads, busy, readFailures atomic.Int64
	var readers sync.WaitGroup
	var firstReadErr atomic.Value
	for i := 0; i < 2; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := httptest.NewRecorder()
				f.server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/fx/issues", nil))
				reads.Add(1)
				if rec.Code == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") != "" {
					busy.Add(1)
					continue
				}
				if rec.Code != http.StatusOK {
					readFailures.Add(1)
					firstReadErr.CompareAndSwap(nil, fmt.Sprintf("%d %s", rec.Code, rec.Body.String()))
				}
			}
		}()
	}

	const creates, updates = 12, 8
	var writers sync.WaitGroup
	var mu sync.Mutex
	var created []string
	var failures []error
	for i := 0; i < creates+updates; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			var out string
			var err error
			if i < creates {
				out, err = f.w.Exec("create", fmt.Sprintf("concurrent %d", i), "--silent")
			} else {
				target := f.allIDs[i%len(f.allIDs)]
				_, err = f.w.Exec("update", target, "--notes", fmt.Sprintf("concurrent update %d", i))
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			if i < creates {
				created = append(created, out)
			}
		}(i)
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	for _, err := range failures {
		var ee *embeddedbd.ExecError
		if errors.As(err, &ee) {
			t.Errorf("bd exited non-zero: %v", ee)
		} else {
			t.Errorf("bd failed: %v", err)
		}
	}
	if len(created) != creates {
		t.Fatalf("bd printed %d ids, want %d", len(created), creates)
	}
	if reads.Load() == 0 {
		t.Fatal("no GET /issues ran while bd wrote")
	}
	if n := readFailures.Load(); n > 0 {
		t.Errorf("%d of %d GET /issues failed during the writes; first: %v", n, reads.Load(), firstReadErr.Load())
	}
	if busy.Load() == reads.Load() {
		t.Errorf("all %d GET /issues answered busy; none was served during the writes", reads.Load())
	}
	t.Logf("%d GET /issues during %d bd writes, %d answered busy", reads.Load(), creates+updates, busy.Load())

	code, body := f.get(t, "/api/projects/fx/issues")
	if code != http.StatusOK {
		t.Fatalf("GET /issues after the writes = %d: %s", code, body)
	}
	var list queries.IssueList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, is := range list.Issues {
		have[is.ID] = true
	}
	for _, id := range created {
		if !have[id] {
			t.Errorf("bd printed %s, but GET /issues lacks it", id)
		}
	}
}
