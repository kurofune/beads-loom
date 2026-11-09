package runs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// mkRepo makes dir a main checkout: a .git directory holding a HEAD file.
func mkRepo(t *testing.T, dir string) string {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return gitDir
}

// realDir resolves symlinks (macOS temp dirs live under /var -> /private/var)
// so paths compare equal to what the resolver returns.
func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGitCommonDirOfAMainCheckout(t *testing.T) {
	dir := realDir(t)
	gitDir := mkRepo(t, dir)

	got, err := GitCommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != gitDir {
		t.Errorf("GitCommonDir = %q, want %q", got, gitDir)
	}

	// A subdirectory of the work tree resolves to the same repo.
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := GitCommonDir(sub); err != nil || got != gitDir {
		t.Errorf("GitCommonDir(sub) = %q, %v; want %q", got, err, gitDir)
	}
}

func TestGitCommonDirOfALinkedWorktree(t *testing.T) {
	root := realDir(t)
	main := filepath.Join(root, "main")
	mainGit := mkRepo(t, main)

	wtGit := filepath.Join(mainGit, "worktrees", "wt")
	if err := os.MkdirAll(wtGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := GitCommonDir(linked)
	if err != nil {
		t.Fatal(err)
	}
	if got != mainGit {
		t.Errorf("GitCommonDir(linked) = %q, want the main checkout's %q", got, mainGit)
	}

	p := Project{Name: "djinn", Workdir: linked}
	dirs := p.SummonerLogDirs()
	want := filepath.Join(mainGit, "djinn", "summoner-logs")
	if len(dirs) != 2 || dirs[0] != want || dirs[1] != p.LegacySummonerLogDir() {
		t.Errorf("SummonerLogDirs = %v, want [%s %s]", dirs, want, p.LegacySummonerLogDir())
	}
}

func TestGitCommonDirOutsideARepo(t *testing.T) {
	dir := realDir(t)

	_, err := GitCommonDir(dir)
	if err == nil {
		// The temp dir happens to sit inside a repo on this machine; the
		// assertion below would be meaningless.
		t.Fatalf("GitCommonDir(%s) resolved; the temp dir must be in no repo", dir)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want one matching fs.ErrNotExist", err)
	}
	var nar *NotARepoError
	if !errors.As(err, &nar) {
		t.Errorf("err = %T, want *NotARepoError", err)
	}

	p := Project{Name: "djinn", Workdir: dir}
	if _, err := p.RunDir(); err == nil {
		t.Error("RunDir resolved outside a repo")
	}
	dirs := p.SummonerLogDirs()
	if len(dirs) != 1 || dirs[0] != p.LegacySummonerLogDir() {
		t.Errorf("SummonerLogDirs = %v, want only the legacy dir", dirs)
	}
}
