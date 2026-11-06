package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/integration"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/registry"
)

// maxBodyBytes caps a request body. Issue prose is markdown written by humans
// and agents; a megabyte is far more than any real bead and still bounds the
// damage from a runaway client.
const maxBodyBytes = integration.MaxBodyBytes

// maxProseBytes caps a single prose field on PATCH. Prose travels to bd as one
// exec argv element, and Linux rejects any single argument over MAX_ARG_STRLEN
// (128 KiB) with an opaque E2BIG; rejecting here turns that into a clear 400.
const maxProseBytes = 100_000

// knownStatuses are bd's built-in statuses (ARCHITECTURE.md §bd). A project may
// add more in its custom_statuses table, which validateStatus checks only when
// the value is not one of these.
var knownStatuses = map[string]bool{
	queries.StatusOpen:       true,
	queries.StatusInProgress: true,
	queries.StatusBlocked:    true,
	queries.StatusDeferred:   true,
	queries.StatusClosed:     true,
	queries.StatusPinned:     true,
	queries.StatusHooked:     true,
}

// patchBody is the PATCH and bulk-patch payload. Every field is optional; a
// pointer distinguishes "absent" from "set to the zero value", which matters
// for priority 0 and for clearing assignee or a date with "".
type patchBody struct {
	Status       *string  `json:"status"`
	Priority     *int     `json:"priority"`
	Assignee     *string  `json:"assignee"`
	Title        *string  `json:"title"`
	Type         *string  `json:"type"`
	Estimate     *int     `json:"estimate"`
	Description  *string  `json:"description"`
	Acceptance   *string  `json:"acceptance"`
	Design       *string  `json:"design"`
	Notes        *string  `json:"notes"`
	LabelsAdd    []string `json:"labelsAdd"`
	LabelsRemove []string `json:"labelsRemove"`
	Defer        *string  `json:"defer"`
	Due          *string  `json:"due"`
}

// fields converts the body to bdexec fields after validating what Loom can
// validate locally. Everything else is bd's job to reject.
func (b patchBody) fields(ctx context.Context, s *Server, project string) (bdexec.UpdateFields, error) {
	if b.Priority != nil && (*b.Priority < 0 || *b.Priority > 4) {
		return bdexec.UpdateFields{}, fmt.Errorf("priority must be 0-4, got %d", *b.Priority)
	}
	if b.Status != nil {
		if err := s.validateStatus(ctx, project, *b.Status); err != nil {
			return bdexec.UpdateFields{}, err
		}
	}
	if b.Title != nil && strings.TrimSpace(*b.Title) == "" {
		return bdexec.UpdateFields{}, errors.New("title cannot be blank")
	}
	if b.Estimate != nil && *b.Estimate < 0 {
		return bdexec.UpdateFields{}, fmt.Errorf("estimate must be >= 0, got %d", *b.Estimate)
	}
	for name, v := range map[string]*string{
		"description": b.Description,
		"acceptance":  b.Acceptance,
		"design":      b.Design,
		"notes":       b.Notes,
	} {
		if v != nil {
			if err := checkProseSize(name, *v); err != nil {
				return bdexec.UpdateFields{}, err
			}
		}
	}

	return bdexec.UpdateFields{
		Status:       b.Status,
		Priority:     b.Priority,
		Assignee:     b.Assignee,
		Title:        b.Title,
		Type:         b.Type,
		Estimate:     b.Estimate,
		Description:  b.Description,
		Acceptance:   b.Acceptance,
		Design:       b.Design,
		Notes:        b.Notes,
		AddLabels:    b.LabelsAdd,
		RemoveLabels: b.LabelsRemove,
		Defer:        b.Defer,
		Due:          b.Due,
	}, nil
}

// checkProseSize rejects a prose value that would exceed the per-argv-element
// budget; see maxProseBytes.
func checkProseSize(name, v string) error {
	if len(v) > maxProseBytes {
		return fmt.Errorf("%s must be at most %d bytes, got %d", name, maxProseBytes, len(v))
	}
	return nil
}

