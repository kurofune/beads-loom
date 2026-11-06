package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
)

// TestMalformedQueryIs400 pins the bug this guard exists for: Go's net/url
// treats ';' as an illegal separator and DROPS the whole segment rather than
// erroring, and r.URL.Query() swallows that error. Without the guard a request
// like ?epic=;DROP%20TABLE reaches the handler as zero parameters, so both the
// unknown-parameter check and bdexec.ValidIssueID never run, and the endpoint
// answers 200 with the full unscoped backlog while the UI still claims to be
// filtered.
//
// The test server's single project is unreachable, so 503 is the "the request
// was accepted and reached the handler" signal and 400 is the guard firing.
func TestMalformedQueryIs400(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "issues: semicolon separator is rejected, not silently dropped",
			path:       "/api/projects/loom/issues?epic=;DROP%20TABLE",
			wantStatus: http.StatusBadRequest,
			wantBody:   "semicolon",
		},
		{
			name:       "issues: semicolon anywhere in the raw query",
			path:       "/api/projects/loom/issues?status=open;includeClosed=true",
			wantStatus: http.StatusBadRequest,
			wantBody:   "semicolon",
		},
		{
			name:       "graph: semicolon separator is rejected",
			path:       "/api/projects/loom/graph?epic=;DROP%20TABLE",
			wantStatus: http.StatusBadRequest,
			wantBody:   "semicolon",
		},
		{
			name:       "initiatives: semicolon separator is rejected",
			path:       "/api/projects/loom/initiatives?includeClosed=true;x=1",
			wantStatus: http.StatusBadRequest,
			wantBody:   "semicolon",
		},
		{
			name:       "stale: semicolon separator is rejected",
			path:       "/api/projects/loom/stale?days=7;x=1",
			wantStatus: http.StatusBadRequest,
			wantBody:   "semicolon",
		},
		{
			name:       "invalid percent-escape is rejected rather than dropped",
			path:       "/api/projects/loom/issues?epic=%zz",
			wantStatus: http.StatusBadRequest,
			wantBody:   "malformed query string",
		},
		{
			name:       "issues: a normal multi-parameter & query still reaches the handler",
			path:       "/api/projects/loom/issues?status=open,in_progress&epic=loom-g1v&limit=25",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "graph: a normal multi-parameter & query still reaches the handler",
			path:       "/api/projects/loom/graph?epic=loom-g1v&includeClosed=true",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "a percent-encoded semicolon inside a value is legitimate",
			path:       "/api/projects/loom/issues?q=a%3Bb",
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusBadRequest {
				return
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("a rejected query must answer JSON: %v (body=%s)", err, rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(body.Error, tc.wantBody) {
				t.Errorf("error = %q, want it to contain %q", body.Error, tc.wantBody)
			}
		})
	}
}

// TestMalformedQueryGuardIsInherited: the guard must live in one shared place,
// so an endpoint that never wrote a parameter check of its own is still
// protected. /api/health parses no query parameters at all — a 400 here can
// only come from the shared middleware.
func TestMalformedQueryGuardIsInherited(t *testing.T) {
	s := newTestServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health?a=1;b=2", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 from the shared guard; body=%s", rec.Code, rec.Body.String())
	}

	// And the same endpoint without the malformed query is untouched.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health?a=1&b=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a well-formed query", rec.Code)
	}
}

// TestMalformedQueryGuardSparesTheFrontend: the guard is an API contract, not a
// static-asset one. A stray ';' in an asset URL must not turn the SPA into a
// JSON error page.
func TestMalformedQueryGuardSparesTheFrontend(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html>loom</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	cfg := &config.Config{Listen: config.DefaultListen, Projects: []config.Project{{Name: "loom", Workdir: t.TempDir()}}}
	reg := registry.New(cfg)
	defer reg.Close()
	s := New(Options{Registry: reg, WebFS: web})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js?v=1;2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: static assets are not query-parameter driven", rec.Code)
	}
}
