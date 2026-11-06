// Package discovery finds and manages beads workspaces outside Loom's explicit
// project configuration.
package discovery

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var builtinExcludes = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".Trash":       true,
}

// ScanOptions selects what to walk. Roots are absolute, ~-expanded paths.
type ScanOptions struct {
	Roots   []string
	Depth   int
	Exclude []string
}

// Found is one discovered beads workspace.
type Found struct {
	Name    string
	Workdir string
}

// Scan walks the roots and returns discovered workspaces sorted by Workdir.
// takenNames must not be assigned; takenWorkdirs are skipped entirely. Missing
// roots, unreadable directories, and invalid exclude patterns are ignored.
func Scan(opts ScanOptions, takenNames, takenWorkdirs map[string]bool) []Found {
	patterns := validPatterns(opts.Exclude)
	workdirs := make(map[string]bool)

	for _, configuredRoot := range opts.Roots {
		root, err := filepath.Abs(configuredRoot)
		if err != nil {
			continue
		}
		root = filepath.Clean(root)
		// A root that is itself a symlink (an iCloud/Dropbox-relocated
		// ~/Projects, say) is the user pointing at a real place; resolve it
		// rather than silently scanning nothing. Only the root gets this
		// treatment — symlinks BELOW a root stay skipped to avoid cycles, and
		// non-symlink roots keep their configured spelling.
		if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if resolved, err := filepath.EvalSymlinks(root); err == nil {
				root = resolved
			}
		}
		scanDir(root, root, 0, opts.Depth, patterns, takenWorkdirs, workdirs)
	}

	found := make([]Found, 0, len(workdirs))
	for workdir := range workdirs {
		found = append(found, Found{Workdir: workdir})
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].Workdir < found[j].Workdir
	})

	assigned := make(map[string]bool, len(takenNames)+len(found))
	for name := range takenNames {
		assigned[name] = true
	}
	for i := range found {
		found[i].Name = assignName(found[i].Workdir, assigned)
		assigned[found[i].Name] = true
	}
	return found
}

func scanDir(root, dir string, depth, maxDepth int, patterns []string, taken, found map[string]bool) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}

	if depth > 0 {
		rel, err := filepath.Rel(root, dir)
		if err != nil || excluded(filepath.ToSlash(rel), filepath.Base(dir), patterns) {
			return
		}
	}

	workdir := filepath.Clean(dir)
	if taken[workdir] {
		return
	}
	if hasMetadata(workdir) {
		found[workdir] = true
		return
	}
	if depth >= maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		child := filepath.Join(dir, entry.Name())
		childInfo, err := os.Lstat(child)
		if err != nil || !childInfo.IsDir() || childInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		scanDir(root, child, depth+1, maxDepth, patterns, taken, found)
	}
}

func hasMetadata(workdir string) bool {
	info, err := os.Stat(filepath.Join(workdir, ".beads", "metadata.json"))
	return err == nil && info.Mode().IsRegular()
}

func validPatterns(patterns []string) []string {
	valid := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err == nil {
			valid = append(valid, pattern)
		}
	}
	return valid
}

func excluded(rel, base string, patterns []string) bool {
	if strings.HasPrefix(base, ".") || builtinExcludes[base] {
		return true
	}
	for _, pattern := range patterns {
		candidate := base
		if strings.Contains(pattern, "/") {
			candidate = rel
		}
		matched, _ := path.Match(pattern, candidate)
		if matched {
			return true
		}
	}
	return false
}

func assignName(workdir string, assigned map[string]bool) string {
	base := sanitize(filepath.Base(workdir))
	if !assigned[base] {
		return base
	}

	parentBase := filepath.Base(filepath.Dir(workdir))
	candidate := sanitize(parentBase + "-" + filepath.Base(workdir))
	if !assigned[candidate] {
		return candidate
	}
	for suffix := 2; ; suffix++ {
		name := candidate + "-" + strconv.Itoa(suffix)
		if !assigned[name] {
			return name
		}
	}
}

func sanitize(name string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range name {
		allowed := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' || r == '.' || r == '_'
		if allowed {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	result := strings.Trim(b.String(), "-.")
	if result == "" {
		return "project"
	}
	return result
}