// validateStatus accepts bd's built-ins, then falls back to the project's
// custom_statuses table before rejecting.
func (s *Server) validateStatus(ctx context.Context, project, status string) error {
	if knownStatuses[status] {
		return nil
	}
	var custom bool
	err := s.reg.Do(ctx, project, func(ctx context.Context, db *sql.DB) error {
		var n int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM custom_statuses WHERE name = ?", status).Scan(&n); err != nil {
			return err
		}
		custom = n > 0
		return nil
	})
	if err != nil {
		// The custom_statuses table could not be consulted. bd validates the
		// status itself and will reject a bad one with a clear message, so
		// letting it through beats blocking a legitimate write on an outage —
		// and beats claiming the status is unknown when we simply could not
		// check (review finding N2).
		s.log.Warn("could not check custom statuses; deferring to bd",
			"project", project, "status", status, "err", err)
		return nil
	}
	if !custom {
		return fmt.Errorf("unknown status %q (known: %s, plus this project's custom statuses)",
			status, strings.Join(sortedStatuses(), ", "))
	}
	return nil
}

func sortedStatuses() []string {
	// Fixed order, most useful first, rather than map iteration order.
	return []string{
		queries.StatusOpen, queries.StatusInProgress, queries.StatusBlocked,
		queries.StatusDeferred, queries.StatusClosed, queries.StatusPinned,
		queries.StatusHooked,
	}
}

// --- routes ---------------------------------------------------------------

// writeRoutes registers the mutating endpoints. They are only mounted when a bd
// client is configured, so a read-only deployment simply has no write surface.
func (s *Server) writeRoutes(r chi.Router) {
	r.Post("/issues", s.handleCreateIssue)
	r.Post("/issues/bulk", s.handleBulkPatch)
	r.Post("/issues/close", s.handleBulkClose)
	r.Patch("/issues/{id}", s.handlePatchIssue)
	r.Post("/issues/{id}/close", s.handleCloseIssue)
	r.Post("/issues/{id}/reopen", s.handleReopenIssue)
	r.Post("/issues/{id}/comments", s.handleCommentIssue)
	r.Post("/deps", s.handleDepAdd)
	r.Delete("/deps", s.handleDepRemove)
	r.Post("/dolt/push", s.handleDoltPush)
	r.Post("/dolt/pull", s.handleDoltPull)
}

// --- handlers -------------------------------------------------------------

func (s *Server) handlePatchIssue(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "id")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	if !requireIssueID(w, id) {
		return
	}

	var body patchBody
	if !decodeBody(w, r, &body) {
		return
	}
	if !requireLabels(w, body.LabelsAdd) || !requireLabels(w, body.LabelsRemove) {
		return
	}
	fields, err := body.fields(r.Context(), s, project)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if fields.IsEmpty() {
		writeError(w, http.StatusBadRequest, "patch contains no fields to update")
		return
	}

	if err := s.bd.Update(r.Context(), workdir, []string{id}, fields); err != nil {
		s.writeWriteError(w, "bd update failed", err)
		return
	}
	s.reg.MarkWritten(project)
	s.respondWithIssue(w, r, project, id, http.StatusOK)
}

type bulkBody struct {
	IDs   []string  `json:"ids"`
	Patch patchBody `json:"patch"`
}

// bulkResponse reports which issues were updated. It deliberately does not
// echo full issue bodies: a bulk edit can touch hundreds of beads, and the UI
// refetches the list it is showing anyway.
type bulkResponse struct {
	Updated int      `json:"updated"`
	IDs     []string `json:"ids"`
}

func (s *Server) handleBulkPatch(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	var body bulkBody
	if !decodeBody(w, r, &body) {
		return
	}

	ids := dedupeNonEmpty(body.IDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "ids must contain at least one issue id")
		return
	}
	if !requireIssueIDs(w, ids) {
		return
	}
	if !requireLabels(w, body.Patch.LabelsAdd) || !requireLabels(w, body.Patch.LabelsRemove) {
		return
	}
	fields, err := body.Patch.fields(r.Context(), s, project)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if fields.IsEmpty() {
		writeError(w, http.StatusBadRequest, "patch contains no fields to update")
		return
	}

	// One invocation for every id, so bd applies the change as a single unit.
	if err := s.bd.Update(r.Context(), workdir, ids, fields); err != nil {
		s.writeWriteError(w, "bd update failed", err)
		return
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, bulkResponse{Updated: len(ids), IDs: ids})
}

