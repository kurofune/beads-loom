// Package registry tracks the configured projects, resolves each one to a live
// dolt sql-server or to bd's embedded Dolt store, and owns the connection
// pools.
//
// The central fact this package exists to absorb: bd starts a dolt sql-server
// per project on a *dynamic* port, so the port recorded in
// <workdir>/.beads/dolt-server.port changes whenever the server restarts. Every
// health check re-reads that file, and a query that fails with a connection
// error re-reads it once more before giving up. An unreachable project is a
// normal degraded state carrying a human-readable reason — never an error that
// propagates to other projects.
//
// A workspace whose .beads/metadata.json declares dolt_mode "embedded" has no
// server at all. Its reads open the embedded database in-process, read-only,
// once per Do call (embedded.go); writes stay with bd in every mode.
package registry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/kurofune/beads-loom/internal/config"
)

// dialTimeout bounds how long a health probe waits for the dolt server.
const dialTimeout = 2 * time.Second

// QueryTimeout bounds every Do call that arrives without an earlier deadline.
// It is a context deadline, not a socket read timeout: go-sql-driver reports
// an expired read timeout as ErrInvalidConn, which reads exactly like a dead
// server, while an expired context comes back as context.DeadlineExceeded and
// can be told apart from one.
const QueryTimeout = 30 * time.Second

// The DSN's readTimeout is 60s, twice QueryTimeout, so a query always meets
// its context deadline first; the socket timeout only catches a read made
// outside Do, which has no deadline of its own.

// Source values for Project.Source.
const (
	SourceConfig     = "config"
	SourceDiscovered = "discovered"
)

// ErrUnknownProject is returned when a project name is not configured.
var ErrUnknownProject = errors.New("unknown project")

// ErrUnreachable is returned when a project's dolt server cannot be reached.
// Callers should surface Project.Reason rather than this sentinel's text.
var ErrUnreachable = errors.New("project unreachable")

// ErrQueryTimeout is returned when a query outlives its deadline on a server
// that still answers. The project stays reachable: the database is slow, not
// gone, and the same read can succeed on retry or once a cache is warm.
var ErrQueryTimeout = errors.New("query timed out")

// ErrStoreBusy is returned when bd holds an embedded store's lock for the
// whole open budget. The project stays reachable: bd is writing, and the same
// read succeeds once it releases, so callers should answer "retry shortly".
var ErrStoreBusy = errors.New("store busy: bd is writing")

// Project is the registry's view of one configured beads workspace. Values
// returned by Snapshot are copies and safe to read without locking.
type Project struct {
	Name     string
	Workdir  string
	Database string
	Source   string
	// Server is the configured remote server name, or "" for a local project
	// whose port is resolved from .beads/dolt-server.port.
	Server string

	Host string
	Port int
	// Embedded is true when reads open bd's embedded Dolt store in-process
	// rather than dialing a server; Host and Port are then empty.
	Embedded bool

	Reachable bool
	// Reason explains why Reachable is false. Empty when reachable.
	Reason string
	// Writable is false when bd would write to a different database than the
	// one Loom reads, so a write would land where the UI never shows it. It is
	// derived on every refresh (writable.go).
	Writable bool
	// WriteReason explains why Writable is false. Empty when writable.
	WriteReason string
	// Integrations holds, per provider id, the capabilities of every optional
	// integration that applies to this workdir. A provider that does not apply
	// has no key. Refresh replaces the map whole and never mutates it, so the
	// copy Snapshot hands out is safe to read.
	Integrations map[string]Capabilities
	LastCheck    time.Time

	// Generation increments every time Loom writes to this project. It is a
	// cheap cache-buster: anything holding derived state (health counts today,
	// SSE subscribers and ETags later) can compare generations instead of
	// re-querying to find out whether anything changed.
	Generation uint64
}

