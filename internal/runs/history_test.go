package runs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeLifecycle drops a lifecycle log into a summoner-logs directory.
func writeLifecycle(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectPaths(t *testing.T) {
	p := Project{Name: "djinn", Workdir: "/w"}
	if p.DjinnDir() != "/w/.djinn" {
		t.Errorf("DjinnDir = %q", p.DjinnDir())
	}
	if p.LegacySummonerLogDir() != "/w/.djinn/summoner-logs" {
		t.Errorf("SummonerLogDir = %q", p.LegacySummonerLogDir())
	}
	if p.StateDir() != "/w/.djinn/state" {
		t.Errorf("StateDir = %q", p.StateDir())
	}
	if p.IsDjinnProject() {
		t.Error("a nonexistent workdir must not report IsDjinnProject")
	}
}

func TestHistoryRequiresDjinn(t *testing.T) {
	p := Project{Name: "loom", Workdir: t.TempDir()} // no .djinn
	h := NewHistory(NewStore(t.TempDir()), nil)

	if _, err := h.List(p); !errors.Is(err, ErrNoDjinn) {
		t.Errorf("List error = %v, want ErrNoDjinn", err)
	}
	if _, err := h.Get(p, "run-1"); !errors.Is(err, ErrNoDjinn) {
		t.Errorf("Get error = %v, want ErrNoDjinn", err)
	}
}

// TestHistoryDetectsExternalRuns is the "window onto djinn" property: a drain
// nobody started through Loom must still appear.
func TestHistoryDetectsExternalRuns(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	// Two clusters separated by hours — a real slice of 2026-06-16. Note the
	// first worker ran 10m13s, longer than ExternalRunGap, so this also pins
	// that an in-flight worker holds its run open.
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", `2026-06-16T00:06:28-07:00	djinn-hmmp	spawn
2026-06-16T00:16:41-07:00	djinn-hmmp	exit	COMPLETE	closed
2026-06-16T08:28:02-07:00	djinn-17lo	spawn
2026-06-16T08:29:23-07:00	djinn-17lo	exit	FAILED	operator-escalation halt (loop_exhausted)
`)

	runs, err := h.List(p)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2 (the gap splits them): %+v", len(runs), runs)
	}
	for _, r := range runs {
		if r.Source != SourceExternal {
			t.Errorf("run %s source = %q, want external", r.ID, r.Source)
		}
		if len(r.Args) != 0 {
			t.Errorf("external run %s should carry no args", r.ID)
		}
		if len(r.Beads) != 1 {
			t.Errorf("run %s has %d beads, want 1", r.ID, len(r.Beads))
		}
	}
	// Newest first.
	if !runs[0].StartedAt.After(runs[1].StartedAt) {
		t.Errorf("runs are not newest-first: %v then %v", runs[0].StartedAt, runs[1].StartedAt)
	}

	// The failed cluster must carry its verdict through.
	newest := runs[0]
	if newest.Beads[0].Verdict != VerdictFailed {
		t.Errorf("verdict = %q, want FAILED", newest.Beads[0].Verdict)
	}
	if newest.Beads[0].FinalStatus != "operator-escalation halt (loop_exhausted)" {
		t.Errorf("finalStatus = %q", newest.Beads[0].FinalStatus)
	}
}

// TestHistoryDoesNotDuplicateManagedRuns: activity inside a managed run's
// window belongs to that run, not to a synthesized external twin.
func TestHistoryDoesNotDuplicateManagedRuns(t *testing.T) {
	p := djinnProject(t, "djinn")
	store := NewStore(t.TempDir())
	h := NewHistory(store, nil)

	start := time.Date(2026, 6, 16, 7, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	if err := store.Save(Run{
		ID: "run-1", Project: "djinn", Source: SourceLoom,
		StartedAt: start, EndedAt: &end, State: StateFinished,
	}); err != nil {
		t.Fatal(err)
	}

	// 08:28-07:00 is 15:28 UTC — outside the window; 07:30Z is inside.
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", `2026-06-16T07:30:00Z	djinn-inside	spawn
2026-06-16T07:31:00Z	djinn-inside	exit	COMPLETE	closed
2026-06-16T20:00:00Z	djinn-outside	spawn
2026-06-16T20:05:00Z	djinn-outside	exit	COMPLETE	closed
`)

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want the managed one plus one external: %+v", len(runs), runs)
	}

	var managed, external int
	for _, r := range runs {
		switch r.Source {
		case SourceLoom:
			managed++
		case SourceExternal:
			external++
			for _, b := range r.Beads {
				if b.ID == "djinn-inside" {
					t.Error("activity inside the managed window was duplicated as an external run")
				}
			}
		}
	}
	if managed != 1 || external != 1 {
		t.Errorf("managed=%d external=%d, want 1 and 1", managed, external)
	}
}

// TestHistoryMarksAbandonedExternalRunsFailed: an old external run whose beads
// never exited is dead, not live.
func TestHistoryMarksAbandonedExternalRunsFailed(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", `2026-06-16T00:06:28-07:00	djinn-abandoned	spawn
`)

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs", len(runs))
	}
	if runs[0].State != StateFailed {
		t.Errorf("state = %q, want failed for a long-abandoned run", runs[0].State)
	}
	if runs[0].ExitError == "" {
		t.Error("an abandoned run should explain itself")
	}
	if runs[0].EndedAt == nil {
		t.Error("an abandoned run should have an end time")
	}
}

// TestHistoryKeepsRecentExternalRunRunning: the same shape, but recent, is a
// live run.
func TestHistoryKeepsRecentExternalRunRunning(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	recent := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", recent+"\tdjinn-live\tspawn\n")

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs", len(runs))
	}
	if runs[0].State != StateRunning {
		t.Errorf("state = %q, want running for recent activity", runs[0].State)
	}
}

func TestHistoryIgnoresNonLifecycleFiles(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	// Console logs and rerun captures live in the same directory and are a
	// different format entirely.
	writeLifecycle(t, p.LegacySummonerLogDir(), "console-2026-06-13.log", "not\ta\tlifecycle log\n")
	writeLifecycle(t, p.LegacySummonerLogDir(), "rerun-uy0d-1781850236.out", "junk\n")
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", "2026-06-16T00:06:28-07:00\tdjinn-a\tspawn\n")

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1 from the single real lifecycle log: %+v", len(runs), runs)
	}
	if runs[0].Beads[0].ID != "djinn-a" {
		t.Errorf("bead = %q", runs[0].Beads[0].ID)
	}
}

func TestHistoryCachesByModTime(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)
	path := filepath.Join(p.LegacySummonerLogDir(), "2026-06-16.log")

	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", "2026-06-16T00:06:28-07:00\tdjinn-a\tspawn\n")
	first, err := h.events(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.events(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatal("cached parse disagreed with the first")
	}

	// A changed file must be re-read. Size changes, so the cache key moves even
	// if the filesystem's mtime resolution is coarse.
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log",
		"2026-06-16T00:06:28-07:00\tdjinn-a\tspawn\n2026-06-16T00:07:00-07:00\tdjinn-b\tspawn\n")
	third, err := h.events(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 2 {
		t.Errorf("got %d events after the file grew, want 2 (cache not invalidated)", len(third))
	}

	h.Invalidate(p)
	h.mu.Lock()
	n := len(h.cache)
	h.mu.Unlock()
	if n != 0 {
		t.Errorf("Invalidate left %d cached entries", n)
	}
}

func TestHistoryGetRunDetail(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	// One bead exited, one still going — the second is an active worker slot.
	recent := time.Now().UTC().Add(-time.Minute)
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log",
		recent.Format(time.RFC3339)+"\tdjinn-done\tspawn\n"+
			recent.Add(10*time.Second).Format(time.RFC3339)+"\tdjinn-done\texit\tCOMPLETE\tclosed\n"+
			recent.Add(20*time.Second).Format(time.RFC3339)+"\tdjinn-busy\tspawn\n")

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs", len(runs))
	}

	detail, err := h.Get(p, runs[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(detail.ActiveSlots) != 1 {
		t.Fatalf("activeSlots = %+v, want just djinn-busy", detail.ActiveSlots)
	}
	if detail.ActiveSlots[0].Bead != "djinn-busy" {
		t.Errorf("active slot = %q", detail.ActiveSlots[0].Bead)
	}
	// No state directory for that bead yet, so no current step.
	if detail.ActiveSlots[0].CurrentStep != nil {
		t.Errorf("currentStep = %+v, want nil with no state dir", detail.ActiveSlots[0].CurrentStep)
	}

	if _, err := h.Get(p, "run-nope"); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("Get(unknown) = %v, want ErrRunNotFound", err)
	}
	if _, err := h.Get(p, "../escape"); err == nil {
		t.Error("Get with an unsafe id should be rejected")
	}
}

// TestHistoryGetReadsCurrentStepFromStateDir wires the state-dir parser into
// the live worker slot.
func TestHistoryGetReadsCurrentStepFromStateDir(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	// Give the busy bead a state directory shaped like a real one.
	stepDir := filepath.Join(p.StateDir(), "djinn-busy", "impl-loop.iter-2")
	if err := os.MkdirAll(stepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stepDir, "verify-impl"),
		[]byte(`{"complete":false,"verdict":"REVISE"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	recent := time.Now().UTC().Add(-time.Minute)
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", recent.Format(time.RFC3339)+"\tdjinn-busy\tspawn\n")

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := h.Get(p, runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.ActiveSlots) != 1 {
		t.Fatalf("activeSlots = %+v", detail.ActiveSlots)
	}
	step := detail.ActiveSlots[0].CurrentStep
	if step == nil {
		t.Fatal("currentStep is nil despite a populated state dir")
	}
	if step.Loop != "impl-loop" || step.Iter != 2 || step.Procedure != "verify-impl" {
		t.Errorf("currentStep = %+v", step)
	}
	if step.Step != "impl" {
		t.Errorf("step display name = %q, want impl", step.Step)
	}
}

// TestHistoryOverlaysLiveManagedRun: the manager's in-memory state is fresher
// than the persisted record.
func TestHistoryOverlaysLiveManagedRun(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, &recordingHub{})
	h := NewHistory(m.Store(), m)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	run, err := m.Summon(t.Context(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(p.Name, run.ID) })

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want the live managed one", len(runs))
	}
	if runs[0].ID != run.ID || runs[0].State != StateRunning {
		t.Errorf("run = %+v, want the live record", runs[0])
	}
	if runs[0].Source != SourceLoom {
		t.Errorf("source = %q", runs[0].Source)
	}
}

func TestReadLogChunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	body := "\x1b[32mline one\x1b[0m\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("stripped by default", func(t *testing.T) {
		text, next, eof, err := ReadLogChunk(path, 0, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if !eof {
			t.Error("a full read should report eof")
		}
		if next != int64(len(body)) {
			t.Errorf("next = %d, want %d", next, len(body))
		}
		if contains(text, "\x1b[") {
			t.Errorf("ANSI survived stripping: %q", text)
		}
		if !contains(text, "line one") {
			t.Errorf("text = %q", text)
		}
	})

	t.Run("raw with ansi", func(t *testing.T) {
		text, _, _, err := ReadLogChunk(path, 0, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		if !contains(text, "\x1b[32m") {
			t.Errorf("ansi=1 should return the raw bytes: %q", text)
		}
	})

	t.Run("offset paging", func(t *testing.T) {
		first, next, eof, err := ReadLogChunk(path, 0, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		if eof {
			t.Error("a partial read should not report eof")
		}
		if next != 10 {
			t.Errorf("next = %d, want 10", next)
		}
		second, _, _, err := ReadLogChunk(path, next, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		if first == "" || second == "" {
			t.Error("both chunks should have content")
		}
	})

	t.Run("offset past the end clamps", func(t *testing.T) {
		text, _, eof, err := ReadLogChunk(path, 1<<20, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if text != "" || !eof {
			t.Errorf("text = %q eof = %v, want empty at eof", text, eof)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, _, _, err := ReadLogChunk(filepath.Join(dir, "nope.log"), 0, 0, false); err == nil {
			t.Error("expected an error for a missing log")
		}
	})
}

// TestWatcherIncludesSpawnLoggedInTheStartSecond guards a real off-by-a-fraction
// bug: lifecycle timestamps have second resolution and truncate downwards, so a
// worker spawned in the same second the run started logs an instant *earlier*
// than run.StartedAt and would otherwise be dropped from the run entirely.
func TestWatcherIncludesSpawnLoggedInTheStartSecond(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	h := NewHistory(m.Store(), m)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	run, err := m.Summon(t.Context(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(p.Name, run.ID) })

	// The spawn line lands in the same second the run started, truncated down —
	// exactly what a real summoner writes.
	spawnAt := run.StartedAt.Truncate(time.Second)
	if !spawnAt.Before(run.StartedAt) {
		// StartedAt happened to be exactly on a second boundary; force the case.
		spawnAt = run.StartedAt.Add(-500 * time.Millisecond).Truncate(time.Second)
	}
	writeLifecycle(t, p.LegacySummonerLogDir(), spawnAt.UTC().Format("2006-01-02")+".log",
		spawnAt.UTC().Format(time.RFC3339)+"\tdjinn-first\tspawn\n")

	w := NewWatcher(p, m, h, hub)
	w.tick(t.Context())

	// The bead must be in the run.
	active, ok := m.Active(p.Name)
	if !ok {
		t.Fatal("run is no longer active")
	}
	if len(active.Beads) != 1 || active.Beads[0].ID != "djinn-first" {
		t.Fatalf("beads = %+v, want the spawn logged in the start second", active.Beads)
	}

	// And a bead-state event must have been published for it.
	var sawSpawn bool
	for _, e := range hub.events {
		if e.Type != EventBeadState {
			continue
		}
		if payload, ok := e.Payload.(map[string]any); ok &&
			payload["bead"] == "djinn-first" && payload["kind"] == KindSpawn {
			sawSpawn = true
		}
	}
	if !sawSpawn {
		t.Errorf("no bead-state spawn event; saw %v", hub.types())
	}
}

// TestI4MidnightRolloverIsOneRun is the regression test for review finding I4.
//
// summoner rotates its lifecycle log at midnight and night drains are normal —
// the real 2026-06-16 log opens at 00:06. A drain kicked off at 23:40 writes its
// spawns in one file and its exits in the next. Grouping per file split that
// into two fictional runs: the first declared "failed — no exit recorded"
// though it finished cleanly, the second full of beads that appeared to spawn at
// the instant they exited.
func TestI4MidnightRolloverIsOneRun(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	// 23:40 spawn on one day, 00:12 exit on the next.
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-15.log",
		"2026-06-15T23:40:00Z\tdjinn-night\tspawn\n"+
			"2026-06-15T23:45:00Z\tdjinn-other\tspawn\n")
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log",
		"2026-06-16T00:12:00Z\tdjinn-night\texit\tCOMPLETE\tclosed\n"+
			"2026-06-16T00:18:00Z\tdjinn-other\texit\tCOMPLETE\tclosed\n")

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1 — the drain crossed midnight, it did not restart: %+v",
			len(runs), runs)
	}

	run := runs[0]
	if run.State != StateFinished {
		t.Errorf("state = %q, want finished; it exited cleanly on the far side of midnight", run.State)
	}
	if run.ExitError != "" {
		t.Errorf("exitError = %q, want empty — this run never failed", run.ExitError)
	}
	if len(run.Beads) != 2 {
		t.Fatalf("got %d beads, want 2: %+v", len(run.Beads), run.Beads)
	}

	// Each bead must keep its real spawn time, not its exit instant.
	for _, b := range run.Beads {
		if b.ExitedAt == nil {
			t.Errorf("%s: no exit recorded", b.ID)
			continue
		}
		if !b.SpawnedAt.Before(*b.ExitedAt) {
			t.Errorf("%s: spawnedAt %v is not before exitedAt %v — the spawn row was lost",
				b.ID, b.SpawnedAt, *b.ExitedAt)
		}
		if b.SpawnedAt.Day() != 15 {
			t.Errorf("%s: spawnedAt = %v, want the 15th", b.ID, b.SpawnedAt)
		}
		if b.Verdict != VerdictComplete {
			t.Errorf("%s: verdict = %q", b.ID, b.Verdict)
		}
	}
	if run.StartedAt.Day() != 15 {
		t.Errorf("run started %v, want the 15th", run.StartedAt)
	}
}

