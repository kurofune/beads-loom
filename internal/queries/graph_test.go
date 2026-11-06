package queries_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/queries"
)

// TestGraphWholeProject checks the unscoped graph against the seeded db.
func TestGraphWholeProject(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	g, err := queries.GraphFor(ctx, db, "", false)
	if err != nil {
		t.Fatalf("GraphFor: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("no nodes")
	}
	if g.Epic != "" {
		t.Errorf("epic = %q, want empty for a whole-project graph", g.Epic)
	}

	ids := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == "" || n.Title == "" || n.Type == "" || n.Status == "" {
			t.Errorf("incomplete node %+v", n)
		}
		if n.Labels == nil {
			t.Errorf("%s: labels must serialize as [] not null", n.ID)
		}
		if n.External {
			t.Errorf("%s: nothing is external in a whole-project graph", n.ID)
		}
		if n.Status == queries.StatusClosed {
			t.Errorf("%s: closed node in a graph that excluded closed", n.ID)
		}
		// Infra beads must be excluded, as everywhere else.
		for _, infra := range []string{"gate", "agent", "role", "message", "event"} {
			if n.Type == infra {
				t.Errorf("%s: infra type %q leaked into the graph", n.ID, infra)
			}
		}
		ids[n.ID] = true
	}

	// Every edge endpoint must be a node — no arrows to nowhere.
	kinds := map[string]int{}
	for _, e := range g.Edges {
		if !ids[e.From] {
			t.Errorf("edge from unknown node %q", e.From)
		}
		if !ids[e.To] {
			t.Errorf("edge to unknown node %q", e.To)
		}
		if e.From == e.To {
			t.Errorf("self-edge on %s", e.From)
		}
		kinds[e.Type]++
	}
	if len(g.Edges) == 0 {
		t.Fatal("the seeded backlog has dependencies; none came through")
	}

	// Which kinds the OPEN graph carries is not a constant. This is the repo's
	// own live backlog: every blocking pair the seed created has since been
	// closed, so an open graph with no `blocks` edge is the right answer today
	// and the wrong one the day someone files a blocked bead. Asking the
	// database which kinds survive the same filters keeps this an assertion
	// about GraphFor rather than about which beads happen to be open.
	wantKinds := openDependencyKinds(t, db)
	for kind := range wantKinds {
		if kinds[kind] == 0 {
			t.Errorf("the dependencies table has an open %s pair but the graph carries no %s edge; kinds = %v",
				kind, kind, kinds)
		}
	}
	for kind := range kinds {
		if !wantKinds[kind] {
			t.Errorf("graph carries %s edges that no open dependency pair backs; kinds = %v", kind, kinds)
		}
	}

	// The durable statement about the seeded backlog belongs on the graph over
	// all of it: closing a bead never un-closes it, so the blocking pairs the
	// seed wires stay reachable here for good. This is the assertion that goes
	// red if GraphFor ever stops surfacing a dependency kind.
	all, err := queries.GraphFor(ctx, db, "", true)
	if err != nil {
		t.Fatalf("GraphFor(includeClosed): %v", err)
	}
	allKinds := map[string]int{}
	for _, e := range all.Edges {
		allKinds[e.Type]++
	}
	for _, want := range []string{queries.DepParentChild, queries.DepBlocks} {
		if allKinds[want] == 0 {
			t.Fatalf("fixture: the whole backlog carries no %s dependency, so the graph cannot be shown to surface one; kinds = %v; run `make seed`",
				want, allKinds)
		}
	}

	t.Logf("whole project: %d nodes, %d edges, kinds=%v (all: %d edges, kinds=%v)",
		len(g.Nodes), len(g.Edges), kinds, len(all.Edges), allKinds)
}