// Addr is the host:port the project last resolved to, or "" if unresolved.
func (p Project) Addr() string {
	if p.Host == "" || p.Port == 0 {
		return ""
	}
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

// EmbeddedManifest is the manifest of the project's embedded store, which bd
// rewrites on every write, or "" for a server project.
func (p Project) EmbeddedManifest() string {
	if !p.Embedded || p.Database == "" {
		return ""
	}
	return embeddedTarget{Dir: filepath.Join(p.Workdir, ".beads", EmbeddedDirName), Database: p.Database}.manifest()
}

// Capabilities is what one optional integration can do in one workdir.
type Capabilities struct {
	// Present is false when the integration does not apply to the workdir at
	// all; the registry stores no key for it then.
	Present bool `json:"-"`
	// Capabilities lists what works here right now. Never nil when Present.
	Capabilities []string `json:"capabilities"`
	// Reasons says, per capability that does not work, why not.
	Reasons map[string]string `json:"reasons,omitempty"`
}

// Has reports whether capability c works.
func (c Capabilities) Has(capability string) bool {
	for _, have := range c.Capabilities {
		if have == capability {
			return true
		}
	}
	return false
}

// Detector reports an integration's capabilities for a workdir. It runs inside
// every refresh, so it must be cheap: a stat and cached lookups, never a
// subprocess.
type Detector interface {
	Name() string
	Capabilities(workdir string) Capabilities
}

// project is the mutable internal record. All fields are guarded by Registry.mu
// except db, which is only swapped while holding the lock but used unlocked
// (*sql.DB is safe for concurrent use).
type project struct {
	Project

	cfg    config.Project
	db     *sql.DB
	dsn    string
	active bool
	// writeCheckWarn is the last bd failure the write check logged, so a bd
	// that keeps failing logs once rather than on every refresh.
	writeCheckWarn string
	// embedded is set while the project resolves to an embedded store. It is
	// never a handle: each Do opens and closes its own.
	embedded *embeddedTarget
}

// Registry holds every configured project and its connection pool.
type Registry struct {
	cfg       *config.Config
	detectors []Detector

	mu       sync.RWMutex
	byName   map[string]*project
	order    []string
	closed   bool
	nowFunc  func() time.Time
	dialFunc func(network, addr string, timeout time.Duration) (net.Conn, error)
	// embeddedOpen opens an embedded store for one request scope.
	embeddedOpen embeddedOpener
	// writeTargets asks bd where it writes; nil skips the write check.
	writeTargets WriteTargets
	// queryTimeout is the deadline Do puts on a call that has none sooner.
	queryTimeout time.Duration
	log          *slog.Logger
}

// New builds a registry from config. It does not contact any server; call
// Refresh (or let the refresh loop run) to populate health. Every refresh asks
// each detector what its integration can do in the project's workdir.
func New(cfg *config.Config, detectors ...Detector) *Registry {
	r := &Registry{
		cfg:          cfg,
		detectors:    detectors,
		byName:       make(map[string]*project, len(cfg.Projects)),
		nowFunc:      time.Now,
		dialFunc:     net.DialTimeout,
		embeddedOpen: openDoltEmbedded,
		queryTimeout: QueryTimeout,
		log:          slog.Default(),
	}
	for _, pc := range cfg.Projects {
		p := &project{
			Project: Project{
				Name:      pc.Name,
				Workdir:   pc.Workdir,
				Database:  pc.Database,
				Source:    SourceConfig,
				Server:    pc.Server,
				Reachable: false,
				Reason:    "not checked yet",
				Writable:  true,
			},
			cfg:    pc,
			active: true,
		}
		r.byName[pc.Name] = p
		r.order = append(r.order, pc.Name)
	}
	return r
}

// SetDiscovered replaces the set of discovered projects. Entries whose Name or
// Workdir collides with a config-sourced project are dropped (explicit config
// wins). A discovered project already present with the same Name+Workdir keeps
// its pool, health and generation. Removed projects' pools are closed.
// Snapshot/Names order: config order first, then discovered sorted by Name.
// Returns the names actually added and removed.
func (r *Registry) SetDiscovered(ps []config.Project) (added, removed []string) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, nil
	}

	configNames := make(map[string]bool)
	configWorkdirs := make(map[string]bool)
	configOrder := make([]string, 0, len(r.order))
	for _, name := range r.order {
		p := r.byName[name]
		if p.Source != SourceConfig {
			continue
		}
		configNames[p.Name] = true
		configWorkdirs[filepath.Clean(p.Workdir)] = true
		configOrder = append(configOrder, p.Name)
	}

	sorted := append([]config.Project(nil), ps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	desired := make(map[string]config.Project, len(sorted))
	desiredOrder := make([]string, 0, len(sorted))
	seenWorkdirs := make(map[string]bool, len(sorted))
	for _, pc := range sorted {
		workdir := filepath.Clean(pc.Workdir)
		if configNames[pc.Name] || configWorkdirs[workdir] || seenWorkdirs[workdir] {
			continue
		}
		if _, exists := desired[pc.Name]; exists {
			continue
		}
		desired[pc.Name] = config.Project{Name: pc.Name, Workdir: pc.Workdir}
		desiredOrder = append(desiredOrder, pc.Name)
		seenWorkdirs[workdir] = true
	}

	var closeDBs []*sql.DB
	for name, p := range r.byName {
		if p.Source != SourceDiscovered {
			continue
		}
		pc, keep := desired[name]
		if keep && filepath.Clean(pc.Workdir) == filepath.Clean(p.Workdir) {
			delete(desired, name)
			continue
		}

		removed = append(removed, name)
		delete(r.byName, name)
		p.active = false
		if p.db != nil {
			closeDBs = append(closeDBs, p.db)
			p.db = nil
			p.dsn = ""
		}
	}

	for _, name := range desiredOrder {
		pc, ok := desired[name]
		if !ok {
			continue
		}
		r.byName[name] = &project{
			Project: Project{
				Name:      pc.Name,
				Workdir:   pc.Workdir,
				Source:    SourceDiscovered,
				Reachable: false,
				Reason:    "not checked yet",
				Writable:  true,
			},
			cfg:    pc,
			active: true,
		}
		added = append(added, name)
	}

	r.order = append(configOrder, desiredOrder...)
	sort.Strings(added)
	sort.Strings(removed)
	r.mu.Unlock()

	for _, db := range closeDBs {
		_ = db.Close()
	}
	return added, removed
}

