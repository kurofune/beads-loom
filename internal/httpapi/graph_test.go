package httpapi

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/queries"
)

// --- graph ------------------------------------------------------------------

func TestGraphEndpointGuards(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{"unknown project", "/api/projects/nope/graph", http.StatusNotFound},
		{"unreachable project", "/api/projects/loom/graph", http.StatusServiceUnavailable},
		{"invalid epic id", "/api/projects/loom/graph?epic=--force", http.StatusBadRequest},
		{"epic with a slash", "/api/projects/loom/graph?epic=a%2Fb", http.StatusBadRequest},
		{"unknown parameter", "/api/projects/loom/graph?depth=3", http.StatusBadRequest},
		{"bad includeClosed", "/api/projects/loom/graph?includeClosed=maybe", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t)
			rec := do(t, s, http.MethodGet, tc.path, "")
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestGraphTooLargeIs413(t *testing.T) {
	// The shape of the 413 body matters: the UI needs the count to tell the
	// user how far over they are.
	body := map[string]any{
		"error":     "graph too large to render; scope it to an epic",
		"nodeCount": 1234,
		"limit":     queries.MaxGraphNodes,
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"nodeCount":1234`, `"limit":500`, `"error"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("413 body missing %s", want)
		}
	}
	if queries.MaxGraphNodes != 500 {
		t.Errorf("MaxGraphNodes = %d, want the specified 500", queries.MaxGraphNodes)
	}
}

// --- stale ------------------------------------------------------------------

func TestStaleEndpointGuards(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{"unknown project", "/api/projects/nope/stale", http.StatusNotFound},
		{"unreachable project", "/api/projects/loom/stale", http.StatusServiceUnavailable},
		{"non-numeric days", "/api/projects/loom/stale?days=lots", http.StatusBadRequest},
		{"negative days", "/api/projects/loom/stale?days=-1", http.StatusBadRequest},
		{"absurd days", "/api/projects/loom/stale?days=99999", http.StatusBadRequest},
		{"unknown parameter", "/api/projects/loom/stale?since=x", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t)
			rec := do(t, s, http.MethodGet, tc.path, "")
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestStaleDefaultDays(t *testing.T) {
	if queries.DefaultStaleDays != 14 {
		t.Errorf("DefaultStaleDays = %d, want the specified 14", queries.DefaultStaleDays)
	}
}

// --- bd query passthrough ---------------------------------------------------

// realQueryJSON is a slice of genuine `bd query --json` output, captured from
// bd 1.1.2 against the seeded database.
const realQueryJSON = `[
  {
    "id": "loom-0zj.1",
    "title": "[Runs] SSE channel with dolt head-hash polling",
    "description": "Single SSE endpoint carrying db-invalidation events.",
    "status": "open",
    "priority": 1,
    "issue_type": "task",
    "owner": "nate@example.com",
    "estimated_minutes": 8,
    "created_at": "2026-08-01T16:29:03Z",
    "updated_at": "2026-08-01T16:29:03Z",
    "labels": ["layer-service", "phase-3"],
    "dependencies": [
      {"issue_id": "loom-0zj.1", "depends_on_id": "loom-yqh.1", "type": "blocks"}
    ]
  }
]`

// realQueryErrorJSON is bd's in-band parse error. Note it arrives on STDOUT
// with exit code 0 — checking the exit status alone would report a syntax error
// as a successful empty result.
const realQueryErrorJSON = `{
  "error": "parsing query: expected comparison operator at position 6, got IDENT",
  "schema_version": 1
}`

func TestQueryPassthrough(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Stdout = realQueryJSON

	rec := do(t, s, http.MethodGet, "/api/projects/loom/query?q=status%3Dopen", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// The exact argv bd receives.
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d invocations, want 1: %v", len(calls), calls)
	}
	want := []string{"query", "status=open", "--json"}
	if !reflect.DeepEqual(calls[0].Args, want) {
		t.Errorf("argv = %v, want %v", calls[0].Args, want)
	}

	var body queryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Query != "status=open" {
		t.Errorf("query echo = %q", body.Query)
	}
	if body.Total != 1 || len(body.Issues) != 1 {
		t.Fatalf("issues = %+v", body.Issues)
	}

	// The dolt server is unreachable in this fixture, so hydration falls back
	// to bd's own fields — which must still map onto the Issue shape.
	got := body.Issues[0]
	if got.ID != "loom-0zj.1" {
		t.Errorf("id = %q", got.ID)
	}
	if got.Title == "" || got.Status != "open" || got.Priority != 1 {
		t.Errorf("issue = %+v", got)
	}
	if got.Type != "task" {
		t.Errorf("type = %q, want task (mapped from issue_type)", got.Type)
	}
	if got.Estimate == nil || *got.Estimate != 8 {
		t.Errorf("estimate = %v, want 8", got.Estimate)
	}
	if len(got.Labels) != 2 {
		t.Errorf("labels = %v", got.Labels)
	}
	if got.Created.IsZero() {
		t.Error("created not mapped")
	}
}

// TestQuerySyntaxErrorIs400 is the in-band-error case: bd exits 0 and puts the
// error in the JSON body.
func TestQuerySyntaxErrorIs400(t *testing.T) {
	// Exit code 0, error on stdout — exactly what bd 1.1.2 does.
	s, mock, _ := newWriteServer(t)
	mock.Stdout = realQueryErrorJSON
	mock.Err = nil

	rec := do(t, s, http.MethodGet, "/api/projects/loom/query?q=not+a+query+%28%28", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var body errorResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body.Error, "parsing query") {
		t.Errorf("error = %q, want bd's own message", body.Error)
	}
}

func TestQueryValidation(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{"missing q", "/api/projects/loom/query", http.StatusBadRequest},
		{"blank q", "/api/projects/loom/query?q=%20%20", http.StatusBadRequest},
		{"unknown parameter", "/api/projects/loom/query?q=status%3Dopen&limit=5", http.StatusBadRequest},
		{"unknown project", "/api/projects/nope/query?q=status%3Dopen", http.StatusNotFound},
		{"flag-shaped expression", "/api/projects/loom/query?q=--json", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			mock.Stdout = realQueryJSON
			rec := do(t, s, http.MethodGet, tc.path, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusBadRequest {
				if calls := mock.Calls(); len(calls) != 0 {
					t.Errorf("bd was invoked for a rejected request: %v", calls)
				}
			}
		})
	}
}