// openDependencyKinds returns the dependency types whose two endpoints are both
// visible and both still open — exactly the edges a whole-project graph that
// excludes closed work must carry.
func openDependencyKinds(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`
SELECT DISTINCT d.type
FROM dependencies d
JOIN issues a ON a.id = d.issue_id
JOIN issues b ON b.id = d.depends_on_issue_id
WHERE `+queries.VisiblePredicate("a")+`
  AND `+queries.VisiblePredicate("b")+`
  AND a.status <> ? AND b.status <> ?`, queries.StatusClosed, queries.StatusClosed)
	if err != nil {
		t.Fatalf("read open dependency kinds: %v", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan dependency kind: %v", err)
		}
		out[kind] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read open dependency kinds: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("fixture: no dependency in the database has two open visible endpoints, so an open graph cannot be shown to carry edges; run `make seed`")
	}
	return out
}

// TestGraphEpicSubtree is the scoped case: loom-3ft's subtree.
func TestGraphEpicSubtree(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	const epic = "loom-3ft"
	g, err := queries.GraphFor(ctx, db, epic, true)
	if err != nil {
		t.Fatalf("GraphFor: %v", err)
	}
	if g.Epic != epic {
		t.Errorf("epic = %q, want %q", g.Epic, epic)
	}

	var internal, external int
	byID := map[string]queries.GraphNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
		if n.External {
			external++
		} else {
			internal++
		}
	}

	// The epic itself must be in scope and not external.
	root, ok := byID[epic]
	if !ok {
		t.Fatalf("the epic itself is missing from its own subtree")
	}
	if root.External {
		t.Error("the epic is marked external in its own graph")
	}
	if root.Type != "epic" {
		t.Errorf("root type = %q", root.Type)
	}

	// The work package's bar: at least 5 nodes in this subtree.
	if internal < 5 {
		t.Errorf("subtree has %d internal nodes, want at least 5", internal)
	}

	// Every non-external node must be a transitive child of the epic (or the
	// epic itself). loom-3ft.N are its direct children in the seeded data.
	for _, n := range g.Nodes {
		if n.External || n.ID == epic {
			continue
		}
		if !strings.HasPrefix(n.ID, epic+".") && !isDescendantByEdges(g, epic, n.ID) {
			t.Errorf("%s is in the subtree but is not a descendant of %s", n.ID, epic)
		}
	}

	// Edges must not dangle.
	for _, e := range g.Edges {
		if _, ok := byID[e.From]; !ok {
			t.Errorf("edge from unknown node %q", e.From)
		}
		if _, ok := byID[e.To]; !ok {
			t.Errorf("edge to unknown node %q", e.To)
		}
	}

	// At least one edge must touch the subtree, and the parent-child spine
	// must be present.
	var spine int
	for _, e := range g.Edges {
		if e.Type == queries.DepParentChild && e.To == epic {
			spine++
		}
	}
	if spine == 0 {
		t.Error("no parent-child edges pointing at the epic")
	}
	t.Logf("epic %s: %d internal, %d external, %d edges", epic, internal, external, len(g.Edges))
}

// isDescendantByEdges walks the graph's own parent-child edges.
func isDescendantByEdges(g *queries.Graph, root, id string) bool {
	parent := map[string]string{}
	for _, e := range g.Edges {
		if e.Type == queries.DepParentChild {
			parent[e.From] = e.To
		}
	}
	for cur, depth := id, 0; depth < 50; depth++ {
		p, ok := parent[cur]
		if !ok {
			return false
		}
		if p == root {
			return true
		}
		cur = p
	}
	return false
}

