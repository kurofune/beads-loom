package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kurofune/beads-loom/internal/registry"
)

// integrationRoutes mounts every turned-on integration under
// /x/{provider}/, inside the project route and so inside the same middleware
// chain as core routes, behind requireIntegration.
func (s *Server) integrationRoutes(r chi.Router) {
	for _, p := range s.integrations {
		gate := s.requireIntegration(p.Name())
		r.Route("/x/"+p.Name(), func(r chi.Router) {
			r.Use(gate)
			p.Mount(r, s.deps)
		})
	}
}

// requireIntegration answers 404 unless the URL's project lists provider in
// its registry snapshot: an integration that does not apply to a project has
// no routes there, and a 404 says so more honestly than an empty answer.
func (s *Server) requireIntegration(provider string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := chi.URLParam(r, "project")
			p, err := s.reg.Get(name)
			if err != nil {
				if errors.Is(err, registry.ErrUnknownProject) {
					writeError(w, http.StatusNotFound, "no such project: "+name)
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if _, ok := p.Integrations[provider]; !ok {
				writeError(w, http.StatusNotFound,
					"project "+name+" does not have the "+provider+" integration")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// integrationsResponse renders a snapshot's integrations for the wire: never a
// null map or a null capability list.
func integrationsResponse(in map[string]registry.Capabilities) map[string]registry.Capabilities {
	out := make(map[string]registry.Capabilities, len(in))
	for name, caps := range in {
		if caps.Capabilities == nil {
			caps.Capabilities = []string{}
		}
		out[name] = caps
	}
	return out
}
