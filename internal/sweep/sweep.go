// Package sweep assembles mechanical reconciliation findings from tracker and
// git reads. Running a sweep does not expose any bd write operation.
package sweep

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kurofune/beads-loom/internal/gitread"
	"github.com/kurofune/beads-loom/internal/queries"
)

const (
	DefaultWindow = 500
	MaxWindow     = 2000
	MaxFindings   = 1000
	SweepTimeout  = 30 * time.Second
	// DismissalKeyPrefix shares bd's KV namespace without colliding with memories.
	DismissalKeyPrefix = "loom.sweep.dismiss."
)

// GitReader is the read-only git surface a sweep can reach.
type GitReader interface {
	DefaultBranch(ctx context.Context, workdir string) (branch, method string, err error)
	Log(ctx context.Context, workdir, branch string, limit int) ([]gitread.Commit, error)
}

// KVReader is the only bd read available to a sweep.
type KVReader interface {
	KVList(ctx context.Context, workdir string) (map[string]string, error)
}

// Dismissal is the persisted decision attached to one finding fingerprint.
type Dismissal struct {
	MaterialDigest string    `json:"materialDigest"`
	Kind           string    `json:"kind"`
	DismissedAt    time.Time `json:"dismissedAt"`
	Note           string    `json:"note"`
}

// DismissalKey returns the one-key-per-finding KV key.
func DismissalKey(fingerprint string) string { return DismissalKeyPrefix + fingerprint }

// Input supplies one project's read sources and sweep options.
type Input struct {
	DB               *sql.DB
	Project          string
	Workdir          string
	Branch           string
	Window           int
	IncludeDismissed bool
	Now              time.Time
	Git              GitReader
	KV               KVReader
}

// Suggestion is a bd command template shown verbatim before any action.
type Suggestion struct {
	Steps      [][]string `json:"steps"`
	Applicable bool       `json:"applicable"`
	Rationale  string     `json:"rationale"`
}

