package registry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	embedded "github.com/dolthub/driver/v2"
)

// EmbeddedDirName is the directory under .beads/ that holds bd's embedded Dolt
// databases, one subdirectory per database.
const EmbeddedDirName = "embeddeddolt"

// embeddedLockedText is dolt's error text when another process holds the
// embedded store's exclusive lock. It is the only open error worth retrying.
const embeddedLockedText = "database is locked by another dolt process"

// embeddedOpenBudget bounds the whole open sequence on a locked store: every
// attempt, its shared-lock wait and every retry delay. It stays under the 2s a
// read may spend before it gives up and reports the error.
const embeddedOpenBudget = 1500 * time.Millisecond

// embeddedRetryDelays are the waits between open attempts that dolt itself
// refused as locked: five attempts, 100ms of waiting in total. The lock is free
// for only a few milliseconds at a time while bd writes, and an attempt paused
// here cannot take it, so the pauses stay short. The shared-lock wait needs no
// retry; it holds its place in the kernel's queue for the whole budget.
var embeddedRetryDelays = []time.Duration{
	10 * time.Millisecond,
	20 * time.Millisecond,
	30 * time.Millisecond,
	40 * time.Millisecond,
}

// embeddedTarget names one embedded database on disk.
type embeddedTarget struct {
	// Dir is <workdir>/.beads/embeddeddolt, the directory the driver loads.
	Dir      string
	Database string
}

// manifest is the store file bd rewrites on every write.
func (t embeddedTarget) manifest() string {
	return filepath.Join(t.Dir, t.Database, ".dolt", "noms", "manifest")
}

// embeddedOpener returns a fresh connector for one request scope. The store
// opens the database through it, and closes it, before the scope ends.
type embeddedOpener func(ctx context.Context, t embeddedTarget) (driver.Connector, error)

// writerGap is how long a store stays unopened after Loom closes it. bd waits
// for the store lock by polling it at most 32ms apart (dolthub/fslock's
// maxLockPoll), so a gap of twice that lets a waiting bd in before Loom's next
// read. Without it, back-to-back reads hold the shared lock end to end and bd
// waits forever.
const writerGap = 64 * time.Millisecond

// storeGate admits one Loom scope at a time to one embedded store and spaces
// scopes writerGap apart. Overlapping shared locks would leave no moment free
// for bd's exclusive one.
type storeGate struct {
	sem       chan struct{}
	lastClose time.Time // written only while holding sem
}

// gates holds one storeGate per embedded database directory, process-wide:
// two registries in one process still share the store's lock.
var gates sync.Map

func gateFor(t embeddedTarget) *storeGate {
	g, _ := gates.LoadOrStore(filepath.Join(t.Dir, t.Database), &storeGate{sem: make(chan struct{}, 1)})
	return g.(*storeGate)
}

func (g *storeGate) enter(ctx context.Context) error {
	select {
	case g.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if wait := time.Until(g.lastClose.Add(writerGap)); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			<-g.sem
			return ctx.Err()
		}
	}
	return nil
}

func (g *storeGate) leave() {
	g.lastClose = time.Now()
	<-g.sem
}

// runEmbedded is one request scope: it waits its turn at the store's gate,
// opens t, runs fn, and closes the database and its connector before
// returning. opened is false when the error came from the open, not from fn.
//
// The handle never outlives fn, even when fn panics: while it is open Loom
// holds the store's lock, and bd needs that lock to write.
func runEmbedded(ctx context.Context, open embeddedOpener, t embeddedTarget, fn func(context.Context, *sql.DB) error) (opened bool, err error) {
	gate := gateFor(t)
	if err := gate.enter(ctx); err != nil {
		return false, err
	}
	defer gate.leave()

	db, closeDB, err := openEmbedded(ctx, open, t)
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := closeDB(); err == nil {
			err = closeErr
		}
	}()
	return true, fn(ctx, db)
}

