package djinn

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/runs"
)

// evidenceResponse is GET /x/djinn/beads/{id}/evidence.
type evidenceResponse struct {
	// Evidence is the parsed .djinn/evidence/<bead>.yml, or null when the bead
	// has none, which is the common case.
	Evidence *runs.Evidence `json:"evidence"`
}

func (h *handlers) handleEvidence(w http.ResponseWriter, r *http.Request) {
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

	ev, err := runs.ReadEvidence(proj.DjinnDir(), bead)
	if err != nil {
		h.log.Warn("could not read evidence", "project", project, "bead", bead, "err", err)
		writeError(w, http.StatusUnprocessableEntity, "evidence for "+bead+" is unreadable: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, evidenceResponse{Evidence: ev})
}

// spendResponse is GET /x/djinn/spend.
type spendResponse struct {
	// Last7dUSD is run cost over the last seven days, or null when no run
	// touched the window: "no runs" and "runs that cost nothing" are
	// different answers.
	Last7dUSD *float64 `json:"last7dUsd"`
}

func (h *handlers) handleSpend(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	proj, ok := h.project(w, project)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, spendResponse{Last7dUSD: h.p.spendRecent(proj, time.Now())})
}

// spendRecent sums the cost of runs that were active in the last seven days.
//
// A run counts when it started OR ended inside the window. Bounding on start
// time alone dropped a long drain that began eight days ago and finished
// yesterday — money spent this week, reported as zero.
//
// Returns nil when no run touched the window at all.
func (p *Provider) spendRecent(proj runs.Project, now time.Time) *float64 {
	list, err := p.history.List(proj)
	if err != nil || len(list) == 0 {
		return nil
	}

	cutoff := now.UTC().AddDate(0, 0, -7)
	var total float64
	var counted int
	for _, run := range list {
		startedInWindow := !run.StartedAt.Before(cutoff)
		// A run with no end is still going, so it is active now by definition.
		endedInWindow := run.EndedAt == nil || !run.EndedAt.Before(cutoff)
		if !startedInWindow && !endedInWindow {
			continue
		}
		counted++
		switch {
		case run.Summary != nil && run.Summary.TotalCostUSD > 0:
			total += run.Summary.TotalCostUSD
		default:
			total += run.CostSoFar
		}
	}
	if counted == 0 {
		return nil
	}
	return &total
}
