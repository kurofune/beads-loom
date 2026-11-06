// Package integration is the seam between Loom's core and its optional,
// compiled-in integrations (djinn today).
//
// A provider answers four questions: its id (Name), what it can do in a given
// workdir (Capabilities, asked inside every registry refresh), which HTTP
// routes it adds (Mount, under /api/projects/{project}/x/{name}/) and which SSE
// event names it publishes (EventNames). There is no runtime plugin framework:
// main builds the provider list, and a provider that is turned off is simply
// not in it, so it has no routes, no watchers and no capabilities.
//
// The optional interfaces below (RunLister, ProjectWatcher, Runner,
// Closer) are the lifecycle hooks a provider may also need. Set fans
// each one out over the providers that implement it.
package integration

import (
	"context"
	"log/slog"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/sse"
)

// Capabilities is what one provider can do in one workdir. It is defined in
// registry, which stores it on every project snapshot keyed by provider id, so
// the registry does not have to import this package.
type Capabilities = registry.Capabilities

// RunLookup returns the run ids a project's run history knows.
type RunLookup func(project, workdir string) ([]string, error)

// Deps are the core services a provider's routes may use.
type Deps struct {
	Registry *registry.Registry
	// BD is the write layer; nil on a read-only deployment.
	BD *bdexec.Client
	// Hub is the live-update channel; nil when SSE is off.
	Hub    *sse.Hub
	Logger *slog.Logger
	// RunLookup resolves run ids. Nil when no mounted provider keeps run
	// history.
	RunLookup RunLookup
}

// Provider is one compiled-in optional integration.
type Provider interface {
	// Name is the provider id: the key under a project's integrations and the
	// {provider} segment of its routes.
	Name() string
	// Capabilities reports what the provider can do in workdir right now. It
	// runs inside every registry refresh, so it must be a stat and cached
	// lookups, never a subprocess.
	Capabilities(workdir string) Capabilities
	// Mount registers the provider's routes on r, which is already scoped to
	// /api/projects/{project}/x/{Name()} and gated to projects where the
	// provider is present.
	Mount(r chi.Router, deps Deps)
	// EventNames lists the SSE event types the provider publishes.
	EventNames() []string
}

// RunLister is implemented by a provider that keeps run history. Core uses it
// to resolve run ids cited in initiative updates.
type RunLister interface {
	RunIDs(project, workdir string) ([]string, error)
}

// ProjectWatcher is implemented by a provider that watches project workdirs.
// Core calls it as projects appear (config at startup, discovery later) and
// disappear.
type ProjectWatcher interface {
	ProjectAdded(p registry.Project)
	ProjectRemoved(name string)
}

// Runner is implemented by a provider with background work. Run blocks until
// ctx is cancelled and the work has stopped.
type Runner interface {
	Run(ctx context.Context)
}

// Closer is implemented by a provider that owns processes or files to release
// at shutdown.
type Closer interface {
	Close(ctx context.Context)
}

// Set is the provider list main builds.
type Set []Provider

// Detectors returns the providers as registry detectors.
func (s Set) Detectors() []registry.Detector {
	out := make([]registry.Detector, 0, len(s))
	for _, p := range s {
		out = append(out, p)
	}
	return out
}

// Get returns the provider named name.
func (s Set) Get(name string) (Provider, bool) {
	for _, p := range s {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// RunLookup returns the first RunLister's lookup, or nil when no provider keeps
// run history.
func (s Set) RunLookup() RunLookup {
	for _, p := range s {
		if rl, ok := p.(RunLister); ok {
			return rl.RunIDs
		}
	}
	return nil
}

// EventNames returns every provider's event names, in provider order.
func (s Set) EventNames() []string {
	var out []string
	for _, p := range s {
		out = append(out, p.EventNames()...)
	}
	return out
}

// ProjectAdded tells every ProjectWatcher about p.
func (s Set) ProjectAdded(p registry.Project) {
	for _, pr := range s {
		if w, ok := pr.(ProjectWatcher); ok {
			w.ProjectAdded(p)
		}
	}
}

// ProjectRemoved tells every ProjectWatcher that the named project is gone.
func (s Set) ProjectRemoved(name string) {
	for _, pr := range s {
		if w, ok := pr.(ProjectWatcher); ok {
			w.ProjectRemoved(name)
		}
	}
}

// Run runs every Runner and blocks until all of them have returned.
func (s Set) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range s {
		if r, ok := p.(Runner); ok {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.Run(ctx)
			}()
		}
	}
	wg.Wait()
}

// Close closes every Closer, in provider order.
func (s Set) Close(ctx context.Context) {
	for _, p := range s {
		if c, ok := p.(Closer); ok {
			c.Close(ctx)
		}
	}
}
