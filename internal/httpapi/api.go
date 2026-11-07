// Package httpapi serves Loom's read-only JSON API and, when one has been
// built, the embedded frontend.
//
// The registry is injected; this package holds no global state.
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/gitread"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/readcache"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/sse"
	"github.com/kurofune/beads-loom/internal/sweep"
)

// DevOrigins are the vite dev-server origins allowed to call the API during
// development. In production the frontend is same-origin and CORS is moot.
var DevOrigins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}

// Options configures a Server.
type Options struct {
	Registry *registry.Registry
	// BD is the write layer. Nil mounts no write endpoints, which is how a
	// read-only deployment (or a machine without bd) is expressed.
	BD *bdexec.Client
	// BDVersion is the `bd --version` string reported by /api/health. Empty
	// when bd was not found on PATH.
	BDVersion string
	// Git is the read-only history source used by reconciliation sweeps.
	Git sweep.GitReader
	// Sweep overrides report assembly, primarily for transport-level tests.
	Sweep func(context.Context, string, string, string, int, bool) (sweep.Report, error)
	// Integrations are the optional integrations that are turned on. Each one
	// mounts under /api/projects/{project}/x/{name}/, reachable only for a
	// project whose registry snapshot lists it.
	Integrations integration.Set
	// Hub is the live-update channel. Nil disables /api/events.
	Hub *sse.Hub
	// Monitor supplies cached per-project health and the snapshot a new SSE
	// subscriber receives. Nil falls back to computing health per request.
	Monitor HealthSource
	// ReadCache keeps heavy read results keyed by each project's
	// dolt_hashof_db(). Nil reads the database on every request.
	ReadCache *readcache.Cache
	// Discovery supplies Loom-local discovery state. Nil means discovery is
	// disabled; its routes remain mounted so clients receive the disabled shape.
	Discovery DiscoverySource
	// Listen is the configured listen address, used to validate the Host
	// header on writes. Empty disables the Host check.
	Listen string
	// WebFS is the built frontend. Nil serves the API only.
	WebFS  fs.FS
	Logger *slog.Logger
}

// HealthSource is the cached-health view httpapi needs from the SSE monitor.
// Keeping it an interface means the API does not depend on the poller's
// internals, and a nil Monitor simply falls back to a live query.
type HealthSource interface {
	// Health returns cached counts, or false if none are known yet.
	Health(project string) (queries.Health, bool)
	// Snapshot returns the current health of every project, for replay to a
	// newly connected SSE client.
	Snapshot() []sse.ProjectHealth
}

// Server bundles the router and its dependencies.
type Server struct {
	reg          *registry.Registry
	bd           *bdexec.Client
	integrations integration.Set
	// deps are the core services handed to every integration's Mount.
	deps     integration.Deps
	git      sweep.GitReader
	sweepRun func(context.Context, string, string, string, int, bool) (sweep.Report, error)
	// queryDB is the registry's per-project SQL accessor, held as a field so a
	// transport test can substitute a scripted database.
	queryDB func(context.Context, string, func(context.Context, *sql.DB) error) error
	cache   *readcache.Cache

	hub          *sse.Hub
	monitor      HealthSource
	discovery    DiscoverySource
	allowedHosts map[string]bool
	bdVersion    string
	webFS        fs.FS
	log          *slog.Logger
	router       chi.Router
}

