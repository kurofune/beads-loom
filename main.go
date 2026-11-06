// Command loom serves the Loom manager cockpit: a read-only JSON API over the
// per-project beads (dolt) databases, plus the embedded frontend when one has
// been built.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"path/filepath"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/buildinfo"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/discovery"
	"github.com/kurofune/beads-loom/internal/httpapi"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/integration/djinn"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/runs"
	"github.com/kurofune/beads-loom/internal/sse"
	"github.com/kurofune/beads-loom/internal/webembed"
)

// shutdownGrace bounds how long in-flight requests get to finish.
const shutdownGrace = 5 * time.Second

// bdVersionTimeout bounds the startup `bd --version` probe.
const bdVersionTimeout = 3 * time.Second

// expectedBDVersion is the bd release Loom's schema assumptions were verified
// against. A mismatch is a warning, never fatal.
const expectedBDVersion = "1.3.2"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "loom:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", "", "path to config.yaml (default $LOOM_CONFIG, else ~/.config/loom/config.yaml)")
		listen      = flag.String("listen", "", "address to listen on (overrides the config file)")
		writeConfig = flag.Bool("write-default-config", false, "write the default config to --config and exit")
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.Info())
		return nil
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if *writeConfig {
		path, err := config.WriteDefault(*configPath)
		if err != nil {
			return err
		}
		fmt.Println("wrote default config to", path)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	bdVersion := checkBD(log)
	banner(cfg, bdVersion)

	// The live-update hub comes first: the integrations publish on it, and the
	// registry asks them for capabilities on every refresh.
	hub := sse.NewHub()
	defer hub.Close()

	integrations := buildIntegrations(cfg, hub, log)

	reg := registry.New(cfg, integrations.Detectors()...)
	defer reg.Close()

	// Without bd there is nothing to write with, so the API comes up
	// read-only rather than exposing endpoints that would fail on every call.
	// With bd, every refresh asks it where a write would land, so a project
	// whose override points Loom's reads elsewhere is read-only, not split.
	var bd *bdexec.Client
	if bdVersion != "" {
		bd = bdexec.New(bdexec.NewExecRunner())
		reg.SetWriteCheck(func(ctx context.Context, workdir string) (registry.BdTarget, error) {
			t, err := bd.Info(ctx, workdir)
			return registry.BdTarget(t), err
		}, log)
	} else {
		log.Warn("write endpoints disabled: bd is unavailable")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The first refresh runs inline so /api/projects has real data the moment
	// the listener opens.
	reg.Refresh(ctx)
	logProjectHealth(log, reg)

	// The SSE monitor replaces phase 1's blanket refresh loop: one poller per
	// project watches dolt_hashof_db(), and health counts are recomputed only
	// when the data actually moves or reachability flips.
	prober := sse.NewRegistryProber(reg)
	monitor := sse.NewMonitor(hub, reg.Names(), sse.MonitorOptions{
		Prober: prober,
	})

	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		monitor.Run(ctx)
	}()

	webFS, webPresent := webembed.FS()
	if webPresent {
		log.Info("serving embedded frontend")
	} else {
		log.Info("no frontend embedded; serving the API only",
			"hint", "make build embeds web/dist, or use make dev for vite on :5173")
	}

	// Each integration watches the projects it applies to; discovery attaches
	// and detaches projects while Loom runs.
	for _, p := range reg.Snapshot() {
		integrations.ProjectAdded(p)
	}
	integrationsDone := make(chan struct{})
	go func() {
		defer close(integrationsDone)
		integrations.Run(ctx)
	}()

	disc, discDone, err := startDiscovery(ctx, cfg, log, reg, monitor, integrations, hub)
	if err != nil {
		return err
	}

	apiOpts := httpapi.Options{
		Registry:     reg,
		Listen:       cfg.Listen,
		BD:           bd,
		Integrations: integrations,
		Hub:          hub,
		Monitor:      monitor,
		ReadCache:    prober.Cache(),
		BDVersion:    bdVersion,
		WebFS:        webFS,
		Logger:       log,
	}
	if disc != nil {
		// Assigned only when non-nil: a typed-nil interface would read as
		// "discovery enabled" to the handlers.
		apiOpts.Discovery = discoverySource{m: disc}
	}
	api := httpapi.New(apiOpts)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
		}
	case <-ctx.Done():
		log.Info("shutting down")
		stop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Warn("graceful shutdown timed out", "err", err)
		}
		// Each integration releases what it owns; djinn stops its managed
		// summoner runs and names every live drain it kills.
		integrations.Close(shutdownCtx)

		// Ends any SSE handler still parked on its subscription.
		hub.Close()
		<-refreshDone
		<-integrationsDone
		<-discDone
		<-serveErr
	}
	return nil
}

