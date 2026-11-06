package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

// Malformed-query rejection, applied once to every /api route.
//
// Go's net/url does not accept ';' as a parameter separator (it was removed in
// Go 1.17). Rather than failing, url.ParseQuery DROPS every segment containing
// one and reports an error out of band — and r.URL.Query(), which every handler
// here calls, throws that error away. The result is a request the server reads
// as having *no* parameters:
//
//	GET /api/projects/loom/issues?epic=;DROP%20TABLE
//	  -> r.URL.Query() == url.Values{}
//
// Every per-handler guard then passes vacuously: the unknown-parameter loop
// iterates zero keys, bdexec.ValidIssueID never sees an epic, and the endpoint
// answers 200 with the full unscoped backlog while the UI still renders its
// "scoped to this epic" chip. Silently-wrong data is worse than an error.
//
// The same reasoning covers a bad percent-escape (?epic=%zz), which ParseQuery
// drops the same way. So the guard is not "reject semicolons" but the general
// invariant: if the server cannot parse the query the client sent, it must say
// so instead of acting on a truncated view of it.
//
// It lives in middleware rather than in filterFromQuery because the bug is not
// specific to /issues — it turns EVERY query-parameter guard into a no-op, so
// the fix has to be inherited by endpoints that have not been written yet.

// rejectMalformedQuery rejects any request whose raw query string does not
// parse, so no handler ever sees a silently-truncated set of parameters.
func rejectMalformedQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := r.URL.RawQuery; raw != "" {
			if _, err := url.ParseQuery(raw); err != nil {
				writeError(w, http.StatusBadRequest, malformedQueryMessage(err))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// malformedQueryMessage turns a url.ParseQuery error into something the caller
// can act on. The semicolon case gets its own sentence because it is the one a
// human hits by hand-editing a URL, and the remedy is not obvious.
func malformedQueryMessage(err error) string {
	msg := "malformed query string: " + err.Error()
	if strings.Contains(err.Error(), "semicolon") {
		msg += " (separate parameters with '&'; a literal ';' inside a value must be encoded as %3B)"
	}
	return msg + ". Parameters were not applied, so the request was rejected rather than answered with unfiltered data"
}
