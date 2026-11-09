package runs

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Lifecycle event kinds seen in real logs.
//
// The work package documents spawn and exit. Real logs also contain restart
// (13 occurrences across ~/Projects/djinn/.djinn/summoner-logs), which is how a
// retried worker shows up, so it is modelled rather than discarded.
const (
	KindSpawn   = "spawn"
	KindExit    = "exit"
	KindRestart = "restart"
	// KindRound is a formula loop's iteration, written by current djinn for
	// every round of every loop step. Its columns past the kind parse as
	// Verdict = "<loop>" and Detail = "iter\t<n>\t<state>". It is liveness: it
	// counts as activity when grouping runs but never creates a bead.
	KindRound = "round"
)

// LifecycleEvent is one TSV row of a per-day lifecycle log.
//
// Real shapes, verified against the logs:
//
//	2026-06-16T00:06:28-07:00\tdjinn-hmmp\tspawn
//	2026-06-16T00:17:06-07:00\tdjinn-zvjs\trestart
//	2026-06-08T01:30:56-07:00\tdjinn-78l6\texit\tCOMPLETE                 (4 fields)
//	2026-06-16T00:16:41-07:00\tdjinn-mxbm\texit\tCOMPLETE\tclosed         (5 fields)
//	2026-06-11T21:42:23-07:00\tdjinn-0e5h.1\texit\tCRASHED\texit status 3
type LifecycleEvent struct {
	At   time.Time `json:"at"`
	Bead string    `json:"bead"`
	Kind string    `json:"kind"`
	// Verdict is set on exit lines.
	Verdict string `json:"verdict,omitempty"`
	// Detail is the optional fifth column.
	Detail string `json:"detail,omitempty"`
	// Line is the 1-based source line, for error messages.
	Line int `json:"-"`
}