// Snapshot returns the current state of every project, in config order.
func (r *Registry) Snapshot() []Project {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Project, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name].Project)
	}
	return out
}

// Get returns the current state of one project.
func (r *Registry) Get(name string) (Project, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.byName[name]
	if !ok {
		return Project{}, fmt.Errorf("%w: %q", ErrUnknownProject, name)
	}
	return p.Project, nil
}

// Names returns the configured project names in config order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// Refresh re-resolves and health-checks every project concurrently. One
// project's failure never affects another: failures are recorded as
// Reachable=false with a reason.
func (r *Registry) Refresh(ctx context.Context) {
	r.mu.RLock()
	ps := make([]*project, 0, len(r.order))
	for _, name := range r.order {
		ps = append(ps, r.byName[name])
	}
	closed := r.closed
	r.mu.RUnlock()

	if closed {
		return
	}

	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func(p *project) {
			defer wg.Done()
			r.refreshOne(ctx, p)
		}(p)
	}
	wg.Wait()
}

// MarkWritten records that Loom just changed this project's data.
//
// It bumps the project's generation and kicks an out-of-band health refresh, so
// the next /api/projects reflects the new counts without waiting out the 10s
// tick. The refresh runs in its own goroutine with its own context: a write
// request must not pay for it, and must not cancel it by returning.
func (r *Registry) MarkWritten(name string) {
	r.mu.Lock()
	p, ok := r.byName[name]
	if !ok || r.closed {
		r.mu.Unlock()
		return
	}
	p.Generation++
	r.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*dialTimeout)
		defer cancel()
		r.refreshOne(ctx, p)
	}()
}

// Generation returns the project's current write generation, and whether the
// project exists.
func (r *Registry) Generation(name string) (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byName[name]
	if !ok {
		return 0, false
	}
	return p.Generation, true
}

// RefreshProject re-resolves a single project.
func (r *Registry) RefreshProject(ctx context.Context, name string) error {
	r.mu.RLock()
	p, ok := r.byName[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownProject, name)
	}
	r.refreshOne(ctx, p)
	return nil
}

