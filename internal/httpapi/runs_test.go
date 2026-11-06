package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/integration/djinn"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/runs"
)

// runsFixture builds a server whose single project is a djinn project backed by
// the fake summoner script, served through the djinn integration.
type runsFixture struct {
	server   *Server
	project  runs.Project
	provider *djinn.Provider
	mgr      *runs.Manager
	store    *runs.Store
}

func newRunsServer(t *testing.T, withDjinn bool) runsFixture {
	t.Helper()
	return newRunsServerWith(t, withDjinn, djinn.Options{})
}

// newRunsServerWith is newRunsServer with provider options; Binary defaults to
// the fake summoner and StateDir to a temp dir.
func newRunsServerWith(t *testing.T, withDjinn bool, opts djinn.Options) runsFixture {
	t.Helper()

	workdir := t.TempDir()
	if withDjinn {
		for _, sub := range []string{".djinn", ".djinn/summoner-logs", ".djinn/state", ".djinn/evidence"} {
			if err := os.MkdirAll(filepath.Join(workdir, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	if opts.Binary == "" {
		fake, err := filepath.Abs(filepath.Join("..", "runs", "testdata", "bin", "fake-summoner"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(fake); err != nil {
			t.Fatalf("fake summoner fixture missing: %v", err)
		}
		opts.Binary = fake
	}
	if opts.StateDir == "" {
		opts.StateDir = t.TempDir()
	}
	if opts.StopGrace == 0 {
		opts.StopGrace = 500 * time.Millisecond
	}
	provider := djinn.New(opts)
	// Registered after the temp dirs, so it runs before they are removed: a
	// still-exiting fake summoner writing its run record would otherwise race
	// the cleanup.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		provider.Close(ctx)
	})

	cfg := &config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "djinn", Workdir: workdir}},
	}
	reg := registry.New(cfg, provider)
	t.Cleanup(func() { reg.Close() })
	reg.Refresh(context.Background())

	s := New(Options{Registry: reg, Integrations: integration.Set{provider}})
	return runsFixture{
		server:   s,
		project:  runs.Project{Name: "djinn", Workdir: workdir},
		provider: provider,
		mgr:      provider.Manager(),
		store:    provider.Store(),
	}
}

// --- guards ----------------------------------------------------------------

// TestRunEndpointsRequireDjinn: every run endpoint 404s without a .djinn dir.
func TestRunEndpointsRequireDjinn(t *testing.T) {
	f := newRunsServer(t, false)

	for _, tc := range djinnRouteCases("djinn") {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := do(t, f.server, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 for a non-djinn project", rec.Code)
			}
			var body errorResponse
			json.Unmarshal(rec.Body.Bytes(), &body)
			if !strings.Contains(body.Error, "djinn") {
				t.Errorf("error = %q, want it to explain the missing .djinn", body.Error)
			}
		})
	}
}

func TestRunEndpointsUnknownProject(t *testing.T) {
	f := newRunsServer(t, true)
	rec := do(t, f.server, http.MethodGet, "/api/projects/nope/x/djinn/runs", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestSummonerMissingIs503 is the specified behaviour when the binary is absent.
func TestSummonerMissingIs503(t *testing.T) {
	f := newRunsServerWith(t, true, djinn.Options{
		Binary:   "definitely-not-summoner",
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	s := f.server

	for _, path := range []string{
		"/api/projects/djinn/x/djinn/summon/preview",
		"/api/projects/djinn/x/djinn/summon",
		"/api/projects/djinn/x/djinn/summon/preview",
		"/api/projects/djinn/x/djinn/summon",
	} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, s, http.MethodPost, path, `{}`)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
			var body errorResponse
			json.Unmarshal(rec.Body.Bytes(), &body)
			if body.Error != "summoner not found in PATH" {
				t.Errorf("error = %q, want the specified message", body.Error)
			}
		})
	}
}