// TestI4AbandonedRunDoesNotSwallowLaterOnes is the backstop that concatenating
// files makes necessary: a worker whose summoner was killed never logs an exit,
// so "in flight" would otherwise hold its run open across every later day.
func TestI4AbandonedRunDoesNotSwallowLaterOnes(t *testing.T) {
	p := djinnProject(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	// A worker that never exited, then an unrelated drain three days later.
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-10.log", "2026-06-10T10:00:00Z\tdjinn-abandoned\tspawn\n")
	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-13.log",
		"2026-06-13T09:00:00Z\tdjinn-later\tspawn\n"+
			"2026-06-13T09:20:00Z\tdjinn-later\texit\tCOMPLETE\tclosed\n")

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2 — the abandoned worker must not absorb the later drain: %+v",
			len(runs), runs)
	}

	byBead := map[string]Run{}
	for _, r := range runs {
		for _, b := range r.Beads {
			byBead[b.ID] = r
		}
	}
	if a, ok := byBead["djinn-abandoned"]; !ok {
		t.Error("the abandoned run is missing")
	} else if a.State != StateFailed {
		t.Errorf("abandoned run state = %q, want failed", a.State)
	}
	if l, ok := byBead["djinn-later"]; !ok {
		t.Error("the later run is missing")
	} else if l.State != StateFinished {
		t.Errorf("later run state = %q, want finished", l.State)
	}
	if byBead["djinn-abandoned"].ID == byBead["djinn-later"].ID {
		t.Error("both beads landed in the same run")
	}
}