// TestGraphExternalNodesAreMarked: an edge leaving the subtree pulls the other
// end in, flagged as a boundary node.
func TestGraphExternalNodesAreMarked(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	// loom-3ft blocks loom-yqh in the seeded data, and loom-yqh is not a
	// descendant of loom-3ft.
	g, err := queries.GraphFor(ctx, db, "loom-3ft", true)
	if err != nil {
		t.Fatal(err)
	}

	var externals []string
	for _, n := range g.Nodes {
		if n.External {
			externals = append(externals, n.ID)
		}
	}
	if len(externals) == 0 {
		t.Skip("no cross-subtree edges in the current fixture")
	}

	// Every external node must be reachable by an edge from inside.
	inside := map[string]bool{}
	for _, n := range g.Nodes {
		if !n.External {
			inside[n.ID] = true
		}
	}
	for _, ext := range externals {
		var touched bool
		for _, e := range g.Edges {
			if (e.From == ext && inside[e.To]) || (e.To == ext && inside[e.From]) {
				touched = true
				break
			}
		}
		if !touched {
			t.Errorf("external node %s is not connected to the subtree", ext)
		}
	}
	t.Logf("external boundary nodes: %v", externals)
}

func TestGraphUnknownEpicIsEmpty(t *testing.T) {
	db := openSeeded(t)
	g, err := queries.GraphFor(context.Background(), db, "loom-nosuch", false)
	if err != nil {
		t.Fatalf("an unknown epic must not error: %v", err)
	}
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Errorf("graph = %+v, want empty", g)
	}
	if g.Nodes == nil || g.Edges == nil {
		t.Error("slices must serialize as [] not null")
	}
}

func TestGraphIncludeClosed(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	open, err := queries.GraphFor(ctx, db, "", false)
	if err != nil {
		t.Fatal(err)
	}
	all, err := queries.GraphFor(ctx, db, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Nodes) <= len(open.Nodes) {
		t.Errorf("includeClosed did not add nodes: %d vs %d", len(all.Nodes), len(open.Nodes))
	}

	var sawClosed bool
	for _, n := range all.Nodes {
		if n.Status == queries.StatusClosed {
			sawClosed = true
		}
	}
	if !sawClosed {
		t.Error("includeClosed returned no closed nodes")
	}
}

func TestGraphJSONShape(t *testing.T) {
	g := queries.Graph{
		Nodes: []queries.GraphNode{
			{ID: "a-1", Title: "t", Status: "open", Priority: 1, Type: "task", Ready: true, Labels: []string{}},
			{ID: "b-2", Title: "u", Status: "open", Priority: 2, Type: "task", Labels: []string{}, External: true},
		},
		Edges: []queries.GraphEdge{{From: "a-1", To: "b-2", Type: "blocks"}},
		Epic:  "a-1",
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"nodes"`, `"edges"`, `"epic":"a-1"`,
		`"id":"a-1"`, `"ready":true`, `"labels":[]`,
		`"external":true`, `"from":"a-1"`, `"to":"b-2"`, `"type":"blocks"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON missing %s\ngot: %s", want, got)
		}
	}
	// A non-external node must omit the flag entirely.
	if strings.Count(got, `"external"`) != 1 {
		t.Errorf("external should be omitted on internal nodes: %s", got)
	}
}

func TestGraphTooLargeError(t *testing.T) {
	err := &queries.GraphTooLargeError{NodeCount: 1234}
	if !strings.Contains(err.Error(), "1234") {
		t.Errorf("error = %q, want the node count", err)
	}
	if !errorsIs(err, queries.ErrGraphTooLarge) {
		t.Error("GraphTooLargeError should unwrap to ErrGraphTooLarge")
	}
}

func errorsIs(err, target error) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// --- stats extensions -------------------------------------------------------