// ApplyIntent is the typed source of truth behind an applicable finding.
//
// Both halves of the promise derive from these fields: the Suggested.Steps argv
// the reader is shown (rendered by the methods below) and the bd call the apply
// endpoint executes. Nothing reads the displayed argv back, so changing the
// suggestion's shape breaks the build rather than the write path.
type ApplyIntent struct {
	// IssueID and Status drive a dangling-in-progress reopen.
	IssueID string `json:"issueId,omitempty"`
	Status  string `json:"status,omitempty"`
	// Title, Type, Priority, Description and SHA drive an untracked-commit
	// retroactive bead.
	Title       string `json:"title,omitempty"`
	Type        string `json:"type,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	Description string `json:"description,omitempty"`
	SHA         string `json:"sha,omitempty"`
}

// CloseReason is the reason a retroactive bead is closed with.
func (a ApplyIntent) CloseReason() string { return "shipped in " + a.SHA }

// ReopenSteps renders the displayed argv for a dangling-in-progress finding.
func (a ApplyIntent) ReopenSteps() [][]string {
	return [][]string{{"bd", "update", a.IssueID, "--status", a.Status}}
}

// RetroactiveBeadSteps renders the displayed argv for an untracked-commit
// finding: create the bead, then close it against the commit that shipped it.
func (a ApplyIntent) RetroactiveBeadSteps() [][]string {
	return [][]string{
		{"bd", "create", "--title", a.Title, "--type", a.Type,
			"--priority", strconv.Itoa(a.Priority), "--description", a.Description},
		{"bd", "close", "{{created}}", "--reason", a.CloseReason()},
	}
}

// Finding is one divergence between tracker state and repository history.
type Finding struct {
	Fingerprint    string      `json:"fingerprint"`
	MaterialDigest string      `json:"materialDigest"`
	Kind           string      `json:"kind"`
	Severity       string      `json:"severity"`
	Title          string      `json:"title"`
	IssueIDs       []string    `json:"issueIds"`
	Commits        []string    `json:"commits"`
	Evidence       []string    `json:"evidence"`
	Apply          ApplyIntent `json:"apply"`
	Suggested      Suggestion  `json:"suggested"`
	Dismissed      bool        `json:"dismissed"`
	DismissedAt    *time.Time  `json:"dismissedAt"`
}

// Partial records a detector that could not finish without discarding results
// produced by the others.
type Partial struct {
	Detector string `json:"detector"`
	Reason   string `json:"reason"`
}

// Report is one bounded sweep result.
type Report struct {
	SweptAt        time.Time      `json:"sweptAt"`
	Branch         string         `json:"branch"`
	Window         int            `json:"window"`
	CommitsScanned int            `json:"commitsScanned"`
	Findings       []Finding      `json:"findings"`
	Partial        []Partial      `json:"partial"`
	Counts         map[string]int `json:"counts"`
}

// Run assembles findings from bounded, read-only detectors.
func Run(ctx context.Context, in Input) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, SweepTimeout)
	defer cancel()

	now := in.Now.UTC()
	if in.Now.IsZero() {
		now = time.Now().UTC()
	}
	window := in.Window
	if window <= 0 {
		window = DefaultWindow
	}
	report := Report{
		SweptAt: now, Branch: in.Branch, Window: window,
		Findings: []Finding{}, Partial: []Partial{}, Counts: map[string]int{},
	}

	entries, err := queries.NowFor(ctx, in.DB, in.Project, now)
	if err != nil {
		return Report{}, err
	}
	for _, entry := range entries {
		if !entry.Stale {
			continue
		}
		id := entry.Issue.ID
		since := "unknown"
		elapsed := "unknown"
		if entry.InStatusSince != nil {
			since = entry.InStatusSince.UTC().Format(time.RFC3339)
			elapsed = humanDuration(now.Sub(entry.InStatusSince.UTC()))
		}
		intent := ApplyIntent{IssueID: id, Status: "open"}
		finding := Finding{
			Kind: "dangling-in-progress", Severity: "medium",
			Title:    "Dangling in-progress issue: " + id,
			IssueIDs: []string{id}, Commits: []string{},
			Evidence: []string{fmt.Sprintf("%s has been in in_progress since %s (%s)", id, since, elapsed)},
			Apply:    intent,
			Suggested: Suggestion{
				Steps: intent.ReopenSteps(), Applicable: true,
				Rationale: "Return abandoned work to the ready backlog.",
			},
		}
		finding.Fingerprint = fingerprint(finding.Kind, id)
		finding.MaterialDigest = materialDigest(id, entry.Issue.Status, since)
		report.Findings = append(report.Findings, finding)
		report.Counts[finding.Kind]++
	}

	offset := 0
	for {
		page, err := queries.ListIssues(ctx, in.DB, queries.Filter{
			NoReceipt: true, IncludeClosed: true, Limit: 500, Offset: offset,
		})
		if err != nil {
			return Report{}, err
		}
		for _, issue := range page.Issues {
			closedAt := "unknown"
			if issue.Closed != nil {
				closedAt = issue.Closed.UTC().Format(time.RFC3339)
			}
			if issue.MissingAcceptance {
				finding := Finding{
					Kind: "empty-ac-close", Severity: "low",
					Title:    "Closed issue has no acceptance criteria: " + issue.ID,
					IssueIDs: []string{issue.ID}, Commits: []string{},
					Evidence: []string{fmt.Sprintf("%s closed at %s with blank acceptance criteria", issue.ID, closedAt)},
					Suggested: Suggestion{
						Steps:      [][]string{{"bd", "update", issue.ID, "--acceptance", "<what done meant>"}},
						Applicable: false, Rationale: "Acceptance criteria require human reconstruction.",
					},
				}
				finding.Fingerprint = fingerprint(finding.Kind, issue.ID)
				finding.MaterialDigest = materialDigest(issue.ID, closedAt, "acceptance")
				report.Findings = append(report.Findings, finding)
				report.Counts[finding.Kind]++
			}
			if issue.MissingCloseReason {
				finding := Finding{
					Kind: "empty-reason-close", Severity: "low",
					Title:    "Closed issue has no close reason: " + issue.ID,
					IssueIDs: []string{issue.ID}, Commits: []string{},
					Evidence: []string{fmt.Sprintf("%s closed at %s with a blank close reason", issue.ID, closedAt)},
					Suggested: Suggestion{
						Steps:      [][]string{{"bd", "close", issue.ID, "--reason", "<why it closed>"}},
						Applicable: false, Rationale: "The close reason requires human context.",
					},
				}
				finding.Fingerprint = fingerprint(finding.Kind, issue.ID)
				finding.MaterialDigest = materialDigest(issue.ID, closedAt, "reason")
				report.Findings = append(report.Findings, finding)
				report.Counts[finding.Kind]++
			}
		}
		offset += len(page.Issues)
		if len(page.Issues) == 0 || offset >= page.Total {
			break
		}
	}

	if in.Git != nil {
		branch := strings.TrimSpace(in.Branch)
		branchMethod := "request parameter"
		if branch == "" {
			var err error
			branch, branchMethod, err = in.Git.DefaultBranch(ctx, in.Workdir)
			if err != nil {
				report.Partial = append(report.Partial, Partial{Detector: "untracked-commit", Reason: gitErrorReason(err)})
				return finishReport(ctx, in, report)
			}
		}
		report.Branch = branch

		prefixes, err := projectPrefixes(ctx, in.DB)
		if err != nil {
			return Report{}, err
		}
		// With no prefixes there is nothing to recognise a tracked commit by, so
		// every commit in the window would be flagged and the prefix evidence line
		// would name nothing. Say why the detector did not run instead.
		if len(prefixes) == 0 {
			report.Partial = append(report.Partial, Partial{
				Detector: "untracked-commit",
				Reason: "the project tracker holds no issues, so no bead id prefixes exist to " +
					"recognise a tracked commit by; untracked-commit detection is skipped until it has at least one",
			})
			return finishReport(ctx, in, report)
		}
		commits, err := in.Git.Log(ctx, in.Workdir, branch, window)
		if err != nil {
			report.Partial = append(report.Partial, Partial{Detector: "untracked-commit", Reason: gitErrorReason(err)})
			return finishReport(ctx, in, report)
		}
		report.CommitsScanned = len(commits)
		idPattern := beadIDPattern(prefixes)
		prefixEvidence := strings.Join(prefixes, ", ")
		for _, commit := range commits {
			if idPattern.MatchString(commit.Subject) {
				continue
			}
			date := commit.AuthoredAt.UTC().Format("2006-01-02")
			description := fmt.Sprintf(
				"Retroactive bead for %s on %s (%s); the subject cited no bead id.",
				commit.SHA, branch, date,
			)
			intent := ApplyIntent{
				Title: commit.Subject, Type: "chore", Priority: 3,
				Description: description, SHA: commit.SHA,
			}
			finding := Finding{
				Kind: "untracked-commit", Severity: "low",
				Title:    "Commit cites no tracked bead: " + commit.Subject,
				IssueIDs: []string{}, Commits: []string{commit.SHA},
				Evidence: []string{
					fmt.Sprintf("%s %s", commit.SHA, commit.Subject),
					fmt.Sprintf("default branch %s via %s; window %d; merges excluded", branch, branchMethod, window),
					"project bead prefixes used: " + prefixEvidence,
				},
				Apply: intent,
				Suggested: Suggestion{
					Steps:      intent.RetroactiveBeadSteps(),
					Applicable: true, Rationale: "Create and close a retroactive bead for shipped work.",
				},
			}
			finding.Fingerprint = fingerprint(finding.Kind, commit.SHA)
			finding.MaterialDigest = materialDigest(commit.SHA)
			report.Findings = append(report.Findings, finding)
			report.Counts[finding.Kind]++
		}
	}
	return finishReport(ctx, in, report)
}

// finishReport marks dismissals, drops the ones the reader did not ask to see,
// and only then applies the per-kind cap.
//
// The order is the whole point: capping first let dismissed findings consume cap
// slots, so past MaxFindings of one kind dismissing revealed nothing while the
// Partial kept insisting more was hidden. Live findings are budgeted first and
// dismissed ones fill whatever room is left, so the cap's Partial reflects the
// post-dismissal set.
func finishReport(ctx context.Context, in Input, report Report) (Report, error) {
	dismissals, err := loadDismissals(ctx, in, &report)
	if err != nil {
		return Report{}, err
	}

	kept := make([]Finding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		if dismissal, ok := dismissals[finding.Fingerprint]; ok && dismissal.MaterialDigest == finding.MaterialDigest {
			finding.Dismissed = true
			dismissedAt := dismissal.DismissedAt.UTC()
			finding.DismissedAt = &dismissedAt
			if !in.IncludeDismissed {
				continue
			}
		}
		kept = append(kept, finding)
	}

	admitted := make([]bool, len(kept))
	budget := make(map[string]int)
	for i, finding := range kept {
		if finding.Dismissed {
			continue
		}
		if budget[finding.Kind] >= MaxFindings {
			report.Partial = appendCapPartial(report.Partial, finding.Kind,
				fmt.Sprintf("finding cap %d reached; additional findings omitted", MaxFindings))
			continue
		}
		budget[finding.Kind]++
		admitted[i] = true
	}
	// Dismissed findings are only in `kept` when the reader asked for them; they
	// take the slots live findings did not need, never the other way round.
	for i, finding := range kept {
		if !finding.Dismissed {
			continue
		}
		if budget[finding.Kind] >= MaxFindings {
			report.Partial = appendCapPartial(report.Partial, finding.Kind,
				fmt.Sprintf("finding cap %d reached; additional dismissed findings omitted", MaxFindings))
			continue
		}
		budget[finding.Kind]++
		admitted[i] = true
	}

	filtered := make([]Finding, 0, len(kept))
	report.Counts = map[string]int{}
	for i, finding := range kept {
		if !admitted[i] {
			continue
		}
		if !finding.Dismissed {
			report.Counts[finding.Kind]++
		}
		filtered = append(filtered, finding)
	}
	report.Findings = filtered
	return report, nil
}

// appendCapPartial records at most one cap Partial per detector, keeping the
// first (live-overflow) reason when both overflows trip for one kind.
func appendCapPartial(partials []Partial, detector, reason string) []Partial {
	for _, partial := range partials {
		if partial.Detector == detector && strings.HasPrefix(partial.Reason, "finding cap ") {
			return partials
		}
	}
	return append(partials, Partial{Detector: detector, Reason: reason})
}

func loadDismissals(ctx context.Context, in Input, report *Report) (map[string]Dismissal, error) {
	values := map[string]string{}
	if in.KV != nil {
		var err error
		values, err = in.KV.KVList(ctx, in.Workdir)
		if err != nil {
			return nil, err
		}
	}
	dismissals := make(map[string]Dismissal)
	for key, value := range values {
		if !strings.HasPrefix(key, DismissalKeyPrefix) {
			continue
		}
		var dismissal Dismissal
		if err := json.Unmarshal([]byte(value), &dismissal); err != nil {
			report.Partial = append(report.Partial, Partial{Detector: "dismissals", Reason: fmt.Sprintf("%s: %v", key, err)})
			continue
		}
		dismissals[strings.TrimPrefix(key, DismissalKeyPrefix)] = dismissal
	}
	return dismissals, nil
}

func projectPrefixes(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT SUBSTRING_INDEX(id,'-',1) AS prefix, COUNT(*) FROM issues GROUP BY prefix ORDER BY 2 DESC")
	if err != nil {
		return nil, fmt.Errorf("list project bead prefixes: %w", err)
	}
	defer rows.Close()
	prefixes := []string{}
	for rows.Next() {
		var prefix string
		var count int
		if err := rows.Scan(&prefix, &count); err != nil {
			return nil, fmt.Errorf("scan project bead prefix: %w", err)
		}
		if prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes, rows.Err()
}

// beadIDPattern matches a bead id carrying one of the project's own prefixes.
// It requires at least one prefix: a pattern built from none matches any
// hyphenated word, and a nil pattern matches nothing, so either would turn the
// whole commit window into findings. Callers gate on len(prefixes) first.
func beadIDPattern(prefixes []string) *regexp.Regexp {
	if len(prefixes) == 0 {
		panic("sweep: beadIDPattern requires at least one project bead prefix")
	}
	quoted := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		quoted[i] = regexp.QuoteMeta(prefix)
	}
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:` + strings.Join(quoted, "|") + `)-[a-z0-9]+(?:\.[0-9]+)*(?:$|[^A-Za-z0-9_.])`)
}

func gitErrorReason(err error) string {
	var commandErr *gitread.CommandError
	if errors.As(err, &commandErr) {
		return commandErr.Stderr
	}
	return err.Error()
}

func fingerprint(kind, subject string) string {
	sum := sha256.Sum256([]byte(kind + "\x1f" + subject))
	return hex.EncodeToString(sum[:8])
}

func materialDigest(fields ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x1f")))
	return hex.EncodeToString(sum[:])
}

func humanDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
