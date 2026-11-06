package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
)

// writeRegistryError answers a busy store with 503 and Retry-After, an
// unreachable project with 503 and no Retry-After, and anything else with 500.
func TestWriteRegistryErrorStoreBusy(t *testing.T) {
	s := New(Options{Registry: registry.New(&config.Config{})})
	cases := []struct {
		err        error
		code       int
		retryAfter string
	}{
		{fmt.Errorf("%w: fx: %w", registry.ErrStoreBusy, errors.New("database is locked by another dolt process")), http.StatusServiceUnavailable, "1"},
		{fmt.Errorf("%w: fx: gone", registry.ErrUnreachable), http.StatusServiceUnavailable, ""},
		{sql.ErrConnDone, http.StatusInternalServerError, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.writeRegistryError(rec, "fx", c.err)
		if rec.Code != c.code || rec.Header().Get("Retry-After") != c.retryAfter {
			t.Errorf("%v → %d Retry-After %q, want %d %q", c.err, rec.Code, rec.Header().Get("Retry-After"), c.code, c.retryAfter)
		}
	}
}

// A query that outlived its deadline on a live server answers 504 with the
// machine-readable code the UI keys its inline "query timed out" state on.
func TestWriteRegistryErrorQueryTimeout(t *testing.T) {
	s := New(Options{Registry: registry.New(&config.Config{})})
	rec := httptest.NewRecorder()
	s.writeRegistryError(rec, "fx", fmt.Errorf("%w: fx: %w", registry.ErrQueryTimeout, context.DeadlineExceeded))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "query_timeout" || body.Error == "" {
		t.Errorf("body = %+v, want code query_timeout and a message", body)
	}
}

// End to end through the router: /stats whose query times out is a 504
// query_timeout, not the 503 an unreachable server gets.
func TestStatsQueryTimeoutIs504(t *testing.T) {
	s := New(Options{Registry: registry.New(&config.Config{})})
	s.queryDB = func(context.Context, string, func(context.Context, *sql.DB) error) error {
		return fmt.Errorf("%w: fx: stats ready: %w", registry.ErrQueryTimeout, context.DeadlineExceeded)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/fx/stats", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"code":"query_timeout"`) {
		t.Errorf("body = %s, want code query_timeout", rec.Body)
	}
}
