package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
)

// metadataBd is a bd that resolves its target the way bd does: from the
// workdir's .beads/metadata.json, read fresh on every call.
func metadataBd(t *testing.T) *bdexec.MockRunner {
	t.Helper()
	mock := bdexec.NewMockRunner()
	mock.Handler = func(call bdexec.Call) (string, string, error) {
		data, err := os.ReadFile(filepath.Join(call.Workdir, ".beads", MetadataFileName))
		if err != nil {
			return "", err.Error(), err
		}
		var md struct {
			DoltDatabase string `json:"dolt_database"`
			Host         string `json:"dolt_server_host"`
			Port         int    `json:"dolt_server_port"`
		}
		if err := json.Unmarshal(data, &md); err != nil {
			return "", err.Error(), err
		}
		out, _ := json.Marshal(map[string]any{
			"backend": "dolt", "connection_ok": true, "database": md.DoltDatabase,
			"embedded": false, "host": md.Host, "port": md.Port,
		})
		return string(out), "", nil
	}
	return mock
}

func metadataFor(database string) string {
	return `{"backend":"dolt","dolt_mode":"server","dolt_server_host":"127.0.0.1","dolt_server_port":3311,"dolt_database":"` + database + `"}`
}

func rewriteMetadata(t *testing.T, workdir, database string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workdir, ".beads", MetadataFileName), []byte(metadataFor(database)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func checkedRegistry(t *testing.T, cfg *config.Config, runner bdexec.Runner, log *slog.Logger) *Registry {
	t.Helper()
	reg := New(cfg)
	t.Cleanup(func() { reg.Close() })
	reg.dialFunc = func(network, addr string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	}
	reg.SetWriteCheck(bdInfo(bdexec.New(runner)), log)
	return reg
}

// bdInfo adapts the bd client the way main does.
func bdInfo(c *bdexec.Client) WriteTargets {
	return func(ctx context.Context, workdir string) (BdTarget, error) {
		t, err := c.Info(ctx, workdir)
		return BdTarget(t), err
	}
}

func mustGet(t *testing.T, reg *Registry, name string) Project {
	t.Helper()
	p, err := reg.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWritableComparesLoomsDatabaseWithBds(t *testing.T) {
	t.Run("override differs from bd", func(t *testing.T) {
		dir := writeWorkspace(t, withPort("3311"), withMetadata(metadataFor("bd_db")))
		cfg := &config.Config{Projects: []config.Project{{Name: "p", Workdir: dir, Database: "loom_db"}}}
		reg := checkedRegistry(t, cfg, metadataBd(t), nil)

		if err := reg.RefreshProject(context.Background(), "p"); err != nil {
			t.Fatal(err)
		}
		p := mustGet(t, reg, "p")
		if p.Writable {
			t.Fatalf("Writable = true, want false for override loom_db vs bd's bd_db")
		}
		for _, want := range []string{`"loom_db"`, `"bd_db"`} {
			if !strings.Contains(p.WriteReason, want) {
				t.Errorf("WriteReason = %q, want it to name %s", p.WriteReason, want)
			}
		}
	})

	t.Run("override matches bd", func(t *testing.T) {
		dir := writeWorkspace(t, withPort("3311"), withMetadata(metadataFor("same")))
		cfg := &config.Config{Projects: []config.Project{{Name: "p", Workdir: dir, Database: "same"}}}
		reg := checkedRegistry(t, cfg, metadataBd(t), nil)

		if err := reg.RefreshProject(context.Background(), "p"); err != nil {
			t.Fatal(err)
		}
		if p := mustGet(t, reg, "p"); !p.Writable || p.WriteReason != "" {
			t.Fatalf("Writable=%v WriteReason=%q, want true and empty", p.Writable, p.WriteReason)
		}
	})

	t.Run("no override", func(t *testing.T) {
		dir := writeWorkspace(t, withPort("3311"), withMetadata(metadataFor("same")))
		cfg := &config.Config{Projects: []config.Project{{Name: "p", Workdir: dir}}}
		mock := metadataBd(t)
		reg := checkedRegistry(t, cfg, mock, nil)

		if err := reg.RefreshProject(context.Background(), "p"); err != nil {
			t.Fatal(err)
		}
		if p := mustGet(t, reg, "p"); !p.Writable || p.WriteReason != "" {
			t.Fatalf("Writable=%v WriteReason=%q, want true and empty", p.Writable, p.WriteReason)
		}
		// Loom then reads the same metadata.json bd does, so there is nothing
		// to compare and no subprocess to pay for.
		if n := len(mock.Calls()); n != 0 {
			t.Fatalf("bd ran %d times for a project with no override, want 0", n)
		}
	})
}

func TestWritableRemoteServerComparesTheAddress(t *testing.T) {
	dir := writeWorkspace(t, withMetadata(metadataFor("loom")))
	cfg := &config.Config{
		Servers:  []config.Server{{Name: "team", Host: "10.0.0.9", Port: 3307, Kind: config.KindRemote}},
		Projects: []config.Project{{Name: "p", Workdir: dir, Server: "team"}},
	}
	reg := checkedRegistry(t, cfg, metadataBd(t), nil)

	if err := reg.RefreshProject(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	p := mustGet(t, reg, "p")
	if p.Writable {
		t.Fatalf("Writable = true, want false: Loom reads 10.0.0.9:3307, bd writes 127.0.0.1:3311")
	}
	for _, want := range []string{"10.0.0.9:3307", "127.0.0.1:3311", `"loom"`} {
		if !strings.Contains(p.WriteReason, want) {
			t.Errorf("WriteReason = %q, want it to name %s", p.WriteReason, want)
		}
	}
}

func TestWritableIsRederivedOnEveryRefresh(t *testing.T) {
	dir := writeWorkspace(t, withPort("3311"), withMetadata(metadataFor("loom_db")))
	cfg := &config.Config{Projects: []config.Project{{Name: "p", Workdir: dir, Database: "loom_db"}}}
	reg := checkedRegistry(t, cfg, metadataBd(t), nil)
	ctx := context.Background()

	if err := reg.RefreshProject(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if p := mustGet(t, reg, "p"); !p.Writable {
		t.Fatalf("first refresh: Writable = false (%q), want true", p.WriteReason)
	}

	rewriteMetadata(t, dir, "elsewhere")
	if err := reg.RefreshProject(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if p := mustGet(t, reg, "p"); p.Writable || !strings.Contains(p.WriteReason, `"elsewhere"`) {
		t.Fatalf("second refresh: Writable=%v WriteReason=%q, want false naming \"elsewhere\"", p.Writable, p.WriteReason)
	}

	rewriteMetadata(t, dir, "loom_db")
	if err := reg.RefreshProject(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if p := mustGet(t, reg, "p"); !p.Writable || p.WriteReason != "" {
		t.Fatalf("third refresh: Writable=%v WriteReason=%q, want true and empty", p.Writable, p.WriteReason)
	}
}

func TestWritableFailsOpenWhenBdCannotSay(t *testing.T) {
	cases := map[string]*bdexec.MockRunner{
		"bd fails": {Stderr: "Error: failed to open database: boom", Err: errors.New("exit status 1")},
		"bd omits the target": {
			Stdout: `{"backend":"dolt","embedded":false}`,
			Stderr: "Error: failed to open database: boom",
		},
	}
	for name, mock := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeWorkspace(t, withPort("3311"), withMetadata(metadataFor("bd_db")))
			cfg := &config.Config{Projects: []config.Project{{Name: "proj-x", Workdir: dir, Database: "loom_db"}}}
			var logs bytes.Buffer
			reg := checkedRegistry(t, cfg, mock, slog.New(slog.NewTextHandler(&logs, nil)))

			if err := reg.RefreshProject(context.Background(), "proj-x"); err != nil {
				t.Fatal(err)
			}
			if p := mustGet(t, reg, "proj-x"); !p.Writable || p.WriteReason != "" {
				t.Fatalf("Writable=%v WriteReason=%q, want true and empty", p.Writable, p.WriteReason)
			}
			var warns []string
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				if strings.Contains(line, "level=WARN") {
					warns = append(warns, line)
				}
			}
			if len(warns) != 1 {
				t.Fatalf("got %d warn lines, want 1:\n%s", len(warns), logs.String())
			}
			for _, want := range []string{"proj-x", "failed to open database: boom"} {
				if !strings.Contains(warns[0], want) {
					t.Errorf("warn line %q does not name %q", warns[0], want)
				}
			}
		})
	}
}

func TestWritableLocalServerComparesTheAddress(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		port     int
		writable bool
		names    []string
	}{
		{name: "another port", host: "127.0.0.1", port: 3312, names: []string{"127.0.0.1:3311", "127.0.0.1:3312"}},
		{name: "another host", host: "10.0.0.7", port: 3311, names: []string{"127.0.0.1:3311", "10.0.0.7:3311"}},
		{name: "loopback spelled differently", host: "localhost", port: 3311, writable: true},
		{name: "bd reports no port", host: "127.0.0.1", port: 0, writable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeWorkspace(t, withPort("3311"), withMetadata(metadataFor("same")))
			cfg := &config.Config{Projects: []config.Project{{Name: "p", Workdir: dir, Database: "same"}}}
			out, _ := json.Marshal(map[string]any{"database": "same", "embedded": false, "host": tc.host, "port": tc.port})
			reg := checkedRegistry(t, cfg, &bdexec.MockRunner{Stdout: string(out)}, nil)

			if err := reg.RefreshProject(context.Background(), "p"); err != nil {
				t.Fatal(err)
			}
			p := mustGet(t, reg, "p")
			if p.Writable != tc.writable {
				t.Fatalf("Writable=%v WriteReason=%q, want Writable=%v", p.Writable, p.WriteReason, tc.writable)
			}
			if tc.writable && p.WriteReason != "" {
				t.Fatalf("WriteReason = %q, want empty", p.WriteReason)
			}
			for _, want := range tc.names {
				if !strings.Contains(p.WriteReason, want) {
					t.Errorf("WriteReason = %q, want it to name %s", p.WriteReason, want)
				}
			}
		})
	}
}