type closeBody struct {
	Reason string `json:"reason"`
	// Force closes an issue another actor has claimed. The UI sets it only
	// after the user has seen the 409's assignee and confirmed.
	Force bool `json:"force"`
}

// closeConflictResponse is the 409 for a close bd refused because another
// actor holds the issue. Assignee is the field the UI's confirm step names.
type closeConflictResponse struct {
	Error    string `json:"error"`
	Stderr   string `json:"stderr,omitempty"`
	Assignee string `json:"assignee"`
}

// bulkCloseBody is the payload of POST /issues/close.
type bulkCloseBody struct {
	IDs    []string `json:"ids"`
	Reason string   `json:"reason"`
}

// handleBulkClose closes many issues in one `bd close id... --reason` call.
//
// bd's close verb has always accepted multiple ids and bdexec has always taken
// a slice; only the HTTP surface was missing, so the UI was issuing one POST
// per issue and triggering one invalidate/refetch cycle per issue (review
// finding N3).
func (s *Server) handleBulkClose(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	var body bulkCloseBody
	if !decodeBody(w, r, &body) {
		return
	}
	ids := dedupeNonEmpty(body.IDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "ids must contain at least one issue id")
		return
	}
	if !requireIssueIDs(w, ids) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		writeError(w, http.StatusBadRequest, "reason is required to close an issue")
		return
	}

	if err := s.bd.Close(r.Context(), workdir, ids, bdexec.CloseOptions{Reason: body.Reason}); err != nil {
		s.writeWriteError(w, "bd close failed", err)
		return
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, bulkResponse{Updated: len(ids), IDs: ids})
}

func (s *Server) handleCloseIssue(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "id")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	if !requireIssueID(w, id) {
		return
	}

	var body closeBody
	if !decodeBody(w, r, &body) {
		return
	}
	// A close with no reason loses the one piece of context that makes a
	// closed backlog readable later.
	if strings.TrimSpace(body.Reason) == "" {
		writeError(w, http.StatusBadRequest, "reason is required to close an issue")
		return
	}

	err := s.bd.Close(r.Context(), workdir, []string{id}, bdexec.CloseOptions{Reason: body.Reason, Force: body.Force})
	if named, claimed := bdexec.ClaimedByAnother(err); claimed {
		s.writeClaimedConflict(w, r, project, id, named, err)
		return
	}
	if err != nil {
		s.writeWriteError(w, "bd close failed", err)
		return
	}
	s.reg.MarkWritten(project)
	s.respondWithIssue(w, r, project, id, http.StatusOK)
}

// writeClaimedConflict answers a close bd refused because another actor holds
// the issue. The assignee comes from the issue row so the 409 does not depend
// on bd's prose; the name bd printed is the fallback when the row cannot be
// read.
func (s *Server) writeClaimedConflict(w http.ResponseWriter, r *http.Request, project, id, named string, err error) {
	assignee := named
	readErr := s.reg.Do(r.Context(), project, func(ctx context.Context, db *sql.DB) error {
		detail, err := queries.GetIssue(ctx, db, id)
		if err == nil && detail.Assignee != "" {
			assignee = detail.Assignee
		}
		return err
	})
	if readErr != nil {
		s.log.Warn("assignee read for a claimed close failed; using bd's text", "project", project, "issue", id, "err", readErr)
	}
	holder := "another actor"
	if assignee != "" {
		holder = assignee
	}
	be, _ := bdexec.AsBdError(err)
	writeJSON(w, http.StatusConflict, closeConflictResponse{
		Error:    id + " is claimed by " + holder + "; close it anyway to override the claim",
		Stderr:   be.Message(),
		Assignee: assignee,
	})
}

type reopenBody struct {
	Reason string `json:"reason"`
}

func (s *Server) handleReopenIssue(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "id")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	if !requireIssueID(w, id) {
		return
	}

	var body reopenBody
	if !decodeBody(w, r, &body) {
		return
	}

	if err := s.bd.Reopen(r.Context(), workdir, []string{id}, body.Reason); err != nil {
		s.writeWriteError(w, "bd reopen failed", err)
		return
	}
	s.reg.MarkWritten(project)
	s.respondWithIssue(w, r, project, id, http.StatusOK)
}