// TestI4WatcherFoldsAcrossMidnight: the managed-run half. The watcher used to
// read only the newest log, so after a rollover every bead spawned before
// midnight vanished from run.Beads.
func TestI4WatcherFoldsAcrossMidnight(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	h := NewHistory(m.Store(), m)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	// Start the run "yesterday" so today's log is not the only relevant one.
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	m.now = func() time.Time { return yesterday.Add(-2 * time.Hour) }

	run, err := m.Summon(t.Context(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(p.Name, run.ID) })

	// Spawn in yesterday's file, exit in today's.
	writeLifecycle(t, p.LegacySummonerLogDir(), yesterday.Format("2006-01-02")+".log",
		yesterday.Format(time.RFC3339)+"\tdjinn-crossing\tspawn\n")
	writeLifecycle(t, p.LegacySummonerLogDir(), time.Now().UTC().Format("2006-01-02")+".log",
		time.Now().UTC().Format(time.RFC3339)+"\tdjinn-crossing\texit\tCOMPLETE\tclosed\n")

	w := NewWatcher(p, m, h, hub)
	w.tick(t.Context())

	active, ok := m.Active(p.Name)
	if !ok {
		t.Fatal("run is no longer active")
	}
	if len(active.Beads) != 1 {
		t.Fatalf("beads = %+v, want the one bead that crossed midnight", active.Beads)
	}
	b := active.Beads[0]
	if b.ID != "djinn-crossing" {
		t.Errorf("bead = %q", b.ID)
	}
	if b.ExitedAt == nil {
		t.Fatal("the exit from today's log was not folded in")
	}
	// The spawn must come from yesterday's file, not be fabricated from the exit.
	if !b.SpawnedAt.Before(*b.ExitedAt) {
		t.Errorf("spawnedAt %v is not before exitedAt %v — yesterday's spawn row was lost",
			b.SpawnedAt, *b.ExitedAt)
	}
	if b.Verdict != VerdictComplete {
		t.Errorf("verdict = %q", b.Verdict)
	}
}

