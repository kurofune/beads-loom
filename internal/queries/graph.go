package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// MaxGraphNodes caps a graph response. A dependency graph stops being readable
// long before this, and rendering thousands of nodes in a browser is worse than
// telling the user to scope to an epic.
const MaxGraphNodes = 500

// ErrGraphTooLarge is returned when a graph would exceed MaxGraphNodes. The
// handler turns it into 413 with the count, so the UI can say how far over.
var ErrGraphTooLarge = errors.New("graph too large")

// GraphNode is one issue in the dependency graph.
type GraphNode struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Priority int      `json:"priority"`
	Type     string   `json:"type"`
	Ready    bool     `json:"ready"`
	Labels   []string `json:"labels"`
	// External marks a node pulled in only because an edge reaches it from the
	// requested subtree. It is not part of the epic and the UI should render it
	// as a boundary node.
	External bool `json:"external,omitempty"`
}

// GraphEdge is one dependency. Direction matches the dependencies table: From
// depends on To.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// Graph is the /graph response.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	// Epic echoes the requested scope, empty for a whole-project graph.
	Epic string `json:"epic,omitempty"`
}

// GraphTooLargeError carries the node count that tripped the cap.
type GraphTooLargeError struct {
	NodeCount int
}

func (e *GraphTooLargeError) Error() string {
	return fmt.Sprintf("%s: %d nodes exceeds the %d limit", ErrGraphTooLarge, e.NodeCount, MaxGraphNodes)
}

func (e *GraphTooLargeError) Unwrap() error { return ErrGraphTooLarge }

// subtreeCTE walks an epic's transitive parent-child descendants.
//
// The depth guard mirrors the ready_issues view: a malformed parent-child cycle
// must not spin here. Dependencies are stored child→parent, so descending means
// following depends_on_issue_id backwards.
const subtreeDefinition = `subtree AS (
  SELECT id AS root, id, 0 AS depth FROM issues WHERE id = ? AND issue_type = 'epic'
  UNION ALL
  SELECT s.root, d.issue_id, s.depth + 1
  FROM subtree s
  JOIN dependencies d ON d.depends_on_issue_id = s.id AND d.type = 'parent-child'
  WHERE s.depth < 50
)`

const subtreeCTE = `
WITH RECURSIVE ` + subtreeDefinition + `
SELECT DISTINCT id FROM subtree`

// GraphFor builds the dependency graph for a project.
//
// With no epic the whole project is returned. With an epic, the scope is that
// epic's parent-child subtree, plus any node an edge reaches from inside it —
// those are flagged External so the UI can draw the boundary.
func GraphFor(ctx context.Context, db *sql.DB, epic string, includeClosed bool) (*Graph, error) {
	g := &Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}, Epic: epic}

	scope, err := graphScope(ctx, db, epic)
	if err != nil {
		return nil, err
	}
	if epic != "" && len(scope) == 0 {
		// The epic does not exist, or is itself an infra row.
		return g, nil
	}

	edges, referenced, err := graphEdges(ctx, db, scope)
	if err != nil {
		return nil, err
	}

	// The node set is the scope plus anything an edge reached.
	want := map[string]bool{}
	for id := range scope {
		want[id] = true
	}
	for id := range referenced {
		want[id] = true
	}

	nodes, err := graphNodes(ctx, db, want, epic == "", includeClosed)
	if err != nil {
		return nil, err
	}
	if len(nodes) > MaxGraphNodes {
		return nil, &GraphTooLargeError{NodeCount: len(nodes)}
	}

	present := make(map[string]bool, len(nodes))
	for i := range nodes {
		present[nodes[i].ID] = true
		if epic != "" && !scope[nodes[i].ID] {
			nodes[i].External = true
		}
	}

	// Drop edges whose endpoints did not survive node filtering (infra beads,
	// or closed issues when they were excluded). A dangling edge would render
	// as an arrow to nowhere.
	kept := make([]GraphEdge, 0, len(edges))
	connected := map[string]bool{}
	for _, e := range edges {
		if present[e.From] && present[e.To] {
			kept = append(kept, e)
			connected[e.From] = true
			connected[e.To] = true
		}
	}

	// Then drop external nodes left with no surviving edge. An external node
	// only earns its place by being connected to the subtree; once the edge
	// that pulled it in is gone — because its other end was closed or is an
	// infra bead — it would render as a floating, unexplained node. Internal
	// nodes stay regardless: they are in scope even when isolated.
	if epic != "" {
		attached := nodes[:0]
		for _, n := range nodes {
			if n.External && !connected[n.ID] {
				continue
			}
			attached = append(attached, n)
		}
		nodes = attached
	}

	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Priority != nodes[j].Priority {
			return nodes[i].Priority < nodes[j].Priority
		}
		return nodes[i].ID < nodes[j].ID
	})
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].From != kept[j].From {
			return kept[i].From < kept[j].From
		}
		if kept[i].To != kept[j].To {
			return kept[i].To < kept[j].To
		}
		return kept[i].Type < kept[j].Type
	})

	g.Nodes, g.Edges = nodes, kept
	return g, nil
}

