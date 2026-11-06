package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/queries"
)

// TestNowDegradesOverUnreachableProjects: /api/now must answer even when every
// project is down, naming what it skipped rather than silently reporting an
// empty world.
func TestNowDegradesOverUnreachableProjects(t *testing.T) {
	s := newTestServer(t) // one project, no dolt server

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/now", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with everything down", rec.Code)
	}
	var body nowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Entries == nil {
		t.Error("entries must serialize as [] not null")
	}
	if len(body.Entries) != 0 {
		t.Errorf("entries = %v, want empty", body.Entries)
	}
	if len(body.Skipped) != 1 || body.Skipped[0] != "loom" {
		t.Errorf("skipped = %v, want [loom]", body.Skipped)
	}
}

func TestNowJSONShape(t *testing.T) {
	// Pin the wire contract the frontend is built against.
	data, err := json.Marshal(nowResponse{
		Entries: []queries.NowEntry{{
			Project: "loom",
			Issue: queries.NowIssue{
				ID: "loom-1", Title: "t", Status: "in_progress",
				Priority: 1, Ready: false, Assignee: "nate",
			},
			Stale: true,
		}},
		Skipped: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"project":"loom"`, `"issue":{`, `"id":"loom-1"`, `"assignee":"nate"`,
		`"startedAt":null`, `"inStatusSince":null`, `"stale":true`, `"skipped":[]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON missing %s\ngot: %s", want, got)
		}
	}
}

func TestDoltStatusUnreachableIs503(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/loom/dolt", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestDoltStatusUnknownProjectIs404(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope/dolt", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDoltPushPullArgv(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/api/projects/loom/dolt/push", []string{"dolt", "push"}},
		{"/api/projects/loom/dolt/pull", []string{"dolt", "pull"}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			mock.Stdout = "Everything up-to-date\n"

			rec := do(t, s, http.MethodPost, tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}

			calls := mock.Calls()
			if len(calls) != 1 {
				t.Fatalf("got %d invocations, want 1: %v", len(calls), calls)
			}
			if strings.Join(calls[0].Args, " ") != strings.Join(tc.want, " ") {
				t.Errorf("argv = %v, want %v", calls[0].Args, tc.want)
			}

			var body doltSyncResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !body.OK {
				t.Error("ok = false on a successful sync")
			}
			if body.Output != "Everything up-to-date" {
				t.Errorf("output = %q, want bd's stdout", body.Output)
			}
		})
	}
}

// TestDoltSyncConflictIs409 is the loud-conflict rule: Loom surfaces the
// conflict and never tries to resolve it.
func TestDoltSyncConflictIs409(t *testing.T) {
	conflictOutputs := []string{
		"Error: merge conflict in table issues",
		"error: failed to push: non-fast-forward",
		"branches have diverged",
		"Automatic merge failed; fix conflicts and commit the result",
	}

	for _, stderr := range conflictOutputs {
		t.Run(stderr, func(t *testing.T) {
			s, mock, _ := newWriteServer(t)
			mock.Stdout = ""
			mock.Stderr = stderr
			mock.Err = &exec.ExitError{}

			rec := do(t, s, http.MethodPost, "/api/projects/loom/dolt/pull", "")
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body.Error, "conflict") {
				t.Errorf("error = %q, want it to name the conflict", body.Error)
			}
			if body.Stderr != stderr {
				t.Errorf("stderr = %q, want bd's message verbatim", body.Stderr)
			}
		})
	}
}

// TestDoltSyncNonConflictFailureIs422 keeps ordinary failures distinguishable
// from conflicts, since the UI treats them differently.
func TestDoltSyncNonConflictFailureIs422(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Stdout = ""
	mock.Stderr = "Error: no remote configured"
	mock.Err = &exec.ExitError{}

	rec := do(t, s, http.MethodPost, "/api/projects/loom/dolt/push", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var body errorResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Stderr != "Error: no remote configured" {
		t.Errorf("stderr = %q", body.Stderr)
	}
}

func TestDoltSyncMarksProjectWritten(t *testing.T) {
	s, _, _ := newWriteServer(t)

	before, _ := s.reg.Generation("loom")
	do(t, s, http.MethodPost, "/api/projects/loom/dolt/pull", "")
	after, _ := s.reg.Generation("loom")

	if after != before+1 {
		t.Errorf("generation = %d, want %d; a sync changes what clients should see", after, before)
	}
}

func TestDoltSyncWithoutBDIsNotRouted(t *testing.T) {
	s := newTestServer(t) // no bd client
	for _, path := range []string{
		"/api/projects/loom/dolt/push",
		"/api/projects/loom/dolt/pull",
	} {
		rec := do(t, s, http.MethodPost, path, "")
		if rec.Code == http.StatusOK {
			t.Errorf("%s returned 200 without a bd client", path)
		}
	}
}

func TestIsConflictClassification(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"merge conflict", "Error: merge conflict in issues", true},
		{"non fast forward", "failed to push: non-fast-forward", true},
		{"diverged", "local and remote have diverged", true},
		{"case insensitive", "CONFLICT detected", true},
		{"no remote", "Error: no remote configured", false},
		{"auth failure", "Permission denied (publickey)", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := bdexec.NewMockRunner()
			mock.Stderr = tc.stderr
			mock.Err = &exec.ExitError{}
			_, err := bdexec.New(mock).DoltPush(t.Context(), "/tmp")
			if got := bdexec.IsConflict(err); got != tc.want {
				t.Errorf("IsConflict(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}

	if bdexec.IsConflict(nil) {
		t.Error("IsConflict(nil) should be false")
	}
}