// Operator ruling 2026-10-31: every loopback address (127.0.0.0/8, ::1,
// localhost) on the same port is one server.
func TestWritableLoopbackAddressesOnOnePortAreOneServer(t *testing.T) {
	cases := []struct {
		name     string
		loomHost string
		bdHost   string
		bdPort   int
		writable bool
	}{
		{name: "loom ::1, bd 127.0.0.1", loomHost: "::1", bdHost: "127.0.0.1", bdPort: 3311, writable: true},
		{name: "loom ::1, bd localhost", loomHost: "::1", bdHost: "localhost", bdPort: 3311, writable: true},
		{name: "loom localhost, bd ::1", loomHost: "localhost", bdHost: "::1", bdPort: 3311, writable: true},
		{name: "loom 127.0.0.1, bd 127.0.0.2", loomHost: "127.0.0.1", bdHost: "127.0.0.2", bdPort: 3311, writable: true},
		{name: "loom ::1, bd 127.0.0.1 on another port", loomHost: "::1", bdHost: "127.0.0.1", bdPort: 3312},
		{name: "loom ::1, bd non-loopback host", loomHost: "::1", bdHost: "10.0.0.7", bdPort: 3311},
		{name: "loom non-loopback host, bd 127.0.0.1", loomHost: "10.0.0.9", bdHost: "127.0.0.1", bdPort: 3311},
		{name: "two non-loopback hosts", loomHost: "10.0.0.9", bdHost: "10.0.0.7", bdPort: 3311},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeWorkspace(t, withMetadata(metadataFor("same")))
			cfg := &config.Config{
				Servers:  []config.Server{{Name: "s", Host: tc.loomHost, Port: 3311, Kind: config.KindRemote}},
				Projects: []config.Project{{Name: "p", Workdir: dir, Server: "s", Database: "same"}},
			}
			out, _ := json.Marshal(map[string]any{"database": "same", "embedded": false, "host": tc.bdHost, "port": tc.bdPort})
			reg := checkedRegistry(t, cfg, &bdexec.MockRunner{Stdout: string(out)}, nil)

			if err := reg.RefreshProject(context.Background(), "p"); err != nil {
				t.Fatal(err)
			}
			p := mustGet(t, reg, "p")
			if p.Writable != tc.writable {
				t.Fatalf("Writable=%v WriteReason=%q, want Writable=%v", p.Writable, p.WriteReason, tc.writable)
			}
			if tc.writable && p.WriteReason != "" {
				t.Fatalf("WriteReason = %q, want empty", p.WriteReason)
			}
			if !tc.writable {
				loom := net.JoinHostPort(tc.loomHost, "3311")
				bd := net.JoinHostPort(tc.bdHost, strconv.Itoa(tc.bdPort))
				for _, want := range []string{loom, bd} {
					if !strings.Contains(p.WriteReason, want) {
						t.Errorf("WriteReason = %q, want it to name %s", p.WriteReason, want)
					}
				}
			}
		})
	}
}
