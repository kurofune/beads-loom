package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
)

// newCSRFServer builds a server with a known listen address so the Host check
// has something to compare against.
func newCSRFServer(t *testing.T) (*Server, *bdexec.MockRunner) {
	t.Helper()
	cfg := &config.Config{
		Listen:   "127.0.0.1:7333",
		Projects: []config.Project{{Name: "loom", Workdir: t.TempDir()}},
	}
	reg := registry.New(cfg)
	t.Cleanup(func() { reg.Close() })

	mock := bdexec.NewMockRunner()
	mock.Stdout = "loom-new\n"
	s := New(Options{Registry: reg, BD: bdexec.New(mock), Listen: cfg.Listen})
	return s, mock
}

// request builds a write request with full control over the CSRF-relevant
// headers.
func request(method, path, body, contentType, origin, host string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if host != "" {
		r.Host = host
	}
	return r
}

// TestB2SimpleCrossOriginPostIsRejected is the reviewer's concrete failure: a
// malicious page firing a CORS "simple request" at a running Loom.
//
// Such a request carries text/plain (or form encoding) and needs no preflight,
// so chi/cors lets it through to the handler; only the Content-Type check stops
// it before anything is written.
func TestB2SimpleCrossOriginPostIsRejected(t *testing.T) {
	simpleTypes := []string{
		"text/plain",
		"text/plain;charset=UTF-8",
		"application/x-www-form-urlencoded",
		"multipart/form-data",
		"", // fetch with no explicit type
	}

	targets := []struct{ method, path, body string }{
		{http.MethodPost, "/api/projects/loom/issues/loom-1/close", `{"reason":"pwned"}`},
		{http.MethodPost, "/api/projects/loom/issues", `{"title":"spam"}`},
		{http.MethodPost, "/api/projects/loom/dolt/push", ""},
		{http.MethodPatch, "/api/projects/loom/issues/loom-1", `{"status":"closed"}`},
		{http.MethodDelete, "/api/projects/loom/deps", `{"from":"loom-1","to":"loom-2"}`},
	}

	for _, ct := range simpleTypes {
		for _, tgt := range targets {
			t.Run(tgt.method+" "+tgt.path+" ct="+ct, func(t *testing.T) {
				s, mock := newCSRFServer(t)
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, request(tgt.method, tgt.path, tgt.body, ct,
					"https://evil.example", "127.0.0.1:7333"))

				if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnsupportedMediaType {
					t.Fatalf("status = %d, want 403 or 415 (body: %s)", rec.Code, rec.Body.String())
				}
				if calls := mock.Calls(); len(calls) != 0 {
					t.Errorf("the write reached bd: %v", calls)
				}
			})
		}
	}
}

// TestB2ForeignOriginRejectedEvenWithJSONContentType covers the case where the
// attacker can set the header (an extension, a non-browser client that still
// sends Origin).
func TestB2ForeignOriginRejected(t *testing.T) {
	for _, origin := range []string{
		"https://evil.example",
		"http://localhost:3000",
		"http://127.0.0.1:9999",
		"null",
	} {
		t.Run(origin, func(t *testing.T) {
			s, mock := newCSRFServer(t)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, request(http.MethodPost, "/api/projects/loom/issues/loom-1/close",
				`{"reason":"x"}`, "application/json", origin, "127.0.0.1:7333"))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
			}
			if calls := mock.Calls(); len(calls) != 0 {
				t.Errorf("the write reached bd: %v", calls)
			}
		})
	}
}

// TestB2HostCheckDefeatsDNSRebinding: a rebinding attack resolves an
// attacker-controlled name to 127.0.0.1, so the request arrives with a foreign
// Host. "It only binds localhost" is not a defence without this.
func TestB2HostRejected(t *testing.T) {
	for _, host := range []string{
		"evil.example",
		"evil.example:7333",
		"attacker.test:7333",
	} {
		t.Run(host, func(t *testing.T) {
			s, mock := newCSRFServer(t)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, request(http.MethodPost, "/api/projects/loom/issues/loom-1/close",
				`{"reason":"x"}`, "application/json", "", host))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
			}
			if calls := mock.Calls(); len(calls) != 0 {
				t.Errorf("the write reached bd: %v", calls)
			}
		})
	}
}

