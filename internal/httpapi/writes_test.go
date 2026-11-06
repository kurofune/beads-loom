package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
)

// newWriteServer builds a server with a mocked bd runner over a project whose
// dolt server does not exist. Writes therefore succeed at the bd layer and fail
// at the read-back, which is exactly the seam these tests want: they assert the
// bd invocation, not the response body.
func newWriteServer(t *testing.T) (*Server, *bdexec.MockRunner, string) {
	t.Helper()
	workdir := t.TempDir()
	cfg := &config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "loom", Workdir: workdir}},
	}
	reg := registry.New(cfg)
	t.Cleanup(func() { reg.Close() })

	mock := bdexec.NewMockRunner()
	mock.Stdout = "loom-new\n"
	s := New(Options{Registry: reg, BD: bdexec.New(mock), BDVersion: "bd version 1.1.2 (test)"})
	return s, mock, workdir
}

// do issues a request the way a correct client would: writes always carry
// Content-Type: application/json, even when the body is empty, because the CSRF
// guard requires it (review finding B2).
func do(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	if isWriteMethod(method) {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, r)
	return rec
}

// TestWriteEndpointArgv asserts the exact bd invocation each endpoint produces.
// This is the contract that matters: a wrong flag silently corrupts a backlog.
func TestWriteEndpointArgv(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   []string
		stdin  string
	}{
		{
			name:   "patch status",
			method: http.MethodPatch,
			path:   "/api/projects/loom/issues/loom-1",
			body:   `{"status":"in_progress"}`,
			want:   []string{"update", "loom-1", "--status", "in_progress"},
		},
		{
			name:   "patch priority zero",
			method: http.MethodPatch,
			path:   "/api/projects/loom/issues/loom-1",
			body:   `{"priority":0}`,
			want:   []string{"update", "loom-1", "--priority", "0"},
		},
		{
			name:   "patch labels add and remove",
			method: http.MethodPatch,
			path:   "/api/projects/loom/issues/loom-1",
			body:   `{"labelsAdd":["phase-2"],"labelsRemove":["stale"]}`,
			want:   []string{"update", "loom-1", "--add-label", "phase-2", "--remove-label", "stale"},
		},
		{
			name:   "patch clears assignee",
			method: http.MethodPatch,
			path:   "/api/projects/loom/issues/loom-1",
			body:   `{"assignee":""}`,
			want:   []string{"update", "loom-1", "--assignee", ""},
		},
		{
			name:   "patch dates",
			method: http.MethodPatch,
			path:   "/api/projects/loom/issues/loom-1",
			body:   `{"defer":"2026-09-01","due":"+2d"}`,
			want:   []string{"update", "loom-1", "--defer", "2026-09-01", "--due", "+2d"},
		},
		{
			name:   "patch prose fields",
			method: http.MethodPatch,
			path:   "/api/projects/loom/issues/loom-1",
			body:   `{"description":"why","acceptance":"one\ntwo","design":"where","notes":"context"}`,
			want: []string{"update", "loom-1",
				"--description", "why",
				"--acceptance", "one\ntwo",
				"--design", "where",
				"--notes", "context"},
		},
		{
			name:   "patch clears a prose field with an empty string",
			method: http.MethodPatch,
			path:   "/api/projects/loom/issues/loom-1",
			body:   `{"notes":""}`,
			want:   []string{"update", "loom-1", "--notes", ""},
		},
		{
			name:   "close with reason",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/loom-1/close",
			body:   `{"reason":"shipped"}`,
			want:   []string{"close", "loom-1", "--reason", "shipped"},
		},
		{
			name:   "close with force",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/loom-1/close",
			body:   `{"reason":"shipped","force":true}`,
			want:   []string{"close", "loom-1", "--reason", "shipped", "--force"},
		},
		{
			name:   "reopen with no body",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/loom-1/reopen",
			body:   "",
			want:   []string{"reopen", "loom-1"},
		},
		{
			name:   "reopen with reason",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/loom-1/reopen",
			body:   `{"reason":"regressed"}`,
			want:   []string{"reopen", "loom-1", "--reason", "regressed"},
		},
		{
			name:   "comment pipes text",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/loom-1/comments",
			body:   `{"text":"a note\nwith newlines"}`,
			want:   []string{"comment", "loom-1", "--stdin"},
			stdin:  "a note\nwith newlines",
		},
		{
			name:   "bulk is one invocation",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/bulk",
			body:   `{"ids":["loom-a","loom-b"],"patch":{"priority":1}}`,
			want:   []string{"update", "loom-a", "loom-b", "--priority", "1"},
		},
		{
			name:   "bulk dedupes and trims ids",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/bulk",
			body:   `{"ids":[" loom-a ","loom-a","","loom-b"],"patch":{"status":"open"}}`,
			want:   []string{"update", "loom-a", "loom-b", "--status", "open"},
		},
		{
			name:   "bulk patch carries prose fields",
			method: http.MethodPost,
			path:   "/api/projects/loom/issues/bulk",
			body:   `{"ids":["loom-a","loom-b"],"patch":{"notes":"triaged 2026-08-02"}}`,
			want:   []string{"update", "loom-a", "loom-b", "--notes", "triaged 2026-08-02"},
		},
		{
			name:   "dep add",
			method: http.MethodPost,
			path:   "/api/projects/loom/deps",
			body:   `{"from":"loom-2","to":"loom-1","type":"blocks"}`,
			want:   []string{"dep", "add", "loom-2", "loom-1", "--type", "blocks"},
		},
		{
			name:   "dep remove",
			method: http.MethodDelete,
			path:   "/api/projects/loom/deps",
			body:   `{"from":"loom-2","to":"loom-1"}`,
			want:   []string{"dep", "remove", "loom-2", "loom-1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, workdir := newWriteServer(t)
			do(t, s, tc.method, tc.path, tc.body)

			calls := mock.Calls()
			if len(calls) == 0 {
				t.Fatal("no bd invocation was made")
			}
			if !reflect.DeepEqual(calls[0].Args, tc.want) {
				t.Errorf("argv mismatch\n got: %v\nwant: %v", calls[0].Args, tc.want)
			}
			if calls[0].Workdir != workdir {
				t.Errorf("workdir = %q, want the project's workdir %q", calls[0].Workdir, workdir)
			}
			if calls[0].Stdin != tc.stdin {
				t.Errorf("stdin = %q, want %q", calls[0].Stdin, tc.stdin)
			}
		})
	}
}

