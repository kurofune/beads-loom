package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest/fakedolt"
)

// netTimeout is a net.Error whose Timeout() is true, the shape a socket read
// deadline produces.
type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return true }

// reachableWithPool registers project "a" as reachable over db, resolved to
// dsn, the state a successful refresh leaves behind.
func reachableWithPool(t *testing.T, workdir string, db *sql.DB, dsn string) *Registry {
	t.Helper()
	r := New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "a", Workdir: workdir}},
	})
	t.Cleanup(func() { r.Close() })
	entry := r.byName["a"]
	entry.Reachable = true
	entry.Reason = ""
	entry.db = db
	entry.dsn = dsn
	return r
}

// liveDSN starts a server that answers the liveness check and returns its DSN.
func liveDSN(t *testing.T) string {
	t.Helper()
	return DSN("127.0.0.1", fakedolt.Start(t, "loom"), "loom")
}

// A query that runs past its deadline on a server that still answers a ping is
// a slow query. The project must stay reachable and the caller must get
// ErrQueryTimeout, never ErrUnreachable.
func TestDoQueryTimeoutLeavesProjectReachable(t *testing.T) {
	planted := map[string]error{
		"context deadline":         fmt.Errorf("stats dep-blocked: %w", context.DeadlineExceeded),
		"net timeout":              &net.OpError{Op: "read", Net: "tcp", Err: netTimeout{}},
		"wrapped net timeout":      fmt.Errorf("stats ready: %w", &net.OpError{Op: "read", Net: "tcp", Err: netTimeout{}}),
		"untyped i/o timeout text": errors.New("read tcp 127.0.0.1:1->127.0.0.1:2: i/o timeout"),
	}
	for name, plantedErr := range planted {
		t.Run(name, func(t *testing.T) {
			db := sql.OpenDB(&countingConnector{})
			t.Cleanup(func() { db.Close() })
			r := reachableWithPool(t, t.TempDir(), db, liveDSN(t))

			calls := 0
			err := r.Do(context.Background(), "a", func(context.Context, *sql.DB) error {
				calls++
				return plantedErr
			})
			if !errors.Is(err, ErrQueryTimeout) {
				t.Fatalf("Do error = %v, want ErrQueryTimeout", err)
			}
			if errors.Is(err, ErrUnreachable) {
				t.Fatalf("Do error = %v; a slow query is not an unreachable server", err)
			}
			if calls != 1 {
				t.Errorf("callback ran %d times; a timed-out query must not be retried", calls)
			}
			got, _ := r.Get("a")
			if !got.Reachable || got.Reason != "" {
				t.Errorf("project = reachable %v reason %q, want reachable with no reason", got.Reachable, got.Reason)
			}
		})
	}
}

// Do bounds every query with its own deadline, so a statement that never
// returns ends as ErrQueryTimeout rather than hanging the request.
func TestDoImposesQueryDeadline(t *testing.T) {
	db := sql.OpenDB(&countingConnector{})
	t.Cleanup(func() { db.Close() })
	r := reachableWithPool(t, t.TempDir(), db, liveDSN(t))
	r.queryTimeout = 50 * time.Millisecond

	start := time.Now()
	err := r.Do(context.Background(), "a", func(ctx context.Context, _ *sql.DB) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("Do error = %v, want ErrQueryTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Do took %s; the query deadline did not fire", elapsed)
	}
	if got, _ := r.Get("a"); !got.Reachable {
		t.Errorf("project went unreachable on a query deadline: %q", got.Reason)
	}
}

// A caller that hangs up is neither a timeout nor an outage: the error comes
// back unchanged and the project stays reachable.
func TestDoCallerCancelIsNotATimeout(t *testing.T) {
	db := sql.OpenDB(&countingConnector{})
	t.Cleanup(func() { db.Close() })
	r := reachableWithPool(t, t.TempDir(), db, liveDSN(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Do(ctx, "a", func(ctx context.Context, _ *sql.DB) error { return ctx.Err() })
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("Do error = %v, want context.Canceled and not ErrQueryTimeout", err)
	}
	if got, _ := r.Get("a"); !got.Reachable {
		t.Errorf("project went unreachable on a cancelled request: %q", got.Reason)
	}
}

// closedPortWorkspace returns a workspace whose port file names a port nothing
// listens on, and that port.
func closedPortWorkspace(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	workdir := writeWorkspace(t, withPort(portStr), withMetadata(`{"dolt_database":"loom"}`))
	port, _ := strconv.Atoi(portStr)
	return workdir, port
}

// A real connection failure still takes the project down, with the reason the
// unreachable panel shows next to `bd dolt start`.
func TestDoConnectionFailureOnClosedPortMarksUnreachable(t *testing.T) {
	workdir, port := closedPortWorkspace(t)
	db, err := sql.Open("mysql", DSN("127.0.0.1", port, "loom"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := reachableWithPool(t, workdir, db, DSN("127.0.0.1", port, "loom"))

	err = r.Do(context.Background(), "a", func(ctx context.Context, db *sql.DB) error {
		var one int
		return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	})
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Do error = %v, want ErrUnreachable", err)
	}
	if errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("Do error = %v; a refused connection is not a slow query", err)
	}
	got, _ := r.Get("a")
	if got.Reachable {
		t.Fatal("project stayed reachable with nothing listening on its port")
	}
	if !strings.Contains(got.Reason, "cannot connect") {
		t.Errorf("reason = %q, want it to say the server cannot be reached", got.Reason)
	}
}

// A timeout on a server that no longer answers is an outage, not a slow query:
// the liveness check behind the timeout classification must see that.
func TestDoTimeoutOnDeadServerMarksUnreachable(t *testing.T) {
	workdir, port := closedPortWorkspace(t)
	db, err := sql.Open("mysql", DSN("127.0.0.1", port, "loom"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := reachableWithPool(t, workdir, db, DSN("127.0.0.1", port, "loom"))

	err = r.Do(context.Background(), "a", func(context.Context, *sql.DB) error {
		return fmt.Errorf("stats ready: %w", context.DeadlineExceeded)
	})
	if !errors.Is(err, ErrUnreachable) || errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("Do error = %v, want ErrUnreachable only", err)
	}
	if got, _ := r.Get("a"); got.Reachable {
		t.Fatal("project stayed reachable although its server is gone")
	}
}
