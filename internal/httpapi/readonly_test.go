package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
)

// newSplitServer builds a server over two projects: "ro", whose database:
// override names a database bd does not write to, and "rw", whose override
// matches bd. The registry's write check and the write handlers use separate
// mocks, so writes records only what a handler sent to bd.
func newSplitServer(t *testing.T) (*Server, *bdexec.MockRunner) {
	t.Helper()
	cfg := &config.Config{
		Listen: config.DefaultListen,
		Projects: []config.Project{
			{Name: "ro", Workdir: t.TempDir(), Database: "loom_db"},
			{Name: "rw", Workdir: t.TempDir(), Database: "bd_db"},
		},
	}
	reg := registry.New(cfg)
	t.Cleanup(func() { reg.Close() })

	info := bdexec.NewMockRunner()
	info.Stdout = `{"backend":"dolt","database":"bd_db","embedded":false,"host":"127.0.0.1","port":3311}`
	infoClient := bdexec.New(info)
	reg.SetWriteCheck(func(ctx context.Context, workdir string) (registry.BdTarget, error) {
		t, err := infoClient.Info(ctx, workdir)
		return registry.BdTarget(t), err
	}, nil)
	reg.Refresh(context.Background())

	writes := bdexec.NewMockRunner()
	writes.Stdout = "loom-new\n"
	return New(Options{Registry: reg, BD: bdexec.New(writes), BDVersion: "bd version 1.3.2 (test)"}), writes
}

// writeRouteCases is every mutating route the router serves under a project,
// with a request a writable project would accept.
var writeRouteCases = map[string]struct{ path, body string }{
	"POST /api/projects/{project}/issues":                                    {"/issues", `{"title":"new"}`},
	"POST /api/projects/{project}/issues/bulk":                               {"/issues/bulk", `{"ids":["ro-1"],"patch":{"priority":1}}`},
	"POST /api/projects/{project}/issues/close":                              {"/issues/close", `{"ids":["ro-1"],"reason":"done"}`},
	"PATCH /api/projects/{project}/issues/{id}":                              {"/issues/ro-1", `{"labelsAdd":["a"],"labelsRemove":["b"]}`},
	"POST /api/projects/{project}/issues/{id}/close":                         {"/issues/ro-1/close", `{"reason":"done"}`},
	"POST /api/projects/{project}/issues/{id}/reopen":                        {"/issues/ro-1/reopen", `{}`},
	"POST /api/projects/{project}/issues/{id}/comments":                      {"/issues/ro-1/comments", `{"text":"hi"}`},
	"POST /api/projects/{project}/deps":                                      {"/deps", `{"from":"ro-1","to":"ro-2"}`},
	"DELETE /api/projects/{project}/deps":                                    {"/deps", `{"from":"ro-1","to":"ro-2"}`},
	"POST /api/projects/{project}/dolt/push":                                 {"/dolt/push", ``},
	"POST /api/projects/{project}/dolt/pull":                                 {"/dolt/pull", ``},
	"POST /api/projects/{project}/sweep/findings/{fingerprint}/apply":        {"/sweep/findings/abc123/apply", `{}`},
	"POST /api/projects/{project}/sweep/findings/{fingerprint}/dismiss":      {"/sweep/findings/abc123/dismiss", `{}`},
	"DELETE /api/projects/{project}/sweep/findings/{fingerprint}/dismiss":    {"/sweep/findings/abc123/dismiss", ``},
	"POST /api/projects/{project}/initiatives/{epic}/updates/{week}/publish": {"/initiatives/ro-1/updates/2026-W44/publish", ``},
}

// TestEveryWriteRouteRefusesAReadOnlyProject walks the router, so a write
// route added later without a case here fails rather than escaping the gate.
func TestEveryWriteRouteRefusesAReadOnlyProject(t *testing.T) {
	s, writes := newSplitServer(t)

	ro, err := s.reg.Get("ro")
	if err != nil {
		t.Fatal(err)
	}
	if ro.Writable || ro.WriteReason == "" {
		t.Fatalf("fixture: ro Writable=%v WriteReason=%q, want a read-only project", ro.Writable, ro.WriteReason)
	}

	var mutating []string
	err = chi.Walk(s.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if isWriteMethod(method) && strings.HasPrefix(route, "/api/projects/{project}/") {
			mutating = append(mutating, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(mutating)
	for _, key := range []string{
		"POST /api/projects/{project}/issues",
		"PATCH /api/projects/{project}/issues/{id}",
		"POST /api/projects/{project}/issues/bulk",
		"POST /api/projects/{project}/issues/{id}/close",
		"POST /api/projects/{project}/issues/close",
		"POST /api/projects/{project}/issues/{id}/comments",
		"DELETE /api/projects/{project}/deps",
	} {
		if !contains(mutating, key) {
			t.Fatalf("router has no %s; walked %v", key, mutating)
		}
	}

	for _, key := range mutating {
		tc, ok := writeRouteCases[key]
		if !ok {
			t.Errorf("%s has no case in writeRouteCases: add one so the read-only gate covers it", key)
			continue
		}
		method := strings.SplitN(key, " ", 2)[0]
		t.Run(key, func(t *testing.T) {
			writes.Reset()
			rec := do(t, s, method, "/api/projects/ro"+tc.path, tc.body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body)
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %s: %v", rec.Body, err)
			}
			if body.Error != ro.WriteReason {
				t.Fatalf("error = %q, want the WriteReason %q", body.Error, ro.WriteReason)
			}
			if calls := writes.Calls(); len(calls) != 0 {
				t.Fatalf("bd ran %d times for a read-only project: %v", len(calls), calls)
			}
		})
	}
}

func TestAWritableProjectStillReachesBd(t *testing.T) {
	s, writes := newSplitServer(t)
	rec := do(t, s, http.MethodPatch, "/api/projects/rw/issues/rw-1", `{"labelsAdd":["a"]}`)
	if rec.Code == http.StatusConflict {
		t.Fatalf("status = 409 for a writable project: %s", rec.Body)
	}
	if n := len(writes.Calls()); n != 1 {
		t.Fatalf("bd ran %d times, want 1", n)
	}
}

func TestReadOnlyProjectStillServesBdReads(t *testing.T) {
	s, writes := newSplitServer(t)
	writes.Stdout = `{}`
	rec := do(t, s, http.MethodGet, "/api/projects/ro/initiative-updates", "")
	if rec.Code == http.StatusConflict {
		t.Fatalf("a read on a read-only project got 409: %s", rec.Body)
	}
}

func TestProjectsCarryWritableAndWriteReason(t *testing.T) {
	s, _ := newSplitServer(t)
	rec := do(t, s, http.MethodGet, "/api/projects", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		writable bool
		reason   bool
	}{"ro": {false, true}, "rw": {true, false}}
	if len(got) != len(want) {
		t.Fatalf("got %d projects, want %d", len(got), len(want))
	}
	for _, p := range got {
		var name, reason string
		var writable bool
		for key, dst := range map[string]any{"name": &name, "writable": &writable, "writeReason": &reason} {
			raw, ok := p[key]
			if !ok {
				t.Fatalf("project JSON has no %q key: %v", key, p)
			}
			if err := json.Unmarshal(raw, dst); err != nil {
				t.Fatalf("%s: %v", key, err)
			}
		}
		w := want[name]
		if writable != w.writable || (reason != "") != w.reason {
			t.Errorf("%s: writable=%v writeReason=%q, want writable=%v and reason present=%v", name, writable, reason, w.writable, w.reason)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
