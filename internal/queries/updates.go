package queries

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// MaxExistenceIDs bounds one ExistingIssueIDs round trip. A year of weekly
// updates cites a few hundred beads; the cap stops a pathological body from
// building an unbounded IN list.
const MaxExistenceIDs = 500

// ExistingIssueIDs returns the subset of ids that name a real issue, sorted.
//
// It exists so prose can be linkified honestly: the tokens harvested from an
// update body are only id-*shaped*, and rendering "read-only" as a dead link to
// an issue that never existed is worse than rendering it as plain text.
func ExistingIssueIDs(ctx context.Context, db *sql.DB, ids []string) ([]string, error) {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
		if len(unique) == MaxExistenceIDs {
			break
		}
	}
	if len(unique) == 0 {
		return []string{}, nil
	}

	args := make([]any, len(unique))
	for i, id := range unique {
		args[i] = id
	}
	query := "SELECT id FROM issues WHERE id IN (" +
		strings.TrimSuffix(strings.Repeat("?, ", len(unique)), ", ") + ")"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("check cited issue ids: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan cited issue id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("check cited issue ids: %w", err)
	}
	sort.Strings(out)
	return out, nil
}
