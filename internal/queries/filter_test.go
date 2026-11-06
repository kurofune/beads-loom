package queries

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// normalize collapses whitespace so tests can assert on fragments without
// caring about the SQL's indentation.
func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

func doltTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initCmd := exec.Command("dolt", "init", "--name", "loom-test", "--email", "loom-test@example.invalid")
	initCmd.Dir = dir
	if output, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("dolt init: %v\n%s", err, output)
	}
	return dir
}

func TestReceiptBlankExprClassifiesWhitespace(t *testing.T) {
	tests := []struct {
		name              string
		acceptanceSQL     string
		closeReasonSQL    string
		missingAcceptance bool
		missingClose      bool
	}{
		{name: "empty acceptance", acceptanceSQL: "''", closeReasonSQL: "'landed'", missingAcceptance: true},
		{name: "spaces-only acceptance", acceptanceSQL: "'   '", closeReasonSQL: "'landed'", missingAcceptance: true},
		{name: "tab-only acceptance", acceptanceSQL: "CHAR(9)", closeReasonSQL: "'landed'", missingAcceptance: true},
		{name: "newline-only acceptance", acceptanceSQL: "CHAR(10)", closeReasonSQL: "'landed'", missingAcceptance: true},
		{name: "populated acceptance", acceptanceSQL: "'promised'", closeReasonSQL: "'landed'"},
		{name: "empty close reason", acceptanceSQL: "'promised'", closeReasonSQL: "''", missingClose: true},
		{name: "spaces-only close reason", acceptanceSQL: "'promised'", closeReasonSQL: "'   '", missingClose: true},
		{name: "tab-only close reason", acceptanceSQL: "'promised'", closeReasonSQL: "CHAR(9)", missingClose: true},
		{name: "newline-only close reason", acceptanceSQL: "'promised'", closeReasonSQL: "CHAR(10)", missingClose: true},
		{name: "populated close reason", acceptanceSQL: "'promised'", closeReasonSQL: "'landed'"},
	}

	dir := doltTestRepo(t)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			query := fmt.Sprintf(
				"SELECT %s AS missingAcceptance, %s AS missingCloseReason FROM (SELECT %s AS acceptance_criteria, %s AS close_reason) fixture",
				receiptBlankExpr("acceptance_criteria"),
				receiptBlankExpr("close_reason"),
				tc.acceptanceSQL,
				tc.closeReasonSQL,
			)
			cmd := exec.Command("dolt", "sql", "-r", "json", "-q", query)
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dolt sql: %v\n%s", err, output)
			}
			var result struct {
				Rows []struct {
					MissingAcceptance bool `json:"missingAcceptance"`
					MissingClose      bool `json:"missingCloseReason"`
				} `json:"rows"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("decode dolt output %q: %v", output, err)
			}
			if len(result.Rows) != 1 {
				t.Fatalf("rows = %#v, want one", result.Rows)
			}
			if got := result.Rows[0].MissingAcceptance; got != tc.missingAcceptance {
				t.Errorf("missing acceptance = %v, want %v", got, tc.missingAcceptance)
			}
			if got := result.Rows[0].MissingClose; got != tc.missingClose {
				t.Errorf("missing close reason = %v, want %v", got, tc.missingClose)
			}
		})
	}
}

func TestNoReceiptPredicateSelectsClosedIssuesMissingEitherReceiptOnce(t *testing.T) {
	dir := doltTestRepo(t)
	query := fmt.Sprintf(`
SELECT id FROM (
  SELECT 'closed-both' AS id, 'closed' AS status, '' AS acceptance_criteria, '' AS close_reason
  UNION ALL SELECT 'closed-no-acceptance', 'closed', '', 'landed'
  UNION ALL SELECT 'closed-no-close-reason', 'closed', 'promised', ''
  UNION ALL SELECT 'closed-complete', 'closed', 'promised', 'landed'
  UNION ALL SELECT 'open-both', 'open', '', ''
) i
WHERE %s
ORDER BY id`, noReceiptPredicate("i"))
	cmd := exec.Command("dolt", "sql", "-r", "json", "-q", query)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dolt sql: %v\n%s", err, output)
	}
	var result struct {
		Rows []struct {
			ID string `json:"id"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode dolt output %q: %v", output, err)
	}
	got := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		got = append(got, row.ID)
	}
	want := []string{"closed-both", "closed-no-acceptance", "closed-no-close-reason"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matching ids = %v, want %v", got, want)
	}
}

