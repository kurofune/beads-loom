package runs

import (
	"bufio"
	"context"
	"io"
	"os"
	"sort"
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

// Watcher tails a project's drains and publishes to the SSE hub.
//
// One Watcher per project. It follows two kinds of file: Loom's own console
// capture of a run it started (for log lines and cost), and summoner's per-day
// lifecycle logs in every SummonerLogDirs directory (for bead transitions, and
// as live log lines of every drain — for a drain started at a terminal they
// are the only record Loom can see).
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
	// lifecycleOffset is the bytes of each lifecycle log already consumed:
	// always the end of a complete line.
	lifecycleOffset map[string]int64
}

// freshLine is one newly complete lifecycle line.
type freshLine struct {
	path string
	// raw is the tab-separated text without its trailing newline.
	raw   string
	event LifecycleEvent
	// parsed is false for a line ParseLifecycle rejects.
	parsed bool
}

// NewWatcher builds a Watcher for one project.
func NewWatcher(p Project, mgr *Manager, history *History, hub Publisher) *Watcher {
	return &Watcher{
		project:         p,
		mgr:             mgr,
		history:         history,
		hub:             hub,
		pollInterval:    PollInterval,
		lifecycleOffset: map[string]int64{},
	}
}

// Run watches until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	if !w.project.IsDjinnProject() {
		return
	}

	// Lines already on disk are history, not news: start each log at its
	// last complete line, so a Loom start does not replay them.
	w.primeLifecycle()

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
	for _, dir := range append(w.project.SummonerLogDirs(), w.project.StateDir()) {
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

// tick does one pass: new lifecycle lines, then, for a managed run, new
// console output and the run's bead list.
//
// The lifecycle tail runs on every pass, managed run or not: a drain started
// at a terminal is exactly the case the Runs tab must follow live.
func (w *Watcher) tick(ctx context.Context) {
	fresh := w.tailLifecycle()

	run, active := w.mgr.Active(w.project.Name)
	if !active {
		w.reset()
		// No invalidation here. History.events keys its cache on each log's
		// (mtime, size), so a changed file is re-parsed on the next read
		// anyway; dropping the cache every 500ms just guaranteed that every
		// /runs request re-parsed every per-day log.
		w.streamExternal(fresh)
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

	// The summoner's repo lock allows one drain per repo, so a managed run owns
	// every lifecycle line in its window — the same window foldLifecycle uses,
	// so the streamed lines and run.Beads agree.
	since := runWindowStart(run)
	var lines []string
	for _, f := range fresh {
		if f.parsed && !f.event.At.Before(since) {
			lines = append(lines, f.raw)
		}
	}
	w.publishRunLog(run.ID, lines)
	w.foldLifecycle(run.ID, fresh)
}

// runWindowStart is the earliest lifecycle instant that belongs to a managed
// run. Lifecycle timestamps have second resolution and truncate downwards, so
// a worker spawned in the same second the run started logs an instant that
// compares as *before* run.StartedAt; widening the window to the start of that
// second is what keeps a run's first spawn from being dropped.
func runWindowStart(run Run) time.Time { return run.StartedAt.Truncate(time.Second) }

// isBeadTransition reports whether a lifecycle kind changes a bead's state.
// Round rows and kinds this code does not model are liveness only.
func isBeadTransition(kind string) bool {
	return kind == KindSpawn || kind == KindRestart || kind == KindExit
}

// lifecycleLogs is every per-day lifecycle log in every SummonerLogDirs
// directory, oldest filename first within each directory.
func (w *Watcher) lifecycleLogs() []string {
	var out []string
	for _, dir := range w.project.SummonerLogDirs() {
		paths, err := LifecycleLogsIn(dir)
		if err != nil {
			continue
		}
		sort.Strings(paths)
		out = append(out, paths...)
	}
	return out
}

// primeLifecycle sets every existing log's offset to the end of its last
// complete line. A line summoner is mid-way through writing stays unread, so
// it arrives whole once finished rather than as an unparseable suffix.
func (w *Watcher) primeLifecycle() {
	for _, path := range w.lifecycleLogs() {
		end, err := completeLinesEnd(path)
		if err != nil {
			continue
		}
		w.mu.Lock()
		w.lifecycleOffset[path] = end
		w.mu.Unlock()
	}
}

// completeLinesEnd is the byte offset just past the last newline in a file,
// or 0 when it holds none. It reads backwards, so a long log costs one chunk.
func completeLinesEnd(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	const chunk = 64 * 1024
	buf := make([]byte, chunk)
	for end := info.Size(); end > 0; {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && err != io.EOF {
			return 0, err
		}
		if i := strings.LastIndexByte(string(buf[:n]), '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}

// tailLifecycle reads the complete lines appended to every lifecycle log since
// the last pass. A log seen for the first time is read from its start; a
// partial final line waits for the next pass.
func (w *Watcher) tailLifecycle() []freshLine {
	var out []freshLine
	for _, path := range w.lifecycleLogs() {
		out = append(out, w.tailLog(path)...)
	}
	return out
}

func (w *Watcher) tailLog(path string) []freshLine {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}

	w.mu.Lock()
	offset := w.lifecycleOffset[path]
	w.mu.Unlock()
	if info.Size() < offset {
		// Truncated or replaced underneath us; start over, as drainConsole does.
		offset = 0
	}
	if info.Size() == offset {
		return nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}

	var (
		out      []freshLine
		consumed int64
	)
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		consumed += int64(len(line))
		raw := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		fl := freshLine{path: path, raw: raw}
		if events, _ := ParseLifecycle(strings.NewReader(raw)); len(events) == 1 {
			fl.event, fl.parsed = events[0], true
		}
		out = append(out, fl)
	}

	w.mu.Lock()
	w.lifecycleOffset[path] = offset + consumed
	w.mu.Unlock()
	return out
}

// streamExternal publishes fresh lifecycle lines of a drain Loom did not start,
// under the run id the Runs tab lists for each line's instant. A line that
// does not parse has no instant and is dropped.
func (w *Watcher) streamExternal(fresh []freshLine) {
	if len(fresh) == 0 || w.history == nil {
		return
	}
	// One List per pass, not one per line: RunAt's rule over the same list.
	runs, err := w.history.List(w.project)
	if err != nil {
		return
	}

	var order []string
	byRun := map[string][]string{}
	for _, f := range fresh {
		if !f.parsed {
			continue
		}
		run, ok := runAt(runs, f.event.At)
		if !ok {
			continue
		}
		if _, seen := byRun[run.ID]; !seen {
			order = append(order, run.ID)
		}
		byRun[run.ID] = append(byRun[run.ID], f.raw)
		if isBeadTransition(f.event.Kind) {
			w.publishBeadState(run.ID, f.event)
		}
	}
	for _, id := range order {
		w.publishRunLog(id, byRun[id])
	}
}

// publishRunLog publishes lines as run-log events, MaxLinesPerEvent at a time.
func (w *Watcher) publishRunLog(runID string, lines []string) {
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
}

func (w *Watcher) publishBeadState(runID string, e LifecycleEvent) {
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

	w.publishRunLog(runID, lines)

	if newCost > 0 {
		w.mgr.updateActive(w.project.Name, func(r *Run) { r.CostSoFar = cost })
	}
}

// foldLifecycle folds the active run's lifecycle events into its record and
// publishes bead-state transitions for the fresh ones.
//
// It reads every per-day log the run could have written to, not just the newest.
// summoner rotates its lifecycle log at midnight, and night drains are normal —
// the real 2026-06-16 log opens at 00:06. Reading only today's file after a
// rollover made every bead spawned before midnight vanish from run.Beads, and an
// exit-only row then recreated the bead with SpawnedAt set to its exit instant.
func (w *Watcher) foldLifecycle(runID string, fresh []freshLine) {
	run, ok := w.mgr.Active(w.project.Name)
	if !ok || run.ID != runID {
		return
	}

	var events []LifecycleEvent
	for _, dir := range w.project.SummonerLogDirs() {
		paths, err := LifecycleLogsSince(dir, run.StartedAt)
		if err != nil {
			continue
		}
		for _, path := range paths {
			parsed, err := ParseLifecycleFile(path)
			if err != nil {
				continue
			}
			events = append(events, parsed...)
		}
	}
	if len(events) == 0 {
		return
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })

	since := runWindowStart(run)

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

	for _, f := range fresh {
		if !f.parsed || f.event.At.Before(since) || !isBeadTransition(f.event.Kind) {
			continue
		}
		w.publishBeadState(runID, f.event)
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
