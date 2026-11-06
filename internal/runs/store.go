package runs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// StateDirEnv overrides where Loom persists run records.
const StateDirEnv = "LOOM_STATE_DIR"

// DefaultStateDir returns ~/.local/state/loom, honouring LOOM_STATE_DIR.
func DefaultStateDir() string {
	if p := os.Getenv(StateDirEnv); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".loom-state")
	}
	return filepath.Join(home, ".local", "state", "loom")
}

// Store persists Loom-spawned run records as JSON under
// <root>/runs/<project>/<runId>.json.
//
// External runs are never persisted: they are reconstructed from djinn's own
// logs on demand, so there is only ever one source of truth for them.
type Store struct {
	root string
	mu   sync.RWMutex
}

// NewStore returns a Store rooted at dir. The directory is created lazily.
func NewStore(dir string) *Store {
	if dir == "" {
		dir = DefaultStateDir()
	}
	return &Store{root: dir}
}

// Root returns the state directory.
func (s *Store) Root() string { return s.root }

// RunDir is where a run's artifacts (console capture, record) live.
func (s *Store) RunDir(project, runID string) (string, error) {
	if !ValidID(project) || !ValidID(runID) {
		return "", fmt.Errorf("invalid project or run id")
	}
	return filepath.Join(s.root, "runs", project, runID), nil
}

// ConsolePath is where Loom captures the run's console stream.
//
// summoner writes its own lifecycle log under the project's .djinn; this is
// Loom's separate capture of stdout+stderr, so a run remains readable even if
// the project directory is cleaned.
func (s *Store) ConsolePath(project, runID string) (string, error) {
	dir, err := s.RunDir(project, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "console.log"), nil
}

func (s *Store) recordPath(project, runID string) (string, error) {
	dir, err := s.RunDir(project, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "run.json"), nil
}

// Save writes a run record, creating its directory.
func (s *Store) Save(run Run) error {
	path, err := s.recordPath(run.Project, run.ID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run: %w", err)
	}

	// Write-then-rename so a crash mid-write cannot leave a truncated record
	// that would fail to parse on every subsequent history read.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write run record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit run record: %w", err)
	}
	return nil
}

// Load reads one persisted run.
func (s *Store) Load(project, runID string) (*Run, error) {
	path, err := s.recordPath(project, runID)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read run record: %w", err)
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("parse run record %s: %w", runID, err)
	}
	return &run, nil
}

// List returns every persisted run for a project, newest first.
//
// An unreadable individual record is skipped rather than failing the whole
// listing: one corrupt file must not hide the rest of the history.
func (s *Store) List(project string) ([]Run, error) {
	if !ValidID(project) {
		return nil, fmt.Errorf("invalid project name %q", project)
	}
	dir := filepath.Join(s.root, "runs", project)

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list runs: %w", err)
	}

	var out []Run
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "run.json"))
		if err != nil {
			continue
		}
		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		out = append(out, run)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// NewRunID builds a run id from a start timestamp. Ids sort chronologically and
// are safe as a path segment.
func NewRunID(unixNano int64) string {
	return fmt.Sprintf("run-%d", unixNano)
}

// IsLoomRunID reports whether an id was minted by Loom rather than synthesized
// for an external run.
func IsLoomRunID(id string) bool { return strings.HasPrefix(id, "run-") }