// TestI3RunUpdatedCarriesProject is the regression test for review finding I3:
// without `project`, the frontend invalidated the cache key for project "",
// which matches nothing, so mid-run bead and cost updates never landed.
func TestI3RunUpdatedCarriesProject(t *testing.T) {
	p := djinnProject(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	h := NewHistory(m.Store(), m)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	run, err := m.Summon(t.Context(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(p.Name, run.ID) })

	spawnAt := run.StartedAt.Truncate(time.Second)
	writeLifecycle(t, p.LegacySummonerLogDir(), spawnAt.UTC().Format("2006-01-02")+".log",
		spawnAt.UTC().Format(time.RFC3339)+"\tdjinn-a\tspawn\n")

	w := NewWatcher(p, m, h, hub)
	w.tick(t.Context())

	var seen bool
	for _, e := range hub.events {
		if e.Type != EventRunUpdated {
			continue
		}
		seen = true
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			t.Fatalf("unexpected payload %#v", e.Payload)
		}
		if payload["project"] != p.Name {
			t.Errorf("run-updated project = %v, want %q", payload["project"], p.Name)
		}
		if payload["runId"] != run.ID {
			t.Errorf("run-updated runId = %v", payload["runId"])
		}
	}
	if !seen {
		t.Fatalf("no run-updated event; saw %v", hub.types())
	}

	// run-started and run-finished already carried it; they must keep doing so.
	for _, e := range hub.events {
		if e.Type != EventRunStarted {
			continue
		}
		if payload, ok := e.Payload.(map[string]any); ok && payload["project"] != p.Name {
			t.Errorf("run-started project = %v", payload["project"])
		}
	}
}

