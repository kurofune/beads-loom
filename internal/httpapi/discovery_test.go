package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
)

type fakeDiscoverySource struct {
	status DiscoveryStatus

	hideErr   error
	unhideErr error
	rescanErr error
	added     []string
	removed   []string

	hiddenWorkdir   string
	unhiddenWorkdir string
	statusCalls     int
}

func (f *fakeDiscoverySource) Status() DiscoveryStatus {
	f.statusCalls++
	return f.status
}

func (f *fakeDiscoverySource) Hide(workdir string) error {
	f.hiddenWorkdir = workdir
	return f.hideErr
}

func (f *fakeDiscoverySource) Unhide(workdir string) error {
	f.unhiddenWorkdir = workdir
	return f.unhideErr
}

func (f *fakeDiscoverySource) Rescan(context.Context) ([]string, []string, error) {
	return f.added, f.removed, f.rescanErr
}

func newDiscoveryServer(t *testing.T, source DiscoverySource) (*Server, *registry.Registry) {
	t.Helper()
	reg := registry.New(&config.Config{Projects: []config.Project{{Name: "configured", Workdir: t.TempDir()}}})
	t.Cleanup(func() { reg.Close() })
	return New(Options{Registry: reg, Discovery: source}), reg
}

func TestProjectsIncludeSource(t *testing.T) {
	s, reg := newDiscoveryServer(t, nil)
	reg.SetDiscovered([]config.Project{{Name: "automatic", Workdir: t.TempDir()}})

	rec := do(t, s, http.MethodGet, "/api/projects", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body []projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || body[0].Source != registry.SourceConfig || body[1].Source != registry.SourceDiscovered {
		t.Fatalf("projects = %+v", body)
	}
}

func TestDiscoveryStatusDisabledAndEnabled(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		s, _ := newDiscoveryServer(t, nil)
		rec := do(t, s, http.MethodGet, "/api/discovery", "")
		want := "{\"enabled\":false,\"roots\":[],\"depth\":0,\"exclude\":[],\"intervalSeconds\":0,\"lastScanAt\":null,\"hidden\":[]}\n"
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("enabled", func(t *testing.T) {
		at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
		fake := &fakeDiscoverySource{status: DiscoveryStatus{
			Enabled: true, Roots: []string{"/projects"}, Depth: 3,
			Exclude: []string{"archive"}, IntervalSeconds: 300, LastScanAt: &at,
			Hidden: []HiddenProject{{Workdir: "/projects/hidden", Name: "hidden"}},
		}}
		s, _ := newDiscoveryServer(t, fake)
		rec := do(t, s, http.MethodGet, "/api/discovery", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var got DiscoveryStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Enabled || got.Depth != 3 || got.IntervalSeconds != 300 || got.LastScanAt == nil || len(got.Hidden) != 1 {
			t.Fatalf("status = %+v", got)
		}
	})

	t.Run("nil slices normalize", func(t *testing.T) {
		fake := &fakeDiscoverySource{status: DiscoveryStatus{Enabled: true}}
		s, _ := newDiscoveryServer(t, fake)
		rec := do(t, s, http.MethodGet, "/api/discovery", "")
		if rec.Body.String() != "{\"enabled\":true,\"roots\":[],\"depth\":0,\"exclude\":[],\"intervalSeconds\":0,\"lastScanAt\":null,\"hidden\":[]}\n" {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestDiscoveryGETRejectsQueryParameters(t *testing.T) {
	s, _ := newDiscoveryServer(t, nil)
	rec := do(t, s, http.MethodGet, "/api/discovery?refresh=1", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDiscoveryMutations(t *testing.T) {
	fake := &fakeDiscoverySource{
		status:  DiscoveryStatus{Enabled: true},
		added:   []string{"new"},
		removed: []string{"old"},
	}
	s, _ := newDiscoveryServer(t, fake)

	rec := do(t, s, http.MethodPost, "/api/discovery/hide", `{"workdir":"/projects/a"}`)
	if rec.Code != http.StatusOK || fake.hiddenWorkdir != "/projects/a" {
		t.Fatalf("hide: status=%d workdir=%q body=%s", rec.Code, fake.hiddenWorkdir, rec.Body.String())
	}
	rec = do(t, s, http.MethodPost, "/api/discovery/unhide", `{"workdir":"/projects/b"}`)
	if rec.Code != http.StatusOK || fake.unhiddenWorkdir != "/projects/b" {
		t.Fatalf("unhide: status=%d workdir=%q body=%s", rec.Code, fake.unhiddenWorkdir, rec.Body.String())
	}
	if fake.statusCalls != 2 {
		t.Fatalf("successful mutations refreshed status %d times, want 2", fake.statusCalls)
	}

	for _, body := range []string{"", `{}`} {
		rec = do(t, s, http.MethodPost, "/api/discovery/rescan", body)
		if rec.Code != http.StatusOK || rec.Body.String() != "{\"added\":[\"new\"],\"removed\":[\"old\"]}\n" {
			t.Fatalf("rescan %q: status=%d body=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestDiscoveryMutationErrors(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		s, _ := newDiscoveryServer(t, nil)
		for _, tc := range []struct {
			path string
			body string
		}{
			{"/api/discovery/hide", `{"workdir":"/a"}`},
			{"/api/discovery/unhide", `{"workdir":"/a"}`},
			{"/api/discovery/rescan", `{}`},
		} {
			rec := do(t, s, http.MethodPost, tc.path, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s status = %d, want 404", tc.path, rec.Code)
			}
		}
	})

	t.Run("source errors", func(t *testing.T) {
		fake := &fakeDiscoverySource{
			hideErr:   errors.New("workdir is not a discovered project"),
			unhideErr: errors.New("workdir is not hidden"),
			rescanErr: errors.New("scan failed"),
		}
		s, _ := newDiscoveryServer(t, fake)
		for _, tc := range []struct {
			path string
			body string
			code int
		}{
			{"/api/discovery/hide", `{"workdir":"/a"}`, http.StatusNotFound},
			{"/api/discovery/unhide", `{"workdir":"/a"}`, http.StatusNotFound},
			{"/api/discovery/rescan", `{}`, http.StatusInternalServerError},
		} {
			rec := do(t, s, http.MethodPost, tc.path, tc.body)
			if rec.Code != tc.code {
				t.Fatalf("%s status = %d, want %d", tc.path, rec.Code, tc.code)
			}
		}
	})
}

func TestDiscoveryStrictBodiesAndCSRF(t *testing.T) {
	s, _ := newDiscoveryServer(t, &fakeDiscoverySource{})
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/discovery/hide", `{}`},
		{"/api/discovery/hide", `{"workdir":"/a","extra":true}`},
		{"/api/discovery/unhide", `{"workdir":"/a","extra":true}`},
		{"/api/discovery/rescan", `{"extra":true}`},
		{"/api/discovery/rescan", `null`},
	} {
		rec := do(t, s, http.MethodPost, tc.path, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s body %s: status = %d, want 400", tc.path, tc.body, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/discovery/hide", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("hide without JSON Content-Type = %d, want 415", rec.Code)
	}
}
