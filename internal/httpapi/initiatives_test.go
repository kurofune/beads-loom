package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/loomtest/livedolt"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/registry"
)

func TestInitiativesEndpointGuards(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		want     int
		wantBody string
	}{
		{"unknown project", "/api/projects/nope/initiatives", http.StatusNotFound, "no such project"},
		{"unreachable project", "/api/projects/loom/initiatives", http.StatusServiceUnavailable, "unreachable"},
		{"unknown parameter", "/api/projects/loom/initiatives?epic=loom-1", http.StatusBadRequest, "known: includeClosed"},
		{"bad includeClosed", "/api/projects/loom/initiatives?includeClosed=maybe", http.StatusBadRequest, "invalid includeClosed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t)
			rec := do(t, s, http.MethodGet, tc.path, "")
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestHealthWireOnInitiativesAndIssueDetail(t *testing.T) {
	s := newLiveHTTPServer(t)

	initiativesRec := do(t, s, http.MethodGet, "/api/projects/loom/initiatives?includeClosed=true", "")
	if initiativesRec.Code != http.StatusOK {
		t.Fatalf("initiatives status = %d, want 200 (body: %s)", initiativesRec.Code, initiativesRec.Body.String())
	}
	var initiatives initiativesResponse
	if err := json.Unmarshal(initiativesRec.Body.Bytes(), &initiatives); err != nil {
		t.Fatalf("decode initiatives: %v", err)
	}
	if len(initiatives.Initiatives) == 0 {
		t.Fatal("live initiatives response is empty")
	}
	epic := initiatives.Initiatives[0]

	epicRec := do(t, s, http.MethodGet, "/api/projects/loom/issues/"+epic.ID, "")
	if epicRec.Code != http.StatusOK {
		t.Fatalf("epic detail status = %d, want 200 (body: %s)", epicRec.Code, epicRec.Body.String())
	}
	var epicDetail queries.IssueDetail
	if err := json.Unmarshal(epicRec.Body.Bytes(), &epicDetail); err != nil {
		t.Fatalf("decode epic detail: %v", err)
	}
	if epicDetail.Health == nil ||
		epicDetail.Health.State != epic.Health.State ||
		epicDetail.Health.Explanation != epic.Health.Explanation {
		t.Fatalf("epic detail health = %#v, want initiatives health %#v", epicDetail.Health, epic.Health)
	}

	issuesRec := do(t, s, http.MethodGet, "/api/projects/loom/issues?includeClosed=true&limit=200", "")
	if issuesRec.Code != http.StatusOK {
		t.Fatalf("issues status = %d, want 200 (body: %s)", issuesRec.Code, issuesRec.Body.String())
	}
	var issues queries.IssueList
	if err := json.Unmarshal(issuesRec.Body.Bytes(), &issues); err != nil {
		t.Fatalf("decode issues: %v", err)
	}
	var nonEpicID string
	for _, issue := range issues.Issues {
		if issue.Type != "epic" && !loomtest.IsScratch(issue.Title) {
			nonEpicID = issue.ID
			break
		}
	}
	if nonEpicID == "" {
		t.Fatal("live issues response has no non-epic fixture")
	}
	nonEpicRec := do(t, s, http.MethodGet, "/api/projects/loom/issues/"+nonEpicID, "")
	if nonEpicRec.Code != http.StatusOK {
		t.Fatalf("non-epic detail status = %d, want 200 (body: %s)", nonEpicRec.Code, nonEpicRec.Body.String())
	}
	var nonEpicRaw map[string]json.RawMessage
	if err := json.Unmarshal(nonEpicRec.Body.Bytes(), &nonEpicRaw); err != nil {
		t.Fatalf("decode non-epic detail: %v", err)
	}
	health, present := nonEpicRaw["health"]
	if !present || string(health) != "null" {
		t.Fatalf("non-epic health = %s (present=%t), want explicit null", health, present)
	}
}

func TestInitiativesResponseIncludesCloseEligible(t *testing.T) {
	body, err := json.Marshal(initiativesResponse{Initiatives: []queries.Initiative{{
		ID:            "epic-1",
		CloseEligible: true,
	}}})
	if err != nil {
		t.Fatalf("marshal initiatives: %v", err)
	}
	var raw struct {
		Initiatives []map[string]json.RawMessage `json:"initiatives"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw initiatives: %v", err)
	}
	if got := string(raw.Initiatives[0]["closeEligible"]); got != "true" {
		t.Fatalf("closeEligible = %s, want true", got)
	}
}

// newLiveHTTPServer serves this repo's own seeded database over the real HTTP
// stack. It goes through the shared environment gate (AGENTS.md → "Testing"):
// no Dolt server means skip, because nothing in the checkout can conjure one.
// Everything after the gate is fixture, so it fails.
func newLiveHTTPServer(t *testing.T) *Server {
	t.Helper()
	return newLiveHTTPServerWithBD(t, nil)
}

// newLiveHTTPServerWithBD is newLiveHTTPServer with the write endpoints
// mounted over bd.
func newLiveHTTPServerWithBD(t *testing.T, bd *bdexec.Client) *Server {
	t.Helper()
	dir := loomtest.RepoRoot(t)
	loomtest.LockFixture(t, dir)
	livedolt.Require(t, dir)

	reg := registry.New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "loom", Workdir: dir}},
	})
	t.Cleanup(func() { reg.Close() })
	reg.Refresh(context.Background())
	project, err := reg.Get("loom")
	if err != nil {
		t.Fatal(err)
	}
	if !project.Reachable {
		// The gate above already reached this server, so the registry
		// disagreeing with it is a real defect, not an absent environment.
		t.Fatalf("live HTTP fixture is unreachable through the registry: %s", project.Reason)
	}
	return New(Options{Registry: reg, BD: bd})
}