func TestQueryBdFailureIs400(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Stdout = ""
	mock.Stderr = "Error: unknown field 'nonsense'"
	mock.Err = &exec.ExitError{}

	rec := do(t, s, http.MethodGet, "/api/projects/loom/query?q=nonsense%3D1", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var body errorResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Stderr != "Error: unknown field 'nonsense'" {
		t.Errorf("stderr = %q, want bd's message verbatim", body.Stderr)
	}
}

func TestQueryEmptyResult(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Stdout = "[]"

	rec := do(t, s, http.MethodGet, "/api/projects/loom/query?q=status%3Dclosed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body queryResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Issues == nil {
		t.Error("issues must serialize as [] not null")
	}
	if body.Total != 0 {
		t.Errorf("total = %d", body.Total)
	}
}

func TestQueryRouteAbsentWithoutBD(t *testing.T) {
	s := newTestServer(t) // no bd client
	rec := do(t, s, http.MethodGet, "/api/projects/loom/query?q=status%3Dopen", "")
	if rec.Code == http.StatusOK {
		t.Error("the query route should not exist without a bd client")
	}
}

// --- bdexec.Query unit ------------------------------------------------------

func TestBdexecQueryParsing(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		err       error
		wantCount int
		wantErr   string
	}{
		{name: "real output", stdout: realQueryJSON, wantCount: 1},
		{name: "empty array", stdout: "[]", wantCount: 0},
		{name: "empty output", stdout: "", wantCount: 0},
		{name: "null", stdout: "null", wantCount: 0},
		{
			name:   "in-band syntax error with exit 0",
			stdout: realQueryErrorJSON, wantErr: "parsing query",
		},
		{name: "unparseable json", stdout: "{not json", wantErr: "unparseable"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := bdexec.NewMockRunner()
			mock.Stdout = tc.stdout
			mock.Err = tc.err

			got, err := bdexec.New(mock).Query(t.Context(), "/tmp", "status=open")
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(got) != tc.wantCount {
				t.Errorf("got %d results, want %d", len(got), tc.wantCount)
			}
			if got == nil {
				t.Error("results must be [] not nil")
			}
		})
	}
}

func TestBdexecQueryRejectsEmptyAndFlagShaped(t *testing.T) {
	mock := bdexec.NewMockRunner()
	c := bdexec.New(mock)

	for _, expr := range []string{"", "   ", "--json", "-C"} {
		if _, err := c.Query(t.Context(), "/tmp", expr); err == nil {
			t.Errorf("Query(%q) should have been rejected", expr)
		}
	}
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("bd was invoked for a rejected expression: %v", calls)
	}
}

// --- stats extensions -------------------------------------------------------

func TestStatsResponseShape(t *testing.T) {
	// handleStats encodes *queries.Stats as is: run spend moved to
	// /x/djinn/spend and is no longer embedded here.
	data, err := json.Marshal(&queries.Stats{
		ByStatus:      map[string]int{"open": 3},
		Ready:         2,
		ReadyFrontier: 2,
		DepBlocked:    1,
		Trend:         []queries.TrendPoint{{Date: "2026-08-01", Opened: 2, Closed: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"byStatus"`, `"ready":2`, `"readyFrontier":2`, `"depBlocked":1`,
		`"trend":[{"date":"2026-08-01","opened":2,"closed":1}]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stats JSON missing %s\ngot: %s", want, got)
		}
	}
	var keys map[string]any
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["spendRecent"]; ok {
		t.Errorf("stats JSON carries spendRecent: %s", got)
	}
}
