package djinn

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/runs"
)

// handlers serves the djinn routes for one mount.
type handlers struct {
	p   *Provider
	reg *registry.Registry
	log *slog.Logger
}

var (
	writeJSON  = integration.WriteJSON
	writeError = integration.WriteError
	decodeBody = integration.DecodeBody
)

type errorResponse = integration.ErrorBody

// runRoutes registers the summoner and run-monitoring endpoints.
func (h *handlers) runRoutes(r chi.Router) {
	r.Post("/summon/preview", h.handleSummonPreview)
	r.Post("/summon", h.handleSummon)
	r.Get("/runs", h.handleRunList)
	r.Get("/runs/{runID}", h.handleRunDetail)
	r.Get("/runs/{runID}/log", h.handleRunLog)
	r.Post("/runs/{runID}/stop", h.handleRunStop)
	r.Get("/beads/{id}/forensics", h.handleForensics)
}

// project resolves the URL's project. Core's presence gate has already
// answered 404 for a project without the djinn integration; the .djinn check
// here covers a directory removed since the last registry refresh.
func (h *handlers) project(w http.ResponseWriter, name string) (runs.Project, bool) {
	p, err := h.reg.Get(name)
	if err != nil {
		if errors.Is(err, registry.ErrUnknownProject) {
			writeError(w, http.StatusNotFound, "no such project: "+name)
			return runs.Project{}, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return runs.Project{}, false
	}

	proj := runs.Project{Name: p.Name, Workdir: p.Workdir}
	if !proj.IsDjinnProject() {
		writeError(w, http.StatusNotFound, "project "+name+" is not a djinn project (no .djinn directory)")
		return runs.Project{}, false
	}
	return proj, true
}

// --- summon ----------------------------------------------------------------

type summonPreviewBody struct {
	Beads     []string `json:"beads"`
	Epic      string   `json:"epic"`
	Preflight bool     `json:"preflight"`
}

// handleSummonPreview runs summoner in dry-run (or preflight) mode.
//
// Both modes are non-mutating by summoner's own contract: they resolve the
// scope, print, and exit without spawning workers or touching bd.
func (h *handlers) handleSummonPreview(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	proj, ok := h.project(w, project)
	if !ok {
		return
	}

	var body summonPreviewBody
	if !decodeBody(w, r, &body) {
		return
	}
	if !validBeadIDs(w, body.Beads) || !validEpic(w, body.Epic) {
		return
	}

	req := runs.SummonRequest{Beads: body.Beads, Epic: body.Epic}
	preview, err := h.p.mgr.Preview(r.Context(), proj.Workdir, req, body.Preflight)
	if err != nil {
		if errors.Is(err, runs.ErrSummonerMissing) {
			writeError(w, http.StatusServiceUnavailable, "summoner not found in PATH")
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
			Error: "summoner preview failed", Stderr: err.Error(),
		})
		return
	}

	// The label check is instant and authoritative; preflight is the deeper,
	// slower LLM pass. Merge both so a needs-operator bead is never missed just
	// because preflight was skipped.
	h.mergeNeedsOperatorLabel(r.Context(), project, preview)
	writeJSON(w, http.StatusOK, preview)
}

// needsOperatorLabel is the label that marks a bead as unable to finish
// unattended (ARCHITECTURE.md §Bead field conventions).
const needsOperatorLabel = "needs-operator"

// mergeNeedsOperatorLabel folds the SQL label check into a preview.
func (h *handlers) mergeNeedsOperatorLabel(ctx context.Context, project string, preview *runs.SummonPreview) {
	if len(preview.Ordered) == 0 {
		return
	}

	seen := make(map[string]bool, len(preview.NeedsOperator))
	for _, b := range preview.NeedsOperator {
		seen[b] = true
	}

	err := h.reg.Do(ctx, project, func(ctx context.Context, db *sql.DB) error {
		args := make([]any, 0, len(preview.Ordered)+1)
		placeholders := ""
		for i, id := range preview.Ordered {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
			args = append(args, id)
		}
		args = append(args, needsOperatorLabel)

		rows, err := db.QueryContext(ctx,
			"SELECT issue_id FROM labels WHERE issue_id IN ("+placeholders+") AND label = ?", args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if !seen[id] {
				seen[id] = true
				preview.NeedsOperator = append(preview.NeedsOperator, id)
			}
		}
		return rows.Err()
	})
	if err != nil {
		// The dolt server being down must not fail a preview; the preflight
		// result (if any) still stands.
		h.log.Warn("needs-operator label check failed", "project", project, "err", err)
	}
}

type summonBody struct {
	Beads      []string `json:"beads"`
	Epic       string   `json:"epic"`
	MaxWorkers int      `json:"maxWorkers"`
	MaxBeads   int      `json:"maxBeads"`
	// NoPush is a pointer so an explicit `false` is distinguishable from an
	// absent field. With a plain bool, a project configured `no_push: true`
	// could never launch a pushing run from the UI — the merge below could not
	// tell "the user unticked it" from "the user said nothing".
	NoPush *bool `json:"noPush"`
}

