package bdexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrQuerySyntax is returned when bd rejects the query expression. The API maps
// it to 400 with bd's own message, since it is the user's expression that is
// wrong.
var ErrQuerySyntax = errors.New("bd query syntax error")

// QueryResult is one issue from `bd query --json`.
//
// Field names are bd's own snake_case JSON, captured from the real binary. Only
// what Loom maps into its Issue shape is modelled; everything else in bd's
// output is ignored rather than fought with.
type QueryResult struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	Priority    int      `json:"priority"`
	IssueType   string   `json:"issue_type"`
	Assignee    string   `json:"assignee"`
	Owner       string   `json:"owner"`
	Estimated   *int     `json:"estimated_minutes"`
	Labels      []string `json:"labels"`
	Description string   `json:"description"`

	CreatedAt  *time.Time `json:"created_at"`
	UpdatedAt  *time.Time `json:"updated_at"`
	ClosedAt   *time.Time `json:"closed_at"`
	DueAt      *time.Time `json:"due_at"`
	DeferUntil *time.Time `json:"defer_until"`
}

// queryErrorEnvelope is what bd emits on a parse failure.
//
// Verified against bd 1.1.2: a malformed expression produces
// `{"error":"parsing query: ...","schema_version":1}` on **stdout**, with exit
// status 1 and nothing on stderr.
//
// The envelope is checked before the exit status regardless. The useful message
// lives in the payload, not in the process error, so detecting it first is what
// turns a bad expression into a 400 carrying bd's own words rather than a
// generic "exit status 1".
//
// (An earlier revision of this comment claimed exit 0. That came from reading
// $? after a shell pipeline, which reports the last command's status, not bd's.)
type queryErrorEnvelope struct {
	Error         string `json:"error"`
	SchemaVersion int    `json:"schema_version"`
}

// Query runs `bd query <expr> --json` and returns the matching issues.
//
// bd query is read-only, but it still goes through the Runner so it is
// mockable like every other verb.
func (c *Client) Query(ctx context.Context, workdir, expr string) ([]QueryResult, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, fmt.Errorf("%w: empty query", ErrQuerySyntax)
	}
	// The expression is a POSITIONAL argument (`bd query <expr> --json`), so a
	// leading dash really would be read as a flag here.
	if err := checkPositionalText("query", "expression", expr); err != nil {
		return nil, err
	}

	args := []string{"query", expr, "--json"}
	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)

	// bd reports a bad expression in-band; check that before the exit status.
	if msg := queryErrorMessage(stdout); msg != "" {
		return nil, fmt.Errorf("%w: %s", ErrQuerySyntax, msg)
	}
	if err != nil {
		return nil, newBdError(args, stdout, stderr, err)
	}

	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" || trimmed == "null" {
		return []QueryResult{}, nil
	}

	var out []QueryResult
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, fmt.Errorf("%w: bd query returned unparseable JSON: %v", ErrQuerySyntax, err)
	}
	if out == nil {
		out = []QueryResult{}
	}
	return out, nil
}

// queryErrorMessage extracts bd's in-band error message, or "" when the output
// is a normal result.
func queryErrorMessage(stdout string) string {
	trimmed := strings.TrimSpace(stdout)
	if !strings.HasPrefix(trimmed, "{") {
		// A successful result is a JSON array.
		return ""
	}
	var env queryErrorEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return ""
	}
	return env.Error
}