func TestCreateEndpointArgv(t *testing.T) {
	s, mock, _ := newWriteServer(t)

	body := `{
	  "title":"[Scratch] created via api",
	  "type":"task",
	  "priority":3,
	  "description":"desc",
	  "acceptance":"one\ntwo",
	  "design":"where to look",
	  "notes":"context",
	  "parent":"loom-3ft",
	  "labels":["phase-2"],
	  "estimate":15,
	  "deps":[{"id":"loom-1","type":"discovered-from"}]
	}`
	do(t, s, http.MethodPost, "/api/projects/loom/issues", body)

	calls := mock.Calls()
	if len(calls) != 3 {
		t.Fatalf("got %d invocations, want 3 (create + tag + dep): %v", len(calls), calls)
	}
	want := []string{
		"create", "--title", "[Scratch] created via api",
		"--type", "task", "--priority", "3",
		"--description", "desc",
		"--acceptance", "one\ntwo",
		"--design", "where to look",
		"--notes", "context",
		"--parent", "loom-3ft",
		"--estimate", "15",
		"--silent",
	}
	if !reflect.DeepEqual(calls[0].Args, want) {
		t.Errorf("create argv mismatch\n got: %v\nwant: %v", calls[0].Args, want)
	}
	if !reflect.DeepEqual(calls[1].Args, []string{"tag", "loom-new", "phase-2"}) {
		t.Errorf("tag argv = %v", calls[1].Args)
	}
	if !reflect.DeepEqual(calls[2].Args, []string{"dep", "add", "loom-new", "loom-1", "--type", "discovered-from"}) {
		t.Errorf("dep argv = %v", calls[2].Args)
	}
}

