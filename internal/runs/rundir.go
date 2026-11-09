package runs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NotARepoError is GitCommonDir's answer for a directory in no git repo. It
// matches fs.ErrNotExist: a workdir outside every repo has no drain records
// under a git common dir, so readers treat it the same as a missing directory.
type NotARepoError struct{ Dir string }

func (e *NotARepoError) Error() string {
	return "resolve git common dir of " + e.Dir + ": not in a git repo"
}

// Unwrap makes errors.Is(err, fs.ErrNotExist) hold.
func (e *NotARepoError) Unwrap() error { return fs.ErrNotExist }

// GitCommonDir is the git common dir of the repo holding workdir, absolute.
//
// It is a port of djinn's rundir.resolve, so Loom finds drain records by the
// same rule djinn writes them: the nearest .git upward is either a git dir
// (a directory with a HEAD file) or a file naming one ("gitdir: <path>"),
// whose commondir file — present in a linked worktree's git dir — names the
// common dir relative to it. It reads the filesystem and runs no git: History
// resolves it on every /runs request and the watcher on every tick, and a
// GIT_DIR in Loom's environment must not point every project at one repo.
func GitCommonDir(workdir string) (string, error) {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", workdir, err)
	}
	for d := abs; ; {
		dotgit := filepath.Join(d, ".git")
		st, err := os.Stat(dotgit)
		switch {
		case err == nil && st.IsDir() && isGitDir(dotgit):
			return dotgit, nil
		case err == nil && st.Mode().IsRegular():
			return linkedCommonDir(d, dotgit)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("resolve git common dir of %s: %w", abs, err)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", &NotARepoError{Dir: abs}
		}
		d = parent
	}
}

// isGitDir reports whether dir holds a regular HEAD file, which tells a repo
// from a bare directory that happens to be named .git.
func isGitDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil && st.Mode().IsRegular()
}

// linkedCommonDir reads a .git file (a linked worktree's or a submodule's) at
// dotgit in work tree root and returns the common dir it leads to.
func linkedCommonDir(root, dotgit string) (string, error) {
	raw, err := os.ReadFile(dotgit)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dotgit, err)
	}
	own, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return "", fmt.Errorf("%s: no gitdir line", dotgit)
	}
	own = absUnder(root, strings.TrimSpace(own))
	c, err := os.ReadFile(filepath.Join(own, "commondir"))
	switch {
	case err == nil:
		return absUnder(own, strings.TrimSpace(string(c))), nil
	case errors.Is(err, fs.ErrNotExist):
		return own, nil
	default:
		return "", fmt.Errorf("read %s: %w", filepath.Join(own, "commondir"), err)
	}
}

// absUnder is p made absolute against base when it is relative.
func absUnder(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}
