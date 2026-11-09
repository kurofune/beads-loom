package runs

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Tail cadences.
const (
	// PollInterval is the mtime poll. fsnotify misses events on some
	// filesystems (network mounts, some FUSE layers), so Loom does both: the
	// watcher makes updates instant, the poll makes them certain.
	PollInterval = 500 * time.Millisecond
	// MaxLinesPerEvent caps one run-log payload so a burst cannot produce a
	// multi-megabyte SSE frame. Emission cadence is the poll tick itself
	// (PollInterval), which already bounds run-log events to ~2/s — well inside
	// the ≤10/s the work package asks for, without a separate batching timer.
	MaxLinesPerEvent = 200
)

// Watcher tails an active run and publishes to the SSE hub.
//
// One Watcher per project. It follows two files: Loom's own console capture
// (for log lines and cost) and summoner's per-day lifecycle log (for bead
// spawn/exit transitions), because the two carry different information.
type Watcher struct {
	project Project
	mgr     *Manager
	history *History
	hub     Publisher

	pollInterval time.Duration

	mu sync.Mutex
	// consoleOffset is how far into the console capture we have read.
	consoleOffset int64
	consolePath   string
	// lastRunID detects a new run starting.
	lastRunID string
	// steps accumulates parsed costs for the active run.
	totalCost float64
	// lifecycleSeen counts events already folded into the run, per log path.
	lifecycleSeen map[string]int
}

// NewWatcher builds a Watcher for one project.
func NewWatcher(p Project, mgr *Manager, history *History, hub Publisher) *Watcher {
	return &Watcher{
		project:       p,
		mgr:           mgr,
		history:       history,
		hub:           hub,
		pollInterval:  PollInterval,
		lifecycleSeen: map[string]int{},
	}
}

// Run watches until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	if !w.project.IsDjinnProject() {
		return
	}

	// fsnotify is best-effort: if it cannot start, the poll still covers us.
	notify := make(chan struct{}, 1)
	stopWatch := w.startFSNotify(ctx, notify)
	defer stopWatch()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-notify:
		}
		w.tick(ctx)
	}
}

// startFSNotify watches the summoner-logs and state directories, signalling
// notify on any change. It returns a stop function that is safe to call even
// when the watcher never started.
func (w *Watcher) startFSNotify(ctx context.Context, notify chan<- struct{}) func() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return func() {}
	}

	added := false
	for _, dir := range []string{w.project.LegacySummonerLogDir(), w.project.StateDir()} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if err := watcher.Add(dir); err == nil {
			added = true
		}
	}
	if !added {
		_ = watcher.Close()
		return func() {}
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				select {
				case notify <- struct{}{}:
				default: // a signal is already pending
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return func() { _ = watcher.Close() }
}

// tick does one pass: new console output, then lifecycle transitions.
func (w *Watcher) tick(ctx context.Context) {
	run, active := w.mgr.Active(w.project.Name)
	if !active {
		w.reset()
		// No invalidation here. History.events keys its cache on each log's
		// (mtime, size), so a changed file is re-parsed on the next read
		// anyway; dropping the cache every 500ms just guaranteed that every
		// /runs request re-parsed every per-day log.
		return
	}

	w.mu.Lock()
	if w.lastRunID != run.ID {
		w.lastRunID = run.ID
		w.consoleOffset = 0
		w.totalCost = 0
		w.consolePath, _ = w.mgr.ActiveConsolePath(w.project.Name)
	}
	path := w.consolePath
	w.mu.Unlock()

	if path != "" {
		w.drainConsole(run.ID, path)
	}
	w.foldLifecycle(run.ID)
}

// drainConsole reads whatever is new in the console capture, publishing the
// stripped lines and updating the running cost.
func (w *Watcher) drainConsole(runID, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	w.mu.Lock()
	offset := w.consoleOffset
	w.mu.Unlock()

	info, err := f.Stat()
	if err != nil {
		return
	}
	if info.Size() < offset {
		// Truncated or rotated underneath us; start over rather than reading
		// from a nonsense position.
		offset = 0
	}
	if info.Size() == offset {
		return
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}

	var (
		lines    []string
		newCost  float64
		consumed int64
	)
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			// A partial final line means the writer is mid-write; leave it for
			// the next tick rather than emitting half a line.
			break
		}
		consumed += int64(len(line))
		stripped := StripANSI(strings.TrimRight(line, "\r\n"))
		if stripped != "" {
			lines = append(lines, stripped)
		}
		if step, ok := ParseStepLine(stripped); ok {
			newCost += step.CostUSD
		}
	}

	if consumed == 0 {
		return
	}

	w.mu.Lock()
	w.consoleOffset = offset + consumed
	w.totalCost += newCost
	cost := w.totalCost
	w.mu.Unlock()

	for start := 0; start < len(lines); start += MaxLinesPerEvent {
		end := start + MaxLinesPerEvent
		if end > len(lines) {
			end = len(lines)
		}
		w.publish(EventRunLog, map[string]any{
			"runId": runID,
			"lines": lines[start:end],
		})
	}

	if newCost > 0 {
		w.mgr.updateActive(w.project.Name, func(r *Run) { r.CostSoFar = cost })
	}
}

