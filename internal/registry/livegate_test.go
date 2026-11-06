package registry

// This file is internal/registry's local copy of the live-test environment
// gate (AGENTS.md → "Testing — live-database tests and the environment gate"):
//
//	An absent ENVIRONMENT skips; a missing FIXTURE inside a reachable
//	database fails.
//
// The environment is `.beads/dolt-server.port`, `.beads/metadata.json`, and a
// Dolt server *answering* on that port for that database. Reading the two files
// is not the gate — a stopped server leaves its port file behind, so a gate that
// only stats the files waves the laptop-reboot state through and the live tests
// FAIL where every other package SKIPs (loom-ii1).
//
// Layering note: internal/loomtest/livedolt is the one implementation of this
// rule for every other package, and this duplicates it on purpose. livedolt
// imports internal/registry (livedolt.go:41), so this package's test binary
// importing livedolt would be an import cycle. The duplication is load-bearing
// and documented at livedolt.go:21 and in AGENTS.md; it is not a dedup target.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// liveEnv is a reachable local Dolt server that a live test may query.
type liveEnv struct {
	Workdir  string // the beads workspace whose .beads/ resolved this server
	Port     int    // from .beads/dolt-server.port, the source of truth
	Database string // dolt_database from .beads/metadata.json
}

const liveHost = "127.0.0.1"

// livePingTimeout bounds the reachability probe. A local server answers in
// milliseconds, so anything near this means it is gone, not busy.
const livePingTimeout = 5 * time.Second

// requireLive is the environment gate this package's live tests go through. It
// skips — with a message naming `bd dolt start -C <workdir>` — when the Dolt
// server for workdir is absent, stale, or unreachable, and returns the resolved
// server otherwise. It never skips for anything found (or not found) inside
// that database: see the file comment.
func requireLive(t *testing.T, workdir string) liveEnv {
	t.Helper()
	env, reason := probeLive(workdir, pingLive)
	if reason != "" {
		t.Skipf("%s", reason)
	}
	return env
}