// TestWriteValidation covers the checks Loom makes before touching bd. Each
// must be a 400 and must not reach the CLI.
func TestWriteValidation(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		wantMsg string
	}{
		{
			name: "priority out of range", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"priority":9}`,
			wantMsg: "priority must be 0-4",
		},
		{
			name: "negative priority", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"priority":-1}`,
			wantMsg: "priority must be 0-4",
		},
		{
			name: "blank title", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"title":"   "}`,
			wantMsg: "title cannot be blank",
		},
		{
			name: "negative estimate", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"estimate":-5}`,
			wantMsg: "estimate must be >= 0",
		},
		{
			name: "empty patch", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{}`,
			wantMsg: "no fields to update",
		},
		{
			name: "patch with no body", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: ``,
			wantMsg: "no fields to update",
		},
		{
			name: "unknown field is rejected", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"statuss":"open"}`,
			wantMsg: "invalid JSON body",
		},
		{
			name: "malformed json", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"status":`,
			wantMsg: "invalid JSON body",
		},
		{
			name: "oversized description", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1",
			body: `{"description":"` + strings.Repeat("x", maxProseBytes+1) + `"}`,
			wantMsg: "description must be at most",
		},
		{
			name: "oversized notes", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1",
			body: `{"notes":"` + strings.Repeat("x", maxProseBytes+1) + `"}`,
			wantMsg: "notes must be at most",
		},
		{
			name: "oversized acceptance", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1",
			body: `{"acceptance":"` + strings.Repeat("x", maxProseBytes+1) + `"}`,
			wantMsg: "acceptance must be at most",
		},
		{
			name: "oversized design", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1",
			body: `{"design":"` + strings.Repeat("x", maxProseBytes+1) + `"}`,
			wantMsg: "design must be at most",
		},
		{
			name: "create with an oversized description", method: http.MethodPost,
			path: "/api/projects/loom/issues",
			body: `{"title":"t","description":"` + strings.Repeat("x", maxProseBytes+1) + `"}`,
			wantMsg: "description must be at most",
		},
		{
			name: "create with oversized notes", method: http.MethodPost,
			path: "/api/projects/loom/issues",
			body: `{"title":"t","notes":"` + strings.Repeat("x", maxProseBytes+1) + `"}`,
			wantMsg: "notes must be at most",
		},
		{
			name: "close without a reason", method: http.MethodPost,
			path: "/api/projects/loom/issues/loom-1/close", body: `{"reason":""}`,
			wantMsg: "reason is required",
		},
		{
			name: "close with a blank reason", method: http.MethodPost,
			path: "/api/projects/loom/issues/loom-1/close", body: `{"reason":"  "}`,
			wantMsg: "reason is required",
		},
		{
			name: "close with no body at all", method: http.MethodPost,
			path: "/api/projects/loom/issues/loom-1/close", body: ``,
			wantMsg: "reason is required",
		},
		{
			name: "empty comment", method: http.MethodPost,
			path: "/api/projects/loom/issues/loom-1/comments", body: `{"text":"  "}`,
			wantMsg: "text is required",
		},
		{
			name: "bulk with no ids", method: http.MethodPost,
			path: "/api/projects/loom/issues/bulk", body: `{"ids":[],"patch":{"priority":1}}`,
			wantMsg: "at least one issue id",
		},
		{
			name: "bulk with only blank ids", method: http.MethodPost,
			path: "/api/projects/loom/issues/bulk", body: `{"ids":["  ",""],"patch":{"priority":1}}`,
			wantMsg: "at least one issue id",
		},
		{
			name: "bulk with an empty patch", method: http.MethodPost,
			path: "/api/projects/loom/issues/bulk", body: `{"ids":["loom-a"],"patch":{}}`,
			wantMsg: "no fields to update",
		},
		{
			name: "create without a title", method: http.MethodPost,
			path: "/api/projects/loom/issues", body: `{"type":"task"}`,
			wantMsg: "title is required",
		},
		{
			name: "create with a bad priority", method: http.MethodPost,
			path: "/api/projects/loom/issues", body: `{"title":"x","priority":7}`,
			wantMsg: "priority must be 0-4",
		},
		{
			name: "dep add without a target", method: http.MethodPost,
			path: "/api/projects/loom/deps", body: `{"from":"loom-1"}`,
			wantMsg: "from and to are required",
		},
		{
			name: "dep remove without a target", method: http.MethodDelete,
			path: "/api/projects/loom/deps", body: `{"to":"loom-1"}`,
			wantMsg: "from and to are required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			rec := do(t, s, tc.method, tc.path, tc.body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !strings.Contains(body.Error, tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", body.Error, tc.wantMsg)
			}
			if calls := mock.Calls(); len(calls) != 0 {
				t.Errorf("a rejected request still invoked bd: %v", calls)
			}
		})
	}
}

// TestSelfDependencyIsRejected: bdexec catches this before bd does, and it must
// surface as a 400 rather than a 422.
func TestSelfDependencyIsRejected(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	rec := do(t, s, http.MethodPost, "/api/projects/loom/deps", `{"from":"loom-1","to":"loom-1"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("self-dependency reached bd: %v", calls)
	}
}