// foldLifecycle folds the active run's lifecycle events into its record and
// publishes bead-state transitions.
//
// It reads every per-day log the run could have written to, not just the newest.
// summoner rotates its lifecycle log at midnight, and night drains are normal —
// the real 2026-06-16 log opens at 00:06. Reading only today's file after a
// rollover made every bead spawned before midnight vanish from run.Beads, and an
// exit-only row then recreated the bead with SpawnedAt set to its exit instant.
func (w *Watcher) foldLifecycle(runID string) {
	run, ok := w.mgr.Active(w.project.Name)
	if !ok || run.ID != runID {
		return
	}

	paths, err := LifecycleLogsSince(w.project.LegacySummonerLogDir(), run.StartedAt)
	if err != nil || len(paths) == 0 {
		return
	}

	var (
		events []LifecycleEvent
		fresh  []LifecycleEvent
	)
	for _, path := range paths {
		parsed, err := ParseLifecycleFile(path)
		if err != nil {
			continue
		}
		events = append(events, parsed...)

		w.mu.Lock()
		seen := w.lifecycleSeen[path]
		if len(parsed) > seen {
			fresh = append(fresh, parsed[seen:]...)
			w.lifecycleSeen[path] = len(parsed)
		}
		w.mu.Unlock()
	}
	if len(events) == 0 {
		return
	}

	// Lifecycle timestamps have second resolution and truncate downwards, so a
	// worker spawned in the same second the run started logs an instant that
	// compares as *before* run.StartedAt. Widening the window to the start of
	// that second is what keeps a run's first spawn from being dropped.
	since := run.StartedAt.Truncate(time.Second)

	// Rebuild the bead list from the run's whole window rather than patching,
	// so retries and re-dispatches stay consistent with the log.
	var window []LifecycleEvent
	for _, e := range events {
		if !e.At.Before(since) {
			window = append(window, e)
		}
	}
	rebuilt := BuildRun(run.ID, run.Project, SourceLoom, window)

	updated, ok := w.mgr.updateActive(w.project.Name, func(r *Run) { r.Beads = rebuilt.Beads })
	if !ok {
		return
	}

	for _, e := range fresh {
		if e.At.Before(since) {
			continue
		}
		w.publish(EventBeadState, map[string]any{
			"project": w.project.Name,
			"runId":   runID,
			"bead":    e.Bead,
			"kind":    e.Kind,
			"verdict": e.Verdict,
			"detail":  e.Detail,
			"at":      e.At,
		})
	}

	// project is load-bearing: the frontend keys its cache invalidation on it,
	// and without it every mid-run update invalidated the key for project "",
	// which matches nothing. run-started and run-finished always carried it.
	w.publish(EventRunUpdated, map[string]any{
		"project":   w.project.Name,
		"runId":     runID,
		"beads":     updated.Beads,
		"costSoFar": updated.CostSoFar,
	})
	w.history.Invalidate(w.project)
}

func (w *Watcher) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastRunID = ""
	w.consoleOffset = 0
	w.consolePath = ""
	w.totalCost = 0
}

func (w *Watcher) publish(typ string, payload any) {
	if w.hub != nil {
		w.hub.Publish(typ, payload)
	}
}

// ReadLogChunk returns a slice of a run's console capture starting at offset.
//
// When ansi is false the text is ANSI-stripped, which is what the UI wants;
// ?ansi=1 gets the raw bytes for anyone who wants the colours.
func ReadLogChunk(path string, offset int64, limit int, ansi bool) (text string, next int64, eof bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", offset, false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", offset, false, err
	}
	if offset < 0 || offset > info.Size() {
		offset = info.Size()
	}
	if limit <= 0 || limit > maxLogChunk {
		limit = maxLogChunk
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, false, err
	}
	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", offset, false, err
	}
	chunk := string(buf[:n])
	if !ansi {
		chunk = StripANSI(chunk)
	}
	next = offset + int64(n)
	return chunk, next, next >= info.Size(), nil
}

// maxLogChunk caps one scrollback request.
const maxLogChunk = 256 * 1024