// openEmbedded opens and pings t, retrying while another dolt process holds
// the store lock, for at most embeddedOpenBudget in total. When the attempts
// or the budget run out it returns the last locked error unchanged.
func openEmbedded(ctx context.Context, open embeddedOpener, t embeddedTarget) (*sql.DB, func() error, error) {
	openCtx, cancel := context.WithTimeout(ctx, embeddedOpenBudget)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt <= len(embeddedRetryDelays); attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(embeddedRetryDelays[attempt-1])
			select {
			case <-openCtx.Done():
				timer.Stop()
				return nil, nil, lastErr
			case <-timer.C:
			}
		}
		db, closeDB, err := openEmbeddedOnce(openCtx, open, t)
		if err == nil {
			return db, closeDB, nil
		}
		if lastErr != nil && openCtx.Err() != nil {
			return nil, nil, lastErr
		}
		if !isEmbeddedLocked(err) {
			return nil, nil, err
		}
		lastErr = err
	}
	return nil, nil, lastErr
}

func openEmbeddedOnce(ctx context.Context, open embeddedOpener, t embeddedTarget) (*sql.DB, func() error, error) {
	connector, err := open(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	db := sql.OpenDB(connector)
	closeDB := func() error {
		dbErr := db.Close()
		var connErr error
		if c, ok := connector.(io.Closer); ok {
			connErr = c.Close()
		}
		return errors.Join(dbErr, connErr)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = closeDB()
		return nil, nil, err
	}
	return db, closeDB, nil
}

func isEmbeddedLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), embeddedLockedText)
}

// loomCommitter is the commit identity the driver requires. Loom never
// writes, so it never appears in a commit.
const (
	loomCommitName  = "loom"
	loomCommitEmail = "loom@localhost"
)

// openDoltEmbedded is the production opener: github.com/dolthub/driver over
// the workspace's embeddeddolt directory, read-only.
//
// The driver has no read-only switch, so read-only comes from the store lock.
// Loom takes a shared flock on the database's noms/LOCK first; dolt's own
// exclusive attempt on that file then fails, and dolt opens the store in its
// read-only access mode, where every write is refused below SQL. The shared
// lock also keeps bd from writing mid-read: bd waits for the lock, it does not
// fail. No shared lock, no open: the driver is never reached without it.
func openDoltEmbedded(ctx context.Context, t embeddedTarget) (driver.Connector, error) {
	return openDoltEmbeddedLocked(ctx, t, shareLock)
}

// lockFunc takes the shared store lock on path, or fails.
type lockFunc func(ctx context.Context, path string) (release func(), err error)

func openDoltEmbeddedLocked(ctx context.Context, t embeddedTarget, lock lockFunc) (driver.Connector, error) {
	release, err := lock(ctx, filepath.Join(t.Dir, t.Database, ".dolt", "noms", "LOCK"))
	if err != nil {
		return nil, err
	}
	c, err := embedded.NewConnector(embedded.Config{
		Directory:   t.Dir,
		CommitName:  loomCommitName,
		CommitEmail: loomCommitEmail,
		Database:    t.Database,
	})
	if err != nil {
		release()
		return nil, err
	}
	return &lockedConnector{Connector: c, release: release}, nil
}

// errNoShareLock is the open error on a platform with no flock.
var errNoShareLock = errors.New("embedded Dolt reads need flock for a shared store lock, and this platform has none")

// refuseShareLock is the lockFunc for a platform with no flock.
func refuseShareLock(context.Context, string) (func(), error) {
	return nil, errNoShareLock
}

// lockedConnector releases the shared store lock after the engine closes.
type lockedConnector struct {
	*embedded.Connector
	release func()
}

func (c *lockedConnector) Close() error {
	err := c.Connector.Close()
	c.release()
	return err
}

// embeddedOpenTimeout bounds a health check's open of an embedded store: the
// shared-lock wait, dolt's own lock wait and every retry fit inside it.
const embeddedOpenTimeout = 5 * time.Second

