package runs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingHub captures published events.
type recordingHub struct {
	mu     sync.Mutex
	events []hubEvent
}

type hubEvent struct {
	Type    string
	Payload any
}

func (h *recordingHub) Publish(typ string, payload any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, hubEvent{typ, payload})
}

func (h *recordingHub) types() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.events))
	for _, e := range h.events {
		out = append(out, e.Type)
	}
	return out
}

func (h *recordingHub) count(typ string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func (h *recordingHub) waitFor(t *testing.T, typ string, timeout time.Duration) hubEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		for _, e := range h.events {
			if e.Type == typ {
				h.mu.Unlock()
				return e
			}
		}
		h.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a %s event; saw %v", typ, h.types())
	return hubEvent{}
}

// waitForConsole blocks until the run's console capture contains marker, so a
// test never signals a child that has not finished starting up.
func waitForConsole(t *testing.T, m *Manager, project, runID, marker string, timeout time.Duration) {
	t.Helper()
	path, err := m.Store().ConsolePath(project, runID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), marker) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in the console capture", marker)
}

// fakeSummoner returns the absolute path to the test's stand-in binary.
func fakeSummoner(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "bin", "fake-summoner"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fake summoner fixture missing: %v", err)
	}
	return p
}

// djinnProject creates a workdir that looks like a djinn project.
func djinnProject(t *testing.T, name string) Project {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{".djinn", ".djinn/summoner-logs", ".djinn/state", ".djinn/evidence"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Project{Name: name, Workdir: dir}
}

// newTestManager registers a Shutdown cleanup after its temp dirs, so it runs
// before they are removed: a run still saving its final record would otherwise
// race the RemoveAll. Call it after djinnProject for the same reason.
func newTestManager(t *testing.T, hub Publisher) *Manager {
	t.Helper()
	m := NewManager(ManagerOptions{
		Store:     NewStore(t.TempDir()),
		Hub:       hub,
		Binary:    fakeSummoner(t),
		StopGrace: 500 * time.Millisecond,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.Shutdown(ctx, nil)
	})
	return m
}

// --- request rendering -----------------------------------------------------

func TestSummonRequestArgs(t *testing.T) {
	tests := []struct {
		name string
		req  SummonRequest
		want []string
	}{
		{name: "empty", req: SummonRequest{}, want: nil},
		{
			name: "single bead",
			req:  SummonRequest{Beads: []string{"djinn-a"}},
			want: []string{"-bead", "djinn-a"},
		},
		{
			name: "repeated beads",
			req:  SummonRequest{Beads: []string{"djinn-a", "djinn-b"}},
			want: []string{"-bead", "djinn-a", "-bead", "djinn-b"},
		},
		{
			name: "epic",
			req:  SummonRequest{Epic: "djinn-ccyz"},
			want: []string{"-epic", "djinn-ccyz"},
		},
		{
			name: "bead and epic compose as an intersection",
			req:  SummonRequest{Beads: []string{"djinn-a"}, Epic: "djinn-ccyz"},
			want: []string{"-bead", "djinn-a", "-epic", "djinn-ccyz"},
		},
		{
			name: "knobs",
			req:  SummonRequest{MaxWorkers: 5, MaxBeads: 3, NoPush: true},
			want: []string{"-max-workers", "5", "-max-beads", "3", "-no-push"},
		},
		{
			name: "zero knobs are omitted so summoner's own defaults apply",
			req:  SummonRequest{MaxWorkers: 0, MaxBeads: 0, NoPush: false},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.req.Args(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Args() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSummonRequestPreviewArgs(t *testing.T) {
	req := SummonRequest{Beads: []string{"djinn-a"}}

	dry := req.PreviewArgs(false)
	if dry[len(dry)-1] != "-dry-run" {
		t.Errorf("dry-run args = %v", dry)
	}
	pre := req.PreviewArgs(true)
	if pre[len(pre)-1] != "-preflight" {
		t.Errorf("preflight args = %v", pre)
	}
	// The two modes are mutually exclusive in summoner; never send both.
	for _, a := range pre {
		if a == "-dry-run" {
			t.Error("preflight args must not include -dry-run")
		}
	}
}

// --- binary resolution -----------------------------------------------------

func TestResolveBinaryMissing(t *testing.T) {
	m := NewManager(ManagerOptions{
		Store:    NewStore(t.TempDir()),
		Binary:   "definitely-not-a-real-summoner",
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	})
	if _, err := m.ResolveBinary(); !errors.Is(err, ErrSummonerMissing) {
		t.Errorf("error = %v, want ErrSummonerMissing", err)
	}

	p := djinnProject(t, "djinn")
	if _, err := m.Preview(context.Background(), p.Workdir, SummonRequest{}, false); !errors.Is(err, ErrSummonerMissing) {
		t.Errorf("Preview error = %v, want ErrSummonerMissing", err)
	}
	if _, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{}); !errors.Is(err, ErrSummonerMissing) {
		t.Errorf("Summon error = %v, want ErrSummonerMissing", err)
	}
}

func TestResolveBinaryAbsoluteMissing(t *testing.T) {
	m := NewManager(ManagerOptions{Store: NewStore(t.TempDir()), Binary: "/nope/summoner"})
	if _, err := m.ResolveBinary(); !errors.Is(err, ErrSummonerMissing) {
		t.Errorf("error = %v, want ErrSummonerMissing", err)
	}
}

// --- preview ---------------------------------------------------------------

func TestPreviewDryRun(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, nil)
	t.Setenv("FAKE_SUMMONER_MODE", "dry-run")

	got, err := m.Preview(context.Background(), p.Workdir, SummonRequest{}, false)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !reflect.DeepEqual(got.Ordered, []string{"djinn-aaaa", "djinn-bbbb"}) {
		t.Errorf("ordered = %v", got.Ordered)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Bead != "djinn-nope" {
		t.Errorf("warnings = %+v", got.Warnings)
	}
	if got.Preflight {
		t.Error("dry-run must not report preflight")
	}
}

// TestPreviewWarnsAboutSilentlyDroppedBeads: summoner does not always print a
// warning for a bead outside the ready set, so the preview diffs the request
// against the result.
func TestPreviewWarnsAboutSilentlyDroppedBeads(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, nil)
	t.Setenv("FAKE_SUMMONER_MODE", "dry-run")

	got, err := m.Preview(context.Background(), p.Workdir,
		SummonRequest{Beads: []string{"djinn-aaaa", "djinn-ghost"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	var warned bool
	for _, w := range got.Warnings {
		if w.Bead == "djinn-ghost" {
			warned = true
		}
	}
	if !warned {
		t.Errorf("warnings = %+v, want one naming djinn-ghost", got.Warnings)
	}
	// A bead that summoner already warned about must not be duplicated.
	seen := map[string]int{}
	for _, w := range got.Warnings {
		seen[w.Bead]++
	}
	for bead, n := range seen {
		if n > 1 {
			t.Errorf("bead %s warned about %d times", bead, n)
		}
	}
}

func TestPreviewPreflight(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, nil)
	t.Setenv("FAKE_SUMMONER_MODE", "preflight")

	got, err := m.Preview(context.Background(), p.Workdir, SummonRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Preflight {
		t.Error("Preflight flag not set")
	}
	var found bool
	for _, b := range got.NeedsOperator {
		if b == "djinn-bbbb" {
			found = true
		}
	}
	if !found {
		t.Errorf("needsOperator = %v, want djinn-bbbb", got.NeedsOperator)
	}
}

func TestPreviewFailureSurfacesStderr(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, nil)
	t.Setenv("FAKE_SUMMONER_MODE", "fail")

	_, err := m.Preview(context.Background(), p.Workdir, SummonRequest{}, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !contains(err.Error(), "scope error: no such epic") {
		t.Errorf("error = %q, want it to carry summoner's message", err)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// --- summon / stop ---------------------------------------------------------

func TestSummonSpawnsAndFinishes(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	t.Setenv("FAKE_SUMMONER_MODE", "default")

	run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{MaxWorkers: 2})
	if err != nil {
		t.Fatalf("Summon: %v", err)
	}
	if run.ID == "" || run.Source != SourceLoom || run.State != StateRunning {
		t.Errorf("run = %+v", run)
	}
	if !reflect.DeepEqual(run.Args, []string{"-max-workers", "2"}) {
		t.Errorf("args = %v", run.Args)
	}

	hub.waitFor(t, EventRunStarted, 3*time.Second)
	fin := hub.waitFor(t, EventRunFinished, 5*time.Second)

	payload := fin.Payload.(map[string]any)
	if payload["state"] != StateFinished {
		t.Errorf("final state = %v, want finished", payload["state"])
	}

	// The record must be persisted and reflect the final state.
	saved, err := m.Store().Load(p.Name, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil {
		t.Fatal("run record was not persisted")
	}
	if saved.State != StateFinished {
		t.Errorf("persisted state = %q, want finished", saved.State)
	}
	if saved.EndedAt == nil {
		t.Error("persisted record has no EndedAt")
	}

	// The console capture must exist and hold what the child printed.
	path, err := m.Store().ConsolePath(p.Name, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("console capture missing: %v", err)
	}
	if !contains(string(data), "fake-summoner") {
		t.Errorf("console capture = %q", data)
	}
}

func TestSummonRefusesConcurrentRuns(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, &recordingHub{})
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	first, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatalf("first Summon: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(p.Name, first.ID) })

	_, err = m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
	if !errors.Is(err, ErrRunActive) {
		t.Errorf("second Summon error = %v, want ErrRunActive", err)
	}
	if !contains(err.Error(), first.ID) {
		t.Errorf("error = %q, want it to name the active run", err)
	}
}

// TestSummonConcurrentCallsOnlySpawnOnce guards the reservation: two confirms
// racing must not both start a drain.
func TestSummonConcurrentCallsOnlySpawnOnce(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, &recordingHub{})
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ok       int
		refused  int
		firstRun string
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
				firstRun = run.ID
			case errors.Is(err, ErrRunActive):
				refused++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if ok != 1 {
		t.Errorf("%d concurrent Summons succeeded, want exactly 1", ok)
	}
	if refused != 7 {
		t.Errorf("%d were refused, want 7", refused)
	}
	if firstRun != "" {
		_ = m.Stop(p.Name, firstRun)
	}
}

func TestStopTerminatesRun(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	hub.waitFor(t, EventRunStarted, 3*time.Second)
	waitForConsole(t, m, p.Name, run.ID, "trap installed", 5*time.Second)

	if err := m.Stop(p.Name, run.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stop must be idempotent.
	if err := m.Stop(p.Name, run.ID); err != nil {
		t.Errorf("second Stop: %v", err)
	}

	fin := hub.waitFor(t, EventRunFinished, 10*time.Second)
	payload := fin.Payload.(map[string]any)
	if payload["state"] != StateStopped {
		t.Errorf("state = %v, want stopped", payload["state"])
	}

	saved, _ := m.Store().Load(p.Name, run.ID)
	if saved == nil || saved.State != StateStopped {
		t.Errorf("persisted run = %+v, want stopped", saved)
	}
	if _, active := m.Active(p.Name); active {
		t.Error("the run should no longer be active")
	}
}

// TestStopKillsAfterGrace: a child that ignores SIGTERM must still die.
func TestStopKillsAfterGrace(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub) // grace is 500ms in tests
	t.Setenv("FAKE_SUMMONER_MODE", "ignore")

	run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	hub.waitFor(t, EventRunStarted, 3*time.Second)
	// The trap must be installed before SIGTERM, or this measures the default
	// disposition rather than the kill-after-grace path.
	waitForConsole(t, m, p.Name, run.ID, "trap installed", 5*time.Second)

	start := time.Now()
	if err := m.Stop(p.Name, run.ID); err != nil {
		t.Fatal(err)
	}
	hub.waitFor(t, EventRunFinished, 15*time.Second)

	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Errorf("run died in %s; SIGTERM should have been given its grace period first", elapsed)
	}
	if _, active := m.Active(p.Name); active {
		t.Error("run still active after the kill")
	}
}

func TestStopUnknownRun(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, nil)

	if err := m.Stop(p.Name, "run-does-not-exist"); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("error = %v, want ErrRunNotFound", err)
	}

	t.Setenv("FAKE_SUMMONER_MODE", "long")
	run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(p.Name, run.ID) })

	if err := m.Stop(p.Name, "run-other"); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("stopping the wrong id = %v, want ErrRunNotFound", err)
	}
}

func TestFailedRunIsRecorded(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	t.Setenv("FAKE_SUMMONER_MODE", "fail")

	run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	hub.waitFor(t, EventRunFinished, 5*time.Second)

	saved, _ := m.Store().Load(p.Name, run.ID)
	if saved == nil {
		t.Fatal("no record")
	}
	if saved.State != StateFailed {
		t.Errorf("state = %q, want failed", saved.State)
	}
	if saved.ExitError == "" {
		t.Error("a failed run should record why")
	}
}

// TestSummonParsesCostFromConsole: the "long" fake emits real-shaped step lines,
// so the finished record should carry their summed cost.
func TestSummonParsesCostFromConsole(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	hub.waitFor(t, EventRunStarted, 3*time.Second)
	waitForConsole(t, m, p.Name, run.ID, "trap installed", 5*time.Second)
	if err := m.Stop(p.Name, run.ID); err != nil {
		t.Fatal(err)
	}
	hub.waitFor(t, EventRunFinished, 10*time.Second)

	saved, _ := m.Store().Load(p.Name, run.ID)
	if saved == nil {
		t.Fatal("no record")
	}
	// $1.50 + $0.50 from the fake's two Done lines.
	if saved.CostSoFar < 1.99 || saved.CostSoFar > 2.01 {
		t.Errorf("costSoFar = %v, want ~2.00 from the parsed step lines", saved.CostSoFar)
	}
}

func TestShutdownStopsEverything(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, &recordingHub{})
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	if _, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.Shutdown(ctx, nil)

	if _, active := m.Active(p.Name); active {
		t.Error("Shutdown left a run active")
	}
}