// TestBdFailureIs422WithStderr is the contract the UI toasts against.
func TestBdFailureIs422WithStderr(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		wantError string
	}{
		{"update", http.MethodPatch, "/api/projects/loom/issues/loom-1", `{"status":"open"}`, "bd update failed"},
		{"close", http.MethodPost, "/api/projects/loom/issues/loom-1/close", `{"reason":"x"}`, "bd close failed"},
		{"reopen", http.MethodPost, "/api/projects/loom/issues/loom-1/reopen", `{}`, "bd reopen failed"},
		{"comment", http.MethodPost, "/api/projects/loom/issues/loom-1/comments", `{"text":"x"}`, "bd comment failed"},
		{"create", http.MethodPost, "/api/projects/loom/issues", `{"title":"x"}`, "bd create failed"},
		{"bulk", http.MethodPost, "/api/projects/loom/issues/bulk", `{"ids":["loom-a"],"patch":{"priority":1}}`, "bd update failed"},
		{"dep add", http.MethodPost, "/api/projects/loom/deps", `{"from":"loom-a","to":"loom-b"}`, "bd dep add failed"},
		{"dep remove", http.MethodDelete, "/api/projects/loom/deps", `{"from":"loom-a","to":"loom-b"}`, "bd dep remove failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			mock.Stdout = ""
			mock.Stderr = "Error: issue not found\n"
			mock.Err = &exec.ExitError{}

			rec := do(t, s, tc.method, tc.path, tc.body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body: %s)", rec.Code, rec.Body.String())
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error != tc.wantError {
				t.Errorf("error = %q, want %q", body.Error, tc.wantError)
			}
			if body.Stderr != "Error: issue not found" {
				t.Errorf("stderr = %q, want bd's message verbatim", body.Stderr)
			}
		})
	}
}

