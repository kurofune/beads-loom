package runs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// appendLog appends text to a lifecycle log, creating it if needed.
func appendLog(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// runLogLines is every run-log line published so far, with its run id.
func (h *recordingHub) runLogLines() (ids []string, lines []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.events {
		if e.Type != EventRunLog {
			continue
		}
		p := e.Payload.(map[string]any)
		for _, l := range p["lines"].([]string) {
			ids = append(ids, p["runId"].(string))
			lines = append(lines, l)
		}
	}
	return ids, lines
}

// beadStates is every bead-state payload published so far.
func (h *recordingHub) beadStates() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]any
	for _, e := range h.events {
		if e.Type == EventBeadState {
			out = append(out, e.Payload.(map[string]any))
		}
	}
	return out
}

func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// TestWatcherStreamsAnExternalLifecycleLine: a drain started at a terminal has
// no managed run, and its lifecycle lines are all Loom can see of it. Each
// appended line reaches the run's live log under the id the Runs tab shows.
func TestWatcherStreamsAnExternalLifecycleLine(t *testing.T) {
	p, logs := djinnProjectInRepo(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	h := NewHistory(m.Store(), m)
	w := NewWatcher(p, m, h, hub)

	t0 := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	t1 := t0.Add(30 * time.Second)
	path := filepath.Join(logs, t0.Format("2006-01-02")+".log")
	appendLog(t, path, stamp(t0)+"\tdjinn-a\tspawn\n")
	w.tick(t.Context())
	_, before := hub.runLogLines()
	beforeStates := len(hub.beadStates())

	exit := stamp(t1) + "\tdjinn-a\texit\tCOMPLETE\tclosed"
	appendLog(t, path, exit+"\n")
	w.tick(t.Context())

	ids, lines := hub.runLogLines()
	lines, ids = lines[len(before):], ids[len(before):]
	if len(lines) != 1 || lines[0] != exit {
		t.Fatalf("run-log lines = %q, want exactly %q", lines, exit)
	}
	if ids[0] != ExternalRunID(t0) {
		t.Errorf("runId = %q, want %q", ids[0], ExternalRunID(t0))
	}
	states := hub.beadStates()[beforeStates:]
	if len(states) != 1 {
		t.Fatalf("bead-state events = %+v, want one exit", states)
	}
	s := states[0]
	if s["runId"] != ExternalRunID(t0) || s["bead"] != "djinn-a" || s["kind"] != KindExit ||
		s["verdict"] != VerdictComplete || s["project"] != p.Name {
		t.Errorf("bead-state = %+v", s)
	}

	// A torn line waits for its newline.
	t2 := stamp(t1.Add(time.Second)) + "\tdjinn-b\tspawn"
	appendLog(t, path, t2)
	w.tick(t.Context())
	if _, l := hub.runLogLines(); len(l) != len(before)+1 {
		t.Fatalf("a line with no newline was streamed: %q", l[len(before)+1:])
	}
	appendLog(t, path, "\n")
	w.tick(t.Context())
	if _, l := hub.runLogLines(); len(l) != len(before)+2 || l[len(l)-1] != t2 {
		t.Errorf("completed line not streamed once: %q", l[len(before):])
	}
}

// TestWatcherStreamsAManagedLifecycleLine: during a Loom-managed run, a
// lifecycle line reaches the run's live log too, not only its console text.
func TestWatcherStreamsAManagedLifecycleLine(t *testing.T) {
	p, logs := djinnProjectInRepo(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	h := NewHistory(m.Store(), m)
	t.Setenv("FAKE_SUMMONER_MODE", "long")

	run, err := m.Summon(t.Context(), p.Name, p.Workdir, SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(p.Name, run.ID) })
	waitForConsole(t, m, p.Name, run.ID, "trap installed", 5*time.Second)

	w := NewWatcher(p, m, h, hub)
	w.tick(t.Context())

	spawnAt := run.StartedAt.Truncate(time.Second)
	path := filepath.Join(logs, spawnAt.UTC().Format("2006-01-02")+".log")
	appendLog(t, path, stamp(spawnAt)+"\tdjinn-first\tspawn\n")
	w.tick(t.Context())

	var sawSpawn bool
	for _, s := range hub.beadStates() {
		if s["bead"] == "djinn-first" && s["kind"] == KindSpawn && s["runId"] == run.ID {
			sawSpawn = true
		}
	}
	if !sawSpawn {
		t.Fatalf("no bead-state spawn for the managed run; saw %v", hub.types())
	}
	if active, ok := m.Active(p.Name); !ok || len(active.Beads) != 1 || active.Beads[0].ID != "djinn-first" {
		t.Fatalf("active beads = %+v", active.Beads)
	}

	exit := stamp(time.Now()) + "\tdjinn-lifecycle-only\texit\tCOMPLETE\tclosed"
	appendLog(t, path, exit+"\n")
	w.tick(t.Context())
	w.tick(t.Context())

	ids, lines := hub.runLogLines()
	n := 0
	for i, l := range lines {
		if l == exit {
			n++
			if ids[i] != run.ID {
				t.Errorf("lifecycle line streamed under %q, want %q", ids[i], run.ID)
			}
		}
	}
	if n != 1 {
		t.Fatalf("lifecycle line streamed %d times, want once; lines = %q", n, lines)
	}
	exits := 0
	for _, s := range hub.beadStates() {
		if s["bead"] == "djinn-lifecycle-only" && s["kind"] == KindExit {
			exits++
			if s["runId"] != run.ID || s["verdict"] != VerdictComplete {
				t.Errorf("bead-state = %+v", s)
			}
		}
	}
	if exits != 1 {
		t.Errorf("bead-state exits = %d, want 1", exits)
	}

	// A line from before the run started belongs to no one.
	early := stamp(run.StartedAt.Add(-time.Minute)) + "\tdjinn-early\tspawn"
	appendLog(t, path, early+"\n")
	w.tick(t.Context())
	_, lines = hub.runLogLines()
	for _, l := range lines {
		if l == early {
			t.Errorf("a line before the run's start was streamed: %q", l)
		}
	}
	for _, s := range hub.beadStates() {
		if s["bead"] == "djinn-early" {
			t.Errorf("a line before the run's start published bead-state: %+v", s)
		}
	}
}

// runWatcher starts w.Run with a fast poll and stops it at test end.
func runWatcher(t *testing.T, w *Watcher) {
	t.Helper()
	w.pollInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// TestWatcherRunDoesNotReplayHistoryAtStart: a Loom start streams only what is
// written after it, not every line already on disk.
func TestWatcherRunDoesNotReplayHistoryAtStart(t *testing.T) {
	p, logs := djinnProjectInRepo(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	w := NewWatcher(p, m, NewHistory(m.Store(), m), hub)

	t0 := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	path := filepath.Join(logs, t0.Format("2006-01-02")+".log")
	appendLog(t, path, stamp(t0)+"\tdjinn-a\tspawn\n"+stamp(t0.Add(time.Second))+"\tdjinn-b\tspawn\n")

	runWatcher(t, w)
	time.Sleep(5 * w.pollInterval)
	if n := hub.count(EventRunLog); n != 0 {
		t.Fatalf("%d run-log events before any append; history was replayed", n)
	}

	line := stamp(t0.Add(2*time.Second)) + "\tdjinn-a\texit\tCOMPLETE\tclosed"
	appendLog(t, path, line+"\n")
	hub.waitFor(t, EventRunLog, 3*time.Second)
	if _, lines := hub.runLogLines(); len(lines) != 1 || lines[0] != line {
		t.Errorf("run-log lines = %q, want only %q", lines, line)
	}
}

// TestWatcherRunCompletesALineTornAtStartup: Loom can start while summoner is
// mid-write. The torn tail must arrive whole once finished, not be lost as an
// unparseable suffix.
func TestWatcherRunCompletesALineTornAtStartup(t *testing.T) {
	p, logs := djinnProjectInRepo(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	w := NewWatcher(p, m, NewHistory(m.Store(), m), hub)

	t0 := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	path := filepath.Join(logs, t0.Format("2006-01-02")+".log")
	full := stamp(t0.Add(2*time.Second)) + "\tdjinn-a\texit\tCOMPLETE\tclosed"
	appendLog(t, path, stamp(t0)+"\tdjinn-a\tspawn\n"+
		stamp(t0.Add(time.Second))+"\tdjinn-b\tspawn\n"+full[:len(full)-9])

	runWatcher(t, w)
	time.Sleep(5 * w.pollInterval)
	if n := hub.count(EventRunLog); n != 0 {
		t.Fatalf("%d run-log events before the torn line finished", n)
	}

	appendLog(t, path, full[len(full)-9:]+"\n")
	hub.waitFor(t, EventRunLog, 3*time.Second)
	time.Sleep(5 * w.pollInterval)
	if _, lines := hub.runLogLines(); len(lines) != 1 || lines[0] != full {
		t.Errorf("run-log lines = %q, want exactly the completed line %q", lines, full)
	}
}

// TestWatcherRereadsALogTruncatedToEmpty: a log cut to zero bytes and then
// regrown past its old length is read from its start. A tick that saw it empty
// must forget the old offset, or the regrown log is read from that stale
// position and its first lines are lost for good.
func TestWatcherRereadsALogTruncatedToEmpty(t *testing.T) {
	p, logs := djinnProjectInRepo(t, "djinn")
	hub := &recordingHub{}
	m := newTestManager(t, hub)
	w := NewWatcher(p, m, NewHistory(m.Store(), m), hub)

	t0 := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	path := filepath.Join(logs, t0.Format("2006-01-02")+".log")
	appendLog(t, path, stamp(t0)+"\tdjinn-a\tspawn\n")
	w.tick(t.Context())

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	w.tick(t.Context())
	_, before := hub.runLogLines()

	var want []string
	for i, bead := range []string{"djinn-first", "djinn-second", "djinn-third"} {
		line := stamp(t0.Add(time.Duration(i+1)*time.Second)) + "\t" + bead + "\tspawn"
		want = append(want, line)
		appendLog(t, path, line+"\n")
	}
	w.tick(t.Context())
	w.tick(t.Context())

	_, lines := hub.runLogLines()
	got := lines[len(before):]
	if len(got) != len(want) {
		t.Fatalf("lines after regrowth = %q, want each of %q once", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}
