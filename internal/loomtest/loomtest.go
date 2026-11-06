// Package loomtest holds constants shared between test packages that touch the
// live seeded beads database.
//
// It exists to make one coupling explicit: the write-path integration tests
// (internal/bdexec) create and delete temporary beads in the same database the
// read-path integration tests (internal/queries) iterate over. Both sides agree
// on ScratchTitlePrefix so the readers can tell fixture data from another
// test's in-flight scratch work.
package loomtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ScratchTitlePrefix marks a bead created by a test. Anything carrying it is
// temporary: it will be deleted before its test finishes and is never part of
// the seeded fixture.
const ScratchTitlePrefix = "[Scratch]"

// lockTimeout bounds how long a test waits for the fixture. Live-db tests run
// in seconds, so anything near this means a hung or crashed peer.
const lockTimeout = 90 * time.Second

// IsScratch reports whether a title belongs to a test's temporary bead.
func IsScratch(title string) bool {
	return strings.HasPrefix(strings.TrimSpace(title), ScratchTitlePrefix)
}

// RepoRoot walks up from the test's working directory to the module root — the
// beads workspace whose .beads/ every live test resolves, and the workdir `bd`
// is invoked against.
//
// A test binary always runs inside the module, so a failure here is a broken
// checkout rather than an absent environment: it fails, it does not skip.
func RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}

// LockFixture serializes tests that touch the live seeded database.
//
// `go test ./...` runs each package's test binary concurrently, so without this
// the write-path tests in internal/bdexec mutate rows out from under the
// read-path tests in internal/queries. The lock is held for the calling test
// and released automatically when it finishes.
//
// It does not — and cannot — exclude other writers such as an agent working in
// the same repo, so live-db tests must still tolerate concurrent change.
func LockFixture(t *testing.T, repoRoot string) {
	t.Helper()
	release, err := lockFixture(repoRoot)
	if err != nil {
		t.Fatalf("could not acquire the shared fixture lock: %v", err)
	}
	t.Cleanup(release)
}
