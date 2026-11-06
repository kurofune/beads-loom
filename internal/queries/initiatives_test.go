package queries_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/queries"
)

const initiativesRecordingDriverName = "loom-initiatives-recording"
const initiativesFixtureDriverName = "loom-initiatives-fixture"

var initiativesRecorder = &recordingQueryDriver{}

func init() {
	sql.Register(initiativesRecordingDriverName, initiativesRecorder)
	sql.Register(initiativesFixtureDriverName, initiativesFixtureDriver{})
}

type initiativesFixtureDriver struct{}

func (initiativesFixtureDriver) Open(name string) (driver.Conn, error) {
	return initiativesFixtureConn{scenario: name}, nil
}

type initiativesFixtureConn struct {
	scenario string
}

func (initiativesFixtureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}

func (initiativesFixtureConn) Close() error { return nil }

func (initiativesFixtureConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c initiativesFixtureConn) QueryContext(
	_ context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("initiatives query args = %#v, want one cutoff", args)
	}
	if _, ok := args[0].Value.(time.Time); !ok {
		return nil, fmt.Errorf("initiatives cutoff = %#v, want time.Time", args[0].Value)
	}
	if c.scenario == "receipts" {
		for _, want := range []string{"AS no_receipt", "AS no_acceptance", "AS no_close_reason"} {
			if !strings.Contains(query, want) {
				return nil, fmt.Errorf("initiatives query is missing %s", want)
			}
		}
		noReceiptEnd := strings.Index(query, "AS no_receipt")
		if noReceiptEnd < 0 {
			return nil, errors.New("initiatives query has no no-receipt total")
		}
		noReceiptStart := strings.LastIndex(query[:noReceiptEnd], "SUM(CASE WHEN")
		if noReceiptStart < 0 {
			return nil, errors.New("initiatives query has no isolated no-receipt total")
		}
		noReceiptExpr := query[noReceiptStart:noReceiptEnd]
		for _, want := range []string{"c.status = 'closed'", "c.acceptance_criteria", "c.close_reason", " OR "} {
			if !strings.Contains(noReceiptExpr, want) {
				return nil, fmt.Errorf("no-receipt total must count a closed descendant missing either receipt once; missing %q in %s", want, noReceiptExpr)
			}
		}
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		columns := initiativeColumns()
		return &scriptedRows{columns: columns, values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now,
				int64(4), int64(3), int64(1), int64(0), int64(1), int64(0), int64(0),
				int64(2), int64(2), int64(1)},
		}}, nil
	}
	if c.scenario == "needs-target" {
		// The opt-out must ride the one statement, and must read the shared
		// label constant rather than a second spelling of it.
		if !strings.Contains(query, "AS optout_label") {
			return nil, errors.New("initiatives query did not select the no-target opt-out")
		}
		if !strings.Contains(query, "FROM labels l WHERE l.issue_id = e.id AND l.label = '"+queries.NoTargetLabel+"'") {
			return nil, fmt.Errorf("initiatives opt-out must match the %s label on the epic itself: %s", queries.NoTargetLabel, query)
		}
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		due := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			// open, three descendants, no due, no opt-out -> flagged.
			{"epic-flagged", "Flagged", "open", int64(1), "", nil, now, now,
				int64(3), int64(1), int64(1), int64(0), int64(1), int64(0), int64(0),
				int64(0), int64(0), int64(0), nil},
			// same shape, but opted out -> silent.
			{"epic-optout", "Opted out", "open", int64(1), "", nil, now, now,
				int64(3), int64(1), int64(1), int64(0), int64(1), int64(0), int64(0),
				int64(0), int64(0), int64(0), queries.NoTargetLabel},
			// same shape, but dated -> silent.
			{"epic-dated", "Dated", "open", int64(1), "", due, now, now,
				int64(3), int64(1), int64(1), int64(0), int64(1), int64(0), int64(0),
				int64(0), int64(0), int64(0), nil},
			// closed epic with no due -> silent.
			{"epic-closed", "Closed", "closed", int64(1), "", nil, now, now,
				int64(3), int64(3), int64(0), int64(0), int64(0), int64(0), int64(0),
				int64(0), int64(0), int64(0), nil},
			// no descendants -> a scope problem, not a target problem.
			{"epic-empty", "Empty", "open", int64(1), "", nil, now, now,
				int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0),
				int64(0), int64(0), int64(0), nil},
		}}, nil
	}
	if c.scenario == "started" {
		// started is a STATUS bucket and must never be routed through the
		// readiness view. Checked on the isolated SUM expression, because the
		// surrounding statement legitimately joins ready_issues for the *ready*
		// bucket — only the started expression itself must stay clean.
		startedExpr, err := isolateStartedExpr(query)
		if err != nil {
			return nil, err
		}
		for _, readinessToken := range []string{"r.", "bt.", "ready_issues", "notReady"} {
			if strings.Contains(startedExpr, readinessToken) {
				return nil, fmt.Errorf(
					"started must not be routed through the readiness view, but its SUM references %q: %s",
					readinessToken, startedExpr)
			}
		}
		// The started bucket must be a status bucket built from the shared
		// constants, summed inside the one recursive-CTE statement.
		if !strings.Contains(query, "SUM(CASE WHEN c.status IN ('in_progress', 'hooked') THEN 1 ELSE 0 END) AS started") {
			return nil, errors.New("initiatives query did not count started descendants from the status constants")
		}
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		// One in_progress descendant and nothing else: the row the AC-3 tuple
		// is asserted against.
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now, int64(1), int64(0), int64(0), int64(1), int64(0), int64(0), int64(0)},
		}}, nil
	}
	if c.scenario == "blocked" {
		// The blocked bucket is the existing dep_blocked SUM with one extra OR
		// arm — the same shape status='deferred' already uses on defer_scheduled.
		// Isolated, because the surrounding statement legitimately mentions
		// every other bucket.
		depBlockedExpr, err := isolateSumExpr(query, "AS dep_blocked")
		if err != nil {
			return nil, err
		}
		wantArm := "OR c.status = '" + queries.StatusBlocked + "'"
		if !strings.Contains(depBlockedExpr, wantArm) {
			return nil, fmt.Errorf(
				"dep_blocked must union %s descendants into the existing bucket, missing %q: %s",
				queries.StatusBlocked, wantArm, depBlockedExpr)
		}
		if !strings.Contains(depBlockedExpr, "END = 'deps' OR") {
			return nil, fmt.Errorf(
				"dep_blocked must keep counting notReadyExpr='deps' alongside the status arm: %s",
				depBlockedExpr)
		}
		// Counted exactly once: the deferral bucket must not also claim a
		// status=blocked descendant.
		deferExpr, err := isolateSumExpr(query, "AS defer_scheduled")
		if err != nil {
			return nil, err
		}
		if strings.Contains(deferExpr, "'"+queries.StatusBlocked+"'") {
			return nil, fmt.Errorf(
				"defer_scheduled must not also claim a status=%s descendant: %s",
				queries.StatusBlocked, deferExpr)
		}
		// A status bucket, never a readiness token: the isolated expression may
		// name the readiness CASE it wraps, but must not invent a new one.
		if strings.Contains(depBlockedExpr, "'"+queries.StatusBlocked+"' THEN 'deps'") {
			return nil, fmt.Errorf(
				"blocked is a status bucket, not a readiness verdict: %s", depBlockedExpr)
		}
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		// One status=blocked descendant and nothing else.
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now,
				int64(1), int64(0), int64(0), int64(0), int64(0), int64(1), int64(0)},
		}}, nil
	}
	if c.scenario == "pinned-excluded" {
		// pinned rows leave the descendant join entirely, so they reach neither
		// total nor any bucket.
		wantJoin := "LEFT JOIN issues c ON c.id = dsc.node AND c.status <> '" + queries.StatusPinned + "'"
		if !strings.Contains(query, wantJoin) {
			return nil, fmt.Errorf(
				"the descendant join must exclude %s rows, missing %q: %s",
				queries.StatusPinned, wantJoin, query)
		}
		// Excluded from the denominator, NOT given a rollup column of its own.
		if strings.Contains(query, "AS pinned") {
			return nil, errors.New("pinned must not gain a rollup column; it is excluded from the subtree")
		}
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now,
				int64(2), int64(1), int64(1), int64(0), int64(1), int64(0), int64(0)},
		}}, nil
	}
	if c.scenario == "blocked-and-deferred" {
		dependencyCase := strings.Index(query, "WHEN bt.issue_id IS NOT NULL THEN 'deps'")
		deferredCase := strings.Index(query, "THEN 'deferred'")
		if dependencyCase < 0 || deferredCase < 0 || dependencyCase > deferredCase {
			return nil, errors.New("initiatives query did not give dependency blocking precedence over deferral")
		}
		if !strings.Contains(query,
			"END = 'deps' OR c.status = '"+queries.StatusBlocked+"' THEN 1 ELSE 0 END) AS dep_blocked") {
			return nil, errors.New("initiatives query did not increment the dependency-blocked bucket")
		}
		deferredBucketEnd := strings.Index(query, "AS defer_scheduled")
		if deferredBucketEnd < 0 {
			return nil, errors.New("initiatives query did not calculate a deferred bucket")
		}
		deferredBucketStart := strings.LastIndex(query[:deferredBucketEnd], "SUM(CASE")
		if deferredBucketStart < 0 {
			return nil, errors.New("initiatives query did not calculate a deferred bucket")
		}
		deferredBucket := query[deferredBucketStart : deferredBucketEnd+len("AS defer_scheduled")]
		dependencyExclusion := strings.Index(deferredBucket, "END = 'deps' THEN 0")
		statusDeferred := strings.Index(deferredBucket, "WHEN c.status = 'deferred' OR")
		if dependencyExclusion < 0 || statusDeferred < 0 || dependencyExclusion > statusDeferred {
			return nil, errors.New("initiatives deferred bucket did not exclude dependency-blocked descendants first")
		}
		if !strings.Contains(deferredBucket, "END = 'deferred' THEN 1\n                ELSE 0 END) AS defer_scheduled") {
			return nil, errors.New("initiatives query did not increment the deferred bucket")
		}
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now, int64(1), int64(0), int64(0), int64(0), int64(0), int64(1), int64(0)},
		}}, nil
	}
	if c.scenario == "deferred" {
		if !strings.Contains(query, "WHEN c.status = 'deferred' OR") ||
			!strings.Contains(query, "END = 'deferred' THEN 1\n                ELSE 0 END) AS defer_scheduled") {
			return nil, errors.New("initiatives query did not count deferred descendants")
		}
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now, int64(1), int64(0), int64(0), int64(0), int64(0), int64(0), int64(1)},
		}}, nil
	}
	if c.scenario == "eligible" {
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now, int64(2), int64(2), int64(2), int64(0), int64(0), int64(0), int64(0)},
		}}, nil
	}
	if c.scenario == "open-descendant" {
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now, int64(2), int64(1), int64(1), int64(0), int64(1), int64(0), int64(0)},
		}}, nil
	}
	if c.scenario == "zero-descendants" {
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", nil, now, now, int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0)},
		}}, nil
	}
	if c.scenario == "closed-epic" {
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "closed", int64(1), "", nil, now, now, int64(2), int64(2), int64(2), int64(0), int64(0), int64(0), int64(0)},
		}}, nil
	}
	if c.scenario != "multilevel" && c.scenario != "eligible-multilevel" {
		return nil, errors.New("unexpected initiatives fixture scenario: " + c.scenario)
	}
	if !strings.Contains(query, "subtree AS (") ||
		!strings.Contains(query, "WHERE s.depth < 50") ||
		!strings.Contains(query, "d.depends_on_issue_id = s.node") ||
		!strings.Contains(query, "SELECT DISTINCT root, node") ||
		!strings.Contains(query, "WHERE depth > 0") {
		return nil, errors.New("initiatives query did not roll up transitive descendants")
	}

	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	closed := int64(0)
	ready := int64(2)
	if c.scenario == "eligible-multilevel" {
		closed = 2
		ready = 0
	}
	return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
		{"epic-root", "Epic root", "open", int64(1), "", nil, now, now.Add(2 * time.Hour), int64(2), closed, closed, int64(0), ready, int64(0), int64(0)},
	}}, nil
}

