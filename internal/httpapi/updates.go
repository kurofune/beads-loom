package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/updates"
)

// updateRoutes mounts the weekly-update surface. Like the sweep routes it needs
// a bd client: every read here goes through `bd kv`, never the config table the
// values physically live in, and the one write goes through `bd kv set`.
func (s *Server) updateRoutes(r chi.Router) {
	r.Get("/initiative-updates", s.handleInitiativeUpdateSummaries)
	r.Get("/initiatives/{epic}/updates", s.handleInitiativeUpdates)
	r.Post("/initiatives/{epic}/updates/{week}/publish", s.handlePublishUpdate)
}

type updateSummariesResponse struct {
	Updates []updates.Summary `json:"updates"`
}

// handleInitiativeUpdateSummaries answers the whole list view in one KV read, so
// the Updates column costs one request rather than one per epic row.
func (s *Server) handleInitiativeUpdateSummaries(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	workdir, ok := s.readWorkdirFor(w, project)
	if !ok {
		return
	}
	if !requireNoQueryParams(w, r) {
		return
	}
	values, err := s.bd.KVList(r.Context(), workdir)
	if err != nil {
		s.writeKVReadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updateSummariesResponse{Updates: updates.Summaries(values)})
}

type initiativeUpdatesResponse struct {
	Updates []updates.Update `json:"updates"`
	// KnownIssueIDs is the subset of the cited, id-shaped tokens that name a
	// real issue. Only these become links; the rest stay plain text.
	KnownIssueIDs []string `json:"knownIssueIds"`
	// KnownRunIDs is the same answer for run citations: the subset the project's
	// run history knows, which is what the run detail route can open.
	KnownRunIDs []string `json:"knownRunIds"`
}

func (s *Server) handleInitiativeUpdates(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	workdir, ok := s.readWorkdirFor(w, project)
	if !ok {
		return
	}
	epic := chi.URLParam(r, "epic")
	if !requireEpicToken(w, epic) {
		return
	}
	if !requireNoQueryParams(w, r) {
		return
	}

	values, err := s.bd.KVList(r.Context(), workdir)
	if err != nil {
		s.writeKVReadError(w, err)
		return
	}
	history := updates.FromKV(values, epic)

	known := []string{}
	if cited := updates.CitedIssueIDs(history); len(cited) > 0 {
		err := s.queryDB(r.Context(), project, func(ctx context.Context, db *sql.DB) error {
			var err error
			known, err = queries.ExistingIssueIDs(ctx, db, cited)
			return err
		})
		if err != nil {
			// The history is the answer; linkification is a nicety. An
			// unreachable dolt server degrades citations to plain text rather
			// than hiding a year of updates behind a 503.
			s.log.Warn("could not resolve cited issue ids",
				"project", project, "epic", epic, "err", err)
			known = []string{}
		}
	}
	writeJSON(w, http.StatusOK, initiativeUpdatesResponse{
		Updates:       history,
		KnownIssueIDs: known,
		KnownRunIDs:   s.knownRunIDs(project, workdir, updates.CitedRunIDs(history)),
	})
}

// knownRunIDs is the run half of what ExistingIssueIDs does for beads: a cited
// run id links only when the project's run history has that run.
//
// It reads through Deps.RunLookup, which the integration keeping run history
// supplies from the same history its run routes answer from, so the link the
// sheet writes and the page it opens agree by construction. Update history is
// served for every project, but run history exists only where such an
// integration is mounted and applies, so no lookup and an unreadable history
// both degrade to plain text.
func (s *Server) knownRunIDs(project, workdir string, cited []string) []string {
	known := []string{}
	if len(cited) == 0 || s.deps.RunLookup == nil {
		return known
	}
	ids, err := s.deps.RunLookup(project, workdir)
	if err != nil {
		s.log.Warn("could not resolve cited run ids", "project", project, "err", err)
		return known
	}
	have := make(map[string]bool, len(ids))
	for _, id := range ids {
		have[id] = true
	}
	// cited arrives sorted, so the answer is sorted too.
	for _, id := range cited {
		if have[id] {
			known = append(known, id)
		}
	}
	return known
}

// publishUpdateBody carries the sha of the body the operator actually read.
type publishUpdateBody struct {
	BodySHA string `json:"bodySha"`
}