func TestStatsTrend(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	s, err := queries.StatsFor(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Trend) != queries.TrendDays {
		t.Fatalf("trend has %d points, want %d (one per day including zeros)",
			len(s.Trend), queries.TrendDays)
	}
	if s.ReadyFrontier != s.Ready {
		t.Errorf("readyFrontier = %d, want it to equal ready %d", s.ReadyFrontier, s.Ready)
	}

	// Oldest first, one day apart, no gaps, and ending today.
	var prev time.Time
	for i, p := range s.Trend {
		day, err := time.Parse("2006-01-02", p.Date)
		if err != nil {
			t.Fatalf("trend[%d].date = %q: %v", i, p.Date, err)
		}
		if i > 0 {
			if gap := day.Sub(prev); gap != 24*time.Hour {
				t.Errorf("trend[%d] is %v after the previous point, want 24h", i, gap)
			}
		}
		prev = day
		if p.Opened < 0 || p.Closed < 0 {
			t.Errorf("trend[%d] = %+v has a negative count", i, p)
		}
	}
	// The trend ends on the DATABASE's current date, exactly.
	//
	// This used to accept "today or tomorrow in UTC", which was wrong in both
	// directions. events.created_at holds the writing process's local
	// wall-clock time and Dolt's CURDATE() resolves in the server's system
	// zone, so the whole series lives in a local day space that Go's UTC clock
	// has no claim on; and the smear window it allowed was on the wrong side —
	// a server west of Greenwich reads a day BEHIND UTC for the tail of every
	// UTC day, which the window rejected, while accepting a day ahead. That is
	// what reddened this test at 2026-08-13T00:43Z (17:43 local). Reading the
	// same clock the query reads needs no tolerance at all.
	last := s.Trend[len(s.Trend)-1].Date
	if last != dbToday(t, db) {
		t.Errorf("trend ends %q, want the database's current date %q", last, dbToday(t, db))
	}

	// The series must account for every windowed event, not merely contain
	// some. A window laid out in a different day space than the buckets drops
	// events off one end and pads the other with zeros, which is the all-zero
	// trend this query's clock note describes; totals catch that where a
	// "non-empty" check cannot. Both reads are against a live database that
	// agents write to, so a disagreement is only a finding if it survives a
	// retry.
	var opened, closed, wantOpened, wantClosed int
	for attempt := 0; attempt < 3; attempt++ {
		st, err := queries.StatsFor(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		wantOpened, wantClosed = windowedEventCounts(t, db, queries.TrendDays)
		opened, closed = 0, 0
		for _, p := range st.Trend {
			opened += p.Opened
			closed += p.Closed
		}
		if opened == wantOpened && closed == wantClosed {
			break
		}
	}
	if opened != wantOpened || closed != wantClosed {
		t.Errorf("trend totals %d opened / %d closed, but the events table holds %d / %d in the same %d-day window across 3 attempts",
			opened, closed, wantOpened, wantClosed, queries.TrendDays)
	}

	// Fixture, not environment: an empty window would make the totals check
	// above agree on nothing.
	if wantOpened == 0 {
		t.Fatalf("fixture: no 'created' events in the last %d days, so the trend has nothing to bucket; run `make seed`", queries.TrendDays)
	}
	t.Logf("trend over %d days: %d opened, %d closed", queries.TrendDays, opened, closed)

	// Trend closures cannot exceed the total closed count.
	if closed > s.ByStatus[queries.StatusClosed]+opened {
		t.Errorf("trend closed %d looks impossible against byStatus %v", closed, s.ByStatus)
	}
}

// dbToday reads the current date from the database, in the same day space the
// trend query anchors its window to.
func dbToday(t *testing.T, db *sql.DB) string {
	t.Helper()
	var today string
	if err := db.QueryRow("SELECT CAST(CURDATE() AS CHAR)").Scan(&today); err != nil {
		t.Fatalf("read the database's current date: %v", err)
	}
	return today
}

// windowedEventCounts counts the created/closed events the trend's window
// covers, spelled in the database's own day space so the comparison is against
// the events themselves and not against a second copy of the Go-side layout.
func windowedEventCounts(t *testing.T, db *sql.DB, days int) (opened, closed int) {
	t.Helper()
	err := db.QueryRow(`
SELECT
  COALESCE(SUM(CASE WHEN e.event_type = 'created' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN e.event_type = 'closed'  THEN 1 ELSE 0 END), 0)
FROM events e
JOIN issues i ON i.id = e.issue_id
WHERE `+queries.VisiblePredicate("i")+`
  AND e.event_type IN ('created', 'closed')
  AND DATE(e.created_at) BETWEEN CURDATE() - INTERVAL ? DAY AND CURDATE()`, days-1).
		Scan(&opened, &closed)
	if err != nil {
		t.Fatalf("count windowed events: %v", err)
	}
	return opened, closed
}

// --- stale ------------------------------------------------------------------

func TestStaleFor(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("nothing is stale against a fresh fixture", func(t *testing.T) {
		got, err := queries.StaleFor(ctx, db, 14, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			if loomtest.IsScratch(s.Title) {
				continue
			}
			if s.StaleDays < 14 {
				t.Errorf("%s: staleDays = %d, below the 14-day threshold", s.ID, s.StaleDays)
			}
		}
	})

	t.Run("everything is stale from far enough in the future", func(t *testing.T) {
		future := now.AddDate(1, 0, 0)
		got, err := queries.StaleFor(ctx, db, 14, future)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatal("expected every open issue to be stale a year later")
		}
		for _, s := range got {
			// Only open and in-progress work can be stale.
			if s.Status != queries.StatusOpen && s.Status != queries.StatusInProgress {
				t.Errorf("%s: status %q should not appear in the stale list", s.ID, s.Status)
			}
			if s.StaleDays < 300 {
				t.Errorf("%s: staleDays = %d, want ~365", s.ID, s.StaleDays)
			}
		}
		// Stalest first.
		for i := 1; i < len(got); i++ {
			if got[i-1].Updated.After(got[i].Updated) {
				t.Errorf("stale list out of order at %d", i)
			}
		}
	})

	t.Run("days=0 falls back to the default", func(t *testing.T) {
		a, err := queries.StaleFor(ctx, db, 0, now)
		if err != nil {
			t.Fatal(err)
		}
		b, err := queries.StaleFor(ctx, db, queries.DefaultStaleDays, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != len(b) {
			t.Errorf("days=0 returned %d, default returned %d", len(a), len(b))
		}
	})

	t.Run("closed and deferred are excluded", func(t *testing.T) {
		got, err := queries.StaleFor(ctx, db, 0, now.AddDate(1, 0, 0))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			if s.Status == queries.StatusClosed || s.Status == queries.StatusDeferred {
				t.Errorf("%s: %q work is not stale, it is finished or parked", s.ID, s.Status)
			}
		}
	})
}