// TestCreatePartialFailureNamesTheBead: if create succeeds but tagging fails,
// the response must say which bead now exists.
func TestCreatePartialFailureNamesTheBead(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Handler = func(call bdexec.Call) (string, string, error) {
		if call.Args[0] == "create" {
			return "loom-orphan\n", "", nil
		}
		return "", "Error: bad label", &exec.ExitError{}
	}

	rec := do(t, s, http.MethodPost, "/api/projects/loom/issues", `{"title":"x","labels":["bad"]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var body errorResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body.Error, "loom-orphan") {
		t.Errorf("error = %q, want it to name the created bead", body.Error)
	}
}

func TestWritesMarkTheProjectWritten(t *testing.T) {
	s, _, _ := newWriteServer(t)

	before, _ := s.reg.Generation("loom")
	do(t, s, http.MethodPatch, "/api/projects/loom/issues/loom-1", `{"status":"open"}`)
	after, _ := s.reg.Generation("loom")

	if after != before+1 {
		t.Errorf("generation = %d, want %d after a successful write", after, before+1)
	}
}

func TestFailedWriteDoesNotMarkWritten(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Stderr = "Error: nope"
	mock.Err = &exec.ExitError{}

	before, _ := s.reg.Generation("loom")
	do(t, s, http.MethodPatch, "/api/projects/loom/issues/loom-1", `{"status":"open"}`)
	after, _ := s.reg.Generation("loom")

	if after != before {
		t.Errorf("generation moved from %d to %d on a failed write", before, after)
	}
}

func TestWriteToUnknownProjectIs404(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPatch, "/api/projects/nope/issues/x", `{"status":"open"}`},
		{http.MethodPost, "/api/projects/nope/issues/x/close", `{"reason":"r"}`},
		{http.MethodPost, "/api/projects/nope/issues", `{"title":"t"}`},
		{http.MethodPost, "/api/projects/nope/issues/bulk", `{"ids":["loom-a"],"patch":{"priority":1}}`},
		{http.MethodPost, "/api/projects/nope/deps", `{"from":"loom-a","to":"loom-b"}`},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := do(t, s, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("a write to an unknown project reached bd: %v", calls)
	}
}

// TestNoWriteRoutesWithoutBD: a server built without a bd client must expose no
// mutating surface at all.
func TestNoWriteRoutesWithoutBD(t *testing.T) {
	s := newTestServer(t) // built with Options.BD nil

	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPatch, "/api/projects/loom/issues/loom-1", `{"status":"open"}`},
		{http.MethodPost, "/api/projects/loom/issues/loom-1/close", `{"reason":"r"}`},
		{http.MethodPost, "/api/projects/loom/issues", `{"title":"t"}`},
		{http.MethodPost, "/api/projects/loom/deps", `{"from":"loom-a","to":"loom-b"}`},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := do(t, s, tc.method, tc.path, tc.body)
			if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
				t.Errorf("status = %d; writes must not work without bd", rec.Code)
			}
		})
	}
}

func TestBulkResponseShape(t *testing.T) {
	s, _, _ := newWriteServer(t)
	rec := do(t, s, http.MethodPost, "/api/projects/loom/issues/bulk",
		`{"ids":["loom-a","loom-b"],"patch":{"priority":1}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body bulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Updated != 2 {
		t.Errorf("updated = %d, want 2", body.Updated)
	}
	if !reflect.DeepEqual(body.IDs, []string{"loom-a", "loom-b"}) {
		t.Errorf("ids = %v", body.IDs)
	}
}

// TestProseAtCapIsAccepted pins the cap as inclusive: a field of exactly
// maxProseBytes must reach bd, so a `>` to `>=` slip cannot pass silently.
func TestProseAtCapIsAccepted(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	atCap := strings.Repeat("x", maxProseBytes)
	rec := do(t, s, http.MethodPatch, "/api/projects/loom/issues/loom-1",
		`{"description":"`+atCap+`"}`)

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("a description of exactly maxProseBytes was rejected: %s", rec.Body.String())
	}
	calls := mock.Calls()
	if len(calls) == 0 {
		t.Fatal("bd was not invoked for an at-cap prose field")
	}
	want := []string{"update", "loom-1", "--description", atCap}
	if !reflect.DeepEqual(calls[0].Args, want) {
		t.Errorf("argv mismatch: got %d args, want --description with the full at-cap value", len(calls[0].Args))
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	huge := `{"title":"` + strings.Repeat("x", maxBodyBytes+1024) + `"}`
	rec := do(t, s, http.MethodPost, "/api/projects/loom/issues", huge)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an oversized body", rec.Code)
	}
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("oversized body reached bd: %v", calls)
	}
}