// refreshOne resolves the project's address and database, ensures a pool for
// that DSN, and pings it. It records the outcome on the project.
func (r *Registry) refreshOne(ctx context.Context, p *project) {
	r.mu.RLock()
	active := p.active && !r.closed
	r.mu.RUnlock()
	if !active {
		return
	}

	integrations := r.detect(p.cfg.Workdir)
	info, statErr := os.Stat(p.cfg.Workdir)
	workdirProblem := ""
	if errors.Is(statErr, fs.ErrNotExist) {
		workdirProblem = "does not exist"
	} else if statErr == nil && !info.IsDir() {
		workdirProblem = "is not a directory"
	}
	if workdirProblem != "" {
		configPath := r.cfg.Path
		if configPath == "" {
			configPath = "the Loom config"
		}
		r.recordWritable(p, true, "")
		r.markUnreachable(p, resolved{}, integrations, fmt.Sprintf("workdir %s %s — remove or fix the %q entry in %s", p.cfg.Workdir, workdirProblem, p.cfg.Name, configPath))
		return
	}

	res, reason := r.resolve(p.cfg)
	writable, writeReason := r.checkWritable(ctx, p, res)
	r.recordWritable(p, writable, writeReason)
	if reason != "" {
		r.markUnreachable(p, res, integrations, reason)
		return
	}
	if res.Embedded != nil {
		r.refreshEmbedded(ctx, p, res, integrations)
		return
	}

	addr := net.JoinHostPort(res.Host, strconv.Itoa(res.Port))
	conn, err := r.dialFunc("tcp", addr, dialTimeout)
	if err != nil {
		r.markUnreachable(p, res, integrations, fmt.Sprintf("cannot connect to dolt server at %s: %s", addr, rootCause(err)))
		return
	}
	_ = conn.Close()

	if _, err := r.ensurePool(p, res); err != nil {
		r.markUnreachable(p, res, integrations, fmt.Sprintf("cannot open pool for %s: %s", addr, rootCause(err)))
		return
	}

	// The ping uses its own connection, not the pool: slow queries holding
	// every pool connection would make a pool ping time out on a live server.
	pingCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := pingFresh(pingCtx, DSNWithCredentials(res.Host, res.Port, res.Database, res.User, res.Password)); err != nil {
		r.markUnreachable(p, res, integrations, fmt.Sprintf("dolt server at %s did not answer: %s", addr, rootCause(err)))
		return
	}

	r.mu.Lock()
	if !p.active || r.closed {
		r.mu.Unlock()
		return
	}
	p.Host, p.Port, p.Database = res.Host, res.Port, res.Database
	p.Embedded = false
	p.embedded = nil
	p.Integrations = integrations
	p.Reachable = true
	p.Reason = ""
	p.LastCheck = r.nowFunc()
	r.mu.Unlock()
}

// detect asks every detector about workdir and keeps the ones that apply.
func (r *Registry) detect(workdir string) map[string]Capabilities {
	out := make(map[string]Capabilities, len(r.detectors))
	for _, d := range r.detectors {
		caps := d.Capabilities(workdir)
		if !caps.Present {
			continue
		}
		if caps.Capabilities == nil {
			caps.Capabilities = []string{}
		}
		out[d.Name()] = caps
	}
	return out
}

func (r *Registry) markUnreachable(p *project, res resolved, integrations map[string]Capabilities, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !p.active || r.closed {
		return
	}
	p.Host, p.Port = res.Host, res.Port
	if res.Database != "" {
		p.Database = res.Database
	}
	p.Embedded = res.Embedded != nil
	p.embedded = nil
	p.Integrations = integrations
	p.Reachable = false
	p.Reason = reason
	p.LastCheck = r.nowFunc()
}

// resolve determines host, port and database for a project. A non-empty reason
// means resolution failed and the other values are best-effort.
func (r *Registry) resolve(pc config.Project) (res resolved, reason string) {
	res.Database = pc.Database

	if pc.Server != "" {
		s, ok := r.cfg.ServerByName(pc.Server)
		if !ok {
			// config validation should have caught this.
			return res, fmt.Sprintf("configured server %q does not exist", pc.Server)
		}
		if s.Kind == config.KindRemote {
			res.Remote = true
			res.Host, res.Port = s.Host, s.Port
			res.User, res.Password = s.User, s.Password
			if res.Database == "" {
				res.Database, reason = readDatabaseName(pc.Workdir)
				if res.Database == "" {
					return res, reason
				}
			}
			return res, ""
		}
		// kind: local — fall through to port-file resolution, but keep any
		// credentials the server entry carries.
		res.User, res.Password = s.User, s.Password
	}

	// Embedded mode is decided before the port file is read: a port file left
	// behind by an earlier server-mode life names a dead port, and dialing it
	// would mark a readable workspace unreachable.
	if target, isEmbedded, reason := readEmbedded(pc.Workdir, res.Database); isEmbedded {
		res.Embedded = &target
		res.Database = target.Database
		return res, reason
	}

	res.Host = "127.0.0.1"
	res.Port, reason = readPortFile(pc.Workdir)
	if reason != "" {
		res.Port = 0
		return res, reason
	}
	if res.Database == "" {
		res.Database, reason = readDatabaseName(pc.Workdir)
		if res.Database == "" {
			return res, reason
		}
	}
	return res, ""
}

