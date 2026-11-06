package queries_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/loomtest/livedolt"
	"github.com/kurofune/beads-loom/internal/queries"
)

// openSeeded connects to this repo's own seeded loom database, resolving the
// server exactly the way the registry does. It goes through the shared
// environment gate (AGENTS.md → "Testing"): no Dolt server means skip, so a
// developer without one still gets a green `go test ./...`. Anything missing
// INSIDE the database it returns is a fixture, and those still fail.
func openSeeded(t *testing.T) *sql.DB {
	t.Helper()

	workdir := loomtest.RepoRoot(t)
	// Serialize with the write-path tests in internal/bdexec, which mutate this
	// same database from a separate test binary.
	loomtest.LockFixture(t, workdir)

	env := livedolt.Require(t, workdir)
	db, err := sql.Open("mysql", env.DSN())
	if err != nil {
		// The gate already connected to this DSN, so a failure here is a
		// defect rather than an absent environment.
		t.Fatalf("cannot open %s after the environment gate passed: %v", env.Database, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestListIssuesAgainstSeededDB(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	got, err := queries.ListIssues(ctx, db, queries.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(got.Issues) == 0 {
		t.Fatal("seeded database returned no issues")
	}
	if got.Total < len(got.Issues) {
		t.Errorf("total %d < page size %d", got.Total, len(got.Issues))
	}

	var epics, withParent, withLabels, ready int
	for _, iss := range got.Issues {
		// Another test package may be mid-flight with a temporary bead in this
		// same database. Those are not fixture data; skip them.
		if loomtest.IsScratch(iss.Title) {
			continue
		}
		if iss.Status == queries.StatusClosed {
			t.Errorf("%s: closed issue leaked into the default list", iss.ID)
		}
		// Infra beads must never surface.
		for _, infra := range []string{"gate", "agent", "role", "message", "event"} {
			if iss.Type == infra {
				t.Errorf("%s: infra type %q leaked into the list", iss.ID, infra)
			}
		}
		if iss.Priority < 0 || iss.Priority > 4 {
			t.Errorf("%s: priority %d out of the 0-4 range", iss.ID, iss.Priority)
		}
		if iss.Labels == nil {
			t.Errorf("%s: Labels is nil; it must serialize as [] not null", iss.ID)
		}
		if iss.Created.IsZero() || iss.Updated.IsZero() {
			t.Errorf("%s: created/updated not populated", iss.ID)
		}
		if iss.Created.Location() != time.UTC {
			t.Errorf("%s: created is not UTC (%v)", iss.ID, iss.Created.Location())
		}
		// Readiness is the ready_issues view: only active issues qualify.
		if iss.Ready && iss.Status != queries.StatusOpen {
			t.Errorf("%s: ready but status is %q; ready must be a subset of open", iss.ID, iss.Status)
		}
		if iss.Ready {
			ready++
			if iss.DepCounts.BlockedBy != 0 {
				t.Errorf("%s: ready but blockedBy=%d", iss.ID, iss.DepCounts.BlockedBy)
			}
		}
		if iss.Type == "epic" {
			epics++
		}
		if iss.Parent != nil {
			withParent++
			if iss.Parent.ID == "" || iss.Parent.Title == "" {
				t.Errorf("%s: parent ref is incomplete: %+v", iss.ID, iss.Parent)
			}
		}
		if len(iss.Labels) > 0 {
			withLabels++
		}
		if iss.ChildStats != nil {
			if iss.ChildStats.Total <= 0 {
				t.Errorf("%s: childStats present but total=%d", iss.ID, iss.ChildStats.Total)
			}
			if iss.ChildStats.Closed > iss.ChildStats.Total {
				t.Errorf("%s: childStats closed %d > total %d", iss.ID, iss.ChildStats.Closed, iss.ChildStats.Total)
			}
		}
	}

	if epics < 1 {
		t.Error("expected at least one epic in the seeded backlog")
	}
	if withParent < 1 {
		t.Error("expected at least one issue with a parent")
	}
	if withLabels < 1 {
		t.Error("expected at least one labelled issue")
	}
	if ready < 1 {
		t.Error("expected at least one ready issue")
	}
}

func TestListIssuesFiltersAgainstSeededDB(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	t.Run("needs-operator label exists and filters", func(t *testing.T) {
		got, err := queries.ListIssues(ctx, db, queries.Filter{
			Labels:        []string{"needs-operator"},
			IncludeClosed: true,
		})
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(got.Issues) == 0 {
			t.Fatal("the seeded data should carry a needs-operator bead")
		}
		for _, iss := range got.Issues {
			if !contains(iss.Labels, "needs-operator") {
				t.Errorf("%s: returned by a needs-operator filter but labels are %v", iss.ID, iss.Labels)
			}
		}
	})

	t.Run("label AND semantics", func(t *testing.T) {
		one, err := queries.ListIssues(ctx, db, queries.Filter{Labels: []string{"phase-1"}, IncludeClosed: true})
		if err != nil {
			t.Fatal(err)
		}
		both, err := queries.ListIssues(ctx, db, queries.Filter{
			Labels: []string{"phase-1", "needs-operator"}, IncludeClosed: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if both.Total > one.Total {
			t.Errorf("AND-ing a second label widened the result: %d > %d", both.Total, one.Total)
		}
		for _, iss := range both.Issues {
			if !contains(iss.Labels, "phase-1") || !contains(iss.Labels, "needs-operator") {
				t.Errorf("%s: labels %v do not satisfy the AND filter", iss.ID, iss.Labels)
			}
		}
	})

	t.Run("status filter", func(t *testing.T) {
		got, err := queries.ListIssues(ctx, db, queries.Filter{Statuses: []string{queries.StatusInProgress}})
		if err != nil {
			t.Fatal(err)
		}
		for _, iss := range got.Issues {
			if iss.Status != queries.StatusInProgress {
				t.Errorf("%s: status %q leaked past an in_progress filter", iss.ID, iss.Status)
			}
		}
	})

	t.Run("type filter", func(t *testing.T) {
		got, err := queries.ListIssues(ctx, db, queries.Filter{Types: []string{"epic"}, IncludeClosed: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Issues) == 0 {
			t.Fatal("expected at least one epic")
		}
		for _, iss := range got.Issues {
			if iss.Type != "epic" {
				t.Errorf("%s: type %q leaked past an epic filter", iss.ID, iss.Type)
			}
		}
	})

	t.Run("priority range", func(t *testing.T) {
		max := 1
		got, err := queries.ListIssues(ctx, db, queries.Filter{PriorityMax: &max, IncludeClosed: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, iss := range got.Issues {
			if iss.Priority > 1 {
				t.Errorf("%s: priority %d leaked past a P<=1 filter", iss.ID, iss.Priority)
			}
		}
	})

	t.Run("closed hidden by default and shown on request", func(t *testing.T) {
		open, err := queries.ListIssues(ctx, db, queries.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		all, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true})
		if err != nil {
			t.Fatal(err)
		}
		if all.Total <= open.Total {
			t.Errorf("including closed did not add rows: %d vs %d", all.Total, open.Total)
		}
	})

	t.Run("free text search", func(t *testing.T) {
		all, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(all.Issues) == 0 {
			t.Fatal("fixture: the seeded database holds no issues to search; run `make seed`")
		}
		// Search for a word taken from a real title so the test does not
		// depend on any specific seeded wording.
		word := firstWord(all.Issues[0].Title)
		if word == "" {
			t.Fatalf("fixture: no title word of 4+ characters in %q, so free-text search cannot be exercised; run `make seed`", all.Issues[0].Title)
		}
		got, err := queries.ListIssues(ctx, db, queries.Filter{Text: word, IncludeClosed: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Issues) == 0 {
			t.Errorf("searching for %q from an existing title returned nothing", word)
		}
		for _, iss := range got.Issues {
			hay := strings.ToLower(iss.Title + " " + iss.ID)
			if !strings.Contains(hay, strings.ToLower(word)) {
				t.Errorf("%s: neither title %q nor id contains %q", iss.ID, iss.Title, word)
			}
		}
	})

	t.Run("free text reaches a row outside the first default page", func(t *testing.T) {
		// The command palette's whole job is finding a bead by name, and a
		// bead does not stop existing because it sorts below row 200
		// (loom-nlm). Search is a lookup over the project, not over page one.
		full, err := queries.ListIssues(ctx, db, queries.Filter{
			IncludeClosed: true,
			Limit:         queries.MaxLimit,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(full.Issues) == 0 {
			t.Fatal("fixture: the seeded database holds no issues to search; run `make seed`")
		}
		// The deepest row this page can name: past the default page whenever
		// the database is large enough to have one.
		deepest := full.Issues[len(full.Issues)-1]
		if len(full.Issues) <= queries.DefaultLimit {
			t.Logf("seeded database holds %d rows, at or under DefaultLimit %d: "+
				"this case still proves id lookup, not depth", len(full.Issues), queries.DefaultLimit)
		}

		got, err := queries.ListIssues(ctx, db, queries.Filter{
			Text:          deepest.ID,
			IncludeClosed: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, iss := range got.Issues {
			if iss.ID == deepest.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("searching for id %q (default-sort row %d of %d) returned %d rows, none of them it",
				deepest.ID, len(full.Issues), full.Total, len(got.Issues))
		}
	})

	t.Run("paging is stable", func(t *testing.T) {
		full, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if len(full.Issues) < 3 {
			t.Fatalf("fixture: only %d rows in the seeded database, need 3+ to exercise paging; run `make seed`", len(full.Issues))
		}
		page1, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Limit: 2, Offset: 0})
		if err != nil {
			t.Fatal(err)
		}
		page2, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Limit: 2, Offset: 2})
		if err != nil {
			t.Fatal(err)
		}
		if page1.Total != full.Total || page2.Total != full.Total {
			t.Errorf("total changed across pages: %d, %d, want %d", page1.Total, page2.Total, full.Total)
		}
		for i, iss := range append(page1.Issues, page2.Issues...) {
			if i < len(full.Issues) && iss.ID != full.Issues[i].ID {
				t.Errorf("paged row %d = %s, want %s", i, iss.ID, full.Issues[i].ID)
			}
		}
	})

	t.Run("sort orders", func(t *testing.T) {
		for _, sort := range []string{queries.SortPriority, queries.SortCreated, queries.SortUpdated} {
			got, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Sort: sort})
			if err != nil {
				t.Fatalf("sort %s: %v", sort, err)
			}
			if len(got.Issues) == 0 {
				t.Fatalf("sort %s returned nothing", sort)
			}
			if sort == queries.SortPriority {
				for i := 1; i < len(got.Issues); i++ {
					if got.Issues[i-1].Priority > got.Issues[i].Priority {
						t.Errorf("priority sort out of order at %d: %d then %d",
							i, got.Issues[i-1].Priority, got.Issues[i].Priority)
					}
				}
			}
		}
	})
}

func TestGetIssueAgainstSeededDB(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	list, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Limit: 500})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list.Issues) == 0 {
		t.Fatal("no issues to fetch")
	}

	var sawParent, sawChildren, sawBlockedBy, sawBlocks, sawDiscovered, sawEvents, sawComments int
	var skipped int

	for _, summary := range list.Issues {
		// Scratch beads belong to a concurrently running write test: they are
		// created, mutated and deleted while this loop runs, so cross-checking
		// two queries against them proves nothing.
		if loomtest.IsScratch(summary.Title) {
			skipped++
			continue
		}
		d, err := queries.GetIssue(ctx, db, summary.ID)
		if errors.Is(err, queries.ErrNotFound) {
			// The seeded db is live: agents (and Loom's own write tests) create
			// and delete beads while this runs. A row that vanished between the
			// list and the detail query is not a bug.
			skipped++
			continue
		}
		if err != nil {
			t.Fatalf("GetIssue(%s): %v", summary.ID, err)
		}

		// Same reasoning for a row that was edited in between: the two queries
		// saw different versions, so cross-checking them proves nothing.
		if !d.Updated.Equal(summary.Updated) {
			skipped++
			continue
		}

		if d.ID != summary.ID || d.Title != summary.Title {
			t.Errorf("%s: detail disagrees with the list row", summary.ID)
		}
		if d.Ready != summary.Ready {
			t.Errorf("%s: ready = %v in detail, %v in list", summary.ID, d.Ready, summary.Ready)
		}
		if d.DepCounts != summary.DepCounts {
			t.Errorf("%s: depCounts = %+v in detail, %+v in list", summary.ID, d.DepCounts, summary.DepCounts)
		}

		// The dep lists must agree with the counts computed by the list query.
		if len(d.Deps.Blocks) != d.DepCounts.Blocks {
			t.Errorf("%s: %d blocks refs but depCounts.blocks=%d",
				summary.ID, len(d.Deps.Blocks), d.DepCounts.Blocks)
		}
		if len(d.Deps.BlockedBy) < d.DepCounts.BlockedBy {
			t.Errorf("%s: %d blockedBy refs but depCounts.blockedBy=%d (counts unresolved only, so refs >= count)",
				summary.ID, len(d.Deps.BlockedBy), d.DepCounts.BlockedBy)
		}
		if d.ChildStats != nil && len(d.Deps.Children) < d.ChildStats.Total {
			t.Errorf("%s: %d children refs but childStats.total=%d",
				summary.ID, len(d.Deps.Children), d.ChildStats.Total)
		}

		// deps.parent and the summary parent must be the same issue.
		if (d.Deps.Parent == nil) != (d.Parent == nil) {
			t.Errorf("%s: deps.parent and parent disagree on existence", summary.ID)
		}
		if d.Deps.Parent != nil && d.Parent != nil && d.Deps.Parent.ID != d.Parent.ID {
			t.Errorf("%s: deps.parent %s != parent %s", summary.ID, d.Deps.Parent.ID, d.Parent.ID)
		}

		for _, group := range [][]queries.IssueRef{d.Deps.Blocks, d.Deps.BlockedBy, d.Deps.Children, d.Deps.DiscoveredFrom} {
			for _, ref := range group {
				if ref.ID == "" || ref.Title == "" || ref.Status == "" {
					t.Errorf("%s: incomplete IssueRef %+v", summary.ID, ref)
				}
			}
		}

		if d.Deps.Parent != nil {
			sawParent++
		}
		sawChildren += len(d.Deps.Children)
		sawBlockedBy += len(d.Deps.BlockedBy)
		sawBlocks += len(d.Deps.Blocks)
		sawDiscovered += len(d.Deps.DiscoveredFrom)
		sawEvents += len(d.Events)
		sawComments += len(d.Comments)

		for _, e := range d.Events {
			if e.Type == "" {
				t.Errorf("%s: event with empty type", summary.ID)
			}
			if e.Created.IsZero() {
				t.Errorf("%s: event with zero timestamp", summary.ID)
			}
		}
		for _, c := range d.Comments {
			if c.ID == "" || c.Text == "" {
				t.Errorf("%s: incomplete comment %+v", summary.ID, c)
			}
		}
		if d.Status == queries.StatusClosed && d.Closed == nil {
			t.Errorf("%s: closed issue has no closed timestamp", summary.ID)
		}
	}

	if skipped > 0 {
		t.Logf("skipped %d of %d issues that changed under the test", skipped, len(list.Issues))
	}
	if skipped == len(list.Issues) {
		t.Skip("every issue changed mid-test; the fixture is too busy to assert against")
	}

	// The seed models a realistic backlog, so each dependency kind should be
	// represented somewhere.
	if sawParent == 0 {
		t.Error("no parent-child relationship found in the seeded data")
	}
	if sawChildren == 0 {
		t.Error("no children found")
	}
	if sawBlockedBy == 0 || sawBlocks == 0 {
		t.Errorf("no blocking deps found (blockedBy=%d blocks=%d)", sawBlockedBy, sawBlocks)
	}
	if sawDiscovered == 0 {
		t.Error("no discovered-from provenance found")
	}
	if sawEvents == 0 {
		t.Error("no history events found")
	}
	if sawComments == 0 {
		t.Error("no comments found")
	}
}

func TestGetIssueProseFields(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	list, err := queries.ListIssues(ctx, db, queries.Filter{Types: []string{"epic"}, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Issues) == 0 {
		t.Fatal("fixture: no epics in the seeded database, so epic-as-brief cannot be asserted; run `make seed`")
	}

	// Epic-as-brief: the epic's description carries the shared brief, so at
	// least one epic must have a non-empty description.
	var withDescription int
	for _, e := range list.Issues {
		d, err := queries.GetIssue(ctx, db, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(d.Description) != "" {
			withDescription++
		}
	}
	if withDescription == 0 {
		t.Error("no epic carries a description brief")
	}
}

func TestGetIssueNotFound(t *testing.T) {
	db := openSeeded(t)
	_, err := queries.GetIssue(context.Background(), db, "definitely-not-an-issue")
	if err == nil {
		t.Fatal("expected an error for a missing issue")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want a not-found error", err)
	}
}

func TestStatsAgainstSeededDB(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	s, err := queries.StatsFor(ctx, db)
	if err != nil {
		t.Fatalf("StatsFor: %v", err)
	}

	if len(s.ByStatus) == 0 {
		t.Fatal("byStatus is empty")
	}
	if s.ByStatus[queries.StatusOpen] == 0 {
		t.Error("expected open issues in the seeded data")
	}
	if s.Ready <= 0 {
		t.Error("expected a non-zero ready count")
	}
	if s.Ready > s.ByStatus[queries.StatusOpen] {
		t.Errorf("ready %d > open %d; ready must be a subset of open", s.Ready, s.ByStatus[queries.StatusOpen])
	}
	// Every open-but-not-ready issue falls into exactly one bucket. Asserting
	// depBlocked alone equals open-minus-ready is what review finding I2
	// identified as wrong: defer-scheduled work is not blocked by anything.
	if want := s.ByStatus[queries.StatusOpen] - s.Ready; s.DepBlocked+s.DeferScheduled != want {
		t.Errorf("depBlocked(%d) + deferScheduled(%d) = %d, want open(%d) - ready(%d) = %d",
			s.DepBlocked, s.DeferScheduled, s.DepBlocked+s.DeferScheduled,
			s.ByStatus[queries.StatusOpen], s.Ready, want)
	}
	if s.DepBlocked < 0 || s.DeferScheduled < 0 {
		t.Errorf("negative counts: depBlocked=%d deferScheduled=%d", s.DepBlocked, s.DeferScheduled)
	}
	if s.ClosedLast7d > s.ByStatus[queries.StatusClosed] {
		t.Errorf("closedLast7d %d > closed %d", s.ClosedLast7d, s.ByStatus[queries.StatusClosed])
	}

	// Stats and the list must count the same universe of issues. Both are
	// separate queries against a live db, so retry a couple of times before
	// calling a disagreement a bug rather than a concurrent write.
	var agreed bool
	var lastStats, lastList int
	for attempt := 0; attempt < 3 && !agreed; attempt++ {
		st, err := queries.StatsFor(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		all, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Limit: queries.MaxLimit})
		if err != nil {
			t.Fatal(err)
		}
		lastStats, lastList = st.Health().Total, all.Total
		agreed = lastStats == lastList
	}
	if !agreed {
		t.Errorf("health.total = %d but the list reports %d across 3 attempts; stats and list must agree",
			lastStats, lastList)
	}

	h := s.Health()
	if h.Blocked != s.DepBlocked {
		t.Errorf("health.blocked = %d, want depBlocked %d", h.Blocked, s.DepBlocked)
	}
	if h.Ready != s.Ready {
		t.Errorf("health.ready = %d, want %d", h.Ready, s.Ready)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func firstWord(s string) string {
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, "[](){}.,:;")
		if len(f) >= 4 {
			return f
		}
	}
	return ""
}
