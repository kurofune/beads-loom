package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/integration/djinn"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/runs"
)

type routeCase struct{ method, path, body string }

// djinnRunRoutes are the run routes relative to a prefix: the djinn mount
// today, and the project root before the integration seam.
func djinnRunRoutes(prefix string) []routeCase {
	return []routeCase{
		{http.MethodPost, prefix + "/summon/preview", `{}`},
		{http.MethodPost, prefix + "/summon", `{}`},
		{http.MethodGet, prefix + "/runs", ""},
		{http.MethodGet, prefix + "/runs/run-1", ""},
		{http.MethodGet, prefix + "/runs/run-1/log", ""},
		{http.MethodPost, prefix + "/runs/run-1/stop", ""},
		{http.MethodGet, prefix + "/beads/djinn-a/forensics", ""},
	}
}

// djinnRouteCases is every djinn route, at its /x/djinn/ path.
func djinnRouteCases(project string) []routeCase {
	prefix := "/api/projects/" + project + "/x/djinn"
	return append(djinnRunRoutes(prefix),
		routeCase{http.MethodGet, prefix + "/beads/djinn-a/evidence", ""},
		routeCase{http.MethodGet, prefix + "/spend", ""},
	)
}

// preSeamRunRoutes are the paths the removed legacy mount registered directly
// under /api/projects/{project}/ before the djinn routes moved to /x/djinn/.
func preSeamRunRoutes(project string) []routeCase {
	return djinnRunRoutes("/api/projects/" + project)
}

// fakeProvider is a second integration, proving the seam takes more than one.
type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }

func (fakeProvider) Capabilities(string) integration.Capabilities {
	return integration.Capabilities{Present: true, Capabilities: []string{"ping"}}
}

func (fakeProvider) Mount(r chi.Router, _ integration.Deps) {
	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		integration.WriteJSON(w, http.StatusOK, map[string]string{"project": chi.URLParam(r, "project")})
	})
	r.Post("/poke", func(w http.ResponseWriter, _ *http.Request) {
		integration.WriteJSON(w, http.StatusOK, map[string]bool{"poked": true})
	})
}

func (fakeProvider) EventNames() []string { return []string{"fake-event"} }

var _ integration.Provider = fakeProvider{}

// integrationServer builds a server over two projects, "with" (has .djinn) and
// "without", serving the given providers. It mirrors main: the registry detects
// with exactly the providers that are mounted. It sets no listen address, so
// the Host check is off; the CSRF test passes one to integrationServerOn.
func integrationServer(t *testing.T, providers integration.Set, bd *bdexec.Client) (*Server, string) {
	t.Helper()
	return integrationServerOn(t, providers, bd, "")
}

func integrationServerOn(t *testing.T, providers integration.Set, bd *bdexec.Client, listen string) (*Server, string) {
	t.Helper()
	with := t.TempDir()
	if err := os.MkdirAll(filepath.Join(with, ".djinn", "evidence"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Listen: listen,
		Projects: []config.Project{
			{Name: "with", Workdir: with},
			{Name: "without", Workdir: t.TempDir()},
		},
	}
	reg := registry.New(cfg, providers.Detectors()...)
	t.Cleanup(func() { reg.Close() })
	reg.Refresh(context.Background())
	return New(Options{Registry: reg, BD: bd, Listen: cfg.Listen, Integrations: providers}), with
}

func newDjinn(t *testing.T, summonerOnPath bool) *djinn.Provider {
	t.Helper()
	lookPath := func(string) (string, error) { return "", os.ErrNotExist }
	if summonerOnPath {
		lookPath = func(string) (string, error) { return "/usr/local/bin/summoner", nil }
	}
	return djinn.New(djinn.Options{StateDir: t.TempDir(), LookPath: lookPath})
}

func decodeProjects(t *testing.T, s *Server) map[string]map[string]any {
	t.Helper()
	rec := do(t, s, http.MethodGet, "/api/projects", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]map[string]any, len(list))
	for _, p := range list {
		out[p["name"].(string)] = p
	}
	return out
}

func integrationsOf(t *testing.T, p map[string]any) map[string]any {
	t.Helper()
	raw, ok := p["integrations"]
	if !ok {
		t.Fatalf("project %v has no integrations key", p["name"])
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("integrations = %#v, want an object", raw)
	}
	return m
}

// projectKeys is the whole wire shape of one GET /api/projects entry. The
// deprecated djinn alias boolean is gone: integrations.djinn replaced it, and
// any key outside this set fails the test.
var projectKeys = []string{
	"db", "health", "integrations", "name", "reachable", "reason",
	"source", "workdir", "writable", "writeReason",
}

