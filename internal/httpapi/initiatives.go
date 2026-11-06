package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/queries"
)

type initiativesResponse struct {
	Initiatives []queries.Initiative `json:"initiatives"`
}

// handleInitiatives returns one row per epic with its full-subtree rollup.
func (s *Server) handleInitiatives(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	q := r.URL.Query()
	for key := range q {
		if key != "includeClosed" {
			writeError(w, http.StatusBadRequest, "unknown query parameter "+key+" (known: includeClosed)")
			return
		}
	}

	includeClosed, err := boolParam(q, "includeClosed")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	initiatives, err := cachedRead(r.Context(), s, project, "initiatives", cacheParams(q),
		func(ctx context.Context, db *sql.DB) ([]queries.Initiative, error) {
			return queries.InitiativesFor(ctx, db, includeClosed)
		})
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}

	writeJSON(w, http.StatusOK, initiativesResponse{Initiatives: initiatives})
}