// handlePublishUpdate flips one stored draft to published.
//
// It is the only write in this package and it changes exactly one field. The
// value is round-tripped as a map rather than a struct on purpose: the producer
// owns this JSON, and decoding into a struct would silently drop any field this
// build does not know about.
func (s *Server) handlePublishUpdate(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}
	epic := chi.URLParam(r, "epic")
	if !requireEpicToken(w, epic) {
		return
	}
	week := chi.URLParam(r, "week")
	if !updates.ValidWeek(week) {
		writeError(w, http.StatusBadRequest, "invalid ISO week "+strconv.Quote(week)+" (expected YYYY-Www)")
		return
	}
	var body publishUpdateBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.BodySHA == "" {
		writeError(w, http.StatusBadRequest, "bodySha is required: publish confirms the exact draft that was reviewed")
		return
	}

	key := updates.Key(epic, week)
	raw, found, err := s.bd.KVGet(r.Context(), workdir, key)
	if err != nil {
		s.writeKVReadError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no stored update for "+epic+" "+week)
		return
	}

	stored, err := decodeStoredUpdate(raw)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity,
			"the stored update at "+key+" is not valid JSON and cannot be published: "+err.Error())
		return
	}
	if status, _ := stored["status"].(string); status == "published" {
		writeError(w, http.StatusConflict,
			epic+" "+week+" is already published; a published update is never overwritten")
		return
	}
	storedBody, _ := stored["body"].(string)
	if updates.BodySHA(storedBody) != body.BodySHA {
		writeError(w, http.StatusConflict,
			"the draft changed since it was reviewed; reload and review again")
		return
	}
	if uncited := updates.ValidateCitations(epic, storedBody); len(uncited) > 0 {
		writeError(w, http.StatusUnprocessableEntity, uncitedMessage(uncited))
		return
	}

	stored["status"] = "published"
	value, err := json.Marshal(stored)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "could not re-encode the stored update: "+err.Error())
		return
	}
	if err := s.bd.KVSet(r.Context(), workdir, key, string(value)); err != nil {
		s.writeWriteError(w, "bd kv set failed", err)
		return
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, map[string]any{"published": true, "epic": epic, "week": week})
}

// decodeStoredUpdate parses the producer's value with numbers held as
// json.Number, so a re-encode cannot turn `"schema":1` into `1e+00` or round a
// large integer through float64.
func decodeStoredUpdate(raw string) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var stored map[string]any
	if err := dec.Decode(&stored); err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("the stored value is null, not an update object")
	}
	return stored, nil
}

// uncitedMessage names the first offending claim verbatim and says how many
// there are, so the operator can find the line in the draft without guessing.
func uncitedMessage(uncited []string) string {
	msg := "this update cannot be published: a claim carries no citation (bead id, commit sha, or run id): " +
		strconv.Quote(uncited[0])
	if len(uncited) > 1 {
		msg = fmt.Sprintf("%s — %d claims are uncited", msg, len(uncited))
	}
	return msg
}

// requireEpicToken rejects an epic id that would not be safe in bd's argv, and
// which therefore could not have addressed a key either.
func requireEpicToken(w http.ResponseWriter, epic string) bool {
	if bdexec.ValidToken(epic) {
		return true
	}
	writeError(w, http.StatusBadRequest, "invalid epic id "+strconv.Quote(epic))
	return false
}

// requireNoQueryParams keeps the repo convention that a typo'd parameter is a
// 400, not a silently unfiltered 200.
func requireNoQueryParams(w http.ResponseWriter, r *http.Request) bool {
	for key := range r.URL.Query() {
		writeError(w, http.StatusBadRequest, "unknown query parameter "+strconv.Quote(key)+" (this endpoint takes none)")
		return false
	}
	return true
}

// writeKVReadError reports a failed `bd kv` read with bd's own stderr attached,
// which is what lets the UI's error state say why rather than "request failed".
func (s *Server) writeKVReadError(w http.ResponseWriter, err error) {
	if be, ok := bdexec.AsBdError(err); ok {
		writeJSON(w, http.StatusInternalServerError,
			errorResponse{Error: "bd kv read failed", Stderr: be.Message()})
		return
	}
	s.log.Error("kv read failed", "err", err)
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "bd kv read failed", Stderr: err.Error()})
}