func TestCORSAllowsWriteMethods(t *testing.T) {
	s, _, _ := newWriteServer(t)

	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/api/projects/loom/issues", nil)
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", method)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
				t.Errorf("preflight for %s was not allowed (origin header %q)", method, got)
			}
		})
	}
}

func TestDedupeNonEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"trims", []string{" a ", "b"}, []string{"a", "b"}},
		{"drops blanks", []string{"a", "", "   ", "b"}, []string{"a", "b"}},
		{"dedupes preserving order", []string{"b", "a", "b"}, []string{"b", "a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dedupeNonEmpty(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("dedupeNonEmpty(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestB1FlagShapedIDsAreRejected is the regression test for review finding B1.
//
// bd puts positional ids before flags and offers no `--` terminator, so an id
// beginning with `-` is eaten by bd's flag parser. `bd update` then finds no
// positional id and falls back to "the last touched issue" — mutating a bead
// the caller never named. Both cases below are the reviewer's concrete
// failures verbatim.
func TestB1FlagShapedIDsAreRejected(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			// PATCH /issues/--claim {"priority":1} would have produced
			// `update --claim --priority 1`: claim + re-prioritise the last
			// touched bead.
			name: "reviewer case 1: --claim as the URL id", method: http.MethodPatch,
			path: "/api/projects/loom/issues/--claim", body: `{"priority":1}`,
		},
		{
			// bulk ids ["--ephemeral","loom-abc"] would have produced
			// `update --ephemeral loom-abc --status open`: updates loom-abc AND
			// silently marks it ephemeral (hidden from export, TTL-compactable).
			name: "reviewer case 2: --ephemeral in bulk ids", method: http.MethodPost,
			path: "/api/projects/loom/issues/bulk",
			body: `{"ids":["--ephemeral","loom-abc"],"patch":{"status":"open"}}`,
		},
		{name: "leading dash on close", method: http.MethodPost,
			path: "/api/projects/loom/issues/-f/close", body: `{"reason":"x"}`},
		{name: "leading dash on reopen", method: http.MethodPost,
			path: "/api/projects/loom/issues/--force/reopen", body: `{}`},
		{name: "leading dash on comment", method: http.MethodPost,
			path: "/api/projects/loom/issues/--stdin/comments", body: `{"text":"x"}`},
		{name: "dep from is a flag", method: http.MethodPost,
			path: "/api/projects/loom/deps", body: `{"from":"--no-cycle-check","to":"loom-1"}`},
		{name: "dep to is a flag", method: http.MethodDelete,
			path: "/api/projects/loom/deps", body: `{"from":"loom-1","to":"-t"}`},
		{name: "create parent is a flag", method: http.MethodPost,
			path: "/api/projects/loom/issues", body: `{"title":"t","parent":"--ephemeral"}`},
		{name: "create dep is a flag", method: http.MethodPost,
			path: "/api/projects/loom/issues", body: `{"title":"t","deps":[{"id":"--force"}]}`},
		{name: "label is a flag", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"labelsAdd":["--ephemeral"]}`},
		{name: "remove-label is a flag", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1", body: `{"labelsRemove":["-x"]}`},
		{name: "create label is a flag", method: http.MethodPost,
			path: "/api/projects/loom/issues", body: `{"title":"t","labels":["--no-history"]}`},
		{name: "malformed id shape", method: http.MethodPatch,
			path: "/api/projects/loom/issues/not-an-id-shape", body: `{"priority":1}`},
		{name: "id with a slash", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom%2F..%2Fx", body: `{"priority":1}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			rec := do(t, s, tc.method, tc.path, tc.body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if calls := mock.Calls(); len(calls) != 0 {
				t.Errorf("bd was invoked with %v; validation must run first", calls)
			}
		})
	}
}

