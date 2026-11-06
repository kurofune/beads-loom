// Package buildinfo carries the version stamped into the binary at link time.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// These are set by the linker in `make build` / `make release`:
//
//	-X github.com/kurofune/beads-loom/internal/buildinfo.Version=v0.1.0
//
// A `go build` with no flags leaves them at their defaults, and Info() falls
// back to the VCS stamps the Go toolchain embeds automatically.
var (
	// Version is the release version, e.g. "v0.1.0".
	Version = "dev"
	// Commit is the git SHA the binary was built from.
	Commit = ""
	// Date is the build timestamp, RFC3339.
	Date = ""
)

// Build describes the running binary.
type Build struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	// Dirty reports that the working tree had uncommitted changes at build
	// time, when the toolchain could tell.
	Dirty bool `json:"dirty,omitempty"`
}

// Info returns the build description, filling gaps from the toolchain's own VCS
// stamps so an unstamped `go build` still reports something useful.
func Info() Build {
	b := Build{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if b.Commit == "" {
				b.Commit = s.Value
			}
		case "vcs.time":
			if b.Date == "" {
				b.Date = s.Value
			}
		case "vcs.modified":
			b.Dirty = s.Value == "true"
		}
	}
	return b
}

// ShortCommit is the first 12 characters of the commit, for display.
func (b Build) ShortCommit() string {
	if len(b.Commit) > 12 {
		return b.Commit[:12]
	}
	return b.Commit
}

// String renders a one-line version summary, e.g.
// "loom dev (3bd9bd4a1c2e, dirty) go1.26.4 darwin/arm64".
func (b Build) String() string {
	var sb strings.Builder
	sb.WriteString("loom ")
	sb.WriteString(b.Version)

	var parens []string
	if c := b.ShortCommit(); c != "" {
		parens = append(parens, c)
	}
	if b.Date != "" {
		parens = append(parens, b.Date)
	}
	if b.Dirty {
		parens = append(parens, "dirty")
	}
	if len(parens) > 0 {
		fmt.Fprintf(&sb, " (%s)", strings.Join(parens, ", "))
	}
	fmt.Fprintf(&sb, " %s %s/%s", b.Go, b.OS, b.Arch)
	return sb.String()
}
