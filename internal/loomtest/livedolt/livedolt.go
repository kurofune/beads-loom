// Package livedolt is the one implementation of Loom's live-test environment
// rule (AGENTS.md → "Testing — live-database tests", loom-648). In one
// sentence:
//
//	An absent ENVIRONMENT skips; a missing FIXTURE inside a reachable
//	database fails.
//
// The environment is everything a live test needs before it can ask the
// database a question: `.beads/dolt-server.port`, `.beads/metadata.json`, and a
// Dolt server that answers on that port for that database. None of it is
// produced by the code under test, none of it exists in a fresh clone or a new
// worktree, and nothing in the repo can conjure it — so a test that fails on
// its absence reddens `go test ./...` for every developer who has not run
// `bd dolt start` while proving nothing about the code. Hence the skip, and
// hence every skip message naming that exact command.
//
// Everything past the gate is a fixture: a bead, an epic, a row, a column.
// Its absence is a finding about the seeded database or the code under test,
// so it must fail loudly. Do not extend this gate to cover query results.
//
// Layering note: this package imports internal/registry, so registry's own
// in-package live tests cannot import it (that would be an import cycle in
// their test binary). Their gates stay local port-file skips carrying the same
// `bd dolt start` message.
package livedolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/kurofune/beads-loom/internal/registry"
)

// Env is a reachable local Dolt server that a live test may query.
type Env struct {
	Workdir  string // the beads workspace whose .beads/ resolved this server
	Port     int    // from .beads/dolt-server.port, the source of truth
	Database string // dolt_database from .beads/metadata.json
}

// DSN addresses the resolved server, exactly the way the registry does.
func (e Env) DSN() string {
	return registry.DSN(localHost, e.Port, e.Database)
}

const localHost = "127.0.0.1"

// pingTimeout bounds the reachability probe. A local server answers in
// milliseconds, so anything near this means it is gone, not busy.
const pingTimeout = 5 * time.Second

// Require is the environment gate every live-database test goes through. It
// skips — with a message naming `bd dolt start` — when the Dolt server for
// workdir is absent or unreachable, and returns the resolved server otherwise.
// It never skips for anything found (or not found) inside that database: see
// the package comment.
func Require(t *testing.T, workdir string) Env {
	t.Helper()
	env, reason := probe(workdir, ping)
	if reason != "" {
		t.Skipf("%s", reason)
	}
	return env
}

// Probe applies the same rule without touching the test's outcome, for tests
// that scan candidate workspaces and take the first live one. A non-empty
// reason means that workspace's environment is absent.
func Probe(workdir string) (Env, string) {
	return probe(workdir, ping)
}

// probe resolves and probes the environment. A non-empty reason means the
// environment is absent and the caller should skip; it always names the
// command that makes the test run.
func probe(workdir string, pingFn func(Env) error) (Env, string) {
	env := Env{Workdir: workdir}

	beads := filepath.Join(workdir, ".beads")
	if st, err := os.Stat(beads); err != nil || !st.IsDir() {
		return env, absent(workdir, fmt.Sprintf("no .beads directory in %s", workdir))
	}

	portFile := filepath.Join(beads, registry.PortFileName)
	raw, err := os.ReadFile(portFile)
	if err != nil {
		return env, absent(workdir, fmt.Sprintf("no %s", portFile))
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return env, absent(workdir, fmt.Sprintf("%s does not hold a port number", portFile))
	}
	if port <= 0 || port > 65535 {
		return env, absent(workdir, fmt.Sprintf("%s holds out-of-range port %d", portFile, port))
	}
	env.Port = port

	metaFile := filepath.Join(beads, registry.MetadataFileName)
	metaRaw, err := os.ReadFile(metaFile)
	if err != nil {
		return env, absent(workdir, fmt.Sprintf("no %s", metaFile))
	}
	var meta struct {
		DoltDatabase string `json:"dolt_database"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return env, absent(workdir, fmt.Sprintf("cannot parse %s: %v", metaFile, err))
	}
	if strings.TrimSpace(meta.DoltDatabase) == "" {
		return env, absent(workdir, fmt.Sprintf("%s has no dolt_database key", metaFile))
	}
	env.Database = strings.TrimSpace(meta.DoltDatabase)

	if err := pingFn(env); err != nil {
		return env, absent(workdir, fmt.Sprintf("the dolt server on port %d is not answering for %s: %v", env.Port, env.Database, err))
	}
	return env, ""
}

// absent phrases one skip reason: what is missing, then how to fix it.
func absent(workdir, cause string) string {
	return fmt.Sprintf("live-database test skipped — %s; start the dolt server with `bd dolt start -C %s` to run it", cause, workdir)
}

// ping is the reachability probe: a real connection to the resolved database,
// since an open TCP port alone does not mean Dolt is serving it.
func ping(env Env) error {
	db, err := sql.Open("mysql", env.DSN())
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	return db.PingContext(ctx)
}