// TestB2LegitimateDevOriginWorks is the other half: the vite dev server must
// still be able to write.
func TestB2LegitimateWritesWork(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		origin      string
		host        string
	}{
		{"vite dev origin", "application/json", "http://localhost:5173", "127.0.0.1:7333"},
		{"vite dev origin on loopback ip", "application/json", "http://127.0.0.1:5173", "127.0.0.1:7333"},
		{"same-origin production", "application/json", "http://127.0.0.1:7333", "127.0.0.1:7333"},
		{"same-origin via localhost", "application/json", "http://localhost:7333", "localhost:7333"},
		{"no origin header (curl)", "application/json", "", "127.0.0.1:7333"},
		{"charset parameter tolerated", "application/json; charset=utf-8", "http://localhost:5173", "127.0.0.1:7333"},
		{"uppercase content type", "APPLICATION/JSON", "", "127.0.0.1:7333"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newCSRFServer(t)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, request(http.MethodPost, "/api/projects/loom/issues/loom-1/close",
				`{"reason":"shipped"}`, tc.contentType, tc.origin, tc.host))

			if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
				t.Fatalf("a legitimate write was rejected: %d %s", rec.Code, rec.Body.String())
			}
			calls := mock.Calls()
			if len(calls) == 0 {
				t.Fatal("the write did not reach bd")
			}
			if calls[0].Args[0] != "close" {
				t.Errorf("argv = %v", calls[0].Args)
			}
		})
	}
}

// TestB2ReadsAreUnaffected: GETs change nothing and must not need the header.
func TestB2ReadsAreUnaffected(t *testing.T) {
	s, _ := newCSRFServer(t)
	for _, path := range []string{
		"/api/health",
		"/api/projects",
		"/api/now",
		"/api/projects/loom/issues",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, request(http.MethodGet, path, "", "", "https://evil.example", "evil.example"))
			if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
				t.Errorf("a read was blocked by the CSRF guard: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestB2PreflightStillAnswered: the Content-Type requirement only helps because
// the browser then preflights, and the preflight must be answered for the
// allowed origin and denied for others.
func TestB2Preflight(t *testing.T) {
	s, _ := newCSRFServer(t)

	t.Run("allowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/projects/loom/issues/loom-1/close", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "content-type")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Errorf("Access-Control-Allow-Origin = %q", got)
		}
	})

	t.Run("foreign origin is not granted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/projects/loom/issues/loom-1/close", nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "content-type")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "https://evil.example" {
			t.Error("the preflight granted a foreign origin")
		}
	})
}

func TestAllowedHosts(t *testing.T) {
	tests := []struct {
		listen  string
		host    string
		allowed bool
	}{
		{"127.0.0.1:7333", "127.0.0.1:7333", true},
		{"127.0.0.1:7333", "localhost:7333", true},
		{"127.0.0.1:7333", "[::1]:7333", true},
		{"127.0.0.1:7333", "evil.example:7333", false},
		{"127.0.0.1:7333", "127.0.0.1:9999", false},
		{"0.0.0.0:7333", "localhost:7333", true},
		{"192.168.1.5:8080", "192.168.1.5:8080", true},
		{"192.168.1.5:8080", "127.0.0.1:8080", true},
	}
	for _, tc := range tests {
		t.Run(tc.listen+"/"+tc.host, func(t *testing.T) {
			if got := allowedHosts(tc.listen)[tc.host]; got != tc.allowed {
				t.Errorf("allowedHosts(%q)[%q] = %v, want %v", tc.listen, tc.host, got, tc.allowed)
			}
		})
	}
}

func TestIsJSONContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"APPLICATION/JSON", true},
		{" application/json ", true},
		{"text/plain", false},
		{"application/x-www-form-urlencoded", false},
		{"multipart/form-data", false},
		{"", false},
		{"application/jsonp", false},
	}
	for _, tc := range tests {
		t.Run(tc.ct, func(t *testing.T) {
			if got := isJSONContentType(tc.ct); got != tc.want {
				t.Errorf("isJSONContentType(%q) = %v, want %v", tc.ct, got, tc.want)
			}
		})
	}
}

// TestB2HostCheckDisabledWithoutListen keeps the guard usable in tests and
// embeddings that do not configure a listen address.
func TestB2HostCheckDisabledWithoutListen(t *testing.T) {
	cfg := &config.Config{Listen: "", Projects: []config.Project{{Name: "loom", Workdir: t.TempDir()}}}
	reg := registry.New(cfg)
	defer reg.Close()
	mock := bdexec.NewMockRunner()
	s := New(Options{Registry: reg, BD: bdexec.New(mock)}) // no Listen

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request(http.MethodPost, "/api/projects/loom/issues/loom-1/close",
		`{"reason":"x"}`, "application/json", "", "anything:1234"))
	if rec.Code == http.StatusForbidden {
		t.Errorf("Host check should be disabled with no configured listen: %s", rec.Body.String())
	}
}

func TestErrorResponsesAreJSON(t *testing.T) {
	s, _ := newCSRFServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request(http.MethodPost, "/api/projects/loom/issues/loom-1/close",
		`{"reason":"x"}`, "text/plain", "", "127.0.0.1:7333"))

	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("rejection is not JSON: %v", err)
	}
	if body.Error == "" {
		t.Error("rejection carries no message")
	}
}