// probeLive resolves and probes the environment. A non-empty reason means the
// environment is absent and the caller should skip; it always names the command
// that makes the test run.
func probeLive(workdir string, pingFn func(liveEnv) error) (liveEnv, string) {
	env := liveEnv{Workdir: workdir}

	beads := filepath.Join(workdir, ".beads")
	if st, err := os.Stat(beads); err != nil || !st.IsDir() {
		return env, liveAbsent(workdir, fmt.Sprintf("no .beads directory in %s", workdir))
	}

	portFile := filepath.Join(beads, PortFileName)
	raw, err := os.ReadFile(portFile)
	if err != nil {
		return env, liveAbsent(workdir, fmt.Sprintf("no %s", portFile))
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return env, liveAbsent(workdir, fmt.Sprintf("%s does not hold a port number", portFile))
	}
	if port <= 0 || port > 65535 {
		return env, liveAbsent(workdir, fmt.Sprintf("%s holds out-of-range port %d", portFile, port))
	}
	env.Port = port

	metaFile := filepath.Join(beads, MetadataFileName)
	metaRaw, err := os.ReadFile(metaFile)
	if err != nil {
		return env, liveAbsent(workdir, fmt.Sprintf("no %s", metaFile))
	}
	var meta struct {
		DoltDatabase string `json:"dolt_database"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return env, liveAbsent(workdir, fmt.Sprintf("cannot parse %s: %v", metaFile, err))
	}
	if strings.TrimSpace(meta.DoltDatabase) == "" {
		return env, liveAbsent(workdir, fmt.Sprintf("%s has no dolt_database key", metaFile))
	}
	env.Database = strings.TrimSpace(meta.DoltDatabase)

	if err := pingFn(env); err != nil {
		return env, liveAbsent(workdir, fmt.Sprintf("the dolt server on port %d is not answering for %s: %v", env.Port, env.Database, err))
	}
	return env, ""
}

// liveAbsent phrases one skip reason: what is missing, then how to fix it.
func liveAbsent(workdir, cause string) string {
	return fmt.Sprintf("live-database test skipped — %s; start the dolt server with `bd dolt start -C %s` to run it", cause, workdir)
}

// pingLive is the reachability probe: a real connection to the resolved
// database, since an open TCP port alone does not mean Dolt is serving it.
func pingLive(env liveEnv) error {
	db, err := sql.Open("mysql", DSN(liveHost, env.Port, env.Database))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), livePingTimeout)
	defer cancel()
	return db.PingContext(ctx)
}

// writeLiveEnv lays down a .beads directory with the given port-file and
// metadata contents. An empty string means "do not write this file at all".
func writeLiveEnv(t *testing.T, port, metadata string) string {
	t.Helper()
	dir := t.TempDir()
	beads := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	if port != "" {
		if err := os.WriteFile(filepath.Join(beads, PortFileName), []byte(port), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if metadata != "" {
		if err := os.WriteFile(filepath.Join(beads, MetadataFileName), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const liveMetadata = `{"dolt_database":"loom","dolt_server_port":3311}`

func livePingOK(liveEnv) error { return nil }

// TestLiveGateSkipsOnAStalePortFile is the laptop-reboot state that reddened
// this package (loom-ii1): the Dolt server is stopped but .beads/dolt-server.port
// is left behind, so every file the gate reads is present and well-formed and
// nothing is listening. Resolving the files is not the gate — answering is.
func TestLiveGateSkipsOnAStalePortFile(t *testing.T) {
	dir := writeLiveEnv(t, strconv.Itoa(freePort(t)), liveMetadata)

	env, reason := probeLive(dir, pingLive)
	if reason == "" {
		t.Fatalf("probeLive accepted a stale port file as live (%+v)", env)
	}
	if !strings.Contains(reason, "bd dolt start -C "+dir) {
		t.Errorf("skip reason does not name the start command: %s", reason)
	}
}

// Every skip reason must name the command that makes the test run — a skip that
// only says "not available" leaves the reader with no way to turn it green.
func TestLiveGateReasonsNameTheStartCommand(t *testing.T) {
	tests := []struct {
		name string
		dir  func(t *testing.T) string
		ping func(liveEnv) error // nil means "the server answers"
		want string              // a fragment identifying the cause
	}{
		{
			name: "no .beads directory",
			dir:  func(t *testing.T) string { return t.TempDir() },
			want: ".beads",
		},
		{
			name: "no port file",
			dir:  func(t *testing.T) string { return writeLiveEnv(t, "", liveMetadata) },
			want: PortFileName,
		},
		{
			name: "port file is not a number",
			dir:  func(t *testing.T) string { return writeLiveEnv(t, "not-a-port", liveMetadata) },
			want: "port number",
		},
		{
			name: "port is out of range",
			dir:  func(t *testing.T) string { return writeLiveEnv(t, "99999", liveMetadata) },
			want: "out-of-range",
		},
		{
			name: "no metadata file",
			dir:  func(t *testing.T) string { return writeLiveEnv(t, "3311", "") },
			want: MetadataFileName,
		},
		{
			name: "metadata is unparseable",
			dir:  func(t *testing.T) string { return writeLiveEnv(t, "3311", "{") },
			want: MetadataFileName,
		},
		{
			name: "metadata has no database name",
			dir:  func(t *testing.T) string { return writeLiveEnv(t, "3311", `{"database":"dolt"}`) },
			want: "dolt_database",
		},
		{
			name: "server does not answer",
			dir:  func(t *testing.T) string { return writeLiveEnv(t, "3311", liveMetadata) },
			ping: func(liveEnv) error { return errors.New("connection refused") },
			want: "not answering",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.dir(t)
			ping := tc.ping
			if ping == nil {
				ping = livePingOK
			}
			env, reason := probeLive(dir, ping)
			if reason == "" {
				t.Fatalf("probeLive(%s) accepted a broken environment as live (%+v)", dir, env)
			}
			if !strings.Contains(reason, "bd dolt start -C "+dir) {
				t.Errorf("skip reason does not name the start command: %s", reason)
			}
			if !strings.Contains(reason, tc.want) {
				t.Errorf("skip reason does not name the cause %q: %s", tc.want, reason)
			}
		})
	}
}

// The other half of the rule: a complete, reachable environment must NOT skip.
// A gate that always skipped would satisfy every test above while guarding
// nothing — this is the case that keeps it honest.
func TestLiveGateAcceptsAReachableEnvironment(t *testing.T) {
	dir := writeLiveEnv(t, " 55933\n", liveMetadata)

	env, reason := probeLive(dir, livePingOK)
	if reason != "" {
		t.Fatalf("probeLive rejected a live environment: %s", reason)
	}
	if env.Workdir != dir || env.Port != 55933 || env.Database != "loom" {
		t.Fatalf("probeLive = %+v, want workdir %s, port 55933, database loom", env, dir)
	}
}

// pingLive is the real reachability probe; it must actually fail on a dead port
// rather than wave every environment through — otherwise the stale-port-file
// case above would pass for the wrong reason.
func TestLivePingFailsOnAClosedPort(t *testing.T) {
	port := freePort(t)
	if err := pingLive(liveEnv{Workdir: t.TempDir(), Port: port, Database: "loom"}); err == nil {
		t.Fatalf("pingLive(closed port %d) = nil, want an error", port)
	}
}

// requireLive is the t-facing wrapper: an absent environment must skip the
// test, not fail it.
func TestRequireLiveSkipsInsteadOfFailing(t *testing.T) {
	t.Cleanup(func() {
		if !t.Skipped() {
			t.Errorf("requireLive did not skip when the environment was absent")
		}
	})
	requireLive(t, t.TempDir())
}
