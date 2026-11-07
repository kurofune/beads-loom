package runs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// StopGrace is how long a stopped run has to exit after SIGTERM before it is
// killed. summoner has to tear down worker subprocesses and their worktrees, so
// this is generous on purpose.
const StopGrace = 30 * time.Second

// SummonerBinary is the executable Loom spawns.
const SummonerBinary = "summoner"

// Errors the API maps to specific status codes.
var (
	// ErrSummonerMissing means the binary is not on PATH.
	ErrSummonerMissing = errors.New("summoner not found in PATH")
	// ErrRunActive means a managed run is already going for this project.
	ErrRunActive = errors.New("a run is already active for this project")
	// ErrNoDjinn means the project has no .djinn directory.
	ErrNoDjinn = errors.New("project is not a djinn project")
	// ErrRunNotFound means no such run id.
	ErrRunNotFound = errors.New("run not found")
	// ErrNotManaged means the run was not spawned by Loom, so it cannot be
	// stopped from here.
	ErrNotManaged = errors.New("run is not managed by Loom")
)

// Publisher is the SSE hub, narrowed to what runs needs.
type Publisher interface {
	Publish(typ string, payload any)
}

// SSE event types this package emits.
const (
	EventRunStarted  = "run-started"
	EventRunLog      = "run-log"
	EventRunUpdated  = "run-updated"
	EventRunFinished = "run-finished"
	EventBeadState   = "bead-state"
)

// SummonRequest is a dispatch request.
type SummonRequest struct {
	Beads      []string
	Epic       string
	MaxWorkers int
	MaxBeads   int
	NoPush     bool
}

// Args renders the summoner argv for this request.
//
// summoner uses Go's flag package, so single-dash long flags are the native
// form; both `-bead` and `--bead` parse, and the single-dash form is what
// `summoner --help` documents.
func (r SummonRequest) Args() []string {
	var args []string
	for _, b := range r.Beads {
		args = append(args, "-bead", b)
	}
	if r.Epic != "" {
		args = append(args, "-epic", r.Epic)
	}
	if r.MaxWorkers > 0 {
		args = append(args, "-max-workers", strconv.Itoa(r.MaxWorkers))
	}
	if r.MaxBeads > 0 {
		args = append(args, "-max-beads", strconv.Itoa(r.MaxBeads))
	}
	if r.NoPush {
		args = append(args, "-no-push")
	}
	return args
}

// PreviewArgs renders the argv for a dry-run or preflight preview.
//
// --dry-run and --preflight are mutually exclusive per summoner's own help, so
// preflight wins when both are somehow requested.
func (r SummonRequest) PreviewArgs(preflight bool) []string {
	args := r.Args()
	if preflight {
		return append(args, "-preflight")
	}
	return append(args, "-dry-run")
}

// managedRun is a live child process.
type managedRun struct {
	run    Run
	cmd    *exec.Cmd
	cancel context.CancelFunc

	consolePath string
	// stopping records that SIGTERM has been sent.
	stopping bool
	done     chan struct{}
}

// Manager owns Loom-spawned summoner processes and the run store.
//
// It never spawns anything on its own: Summon is only ever reached from an
// explicit user confirmation in the UI.
type Manager struct {
	store *Store
	hub   Publisher

	// lookPath is exec.LookPath, swappable in tests.
	lookPath func(string) (string, error)
	// binary overrides the executable, for the fake-summoner tests.
	binary string
	// stopGrace is StopGrace, shortened in tests.
	stopGrace time.Duration
	// now is time.Now, fixed in tests.
	now func() time.Time

	mu     sync.RWMutex
	active map[string]*managedRun // by project
	// reaping holds every spawned run until wait has saved its final record.
	// A run leaves active before that save, so Shutdown waits on this set.
	reaping map[*managedRun]struct{}
}

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	Store     *Store
	Hub       Publisher
	Binary    string
	StopGrace time.Duration
	Now       func() time.Time
	LookPath  func(string) (string, error)
}