// TestN1IdleTickDoesNotDropTheHistoryCache: the cache is keyed on each log's
// (mtime, size), so dropping it every 500ms just guaranteed that every /runs
// request re-parsed every per-day log.
func TestN1IdleTickPreservesHistoryCache(t *testing.T) {
	p := djinnProject(t, "djinn")
	m := newTestManager(t, &recordingHub{})
	h := NewHistory(m.Store(), m)

	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-06-16.log", "2026-06-16T00:06:28-07:00\tdjinn-a\tspawn\n")
	path := filepath.Join(p.LegacySummonerLogDir(), "2026-06-16.log")
	if _, err := h.events(path); err != nil {
		t.Fatal(err)
	}

	h.mu.Lock()
	cached := len(h.cache)
	h.mu.Unlock()
	if cached != 1 {
		t.Fatalf("cache has %d entries after a read, want 1", cached)
	}

	// No managed run: this is the idle path.
	w := NewWatcher(p, m, h, nil)
	for i := 0; i < 5; i++ {
		w.tick(t.Context())
	}

	h.mu.Lock()
	cached = len(h.cache)
	h.mu.Unlock()
	if cached != 1 {
		t.Errorf("cache has %d entries after idle ticks, want the parse to survive", cached)
	}
}

// TestHistoryListsRunsFromTheGitCommonDir: djinn now writes its lifecycle logs
// under <git common dir>/djinn/summoner-logs. The fixture is shaped like the
// real 2026-11-08 log, where round rows are most of the file: a round row is
// activity that holds a long worker's run open, never a bead of its own.
func TestHistoryListsRunsFromTheGitCommonDir(t *testing.T) {
	p, logs := djinnProjectInRepo(t, "djinn")
	h := NewHistory(NewStore(t.TempDir()), nil)

	body := "2026-11-08T10:00:00Z\tdjinn-a\tspawn\n"
	// Rounds every 30 minutes; the 2h40m worker outlives AbandonedGap only if
	// its rounds count as activity.
	for _, at := range []string{"10:30", "11:00", "11:30", "12:00", "12:30"} {
		body += "2026-11-08T" + at + ":00Z\tdjinn-a\tround\treview-loop\titer\t1\tpass\n"
	}
	body += "2026-11-08T12:35:00Z\tdjinn-orphan\tround\treview-loop\titer\t2\tfail\n" +
		"2026-11-08T12:40:00Z\tdjinn-a\texit\tCOMPLETE\tclosed\n" +
		"2026-11-08T12:41:00Z\tdjinn-b\tspawn\n" +
		"2026-11-08T12:45:00Z\tdjinn-b\tround\timplement\titer\t1\tspawned\n" +
		"2026-11-08T12:50:00Z\tdjinn-b\texit\tFAILED\toperator-escalation halt\n"
	writeLifecycle(t, logs, "2026-11-08.log", body)

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1 — round rows hold the drain together: %+v", len(runs), runs)
	}
	r := runs[0]
	if r.Source != SourceExternal {
		t.Errorf("source = %q, want external", r.Source)
	}
	if len(r.Beads) != 2 || r.Beads[0].ID != "djinn-a" || r.Beads[1].ID != "djinn-b" {
		t.Fatalf("beads = %+v, want djinn-a then djinn-b and no round-only bead", r.Beads)
	}
	if r.Beads[0].Verdict != VerdictComplete || r.Beads[1].Verdict != VerdictFailed {
		t.Errorf("verdicts = %q, %q", r.Beads[0].Verdict, r.Beads[1].Verdict)
	}
	if r.State != StateFinished {
		t.Errorf("state = %q, want finished", r.State)
	}
	wantEnd := time.Date(2026, 11, 8, 12, 50, 0, 0, time.UTC)
	if r.EndedAt == nil || !r.EndedAt.Equal(wantEnd) {
		t.Errorf("endedAt = %v, want %v", r.EndedAt, wantEnd)
	}
}

