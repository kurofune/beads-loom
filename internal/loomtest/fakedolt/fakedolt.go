// Package fakedolt runs an in-process MySQL-protocol server for tests that
// need a live server Loom can dial, connect to and ping, without a Dolt
// install. It serves one empty in-memory database to any user.
package fakedolt

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	sqle "github.com/dolthub/go-mysql-server"
	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/server"
	"github.com/dolthub/go-mysql-server/sql"
)

// Start serves an empty database named database on a free loopback port until
// the test ends, and returns that port.
func Start(t testing.TB, database string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pro := memory.NewDBProvider(memory.NewDatabase(database))
	srv, err := server.NewServer(server.Config{Protocol: "tcp", Listener: ln},
		sqle.NewDefault(pro), sql.NewContext, memory.NewSessionBuilder(pro), nil)
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	go srv.Start()
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// Workspace writes a beads workspace whose port file and metadata point at
// port and database, the files the registry resolves a project from.
func Workspace(t testing.TB, port int, database string) string {
	t.Helper()
	workdir := t.TempDir()
	beads := filepath.Join(workdir, ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"dolt-server.port": strconv.Itoa(port),
		"metadata.json":    `{"dolt_database":"` + database + `"}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(beads, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return workdir
}