// NewManager builds a Manager.
func NewManager(opts ManagerOptions) *Manager {
	m := &Manager{
		store:     opts.Store,
		hub:       opts.Hub,
		binary:    opts.Binary,
		stopGrace: opts.StopGrace,
		now:       opts.Now,
		lookPath:  opts.LookPath,
		active:    make(map[string]*managedRun),
		reaping:   make(map[*managedRun]struct{}),
	}
	if m.store == nil {
		m.store = NewStore("")
	}
	if m.binary == "" {
		m.binary = SummonerBinary
	}
	if m.stopGrace <= 0 {
		m.stopGrace = StopGrace
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.lookPath == nil {
		m.lookPath = exec.LookPath
	}
	return m
}

// Store exposes the run store.
func (m *Manager) Store() *Store { return m.store }

// ResolveBinary returns the summoner path, or ErrSummonerMissing.
func (m *Manager) ResolveBinary() (string, error) {
	if filepath.IsAbs(m.binary) {
		if _, err := os.Stat(m.binary); err != nil {
			return "", ErrSummonerMissing
		}
		return m.binary, nil
	}
	path, err := m.lookPath(m.binary)
	if err != nil {
		return "", ErrSummonerMissing
	}
	return path, nil
}

// Preview runs summoner in dry-run or preflight mode and parses the result.
//
// Both modes are documented as non-mutating: they resolve the scope, print, and
// exit without spawning workers, contacting a remote, or touching bd state.
func (m *Manager) Preview(ctx context.Context, workdir string, req SummonRequest, preflight bool) (*SummonPreview, error) {
	bin, err := m.ResolveBinary()
	if err != nil {
		return nil, err
	}

	args := req.PreviewArgs(preflight)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()

	var stdout, stderr safeBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	outText, errText := stdout.String(), stderr.String()

	// A non-zero exit still carries usable output (summoner warns and exits 0
	// for unknown beads, but a scope error exits non-zero with an explanation).
	if runErr != nil && outText == "" {
		return nil, fmt.Errorf("summoner %v: %w: %s", args, runErr, trimForError(errText))
	}

	var preview SummonPreview
	if preflight {
		preview = ParsePreflight(outText, errText, req.Beads)
	} else {
		preview = ParseDryRun(outText, errText)
	}

	// Requested beads that did not come back are skipped, whether or not
	// summoner printed a warning for them. Diffing the request against the
	// result is more reliable than parsing warning text.
	if len(req.Beads) > 0 {
		got := make(map[string]bool, len(preview.Ordered))
		for _, id := range preview.Ordered {
			got[id] = true
		}
		warned := make(map[string]bool, len(preview.Warnings))
		for _, w := range preview.Warnings {
			warned[w.Bead] = true
		}
		for _, want := range req.Beads {
			if !got[want] && !warned[want] {
				preview.Warnings = append(preview.Warnings, PreviewWarning{
					Bead:   want,
					Reason: "not in the ready set (unknown or not ready); skipping",
				})
			}
		}
	}
	return &preview, nil
}

// Summon spawns a managed summoner run.
//
// The child is placed in its own process group so stopping it takes the whole
// worker tree down, not just the parent.
func (m *Manager) Summon(ctx context.Context, project, workdir string, req SummonRequest) (*Run, error) {
	bin, err := m.ResolveBinary()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.active[project]; ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrRunActive, existing.run.ID)
	}
	// Reserve the slot before doing anything slow, so two confirms racing each
	// other cannot both spawn.
	placeholder := &managedRun{done: make(chan struct{})}
	m.active[project] = placeholder
	m.mu.Unlock()

	release := func() {
		m.mu.Lock()
		if m.active[project] == placeholder {
			delete(m.active, project)
		}
		m.mu.Unlock()
	}

	started := m.now().UTC()
	runID := NewRunID(started.UnixNano())
	args := req.Args()

	consolePath, err := m.store.ConsolePath(project, runID)
	if err != nil {
		release()
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(consolePath), 0o755); err != nil {
		release()
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	consoleFile, err := os.Create(consolePath)
	if err != nil {
		release()
		return nil, fmt.Errorf("create console capture: %w", err)
	}

	// The run outlives the HTTP request that started it, so it gets its own
	// context rather than inheriting the request's.
	runCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	cmd.Stdout = consoleFile
	cmd.Stderr = consoleFile
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		cancel()
		consoleFile.Close()
		release()
		return nil, fmt.Errorf("start summoner: %w", err)
	}

	run := Run{
		ID:        runID,
		Project:   project,
		Source:    SourceLoom,
		StartedAt: started,
		State:     StateRunning,
		Args:      args,
		Beads:     []RunBead{},
	}
	if caps, err := ReadCostCaps(filepath.Join(workdir, ".djinn")); err == nil {
		run.Caps = caps
	}

	mr := &managedRun{
		run:         run,
		cmd:         cmd,
		cancel:      cancel,
		consolePath: consolePath,
		done:        make(chan struct{}),
	}

	m.mu.Lock()
	m.active[project] = mr
	m.reaping[mr] = struct{}{}
	m.mu.Unlock()

	if err := m.store.Save(run); err != nil {
		// Not fatal: the process is running and matters more than the record.
		m.publish(EventRunLog, map[string]any{
			"runId": runID,
			"lines": []string{"loom: could not persist the run record: " + err.Error()},
		})
	}

	m.publish(EventRunStarted, map[string]any{"project": project, "runId": runID})

	go m.wait(project, mr, consoleFile)
	return &run, nil
}

