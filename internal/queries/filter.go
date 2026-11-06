package queries

import (
	"fmt"
	"strings"
)

// MaxLimit caps a single page so a bad request cannot pull the whole database.
const MaxLimit = 1000

// DefaultLimit applies when the caller does not ask for a page size.
const DefaultLimit = 200

// Sort keys accepted by Filter.Sort.
const (
	SortPriority = "priority"
	SortCreated  = "created"
	SortUpdated  = "updated"
)

// Filter selects and orders a page of issues. The zero value is valid: it means
// "every non-closed, non-infra issue, highest priority first".
type Filter struct {
	// Statuses restricts to these raw bd statuses. When set it overrides the
	// default "hide closed" behaviour.
	Statuses []string
	// Types restricts to these issue_type values.
	Types []string
	// Priorities is an explicit priority list. When non-empty PriorityMin and
	// PriorityMax are ignored.
	Priorities []int
	// PriorityMin and PriorityMax bound priority inclusively. Note 0 is the
	// highest priority, so Min is numerically the smaller bound.
	PriorityMin *int
	PriorityMax *int
	// Labels must ALL be present on an issue for it to match.
	Labels []string
	// Assignee matches exactly. The sentinel UnassignedSentinel matches issues
	// with no assignee.
	Assignee string
	// Text is a free-text lookup: every whitespace-separated word must appear
	// in the title or the id. Words are ANDed, columns are ORed.
	Text string
	// IncludeClosed shows closed issues even when Statuses is empty.
	IncludeClosed bool
	// Epic restricts to that epic's transitive parent-child descendants. The
	// epic row itself is excluded, matching the Initiatives rollup scope.
	Epic string
	// NoReceipt restricts to closed issues missing acceptance criteria or a
	// close reason. Its closed-only predicate overrides the default closed hide.
	NoReceipt bool
	// IDs restricts to an explicit set of issue ids. Used by the bd-query
	// passthrough to re-read the rows bd matched.
	IDs []string

	Limit  int
	Offset int
	// Sort is one of the Sort* constants; anything else falls back to priority.
	Sort string
}

// UnassignedSentinel is the Assignee value meaning "has no assignee".
const UnassignedSentinel = "@none"

// infraExclusion drops the rows bd itself hides from list views: ephemeral
// wisps, templates, and the event/agent/gate infra beads that agents use to
// coordinate. Loom never shows these.
//
// The columns are nullable, hence the COALESCE.
const infraExclusion = `COALESCE(i.ephemeral, 0) = 0
 AND COALESCE(i.is_template, 0) = 0
 AND COALESCE(i.event_kind, '') = ''
 AND i.issue_type NOT IN ('gate', 'agent', 'role', 'message', 'event')`

// VisiblePredicate returns the infra exclusion for an arbitrary table alias, so
// subqueries over child or dependent issues hide the same rows the main list
// does.
func VisiblePredicate(alias string) string {
	return strings.ReplaceAll(infraExclusion, "i.", alias+".")
}

// receiptBlankExpr recognizes empty receipt fields after removing the control
// whitespace that Dolt's MySQL-compatible TRIM leaves behind. The caller owns
// the column expression; every receipt query uses this one definition.
func receiptBlankExpr(column string) string {
	return fmt.Sprintf(
		"TRIM(REPLACE(REPLACE(REPLACE(COALESCE(%s, ''), CHAR(9), ''), CHAR(10), ''), CHAR(13), '')) = ''",
		column,
	)
}

func missingAcceptancePredicate(alias string) string {
	return receiptBlankExpr(alias + ".acceptance_criteria")
}

func missingCloseReasonPredicate(alias string) string {
	return receiptBlankExpr(alias + ".close_reason")
}

func missingAcceptanceReceiptPredicate(alias string) string {
	return alias + ".status = '" + StatusClosed + "' AND " + missingAcceptancePredicate(alias)
}

func missingCloseReasonReceiptPredicate(alias string) string {
	return alias + ".status = '" + StatusClosed + "' AND " + missingCloseReasonPredicate(alias)
}

