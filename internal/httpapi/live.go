package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/queries"
)

// nowTimeout bounds the whole cross-project Now query. One slow project must
// not hold the response open indefinitely.
const nowTimeout = 10 * time.Second

// handleDoltStatus reports a project's dolt sync state.
func (s *Server) handleDoltStatus(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	// Sync state reads commits and remote refs, which the data hash does not
	// cover, so the refs are part of the key.
	var refs string
	err := s.queryDB(r.Context(), project, func(ctx context.Context, db *sql.DB) error {
		var err error
		refs, err = queries.DoltRefs(ctx, db)
		return err
	})
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}
	status, err := cachedRead(r.Context(), s, project, "dolt", refs, queries.DoltStatusFor)
	if err != nil {
		s.writeRegistryError(w, project, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// doltSyncResponse is the body of a push or pull.
type doltSyncResponse struct {
	OK bool `json:"ok"`
	// Output is bd's combined stdout and stderr, shown to the user verbatim.
	Output string `json:"output"`
}

func (s *Server) handleDoltPush(w http.ResponseWriter, r *http.Request) {
	s.doltSync(w, r, "push")
}

func (s *Server) handleDoltPull(w http.ResponseWriter, r *http.Request) {
	s.doltSync(w, r, "pull")
}

// doltSync runs bd dolt push/pull.
//
// A conflict is reported as 409 with bd's own words and is never auto-resolved:
// merging someone else's issue history on their behalf is exactly the kind of
// decision a tool should not make silently.
func (s *Server) doltSync(w http.ResponseWriter, r *http.Request, verb string) {
	project := chi.URLParam(r, "project")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	var (
		output string
		err    error
	)
	switch verb {
	case "push":
		output, err = s.bd.DoltPush(r.Context(), workdir)
	default:
		output, err = s.bd.DoltPull(r.Context(), workdir)
	}

	if err != nil {
		if bdexec.IsConflict(err) {
			be, _ := bdexec.AsBdError(err)
			writeJSON(w, http.StatusConflict, errorResponse{
				Error:  "dolt " + verb + " hit a conflict; resolve it manually",
				Stderr: be.Message(),
			})
			return
		}
		s.writeWriteError(w, "bd dolt "+verb+" failed", err)
		return
	}

	// A sync changes what other projects' clients should see.
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, doltSyncResponse{OK: true, Output: output})
}

// nowResponse wraps the cross-project list plus any projects that could not be
// consulted, so the UI can say "3 of 4 projects" rather than silently underreport.
type nowResponse struct {
	Entries []queries.NowEntry `json:"entries"`
	// Skipped names the unreachable projects this answer excludes.
	Skipped []string `json:"skipped"`
}

// handleNow lists in-progress work across every reachable project.
//
// Projects are queried concurrently and an unreachable one is skipped rather
// than failing the request — the same degradation rule the rest of the API uses.
func (s *Server) handleNow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), nowTimeout)
	defer cancel()

	snap := s.reg.Snapshot()
	now := time.Now().UTC()

	var (
		mu      sync.Mutex
		entries []queries.NowEntry
		skipped []string
		wg      sync.WaitGroup
	)

	for _, p := range snap {
		if !p.Reachable {
			skipped = append(skipped, p.Name)
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			var got []queries.NowEntry
			err := s.reg.Do(ctx, name, func(ctx context.Context, db *sql.DB) error {
				var err error
				got, err = queries.NowFor(ctx, db, name, now)
				return err
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				s.log.Warn("now query failed", "project", name, "err", err)
				skipped = append(skipped, name)
				return
			}
			entries = append(entries, got...)
		}(p.Name)
	}
	wg.Wait()

	// Oldest work first: the thing that has been in progress longest is the
	// thing most likely to need attention.
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch {
		case a.InStatusSince == nil && b.InStatusSince == nil:
		case a.InStatusSince == nil:
			return false
		case b.InStatusSince == nil:
			return true
		case !a.InStatusSince.Equal(*b.InStatusSince):
			return a.InStatusSince.Before(*b.InStatusSince)
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Issue.ID < b.Issue.ID
	})
	sort.Strings(skipped)

	if entries == nil {
		entries = []queries.NowEntry{}
	}
	if skipped == nil {
		skipped = []string{}
	}
	writeJSON(w, http.StatusOK, nowResponse{Entries: entries, Skipped: skipped})
}
