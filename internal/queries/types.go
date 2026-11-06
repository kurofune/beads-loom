// Package queries is Loom's read layer. Every read is a SQL statement against a
// project's dolt server — no shelling out to bd, and no N+1: the issue list
// resolves readiness, dependency counts, parent and child rollups in a single
// statement, with labels hydrated by one follow-up query.
//
// The structs here are the HTTP wire contract. Their json tags are what the
// frontend is built against; renaming a field breaks the UI.
package queries

import "time"

// Issue is one row of the backlog. In list responses the prose fields are
// omitted; GetIssue fills them in.
type Issue struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Priority int      `json:"priority"`
	Assignee string   `json:"assignee"`
	Labels   []string `json:"labels"`
	Estimate *int     `json:"estimate"`

	Created    time.Time  `json:"created"`
	Updated    time.Time  `json:"updated"`
	Closed     *time.Time `json:"closed"`
	Due        *time.Time `json:"due"`
	DeferUntil *time.Time `json:"deferUntil"`

	// Ready mirrors the ready_issues view — the same semantics as `bd ready`.
	// A dependency-blocked issue keeps stored status "open", so this is the
	// only trustworthy readiness signal.
	Ready  bool `json:"ready"`
	Pinned bool `json:"pinned"`
	// NotReady explains an open issue's absence from the ready set:
	// "deps" (an unresolved blocks dependency, directly or via a blocked
	// ancestor) or "deferred" (held back only by scheduling). Null when the
	// issue is ready or is not open.
	NotReady *string `json:"notReady"`
	// MissingAcceptance and MissingCloseReason classify a closed issue's absent
	// receipts so a filtered backlog can say exactly what is missing.
	MissingAcceptance  bool `json:"missingAcceptance"`
	MissingCloseReason bool `json:"missingCloseReason"`

	DepCounts DepCounts `json:"depCounts"`
	// Parent is the parent-child dependency where this issue is the child.
	Parent *ParentRef `json:"parent"`
	// ChildStats is nil unless this issue has children.
	ChildStats *ChildStats `json:"childStats"`
}

// DepCounts summarizes an issue's blocking relationships.
type DepCounts struct {
	// BlockedBy counts unresolved `blocks` dependencies pointing at this issue.
	BlockedBy int `json:"blockedBy"`
	// Blocks counts issues that depend on this one.
	Blocks int `json:"blocks"`
}

// ParentRef is the minimal parent reference carried on every list row so the
// UI can show the epic a card belongs to without a second fetch.
type ParentRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ChildStats is the rollup shown on epics. It covers DIRECT children only —
// InitiativeRollup is the transitive-subtree counterpart.
//
// status=pinned children are excluded from every field, matching the exclusion
// InitiativeRollup applies to its descendants: a pinned bead is a standing
// reference note, not work. The two rollups must exclude it together or the
// same epic prints two different closed/total ratios (loom-1ya).
type ChildStats struct {
	Total  int `json:"total"`
	Closed int `json:"closed"`
	// Started counts direct children whose work is under way. It is disjoint
	// from Closed and is never folded into it.
	Started int `json:"started"`
}

// IssueRef identifies a related issue in the detail panel's dependency lists.
type IssueRef struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	Ready    bool   `json:"ready"`
}

// IssueDetail is a single issue with its prose, dependencies and history.
type IssueDetail struct {
	Issue
	// Health is present only for epics and uses the transitive initiative
	// rollup, never the direct-child ChildStats denominator.
	Health *InitiativeHealth `json:"health"`

	Description string `json:"description"`
	// AcceptanceCriteria is raw markdown; each non-blank line is one criterion
	// and the UI renders it as a checklist.
	AcceptanceCriteria string `json:"acceptanceCriteria"`
	Design             string `json:"design"`
	Notes              string `json:"notes"`
	CloseReason        string `json:"closeReason"`
	Owner              string `json:"owner"`

	Deps     Deps      `json:"deps"`
	Comments []Comment `json:"comments"`
	Events   []Event   `json:"events"`
}

// Deps groups an issue's dependencies by kind. The kinds render differently:
// blocks drives readiness, parent-child is hierarchy, discovered-from is
// provenance and never blocks anything.
type Deps struct {
	Blocks         []IssueRef `json:"blocks"`
	BlockedBy      []IssueRef `json:"blockedBy"`
	Children       []IssueRef `json:"children"`
	DiscoveredFrom []IssueRef `json:"discoveredFrom"`
	Parent         *IssueRef  `json:"parent"`
}