// noReceiptPredicate is the shared definition behind both the Initiatives
// count and the Backlog drill-down. Keeping the status gate here prevents an
// open bead with blank prose from appearing in either answer.
func noReceiptPredicate(alias string) string {
	return fmt.Sprintf(
		"%s.status = '%s' AND (%s OR %s)",
		alias,
		StatusClosed,
		missingAcceptancePredicate(alias),
		missingCloseReasonPredicate(alias),
	)
}

// BuildWhere renders the filter as a WHERE body (without the WHERE keyword)
// plus its arguments. The fragment only ever references the `i` alias and
// self-contained subqueries, so the same fragment can drive both the page query
// and the COUNT(*) query.
func BuildWhere(f Filter) (string, []any) {
	clauses := []string{infraExclusion}
	var args []any

	switch {
	case len(f.Statuses) > 0:
		clauses = append(clauses, "i.status IN ("+placeholders(len(f.Statuses))+")")
		for _, s := range f.Statuses {
			args = append(args, s)
		}
	case f.NoReceipt:
		// noReceiptPredicate below supplies the stricter closed-only status gate.
	case !f.IncludeClosed:
		// Closed work is noise in the backlog unless explicitly asked for.
		clauses = append(clauses, "i.status <> ?")
		args = append(args, StatusClosed)
	}

	if len(f.Types) > 0 {
		clauses = append(clauses, "i.issue_type IN ("+placeholders(len(f.Types))+")")
		for _, t := range f.Types {
			args = append(args, t)
		}
	}

	switch {
	case len(f.Priorities) > 0:
		clauses = append(clauses, "i.priority IN ("+placeholders(len(f.Priorities))+")")
		for _, p := range f.Priorities {
			args = append(args, p)
		}
	default:
		if f.PriorityMin != nil {
			clauses = append(clauses, "i.priority >= ?")
			args = append(args, *f.PriorityMin)
		}
		if f.PriorityMax != nil {
			clauses = append(clauses, "i.priority <= ?")
			args = append(args, *f.PriorityMax)
		}
	}

	if len(f.Labels) > 0 {
		// AND semantics: the issue must carry every requested label. Counting
		// DISTINCT matches inside one subquery beats one EXISTS per label.
		clauses = append(clauses, fmt.Sprintf(
			"i.id IN (SELECT l.issue_id FROM labels l WHERE l.label IN (%s) GROUP BY l.issue_id HAVING COUNT(DISTINCT l.label) = ?)",
			placeholders(len(f.Labels))))
		for _, l := range f.Labels {
			args = append(args, l)
		}
		args = append(args, len(f.Labels))
	}

	switch f.Assignee {
	case "":
	case UnassignedSentinel:
		clauses = append(clauses, "COALESCE(i.assignee, '') = ''")
	default:
		clauses = append(clauses, "i.assignee = ?")
		args = append(args, f.Assignee)
	}

	if len(f.IDs) > 0 {
		clauses = append(clauses, "i.id IN ("+placeholders(len(f.IDs))+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}

	if f.NoReceipt {
		clauses = append(clauses, noReceiptPredicate("i"))
	}

	// Free text is a lookup, not a filter: the command palette types bead ids
	// as often as it types words, and it types several words at a time. So
	// every whitespace-separated word must appear in either the title or the
	// id — AND across words, OR across the two columns. One LIKE over the raw
	// phrase would find neither "loom-nlm" nor "board sample".
	for _, word := range strings.Fields(f.Text) {
		clauses = append(clauses, "(i.title LIKE ? OR i.id LIKE ?)")
		like := "%" + escapeLike(word) + "%"
		args = append(args, like, like)
	}

	return strings.Join(clauses, "\n AND "), args
}

// OrderBy renders the ORDER BY body for a sort key. Every ordering ends in
// i.id so paging is stable when the sort column ties.
func OrderBy(sort string) string {
	switch sort {
	case SortCreated:
		return "i.created_at DESC, i.id ASC"
	case SortUpdated:
		return "i.updated_at DESC, i.id ASC"
	case SortPriority, "":
		return "i.priority ASC, i.updated_at DESC, i.id ASC"
	default:
		return "i.priority ASC, i.updated_at DESC, i.id ASC"
	}
}

// Page returns the limit and offset to use, clamped to sane bounds.
func (f Filter) Page() (limit, offset int) {
	limit = f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	offset = f.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// escapeLike neutralizes LIKE wildcards in user text so searching for "100%"
// does not match everything.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
