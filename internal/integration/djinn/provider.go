// Package djinn is Loom's djinn integration: run history, evidence, spend and
// summoner dispatch for projects that carry a .djinn directory.
//
// It is one integration.Provider. It owns the run store, the summoner process
// manager and the per-project watchers, and it is the only part of Loom besides
// main that imports internal/runs.
package djinn

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/runs"
	"github.com/kurofune/beads-loom/internal/sse"
)

// Name is the provider id.
const Name = "djinn"

// Capabilities this provider reports.
const (
	// CapRuns is run history, run detail, logs and forensics. Needs .djinn.
	CapRuns = "runs"
	// CapEvidence is the per-bead evidence record. Needs .djinn.
	CapEvidence = "evidence"
	// CapDispatch is launching summoner. Needs .djinn and the summoner binary.
	CapDispatch = "dispatch"
)

// binaryCacheTTL bounds how stale the cached summoner lookup can be, so
// installing summoner shows up as dispatch within a minute without a PATH
// search on every refresh tick.
const binaryCacheTTL = 30 * time.Second

var _ integration.Provider = (*Provider)(nil)

// The lifecycle hooks Loom's core drives.
var (
	_ integration.RunLister      = (*Provider)(nil)
	_ integration.ProjectWatcher = (*Provider)(nil)
	_ integration.Runner         = (*Provider)(nil)
	_ integration.Closer         = (*Provider)(nil)
)

// Options configures the provider.
type Options struct {
	// Settings are the per-project dispatch defaults from config.yaml, by
	// project name (config.Project.DjinnSettings).
	Settings map[string]*config.Summoner
	// StateDir is where Loom keeps run records and console captures. Empty
	// means runs.DefaultStateDir().
	StateDir string
	// Binary overrides the summoner executable (tests use a fake script).
	Binary string
	// LookPath resolves Binary on PATH; exec.LookPath when nil.
	LookPath func(string) (string, error)
	// StopGrace is how long a stopped run gets before it is killed.
	StopGrace time.Duration
	// Hub receives run events. Nil publishes nothing.
	Hub *sse.Hub
	// Now is the clock; time.Now when nil.
	Now    func() time.Time
	Logger *slog.Logger
}

// Provider is the djinn integration.
type Provider struct {
	settings map[string]*config.Summoner
	store    *runs.Store
	mgr      *runs.Manager
	history  *runs.History
	watchers *runs.WatcherSet
	now      func() time.Time
	log      *slog.Logger

	binMu      sync.Mutex
	binChecked time.Time
	binErr     error
}

// New builds the provider. It spawns nothing: summoner runs only from an
// explicit summon request.
func New(opts Options) *Provider {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = runs.DefaultStateDir()
	}
	// A nil *sse.Hub stored in the Publisher interface would read as non-nil
	// and panic on the first publish.
	var pub runs.Publisher
	if opts.Hub != nil {
		pub = opts.Hub
	}

	store := runs.NewStore(stateDir)
	mgr := runs.NewManager(runs.ManagerOptions{
		Store:     store,
		Hub:       pub,
		Binary:    opts.Binary,
		StopGrace: opts.StopGrace,
		LookPath:  opts.LookPath,
	})
	history := runs.NewHistory(store, mgr)
	return &Provider{
		settings: opts.Settings,
		store:    store,
		mgr:      mgr,
		history:  history,
		watchers: runs.NewWatcherSet(mgr, history, pub),
		now:      now,
		log:      log,
	}
}

// Name implements integration.Provider.
func (p *Provider) Name() string { return Name }

// Store is the run store, where Loom-managed runs are recorded.
func (p *Provider) Store() *runs.Store { return p.store }

// Manager is the summoner process manager.
func (p *Provider) Manager() *runs.Manager { return p.mgr }

// Capabilities implements integration.Provider. The read-only features depend
// only on .djinn; dispatch alone also needs the summoner binary. It is one stat
// and a cached PATH lookup.
func (p *Provider) Capabilities(workdir string) integration.Capabilities {
	st, err := os.Stat(filepath.Join(workdir, ".djinn"))
	if err != nil || !st.IsDir() {
		return integration.Capabilities{}
	}
	caps := integration.Capabilities{
		Present:      true,
		Capabilities: []string{CapRuns, CapEvidence},
	}
	if err := p.binaryErr(); err != nil {
		caps.Reasons = map[string]string{
			CapDispatch: "the " + runs.SummonerBinary + " binary was not found on PATH; install it to dispatch from Loom",
		}
	} else {
		caps.Capabilities = append(caps.Capabilities, CapDispatch)
	}
	return caps
}

// binaryErr resolves summoner at most once per binaryCacheTTL.
func (p *Provider) binaryErr() error {
	p.binMu.Lock()
	defer p.binMu.Unlock()
	if now := p.now(); p.binChecked.IsZero() || now.Sub(p.binChecked) >= binaryCacheTTL {
		_, p.binErr = p.mgr.ResolveBinary()
		p.binChecked = now
	}
	return p.binErr
}

// EventNames implements integration.Provider.
func (p *Provider) EventNames() []string {
	return []string{
		runs.EventRunStarted,
		runs.EventRunUpdated,
		runs.EventRunFinished,
		runs.EventRunLog,
		runs.EventBeadState,
	}
}

// Mount implements integration.Provider: every djinn route, under
// /api/projects/{project}/x/djinn/.
func (p *Provider) Mount(r chi.Router, deps integration.Deps) {
	h := p.handlers(deps)
	h.runRoutes(r)
	r.Get("/beads/{id}/evidence", h.handleEvidence)
	r.Get("/spend", h.handleSpend)
}

func (p *Provider) handlers(deps integration.Deps) *handlers {
	log := deps.Logger
	if log == nil {
		log = p.log
	}
	return &handlers{p: p, reg: deps.Registry, log: log}
}

// RunIDs implements integration.RunLister.
func (p *Provider) RunIDs(project, workdir string) ([]string, error) {
	list, err := p.history.List(runs.Project{Name: project, Workdir: workdir})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, run := range list {
		ids = append(ids, run.ID)
	}
	return ids, nil
}

// ProjectAdded implements integration.ProjectWatcher. Only a project with
// .djinn gets a watcher.
func (p *Provider) ProjectAdded(proj registry.Project) {
	p.watchers.Add(runs.Project{Name: proj.Name, Workdir: proj.Workdir})
}

// ProjectRemoved implements integration.ProjectWatcher.
func (p *Provider) ProjectRemoved(name string) { p.watchers.Remove(name) }

// Watched returns the projects with a live watcher, sorted.
func (p *Provider) Watched() []string { return p.watchers.Names() }

// Run implements integration.Runner: the per-project run watchers.
func (p *Provider) Run(ctx context.Context) { p.watchers.Run(ctx) }

// Close implements integration.Closer. It stops every managed summoner run.
//
// This kills live drains: summoner supervises itself fine without Loom
// attached, so an expensive autonomous run dying because the viewer restarted
// is said out loud rather than done quietly.
func (p *Provider) Close(ctx context.Context) {
	p.mgr.Shutdown(ctx, func(run runs.Run) {
		p.log.Warn("TERMINATING A LIVE DRAIN — Loom is shutting down",
			"project", run.Project, "runId", run.ID,
			"startedAt", run.StartedAt.Format(time.RFC3339),
			"beads", len(run.Beads), "costSoFar", run.CostSoFar,
			"hint", "the run is stopped, not orphaned; re-dispatch what did not finish")
	})
}