// resolved is where and how to connect to one project's database.
type resolved struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	// Embedded is set when the project reads bd's embedded store instead of
	// a server; Host and Port are then unused.
	Embedded *embeddedTarget
	// Remote is true when Host and Port come from a configured remote server
	// rather than the workdir's port file.
	Remote bool
}

// ensurePool returns the pool for the given DSN, replacing an existing pool if
// the address or database changed (i.e. the server restarted on a new port).
func (r *Registry) ensurePool(p *project, res resolved) (*sql.DB, error) {
	dsn := DSNWithCredentials(res.Host, res.Port, res.Database, res.User, res.Password)

	r.mu.Lock()
	if r.closed || !p.active {
		r.mu.Unlock()
		return nil, errors.New("project is no longer registered")
	}
	if p.db != nil && p.dsn == dsn {
		db := p.db
		r.mu.Unlock()
		return db, nil
	}
	r.mu.Unlock()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	// Dolt servers are local and cheap to talk to; a small pool is plenty and
	// keeps idle connections from piling up across many projects.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	r.mu.Lock()
	if r.closed || !p.active {
		r.mu.Unlock()
		_ = db.Close()
		return nil, errors.New("project is no longer registered")
	}
	// Another goroutine may have installed a pool for this DSN meanwhile.
	if p.db != nil && p.dsn == dsn {
		existing := p.db
		r.mu.Unlock()
		_ = db.Close()
		return existing, nil
	}
	// Re-read the pool being replaced *inside* this lock. Capturing it before
	// the first unlock would close a stale value: if a concurrent resolution
	// installed a different pool in the gap, the loser would close the original
	// and leak the intermediate one's connections until idle timeout.
	superseded := p.db
	p.db = db
	p.dsn = dsn
	r.mu.Unlock()

	if superseded != nil {
		_ = superseded.Close()
	}
	return db, nil
}

// DSN builds the MySQL DSN for a dolt database.
//
// bd's own auto-started servers run as root with no password and no TLS, which
// is why user defaults to root and password to empty. A configured remote
// server may require credentials, and those are threaded through here — before
// this they were parsed, documented and then silently discarded, so a
// credentialed remote could only ever report "unreachable".
func DSN(host string, port int, database string) string {
	return DSNWithCredentials(host, port, database, "", "")
}

// DSNWithCredentials builds a DSN with an explicit user and password.
func DSNWithCredentials(host string, port int, database, user, password string) string {
	if user == "" {
		user = "root"
	}
	credentials := user
	if password != "" {
		credentials += ":" + password
	}
	return fmt.Sprintf("%s@tcp(%s)/%s?parseTime=true&timeout=2s&readTimeout=60s",
		credentials, net.JoinHostPort(host, strconv.Itoa(port)), database)
}

// Credentials are passed through verbatim, not escaped. go-sql-driver parses a
// DSN positionally — the last "@" before the database separator ends the
// credentials, and the first ":" within them splits user from password — and it
// does no percent-decoding, so escaping would send the literal escape sequence
// as the username. Passwords containing "@", ":" or "/" therefore work as-is.
//
// The one shape this cannot express is a *username* containing ":", since the
// first colon always starts the password. That is not a real MySQL username.

