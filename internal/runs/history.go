package runs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Project is one djinn-enabled workspace, as history needs it.
type Project struct {
	Name    string
	Workdir string
}

// DjinnDir is <workdir>/.djinn.
func (p Project) DjinnDir() string { return filepath.Join(p.Workdir, ".djinn") }

// LegacySummonerLogDir is where summoner wrote its per-day lifecycle logs
// before djinn moved its drain records under the git common dir. A repo that
// drained before the move still has its history here.
func (p Project) LegacySummonerLogDir() string {
	return filepath.Join(p.DjinnDir(), "summoner-logs")
}

// RunDir is djinn's drain-record directory, <git common dir>/djinn. It is
// shared by every worktree of the repo; it errors when the workdir is in no
// git repo.
func (p Project) RunDir() (string, error) {
	common, err := GitCommonDir(p.Workdir)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "djinn"), nil
}

// SummonerLogDirs lists the directories that may hold per-day lifecycle logs,
// in read order: the current location under RunDir when it resolves, then the
// legacy one. A directory that does not exist stays in the list; readers treat
// it as empty.
func (p Project) SummonerLogDirs() []string {
	var dirs []string
	if run, err := p.RunDir(); err == nil {
		dirs = append(dirs, filepath.Join(run, "summoner-logs"))
	}
	return append(dirs, p.LegacySummonerLogDir())
}

// StateDir is where djinn workers write per-bead state.
func (p Project) StateDir() string { return filepath.Join(p.DjinnDir(), "state") }

// IsDjinnProject reports whether the project is a djinn project at all.
func (p Project) IsDjinnProject() bool {
	st, err := os.Stat(p.DjinnDir())
	return err == nil && st.IsDir()
}

// cachedLog is a parsed lifecycle log, keyed by path and invalidated by mtime.
type cachedLog struct {
	modTime time.Time
	size    int64
	events  []LifecycleEvent
}

// History assembles the run list for a project from Loom's own records plus
// djinn's lifecycle logs.
//
// The external-run half is what makes Loom a window onto djinn rather than the
// only door: a drain someone started at a terminal shows up here too.
type History struct {
	store *Store
	mgr   *Manager

	mu    sync.Mutex
	cache map[string]cachedLog
}

// NewHistory builds a History.
func NewHistory(store *Store, mgr *Manager) *History {
	return &History{store: store, mgr: mgr, cache: map[string]cachedLog{}}
}

// events parses a lifecycle log, reusing the cached parse when the file has not
// changed. These logs are append-only and re-read on every history request, so
// caching by (mtime, size) keeps a busy project cheap.
func (h *History) events(path string) ([]LifecycleEvent, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	if c, ok := h.cache[path]; ok && c.modTime.Equal(info.ModTime()) && c.size == info.Size() {
		events := c.events
		h.mu.Unlock()
		return events, nil
	}
	h.mu.Unlock()

	events, err := ParseLifecycleFile(path)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	h.cache[path] = cachedLog{modTime: info.ModTime(), size: info.Size(), events: events}
	h.mu.Unlock()
	return events, nil
}

// List returns every run for a project, newest first: Loom's persisted records
// merged with runs synthesized from djinn's lifecycle logs.
func (h *History) List(p Project) ([]Run, error) {
	if !p.IsDjinnProject() {
		return nil, ErrNoDjinn
	}

	managed, err := h.store.List(p.Name)
	if err != nil {
		return nil, err
	}

	// A managed run that is still live has fresher state in the manager than on
	// disk, so overlay it.
	if h.mgr != nil {
		if active, ok := h.mgr.Active(p.Name); ok {
			replaced := false
			for i := range managed {
				if managed[i].ID == active.ID {
					managed[i] = active
					replaced = true
					break
				}
			}
			if !replaced {
				managed = append(managed, active)
			}
		}
	}

	external, err := h.externalRuns(p, managed)
	if err != nil {
		return nil, err
	}

	all := append(managed, external...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].StartedAt.After(all[j].StartedAt) })
	if all == nil {
		all = []Run{}
	}
	return all, nil
}

// managedWindow is one Loom run's time span, used to decide which lifecycle
// activity already belongs to a known run.
type managedWindow struct {
	start time.Time
	end   time.Time
}

