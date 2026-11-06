package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

// mountWeb serves the built frontend at the root, falling back to index.html
// for unknown paths so client-side routes survive a page reload.
func (s *Server) mountWeb(r chi.Router) {
	fileServer := http.FileServer(http.FS(s.webFS))

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		upath := strings.TrimPrefix(path.Clean("/"+req.URL.Path), "/")

		// index.html always goes through serveIndex so it gets no-cache; the
		// file server would happily let a browser pin a stale shell.
		if upath == "" || upath == "index.html" {
			serveIndex(w, req, s.webFS)
			return
		}

		if st, err := fs.Stat(s.webFS, upath); err == nil && !st.IsDir() {
			// Hashed asset filenames make these safe to cache hard; index.html
			// must not be, or a deploy never reaches the browser.
			if strings.HasPrefix(upath, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, req)
			return
		}

		// Unknown path: hand back the SPA shell.
		serveIndex(w, req, s.webFS)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	body, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