type commentBody struct {
	Text string `json:"text"`
}

func (s *Server) handleCommentIssue(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "id")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	if !requireIssueID(w, id) {
		return
	}

	var body commentBody
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	if err := s.bd.Comment(r.Context(), workdir, id, body.Text); err != nil {
		s.writeWriteError(w, "bd comment failed", err)
		return
	}
	s.reg.MarkWritten(project)
	s.respondWithIssue(w, r, project, id, http.StatusCreated)
}

// depSpec is one dependency edge in a create request.
type depSpec struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type createBody struct {
	Title       string    `json:"title"`
	Type        string    `json:"type"`
	Priority    *int      `json:"priority"`
	Description string    `json:"description"`
	Acceptance  string    `json:"acceptance"`
	Design      string    `json:"design"`
	Notes       string    `json:"notes"`
	Parent      string    `json:"parent"`
	Labels      []string  `json:"labels"`
	Estimate    *int      `json:"estimate"`
	Deps        []depSpec `json:"deps"`
}

func (s *Server) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	var body createBody
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if body.Priority != nil && (*body.Priority < 0 || *body.Priority > 4) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("priority must be 0-4, got %d", *body.Priority))
		return
	}
	if body.Estimate != nil && *body.Estimate < 0 {
		writeError(w, http.StatusBadRequest, "estimate must be >= 0")
		return
	}
	for name, v := range map[string]string{
		"description": body.Description,
		"acceptance":  body.Acceptance,
		"design":      body.Design,
		"notes":       body.Notes,
	} {
		if err := checkProseSize(name, v); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.Parent != "" && !requireIssueID(w, body.Parent) {
		return
	}
	if !requireLabels(w, body.Labels) {
		return
	}
	for _, d := range body.Deps {
		if !requireIssueID(w, d.ID) {
			return
		}
	}

	spec := bdexec.CreateSpec{
		Title:       body.Title,
		Type:        body.Type,
		Priority:    body.Priority,
		Description: body.Description,
		Acceptance:  body.Acceptance,
		Design:      body.Design,
		Notes:       body.Notes,
		Parent:      body.Parent,
		Labels:      body.Labels,
		Estimate:    body.Estimate,
	}
	for _, d := range body.Deps {
		spec.Deps = append(spec.Deps, bdexec.Dep{ID: d.ID, Type: d.Type})
	}

	id, err := s.bd.Create(r.Context(), workdir, spec)
	if err != nil {
		if id != "" {
			// The bead exists but is half-configured. Naming it beats leaving
			// the user with an orphan they cannot find.
			s.reg.MarkWritten(project)
			s.writeWriteError(w, fmt.Sprintf("issue %s was created but a follow-up step failed", id), err)
			return
		}
		s.writeWriteError(w, "bd create failed", err)
		return
	}
	s.reg.MarkWritten(project)
	s.respondWithIssue(w, r, project, id, http.StatusCreated)
}

type depBody struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