// New builds the API server. It never returns nil.
func New(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		reg:          opts.Registry,
		bd:           opts.BD,
		integrations: opts.Integrations,
		deps: integration.Deps{
			Registry:  opts.Registry,
			BD:        opts.BD,
			Hub:       opts.Hub,
			Logger:    log,
			RunLookup: opts.Integrations.RunLookup(),
		},
		git:      opts.Git,
		sweepRun: opts.Sweep,
		queryDB:  opts.Registry.Do,
		cache:    opts.ReadCache,

		hub:          opts.Hub,
		monitor:      opts.Monitor,
		discovery:    opts.Discovery,
		allowedHosts: allowedHosts(opts.Listen),
		bdVersion:    opts.BDVersion,
		webFS:        opts.WebFS,
		log:          log,
	}
	if s.git == nil {
		s.git = gitread.New(&gitread.ExecRunner{})
	}
	if s.sweepRun == nil {
		s.sweepRun = s.assembleSweepReport
	}
	s.router = s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: DevOrigins,
		AllowedMethods: []string{
			http.MethodGet, http.MethodHead, http.MethodOptions,
			http.MethodPost, http.MethodPatch, http.MethodDelete,
		},
		AllowedHeaders: []string{"Accept", "Content-Type"},
		MaxAge:         300,
	}))
	// Mutating requests get the CSRF checks; reads are untouched.
	r.Use(s.csrfGuard)

	r.Route("/api", func(r chi.Router) {
		// Every API route inherits this: a query string the server cannot
		// parse is a 400, never a handler running against a silently
		// truncated url.Values. See query.go. Mounted here rather than on the
		// root router so a stray ';' in a static asset URL still serves the
		// SPA instead of a JSON error.
		r.Use(rejectMalformedQuery)

		r.Get("/health", s.handleHealth)
		r.Get("/projects", s.handleProjects)
		r.Get("/now", s.handleNow)
		s.discoveryRoutes(r)
		if s.hub != nil {
			r.Get("/events", s.handleEvents)
		}
		r.Route("/projects/{project}", func(r chi.Router) {
			r.Get("/issues", s.handleIssues)
			r.Get("/issues/{id}", s.handleIssue)
			r.Get("/stats", s.handleStats)
			r.Get("/schema", s.handleSchema)
			r.Get("/dolt", s.handleDoltStatus)
			r.Get("/initiatives", s.handleInitiatives)
			r.Get("/graph", s.handleGraph)
			r.Get("/stale", s.handleStale)
			if s.bd != nil {
				r.Get("/query", s.handleQuery)
				s.sweepRoutes(r)
				s.updateRoutes(r)
			}

			// Writes only exist when a bd client is configured, so a
			// read-only deployment has no mutating surface at all.
			if s.bd != nil {
				s.writeRoutes(r)
			}
			s.integrationRoutes(r)
		})
		// Anything else under /api is a client bug, not a frontend route.
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "no such endpoint: "+r.URL.Path)
		})
	})

	if s.webFS != nil {
		s.mountWeb(r)
	}
	return r
}

// --- handlers -------------------------------------------------------------

