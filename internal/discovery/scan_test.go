package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/kurofune/beads-loom/internal/config"
)

func TestScan(t *testing.T) {
	type scanCase struct {
		opts          ScanOptions
		takenNames    map[string]bool
		takenWorkdirs map[string]bool
		want          []Found
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string) scanCase
	}{
		{
			name: "finds workspace at root",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "Root Project")
				makeWorkspace(t, root)
				return scanCase{
					opts: ScanOptions{Roots: []string{root}, Depth: 0},
					want: []Found{{Name: "Root-Project", Workdir: root}},
				}
			},
		},
		{
			name: "finds at maximum depth and not below it",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				atLimit := filepath.Join(root, "one", "two")
				tooDeep := filepath.Join(root, "other", "two", "three")
				makeWorkspace(t, atLimit)
				makeWorkspace(t, tooDeep)
				return scanCase{
					opts: ScanOptions{Roots: []string{root}, Depth: 2},
					want: []Found{{Name: "two", Workdir: atLimit}},
				}
			},
		},
		{
			name: "skips symlink directories",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				target := filepath.Join(dir, "target")
				makeWorkspace(t, target)
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
					t.Fatal(err)
				}
				return scanCase{opts: ScanOptions{Roots: []string{root}, Depth: 2}, want: []Found{}}
			},
		},
		{
			name: "resolves a root that is itself a symlink",
			setup: func(t *testing.T, dir string) scanCase {
				target := filepath.Join(dir, "real-projects")
				workspace := filepath.Join(target, "app")
				makeWorkspace(t, workspace)
				link := filepath.Join(dir, "linked-root")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				// The scanner walks the resolved tree, so results carry the
				// resolved spelling (t.TempDir may live behind /var on macOS).
				resolved, err := filepath.EvalSymlinks(workspace)
				if err != nil {
					t.Fatal(err)
				}
				return scanCase{
					opts: ScanOptions{Roots: []string{link}, Depth: 2},
					want: []Found{{Name: "app", Workdir: resolved}},
				}
			},
		},
		{
			name: "skips dot directories and builtin excludes",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				for _, name := range []string{".secret", "node_modules", "vendor", "dist", "build", "target", ".Trash"} {
					makeWorkspace(t, filepath.Join(root, name, "project"))
				}
				visible := filepath.Join(root, "visible")
				makeWorkspace(t, visible)
				return scanCase{
					opts: ScanOptions{Roots: []string{root}, Depth: 2},
					want: []Found{{Name: "visible", Workdir: visible}},
				}
			},
		},
		{
			name: "applies base and path exclude globs and ignores invalid globs",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				makeWorkspace(t, filepath.Join(root, "ignored", "base"))
				makeWorkspace(t, filepath.Join(root, "groups", "archive", "path"))
				kept := filepath.Join(root, "groups", "active")
				makeWorkspace(t, kept)
				return scanCase{
					opts: ScanOptions{
						Roots:   []string{root},
						Depth:   3,
						Exclude: []string{"ignored", "groups/archive*", "[invalid"},
					},
					want: []Found{{Name: "active", Workdir: kept}},
				}
			},
		},
		{
			name: "stops descending inside a found workspace",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				parent := filepath.Join(root, "parent")
				makeWorkspace(t, parent)
				makeWorkspace(t, filepath.Join(parent, "nested"))
				return scanCase{
					opts: ScanOptions{Roots: []string{root}, Depth: 3},
					want: []Found{{Name: "parent", Workdir: parent}},
				}
			},
		},
		{
			name: "skips configured workdirs",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				taken := filepath.Join(root, "taken")
				kept := filepath.Join(root, "kept")
				makeWorkspace(t, taken)
				makeWorkspace(t, filepath.Join(taken, "nested"))
				makeWorkspace(t, kept)
				return scanCase{
					opts:          ScanOptions{Roots: []string{root}, Depth: 1},
					takenWorkdirs: map[string]bool{filepath.Clean(taken): true},
					want:          []Found{{Name: "kept", Workdir: kept}},
				}
			},
		},
		{
			name: "assigns sanitized collision names and numeric suffixes",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				first := filepath.Join(root, "one", "same", "My Proj!")
				second := filepath.Join(root, "two", "same", "My Proj!")
				makeWorkspace(t, first)
				makeWorkspace(t, second)
				workdirs := []string{first, second}
				sort.Strings(workdirs)
				return scanCase{
					opts:       ScanOptions{Roots: []string{root}, Depth: 3},
					takenNames: map[string]bool{"My-Proj": true, "same-My-Proj": true},
					want: []Found{
						{Name: "same-My-Proj-2", Workdir: workdirs[0]},
						{Name: "same-My-Proj-3", Workdir: workdirs[1]},
					},
				}
			},
		},
		{
			name: "deduplicates overlapping roots",
			setup: func(t *testing.T, dir string) scanCase {
				root := filepath.Join(dir, "root")
				project := filepath.Join(root, "group", "project")
				makeWorkspace(t, project)
				return scanCase{
					opts: ScanOptions{Roots: []string{root, filepath.Join(root, "group")}, Depth: 3},
					want: []Found{{Name: "project", Workdir: project}},
				}
			},
		},
		{
			name: "missing root is ignored",
			setup: func(t *testing.T, dir string) scanCase {
				return scanCase{opts: ScanOptions{Roots: []string{filepath.Join(dir, "missing")}, Depth: 3}, want: []Found{}}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setup := tc.setup(t, t.TempDir())
			got := Scan(setup.opts, setup.takenNames, setup.takenWorkdirs)
			if !reflect.DeepEqual(got, setup.want) {
				t.Errorf("Scan() = %#v, want %#v", got, setup.want)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "My Proj!", want: "My-Proj"},
		{input: "a---b", want: "a-b"},
		{input: "..hello..", want: "hello"},
		{input: "東京", want: "project"},
		{input: "a_b.c", want: "a_b.c"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			if got := sanitize(tc.input); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func makeWorkspace(t *testing.T, workdir string) {
	t.Helper()
	beadsDir := filepath.Join(workdir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", beadsDir, err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile metadata: %v", err)
	}
}

// A stranger runs loom inside the workspace they just ran `bd init` in, with no
// config file. The default roots include the working directory, and a root that
// is itself a workspace is found at depth 0.
func TestScanDefaultRootsFindsTheWorkingDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := filepath.Join(t.TempDir(), "my-tracker")
	makeWorkspace(t, workspace)
	t.Chdir(workspace)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	d := config.Default().Discovery
	got := Scan(ScanOptions{Roots: d.Roots, Depth: d.Depth, Exclude: d.Exclude}, nil, nil)

	want := []Found{{Name: "my-tracker", Workdir: cwd}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Scan(default roots) = %+v, want %+v", got, want)
	}
}