var _ driver.QueryerContext = initiativesFixtureConn{}

// isolateStartedExpr cuts the `SUM(...)` that produces the started column out of
// the full statement.
//
// The isolation is the whole point: a whole-statement `strings.Contains` for a
// readiness alias can never fail, because the statement joins ready_issues and
// blocked_transitively for the ready/blocked buckets by design. Only the
// started expression is required to be readiness-free, so only it may be
// searched.
func isolateStartedExpr(query string) (string, error) {
	return isolateSumExpr(query, "AS started")
}

// isolateSumExpr cuts one `SUM(...) AS <column>` out of the full statement, so
// an assertion about that bucket cannot be satisfied by text belonging to a
// different bucket in the same SELECT list.
func isolateSumExpr(query, marker string) (string, error) {
	end := strings.Index(query, marker)
	if end < 0 {
		return "", fmt.Errorf("initiatives query has no %s column", marker)
	}
	start := strings.LastIndex(query[:end], "SUM(")
	if start < 0 {
		return "", fmt.Errorf("initiatives query does not SUM %s", marker)
	}
	return query[start : end+len(marker)], nil
}

func initiativeColumns() []string {
	return []string{
		"id", "title", "status", "priority", "assignee", "due", "updated",
		"last_activity", "total", "closed", "closed_in_window", "started", "ready", "dep_blocked", "defer_scheduled",
		"no_receipt", "no_acceptance", "no_close_reason", "optout_label",
	}
}