func (s *Server) handleDepAdd(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	var body depBody
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.From) == "" || strings.TrimSpace(body.To) == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if !requireIssueID(w, body.From) || !requireIssueID(w, body.To) {
		return
	}

	if err := s.bd.DepAdd(r.Context(), workdir, body.From, body.To, body.Type); err != nil {
		if !isBdFailure(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeWriteError(w, "bd dep add failed", err)
		return
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDepRemove(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}

	var body depBody
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.From) == "" || strings.TrimSpace(body.To) == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if !requireIssueID(w, body.From) || !requireIssueID(w, body.To) {
		return
	}

	if err := s.bd.DepRemove(r.Context(), workdir, body.From, body.To); err != nil {
		s.writeWriteError(w, "bd dep remove failed", err)
		return
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- helpers --------------------------------------------------------------

// workdirFor resolves the workdir a bd write runs in, writing the error
// response itself when it cannot. Writes do not require a reachable dolt
// server — bd starts its own — but an unknown project is still a 404, and a
// project whose bd writes to a different database than Loom reads is a 409
// carrying the registry's WriteReason: the write would land where the UI never
// shows it.
func (s *Server) workdirFor(w http.ResponseWriter, project string) (string, bool) {
	p, ok := s.projectFor(w, project)
	if !ok {
		return "", false
	}
	if !p.Writable {
		writeError(w, http.StatusConflict, p.WriteReason)
		return "", false
	}
	return p.Workdir, true
}

// readWorkdirFor resolves the workdir for a bd call that only reads, which a
// read-only project still serves.
func (s *Server) readWorkdirFor(w http.ResponseWriter, project string) (string, bool) {
	p, ok := s.projectFor(w, project)
	if !ok {
		return "", false
	}
	return p.Workdir, true
}

func (s *Server) projectFor(w http.ResponseWriter, project string) (registry.Project, bool) {
	p, err := s.reg.Get(project)
	if err != nil {
		if errors.Is(err, registry.ErrUnknownProject) {
			writeError(w, http.StatusNotFound, "no such project: "+project)
			return registry.Project{}, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return registry.Project{}, false
	}
	return p, true
}

// respondWithIssue re-reads the issue after a successful write so the client
// gets bd's authoritative result — recomputed readiness, new event rows and
// bd-normalized values — rather than an echo of the request.
func (s *Server) respondWithIssue(w http.ResponseWriter, r *http.Request, project, id string, status int) {
	var detail *queries.IssueDetail
	err := s.reg.Do(r.Context(), project, func(ctx context.Context, db *sql.DB) error {
		var err error
		detail, err = queries.GetIssue(ctx, db, id)
		return err
	})
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no issue "+id+" in project "+project)
			return
		}
		// The write itself succeeded; only the read-back failed. Say so
		// plainly rather than implying the change was lost.
		s.log.Error("read-back after write failed", "project", project, "issue", id, "err", err)
		writeError(w, http.StatusServiceUnavailable,
			"the write succeeded but reading the issue back failed: "+err.Error())
		return
	}
	writeJSON(w, status, detail)
}

// writeWriteError maps a failed bd invocation to 422 with bd's own stderr, so
// the UI can show the user exactly what bd objected to.
func (s *Server) writeWriteError(w http.ResponseWriter, msg string, err error) {
	if be, ok := bdexec.AsBdError(err); ok {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: msg, Stderr: be.Message()})
		return
	}
	if errors.Is(err, bdexec.ErrNoFields) || errors.Is(err, bdexec.ErrInvalidID) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Error("write failed", "msg", msg, "err", err)
	writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: msg, Stderr: err.Error()})
}

// isBdFailure reports whether the error came from bd itself, as opposed to a
// client-side argument check inside bdexec.
func isBdFailure(err error) bool {
	_, ok := bdexec.AsBdError(err)
	return ok
}

// decodeBody reads a JSON request body, writing a 400 and returning false when
// it cannot. An empty body decodes to the zero value, which lets endpoints like
// reopen be called with no payload at all.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return integration.DecodeBody(w, r, dst)
}

// requireIssueID rejects an id that is not a well-formed bd issue ID, writing a
// 400. This runs before bd is invoked: bd places positional ids ahead of flags
// with no `--` terminator, so a flag-shaped id like `--claim` is swallowed by
// bd's parser and the command silently retargets "the last touched issue".
func requireIssueID(w http.ResponseWriter, id string) bool {
	if bdexec.ValidIssueID(id) {
		return true
	}
	writeError(w, http.StatusBadRequest, "invalid issue id "+strconv.Quote(id))
	return false
}

// requireIssueIDs validates every id in a list.
func requireIssueIDs(w http.ResponseWriter, ids []string) bool {
	for _, id := range ids {
		if !bdexec.ValidIssueID(id) {
			writeError(w, http.StatusBadRequest, "invalid issue id "+strconv.Quote(id))
			return false
		}
	}
	return true
}

// requireLabels validates every label in a list.
func requireLabels(w http.ResponseWriter, labels []string) bool {
	for _, l := range labels {
		if !bdexec.ValidLabel(l) {
			writeError(w, http.StatusBadRequest, "invalid label "+strconv.Quote(l))
			return false
		}
	}
	return true
}

// dedupeNonEmpty trims, drops blanks and removes duplicates while preserving
// order, so a bulk request cannot name the same issue twice.
func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