// wait reaps the child and finalizes the run record.
func (m *Manager) wait(project string, mr *managedRun, consoleFile *os.File) {
	defer func() {
		m.mu.Lock()
		delete(m.reaping, mr)
		m.mu.Unlock()
		close(mr.done)
	}()

	err := mr.cmd.Wait()
	consoleFile.Close()
	mr.cancel()

	ended := m.now().UTC()

	m.mu.Lock()
	run := mr.run
	run.EndedAt = &ended
	switch {
	case mr.stopping:
		run.State = StateStopped
	case err != nil:
		run.State = StateFailed
		run.ExitError = err.Error()
	default:
		run.State = StateFinished
	}
	mr.run = run
	if m.active[project] == mr {
		delete(m.active, project)
	}
	m.mu.Unlock()

	// Fold in whatever the console capture recorded before finishing.
	if parsed := m.parseConsole(mr.consolePath); parsed != nil {
		run.Summary = parsed.Summary
		run.CostSoFar = parsed.TotalCost
	}
	_ = m.store.Save(run)

	m.publish(EventRunFinished, map[string]any{
		"runId":   run.ID,
		"project": project,
		"state":   run.State,
		"summary": run.Summary,
	})
}

func (m *Manager) parseConsole(path string) *ConsoleParse {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	parsed, err := ParseConsole(f)
	if err != nil {
		return parsed
	}
	return parsed
}

// Stop terminates a managed run: SIGTERM to the process group, SIGKILL after
// the grace period.
//
// Stop is idempotent: stopping a run that Loom already stopped succeeds,
// including after the child has exited and left the active set.
func (m *Manager) Stop(project, runID string) error {
	m.mu.Lock()
	mr, ok := m.active[project]
	if !ok || mr.cmd == nil || mr.run.ID != runID {
		exiting, stopped := m.reapingRun(project, runID)
		m.mu.Unlock()
		if stopped || (!exiting && m.savedAsStopped(project, runID)) {
			return nil
		}
		if !ok || mr.cmd == nil {
			return ErrRunNotFound
		}
		return fmt.Errorf("%w: %s is not the active run", ErrRunNotFound, runID)
	}
	if mr.stopping {
		m.mu.Unlock()
		return nil // already stopping; idempotent
	}
	mr.stopping = true
	mr.run.State = StateStopping
	done := mr.done
	cmd := mr.cmd
	m.mu.Unlock()

	terminateGroup(cmd)

	go func() {
		select {
		case <-done:
			// Exited within the grace period.
		case <-time.After(m.stopGrace):
			killGroup(cmd)
		}
	}()
	return nil
}