func assertProjectKeys(t *testing.T, name string, p map[string]any) {
	t.Helper()
	got := make([]string, 0, len(p))
	for k := range p {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, projectKeys) {
		t.Errorf("%s: project keys = %v, want exactly %v", name, got, projectKeys)
	}
}

func capabilitiesOf(t *testing.T, integ any) []string {
	t.Helper()
	obj, ok := integ.(map[string]any)
	if !ok {
		t.Fatalf("integration = %#v, want an object", integ)
	}
	raw, ok := obj["capabilities"].([]any)
	if !ok {
		t.Fatalf("capabilities = %#v, want an array", obj["capabilities"])
	}
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		out = append(out, c.(string))
	}
	sort.Strings(out)
	return out
}

func TestProjectsCarryIntegrationsAndNoDeprecatedAlias(t *testing.T) {
	t.Run("summoner on PATH", func(t *testing.T) {
		s, _ := integrationServer(t, integration.Set{newDjinn(t, true)}, nil)
		projects := decodeProjects(t, s)

		with := integrationsOf(t, projects["with"])
		if got := capabilitiesOf(t, with["djinn"]); !reflect.DeepEqual(got, []string{"dispatch", "evidence", "runs"}) {
			t.Errorf("with.integrations.djinn.capabilities = %v, want dispatch, evidence, runs", got)
		}
		if _, ok := with["djinn"].(map[string]any)["reasons"]; ok {
			t.Errorf("reasons present with every capability working: %#v", with["djinn"])
		}
		if len(integrationsOf(t, projects["without"])) != 0 {
			t.Errorf("without.integrations = %#v, want {}", projects["without"]["integrations"])
		}
		for _, name := range []string{"with", "without"} {
			p, ok := projects[name]
			if !ok {
				t.Fatalf("GET /api/projects has no project %q", name)
			}
			assertProjectKeys(t, name, p)
		}
	})

	t.Run("summoner missing", func(t *testing.T) {
		s, _ := integrationServer(t, integration.Set{newDjinn(t, false)}, nil)
		with := integrationsOf(t, decodeProjects(t, s)["with"])
		if got := capabilitiesOf(t, with["djinn"]); !reflect.DeepEqual(got, []string{"evidence", "runs"}) {
			t.Errorf("capabilities = %v, want exactly evidence, runs", got)
		}
		reasons, _ := with["djinn"].(map[string]any)["reasons"].(map[string]any)
		if reason, _ := reasons["dispatch"].(string); reason == "" {
			t.Errorf("reasons = %#v, want reasons.dispatch to say why", reasons)
		}
	})
}

// TestDisabledDjinnHasNoRoutesAndNoIntegrationsKey is integrations.djinn.enabled:
// false. main builds no djinn provider then (TestDisabledDjinnBuildsNoProvider),
// so the server and registry get a set without it.
func TestDisabledDjinnHasNoRoutesAndNoIntegrationsKey(t *testing.T) {
	for _, set := range []integration.Set{{}, {fakeProvider{}}} {
		s, _ := integrationServer(t, set, nil)
		for _, tc := range djinnRouteCases("with") {
			rec := do(t, s, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("providers %d: %s %s = %d, want 404 with djinn disabled", len(set), tc.method, tc.path, rec.Code)
			}
		}
		p := decodeProjects(t, s)["with"]
		if _, ok := integrationsOf(t, p)["djinn"]; ok {
			t.Errorf("integrations = %#v, want no djinn key with djinn disabled", p["integrations"])
		}
		assertProjectKeys(t, "with", p)
	}
}