func TestListIssuesByID(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	all, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Issues) < 3 {
		t.Skip("not enough issues")
	}

	// Ask in a deliberately scrambled order; bd's ordering must be preserved.
	want := []string{all.Issues[2].ID, all.Issues[0].ID, all.Issues[1].ID}
	got, err := queries.ListIssuesByID(ctx, db, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d issues, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d = %s, want %s (caller order must be preserved)", i, got[i].ID, id)
		}
	}
	// The rows must be fully enriched, not bd's thin shape.
	for _, iss := range got {
		if iss.Labels == nil {
			t.Errorf("%s: labels not hydrated", iss.ID)
		}
	}

	empty, err := queries.ListIssuesByID(ctx, db, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty input = %v, %v", empty, err)
	}
}

// TestGraphDropsOrphanedExternalNodes: an external node earns its place only by
// being connected to the subtree. When the edge that pulled it in is dropped —
// because its other endpoint was closed or is an infra bead — the node must go
// too, or it renders as a floating node with no explanation.
func TestGraphDropsOrphanedExternalNodes(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	for _, includeClosed := range []bool{false, true} {
		g, err := queries.GraphFor(ctx, db, "loom-3ft", includeClosed)
		if err != nil {
			t.Fatal(err)
		}

		connected := map[string]bool{}
		for _, e := range g.Edges {
			connected[e.From] = true
			connected[e.To] = true
		}
		for _, n := range g.Nodes {
			if n.External && !connected[n.ID] {
				t.Errorf("includeClosed=%v: external node %s has no edge", includeClosed, n.ID)
			}
		}
	}
}