// graphScope returns the ids in scope. An empty epic yields a nil set, meaning
// "the whole project".
func graphScope(ctx context.Context, db *sql.DB, epic string) (map[string]bool, error) {
	if epic == "" {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx, subtreeCTE, epic)
	if err != nil {
		return nil, fmt.Errorf("graph subtree: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan subtree id: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// graphEdges returns the dependency edges in scope and every id they mention.
//
// With a nil scope every edge is returned; with a scope, an edge is kept when
// either endpoint is inside it, which is what pulls in external neighbours.
func graphEdges(ctx context.Context, db *sql.DB, scope map[string]bool) ([]GraphEdge, map[string]bool, error) {
	const base = `SELECT d.issue_id, d.depends_on_issue_id, d.type
FROM dependencies d
WHERE d.depends_on_issue_id IS NOT NULL`

	query := base
	var args []any
	if scope != nil {
		ids := sortedKeys(scope)
		ph := placeholders(len(ids))
		query = base + " AND (d.issue_id IN (" + ph + ") OR d.depends_on_issue_id IN (" + ph + "))"
		for _, id := range ids {
			args = append(args, id)
		}
		for _, id := range ids {
			args = append(args, id)
		}
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("graph edges: %w", err)
	}
	defer rows.Close()

	var edges []GraphEdge
	referenced := map[string]bool{}
	for rows.Next() {
		var e GraphEdge
		if err := rows.Scan(&e.From, &e.To, &e.Type); err != nil {
			return nil, nil, fmt.Errorf("scan edge: %w", err)
		}
		edges = append(edges, e)
		referenced[e.From] = true
		referenced[e.To] = true
	}
	return edges, referenced, rows.Err()
}

// graphNodes loads the issue rows for a node set, applying the usual infra
// exclusion. wholeProject ignores the id set and takes everything visible.
func graphNodes(ctx context.Context, db *sql.DB, want map[string]bool, wholeProject, includeClosed bool) ([]GraphNode, error) {
	var (
		where = VisiblePredicate("i")
		args  []any
	)
	if !wholeProject {
		if len(want) == 0 {
			return []GraphNode{}, nil
		}
		ids := sortedKeys(want)
		where += " AND i.id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if !includeClosed {
		where += " AND i.status <> ?"
		args = append(args, StatusClosed)
	}

	rows, err := db.QueryContext(ctx, `
SELECT i.id, i.title, i.status, i.priority, i.issue_type, (r.id IS NOT NULL) AS ready
FROM issues i
LEFT JOIN ready_issues r ON r.id = i.id
WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("graph nodes: %w", err)
	}
	defer rows.Close()

	nodes := []GraphNode{}
	index := map[string]int{}
	for rows.Next() {
		var n GraphNode
		if err := rows.Scan(&n.ID, &n.Title, &n.Status, &n.Priority, &n.Type, &n.Ready); err != nil {
			return nil, fmt.Errorf("scan graph node: %w", err)
		}
		n.Labels = []string{}
		index[n.ID] = len(nodes)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nodes, nil
	}

	// Labels in one round trip, same reason as the list query.
	ids := make([]any, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	labelRows, err := db.QueryContext(ctx,
		"SELECT issue_id, label FROM labels WHERE issue_id IN ("+placeholders(len(ids))+") ORDER BY issue_id, label", ids...)
	if err != nil {
		return nil, fmt.Errorf("graph labels: %w", err)
	}
	defer labelRows.Close()
	for labelRows.Next() {
		var id, label string
		if err := labelRows.Scan(&id, &label); err != nil {
			return nil, fmt.Errorf("scan graph label: %w", err)
		}
		if i, ok := index[id]; ok {
			nodes[i].Labels = append(nodes[i].Labels, label)
		}
	}
	return nodes, labelRows.Err()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ListIssuesByID returns full Issue rows for a set of ids, in the order given.
//
// It exists for the bd-query passthrough: bd owns the query language and
// returns its own row shape, but the UI renders the standard Issue card, so the
// ids bd matched are looked up here to pick up readiness, dependency counts,
// parent and child rollups.
func ListIssuesByID(ctx context.Context, db *sql.DB, ids []string) ([]Issue, error) {
	if len(ids) == 0 {
		return []Issue{}, nil
	}
	if len(ids) > MaxLimit {
		ids = ids[:MaxLimit]
	}

	list, err := ListIssues(ctx, db, Filter{IDs: ids, IncludeClosed: true, Limit: MaxLimit})
	if err != nil {
		return nil, err
	}

	// Preserve bd's ordering: it may have sorted by something Loom's filter
	// cannot express, and the user asked bd, not us.
	pos := make(map[string]int, len(ids))
	for i, id := range ids {
		pos[id] = i
	}
	out := append([]Issue(nil), list.Issues...)
	sort.SliceStable(out, func(i, j int) bool { return pos[out[i].ID] < pos[out[j].ID] })
	return out, nil
}