// handleSummon spawns a managed summoner run.
//
// This endpoint is only ever reached from an explicit confirmation in the UI —
// nothing in Loom calls it automatically.
func (h *handlers) handleSummon(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	proj, ok := h.project(w, project)
	if !ok {
		return
	}

	var body summonBody
	if !decodeBody(w, r, &body) {
		return
	}
	if !validBeadIDs(w, body.Beads) || !validEpic(w, body.Epic) {
		return
	}
	if body.MaxWorkers < 0 || body.MaxBeads < 0 {
		writeError(w, http.StatusBadRequest, "maxWorkers and maxBeads must be >= 0")
		return
	}

	req := summonRequest(body, h.p.defaults(project))

	run, err := h.p.mgr.Summon(r.Context(), project, proj.Workdir, req)
	if err != nil {
		switch {
		case errors.Is(err, runs.ErrSummonerMissing):
			writeError(w, http.StatusServiceUnavailable, "summoner not found in PATH")
		case errors.Is(err, runs.ErrRunActive):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
				Error: "could not start summoner", Stderr: err.Error(),
			})
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": run.ID, "run": run})
}

func (h *handlers) handleRunStop(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	runID := chi.URLParam(r, "runID")
	if _, ok := h.project(w, project); !ok {
		return
	}
	if !runs.ValidID(runID) {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	if err := h.p.mgr.Stop(project, runID); err != nil {
		if errors.Is(err, runs.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "no active managed run "+runID+" for project "+project)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "state": runs.StateStopping})
}

// --- runs ------------------------------------------------------------------

func (h *handlers) handleRunList(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	proj, ok := h.project(w, project)
	if !ok {
		return
	}

	list, err := h.p.history.List(proj)
	if err != nil {
		h.writeRunError(w, project, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": list})
}

func (h *handlers) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	runID := chi.URLParam(r, "runID")
	proj, ok := h.project(w, project)
	if !ok {
		return
	}

	detail, err := h.p.history.Get(proj, runID)
	if err != nil {
		h.writeRunError(w, project, err)
		return
	}
	// LogPath is a server-side path; the client reads it through /log.
	detail.LogPath = ""
	writeJSON(w, http.StatusOK, detail)
}

// handleRunLog returns a chunk of a run's console capture for scrollback.
func (h *handlers) handleRunLog(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	runID := chi.URLParam(r, "runID")
	proj, ok := h.project(w, project)
	if !ok {
		return
	}
	if !runs.ValidID(runID) {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	var start int64
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid offset="+strconv.Quote(raw)+": expected an integer")
			return
		}
		start = int64(n)
	}

	ansi := r.URL.Query().Get("ansi") == "1"

	path, err := h.p.mgr.Store().ConsolePath(proj.Name, runID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	text, next, eof, err := runs.ReadLogChunk(path, start, 0, ansi)
	if err != nil {
		// An external run has no Loom-side capture; say so rather than 500.
		writeError(w, http.StatusNotFound,
			"no console capture for run "+runID+" (external runs are not captured by Loom)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"text":   text,
		"offset": next,
		"eof":    eof,
	})
}

// handleForensics returns a bead's state-directory timeline and verdicts.
func (h *handlers) handleForensics(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	bead := chi.URLParam(r, "id")
	proj, ok := h.project(w, project)
	if !ok {
		return
	}
	if !runs.ValidID(bead) {
		writeError(w, http.StatusBadRequest, "invalid bead id")
		return
	}

	f, err := runs.ReadForensics(proj.StateDir(), bead)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *handlers) writeRunError(w http.ResponseWriter, project string, err error) {
	switch {
	case errors.Is(err, runs.ErrNoDjinn):
		writeError(w, http.StatusNotFound, "project "+project+" is not a djinn project")
	case errors.Is(err, runs.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "run not found")
	default:
		h.log.Error("run query failed", "project", project, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// --- helpers ---------------------------------------------------------------

// validBeadIDs rejects anything that could escape a path or confuse summoner's
// flag parsing.
func validBeadIDs(w http.ResponseWriter, ids []string) bool {
	for _, id := range ids {
		if !runs.ValidID(id) {
			writeError(w, http.StatusBadRequest, "invalid bead id "+strconv.Quote(id))
			return false
		}
	}
	return true
}

func validEpic(w http.ResponseWriter, epic string) bool {
	if epic == "" {
		return true
	}
	if !runs.ValidID(epic) {
		writeError(w, http.StatusBadRequest, "invalid epic id "+strconv.Quote(epic))
		return false
	}
	return true
}

// defaults returns the project's configured dispatch knobs, or nil.
func (p *Provider) defaults(project string) *config.Summoner {
	return p.settings[project]
}

// summonRequest builds the summoner launch request from a summon body and the
// project's configured defaults. Each knob falls back to the default only when
// the request left it unset.
func summonRequest(body summonBody, defaults *config.Summoner) runs.SummonRequest {
	req := runs.SummonRequest{
		Beads:      body.Beads,
		Epic:       body.Epic,
		MaxWorkers: body.MaxWorkers,
		MaxBeads:   body.MaxBeads,
	}
	if defaults != nil {
		if req.MaxWorkers == 0 {
			req.MaxWorkers = defaults.MaxWorkers
		}
		if req.MaxBeads == 0 {
			req.MaxBeads = defaults.MaxBeads
		}
	}
	switch {
	case body.NoPush != nil:
		req.NoPush = *body.NoPush
	case defaults != nil:
		req.NoPush = defaults.NoPush
	}
	return req
}