// buildIntegrations is the compiled-in provider list. A provider that config
// turns off is left out entirely, so it has no routes, no watchers and no
// capabilities anywhere.
func buildIntegrations(cfg *config.Config, hub *sse.Hub, log *slog.Logger) integration.Set {
	var set integration.Set
	if cfg.DjinnEnabled() {
		settings := make(map[string]*config.Summoner, len(cfg.Projects))
		for _, pc := range cfg.Projects {
			settings[pc.Name] = pc.DjinnSettings()
		}
		p := djinn.New(djinn.Options{Settings: settings, Hub: hub, Logger: log})
		if _, err := p.Manager().ResolveBinary(); err != nil {
			log.Warn("summoner not found on PATH; dispatch will report 503 until it is installed")
		}
		log.Info("djinn integration ready", "stateDir", p.Store().Root())
		set = append(set, p)
	} else {
		log.Info("djinn integration disabled by integrations.djinn.enabled: false")
	}
	return set
}

// integrationNames lists the integrations a project has, sorted.
func integrationNames(p registry.Project) []string {
	names := make([]string, 0, len(p.Integrations))
	for name := range p.Integrations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// startDiscovery wires project auto-discovery when the config enables it.
// The returned channel closes when the manager goroutine has exited; it is
// already closed when discovery is disabled, so shutdown can always wait on it.
func startDiscovery(
	ctx context.Context,
	cfg *config.Config,
	log *slog.Logger,
	reg *registry.Registry,
	monitor *sse.Monitor,
	integrations integration.Set,
	hub *sse.Hub,
) (*discovery.Manager, chan struct{}, error) {
	done := make(chan struct{})
	if cfg.Discovery == nil || !cfg.Discovery.Enabled {
		close(done)
		return nil, done, nil
	}

	store, err := discovery.NewStore(hiddenStorePath(cfg))
	if err != nil {
		return nil, nil, fmt.Errorf("discovery: %w", err)
	}

	takenNames := make(map[string]bool, len(cfg.Projects))
	takenWorkdirs := make(map[string]bool, len(cfg.Projects))
	for _, pc := range cfg.Projects {
		takenNames[pc.Name] = true
		takenWorkdirs[filepath.Clean(pc.Workdir)] = true
	}

	disc := discovery.NewManager(discovery.ManagerOptions{
		Scan: discovery.ScanOptions{
			Roots:   cfg.Discovery.Roots,
			Depth:   cfg.Discovery.Depth,
			Exclude: cfg.Discovery.Exclude,
		},
		Interval:      cfg.Discovery.IntervalDuration(),
		Store:         store,
		TakenNames:    takenNames,
		TakenWorkdirs: takenWorkdirs,
		OnChange: func(active []discovery.Found) {
			ps := make([]config.Project, 0, len(active))
			for _, f := range active {
				ps = append(ps, config.Project{Name: f.Name, Workdir: f.Workdir})
			}
			added, removed := reg.SetDiscovered(ps)
			for _, name := range removed {
				monitor.Remove(name)
				integrations.ProjectRemoved(name)
			}
			for _, name := range added {
				// The monitor's first tick resolves and health-checks the
				// project, so no explicit refresh is needed here.
				monitor.Add(name)
				if p, err := reg.Get(name); err == nil {
					integrations.ProjectAdded(p)
				}
			}
			if len(added) > 0 || len(removed) > 0 {
				log.Info("discovered project set changed", "added", added, "removed", removed)
				// append to empty: the payload contract is [] on the wire, never null.
				hub.Publish(sse.TypeProjectsChanged, sse.ProjectsChanged{
					Added:   append([]string{}, added...),
					Removed: append([]string{}, removed...),
				})
			}
		},
		Logger: log,
	})

	log.Info("project discovery enabled",
		"roots", cfg.Discovery.Roots, "interval", cfg.Discovery.IntervalDuration())
	go func() {
		defer close(done)
		disc.Run(ctx)
	}()
	return disc, done, nil
}

// hiddenStorePath is where hide/unhide choices for discovered projects live:
// next to the config file they conceptually extend.
func hiddenStorePath(cfg *config.Config) string {
	dir := filepath.Dir(config.DefaultPath())
	if cfg.Path != "" {
		dir = filepath.Dir(cfg.Path)
	}
	return filepath.Join(dir, "discovered-hidden.json")
}

// discoverySource adapts *discovery.Manager to httpapi.DiscoverySource,
// keeping the two packages decoupled.
type discoverySource struct{ m *discovery.Manager }

func (d discoverySource) Status() httpapi.DiscoveryStatus {
	st := d.m.Status()
	out := httpapi.DiscoveryStatus{
		Enabled:         true,
		Roots:           append([]string{}, st.Roots...),
		Depth:           st.Depth,
		Exclude:         append([]string{}, st.Exclude...),
		IntervalSeconds: int(st.Interval / time.Second),
		Hidden:          make([]httpapi.HiddenProject, 0, len(st.Hidden)),
	}
	if !st.LastScanAt.IsZero() {
		t := st.LastScanAt
		out.LastScanAt = &t
	}
	for _, h := range st.Hidden {
		out.Hidden = append(out.Hidden, httpapi.HiddenProject{Workdir: h.Workdir, Name: h.Name})
	}
	return out
}

func (d discoverySource) Hide(workdir string) error   { return d.m.Hide(workdir) }
func (d discoverySource) Unhide(workdir string) error { return d.m.Unhide(workdir) }

func (d discoverySource) Rescan(ctx context.Context) (added, removed []string, err error) {
	added, removed = d.m.Rescan(ctx)
	return added, removed, nil
}

// checkBD runs `bd --version` and logs the result. bd is only needed for writes
// (phase 2), so a missing or unexpected version is a warning: the read-only API
// works regardless.
func checkBD(log *slog.Logger) string {
	path, err := exec.LookPath("bd")
	if err != nil {
		log.Warn("bd not found on PATH; write operations will not work",
			"hint", "install beads: https://github.com/gastownhall/beads")
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), bdVersionTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		log.Warn("bd --version failed", "path", path, "err", err)
		return ""
	}

	version := strings.TrimSpace(string(out))
	if version == "" {
		log.Warn("bd --version printed nothing", "path", path)
		return ""
	}
	if !strings.Contains(version, expectedBDVersion) {
		log.Warn("unexpected bd version; Loom's schema assumptions were verified against another release",
			"found", version, "verified_against", expectedBDVersion)
	} else {
		log.Info("found bd", "version", version, "path", path)
	}
	return version
}