// Comment is one entry from the comments table.
type Comment struct {
	ID      string    `json:"id"`
	Author  string    `json:"author"`
	Text    string    `json:"text"`
	Created time.Time `json:"created"`
}

// Event is one entry from the events table — the detail panel's history feed.
type Event struct {
	Type     string    `json:"type"`
	Actor    string    `json:"actor"`
	OldValue string    `json:"oldValue"`
	NewValue string    `json:"newValue"`
	Comment  string    `json:"comment"`
	Created  time.Time `json:"created"`
}

// IssueList is the /issues response: the page of issues plus the total number
// matching the filter, ignoring limit and offset.
type IssueList struct {
	Issues []Issue `json:"issues"`
	Total  int     `json:"total"`
}

// Stats is the /stats response.
type Stats struct {
	// ByStatus counts visible issues per raw bd status string.
	ByStatus map[string]int `json:"byStatus"`
	// Ready is the size of the ready_issues view, infra rows excluded.
	Ready int `json:"ready"`
	// DepBlocked counts issues that are open and stuck on a dependency.
	//
	// Defer-scheduled issues are excluded: a child of a deferred epic is open
	// and absent from ready_issues, but nothing is blocking it, and counting it
	// here told the manager a number that was wrong (review finding I2).
	DepBlocked int `json:"depBlocked"`
	// DeferScheduled counts open issues held back only by scheduling.
	DeferScheduled int `json:"deferScheduled"`
	ClosedLast7d   int `json:"closedLast7d"`

	// ReadyFrontier is the number of issues that could be started right now.
	// It equals Ready; the separate name exists because that is the number a
	// manager reads as "how much work is available", and the frontier framing
	// is what the UI labels it.
	ReadyFrontier int `json:"readyFrontier"`
	// Trend is the last TrendDays days of opened/closed counts, oldest first,
	// with a row for every day including zero days.
	Trend []TrendPoint `json:"trend"`
}

// TrendDays is the window the stats trend covers.
const TrendDays = 30

// TrendPoint is one day of the opened/closed trend.
type TrendPoint struct {
	// Date is YYYY-MM-DD in UTC.
	Date   string `json:"date"`
	Opened int    `json:"opened"`
	Closed int    `json:"closed"`
}

// StaleIssue is one issue that has not been touched recently.
type StaleIssue struct {
	Issue
	// StaleDays is how long since updated_at, in whole days.
	StaleDays int `json:"staleDays"`
}

// Health is the compact per-project rollup shown on the project switcher.
type Health struct {
	Ready      int `json:"ready"`
	InProgress int `json:"inProgress"`
	// Blocked is the dependency-blocked count (same as Stats.DepBlocked); it is
	// the number the manager cares about, not the count of the manual
	// "blocked" status.
	Blocked int `json:"blocked"`
	Open    int `json:"open"`
	Total   int `json:"total"`
}

// Health derives the project-switcher rollup from a Stats result.
func (s Stats) Health() Health {
	total := 0
	for _, n := range s.ByStatus {
		total += n
	}
	return Health{
		Ready:      s.Ready,
		InProgress: s.ByStatus[StatusInProgress],
		Blocked:    s.DepBlocked,
		Open:       s.ByStatus[StatusOpen],
		Total:      total,
	}
}

// Raw bd status strings Loom reasons about by name.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusDeferred   = "deferred"
	StatusClosed     = "closed"
	StatusPinned     = "pinned"
	StatusHooked     = "hooked"
)

// startedPredicate is true for a child whose work is under way. It is a
// STATUS bucket, deliberately NOT a readiness one: readiness comes only from
// the ready_issues view (see readiness.go), and an in_progress issue is
// absent from that view, so `started` fills a hole rather than overlapping.
//
// The status set is interpolated from the constants above rather than passed as
// arguments: both call sites build their SQL with positional args, and a new `?`
// inside a subselect would silently shift every argument after it.
func startedPredicate(alias string) string {
	return alias + ".status IN ('" + StatusInProgress + "', '" + StatusHooked + "')"
}

// Dependency types in the dependencies table.
const (
	DepBlocks         = "blocks"
	DepParentChild    = "parent-child"
	DepDiscoveredFrom = "discovered-from"
)