// TestPreSeamRunPathsAre404 pins the removal of the legacy mount: on a djinn
// project every pre-seam run path is unrouted, and the same request under
// /x/djinn/ answers what the pre-seam path answered before (summoner is off
// PATH, so neither summon route launches anything).
func TestPreSeamRunPathsAre404(t *testing.T) {
	s, _ := integrationServer(t, integration.Set{newDjinn(t, false)}, nil)
	want := map[string]struct {
		code int
		body string
	}{
		"POST /summon/preview":         {http.StatusServiceUnavailable, `{"error":"summoner not found in PATH"}`},
		"POST /summon":                 {http.StatusServiceUnavailable, `{"error":"summoner not found in PATH"}`},
		"GET /runs":                    {http.StatusOK, `{"runs":[]}`},
		"GET /runs/run-1":              {http.StatusNotFound, `{"error":"run not found"}`},
		"GET /runs/run-1/log":          {http.StatusNotFound, `{"error":"no console capture for run run-1 (external runs are not captured by Loom)"}`},
		"POST /runs/run-1/stop":        {http.StatusNotFound, `{"error":"no active managed run run-1 for project with"}`},
		"GET /beads/djinn-a/forensics": {http.StatusOK, `{"bead":"djinn-a","timeline":[],"verdicts":[]}`},
	}
	legacy := preSeamRunRoutes("with")
	mounted := djinnRunRoutes("/api/projects/with/x/djinn")
	if len(legacy) != len(want) || len(mounted) != len(want) {
		t.Fatalf("route tables have %d pre-seam and %d mounted rows, want %d each", len(legacy), len(mounted), len(want))
	}
	for i, old := range legacy {
		key := old.method + " " + strings.TrimPrefix(old.path, "/api/projects/with")
		w, ok := want[key]
		if !ok {
			t.Fatalf("no expected answer for %s", key)
		}
		t.Run(key, func(t *testing.T) {
			now := do(t, s, mounted[i].method, mounted[i].path, mounted[i].body)
			if got := strings.TrimSpace(now.Body.String()); now.Code != w.code || got != w.body {
				t.Errorf("%s %s = %d %s, want %d %s", mounted[i].method, mounted[i].path, now.Code, got, w.code, w.body)
			}
			rec := do(t, s, old.method, old.path, old.body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404: the pre-seam path is gone", old.method, old.path, rec.Code)
			}
			if strings.TrimSpace(rec.Body.String()) == w.body {
				t.Errorf("%s %s answered the djinn handler's body %s; the route is still mounted", old.method, old.path, w.body)
			}
		})
	}
}

func TestFakeProviderMountsBesideDjinn(t *testing.T) {
	both, _ := integrationServer(t, integration.Set{newDjinn(t, true), fakeProvider{}}, nil)
	onlyDjinn, _ := integrationServer(t, integration.Set{newDjinn(t, true)}, nil)

	rec := do(t, both, http.MethodGet, "/api/projects/with/x/fake/ping", "")
	if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("GET /x/fake/ping = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := do(t, both, http.MethodPost, "/api/projects/with/x/fake/poke", ""); rec.Code != http.StatusOK {
		t.Errorf("POST /x/fake/poke = %d, want 200", rec.Code)
	}
	if got := integrationsOf(t, decodeProjects(t, both)["without"]); len(got) != 1 || got["fake"] == nil {
		t.Errorf("without.integrations = %#v, want only fake", got)
	}

	for _, path := range []string{
		"/api/projects/with/x/djinn/runs",
		"/api/projects/with/x/djinn/spend",
		"/api/projects/with/x/djinn/beads/djinn-a/evidence",
		"/api/projects/without/x/djinn/runs",
	} {
		a := do(t, both, http.MethodGet, path, "")
		b := do(t, onlyDjinn, http.MethodGet, path, "")
		if a.Code != b.Code || a.Body.String() != b.Body.String() {
			t.Errorf("%s: with fake = %d %s, djinn alone = %d %s; adding a provider changed djinn's routes",
				path, a.Code, a.Body.String(), b.Code, b.Body.String())
		}
	}
}