func TestBuildWhere(t *testing.T) {
	tests := []struct {
		name string
		f    Filter
		// wantContains are fragments the WHERE body must contain.
		wantContains []string
		// wantMissing are fragments it must NOT contain.
		wantMissing []string
		wantArgs    []any
	}{
		{
			name: "zero filter hides closed and infra rows",
			f:    Filter{},
			wantContains: []string{
				"COALESCE(i.ephemeral, 0) = 0",
				"COALESCE(i.is_template, 0) = 0",
				"COALESCE(i.event_kind, '') = ''",
				"i.issue_type NOT IN ('gate', 'agent', 'role', 'message', 'event')",
				"i.status <> ?",
			},
			wantArgs: []any{"closed"},
		},
		{
			name:        "include closed drops the status exclusion",
			f:           Filter{IncludeClosed: true},
			wantMissing: []string{"i.status <> ?"},
			wantArgs:    nil,
		},
		{
			name:         "no receipt returns only closed issues missing either receipt",
			f:            Filter{NoReceipt: true},
			wantContains: []string{"i.status = 'closed'", "i.acceptance_criteria", "i.close_reason", " OR "},
			wantMissing:  []string{"i.status <> ?"},
			wantArgs:     nil,
		},
		{
			name:         "explicit statuses override the closed default",
			f:            Filter{Statuses: []string{"open", "in_progress"}},
			wantContains: []string{"i.status IN (?, ?)"},
			wantMissing:  []string{"i.status <> ?"},
			wantArgs:     []any{"open", "in_progress"},
		},
		{
			name:         "explicit closed status is honoured",
			f:            Filter{Statuses: []string{"closed"}},
			wantContains: []string{"i.status IN (?)"},
			wantArgs:     []any{"closed"},
		},
		{
			name:         "type list",
			f:            Filter{IncludeClosed: true, Types: []string{"epic", "bug"}},
			wantContains: []string{"i.issue_type IN (?, ?)"},
			wantArgs:     []any{"epic", "bug"},
		},
		{
			name:         "priority list",
			f:            Filter{IncludeClosed: true, Priorities: []int{0, 1}},
			wantContains: []string{"i.priority IN (?, ?)"},
			wantArgs:     []any{0, 1},
		},
		{
			name:         "priority range",
			f:            Filter{IncludeClosed: true, PriorityMin: ptr(1), PriorityMax: ptr(3)},
			wantContains: []string{"i.priority >= ?", "i.priority <= ?"},
			wantArgs:     []any{1, 3},
		},
		{
			name:         "priority min only",
			f:            Filter{IncludeClosed: true, PriorityMin: ptr(2)},
			wantContains: []string{"i.priority >= ?"},
			wantMissing:  []string{"i.priority <= ?"},
			wantArgs:     []any{2},
		},
		{
			name:         "explicit priority list wins over range",
			f:            Filter{IncludeClosed: true, Priorities: []int{0}, PriorityMin: ptr(3), PriorityMax: ptr(4)},
			wantContains: []string{"i.priority IN (?)"},
			wantMissing:  []string{"i.priority >= ?", "i.priority <= ?"},
			wantArgs:     []any{0},
		},
		{
			name: "single label",
			f:    Filter{IncludeClosed: true, Labels: []string{"phase-1"}},
			wantContains: []string{
				"i.id IN (SELECT l.issue_id FROM labels l WHERE l.label IN (?) GROUP BY l.issue_id HAVING COUNT(DISTINCT l.label) = ?)",
			},
			wantArgs: []any{"phase-1", 1},
		},
		{
			name: "labels are ANDed via a distinct count",
			f:    Filter{IncludeClosed: true, Labels: []string{"phase-1", "layer-ui"}},
			wantContains: []string{
				"l.label IN (?, ?)",
				"HAVING COUNT(DISTINCT l.label) = ?",
			},
			wantArgs: []any{"phase-1", "layer-ui", 2},
		},
		{
			name:         "assignee",
			f:            Filter{IncludeClosed: true, Assignee: "nate"},
			wantContains: []string{"i.assignee = ?"},
			wantArgs:     []any{"nate"},
		},
		{
			name:         "unassigned sentinel takes no argument",
			f:            Filter{IncludeClosed: true, Assignee: UnassignedSentinel},
			wantContains: []string{"COALESCE(i.assignee, '') = ''"},
			wantMissing:  []string{"i.assignee = ?"},
			wantArgs:     nil,
		},
		{
			// The palette types bead ids as often as it types words, so a
			// lookup that only reads the title cannot find `loom-nlm`.
			name:         "free text matches the title or the id",
			f:            Filter{IncludeClosed: true, Text: "kanban"},
			wantContains: []string{"i.title LIKE ?", "i.id LIKE ?"},
			wantArgs:     []any{"%kanban%", "%kanban%"},
		},
		{
			// "board sample" must find "Board silently renders a 200-row
			// sample"; a single LIKE over the whole phrase never would.
			name:         "every word of free text must match",
			f:            Filter{IncludeClosed: true, Text: "board  sample"},
			wantContains: []string{"i.title LIKE ?", "i.id LIKE ?"},
			wantArgs:     []any{"%board%", "%board%", "%sample%", "%sample%"},
		},
		{
			name:        "blank free text is ignored",
			f:           Filter{IncludeClosed: true, Text: "   "},
			wantMissing: []string{"i.title LIKE ?", "i.id LIKE ?"},
			wantArgs:    nil,
		},
		{
			name:     "like wildcards in user text are escaped",
			f:        Filter{IncludeClosed: true, Text: "100%_x"},
			wantArgs: []any{`%100\%\_x%`, `%100\%\_x%`},
		},
		{
			name: "everything at once",
			f: Filter{
				Statuses:   []string{"open"},
				Types:      []string{"feature"},
				Priorities: []int{1},
				Labels:     []string{"phase-1"},
				Assignee:   "nate",
				Text:       "filter",
			},
			wantArgs: []any{"open", "feature", 1, "phase-1", 1, "nate", "%filter%", "%filter%"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			where, args := BuildWhere(tc.f)
			got := normalize(where)

			for _, want := range tc.wantContains {
				if !strings.Contains(got, normalize(want)) {
					t.Errorf("WHERE missing %q\ngot: %s", want, got)
				}
			}
			for _, unwanted := range tc.wantMissing {
				if strings.Contains(got, normalize(unwanted)) {
					t.Errorf("WHERE unexpectedly contains %q\ngot: %s", unwanted, got)
				}
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, tc.wantArgs)
			}
			// Placeholder count must match argument count or the driver errors
			// at runtime rather than in a test.
			if n := strings.Count(where, "?"); n != len(args) {
				t.Errorf("%d placeholders but %d args\nwhere: %s", n, len(args), where)
			}
		})
	}
}

