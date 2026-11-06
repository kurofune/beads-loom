package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/queries"
)

// handleGraph returns the dependency graph, optionally scoped to an epic.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	q := r.URL.Query()
	for key := range q {
		if key != "epic" && key != "includeClosed" {
			writeError(w, http.StatusBadRequest, "unknown query parameter "+key+" (known: epic, includeClosed)")
			return
		}
	}

	epic := strings.TrimSpace(q.Get("epic"))
	if epic != "" && !bdexec.ValidIssueID(epic) {
		writeError(w, http.StatusBadRequest, "invalid epic id")
		return
	}
	includeClosed, err := boolParam(q, "includeClosed")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	graph, err := cachedRead(r.Context(), s, project, "graph", cacheParams(q),
		func(ctx context.Context, db *sql.DB) (*queries.Graph, error) {
			return queries.GraphFor(ctx, db, epic, includeClosed)
		})
	if err != nil {
		var tooLarge *queries.GraphTooLargeError
		if errors.As(err, &tooLarge) {
			// 413 with the count, so the UI can say how far over and suggest
			// scoping to an epic.
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error":     "graph too large to render; scope it to an epic",
				"nodeCount": tooLarge.NodeCount,
				"limit":     queries.MaxGraphNodes,
			})
			return
		}
		s.writeRegistryError(w, project, err)
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

// handleStale lists issues nobody has touched recently.
func (s *Server) handleStale(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	q := r.URL.Query()
	for key := range q {
		if key != "days" {
			writeError(w, http.StatusBadRequest, "unknown query parameter "+key+" (known: days)")
			return
		}
	}

	days := queries.DefaultStaleDays
	if v, err := optionalInt(q, "days"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if v != nil {
		if *v < 0 || *v > queries.MaxStaleDays {
			writeError(w, http.StatusBadRequest, "days must be between 0 and 3650")
			return
		}
		days = *v
	}

	// The candidate list is cached; which of them count as stale depends on
	// the clock, so that cut is made fresh on every request.
	candidates, err := cachedRead(r.Context(), s, project, "stale-candidates", "", queries.StaleCandidates)
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}
	stale := queries.StaleFrom(candidates, days, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"issues": stale, "days": days})
}

// queryResponse is the bd-query passthrough result.
type queryResponse struct {
	Issues []queries.Issue `json:"issues"`
	Total  int             `json:"total"`
	// Query echoes the expression that produced this.
	Query string `json:"query"`
}

// handleQuery is the `bd query` escape hatch.
//
// bd owns the query language, so Loom shells out rather than reimplementing it.
// The results are then mapped into the standard Issue shape and enriched with
// the readiness flag and labels from SQL, so a query result renders with the
// same card component as the backlog.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	q := r.URL.Query()
	for key := range q {
		if key != "q" {
			writeError(w, http.StatusBadRequest, "unknown query parameter "+key+" (known: q)")
			return
		}
	}
	expr := strings.TrimSpace(q.Get("q"))
	if expr == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}

	workdir, ok := s.readWorkdirFor(w, project)
	if !ok {
		return
	}

	results, err := s.bd.Query(r.Context(), workdir, expr)
	if err != nil {
		if errors.Is(err, bdexec.ErrQuerySyntax) || errors.Is(err, bdexec.ErrInvalidID) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if be, ok := bdexec.AsBdError(err); ok {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Error: "bd query failed", Stderr: be.Message(),
			})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	issues := s.hydrateQueryResults(r.Context(), project, results)
	writeJSON(w, http.StatusOK, queryResponse{Issues: issues, Total: len(issues), Query: expr})
}

// hydrateQueryResults maps bd's rows onto Loom's Issue shape.
//
// bd's JSON has no readiness flag and no dependency counts, so the ids it
// returned are looked up once in SQL and the full Issue rows used where they
// exist.
//
// Enrichment is best-effort by design. bd has already answered the user's
// question; if the dolt server cannot be reached to add readiness and labels,
// returning bd's own fields is far better than failing a query that succeeded.
// The same fallback covers a row SQL does not return — an infra bead bd
// matched, say — which would otherwise vanish from the result.
func (s *Server) hydrateQueryResults(ctx context.Context, project string, results []bdexec.QueryResult) []queries.Issue {
	out := make([]queries.Issue, 0, len(results))
	if len(results) == 0 {
		return out
	}

	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}

	enriched := map[string]queries.Issue{}
	if err := s.reg.Do(ctx, project, func(ctx context.Context, db *sql.DB) error {
		list, err := queries.ListIssuesByID(ctx, db, ids)
		if err != nil {
			return err
		}
		for _, iss := range list {
			enriched[iss.ID] = iss
		}
		return nil
	}); err != nil {
		s.log.Warn("bd query results could not be enriched from SQL; returning bd's fields",
			"project", project, "err", err)
	}

	for _, r := range results {
		if iss, ok := enriched[r.ID]; ok {
			out = append(out, iss)
			continue
		}
		out = append(out, queries.Issue{
			ID: r.ID, Title: r.Title, Type: r.IssueType, Status: r.Status,
			Priority: r.Priority, Assignee: r.Assignee, Labels: nonNil(r.Labels),
			Estimate: r.Estimated,
			Created:  derefTime(r.CreatedAt), Updated: derefTime(r.UpdatedAt),
			Closed: r.ClosedAt, Due: r.DueAt, DeferUntil: r.DeferUntil,
		})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	sort.Strings(s)
	return s
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// boolParam parses an optional boolean query parameter.
func boolParam(q map[string][]string, key string) (bool, error) {
	vals := q[key]
	if len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(vals[0])) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, errInvalid(key, vals[0], "expected true or false")
	}
}
