package livedolt

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEnv lays down a .beads directory with the given port-file and metadata
// contents. An empty string means "do not write this file at all".
func writeEnv(t *testing.T, port, metadata string) string {
	t.Helper()
	dir := t.TempDir()
	beads := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	if port != "" {
		if err := os.WriteFile(filepath.Join(beads, "dolt-server.port"), []byte(port), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if metadata != "" {
		if err := os.WriteFile(filepath.Join(beads, "metadata.json"), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const goodMetadata = `{"dolt_database":"loom","dolt_server_port":3311}`

func pingOK(Env) error { return nil }

// Every skip reason must name the command that makes the test run — a skip that
// only says "not available" leaves the reader with no way to turn it green.
func TestProbeReasonsNameTheStartCommand(t *testing.T) {
	tests := []struct {
		name string
		dir  func(t *testing.T) string
		want string // a fragment identifying the cause
	}{
		{
			name: "no .beads directory",
			dir:  func(t *testing.T) string { return t.TempDir() },
			want: ".beads",
		},
		{
			name: "no port file",
			dir:  func(t *testing.T) string { return writeEnv(t, "", goodMetadata) },
			want: "dolt-server.port",
		},
		{
			name: "port file is not a number",
			dir:  func(t *testing.T) string { return writeEnv(t, "not-a-port", goodMetadata) },
			want: "port number",
		},
		{
			name: "port is out of range",
			dir:  func(t *testing.T) string { return writeEnv(t, "99999", goodMetadata) },
			want: "out-of-range",
		},
		{
			name: "no metadata file",
			dir:  func(t *testing.T) string { return writeEnv(t, "3311", "") },
			want: "metadata.json",
		},
		{
			name: "metadata is unparseable",
			dir:  func(t *testing.T) string { return writeEnv(t, "3311", "{") },
			want: "metadata.json",
		},
		{
			name: "metadata has no database name",
			dir:  func(t *testing.T) string { return writeEnv(t, "3311", `{"database":"dolt"}`) },
			want: "dolt_database",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.dir(t)
			env, reason := probe(dir, pingOK)
			if reason == "" {
				t.Fatalf("probe(%s) accepted a broken environment as live (%+v)", dir, env)
			}
			if !strings.Contains(reason, "bd dolt start") {
				t.Errorf("skip reason does not name the start command: %s", reason)
			}
			if !strings.Contains(reason, tc.want) {
				t.Errorf("skip reason does not name the cause %q: %s", tc.want, reason)
			}
		})
	}
}

// The gate must skip when the server is not answering, not when a fixture is
// missing — so an unreachable port is an environment absence.
func TestProbeSkipsWhenTheServerDoesNotAnswer(t *testing.T) {
	dir := writeEnv(t, "3311", goodMetadata)
	_, reason := probe(dir, func(Env) error { return errors.New("connection refused") })
	if reason == "" {
		t.Fatal("probe accepted an unreachable server as live")
	}
	if !strings.Contains(reason, "bd dolt start") {
		t.Errorf("skip reason does not name the start command: %s", reason)
	}
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("skip reason drops the underlying error: %s", reason)
	}
}

// The other half of the rule: a complete, reachable environment must NOT skip.
// A gate that always skips would satisfy every test above while guarding
// nothing — this is the case that keeps it honest.
func TestProbeAcceptsAReachableEnvironment(t *testing.T) {
	dir := writeEnv(t, " 55933\n", goodMetadata)
	env, reason := probe(dir, pingOK)
	if reason != "" {
		t.Fatalf("probe rejected a live environment: %s", reason)
	}
	if env.Workdir != dir || env.Port != 55933 || env.Database != "loom" {
		t.Fatalf("probe = %+v, want workdir %s, port 55933, database loom", env, dir)
	}
	if !strings.Contains(env.DSN(), "55933") || !strings.Contains(env.DSN(), "/loom") {
		t.Errorf("DSN does not address the resolved server: %s", env.DSN())
	}
}

// pingDolt is the real reachability probe; it must actually fail on a dead
// port rather than wave every environment through.
func TestPingFailsOnAClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ping(Env{Workdir: t.TempDir(), Port: port, Database: "loom"}); err == nil {
		t.Fatalf("ping(closed port %d) = nil, want an error", port)
	}
}

// Probe is the scan-friendly form used when a test tries several
// candidate workspaces: same rule, no effect on the test's outcome.
func TestProbeExportedFormReportsAbsence(t *testing.T) {
	env, reason := Probe(t.TempDir())
	if reason == "" {
		t.Fatalf("Probe accepted an empty workdir as live (%+v)", env)
	}
	if !strings.Contains(reason, "bd dolt start") {
		t.Errorf("reason does not name the start command: %s", reason)
	}
}

// Require is the t-facing wrapper: absent environment => skip, not fail.
func TestRequireSkipsInsteadOfFailing(t *testing.T) {
	t.Run("environment absent", func(t *testing.T) {
		t.Cleanup(func() {
			if !t.Skipped() {
				t.Errorf("Require did not skip when the environment was absent")
			}
		})
		Require(t, t.TempDir())
	})
}