func openInitiativesFixture(t *testing.T, scenario string) *sql.DB {
	t.Helper()
	db, err := sql.Open(initiativesFixtureDriverName, scenario)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type recordedQuery struct {
	SQL  string
	Args []driver.NamedValue
}

type recordingQueryDriver struct {
	mu      sync.Mutex
	queries []recordedQuery
}

func (d *recordingQueryDriver) Open(string) (driver.Conn, error) {
	return &recordingQueryConn{driver: d}, nil
}

func (d *recordingQueryDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = nil
}

func (d *recordingQueryDriver) snapshot() []recordedQuery {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]recordedQuery(nil), d.queries...)
}

type recordingQueryConn struct {
	driver *recordingQueryDriver
}

func (c *recordingQueryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}

func (c *recordingQueryConn) Close() error { return nil }

func (c *recordingQueryConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *recordingQueryConn) QueryContext(
	_ context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	c.driver.mu.Lock()
	c.driver.queries = append(c.driver.queries, recordedQuery{
		SQL:  query,
		Args: append([]driver.NamedValue(nil), args...),
	})
	c.driver.mu.Unlock()
	return &recordedInitiativeRows{}, nil
}

var _ driver.QueryerContext = (*recordingQueryConn)(nil)

type recordedInitiativeRows struct {
	index int
}