// readEmbedded reports whether workdir's metadata.json declares embedded Dolt,
// and if so which database to read. database overrides dolt_database when
// set. A non-empty reason means the workspace is embedded but unreadable.
func readEmbedded(workdir, database string) (t embeddedTarget, isEmbedded bool, reason string) {
	path := filepath.Join(workdir, ".beads", MetadataFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return t, false, ""
	}
	var md beadsMetadata
	if json.Unmarshal(data, &md) != nil || md.DoltMode != "embedded" {
		return t, false, ""
	}
	t.Dir = filepath.Join(workdir, ".beads", EmbeddedDirName)
	t.Database = strings.TrimSpace(database)
	if t.Database == "" {
		t.Database = strings.TrimSpace(md.DoltDatabase)
	}
	if t.Database == "" {
		return t, true, fmt.Sprintf("%s declares embedded Dolt but has no dolt_database key", path)
	}
	if !dirExists(filepath.Join(t.Dir, t.Database, ".dolt")) {
		return t, true, fmt.Sprintf("embedded database %s does not exist", filepath.Join(t.Dir, t.Database))
	}
	return t, true, ""
}

// refreshEmbedded proves the embedded store opens and records the outcome. A
// server pool left from an earlier server-mode resolution is closed.
func (r *Registry) refreshEmbedded(ctx context.Context, p *project, res resolved, integrations map[string]Capabilities) {
	r.mu.Lock()
	stale := p.db
	p.db, p.dsn = nil, ""
	r.mu.Unlock()
	if stale != nil {
		_ = stale.Close()
	}

	openCtx, cancel := context.WithTimeout(ctx, embeddedOpenTimeout)
	defer cancel()
	t := *res.Embedded
	noop := func(context.Context, *sql.DB) error { return nil }
	if opened, err := runEmbedded(openCtx, r.embeddedOpen, t, noop); err != nil && !opened {
		r.markUnreachable(p, res, integrations, embeddedOpenReason(t, err))
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if !p.active || r.closed {
		return
	}
	p.Host, p.Port, p.Database = "", 0, t.Database
	p.Embedded = true
	p.embedded = &t
	p.Integrations = integrations
	p.Reachable = true
	p.Reason = ""
	p.LastCheck = r.nowFunc()
}

// doEmbedded runs fn against a read-only open of t that lives only for this
// call. A store still locked after every retry returns ErrStoreBusy wrapping
// dolt's error and leaves the project reachable: bd is writing, not gone. Any
// other open failure marks the project unreachable.
func (r *Registry) doEmbedded(ctx context.Context, p *project, t embeddedTarget, fn func(context.Context, *sql.DB) error) error {
	opened, err := runEmbedded(ctx, r.embeddedOpen, t, fn)
	if err != nil && !opened {
		if ctx.Err() != nil {
			return err
		}
		if isEmbeddedLocked(err) {
			r.mu.Lock()
			name := p.Name
			r.mu.Unlock()
			return fmt.Errorf("%w: %s: %w", ErrStoreBusy, name, err)
		}
		reason := embeddedOpenReason(t, err)
		r.mu.Lock()
		if p.active && !r.closed {
			p.Reachable = false
			p.Reason = reason
			p.embedded = nil
			p.LastCheck = r.nowFunc()
		}
		name := p.Name
		r.mu.Unlock()
		return fmt.Errorf("%w: %s: %s", ErrUnreachable, name, reason)
	}
	// An opened store has no server to lose, so a timeout here is always a
	// slow query.
	if err != nil && opened && isTimeout(err) && !errors.Is(ctx.Err(), context.Canceled) {
		r.mu.RLock()
		name := p.Name
		r.mu.RUnlock()
		return fmt.Errorf("%w: %s: %w", ErrQueryTimeout, name, err)
	}
	return err
}

func embeddedOpenReason(t embeddedTarget, err error) string {
	return fmt.Sprintf("cannot open embedded database %s: %s", filepath.Join(t.Dir, t.Database), err)
}