// TestB1ValidIDsStillWork guards against the validation being too strict for
// real bd id shapes.
func TestB1ValidIDsStillWork(t *testing.T) {
	for _, id := range []string{"loom-3ft", "loom-3ft.2", "loom-3ft.2.1", "djinn-ipee.8", "phoenix-qgss"} {
		t.Run(id, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			rec := do(t, s, http.MethodPatch, "/api/projects/loom/issues/"+id, `{"priority":1}`)
			if rec.Code == http.StatusBadRequest {
				t.Fatalf("valid id %q was rejected: %s", id, rec.Body.String())
			}
			calls := mock.Calls()
			if len(calls) == 0 {
				t.Fatal("bd was not invoked for a valid id")
			}
			if calls[0].Args[1] != id {
				t.Errorf("argv id = %q, want %q", calls[0].Args[1], id)
			}
		})
	}
}

// TestN2UnknownStatusDefersToBDWhenTableUnreadable is the regression test for
// review finding N2.
//
// The custom_statuses table lives in the project's dolt database. When that
// database cannot be reached, Loom cannot know whether a status is a valid
// custom one — so it must let bd be the judge rather than reject the write with
// a message ("unknown status") that may simply be false.
func TestN2UnknownStatusDefersToBDWhenTableUnreadable(t *testing.T) {
	s, mock, _ := newWriteServer(t) // project has no dolt server

	rec := do(t, s, http.MethodPatch, "/api/projects/loom/issues/loom-1",
		`{"status":"almost_done"}`)

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("status was rejected while the table was unreadable: %s", rec.Body.String())
	}
	calls := mock.Calls()
	if len(calls) == 0 {
		t.Fatal("bd was never invoked; it should be the judge here")
	}
	want := []string{"update", "loom-1", "--status", "almost_done"}
	if strings.Join(calls[0].Args, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want %v", calls[0].Args, want)
	}
}

// TestN2BuiltInStatusesNeverNeedTheTable: the seven built-ins are accepted
// without any database round trip at all.
func TestN2BuiltInStatusesAcceptedOffline(t *testing.T) {
	for _, status := range []string{
		"open", "in_progress", "blocked", "deferred", "closed", "pinned", "hooked",
	} {
		t.Run(status, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			rec := do(t, s, http.MethodPatch, "/api/projects/loom/issues/loom-1",
				`{"status":"`+status+`"}`)
			if rec.Code == http.StatusBadRequest {
				t.Fatalf("built-in status %q rejected: %s", status, rec.Body.String())
			}
			if len(mock.Calls()) == 0 {
				t.Error("bd was not invoked")
			}
		})
	}
}

