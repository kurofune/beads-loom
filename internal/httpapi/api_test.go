package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/registry"
)

func ptr[T any](v T) *T { return &v }

func TestFilterFromQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		want    queries.Filter
		wantErr string
	}{
		{
			name:  "empty query is the zero filter",
			query: "",
			want:  queries.Filter{},
		},
		{
			name:  "comma separated status",
			query: "status=open,in_progress",
			want:  queries.Filter{Statuses: []string{"open", "in_progress"}},
		},
		{
			name:  "repeated status",
			query: "status=open&status=blocked",
			want:  queries.Filter{Statuses: []string{"open", "blocked"}},
		},
		{
			name:  "types and labels",
			query: "type=epic,bug&label=phase-1&label=layer-ui",
			want: queries.Filter{
				Types:  []string{"epic", "bug"},
				Labels: []string{"phase-1", "layer-ui"},
			},
		},
		{
			name:  "priority list",
			query: "priority=0,1",
			want:  queries.Filter{Priorities: []int{0, 1}},
		},
		{
			name:  "priority range",
			query: "priorityMin=1&priorityMax=3",
			want:  queries.Filter{PriorityMin: ptr(1), PriorityMax: ptr(3)},
		},
		{
			name:  "assignee and free text",
			query: "assignee=nate&q=kanban",
			want:  queries.Filter{Assignee: "nate", Text: "kanban"},
		},
		{
			name:  "paging and sort",
			query: "limit=25&offset=50&sort=updated",
			want:  queries.Filter{Limit: 25, Offset: 50, Sort: "updated"},
		},
		{
			name:  "include closed",
			query: "includeClosed=true",
			want:  queries.Filter{IncludeClosed: true},
		},
		{
			name:  "include closed accepts 1",
			query: "includeClosed=1",
			want:  queries.Filter{IncludeClosed: true},
		},
		{
			name:  "epic scope",
			query: "epic=loom-g1v",
			want:  queries.Filter{Epic: "loom-g1v"},
		},
		{
			name:  "no receipt scoped to an epic",
			query: "epic=loom-g1v&noReceipt=true",
			want:  queries.Filter{Epic: "loom-g1v", NoReceipt: true},
		},
		{
			name:    "bad includeClosed",
			query:   "includeClosed=maybe",
			wantErr: "invalid includeClosed",
		},
		{
			name:    "bad no receipt",
			query:   "noReceipt=maybe",
			wantErr: "invalid noReceipt",
		},
		{
			name:    "non-numeric priority",
			query:   "priority=high",
			wantErr: "invalid priority",
		},
		{
			name:    "out of range priority",
			query:   "priority=9",
			wantErr: "invalid priority",
		},
		{
			name:    "non-numeric limit",
			query:   "limit=lots",
			wantErr: "invalid limit",
		},
		{
			name:    "unknown sort key",
			query:   "sort=title",
			wantErr: "invalid sort",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/projects/loom/issues?"+tc.query, nil)
			got, err := filterFromQuery(r)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got filter %+v", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("filterFromQuery: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("filter = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestIssuesEndpointEpicParameterGuards(t *testing.T) {
	s := newTestServer(t)
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantBody   string
	}{
		{name: "valid scope reaches the project", query: "epic=loom-g1v", wantStatus: http.StatusServiceUnavailable},
		{name: "malformed id", query: "epic=not%20valid!", wantStatus: http.StatusBadRequest, wantBody: "invalid epic id"},
		{name: "unknown parameter", query: "epci=loom-g1v", wantStatus: http.StatusBadRequest, wantBody: "unknown query parameter"},
		{name: "misspelled no-receipt parameter", query: "noReciept=true", wantStatus: http.StatusBadRequest, wantBody: "unknown query parameter"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/loom/issues?"+tc.query, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

// newTestServer builds a server over a registry whose single project has no
// dolt server, which is the degraded path most worth exercising.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "loom", Workdir: t.TempDir()}},
	}
	reg := registry.New(cfg)
	t.Cleanup(func() { reg.Close() })
	return New(Options{Registry: reg, BDVersion: "bd version 1.1.2 (test)"})
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if !strings.Contains(body["bdVersion"].(string), "1.1.2") {
		t.Errorf("bdVersion = %v", body["bdVersion"])
	}
}

// TestProjectsDegradesGracefully: an unreachable project must still appear in
// the list with a reason, not blow up the request.
func TestProjectsDegradesGracefully(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with every project down", rec.Code)
	}
	var body []projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("got %d projects, want 1", len(body))
	}
	if body[0].Name != "loom" {
		t.Errorf("name = %q", body[0].Name)
	}
	if body[0].Reachable {
		t.Error("project should be unreachable")
	}
	if body[0].Reason == "" {
		t.Error("an unreachable project must carry a reason")
	}
	if body[0].Health != nil {
		t.Error("health should be null for an unreachable project")
	}
}

func TestUnknownProjectIs404(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{
		"/api/projects/nope/issues",
		"/api/projects/nope/issues/x-1",
		"/api/projects/nope/stats",
		"/api/projects/nope/schema",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !strings.Contains(body.Error, "nope") {
				t.Errorf("error = %q, want it to name the project", body.Error)
			}
		})
	}
}

func TestUnreachableProjectIs503(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{
		"/api/projects/loom/issues",
		"/api/projects/loom/issues/loom-1",
		"/api/projects/loom/stats",
		"/api/projects/loom/schema",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error == "" {
				t.Error("503 must carry an explanation")
			}
		})
	}
}

func TestBadFilterIs400(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/loom/issues?priority=urgent", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(body.Error, "priority") {
		t.Errorf("error = %q, want it to name the bad parameter", body.Error)
	}
}

func TestUnknownAPIEndpointIs404JSON(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nonsense", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unknown /api path must still answer JSON: %v", err)
	}
	if body.Error == "" {
		t.Error("missing error message")
	}
}

func TestCORSAllowsViteDevOrigin(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the vite dev origin", got)
	}

	// A random origin must not be allowed.
	req = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "https://evil.example" {
		t.Error("unexpected origin was allowed")
	}
}

// TestNoWebFSLeavesRootUnhandled: with no frontend built, / must not 500.
func TestNoWebFSLeavesRootAlone(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no frontend is embedded", rec.Code)
	}
}

func TestServesEmbeddedFrontend(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html>loom</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
		"favicon.svg":   &fstest.MapFile{Data: []byte("<svg/>")},
	}
	cfg := &config.Config{Listen: config.DefaultListen, Projects: []config.Project{{Name: "loom", Workdir: t.TempDir()}}}
	reg := registry.New(cfg)
	defer reg.Close()
	s := New(Options{Registry: reg, WebFS: web})

	t.Run("index", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "loom") {
			t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("index Cache-Control = %q, want no-cache", cc)
		}
	})

	t.Run("hashed assets are cached hard", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
		}
	})

	t.Run("client route falls back to index", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/board/loom", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "loom") {
			t.Fatalf("SPA fallback failed: status %d body %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("api 404 stays json even with a frontend mounted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nonsense", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("content-type = %q, want json", rec.Header().Get("Content-Type"))
		}
	})
}