// Do runs fn against the project's pool, under a deadline of at most
// queryTimeout.
//
// If fn fails with a connection error the port file is re-read (bd may have
// restarted the server on a new port) and fn is retried exactly once against
// the freshly resolved pool. If that also fails the project is marked
// unreachable and the error is returned.
//
// If fn runs out of time, the server is pinged on a connection of its own: a
// server that answers is slow, not gone, so the project stays reachable and the error wraps
// ErrQueryTimeout. A server that does not answer is re-resolved like any
// connection failure.
func (r *Registry) Do(ctx context.Context, name string, fn func(context.Context, *sql.DB) error) error {
	r.mu.RLock()
	p, ok := r.byName[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownProject, name)
	}

	qctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	ctx = qctx

	db, target, err := r.poolFor(ctx, p)
	if err != nil {
		return err
	}
	if target != nil {
		return r.doEmbedded(ctx, p, *target, fn)
	}

	runErr := fn(ctx, db)
	if runErr == nil || errors.Is(ctx.Err(), context.Canceled) {
		return runErr
	}
	if isTimeout(runErr) {
		return r.classifyTimeout(ctx, p, runErr)
	}
	if !isConnErr(runErr) || ctx.Err() != nil {
		return runErr
	}

	// Connection error: the server may have restarted on a new port.
	r.refreshOne(ctx, p)
	db, target, err = r.poolFor(ctx, p)
	if err != nil {
		return err
	}
	if target != nil {
		return r.doEmbedded(ctx, p, *target, fn)
	}
	if retryErr := fn(ctx, db); retryErr != nil {
		if isTimeout(retryErr) && !errors.Is(ctx.Err(), context.Canceled) {
			return r.classifyTimeout(ctx, p, retryErr)
		}
		if isConnErr(retryErr) {
			reason := fmt.Sprintf("query failed after reconnect: %s", rootCause(retryErr))
			r.mu.Lock()
			// Same liveness guard as markUnreachable: a project removed by
			// SetDiscovered mid-query must not have its detached record mutated.
			if p.active && !r.closed {
				p.Reachable = false
				p.Reason = reason
				p.LastCheck = r.nowFunc()
			}
			r.mu.Unlock()
			return fmt.Errorf("%w: %s: %s", ErrUnreachable, name, reason)
		}
		return retryErr
	}
	return nil
}

// classifyTimeout decides what a query that ran out of time means. A server
// that still answers a connection of its own was merely slow, so the project
// stays reachable and the error wraps ErrQueryTimeout. A server that does not
// answer is re-resolved, which marks the project unreachable when it is really
// gone.
//
// The check never goes through the project's pool: when slow queries hold
// every pool connection, a pool ping waits for one and times out on a server
// that is up. Both steps run on fresh deadlines, because the request's own has
// already expired.
func (r *Registry) classifyTimeout(ctx context.Context, p *project, runErr error) error {
	detached := context.WithoutCancel(ctx)

	r.mu.RLock()
	name, dsn := p.Name, p.dsn
	r.mu.RUnlock()

	pingCtx, cancelPing := context.WithTimeout(detached, dialTimeout)
	err := pingFresh(pingCtx, dsn)
	cancelPing()
	if err == nil {
		return fmt.Errorf("%w: %s: %w", ErrQueryTimeout, name, runErr)
	}

	refreshCtx, cancelRefresh := context.WithTimeout(detached, 2*dialTimeout)
	defer cancelRefresh()
	r.refreshOne(refreshCtx, p)
	r.mu.RLock()
	reachable, reason := p.Reachable, p.Reason
	r.mu.RUnlock()
	if reachable {
		return fmt.Errorf("%w: %s: %w", ErrQueryTimeout, name, runErr)
	}
	return fmt.Errorf("%w: %s: %s", ErrUnreachable, name, reason)
}

// pingFresh proves the server behind dsn answers, on a connection opened for
// the purpose and closed after it. Connecting runs the server's handshake, so
// a listener that accepts and never speaks fails here too.
func pingFresh(ctx context.Context, dsn string) error {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return err
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return err
	}
	conn, err := connector.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if pinger, ok := conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

// poolFor returns a usable pool for p, or the embedded store to open when p
// reads one, resolving p first if it has never been checked or is currently
// marked unreachable.
func (r *Registry) poolFor(ctx context.Context, p *project) (*sql.DB, *embeddedTarget, error) {
	r.mu.RLock()
	db, target, reachable, reason := p.db, p.embedded, p.Reachable, p.Reason
	r.mu.RUnlock()

	if reachable && target != nil {
		return nil, target, nil
	}
	if db != nil && reachable {
		return db, nil, nil
	}

	r.refreshOne(ctx, p)

	r.mu.RLock()
	db, target, reachable, reason = p.db, p.embedded, p.Reachable, p.Reason
	name := p.Name
	r.mu.RUnlock()

	if reachable && target != nil {
		return nil, target, nil
	}
	if db == nil || !reachable {
		if reason == "" {
			reason = "no connection"
		}
		return nil, nil, fmt.Errorf("%w: %s: %s", ErrUnreachable, name, reason)
	}
	return db, nil, nil
}

