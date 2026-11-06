package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DoltStatus is the per-project sync state shown in the UI's sync chip.
type DoltStatus struct {
	// Dirty means there are uncommitted changes in the working set.
	Dirty bool `json:"dirty"`
	// Ahead and Behind are omitted entirely when there is no remote to compare
	// against, rather than reported as a misleading zero.
	Ahead  *int `json:"ahead,omitempty"`
	Behind *int `json:"behind,omitempty"`
	// Remote is the remote name, or nil when none is configured.
	Remote *string `json:"remote"`
	// Branch is the active branch.
	Branch string `json:"branch"`
	// LastCommit is nil only if the database has no commits at all.
	LastCommit *DoltCommit `json:"lastCommit"`
}

// DoltCommit identifies one dolt commit.
type DoltCommit struct {
	Hash    string    `json:"hash"`
	Message string    `json:"message"`
	When    time.Time `json:"when"`
}

// safeRef guards branch and remote names before they are interpolated into a
// dolt_log() argument. Dolt's table functions do not accept bind parameters for
// their revision argument, so the names — which come from the database itself,
// not from user input — are validated rather than escaped.
var safeRef = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// DoltRefs renders every local and remote-tracking branch head as one string.
// DoltStatus reads commits and remote refs, which dolt_hashof_db() does not
// cover: a push or fetch moves a remote ref and leaves the data hash alone.
// Keying a cached DoltStatus on these refs as well keeps it exact.
func DoltRefs(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT name, hash FROM dolt_branches
UNION ALL
SELECT CONCAT('remotes/', name), hash FROM dolt_remote_branches
ORDER BY 1`)
	if err != nil {
		return "", fmt.Errorf("dolt refs: %w", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			return "", fmt.Errorf("scan dolt ref: %w", err)
		}
		b.WriteString(name + "=" + hash + ";")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("dolt refs: %w", err)
	}
	return b.String(), nil
}

// DoltStatusFor reads a project's sync state.
//
// Everything comes from dolt system tables. Note that `bd dolt status` reports
// server lifecycle (PID, port, data dir) and carries no ahead/behind
// information in bd 1.1.2, so it is not used here.
func DoltStatusFor(ctx context.Context, db *sql.DB) (*DoltStatus, error) {
	out := &DoltStatus{}

	var dirtyCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dolt_status").Scan(&dirtyCount); err != nil {
		return nil, fmt.Errorf("dolt status: %w", err)
	}
	out.Dirty = dirtyCount > 0

	if err := db.QueryRowContext(ctx, "SELECT active_branch()").Scan(&out.Branch); err != nil {
		return nil, fmt.Errorf("dolt active branch: %w", err)
	}

	commit, err := lastCommit(ctx, db)
	if err != nil {
		return nil, err
	}
	out.LastCommit = commit

	remote, err := firstRemote(ctx, db)
	if err != nil {
		return nil, err
	}
	if remote == "" {
		// No remote configured: remote is null and ahead/behind are omitted.
		return out, nil
	}
	out.Remote = &remote

	// Ahead/behind need the remote tracking branch to exist locally. It will
	// not until something has fetched, so treat a missing ref as "unknown"
	// rather than as zero.
	trackingRef := "remotes/" + remote + "/" + out.Branch
	exists, err := remoteBranchExists(ctx, db, trackingRef)
	if err != nil {
		return nil, err
	}
	if !exists {
		return out, nil
	}

	if !safeRef.MatchString(trackingRef) || !safeRef.MatchString(out.Branch) {
		// Refuse to build a revision expression from a name we cannot vouch for.
		return out, nil
	}

	ahead, err := countCommits(ctx, db, trackingRef+".."+out.Branch)
	if err != nil {
		return nil, err
	}
	behind, err := countCommits(ctx, db, out.Branch+".."+trackingRef)
	if err != nil {
		return nil, err
	}
	out.Ahead, out.Behind = &ahead, &behind
	return out, nil
}

func lastCommit(ctx context.Context, db *sql.DB) (*DoltCommit, error) {
	var c DoltCommit
	err := db.QueryRowContext(ctx,
		"SELECT commit_hash, message, date FROM dolt_log ORDER BY date DESC LIMIT 1").
		Scan(&c.Hash, &c.Message, &c.When)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dolt log: %w", err)
	}
	c.When = c.When.UTC()
	return &c, nil
}

// firstRemote returns the remote to compare against, preferring origin.
func firstRemote(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM dolt_remotes ORDER BY name")
	if err != nil {
		return "", fmt.Errorf("dolt remotes: %w", err)
	}
	defer rows.Close()

	var first string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", fmt.Errorf("scan dolt remote: %w", err)
		}
		if name == "origin" {
			return name, nil
		}
		if first == "" {
			first = name
		}
	}
	return first, rows.Err()
}

func remoteBranchExists(ctx context.Context, db *sql.DB, ref string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dolt_remote_branches WHERE name = ?", ref).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("dolt remote branches: %w", err)
	}
	return n > 0, nil
}

// countCommits counts commits in a two-dot revision range such as
// "remotes/origin/main..main".
func countCommits(ctx context.Context, db *sql.DB, revRange string) (int, error) {
	var n int
	// dolt_log()'s argument cannot be a bind parameter; revRange is built only
	// from names validated against safeRef.
	q := fmt.Sprintf("SELECT COUNT(*) FROM dolt_log('%s')", revRange)
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("dolt log %s: %w", revRange, err)
	}
	return n, nil
}
