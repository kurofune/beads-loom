// Package webembed carries the built frontend inside the binary.
//
// The Vite build writes to web/dist, which go:embed cannot reach from here
// (embed patterns may not escape the package directory), so `make build` copies
// web/dist to internal/webembed/dist before compiling. A committed .gitkeep
// keeps the directory non-empty, because go:embed fails to compile against a
// pattern that matches nothing — which means `go build ./...` works with no
// frontend present at all, and Present() reports false.
package webembed

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the built frontend rooted at its index, and whether a real build
// is present. When present is false the caller should serve the API only.
func FS() (fsys fs.FS, present bool) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		// Only the .gitkeep placeholder is in there.
		return nil, false
	}
	return sub, true
}