func TestEvidenceEndpoint(t *testing.T) {
	s, with := integrationServer(t, integration.Set{newDjinn(t, true)}, nil)
	body := `bead: djinn-abc
title: "[Djinn] something"
criteria:
  - text: "it works"
    status: pass
    implementation:
      - internal/x/y.go:42
    verification: "go test ./..."
follow_up:
  - djinn-next
`
	if err := os.WriteFile(filepath.Join(with, ".djinn", "evidence", "djinn-abc.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("present", func(t *testing.T) {
		rec := do(t, s, http.MethodGet, "/api/projects/with/x/djinn/beads/djinn-abc/evidence", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		var got struct {
			Evidence *runs.Evidence `json:"evidence"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Evidence == nil || got.Evidence.Bead != "djinn-abc" ||
			len(got.Evidence.Criteria) != 1 || got.Evidence.Criteria[0].Status != runs.CriterionPass ||
			!reflect.DeepEqual(got.Evidence.FollowUp, []string{"djinn-next"}) {
			t.Errorf("evidence = %+v, want the parsed file", got.Evidence)
		}
	})

	t.Run("null", func(t *testing.T) {
		rec := do(t, s, http.MethodGet, "/api/projects/with/x/djinn/beads/djinn-none/evidence", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if v, ok := got["evidence"]; !ok || v != nil {
			t.Errorf("body = %s, want {\"evidence\": null}", rec.Body.String())
		}
	})

	t.Run("unsafe id", func(t *testing.T) {
		rec := do(t, s, http.MethodGet, "/api/projects/with/x/djinn/beads/..%2F..%2Fetc%2Fpasswd/evidence", "")
		if rec.Code == http.StatusOK {
			t.Errorf("status = 200 for a traversal id (body %s)", rec.Body.String())
		}
	})

	t.Run("404 without .djinn or with djinn disabled", func(t *testing.T) {
		disabled, _ := integrationServer(t, integration.Set{}, nil)
		for _, tc := range []struct {
			s    *Server
			path string
		}{
			{s, "/api/projects/without/x/djinn/beads/djinn-abc/evidence"},
			{disabled, "/api/projects/with/x/djinn/beads/djinn-abc/evidence"},
		} {
			if rec := do(t, tc.s, http.MethodGet, tc.path, ""); rec.Code != http.StatusNotFound {
				t.Errorf("%s = %d, want 404", tc.path, rec.Code)
			}
		}
	})
}

func TestSpendEndpoint(t *testing.T) {
	provider := newDjinn(t, true)
	s, _ := integrationServer(t, integration.Set{provider}, nil)

	t.Run("null with no run history", func(t *testing.T) {
		rec := do(t, s, http.MethodGet, "/api/projects/with/x/djinn/spend", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if v, ok := got["last7dUsd"]; !ok || v != nil {
			t.Errorf("body = %s, want {\"last7dUsd\": null}", rec.Body.String())
		}
	})

	t.Run("present", func(t *testing.T) {
		end := time.Now().UTC().Add(-time.Hour)
		if err := provider.Store().Save(runs.Run{
			ID: "run-1", Project: "with", Source: runs.SourceLoom, State: runs.StateFinished,
			StartedAt: end.Add(-time.Hour), EndedAt: &end, CostSoFar: 2.5,
		}); err != nil {
			t.Fatal(err)
		}
		rec := do(t, s, http.MethodGet, "/api/projects/with/x/djinn/spend", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["last7dUsd"] != 2.5 {
			t.Errorf("body = %s, want last7dUsd 2.5", rec.Body.String())
		}
	})

	t.Run("404 without .djinn or with djinn disabled", func(t *testing.T) {
		disabled, _ := integrationServer(t, integration.Set{}, nil)
		if rec := do(t, s, http.MethodGet, "/api/projects/without/x/djinn/spend", ""); rec.Code != http.StatusNotFound {
			t.Errorf("without .djinn = %d, want 404", rec.Code)
		}
		if rec := do(t, disabled, http.MethodGet, "/api/projects/with/x/djinn/spend", ""); rec.Code != http.StatusNotFound {
			t.Errorf("disabled = %d, want 404", rec.Code)
		}
	})
}

// TestIntegrationRoutesShareTheCSRFGuard: a provider's write routes sit inside
// the same middleware chain as core's, so a foreign Origin gets the same
// rejection.
func TestIntegrationRoutesShareTheCSRFGuard(t *testing.T) {
	mock := bdexec.NewMockRunner()
	s, _ := integrationServerOn(t, integration.Set{newDjinn(t, true), fakeProvider{}}, bdexec.New(mock), "127.0.0.1:7333")

	post := func(path string) int {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, request(http.MethodPost, path, `{}`, "application/json",
			"https://evil.example", "127.0.0.1:7333"))
		return rec.Code
	}
	core := post("/api/projects/with/issues/with-1/close")
	if core != http.StatusForbidden {
		t.Fatalf("core write = %d, want 403 for a foreign origin", core)
	}
	for _, path := range []string{
		"/api/projects/with/x/djinn/summon",
		"/api/projects/with/x/djinn/summon/preview",
		"/api/projects/with/x/fake/poke",
	} {
		if got := post(path); got != core {
			t.Errorf("POST %s with a foreign origin = %d, want %d like the core write routes", path, got, core)
		}
	}
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("a rejected write reached bd: %v", calls)
	}
}

// TestIssueDetailAndStatsCarryNoDjinnFields reads the live seeded database: the
// evidence and spend fields moved to /x/djinn/ and must not come back on the
// core shapes.
func TestIssueDetailAndStatsCarryNoDjinnFields(t *testing.T) {
	s := newLiveHTTPServer(t)

	rec := do(t, s, http.MethodGet, "/api/projects/loom/issues?includeClosed=true&limit=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("issues status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Issues []struct {
			ID string `json:"id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Issues) == 0 {
		t.Fatal("the live backlog has no issues to read")
	}

	for path, gone := range map[string]string{
		"/api/projects/loom/issues/" + list.Issues[0].ID: "evidence",
		"/api/projects/loom/stats":                       "spendRecent",
	} {
		rec := do(t, s, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d (body %s)", path, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body[gone]; ok {
			t.Errorf("%s carries %q; it moved to /x/djinn/", path, gone)
		}
	}
}