// Close shuts every pool down.
func (r *Registry) Close() error {
	r.mu.Lock()
	r.closed = true
	dbs := make([]*sql.DB, 0, len(r.byName))
	for _, p := range r.byName {
		if p.db != nil {
			dbs = append(dbs, p.db)
			p.db = nil
			p.dsn = ""
		}
	}
	r.mu.Unlock()

	var firstErr error
	for _, db := range dbs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RunRefreshLoop refreshes every project on interval until ctx is cancelled.
// It refreshes once immediately so the first /api/projects call has real data.
func (r *Registry) RunRefreshLoop(ctx context.Context, interval time.Duration) {
	r.Refresh(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Refresh(ctx)
		}
	}
}

// --- .beads/ file readers -------------------------------------------------

// PortFileName is the primary source of a local project's dolt server port.
const PortFileName = "dolt-server.port"

// MetadataFileName holds the dolt database name.
const MetadataFileName = "metadata.json"

// readPortFile reads <workdir>/.beads/dolt-server.port. The returned reason is
// empty on success.
func readPortFile(workdir string) (int, string) {
	path := filepath.Join(workdir, ".beads", PortFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if !dirExists(filepath.Join(workdir, ".beads")) {
				return 0, fmt.Sprintf("no .beads directory in %s (not a beads workspace?)", workdir)
			}
			return 0, fmt.Sprintf("no %s (dolt server not running? try: bd dolt start -C %s)", path, workdir)
		}
		return 0, fmt.Sprintf("cannot read %s: %s", path, rootCause(err))
	}

	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Sprintf("%s does not contain a port number", path)
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Sprintf("%s contains out-of-range port %d", path, port)
	}
	return port, ""
}

// beadsMetadata is the subset of .beads/metadata.json that Loom reads.
// dolt_server_port is deliberately ignored: it is deprecated and goes stale,
// the port file is the source of truth.
type beadsMetadata struct {
	DoltDatabase string `json:"dolt_database"`
	DoltMode     string `json:"dolt_mode"`
}

// readDatabaseName reads dolt_database from <workdir>/.beads/metadata.json.
func readDatabaseName(workdir string) (string, string) {
	path := filepath.Join(workdir, ".beads", MetadataFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Sprintf("no %s (cannot determine database name)", path)
		}
		return "", fmt.Sprintf("cannot read %s: %s", path, rootCause(err))
	}
	var md beadsMetadata
	if err := json.Unmarshal(data, &md); err != nil {
		return "", fmt.Sprintf("cannot parse %s: %s", path, err)
	}
	if strings.TrimSpace(md.DoltDatabase) == "" {
		return "", fmt.Sprintf("%s has no dolt_database key", path)
	}
	return md.DoltDatabase, ""
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// isTimeout reports whether err means "the query ran out of time": an expired
// context deadline, or a network operation that timed out. Whether the server
// behind it is alive is a separate question; classifyTimeout answers it.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "i/o timeout")
}

// isConnErr reports whether err means "the connection or server is gone",
// as opposed to a SQL-level error such as a bad query or missing table. A
// timeout is not one: it is checked first, because a timed-out socket is also
// a net.Error and would otherwise mark a slow but live server unreachable.
func isConnErr(err error) bool {
	if err == nil || isTimeout(err) {
		return false
	}
	if errors.Is(err, driverBadConn) || errors.Is(err, mysql.ErrInvalidConn) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	// A *mysql.MySQLError is a server-side response, so the server is alive.
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return false
	}
	// Substring matching is a last resort behind the typed checks above. A bare
	// "eof" used to be in this list and was too wide: a SQL parse error reading
	// "unexpected EOF in ..." would trigger the reconnect path and mark a
	// perfectly healthy project unreachable.
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"invalid connection",
		"bad connection",
		"connection refused",
		"broken pipe",
		"connection reset",
		"no such host",
		"driver: bad connection",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// rootCause unwraps to the most specific message worth showing a human.
func rootCause(err error) string {
	if err == nil {
		return ""
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return opErr.Err.Error()
	}
	return err.Error()
}