// TestN3BulkClose is the regression test for review finding N3: closing many
// issues must be one bd invocation, not one per issue.
func TestN3BulkClose(t *testing.T) {
	s, mock, _ := newWriteServer(t)

	rec := do(t, s, http.MethodPost, "/api/projects/loom/issues/close",
		`{"ids":["loom-a","loom-b","loom-c"],"reason":"sprint closed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d invocations, want exactly 1: %v", len(calls), calls)
	}
	want := []string{"close", "loom-a", "loom-b", "loom-c", "--reason", "sprint closed"}
	if !reflect.DeepEqual(calls[0].Args, want) {
		t.Errorf("argv = %v, want %v", calls[0].Args, want)
	}

	var body bulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Updated != 3 {
		t.Errorf("updated = %d, want 3", body.Updated)
	}
}

func TestN3BulkCloseValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"no ids", `{"ids":[],"reason":"x"}`, "at least one issue id"},
		{"no reason", `{"ids":["loom-a"],"reason":""}`, "reason is required"},
		{"blank reason", `{"ids":["loom-a"],"reason":"   "}`, "reason is required"},
		{"flag-shaped id", `{"ids":["--force","loom-a"],"reason":"x"}`, "invalid issue id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			rec := do(t, s, http.MethodPost, "/api/projects/loom/issues/close", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			var body errorResponse
			json.Unmarshal(rec.Body.Bytes(), &body)
			if !strings.Contains(body.Error, tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", body.Error, tc.wantMsg)
			}
			if calls := mock.Calls(); len(calls) != 0 {
				t.Errorf("bd was invoked: %v", calls)
			}
		})
	}
}

// TestN7UnknownQueryParamsAreRejected is the regression test for review finding
// N7: a typo used to return a full unfiltered page with 200.
func TestN7UnknownQueryParamsAreRejected(t *testing.T) {
	for _, query := range []string{
		"staus=open",  // the reviewer's typo
		"group=phase", // listed in ARCHITECTURE but not implemented
		"query=status%3Dopen",
		"limit=10&nonsense=1",
	} {
		t.Run(query, func(t *testing.T) {
			s := newTestServer(t)
			rec := do(t, s, http.MethodGet, "/api/projects/loom/issues?"+query, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			var body errorResponse
			json.Unmarshal(rec.Body.Bytes(), &body)
			if !strings.Contains(body.Error, "unknown query parameter") {
				t.Errorf("error = %q", body.Error)
			}
		})
	}

	// Every documented parameter must still be accepted.
	s := newTestServer(t)
	all := "status=open&type=task&label=x&assignee=nate&q=t&sort=priority" +
		"&includeClosed=true&priority=1&priorityMin=0&priorityMax=4&limit=10&offset=0"
	rec := do(t, s, http.MethodGet, "/api/projects/loom/issues?"+all, "")
	if rec.Code == http.StatusBadRequest {
		t.Errorf("a fully-specified query was rejected: %s", rec.Body.String())
	}
}

// TestI2BulletedProseIsAccepted is the end-to-end half of review finding I2:
// the most natural shape for a description — a markdown bullet list — used to
// return 400 "description may not begin with '-'".
func TestI2BulletedProseIsAccepted(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   []string
	}{
		{
			name: "create with a bulleted description", method: http.MethodPost,
			path: "/api/projects/loom/issues",
			body: `{"title":"Add the endpoint","description":"- add the endpoint\n- wire the UI"}`,
			want: []string{"create", "--title", "Add the endpoint",
				"--description", "- add the endpoint\n- wire the UI", "--silent"},
		},
		{
			name: "create with bulleted acceptance criteria", method: http.MethodPost,
			path: "/api/projects/loom/issues",
			body: `{"title":"t","acceptance":"- endpoint returns 200\n- covered by a test"}`,
			want: []string{"create", "--title", "t",
				"--acceptance", "- endpoint returns 200\n- covered by a test", "--silent"},
		},
		{
			name: "patch a bulleted description and dash-leading notes", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1",
			body: `{"description":"- add the endpoint\n- wire the UI","notes":"-blocked on loom-2"}`,
			want: []string{"update", "loom-1",
				"--description", "- add the endpoint\n- wire the UI",
				"--notes", "-blocked on loom-2"},
		},
		{
			name: "patch a title starting with a dash", method: http.MethodPatch,
			path: "/api/projects/loom/issues/loom-1",
			body: `{"title":"-wontfix for now"}`,
			want: []string{"update", "loom-1", "--title", "-wontfix for now"},
		},
		{
			name: "close with a dash-leading reason", method: http.MethodPost,
			path: "/api/projects/loom/issues/loom-1/close",
			body: `{"reason":"-wontfix"}`,
			want: []string{"close", "loom-1", "--reason", "-wontfix"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			rec := do(t, s, tc.method, tc.path, tc.body)

			if rec.Code == http.StatusBadRequest {
				t.Fatalf("legitimate prose was rejected: %s", rec.Body.String())
			}
			calls := mock.Calls()
			if len(calls) == 0 {
				t.Fatal("bd was never invoked")
			}
			if !reflect.DeepEqual(calls[0].Args, tc.want) {
				t.Errorf("argv mismatch\n got: %v\nwant: %v", calls[0].Args, tc.want)
			}
		})
	}
}
