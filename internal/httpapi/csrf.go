package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// CSRF hardening for mutating requests.
//
// Loom binds localhost, but that is not a security boundary: the attack runs in
// the user's own browser from any page they visit. `go-chi/cors` withholds
// *response* headers for a disallowed origin — it does not stop the request
// reaching the handler — so a cross-origin `fetch(..., {method:'POST',
// body:'{...}'})` would otherwise execute the write and merely hide a response
// the attacker never needed.
//
// Three checks, each closing a different door:
//
//  1. Content-Type must be application/json. A request with that header is not
//     a CORS "simple request", so the browser must preflight it, and the
//     preflight is answered against the origin allowlist. This is the load-
//     bearing one.
//  2. Origin, when present, must be in the allowlist or same-origin. Catches
//     anything that reaches us with a foreign origin regardless of preflight.
//  3. Host must be one the server expects. Defeats DNS rebinding, which makes
//     "it only binds localhost" untrue.
//
// Reads are untouched: they change nothing, and the SSE stream cannot carry a
// JSON content type.

// allowedHosts returns the Host header values this server accepts.
//
// The configured listen address is included with its own port, plus the
// loopback names on that port, since the user may reach Loom by either.
//
// NOTE on a non-loopback bind: `listen: 0.0.0.0:7333` allowlists only the
// loopback names, so a LAN client's writes are rejected with 403 while its reads
// work. That is deliberate fail-safe behaviour — a wildcard bind should not
// silently accept writes from any host that can route to it — but it means a
// deliberate LAN deployment must set `listen` to the specific interface address
// clients will use (e.g. `192.168.1.5:7333`), which allowlists that host. See
// the README's "Binding to a LAN address" section.
func allowedHosts(listen string) map[string]bool {
	out := map[string]bool{}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		// No port in the configured value; accept it verbatim and move on.
		if listen != "" {
			out[strings.ToLower(listen)] = true
		}
		return out
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		out[strings.ToLower(net.JoinHostPort(host, port))] = true
	}
	for _, h := range []string{"127.0.0.1", "localhost", "[::1]"} {
		out[h+":"+port] = true
	}
	return out
}

// isWriteMethod reports whether a method mutates.
func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// csrfGuard rejects mutating requests that fail any of the three checks.
func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWriteMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		// (3) Host — checked first, because a rebinding attack invalidates
		// every other assumption about who is talking to us.
		if len(s.allowedHosts) > 0 && r.Host != "" {
			if !s.allowedHosts[strings.ToLower(r.Host)] {
				writeError(w, http.StatusForbidden,
					"refusing a write for Host "+r.Host+"; Loom only accepts its configured listen address")
				return
			}
		}

		// (2) Origin, when the browser sent one.
		if origin := r.Header.Get("Origin"); origin != "" {
			if !s.originAllowed(origin, r) {
				writeError(w, http.StatusForbidden,
					"refusing a write from origin "+origin)
				return
			}
		}

		// (1) Content-Type must be JSON, which forces a preflight.
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			writeError(w, http.StatusUnsupportedMediaType,
				"writes require Content-Type: application/json")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether an Origin header may perform a write.
func (s *Server) originAllowed(origin string, r *http.Request) bool {
	for _, allowed := range DevOrigins {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	// Same-origin: the browser sends Origin on same-origin writes too, and the
	// production build is served from this very server.
	if host := originHost(origin); host != "" && strings.EqualFold(host, r.Host) {
		return true
	}
	// The configured listen address under either loopback name.
	if host := originHost(origin); host != "" && s.allowedHosts[strings.ToLower(host)] {
		return true
	}
	return false
}

// originHost extracts host:port from an origin URL without a full parse.
func originHost(origin string) string {
	if i := strings.Index(origin, "://"); i >= 0 {
		return origin[i+3:]
	}
	return ""
}

// isJSONContentType reports whether a Content-Type is application/json,
// tolerating parameters such as "; charset=utf-8".
func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}
