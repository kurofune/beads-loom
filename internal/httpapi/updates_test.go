package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/integration/djinn"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/runs"
	"github.com/kurofune/beads-loom/internal/updates"
)

// --- fixture --------------------------------------------------------------

var updatesDriverSeq atomic.Int64

// updatesConn answers the one statement the updates endpoints issue: the cited
// issue-id existence check. Only the ids it was told about come back.
type updatesConn struct {
	real map[string]bool
	err  error
	// asked records every id the handler checked.
	asked []string
}

func (*updatesConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}
func (*updatesConn) Close() error { return nil }
func (*updatesConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

var _ driver.QueryerContext = (*updatesConn)(nil)

func (c *updatesConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.err != nil {
		return nil, c.err
	}
	if !strings.Contains(query, "FROM issues WHERE id IN") {
		return nil, errors.New("unexpected query: " + query)
	}
	rows := &updatesRows{columns: []string{"id"}}
	for _, a := range args {
		id, _ := a.Value.(string)
		c.asked = append(c.asked, id)
		if c.real[id] {
			rows.values = append(rows.values, []driver.Value{id})
		}
	}
	return rows, nil
}

type updatesRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *updatesRows) Columns() []string { return r.columns }
func (*updatesRows) Close() error        { return nil }
func (r *updatesRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

type updatesDriver struct{ conn *updatesConn }

func (d updatesDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

// newUpdatesServer wires a server whose bd is a mock and whose SQL is scripted.
// No integration is mounted, so the server has no run history.
func newUpdatesServer(t *testing.T, conn *updatesConn) (*Server, *bdexec.MockRunner, string) {
	t.Helper()
	return buildUpdatesServer(t, conn, func(string) integration.Set { return nil })
}

// newUpdatesServerWithRunHistory is newUpdatesServer with the djinn provider
// registered through New(Options{Integrations: ...}), the way the binary does
// it. Its run history holds one persisted run per id.
func newUpdatesServerWithRunHistory(t *testing.T, conn *updatesConn, runIDs ...string) (*Server, *bdexec.MockRunner, string) {
	t.Helper()
	return buildUpdatesServer(t, conn, func(workdir string) integration.Set {
		if err := os.MkdirAll(filepath.Join(workdir, ".djinn"), 0o755); err != nil {
			t.Fatal(err)
		}
		provider := djinn.New(djinn.Options{StateDir: t.TempDir()})
		for i, id := range runIDs {
			run := runs.Run{
				ID:        id,
				Project:   "loom",
				State:     runs.StateFinished,
				Source:    runs.SourceLoom,
				StartedAt: time.Date(2026, 8, 4, 12, i, 0, 0, time.UTC),
			}
			if err := provider.Store().Save(run); err != nil {
				t.Fatal(err)
			}
		}
		return integration.Set{provider}
	})
}

func buildUpdatesServer(t *testing.T, conn *updatesConn, integrations func(workdir string) integration.Set) (*Server, *bdexec.MockRunner, string) {
	t.Helper()
	workdir := t.TempDir()
	reg := registry.New(&config.Config{Projects: []config.Project{{Name: "loom", Workdir: workdir}}})
	t.Cleanup(func() { reg.Close() })

	name := "loom-updates-http-" + itoa(updatesDriverSeq.Add(1))
	sql.Register(name, updatesDriver{conn})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	bdRunner := bdexec.NewMockRunner()
	s := New(Options{Registry: reg, BD: bdexec.New(bdRunner), Integrations: integrations(workdir)})
	s.queryDB = func(ctx context.Context, project string, fn func(context.Context, *sql.DB) error) error {
		if project != "loom" {
			t.Fatalf("queryDB project = %q, want loom", project)
		}
		return fn(ctx, db)
	}
	return s, bdRunner, workdir
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// kvBackedRunner answers `kv list` from an in-memory store, records `kv set`
// into it, and fails anything else — so a test can watch a publish round trip.
func kvBackedRunner(store map[string]string) *bdexec.MockRunner {
	runner := bdexec.NewMockRunner()
	runner.Handler = func(call bdexec.Call) (string, string, error) {
		switch {
		case len(call.Args) >= 2 && call.Args[0] == "kv" && call.Args[1] == "list":
			out, _ := json.Marshal(store)
			return string(out), "", nil
		case len(call.Args) >= 3 && call.Args[0] == "kv" && call.Args[1] == "get":
			value, found := store[call.Args[2]]
			out, _ := json.Marshal(map[string]any{"found": found, "key": call.Args[2], "value": value})
			return string(out), "", nil
		case len(call.Args) >= 4 && call.Args[0] == "kv" && call.Args[1] == "set":
			store[call.Args[2]] = call.Args[3]
			return "", "", nil
		default:
			return "", "unexpected bd call: " + call.Argv(), errors.New("exit status 1")
		}
	}
	return runner
}

func storedValue(t *testing.T, epic, week, status, body string) string {
	t.Helper()
	out, err := json.Marshal(map[string]any{
		"schema": 1, "epic": epic, "week": week, "status": status,
		"generated_at": "2026-08-08T01:17:20Z", "body": body,
		// An unknown field the producer may add later. Publish must preserve it.
		"reviewers": []string{"nate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// --- read endpoints -------------------------------------------------------

func TestInitiativeUpdateSummariesCountEveryEpicInOneKVRead(t *testing.T) {
	store := map[string]string{
		updates.Key("loom-d1q", "2026-W31"): storedValue(t, "loom-d1q", "2026-W31", "draft", "# a"),
		updates.Key("loom-d1q", "2026-W32"): storedValue(t, "loom-d1q", "2026-W32", "published", "# b"),
		updates.Key("loom-yqh", "2026-W32"): storedValue(t, "loom-yqh", "2026-W32", "draft", "# c"),
		"memory.note":                       "remembered",
	}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiative-updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Updates []updates.Summary `json:"updates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []updates.Summary{
		{Epic: "loom-d1q", Total: 2, DraftCount: 1, LatestWeek: "2026-W32", LatestStatus: "published"},
		{Epic: "loom-yqh", Total: 1, DraftCount: 1, LatestWeek: "2026-W32", LatestStatus: "draft"},
	}
	if !reflect.DeepEqual(got.Updates, want) {
		t.Fatalf("summaries = %#v, want %#v", got.Updates, want)
	}

	unknown := do(t, s, http.MethodGet, "/api/projects/loom/initiative-updates?epic=loom-d1q", "")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown parameter status = %d, want 400", unknown.Code)
	}
}

func TestInitiativeUpdatesReturnHistoryNewestFirstWithResolvedCitations(t *testing.T) {
	store := map[string]string{
		updates.Key("loom-d1q", "2026-W31"): storedValue(t, "loom-d1q", "2026-W31", "draft",
			"## Started\n- loom-d1q.1 in flight (loom-d1q.1)\n"),
		updates.Key("loom-d1q", "2026-W32"): storedValue(t, "loom-d1q", "2026-W32", "published",
			"## Closed\n- loom-d1q.1 landed — read-only view shipped in b64f334 (loom-d1q.1)\n"),
		updates.Key("loom-yqh", "2026-W32"): storedValue(t, "loom-yqh", "2026-W32", "draft", "# other epic"),
	}
	conn := &updatesConn{real: map[string]bool{"loom-d1q.1": true}}
	s, _, _ := newUpdatesServer(t, conn)
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/loom-d1q/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Updates       []updates.Update `json:"updates"`
		KnownIssueIDs []string         `json:"knownIssueIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Updates) != 2 {
		t.Fatalf("updates = %d, want this epic's two weeks only: %#v", len(got.Updates), got.Updates)
	}
	if got.Updates[0].Week != "2026-W32" || got.Updates[1].Week != "2026-W31" {
		t.Fatalf("weeks = %q, %q; want newest first", got.Updates[0].Week, got.Updates[1].Week)
	}
	if got.Updates[0].BodySHA != updates.BodySHA(got.Updates[0].Body) {
		t.Fatalf("bodySha does not match the body it was computed from")
	}
	if !reflect.DeepEqual(got.KnownIssueIDs, []string{"loom-d1q.1"}) {
		t.Fatalf("knownIssueIds = %#v, want only the id that resolves", got.KnownIssueIDs)
	}
	// "read-only" is id-shaped prose; it must be offered to the database and
	// then dropped, never linkified on shape alone.
	askedReadOnly := false
	for _, id := range conn.asked {
		if id == "read-only" {
			askedReadOnly = true
		}
	}
	if !askedReadOnly {
		t.Fatalf("asked = %#v, want the id-shaped prose checked and rejected", conn.asked)
	}
}

// A run id is a citation shape with a Loom surface behind it, so it resolves
// the way a bead id does: only the ids the project's run history knows come
// back, and only those become links.
func TestInitiativeUpdatesResolveCitedRunIDsAgainstRunHistory(t *testing.T) {
	const known = "run-1754270000000000000"
	const stale = "run-1600000000000000000"
	store := map[string]string{
		updates.Key("loom-d1q", "2026-W32"): storedValue(t, "loom-d1q", "2026-W32", "draft",
			"## Closed\n- loom-d1q.1 landed, verified by "+known+
				"\n- loom-d1q.2 started under "+stale+"\n"),
	}
	s, _, _ := newUpdatesServerWithRunHistory(t, &updatesConn{real: map[string]bool{"loom-d1q.1": true}}, known)
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/loom-d1q/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		KnownIssueIDs []string `json:"knownIssueIds"`
		KnownRunIDs   []string `json:"knownRunIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.KnownRunIDs, []string{known}) {
		t.Fatalf("knownRunIds = %#v, want only the run the history has", got.KnownRunIDs)
	}
	if !reflect.DeepEqual(got.KnownIssueIDs, []string{"loom-d1q.1"}) {
		t.Fatalf("knownIssueIds = %#v, want the bead half unchanged", got.KnownIssueIDs)
	}
}

// A citation written before external run ids named their project carries the
// ext-<timestamp> alias. Against real external history it links exactly when
// that alias denotes a run the project has, as History.Get resolves it.
func TestInitiativeUpdatesResolveALegacyExternalRunCitation(t *testing.T) {
	const legacy = "ext-20260624T074233Z"
	const noRun = "ext-20260624T074234Z"
	store := map[string]string{
		updates.Key("loom-d1q", "2026-W26"): storedValue(t, "loom-d1q", "2026-W26", "draft",
			"## Closed\n- loom-d1q.1 landed under "+legacy+
				"\n- loom-d1q.2 started under "+noRun+"\n"),
	}
	s, _, _ := buildUpdatesServer(t, &updatesConn{real: map[string]bool{"loom-d1q.1": true}}, func(workdir string) integration.Set {
		logDir := runs.Project{Name: "loom", Workdir: workdir}.LegacySummonerLogDir()
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(logDir, "2026-06-24.log"),
			"2026-06-24T07:42:33Z\tloom-d1q.1\tspawn\n"+
				"2026-06-24T07:55:50Z\tloom-d1q.1\texit\tCOMPLETE\tclosed\n")
		return integration.Set{djinn.New(djinn.Options{StateDir: t.TempDir()})}
	})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/loom-d1q/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		KnownRunIDs []string `json:"knownRunIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.KnownRunIDs, []string{legacy}) {
		t.Fatalf("knownRunIds = %#v, want the legacy alias of the run the history has, and not the one of no run", got.KnownRunIDs)
	}
}

// Update history is served for every project; run history exists only for a
// djinn project. A project without one answers with no linkable runs, not a 500.
func TestInitiativeUpdatesReturnNoKnownRunIDsWithoutRunHistory(t *testing.T) {
	store := map[string]string{
		updates.Key("loom-d1q", "2026-W32"): storedValue(t, "loom-d1q", "2026-W32", "draft",
			"- loom-d1q.1 landed, verified by run-1754270000000000000\n"),
	}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/loom-d1q/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"knownRunIds":[]`) {
		t.Fatalf("body = %s, want an empty knownRunIds array", rec.Body.String())
	}
}

func TestInitiativeUpdatesStillRenderWhenCitationResolutionFails(t *testing.T) {
	store := map[string]string{
		updates.Key("loom-d1q", "2026-W32"): storedValue(t, "loom-d1q", "2026-W32", "draft", "- loom-d1q.1 landed"),
	}
	s, _, _ := newUpdatesServer(t, &updatesConn{err: errors.New("dial tcp: connection refused")})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/loom-d1q/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the history is the point, links are a nicety", rec.Code)
	}
	var got struct {
		Updates       []updates.Update `json:"updates"`
		KnownIssueIDs []string         `json:"knownIssueIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Updates) != 1 || len(got.KnownIssueIDs) != 0 {
		t.Fatalf("got %#v, want the update with no resolved citations", got)
	}
}

func TestInitiativeUpdatesSurfaceAKVReadFailure(t *testing.T) {
	s, bdRunner, _ := newUpdatesServer(t, &updatesConn{})
	bdRunner.Err = errors.New("exit status 1")
	bdRunner.Stderr = "bd: no beads database in this directory"

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/loom-d1q/updates", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no beads database") {
		t.Fatalf("body = %s, want bd's own stderr", rec.Body.String())
	}
}

func TestInitiativeUpdatesRejectAMalformedEpic(t *testing.T) {
	s, bdRunner, _ := newUpdatesServer(t, &updatesConn{})

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/--claim/updates", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if len(bdRunner.Calls()) != 0 {
		t.Fatalf("bd calls = %#v, want none before validation", bdRunner.Calls())
	}
}

func TestInitiativeUpdatesKeepAMalformedValueAsARow(t *testing.T) {
	store := map[string]string{
		updates.Key("loom-d1q", "2026-W30"): "{ this is not json",
		updates.Key("loom-d1q", "2026-W31"): storedValue(t, "loom-d1q", "2026-W31", "draft", "# fine"),
	}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives/loom-d1q/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Updates []updates.Update `json:"updates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Updates) != 2 || got.Updates[1].ParseError == "" {
		t.Fatalf("updates = %#v, want the malformed week kept as a parseError row", got.Updates)
	}
}

// --- publish --------------------------------------------------------------

func TestPublishFlipsOnlyTheStatusAndPreservesUnknownFields(t *testing.T) {
	const body = "## Closed\n- loom-d1q.1 landed (loom-d1q.1)\n"
	key := updates.Key("loom-d1q", "2026-W32")
	store := map[string]string{key: storedValue(t, "loom-d1q", "2026-W32", "draft", body)}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodPost,
		"/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish",
		`{"bodySha":"`+updates.BodySHA(body)+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var stored map[string]any
	if err := json.Unmarshal([]byte(store[key]), &stored); err != nil {
		t.Fatalf("stored value is not JSON: %v", err)
	}
	if stored["status"] != "published" {
		t.Fatalf("status = %v, want published", stored["status"])
	}
	if stored["body"] != body {
		t.Fatalf("body changed during publish")
	}
	if stored["generated_at"] != "2026-08-08T01:17:20Z" || stored["epic"] != "loom-d1q" {
		t.Fatalf("publish rewrote a field it does not own: %#v", stored)
	}
	if !reflect.DeepEqual(stored["reviewers"], []any{"nate"}) {
		t.Fatalf("publish dropped the unknown field: %#v", stored)
	}
	if n, ok := stored["schema"].(float64); !ok || n != 1 {
		t.Fatalf("schema = %#v, want the number 1", stored["schema"])
	}
}

func TestPublishRefusesAnUncitedClaimAndNamesIt(t *testing.T) {
	const body = "## Closed\n- loom-d1q.1 landed (loom-d1q.1)\n- We tidied up a few things\n- And polished some more\n"
	key := updates.Key("loom-d1q", "2026-W32")
	store := map[string]string{key: storedValue(t, "loom-d1q", "2026-W32", "draft", body)}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodPost,
		"/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish",
		`{"bodySha":"`+updates.BodySHA(body)+`"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "We tidied up a few things") {
		t.Fatalf("error = %s, want the offending claim named verbatim", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "2 claims") {
		t.Fatalf("error = %s, want the count of uncited claims", rec.Body.String())
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(store[key]), &stored); err != nil {
		t.Fatal(err)
	}
	if stored["status"] != "draft" {
		t.Fatalf("a refused publish still wrote: status = %v", stored["status"])
	}
}

func TestPublishNeverOverwritesAPublishedUpdate(t *testing.T) {
	const body = "## Closed\n- loom-d1q.1 landed (loom-d1q.1)\n"
	key := updates.Key("loom-d1q", "2026-W32")
	store := map[string]string{key: storedValue(t, "loom-d1q", "2026-W32", "published", body)}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodPost,
		"/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish",
		`{"bodySha":"`+updates.BodySHA(body)+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "never overwritten") {
		t.Fatalf("error = %s, want the frozen-update explanation", rec.Body.String())
	}
}

func TestPublishRefusesADraftThatMovedSinceItWasReviewed(t *testing.T) {
	key := updates.Key("loom-d1q", "2026-W32")
	store := map[string]string{
		key: storedValue(t, "loom-d1q", "2026-W32", "draft", "## Closed\n- loom-d1q.1 landed (loom-d1q.1)\n"),
	}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	rec := do(t, s, http.MethodPost,
		"/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish",
		`{"bodySha":"`+updates.BodySHA("an older body")+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "reload") {
		t.Fatalf("error = %s, want the reload-and-review instruction", rec.Body.String())
	}
}

func TestPublishRejectsMissingKeysMalformedAddressesAndEmptyDigests(t *testing.T) {
	store := map[string]string{}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	s.bd = bdexec.New(kvBackedRunner(store))

	tests := []struct {
		name, path, body string
		want             int
	}{
		{"no stored update", "/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish", `{"bodySha":"abc"}`, http.StatusNotFound},
		{"bad epic", "/api/projects/loom/initiatives/--claim/updates/2026-W32/publish", `{"bodySha":"abc"}`, http.StatusBadRequest},
		{"bad week", "/api/projects/loom/initiatives/loom-d1q/updates/2026-Q3/publish", `{"bodySha":"abc"}`, http.StatusBadRequest},
		{"no digest", "/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish", `{}`, http.StatusBadRequest},
		{"unknown field", "/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish", `{"bodySha":"abc","force":true}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, s, http.MethodPost, tc.path, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPublishReportsBdStderrVerbatimOnAWriteFailure(t *testing.T) {
	const body = "## Closed\n- loom-d1q.1 landed (loom-d1q.1)\n"
	key := updates.Key("loom-d1q", "2026-W32")
	store := map[string]string{key: storedValue(t, "loom-d1q", "2026-W32", "draft", body)}
	s, _, _ := newUpdatesServer(t, &updatesConn{})
	runner := kvBackedRunner(store)
	inner := runner.Handler
	runner.Handler = func(call bdexec.Call) (string, string, error) {
		if len(call.Args) >= 2 && call.Args[0] == "kv" && call.Args[1] == "set" {
			return "", "bd: dolt server refused the write", errors.New("exit status 1")
		}
		return inner(call)
	}
	s.bd = bdexec.New(runner)

	rec := do(t, s, http.MethodPost,
		"/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish",
		`{"bodySha":"`+updates.BodySHA(body)+`"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dolt server refused the write") {
		t.Fatalf("body = %s, want bd's stderr verbatim", rec.Body.String())
	}
}

func TestPublishGoesThroughBdKvNotSQL(t *testing.T) {
	const body = "## Closed\n- loom-d1q.1 landed (loom-d1q.1)\n"
	key := updates.Key("loom-d1q", "2026-W32")
	store := map[string]string{key: storedValue(t, "loom-d1q", "2026-W32", "draft", body)}
	conn := &updatesConn{}
	s, _, workdir := newUpdatesServer(t, conn)
	runner := kvBackedRunner(store)
	s.bd = bdexec.New(runner)

	rec := do(t, s, http.MethodPost,
		"/api/projects/loom/initiatives/loom-d1q/updates/2026-W32/publish",
		`{"bodySha":"`+updates.BodySHA(body)+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var argvs []string
	for _, call := range runner.Calls() {
		if call.Workdir != workdir {
			t.Fatalf("call ran in %q, want the project workdir %q", call.Workdir, workdir)
		}
		argvs = append(argvs, strings.Join(call.Args[:2], " "))
	}
	want := []string{"kv get", "kv set"}
	if !reflect.DeepEqual(argvs, want) {
		t.Fatalf("bd calls = %#v, want exactly %#v", argvs, want)
	}
	if len(conn.asked) != 0 {
		t.Fatalf("publish touched SQL: %#v", conn.asked)
	}
}