// reapingRun reports whether runID has left the active set but has not yet
// saved its final record, and if so whether Loom stopped it. A run is in
// m.reaping until after that save, so a run absent here has its final state in
// the store. The caller holds m.mu.
func (m *Manager) reapingRun(project, runID string) (exiting, stopped bool) {
	for mr := range m.reaping {
		if mr.run.Project == project && mr.run.ID == runID {
			return true, mr.stopping
		}
	}
	return false, false
}

func (m *Manager) savedAsStopped(project, runID string) bool {
	saved, err := m.store.Load(project, runID)
	return err == nil && saved != nil && saved.State == StateStopped
}

// Active returns the running managed run for a project, if any.
func (m *Manager) Active(project string) (Run, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mr, ok := m.active[project]
	if !ok || mr.cmd == nil {
		return Run{}, false
	}
	return mr.run, true
}

// ActiveConsolePath returns the console capture path for the active run.
func (m *Manager) ActiveConsolePath(project string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mr, ok := m.active[project]
	if !ok || mr.consolePath == "" {
		return "", false
	}
	return mr.consolePath, true
}

// updateActive mutates the live run record under lock.
func (m *Manager) updateActive(project string, fn func(*Run)) (Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mr, ok := m.active[project]
	if !ok || mr.cmd == nil {
		return Run{}, false
	}
	fn(&mr.run)
	return mr.run, true
}

// Shutdown stops every managed run and waits until each one has exited and
// saved its final record, including a run that was already exiting.
//
// This terminates live drains. summoner supervises itself perfectly well
// without Loom attached, so killing an expensive autonomous run just because
// the viewer restarted is a real cost — the alternative (orphan the child and
// re-attach on the next start) is tracked as future work. Until then the one
// thing this must not do is take an expensive run down silently, so it names
// every run it is about to kill.
//
// onKill, when set, is called once per run being terminated so the caller can
// log it at whatever level it likes.
func (m *Manager) Shutdown(ctx context.Context, onKill func(run Run)) {
	m.mu.RLock()
	var live, pending []*managedRun
	for _, mr := range m.active {
		if mr.cmd != nil {
			live = append(live, mr)
		}
	}
	for mr := range m.reaping {
		pending = append(pending, mr)
	}
	m.mu.RUnlock()

	for _, mr := range live {
		if onKill != nil {
			m.mu.RLock()
			run := mr.run
			m.mu.RUnlock()
			onKill(run)
		}
		terminateGroup(mr.cmd)
	}
	for _, mr := range pending {
		select {
		case <-mr.done:
		case <-ctx.Done():
			killGroup(mr.cmd)
		}
	}
}

func (m *Manager) publish(typ string, payload any) {
	if m.hub != nil {
		m.hub.Publish(typ, payload)
	}
}

// safeBuffer is an io.Writer with a size cap, so a runaway child cannot exhaust
// memory through a preview's captured output.
type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

const defaultCaptureCap = 4 << 20

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	limit := b.cap
	if limit == 0 {
		limit = defaultCaptureCap
	}
	if len(b.buf) >= limit {
		return len(p), nil
	}
	room := limit - len(b.buf)
	if len(p) > room {
		b.buf = append(b.buf, p[:room]...)
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

var _ io.Writer = (*safeBuffer)(nil)

// trimForError renders captured stderr for an error message: whitespace
// collapsed onto one line and capped, so a wall of output stays readable.
func trimForError(s string) string {
	fields := strings.Fields(s)
	joined := strings.Join(fields, " ")
	const max = 400
	if len(joined) > max {
		return joined[:max] + "…"
	}
	return joined
}

// setProcessGroup puts the child in its own process group so signals reach the
// whole worker tree.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateGroup sends SIGTERM to the child's process group.
func terminateGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative pid targets the group. Fall back to the bare process if the
	// group lookup fails, so a stop always does something.
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		if syscall.Kill(-pgid, syscall.SIGTERM) == nil {
			return
		}
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}

// killGroup sends SIGKILL to the child's process group.
func killGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		if syscall.Kill(-pgid, syscall.SIGKILL) == nil {
			return
		}
	}
	_ = cmd.Process.Kill()
}