type healthResponse struct {
	Status    string `json:"status"`
	BDVersion string `json:"bdVersion"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", BDVersion: s.bdVersion})
}

// projectResponse is one entry of GET /api/projects.
type projectResponse struct {
	Name      string `json:"name"`
	DB        string `json:"db"`
	Workdir   string `json:"workdir"`
	Source    string `json:"source"`
	Reachable bool   `json:"reachable"`
	// Reason explains an unreachable project in words a human can act on.
	Reason string `json:"reason"`
	// Writable is false when bd would write to a different database than the
	// one Loom reads; every write route then answers 409 with WriteReason.
	Writable    bool   `json:"writable"`
	WriteReason string `json:"writeReason"`
	// Integrations lists, per provider id, the optional integrations this
	// project has and what each can do here. Never null.
	Integrations map[string]registry.Capabilities `json:"integrations"`
	Health       *queries.Health                  `json:"health"`
}

// handleProjects reports every configured project. An unreachable project is a
// normal, per-project degraded state: it comes back with reachable=false, a
// reason, and a null health block, never a failed request.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	snap := s.reg.Snapshot()
	out := make([]projectResponse, 0, len(snap))

	for _, p := range snap {
		source := p.Source
		if source == "" {
			source = registry.SourceConfig
		}
		item := projectResponse{
			Name:      p.Name,
			DB:        p.Database,
			Workdir:   p.Workdir,
			Source:    source,
			Reachable: p.Reachable,
			Reason:    p.Reason,

			Writable:    p.Writable,
			WriteReason: p.WriteReason,
		}
		item.Integrations = integrationsResponse(p.Integrations)
		if p.Reachable {
			// Prefer the poller's cache: it is recomputed on every hash change
			// and reachability transition, so it is fresh without this handler
			// running a stats query per project per request.
			if s.monitor != nil {
				if h, ok := s.monitor.Health(p.Name); ok {
					item.Health = &h
					out = append(out, item)
					continue
				}
			}

			// Cache miss (no poller, or it has not completed a cycle yet).
			stats, err := s.stats(r.Context(), p.Name)
			if errors.Is(err, registry.ErrQueryTimeout) {
				// The server answered but the counts are slow: the project is
				// up, its health is just not known yet.
				s.log.Warn("project health query timed out", "project", p.Name, "err", err)
			} else if err != nil {
				// Degrade this one project; the rest of the list still renders.
				s.log.Warn("project health query failed", "project", p.Name, "err", err)
				item.Reachable = false
				item.Reason = err.Error()
			} else {
				h := stats.Health()
				item.Health = &h
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	filter, err := filterFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	list, err := cachedRead(r.Context(), s, project, "issues", cacheParams(r.URL.Query()),
		func(ctx context.Context, db *sql.DB) (*queries.IssueList, error) {
			return queries.ListIssues(ctx, db, filter)
		})
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "id")

	detail, err := cachedRead(r.Context(), s, project, "issue", id,
		func(ctx context.Context, db *sql.DB) (*queries.IssueDetail, error) {
			return queries.GetIssue(ctx, db, id)
		})
	if errors.Is(err, queries.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no issue "+id+" in project "+project)
		return
	}
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	stats, err := s.stats(r.Context(), project)
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// stats reads a project's counts through the stats entry the SSE poller also
// fills.
func (s *Server) stats(ctx context.Context, project string) (*queries.Stats, error) {
	return cachedRead(ctx, s, project, readcache.StatsEndpoint, "", queries.StatsFor)
}

// handleSchema reports the statuses and types this project accepts: bd's
// built-ins plus the project's own status.custom / types.custom.
func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	var schema *queries.Schema
	err := s.reg.Do(r.Context(), project, func(ctx context.Context, db *sql.DB) error {
		var err error
		schema, err = queries.SchemaFor(ctx, db)
		return err
	})
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

// storeBusyRetryAfter is the Retry-After, in seconds, on a read that found bd
// holding an embedded store's lock. bd's writes take milliseconds each, so a
// second later the queue has almost always drained.
const storeBusyRetryAfter = "1"

// ErrCodeQueryTimeout is the error code on a 504 from a query that outlived
// its deadline on a live server.
const ErrCodeQueryTimeout = "query_timeout"

// writeRegistryError maps registry and query failures onto status codes:
// an unknown project is the client's mistake (404), an unreachable dolt server
// is ours to report as a temporary outage (503), an embedded store bd is
// still writing is a 503 with Retry-After, which the UI shows with a Retry, and a
// query that ran out of time on a live server is a 504 query_timeout, which
// the UI shows inline rather than as an outage.
func (s *Server) writeRegistryError(w http.ResponseWriter, project string, err error) {
	switch {
	case errors.Is(err, registry.ErrUnknownProject):
		writeError(w, http.StatusNotFound, "no such project: "+project)
	case errors.Is(err, registry.ErrQueryTimeout):
		s.log.Warn("query timed out", "project", project, "err", err)
		writeJSON(w, http.StatusGatewayTimeout, errorResponse{Error: err.Error(), Code: ErrCodeQueryTimeout})
	case errors.Is(err, registry.ErrStoreBusy):
		w.Header().Set("Retry-After", storeBusyRetryAfter)
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, registry.ErrUnreachable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		s.log.Error("query failed", "project", project, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// --- query parameters -----------------------------------------------------

// filterFromQuery translates the URL query into a queries.Filter.
//
// Repeated parameters and comma-separated values both work, so
// ?status=open&status=blocked and ?status=open,blocked are equivalent.
// knownIssueParams is every query parameter /issues understands. An unknown key
// is a 400 rather than a silently ignored typo: `?staus=open` returning a full
// unfiltered page with 200 is worse than an error (review finding N7).
var knownIssueParams = map[string]bool{
	"status": true, "type": true, "label": true, "assignee": true,
	"q": true, "sort": true, "includeClosed": true, "epic": true, "noReceipt": true,
	"priority": true, "priorityMin": true, "priorityMax": true,
	"limit": true, "offset": true,
}

func filterFromQuery(r *http.Request) (queries.Filter, error) {
	q := r.URL.Query()

	for key := range q {
		if !knownIssueParams[key] {
			return queries.Filter{}, fmt.Errorf(
				"unknown query parameter %q (known: %s)", key, knownIssueParamList())
		}
	}
	f := queries.Filter{
		Statuses: multiValue(q, "status"),
		Types:    multiValue(q, "type"),
		Labels:   multiValue(q, "label"),
		Assignee: strings.TrimSpace(q.Get("assignee")),
		Text:     q.Get("q"),
		Sort:     strings.TrimSpace(q.Get("sort")),
		Epic:     strings.TrimSpace(q.Get("epic")),
	}
	if f.Epic != "" && !bdexec.ValidIssueID(f.Epic) {
		return f, errors.New("invalid epic id")
	}

	if v := strings.TrimSpace(q.Get("includeClosed")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, errInvalid("includeClosed", v, "expected true or false")
		}
		f.IncludeClosed = b
	}
	if v := strings.TrimSpace(q.Get("noReceipt")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, errInvalid("noReceipt", v, "expected true or false")
		}
		f.NoReceipt = b
	}

	// priority accepts either an explicit list (priority=0,1) or a range
	// (priorityMin/priorityMax). A list wins if both are given.
	for _, raw := range multiValue(q, "priority") {
		p, err := strconv.Atoi(raw)
		if err != nil {
			return f, errInvalid("priority", raw, "expected an integer 0-4")
		}
		if p < 0 || p > 4 {
			return f, errInvalid("priority", raw, "expected an integer 0-4")
		}
		f.Priorities = append(f.Priorities, p)
	}
	var err error
	if f.PriorityMin, err = optionalInt(q, "priorityMin"); err != nil {
		return f, err
	}
	if f.PriorityMax, err = optionalInt(q, "priorityMax"); err != nil {
		return f, err
	}

	if f.Limit, err = intParam(q, "limit"); err != nil {
		return f, err
	}
	if f.Offset, err = intParam(q, "offset"); err != nil {
		return f, err
	}

	if f.Sort != "" &&
		f.Sort != queries.SortPriority &&
		f.Sort != queries.SortCreated &&
		f.Sort != queries.SortUpdated {
		return f, errInvalid("sort", f.Sort, "expected priority, created or updated")
	}
	return f, nil
}

// multiValue collects a parameter given either repeatedly or comma-separated.
func multiValue(q map[string][]string, key string) []string {
	var out []string
	for _, raw := range q[key] {
		out = append(out, queries.ParseCSV(raw)...)
	}
	return out
}

func optionalInt(q map[string][]string, key string) (*int, error) {
	vals := q[key]
	if len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(vals[0]))
	if err != nil {
		return nil, errInvalid(key, vals[0], "expected an integer")
	}
	return &n, nil
}

func intParam(q map[string][]string, key string) (int, error) {
	p, err := optionalInt(q, key)
	if err != nil || p == nil {
		return 0, err
	}
	return *p, nil
}

// knownIssueParamList renders the accepted parameters in a stable order.
func knownIssueParamList() string {
	keys := make([]string, 0, len(knownIssueParams))
	for k := range knownIssueParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func errInvalid(key, value, hint string) error {
	return errors.New("invalid " + key + "=" + strconv.Quote(value) + ": " + hint)
}

// --- responses ------------------------------------------------------------

// errorResponse is the JSON shape of every API error.
type errorResponse = integration.ErrorBody

func writeJSON(w http.ResponseWriter, status int, body any) {
	integration.WriteJSON(w, status, body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	integration.WriteError(w, status, msg)
}