// TestPathTraversalRejected: bead and run ids reach the filesystem, so they are
// validated before anything is opened.
func TestPathTraversalRejected(t *testing.T) {
	f := newRunsServer(t, true)

	cases := []struct{ name, method, path, body string }{
		{"forensics traversal", http.MethodGet, "/api/projects/djinn/x/djinn/beads/..%2F..%2Fetc%2Fpasswd/forensics", ""},
		{"forensics dotdot", http.MethodGet, "/api/projects/djinn/x/djinn/beads/../forensics", ""},
		{"run log traversal", http.MethodGet, "/api/projects/djinn/x/djinn/runs/..%2F..%2Fsecrets/log", ""},
		{"stop traversal", http.MethodPost, "/api/projects/djinn/x/djinn/runs/..%2F..%2Fx/stop", ""},
		{"preview bead traversal", http.MethodPost, "/api/projects/djinn/x/djinn/summon/preview", `{"beads":["../../etc/passwd"]}`},
		{"summon bead traversal", http.MethodPost, "/api/projects/djinn/x/djinn/summon", `{"beads":["a/b"]}`},
		{"summon epic traversal", http.MethodPost, "/api/projects/djinn/x/djinn/summon", `{"epic":"../x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, f.server, tc.method, tc.path, tc.body)
			if rec.Code == http.StatusOK || rec.Code == http.StatusAccepted {
				t.Errorf("status = %d; a traversal attempt must be rejected (body: %s)",
					rec.Code, rec.Body.String())
			}
		})
	}
}

// --- preview ---------------------------------------------------------------

func TestSummonPreview(t *testing.T) {
	f := newRunsServer(t, true)
	t.Setenv("FAKE_SUMMONER_MODE", "dry-run")

	rec := do(t, f.server, http.MethodPost, "/api/projects/djinn/x/djinn/summon/preview", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	var got runs.SummonPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Ordered) != 2 {
		t.Errorf("ordered = %v", got.Ordered)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Bead != "djinn-nope" {
		t.Errorf("warnings = %+v", got.Warnings)
	}
	if got.NeedsOperator == nil {
		t.Error("needsOperator must serialize as [] not null")
	}
}

func TestSummonPreviewPreflight(t *testing.T) {
	f := newRunsServer(t, true)
	t.Setenv("FAKE_SUMMONER_MODE", "preflight")

	rec := do(t, f.server, http.MethodPost, "/api/projects/djinn/x/djinn/summon/preview", `{"preflight":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got runs.SummonPreview
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Preflight {
		t.Error("preflight flag not reported")
	}
	if len(got.NeedsOperator) == 0 {
		t.Error("preflight should have surfaced an operator-required bead")
	}
}

// --- summon / stop ---------------------------------------------------------

func TestSummonAndStop(t *testing.T) {
	f := newRunsServer(t, true)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	rec := do(t, f.server, http.MethodPost, "/api/projects/djinn/x/djinn/summon",
		`{"maxWorkers":2,"noPush":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	var started struct {
		RunID string   `json:"runId"`
		Run   runs.Run `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.RunID == "" {
		t.Fatal("no runId returned")
	}
	t.Cleanup(func() { _ = f.mgr.Stop("djinn", started.RunID) })

	if started.Run.Source != runs.SourceLoom {
		t.Errorf("source = %q", started.Run.Source)
	}

	// A second summon must be refused while one is active.
	rec = do(t, f.server, http.MethodPost, "/api/projects/djinn/x/djinn/summon", `{}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("second summon status = %d, want 409", rec.Code)
	}

	// The run appears in history as running.
	rec = do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("runs status = %d", rec.Code)
	}
	var list struct {
		Runs []runs.Run `json:"runs"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Runs) != 1 || list.Runs[0].ID != started.RunID {
		t.Fatalf("runs = %+v", list.Runs)
	}
	if list.Runs[0].State != runs.StateRunning {
		t.Errorf("state = %q, want running", list.Runs[0].State)
	}

	// Stop it.
	rec = do(t, f.server, http.MethodPost,
		"/api/projects/djinn/x/djinn/runs/"+started.RunID+"/stop", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("stop status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if saved, _ := f.store.Load("djinn", started.RunID); saved != nil && saved.State == runs.StateStopped {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	saved, _ := f.store.Load("djinn", started.RunID)
	t.Fatalf("run did not reach stopped; record = %+v", saved)
}

func TestStopUnknownRunIs404(t *testing.T) {
	f := newRunsServer(t, true)
	rec := do(t, f.server, http.MethodPost, "/api/projects/djinn/x/djinn/runs/run-nope/stop", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestSummonRejectsNegativeKnobs(t *testing.T) {
	f := newRunsServer(t, true)
	rec := do(t, f.server, http.MethodPost, "/api/projects/djinn/x/djinn/summon", `{"maxWorkers":-1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- history / detail ------------------------------------------------------

func TestRunListIncludesExternalRuns(t *testing.T) {
	f := newRunsServer(t, true)

	// Real lifecycle lines, written by nobody through Loom.
	body := "2026-06-24T00:42:33-07:00\tdjinn-gggc\tspawn\n" +
		"2026-06-24T00:55:50-07:00\tdjinn-gggc\texit\tCOMPLETE\tclosed\n"
	if err := os.WriteFile(
		filepath.Join(f.project.SummonerLogDir(), "2026-06-24.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var list struct {
		Runs []runs.Run `json:"runs"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Runs) != 1 {
		t.Fatalf("runs = %+v, want the external run", list.Runs)
	}
	r := list.Runs[0]
	if r.Source != runs.SourceExternal {
		t.Errorf("source = %q, want external", r.Source)
	}
	if len(r.Beads) != 1 || r.Beads[0].Verdict != runs.VerdictComplete {
		t.Errorf("beads = %+v", r.Beads)
	}

	// And its detail is fetchable.
	rec = do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs/"+r.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var detail runs.RunDetail
	json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.ID != r.ID {
		t.Errorf("detail id = %q", detail.ID)
	}
	if detail.ActiveSlots == nil {
		t.Error("activeSlots must serialize as [] not null")
	}
	if detail.LogPath != "" {
		t.Error("LogPath is a server-side path and must not be exposed")
	}
}

func TestRunDetailUnknownIs404(t *testing.T) {
	f := newRunsServer(t, true)
	rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs/run-nope", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- log -------------------------------------------------------------------

func TestRunLogChunking(t *testing.T) {
	f := newRunsServer(t, true)

	runID := "run-12345"
	dir, err := f.store.RunDir("djinn", runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "\x1b[32mcoloured line\x1b[0m\nplain line\n"
	if err := os.WriteFile(filepath.Join(dir, "console.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("stripped by default", func(t *testing.T) {
		rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs/"+runID+"/log", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var got struct {
			Text   string `json:"text"`
			Offset int64  `json:"offset"`
			EOF    bool   `json:"eof"`
		}
		json.Unmarshal(rec.Body.Bytes(), &got)
		if strings.Contains(got.Text, "\x1b[") {
			t.Errorf("ANSI leaked into the default response: %q", got.Text)
		}
		if !got.EOF || got.Offset != int64(len(body)) {
			t.Errorf("offset = %d eof = %v", got.Offset, got.EOF)
		}
	})

	t.Run("ansi=1 returns raw", func(t *testing.T) {
		rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs/"+runID+"/log?ansi=1", "")
		var got struct {
			Text string `json:"text"`
		}
		json.Unmarshal(rec.Body.Bytes(), &got)
		if !strings.Contains(got.Text, "\x1b[32m") {
			t.Errorf("ansi=1 should return raw bytes: %q", got.Text)
		}
	})

	t.Run("offset paging", func(t *testing.T) {
		rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs/"+runID+"/log?offset=5", "")
		var got struct {
			Offset int64 `json:"offset"`
		}
		json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Offset != int64(len(body)) {
			t.Errorf("offset = %d, want %d", got.Offset, len(body))
		}
	})

	t.Run("bad offset is 400", func(t *testing.T) {
		rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/runs/"+runID+"/log?offset=abc", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("external run has no capture", func(t *testing.T) {
		rec := do(t, f.server, http.MethodGet,
			"/api/projects/djinn/x/djinn/runs/ext-20260624T074233Z/log", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
		var body errorResponse
		json.Unmarshal(rec.Body.Bytes(), &body)
		if !strings.Contains(body.Error, "external") {
			t.Errorf("error = %q, want it to explain why", body.Error)
		}
	})
}

// --- forensics -------------------------------------------------------------

func TestForensicsEndpoint(t *testing.T) {
	f := newRunsServer(t, true)

	// A state dir shaped like a real one: JSON verdict plus a plain-text one.
	base := filepath.Join(f.project.StateDir(), "djinn-abc")
	steps := []struct{ dir, file, content string }{
		{"ac-quality-loop.iter-1", "ac-quality-review", `{"verdict":"CONCRETE","criteria_count":4}`},
		{"impl-loop.iter-1", "verify-impl", `{"complete":true,"verdict":"APPROVE","criteria":"4/4 pass"}`},
		{"review-impl-loop.iter-1", "review-impl", "overall: SHIP-WITH-FIXES (0 BLOCKER, 1 IMPORTANT fix, 10 NIT)"},
	}
	for _, st := range steps {
		d := filepath.Join(base, st.dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, st.file), []byte(st.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "claim.sha"), []byte("abc123\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/beads/djinn-abc/forensics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	var got runs.Forensics
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ClaimSHA != "abc123" {
		t.Errorf("claimSha = %q", got.ClaimSHA)
	}
	if len(got.Timeline) != 3 {
		t.Fatalf("timeline = %+v, want 3 steps", got.Timeline)
	}
	// Formula order.
	wantLoops := []string{"ac-quality-loop", "impl-loop", "review-impl-loop"}
	for i, want := range wantLoops {
		if got.Timeline[i].Loop != want {
			t.Errorf("timeline[%d] = %q, want %q", i, got.Timeline[i].Loop, want)
		}
	}
	// Both verdict shapes.
	var sawJSON, sawText bool
	for _, v := range got.Verdicts {
		if v.JSON != nil {
			sawJSON = true
		}
		if v.Text != "" {
			sawText = true
		}
	}
	if !sawJSON || !sawText {
		t.Errorf("verdicts = %+v, want both JSON and text", got.Verdicts)
	}
}

func TestForensicsUnknownBeadIsEmpty(t *testing.T) {
	f := newRunsServer(t, true)
	rec := do(t, f.server, http.MethodGet, "/api/projects/djinn/x/djinn/beads/djinn-never/forensics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with an empty record", rec.Code)
	}
	var got runs.Forensics
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Timeline) != 0 {
		t.Errorf("timeline = %+v, want empty", got.Timeline)
	}
	if got.Timeline == nil || got.Verdicts == nil {
		t.Error("slices must serialize as [] not null")
	}
}

// --- no djinn provider -----------------------------------------------------

func TestRunRoutesAbsentWithoutDjinnProvider(t *testing.T) {
	s := newTestServer(t) // Options.Integrations is empty
	for _, tc := range djinnRouteCases("loom") {
		rec := do(t, s, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s returned %d without the djinn provider, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

// TestN3NoPushOverridesProjectDefault covers review finding N3: with a plain
// bool, a project configured `no_push: true` could never launch a pushing run,
// because an explicit `false` was indistinguishable from an absent field.
func TestN3NoPushOverridesProjectDefault(t *testing.T) {
	tests := []struct {
		name        string
		projectDflt bool
		body        string
		wantNoPush  bool
	}{
		{"absent with default true inherits", true, `{}`, true},
		{"absent with default false inherits", false, `{}`, false},
		{"explicit false overrides a true default", true, `{"noPush":false}`, false},
		{"explicit true with a false default", false, `{"noPush":true}`, true},
		{"explicit true with a true default", true, `{"noPush":true}`, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunsServerWith(t, true, djinn.Options{
				Settings: map[string]*config.Summoner{"djinn": {NoPush: tc.projectDflt}},
			})
			t.Setenv("FAKE_SUMMONER_MODE", "long")

			rec := do(t, f.server, http.MethodPost, "/api/projects/djinn/x/djinn/summon", tc.body)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
			}
			var started struct {
				RunID string   `json:"runId"`
				Run   runs.Run `json:"run"`
			}
			json.Unmarshal(rec.Body.Bytes(), &started)
			t.Cleanup(func() { _ = f.mgr.Stop("djinn", started.RunID) })

			var sawFlag bool
			for _, a := range started.Run.Args {
				if a == "-no-push" {
					sawFlag = true
				}
			}
			if sawFlag != tc.wantNoPush {
				t.Errorf("argv = %v, -no-push present = %v, want %v",
					started.Run.Args, sawFlag, tc.wantNoPush)
			}
		})
	}
}