// lifecycleFilePattern matches a per-day lifecycle log: 2026-06-16.log.
// It deliberately excludes console-*.log and rerun-*.out, which live in the
// same directory but are different formats.
var lifecycleFilePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.log$`)

// IsLifecycleLog reports whether a filename is a per-day lifecycle log.
func IsLifecycleLog(name string) bool {
	return lifecycleFilePattern.MatchString(filepath.Base(name))
}

// ParseLifecycle parses a lifecycle log.
//
// Malformed lines are skipped rather than failing the parse: these logs are
// appended to by a live process, so a torn final line is normal and must not
// blank out an entire day's history.
func ParseLifecycle(r io.Reader) ([]LifecycleEvent, error) {
	var out []LifecycleEvent

	sc := bufio.NewScanner(r)
	// Detail columns are short, but give the scanner room for a long halt
	// reason rather than erroring mid-file.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[0]))
		if err != nil {
			continue
		}
		bead := strings.TrimSpace(fields[1])
		kind := strings.TrimSpace(fields[2])
		if bead == "" || kind == "" {
			continue
		}

		ev := LifecycleEvent{At: at.UTC(), Bead: bead, Kind: kind, Line: lineNo}
		if len(fields) >= 4 {
			ev.Verdict = strings.TrimSpace(fields[3])
		}
		if len(fields) >= 5 {
			// Rejoin any further tabs; a halt reason is free text.
			ev.Detail = strings.TrimSpace(strings.Join(fields[4:], "\t"))
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("read lifecycle log: %w", err)
	}
	return out, nil
}

// ParseLifecycleFile parses a lifecycle log from disk.
func ParseLifecycleFile(path string) ([]LifecycleEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open lifecycle log: %w", err)
	}
	defer f.Close()
	return ParseLifecycle(f)
}

// ExternalRunGap is how long a lull in lifecycle activity must last before the
// events after it are treated as a separate run.
//
// summoner polls bd ready every 30s by default and workers take minutes, so a
// ten-minute silence means the drain ended and a new one started later.
const ExternalRunGap = 10 * time.Minute

// AbandonedGap is how long an unexited worker may go silent before it is
// presumed dead rather than in flight. It matches staleExternalAfter, which
// history uses for the same judgement.
const AbandonedGap = 2 * time.Hour

// GroupRuns splits lifecycle events into runs by contiguous activity.
//
// Events must be from the same project. They are sorted by time first, so a log
// whose rows interleave across workers still groups correctly.
//
// A quiet gap only ends a run when no worker is still in flight. That
// qualification is essential rather than defensive: djinn workers routinely run
// far longer than the gap without writing a line — in the real 2026-06-16 log
// djinn-hmmp took 10m13s between its spawn and its exit — so a bare
// time-since-last-line rule would split a single bead's spawn from its own exit
// and invent a run that never happened.
//
// AbandonedGap is the backstop on that qualification. A worker whose summoner
// was killed never logs an exit, so "in flight" would otherwise hold a run open
// forever and swallow every later drain into one enormous fictional run. Past
// that threshold the worker is presumed dead and the gap splits regardless.
func GroupRuns(events []LifecycleEvent, gap time.Duration) [][]LifecycleEvent {
	if len(events) == 0 {
		return nil
	}
	if gap <= 0 {
		gap = ExternalRunGap
	}

	sorted := append([]LifecycleEvent(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	var groups [][]LifecycleEvent
	var current []LifecycleEvent
	// open tracks beads spawned in the current group that have not exited.
	open := map[string]bool{}
	// last is the latest timestamp in the group, not just the previous event,
	// so out-of-order rows cannot split a group spuriously.
	var last time.Time

	for _, ev := range sorted {
		since := ev.At.Sub(last)
		if len(current) > 0 && since > gap && (len(open) == 0 || since > AbandonedGap) {
			groups = append(groups, current)
			current = nil
			open = map[string]bool{}
		}

		current = append(current, ev)
		switch ev.Kind {
		case KindSpawn, KindRestart:
			open[ev.Bead] = true
		case KindExit:
			delete(open, ev.Bead)
		}
		if ev.At.After(last) {
			last = ev.At
		}
	}
	return append(groups, current)
}

// BuildRun folds a group of lifecycle events into a Run.
//
// The caller supplies the id, project and source; this function owns only what
// the log can tell it.
func BuildRun(id, project string, source RunSource, events []LifecycleEvent) Run {
	run := Run{
		ID:      id,
		Project: project,
		Source:  source,
		State:   StateFinished,
		Beads:   []RunBead{},
	}
	if len(events) == 0 {
		return run
	}

	// Index by bead, preserving first-spawn order.
	idx := map[string]*RunBead{}
	var order []string

	for _, ev := range events {
		b, ok := idx[ev.Bead]
		if !ok {
			if !isBeadTransition(ev.Kind) {
				// A round row (or any kind this code does not model) for a
				// bead the group does not hold is liveness, not membership.
				continue
			}
			b = &RunBead{ID: ev.Bead, SpawnedAt: ev.At}
			idx[ev.Bead] = b
			order = append(order, ev.Bead)
		}
		switch ev.Kind {
		case KindSpawn:
			// A second spawn after an exit means the bead was re-dispatched in
			// the same run; keep the earliest spawn and clear the exit.
			if b.ExitedAt != nil {
				b.ExitedAt = nil
				b.Verdict = ""
				b.FinalStatus = ""
				b.Retries++
			}
		case KindRestart:
			b.Retries++
			// A restart supersedes any recorded exit.
			b.ExitedAt = nil
		case KindExit:
			at := ev.At
			b.ExitedAt = &at
			b.Verdict = ev.Verdict
			b.FinalStatus = ev.Detail
		}
	}

	run.StartedAt = events[0].At
	latest := events[0].At
	for _, ev := range events {
		if ev.At.After(latest) {
			latest = ev.At
		}
	}

	active := 0
	for _, name := range order {
		b := *idx[name]
		if b.Active() {
			active++
		}
		run.Beads = append(run.Beads, b)
	}

	if active > 0 {
		// Beads spawned and never exited: from the log alone this run is still
		// going. Callers with process knowledge override this.
		run.State = StateRunning
	} else {
		end := latest
		run.EndedAt = &end
	}
	return run
}

// ExternalRunID derives a stable id for a synthesized run from its project and
// first event, so repeated scans of the same log produce the same id.
//
// The project is part of the id because the id is also the live stream's key:
// every project's watcher publishes run-log into one SSE hub, and the browser
// buffers lines by runId alone. Two projects whose drains start in the same
// second, or a checkout registered beside its own linked worktree (both read
// one common-dir log), would otherwise fill one buffer with each other's
// lines. The suffix is a hash, not the name, because a project name may hold
// characters a run id may not.
func ExternalRunID(project string, first time.Time) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(project))
	return fmt.Sprintf("%s-%08x", legacyExternalRunID(first), h.Sum32())
}

// legacyExternalRunID is the id external runs had before it named a project.
// Citations and links written then still carry it; History.Get resolves it.
func legacyExternalRunID(first time.Time) string {
	return "ext-" + first.UTC().Format("20060102T150405Z")
}

// LogDate extracts the date encoded in a per-day lifecycle log's filename.
func LogDate(path string) (time.Time, bool) {
	m := lifecycleFilePattern.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return time.Time{}, false
	}
	d, err := time.Parse("2006-01-02", m[1])
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}

// LifecycleLogsSince returns the per-day logs a run starting at `since` could
// have written to, oldest first.
//
// summoner rotates its lifecycle log at midnight *local* time while `since` is
// UTC, so the filter is deliberately generous by a day on each side: including
// one extra file costs a cheap re-parse, excluding the wrong one loses half a
// drain. The filenames themselves are the only date information available.
func LifecycleLogsSince(dir string, since time.Time) ([]string, error) {
	all, err := LifecycleLogsIn(dir)
	if err != nil {
		return nil, err
	}
	cutoff := since.UTC().AddDate(0, 0, -1)

	var out []string
	for _, path := range all {
		d, ok := LogDate(path)
		if !ok || d.Before(cutoff) {
			continue
		}
		out = append(out, path)
	}
	// LifecycleLogsIn returns newest first; callers here want chronological.
	sort.Strings(out)
	return out, nil
}

// LifecycleLogsIn lists the per-day lifecycle logs in a directory, newest
// filename first. Console logs and rerun captures are ignored.
func LifecycleLogsIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read summoner-logs: %w", err)
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() || !IsLifecycleLog(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	// Filenames are ISO dates, so lexical descending is newest first.
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}