// TestHistoryListsRunsFromBothLayouts: a repo that drained before and after
// djinn's move shows all of its history in one list.
func TestHistoryListsRunsFromBothLayouts(t *testing.T) {
	p, logs := djinnProjectInRepo(t, "djinn")
	if err := os.MkdirAll(p.LegacySummonerLogDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	h := NewHistory(NewStore(t.TempDir()), nil)

	writeLifecycle(t, p.LegacySummonerLogDir(), "2026-10-20.log",
		"2026-10-20T09:00:00Z\tdjinn-old\tspawn\n2026-10-20T09:10:00Z\tdjinn-old\texit\tCOMPLETE\tclosed\n")
	writeLifecycle(t, logs, "2026-11-08.log",
		"2026-11-08T09:00:00Z\tdjinn-new\tspawn\n2026-11-08T09:10:00Z\tdjinn-new\texit\tCOMPLETE\tclosed\n")

	runs, err := h.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want one from each layout: %+v", len(runs), runs)
	}
	if runs[0].Beads[0].ID != "djinn-new" || runs[1].Beads[0].ID != "djinn-old" {
		t.Errorf("order = %s, %s; want djinn-new (newest) first",
			runs[0].Beads[0].ID, runs[1].Beads[0].ID)
	}
}

// TestBuildRunIgnoresARoundRowForAnUnseenBead: only spawn, restart and exit
// create a bead; a round row is liveness, not membership.
func TestBuildRunIgnoresARoundRowForAnUnseenBead(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 8, 10, m, 0, 0, time.UTC) }
	run := BuildRun("ext-x", "djinn", SourceExternal, []LifecycleEvent{
		{At: at(0), Bead: "a", Kind: KindSpawn},
		{At: at(1), Bead: "a", Kind: KindRound, Verdict: "review-loop", Detail: "iter\t1\tpass"},
		{At: at(2), Bead: "x", Kind: KindRound, Verdict: "review-loop", Detail: "iter\t1\tpass"},
		{At: at(3), Bead: "a", Kind: KindExit, Verdict: VerdictComplete},
	})
	if len(run.Beads) != 1 || run.Beads[0].ID != "a" {
		t.Fatalf("beads = %+v, want only a", run.Beads)
	}
	if run.State != StateFinished {
		t.Errorf("state = %q, want finished", run.State)
	}
	if run.Beads[0].Verdict != VerdictComplete {
		t.Errorf("verdict = %q", run.Beads[0].Verdict)
	}
}