func (*recordedInitiativeRows) Columns() []string {
	return initiativeColumns()
}

func (*recordedInitiativeRows) Close() error { return nil }

func (r *recordedInitiativeRows) Next(dest []driver.Value) error {
	// Thirty non-empty rows are load-bearing: they exercise AC-7's named scale
	// and make the one-query guard non-vacuous. A per-epic query loop would add
	// 30 QueryContext calls and fail the caller's exact-one assertion.
	if r.index >= 30 {
		return io.EOF
	}
	updated := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	row := []driver.Value{
		fmt.Sprintf("epic-%d", r.index+1), fmt.Sprintf("Epic %d", r.index+1),
		"open", int64(2), "", nil, updated, updated,
		int64(2), int64(0), int64(0), int64(0), int64(1), int64(0), int64(1),
	}
	if r.index == 0 {
		row = []driver.Value{"epic-1", "First epic", "open", int64(1), "alex", nil, updated, updated.Add(24 * time.Hour), int64(4), int64(1), int64(0), int64(1), int64(1), int64(1), int64(0)}
	}
	copy(dest, row)
	r.index++
	return nil
}

// The missing-target nudge fires off Initiative.NeedsTarget, so the flag has to
// come out of the same statement as the rollups — and has to be narrow enough
// that only the actionable state lights up.
func TestInitiativesFlagsOnlyEpicsInTheChildrenButNoDueDateState(t *testing.T) {
	db := openInitiativesFixture(t, "needs-target")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}

	want := map[string]bool{
		"epic-flagged": true,
		"epic-optout":  false,
		"epic-dated":   false,
		"epic-closed":  false,
		"epic-empty":   false,
	}
	if len(got) != len(want) {
		t.Fatalf("initiatives = %d, want %d", len(got), len(want))
	}
	for _, initiative := range got {
		if initiative.NeedsTarget != want[initiative.ID] {
			t.Errorf("%s NeedsTarget = %v, want %v", initiative.ID, initiative.NeedsTarget, want[initiative.ID])
		}
	}
}