func TestVisiblePredicate(t *testing.T) {
	got := normalize(VisiblePredicate("c"))
	for _, want := range []string{
		"COALESCE(c.ephemeral, 0) = 0",
		"COALESCE(c.is_template, 0) = 0",
		"COALESCE(c.event_kind, '') = ''",
		"c.issue_type NOT IN",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("VisiblePredicate(c) missing %q\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, "i.") {
		t.Errorf("VisiblePredicate(c) still references the i alias: %s", got)
	}
}

func TestOrderBy(t *testing.T) {
	tests := []struct{ sort, want string }{
		{SortPriority, "i.priority ASC, i.updated_at DESC, i.id ASC"},
		{"", "i.priority ASC, i.updated_at DESC, i.id ASC"},
		{SortCreated, "i.created_at DESC, i.id ASC"},
		{SortUpdated, "i.updated_at DESC, i.id ASC"},
		{"nonsense", "i.priority ASC, i.updated_at DESC, i.id ASC"},
	}
	for _, tc := range tests {
		t.Run(tc.sort, func(t *testing.T) {
			if got := OrderBy(tc.sort); got != tc.want {
				t.Errorf("OrderBy(%q) = %q, want %q", tc.sort, got, tc.want)
			}
		})
	}
	// Every ordering must be total, or paging silently drops or repeats rows.
	for _, s := range []string{SortPriority, SortCreated, SortUpdated, ""} {
		if !strings.HasSuffix(OrderBy(s), "i.id ASC") {
			t.Errorf("OrderBy(%q) is not a total order: %q", s, OrderBy(s))
		}
	}
}

func TestPage(t *testing.T) {
	tests := []struct {
		name                  string
		f                     Filter
		wantLimit, wantOffset int
	}{
		{"zero uses defaults", Filter{}, DefaultLimit, 0},
		{"explicit", Filter{Limit: 50, Offset: 100}, 50, 100},
		{"negative limit falls back", Filter{Limit: -1}, DefaultLimit, 0},
		{"limit is capped", Filter{Limit: 1 << 20}, MaxLimit, 0},
		{"negative offset clamps to zero", Filter{Offset: -5}, DefaultLimit, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset := tc.f.Page()
			if limit != tc.wantLimit || offset != tc.wantOffset {
				t.Errorf("Page() = (%d, %d), want (%d, %d)", limit, offset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}

func TestPlaceholders(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "NULL"},
		{-1, "NULL"},
		{1, "?"},
		{3, "?, ?, ?"},
	}
	for _, tc := range tests {
		if got := placeholders(tc.n); got != tc.want {
			t.Errorf("placeholders(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestParseCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"open", []string{"open"}},
		{"open,in_progress", []string{"open", "in_progress"}},
		{" open , , in_progress ", []string{"open", "in_progress"}},
		{",,,", nil},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := ParseCSV(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseCSV(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestStatsHealth(t *testing.T) {
	s := Stats{
		ByStatus:   map[string]int{"open": 13, "in_progress": 2, "blocked": 1, "deferred": 1, "closed": 3, "pinned": 1},
		Ready:      5,
		DepBlocked: 8,
	}
	h := s.Health()
	if h.Ready != 5 || h.InProgress != 2 || h.Blocked != 8 || h.Open != 13 || h.Total != 21 {
		t.Errorf("Health() = %+v", h)
	}
}