// TestShutdownWaitsForTheFinalRecord: a run that has left the active set is
// still writing its final record, and Shutdown must not return before that
// write lands. A caller that removes the state dir after Shutdown (a test
// cleanup, a process exit) would otherwise race the write.
func TestShutdownWaitsForTheFinalRecord(t *testing.T) {
	t.Setenv("FAKE_SUMMONER_MODE", "long")
	for i := range 10 {
		p := djinnProject(t, "djinn")
		m := newTestManager(t, &recordingHub{})
		run, err := m.Summon(context.Background(), p.Name, p.Workdir, SummonRequest{})
		if err != nil {
			t.Fatal(err)
		}
		waitForConsole(t, m, p.Name, run.ID, "trap installed", 5*time.Second)
		if err := m.Stop(p.Name, run.ID); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if _, active := m.Active(p.Name); !active {
				break
			}
			time.Sleep(time.Millisecond)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		m.Shutdown(ctx, nil)
		cancel()

		saved, err := m.Store().Load(p.Name, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved == nil || saved.State != StateStopped {
			t.Fatalf("attempt %d: record after Shutdown = %+v, want the final stopped record", i, saved)
		}
	}
}

// --- store -----------------------------------------------------------------

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	ended := time.Now().UTC().Truncate(time.Second)
	run := Run{
		ID: "run-1", Project: "djinn", Source: SourceLoom,
		StartedAt: ended.Add(-time.Minute), EndedAt: &ended,
		State: StateFinished, Args: []string{"-max-workers", "3"},
		Beads: []RunBead{{ID: "djinn-a", SpawnedAt: ended.Add(-time.Minute), Verdict: VerdictComplete}},
	}
	if err := s.Save(run); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load("djinn", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no record loaded")
	}
	if got.ID != run.ID || got.State != run.State || len(got.Beads) != 1 {
		t.Errorf("loaded = %+v", got)
	}
	if !got.StartedAt.Equal(run.StartedAt) {
		t.Errorf("startedAt = %v, want %v", got.StartedAt, run.StartedAt)
	}

	missing, err := s.Load("djinn", "run-nope")
	if err != nil || missing != nil {
		t.Errorf("missing run = %+v, %v; want nil, nil", missing, err)
	}
}