// usage prints the flag help with a short description above it.
func usage() {
	fmt.Fprintf(flag.CommandLine.Output(),
		"loom — a manager's cockpit for beads (bd)\n\nUsage:\n  loom [flags]\n\nFlags:\n")
	flag.PrintDefaults()
	fmt.Fprintf(flag.CommandLine.Output(),
		"\nEnvironment:\n"+
			"  %s\tpath to config.yaml (overridden by --config)\n"+
			"  %s\twhere run records and console captures are kept\n"+
			"\nSee README.md for the full configuration reference.\n",
		config.EnvConfigPath, runs.StateDirEnv)
}

// banner prints the startup summary to stdout.
//
// It goes to stdout rather than the structured log because it is the first
// thing a human sees: the four facts that explain what this process is about to
// do, and where to look when it does not.
func banner(cfg *config.Config, bdVersion string) {
	b := buildinfo.Info()

	configLine := cfg.Path
	if configLine == "" {
		configLine = "(built-in defaults — run `loom --write-default-config` to create one)"
	}

	bdLine := bdVersion
	if bdLine == "" {
		bdLine = "NOT FOUND on PATH — writes and dispatch are disabled"
	} else if !strings.Contains(bdLine, expectedBDVersion) {
		bdLine += fmt.Sprintf("  (warning: verified against %s)", expectedBDVersion)
	}

	djinn := 0
	for _, p := range cfg.Projects {
		if (runs.Project{Name: p.Name, Workdir: p.Workdir}).IsDjinnProject() {
			djinn++
		}
	}
	projectLine := fmt.Sprintf("%d configured", len(cfg.Projects))
	if djinn > 0 {
		projectLine += fmt.Sprintf(", %d with .djinn", djinn)
	}
	if cfg.Discovery != nil && cfg.Discovery.Enabled {
		projectLine += fmt.Sprintf(", discovery on (%d roots)", len(cfg.Discovery.Roots))
	}

	fmt.Printf(""+
		"\n  loom %s\n"+
		"  ────────────────────────────────────────────────\n"+
		"  build     %s\n"+
		"  config    %s\n"+
		"  bd        %s\n"+
		"  projects  %s\n"+
		"  listen    http://%s\n\n",
		b.Version, buildLine(b), configLine, bdLine, projectLine, cfg.Listen)
}

// buildLine renders the commit/date/platform part of the banner.
func buildLine(b buildinfo.Build) string {
	parts := []string{}
	if c := b.ShortCommit(); c != "" {
		if b.Dirty {
			c += "-dirty"
		}
		parts = append(parts, c)
	}
	if b.Date != "" {
		parts = append(parts, b.Date)
	}
	parts = append(parts, fmt.Sprintf("%s %s/%s", b.Go, b.OS, b.Arch))
	return strings.Join(parts, "  ")
}

func logProjectHealth(log *slog.Logger, reg *registry.Registry) {
	for _, p := range reg.Snapshot() {
		if p.Reachable {
			log.Info("project ready", "name", p.Name, "db", p.Database, "addr", p.Addr(),
				"integrations", integrationNames(p))
		} else {
			log.Warn("project unreachable", "name", p.Name, "reason", p.Reason)
		}
		if !p.Writable {
			log.Warn("project read-only", "name", p.Name, "reason", p.WriteReason)
		}
	}
}
