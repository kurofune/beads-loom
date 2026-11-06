//go:build unix

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// holdStoreLock takes the exclusive flock bd takes on the store's noms/LOCK
// and returns its release. flock locks belong to the open file, so the
// registry's own open of LOCK in this process still waits behind it.
func holdStoreLock(t *testing.T, f *embeddedFixture) func() {
	t.Helper()
	path := filepath.Join(f.w.Workdir, ".beads", "embeddeddolt", f.w.Database, ".dolt", "noms", "LOCK")
	lock, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("exclusive lock on %s: %v", path, err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}
	t.Cleanup(release)
	return release
}

// A GET /issues on an embedded store that bd holds for longer than the open
// budget answers 503 with Retry-After: 1, never 500, and leaves the project
// reachable; the same read answers 200 once the lock is released.
func TestEmbeddedIssuesWhileStoreHeldPastBudget(t *testing.T) {
	f := newEmbeddedFixture(t)
	release := holdStoreLock(t, f)

	start := time.Now()
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/fx/issues", nil))
	elapsed := time.Since(start)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /issues with the store held = %d, want 503: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\"", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "store busy") {
		t.Fatalf("503 body = %s, want it to name the busy store", body)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("GET /issues took %s with the store held; the open budget is 1.5s", elapsed)
	}
	if p, _ := f.reg.Get("fx"); !p.Reachable {
		t.Fatalf("a held store marked the project unreachable: %q", p.Reason)
	}

	release()
	code, body := f.get(t, "/api/projects/fx/issues")
	if code != http.StatusOK {
		t.Fatalf("GET /issues after release = %d: %s", code, body)
	}
	for _, id := range f.allIDs {
		if !strings.Contains(string(body), `"`+id+`"`) {
			t.Fatalf("GET /issues after release lacks %s: %s", id, body)
		}
	}
}
