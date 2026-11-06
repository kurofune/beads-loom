package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// DiscoverySource is implemented by the discovery manager (adapted in main).
// A nil DiscoverySource means discovery is disabled.
type DiscoverySource interface {
	Status() DiscoveryStatus
	Hide(workdir string) error
	Unhide(workdir string) error
	Rescan(ctx context.Context) (added, removed []string, err error)
}

// DiscoveryStatus is the discovery state exposed by the HTTP API.
type DiscoveryStatus struct {
	Enabled         bool            `json:"enabled"`
	Roots           []string        `json:"roots"`
	Depth           int             `json:"depth"`
	Exclude         []string        `json:"exclude"`
	IntervalSeconds int             `json:"intervalSeconds"`
	LastScanAt      *time.Time      `json:"lastScanAt"`
	Hidden          []HiddenProject `json:"hidden"`
}

// HiddenProject is a discovered project hidden from the active registry.
type HiddenProject struct {
	Workdir string `json:"workdir"`
	Name    string `json:"name"`
}

type discoveryWorkdirRequest struct {
	Workdir string `json:"workdir"`
}

type discoveryRescanResponse struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

func (s *Server) discoveryRoutes(r chi.Router) {
	r.Get("/discovery", s.handleDiscoveryStatus)
	r.Post("/discovery/hide", s.handleDiscoveryHide)
	r.Post("/discovery/unhide", s.handleDiscoveryUnhide)
	r.Post("/discovery/rescan", s.handleDiscoveryRescan)
}

func (s *Server) handleDiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		writeError(w, http.StatusBadRequest, "unknown query parameters")
		return
	}
	writeJSON(w, http.StatusOK, s.discoveryStatus())
}

func (s *Server) handleDiscoveryHide(w http.ResponseWriter, r *http.Request) {
	var body discoveryWorkdirRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Workdir) == "" {
		writeError(w, http.StatusBadRequest, "workdir is required")
		return
	}
	if s.discovery == nil {
		writeError(w, http.StatusNotFound, "project discovery is not enabled")
		return
	}
	if err := s.discovery.Hide(body.Workdir); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.discoveryStatus())
}

func (s *Server) handleDiscoveryUnhide(w http.ResponseWriter, r *http.Request) {
	var body discoveryWorkdirRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Workdir) == "" {
		writeError(w, http.StatusBadRequest, "workdir is required")
		return
	}
	if s.discovery == nil {
		writeError(w, http.StatusNotFound, "project discovery is not enabled")
		return
	}
	if err := s.discovery.Unhide(body.Workdir); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.discoveryStatus())
}

func (s *Server) handleDiscoveryRescan(w http.ResponseWriter, r *http.Request) {
	if !decodeEmptyObjectBody(w, r) {
		return
	}
	if s.discovery == nil {
		writeError(w, http.StatusNotFound, "project discovery is not enabled")
		return
	}
	added, removed, err := s.discovery.Rescan(r.Context())
	if err != nil {
		s.log.Error("discovery rescan failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if added == nil {
		added = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	writeJSON(w, http.StatusOK, discoveryRescanResponse{Added: added, Removed: removed})
}

func decodeEmptyObjectBody(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body: "+err.Error())
		return false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return true
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil || body == nil {
		if err == nil {
			err = errors.New("body must be a JSON object")
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	if len(body) != 0 {
		writeError(w, http.StatusBadRequest, "invalid JSON body: object must be empty")
		return false
	}
	return true
}

func (s *Server) discoveryStatus() DiscoveryStatus {
	if s.discovery == nil {
		return DiscoveryStatus{
			Roots:   []string{},
			Exclude: []string{},
			Hidden:  []HiddenProject{},
		}
	}
	status := s.discovery.Status()
	if status.Roots == nil {
		status.Roots = []string{}
	}
	if status.Exclude == nil {
		status.Exclude = []string{}
	}
	if status.Hidden == nil {
		status.Hidden = []HiddenProject{}
	}
	return status
}