func TestInitiativesIsOneQuery(t *testing.T) {
	initiativesRecorder.reset()
	db, err := sql.Open(initiativesRecordingDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.InitiativesFor(context.Background(), db, false)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 30 {
		t.Fatalf("initiatives = %d, want 30 fabricated rows", len(got))
	}

	queries := initiativesRecorder.snapshot()
	if len(queries) != 1 {
		t.Fatalf("QueryContext calls = %d, want exactly one", len(queries))
	}
	if len(queries[0].Args) != 1 {
		t.Errorf("query args = %d, want one shared close-window cutoff", len(queries[0].Args))
	}
	if strings.Contains(strings.ToLower(queries[0].SQL), "where e.id = ?") {
		t.Errorf("query contains a per-epic id lookup:\n%s", queries[0].SQL)
	}
	for _, receiptColumn := range []string{"AS no_receipt", "AS no_acceptance", "AS no_close_reason"} {
		if !strings.Contains(queries[0].SQL, receiptColumn) {
			t.Errorf("the one 30-epic statement is missing %s:\n%s", receiptColumn, queries[0].SQL)
		}
	}
}

func TestInitiativeForSharesStatementAndBindsRootBeforeCutoff(t *testing.T) {
	initiativesRecorder.reset()
	db, err := sql.Open(initiativesRecordingDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.InitiativeFor(context.Background(), db, "epic-1")
	if err != nil {
		t.Fatalf("InitiativeFor: %v", err)
	}
	if got == nil || got.ID != "epic-1" {
		t.Fatalf("initiative = %#v, want epic-1", got)
	}
	if got.Health.State != "no-target" {
		t.Errorf("health = %#v, want no-target from the shared classifier", got.Health)
	}

	recorded := initiativesRecorder.snapshot()
	if len(recorded) != 1 {
		t.Fatalf("QueryContext calls = %d, want exactly one", len(recorded))
	}
	if strings.Count(recorded[0].SQL, "WITH RECURSIVE") != 1 {
		t.Fatalf("shared query has %d recursive statement starts, want one", strings.Count(recorded[0].SQL, "WITH RECURSIVE"))
	}
	rootAt := strings.Index(recorded[0].SQL, "i.id = ?")
	cutoffAt := strings.Index(recorded[0].SQL, "c.closed_at >= ?")
	if rootAt < 0 || cutoffAt < 0 || rootAt >= cutoffAt {
		t.Fatalf("root filter must precede close-window cutoff in SQL:\n%s", recorded[0].SQL)
	}
	if len(recorded[0].Args) != 2 {
		t.Fatalf("query args = %#v, want root id then cutoff", recorded[0].Args)
	}
	if gotID := recorded[0].Args[0].Value; gotID != "epic-1" {
		t.Errorf("first arg = %#v, want epic-1 root id", gotID)
	}
	if _, ok := recorded[0].Args[1].Value.(time.Time); !ok {
		t.Errorf("second arg = %#v, want time.Time cutoff", recorded[0].Args[1].Value)
	}
}

// TestInitiativesStartedRidesTheSingleStatement pins AC-2: the started SUM is
// part of the one recursive-CTE statement, not a second query or a per-epic
// fetch. The recording driver fabricates two rows so the guard cannot pass
// vacuously — a per-epic loop over zero rows would execute zero extra queries.
func TestInitiativesStartedRidesTheSingleStatement(t *testing.T) {
	initiativesRecorder.reset()
	db, err := sql.Open(initiativesRecordingDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.InitiativesFor(context.Background(), db, false)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 30 {
		t.Fatalf("initiatives = %d rows, want 30 fabricated rows", len(got))
	}
	// Scanned, not defaulted: epic-1's fabricated started column is 1.
	if got[0].Subtree.Started != 1 {
		t.Errorf("epic-1 started = %d, want the fabricated 1", got[0].Subtree.Started)
	}
	if got[1].Subtree.Started != 0 {
		t.Errorf("epic-2 started = %d, want the fabricated 0", got[1].Subtree.Started)
	}

	recorded := initiativesRecorder.snapshot()
	if len(recorded) != 1 {
		t.Fatalf("QueryContext calls = %d, want exactly one", len(recorded))
	}
	if !strings.Contains(recorded[0].SQL, "AS started") {
		t.Fatalf("the single statement does not compute started:\n%s", recorded[0].SQL)
	}
	if !strings.Contains(recorded[0].SQL, "SUM(CASE WHEN c.status IN ('in_progress', 'hooked') THEN 1 ELSE 0 END) AS started") {
		t.Errorf("started is not summed from the status constants:\n%s", recorded[0].SQL)
	}
}

func TestInitiativesReportsClosedDescendantsMissingReceipts(t *testing.T) {
	db := openInitiativesFixture(t, "receipts")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	receipts := got[0].NoReceipt
	if receipts.Total != 2 {
		t.Errorf("no-receipt total = %d, want 2 closed descendants missing either receipt", receipts.Total)
	}
	if receipts.NoAcceptance != 2 {
		t.Errorf("missing acceptance = %d, want 2", receipts.NoAcceptance)
	}
	if receipts.NoCloseReason != 1 {
		t.Errorf("missing close reason = %d, want 1", receipts.NoCloseReason)
	}
	if receipts.Total == receipts.NoAcceptance+receipts.NoCloseReason {
		t.Error("total double-counted the descendant missing both receipts")
	}
}

// TestInitiativesStartedIsAMutuallyExclusiveBucket pins AC-3: a subtree holding
// exactly one in_progress descendant reports started=1 and zero in every other
// bucket, so in-flight work is no longer counted in total and nothing else.
func TestInitiativesStartedIsAMutuallyExclusiveBucket(t *testing.T) {
	db := openInitiativesFixture(t, "started")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	rollup := got[0].Subtree
	if rollup.Started != 1 {
		t.Errorf("started = %d, want the one in_progress descendant", rollup.Started)
	}
	if rollup.Closed != 0 {
		t.Errorf("closed = %d, want 0", rollup.Closed)
	}
	if rollup.Ready != 0 {
		t.Errorf("ready = %d, want 0 — an in_progress issue is absent from ready_issues", rollup.Ready)
	}
	if rollup.DepBlocked != 0 {
		t.Errorf("depBlocked = %d, want 0 — notReadyExpr is NULL for a non-open status", rollup.DepBlocked)
	}
	if rollup.DeferScheduled != 0 {
		t.Errorf("deferScheduled = %d, want 0", rollup.DeferScheduled)
	}
	if rollup.Total != 1 {
		t.Errorf("total = %d, want the single descendant", rollup.Total)
	}
}

func TestInitiativesSubtreeRollupIncludesGrandchildren(t *testing.T) {
	db := openInitiativesFixture(t, "multilevel")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if got[0].ID != "epic-root" {
		t.Fatalf("initiative id = %q, want epic-root", got[0].ID)
	}
	if got[0].Subtree.Total != 2 {
		t.Fatalf("subtree total = %d, want direct child plus grandchild", got[0].Subtree.Total)
	}
}

func TestInitiativeIsCloseEligibleWhenEveryDescendantIsClosed(t *testing.T) {
	db := openInitiativesFixture(t, "eligible")

	got, err := queries.InitiativesFor(context.Background(), db, false)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if !got[0].CloseEligible {
		t.Fatalf("close eligible = false, want true for two closed descendants")
	}
}

func TestInitiativeIsNotCloseEligibleWithAnOpenDescendant(t *testing.T) {
	db := openInitiativesFixture(t, "open-descendant")

	got, err := queries.InitiativesFor(context.Background(), db, false)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if got[0].CloseEligible {
		t.Fatalf("close eligible = true, want false with one open descendant")
	}
}

func TestInitiativeWithNoDescendantsIsNotCloseEligible(t *testing.T) {
	db := openInitiativesFixture(t, "zero-descendants")

	got, err := queries.InitiativesFor(context.Background(), db, false)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if got[0].CloseEligible {
		t.Fatalf("close eligible = true, want false with no descendants")
	}
}

func TestClosedInitiativeIsNotCloseEligible(t *testing.T) {
	db := openInitiativesFixture(t, "closed-epic")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one closed epic", got)
	}
	if got[0].CloseEligible {
		t.Fatalf("close eligible = true, want false when the epic is already closed")
	}
}

func TestInitiativeCloseEligibilityIncludesClosedNestedDescendants(t *testing.T) {
	db := openInitiativesFixture(t, "eligible-multilevel")

	got, err := queries.InitiativesFor(context.Background(), db, false)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if got[0].Subtree.Total != 2 || got[0].Subtree.Closed != 2 {
		t.Fatalf("subtree = %#v, want a closed child and grandchild", got[0].Subtree)
	}
	if !got[0].CloseEligible {
		t.Fatalf("close eligible = false, want true with a closed nested descendant")
	}
}

func TestInitiativesSubtreeRollups(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	got, err := queries.InitiativesFor(ctx, db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("seeded database returned no initiatives")
	}

	list, err := queries.ListIssues(ctx, db, queries.Filter{
		IncludeClosed: true,
		Limit:         queries.MaxLimit,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	issues := make(map[string]queries.Issue, len(list.Issues))
	children := make(map[string][]string)
	for _, issue := range list.Issues {
		issues[issue.ID] = issue
		if issue.Parent != nil {
			children[issue.Parent.ID] = append(children[issue.Parent.ID], issue.ID)
		}
	}

	var checkedGrandchildren bool
	for _, initiative := range got {
		issue, ok := issues[initiative.ID]
		if !ok {
			t.Errorf("%s: initiative is not in the visible issue list", initiative.ID)
			continue
		}
		if issue.Type != "epic" {
			t.Errorf("%s: type = %q, want epic", initiative.ID, issue.Type)
		}
		if initiative.Subtree.Closed > initiative.Subtree.Total {
			t.Errorf("%s: closed %d > total %d", initiative.ID, initiative.Subtree.Closed, initiative.Subtree.Total)
		}
		// started is mutually exclusive with every other bucket by construction,
		// so the five buckets never oversubscribe the subtree.
		if initiative.Subtree.Ready+initiative.Subtree.DepBlocked+initiative.Subtree.DeferScheduled+initiative.Subtree.Started+initiative.Subtree.Closed > initiative.Subtree.Total {
			t.Errorf("%s: closed + started + ready + blocked + deferred exceeds total: %+v", initiative.ID, initiative.Subtree)
		}
		if initiative.LastActivity.Before(initiative.Updated) {
			t.Errorf("%s: last activity %s is before epic update %s", initiative.ID, initiative.LastActivity, initiative.Updated)
		}

		type pending struct {
			id    string
			depth int
		}
		queue := []pending{{id: initiative.ID}}
		seen := map[string]bool{initiative.ID: true}
		var descendants []queries.Issue
		maxDepth := 0
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if current.depth >= 50 {
				continue
			}
			for _, childID := range children[current.id] {
				if seen[childID] {
					continue
				}
				seen[childID] = true
				depth := current.depth + 1
				if child, ok := issues[childID]; ok {
					// A pinned descendant is a standing reference note, not
					// work: the rollup drops it from the subtree entirely. Its
					// own children still count, so the walk continues through
					// it — only the pinned row itself is skipped.
					if child.Status != queries.StatusPinned {
						descendants = append(descendants, child)
					}
					queue = append(queue, pending{id: childID, depth: depth})
					if depth > maxDepth {
						maxDepth = depth
					}
				}
			}
		}
		if maxDepth >= 2 {
			checkedGrandchildren = true
		}
		ready := 0
		closed := 0
		started := 0
		lastActivity := initiative.Updated
		for _, descendant := range descendants {
			if descendant.Ready {
				ready++
			}
			if descendant.Status == queries.StatusClosed {
				closed++
			}
			if descendant.Status == queries.StatusInProgress || descendant.Status == queries.StatusHooked {
				started++
			}
			if descendant.Updated.After(lastActivity) {
				lastActivity = descendant.Updated
			}
		}
		if initiative.Subtree.Total != len(descendants) {
			t.Errorf("%s: subtree total = %d, hand walk = %d", initiative.ID, initiative.Subtree.Total, len(descendants))
		}
		if initiative.Subtree.Ready != ready {
			t.Errorf("%s: ready = %d, ready_issues-backed list = %d", initiative.ID, initiative.Subtree.Ready, ready)
		}
		if initiative.Subtree.Closed != closed {
			t.Errorf("%s: closed = %d, hand walk = %d", initiative.ID, initiative.Subtree.Closed, closed)
		}
		if initiative.Subtree.Started != started {
			t.Errorf("%s: started = %d, hand walk = %d", initiative.ID, initiative.Subtree.Started, started)
		}
		if !initiative.LastActivity.Equal(lastActivity.UTC()) {
			t.Errorf("%s: last activity = %s, want %s", initiative.ID, initiative.LastActivity, lastActivity.UTC())
		}
	}
	if !checkedGrandchildren {
		t.Log("seeded database has no visible epic with grandchildren; flat subtree rollups were verified")
	}
}

func TestInitiativesDirectDeferredDescendantsReconcileBuckets(t *testing.T) {
	db := openInitiativesFixture(t, "deferred")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if got[0].Subtree.DeferScheduled != 1 {
		t.Fatalf("deferred bucket = %d, want the deferred descendant counted", got[0].Subtree.DeferScheduled)
	}
}

func TestInitiativesDependencyBlockedTakesPrecedenceOverDeferred(t *testing.T) {
	db := openInitiativesFixture(t, "blocked-and-deferred")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if got[0].Subtree.DepBlocked != 1 {
		t.Fatalf("dependency-blocked bucket = %d, want 1", got[0].Subtree.DepBlocked)
	}
	if got[0].Subtree.DeferScheduled != 0 {
		t.Fatalf("deferred bucket = %d, want dependency-blocked descendant excluded", got[0].Subtree.DeferScheduled)
	}
}