// externalRuns synthesizes runs from lifecycle activity that no managed run
// accounts for.
func (h *History) externalRuns(p Project, managed []Run) ([]Run, error) {
	// Both layouts: djinn's current one under the git common dir and the
	// legacy <workdir>/.djinn/summoner-logs. A repo that drained before and
	// after the move has history in each, and GroupRuns sorts by time anyway.
	var logs []string
	for _, dir := range p.SummonerLogDirs() {
		in, err := LifecycleLogsIn(dir)
		if err != nil {
			return nil, err
		}
		logs = append(logs, in...)
	}
	if len(logs) == 0 {
		return nil, nil
	}

	windows := make([]managedWindow, 0, len(managed))
	for _, r := range managed {
		w := managedWindow{start: r.StartedAt}
		if r.EndedAt != nil {
			w.end = *r.EndedAt
		} else {
			// Still running: it owns everything from its start onwards.
			w.end = time.Now().UTC().Add(time.Hour)
		}
		windows = append(windows, w)
	}

	// Concatenate every per-day log before grouping, rather than grouping each
	// file separately. summoner rotates at midnight and night drains are normal
	// (the real 2026-06-16 log opens at 00:06), so a drain that crosses midnight
	// writes its spawns in one file and its exits in the next. Grouping per file
	// split that into two fictional runs: the first declared "failed — no exit
	// recorded" though it finished fine, the second full of beads that appeared
	// to spawn at the instant they exited.
	//
	// GroupRuns already handles the seam: consecutive days that are genuinely
	// one drain have no gap at the boundary, and days that are not are separated
	// by hours of silence.
	var all []LifecycleEvent
	for _, path := range logs {
		events, err := h.events(path)
		if err != nil {
			// A log we cannot read must not blank out the rest of the history.
			continue
		}
		all = append(all, events...)
	}
	if len(all) == 0 {
		return nil, nil
	}

	var out []Run
	for _, group := range GroupRuns(all, ExternalRunGap) {
		if len(group) == 0 || overlapsManaged(group, windows) {
			continue
		}
		id := ExternalRunID(group[0].At)
		run := BuildRun(id, p.Name, SourceExternal, group)
		// An external run whose beads never exited is only "running" if the
		// activity is recent; otherwise summoner died and nobody logged it.
		if run.State == StateRunning && time.Since(lastEventTime(group)) > staleExternalAfter {
			run.State = StateFailed
			end := lastEventTime(group)
			run.EndedAt = &end
			run.ExitError = "no exit recorded; the run appears to have died"
		}
		out = append(out, run)
	}
	return out, nil
}

// staleExternalAfter is how long an external run with unexited beads may go
// quiet before it is presumed dead rather than live.
const staleExternalAfter = 2 * time.Hour

func lastEventTime(events []LifecycleEvent) time.Time {
	latest := events[0].At
	for _, e := range events[1:] {
		if e.At.After(latest) {
			latest = e.At
		}
	}
	return latest
}

// overlapsManaged reports whether a group of lifecycle events falls inside a
// window Loom already has a record for.
func overlapsManaged(group []LifecycleEvent, windows []managedWindow) bool {
	if len(windows) == 0 {
		return false
	}
	first := group[0].At
	last := lastEventTime(group)
	for _, w := range windows {
		// Any overlap at all means this activity is the managed run's.
		if !last.Before(w.start) && !first.After(w.end) {
			return true
		}
	}
	return false
}

// Get returns one run with its live detail.
func (h *History) Get(p Project, runID string) (*RunDetail, error) {
	if !p.IsDjinnProject() {
		return nil, ErrNoDjinn
	}
	if !ValidID(runID) {
		return nil, fmt.Errorf("%w: invalid id", ErrRunNotFound)
	}

	runs, err := h.List(p)
	if err != nil {
		return nil, err
	}
	var found *Run
	for i := range runs {
		if runs[i].ID == runID {
			found = &runs[i]
			break
		}
	}
	if found == nil {
		return nil, ErrRunNotFound
	}

	detail := &RunDetail{Run: *found, ActiveSlots: []WorkerSlot{}}

	// Worker slots: beads spawned but not exited, plus the step each is on.
	stateDir := p.StateDir()
	for _, b := range found.Beads {
		if !b.Active() {
			continue
		}
		slot := WorkerSlot{Bead: b.ID, SpawnedAt: b.SpawnedAt}
		if f, err := ReadForensics(stateDir, b.ID); err == nil {
			slot.CurrentStep = LatestStep(f.Timeline)
		}
		detail.ActiveSlots = append(detail.ActiveSlots, slot)
	}

	if found.Caps == nil {
		if caps, err := ReadCostCaps(p.DjinnDir()); err == nil {
			detail.Caps = caps
		}
	}

	if IsLoomRunID(runID) {
		if path, err := h.store.ConsolePath(p.Name, runID); err == nil {
			if _, statErr := os.Stat(path); statErr == nil {
				detail.LogPath = path
				// Refresh cost and summary from the capture for a live run.
				if found.State == StateRunning || found.Summary == nil {
					if parsed := parseConsoleFile(path); parsed != nil {
						detail.CostSoFar = parsed.TotalCost
						if parsed.Summary != nil {
							detail.Summary = parsed.Summary
						}
					}
				}
			}
		}
	}
	return detail, nil
}

func parseConsoleFile(path string) *ConsoleParse {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	parsed, _ := ParseConsole(f)
	return parsed
}

// Invalidate drops the cached parse for a project's logs, so the next read
// re-parses. The fsnotify watcher calls this on change.
func (h *History) Invalidate(p Project) {
	dirs := map[string]bool{}
	for _, dir := range p.SummonerLogDirs() {
		dirs[dir] = true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for path := range h.cache {
		if dirs[filepath.Dir(path)] {
			delete(h.cache, path)
		}
	}
}

// RunAt is the run in List(p) that an instant belongs to: the newest run whose
// StartedAt is not after at. The watcher uses it to route a lifecycle line of a
// drain Loom did not start to the same run id the Runs tab shows.
func (h *History) RunAt(p Project, at time.Time) (Run, bool) {
	runs, err := h.List(p)
	if err != nil {
		return Run{}, false
	}
	// List is newest first.
	for _, r := range runs {
		if !r.StartedAt.After(at) {
			return r, true
		}
	}
	return Run{}, false
}