func TestStoreListNewestFirst(t *testing.T) {
	s := NewStore(t.TempDir())
	base := time.Now().UTC()
	for i, id := range []string{"run-1", "run-2", "run-3"} {
		if err := s.Save(Run{
			ID: id, Project: "djinn", Source: SourceLoom,
			StartedAt: base.Add(time.Duration(i) * time.Minute),
			State:     StateFinished,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.List("djinn")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d runs, want 3", len(got))
	}
	if got[0].ID != "run-3" || got[2].ID != "run-1" {
		t.Errorf("order = %s, %s, %s; want newest first", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestStoreSkipsCorruptRecords(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if err := s.Save(Run{ID: "run-good", Project: "djinn", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// A truncated record must not hide the rest of the history.
	bad := filepath.Join(root, "runs", "djinn", "run-bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "run.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := s.List("djinn")
	if err != nil {
		t.Fatalf("a corrupt record must not fail the listing: %v", err)
	}
	if len(got) != 1 || got[0].ID != "run-good" {
		t.Errorf("runs = %+v, want just run-good", got)
	}
}

func TestStoreRejectsUnsafeIDs(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, tc := range []struct{ project, run string }{
		{"../etc", "run-1"},
		{"djinn", "../../escape"},
		{"", "run-1"},
		{"djinn", ""},
	} {
		if _, err := s.RunDir(tc.project, tc.run); err == nil {
			t.Errorf("RunDir(%q, %q) should have been rejected", tc.project, tc.run)
		}
	}
	if _, err := s.List("../etc"); err == nil {
		t.Error("List with an unsafe project name should be rejected")
	}
}

func TestNewRunIDIsSortableAndSafe(t *testing.T) {
	a := NewRunID(1000)
	b := NewRunID(2000)
	if a >= b {
		t.Errorf("ids should sort chronologically: %q vs %q", a, b)
	}
	if !ValidID(a) {
		t.Errorf("run id %q is not path-safe", a)
	}
	if !IsLoomRunID(a) {
		t.Errorf("%q should be recognised as a Loom run id", a)
	}
	if IsLoomRunID("ext-20260616T082802Z") {
		t.Error("external ids must not be mistaken for Loom ids")
	}
}

func TestDefaultStateDirHonoursEnv(t *testing.T) {
	t.Setenv(StateDirEnv, "/tmp/loom-state-test")
	if got := DefaultStateDir(); got != "/tmp/loom-state-test" {
		t.Errorf("DefaultStateDir() = %q", got)
	}
}
