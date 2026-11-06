package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/sweep"
)

var knownSweepParams = map[string]bool{
	"window": true, "branch": true, "includeDismissed": true,
}

func (s *Server) sweepRoutes(r chi.Router) {
	r.Get("/sweep", s.handleSweep)
	r.Post("/sweep/findings/{fingerprint}/apply", s.handleApplyFinding)
	r.Post("/sweep/findings/{fingerprint}/dismiss", s.handleDismissFinding)
	r.Delete("/sweep/findings/{fingerprint}/dismiss", s.handleUndismissFinding)
}

func (s *Server) handleSweep(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	workdir, ok := s.readWorkdirFor(w, project)
	if !ok {
		return
	}
	window, branch, includeDismissed, err := sweepParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	report, err := s.sweepRun(r.Context(), project, workdir, branch, window, includeDismissed)
	if err != nil {
		s.writeSweepError(w, project, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func sweepParams(r *http.Request) (window int, branch string, includeDismissed bool, err error) {
	q := r.URL.Query()
	for key := range q {
		if !knownSweepParams[key] {
			keys := make([]string, 0, len(knownSweepParams))
			for known := range knownSweepParams {
				keys = append(keys, known)
			}
			sort.Strings(keys)
			return 0, "", false, fmt.Errorf("unknown query parameter %q (known: %s)", key, strings.Join(keys, ", "))
		}
	}

	window = sweep.DefaultWindow
	if raw := strings.TrimSpace(q.Get("window")); raw != "" {
		window, err = strconv.Atoi(raw)
		if err != nil || window <= 0 || window > sweep.MaxWindow {
			return 0, "", false, errInvalid("window", raw, fmt.Sprintf("expected an integer 1-%d", sweep.MaxWindow))
		}
	}
	branch = strings.TrimSpace(q.Get("branch"))
	if raw := strings.TrimSpace(q.Get("includeDismissed")); raw != "" {
		includeDismissed, err = strconv.ParseBool(raw)
		if err != nil {
			return 0, "", false, errInvalid("includeDismissed", raw, "expected true or false")
		}
	}
	return window, branch, includeDismissed, nil
}

func (s *Server) assembleSweepReport(ctx context.Context, project, workdir, branch string, window int, includeDismissed bool) (sweep.Report, error) {
	var report sweep.Report
	err := s.queryDB(ctx, project, func(ctx context.Context, db *sql.DB) error {
		var err error
		report, err = sweep.Run(ctx, sweep.Input{
			DB: db, Project: project, Workdir: workdir, Branch: branch, Window: window,
			IncludeDismissed: includeDismissed, Git: s.git, KV: s.bd,
		})
		return err
	})
	return report, err
}

type findingDigestBody struct {
	MaterialDigest string `json:"materialDigest"`
	Window         int    `json:"window"`
	Branch         string `json:"branch"`
}

func (s *Server) handleApplyFinding(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}
	fingerprint := chi.URLParam(r, "fingerprint")
	if !requireFingerprint(w, fingerprint) {
		return
	}
	var body findingDigestBody
	if !decodeBody(w, r, &body) {
		return
	}
	finding, ok, err := s.currentFinding(r.Context(), project, workdir, fingerprint, body.Branch, body.Window)
	if err != nil {
		s.writeSweepError(w, project, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no current sweep finding "+fingerprint)
		return
	}
	if finding.MaterialDigest != body.MaterialDigest {
		writeError(w, http.StatusConflict, "the finding changed since it was swept; run a new sweep and review it again")
		return
	}
	if finding.Kind == "untracked-commit" && finding.Dismissed {
		writeError(w, http.StatusNotFound, "no current sweep finding "+fingerprint)
		return
	}
	if !finding.Suggested.Applicable {
		writeError(w, http.StatusUnprocessableEntity, "this finding requires human context and cannot be applied automatically")
		return
	}
	if err := s.applyFinding(r.Context(), workdir, finding); err != nil {
		s.writeWriteError(w, "bd apply failed", err)
		return
	}
	if finding.Kind == "untracked-commit" {
		appliedAt := time.Now().UTC()
		// An untracked commit's material does not change when its retroactive bead is
		// created. Store "resolved by apply" in the existing dismissal record so the
		// resolution is reversible through the same undismiss endpoint.
		note := "Applied automatically at " + appliedAt.Format(time.RFC3339) + "."
		if err := s.storeFindingDismissal(r.Context(), workdir, finding, note, appliedAt); err != nil {
			s.writeWriteError(w, "bd kv set failed", err)
			return
		}
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, map[string]any{"applied": true, "fingerprint": fingerprint})
}

func (s *Server) currentFinding(ctx context.Context, project, workdir, fingerprint, branch string, window int) (sweep.Finding, bool, error) {
	if window == 0 {
		window = sweep.DefaultWindow
	}
	report, err := s.sweepRun(ctx, project, workdir, branch, window, true)
	if err != nil {
		return sweep.Finding{}, false, err
	}
	for _, finding := range report.Findings {
		if finding.Fingerprint == fingerprint {
			return finding, true, nil
		}
	}
	return sweep.Finding{}, false, nil
}

// applyFinding executes the finding's typed apply intent. It never reads
// Suggested.Steps: the displayed argv is rendered from the same ApplyIntent, so
// a shape change in internal/sweep breaks the build here rather than the write
// path at runtime.
func (s *Server) applyFinding(ctx context.Context, workdir string, finding sweep.Finding) error {
	intent := finding.Apply
	switch finding.Kind {
	case "dangling-in-progress":
		if intent.IssueID == "" || intent.Status == "" {
			return errors.New("dangling-in-progress finding carries no issue id and status to apply")
		}
		status := intent.Status
		return s.bd.Update(ctx, workdir, []string{intent.IssueID}, bdexec.UpdateFields{Status: &status})
	case "untracked-commit":
		if intent.Title == "" || intent.Type == "" || intent.Priority == 0 || intent.SHA == "" {
			return errors.New("untracked-commit finding carries an incomplete retroactive bead intent")
		}
		priority := intent.Priority
		created, err := s.bd.Create(ctx, workdir, bdexec.CreateSpec{
			Title: intent.Title, Type: intent.Type, Priority: &priority, Description: intent.Description,
		})
		if err != nil {
			return err
		}
		return s.bd.Close(ctx, workdir, []string{created}, bdexec.CloseOptions{Reason: intent.CloseReason()})
	default:
		return fmt.Errorf("unsupported applicable finding kind %q", finding.Kind)
	}
}

func requireFingerprint(w http.ResponseWriter, fingerprint string) bool {
	if bdexec.ValidToken(fingerprint) {
		return true
	}
	writeError(w, http.StatusBadRequest, "invalid finding fingerprint "+strconv.Quote(fingerprint))
	return false
}

type dismissFindingBody struct {
	MaterialDigest string `json:"materialDigest"`
	Note           string `json:"note"`
	Window         int    `json:"window"`
	Branch         string `json:"branch"`
}

func (s *Server) handleDismissFinding(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}
	fingerprint := chi.URLParam(r, "fingerprint")
	if !requireFingerprint(w, fingerprint) {
		return
	}
	var body dismissFindingBody
	if !decodeBody(w, r, &body) {
		return
	}
	finding, found, err := s.currentFinding(r.Context(), project, workdir, fingerprint, body.Branch, body.Window)
	if err != nil {
		s.writeSweepError(w, project, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no current sweep finding "+fingerprint)
		return
	}
	if finding.MaterialDigest != body.MaterialDigest {
		writeError(w, http.StatusConflict, "the finding changed since it was swept; run a new sweep and review it again")
		return
	}
	if err := s.storeFindingDismissal(r.Context(), workdir, finding, body.Note, time.Now().UTC()); err != nil {
		s.writeWriteError(w, "bd kv set failed", err)
		return
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": true, "fingerprint": fingerprint})
}

func (s *Server) storeFindingDismissal(ctx context.Context, workdir string, finding sweep.Finding, note string, dismissedAt time.Time) error {
	value, err := json.Marshal(sweep.Dismissal{
		MaterialDigest: finding.MaterialDigest,
		Kind:           finding.Kind,
		DismissedAt:    dismissedAt,
		Note:           note,
	})
	if err != nil {
		return err
	}
	return s.bd.KVSet(ctx, workdir, sweep.DismissalKey(finding.Fingerprint), string(value))
}

func (s *Server) writeSweepError(w http.ResponseWriter, project string, err error) {
	if be, ok := bdexec.AsBdError(err); ok {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "sweep failed", Stderr: be.Message()})
		return
	}
	s.writeRegistryError(w, project, err)
}

func (s *Server) handleUndismissFinding(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	workdir, ok := s.workdirFor(w, project)
	if !ok {
		return
	}
	fingerprint := chi.URLParam(r, "fingerprint")
	if !requireFingerprint(w, fingerprint) {
		return
	}
	if err := s.bd.KVClear(r.Context(), workdir, sweep.DismissalKey(fingerprint)); err != nil {
		s.writeWriteError(w, "bd kv clear failed", err)
		return
	}
	s.reg.MarkWritten(project)
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": false, "fingerprint": fingerprint})
}
