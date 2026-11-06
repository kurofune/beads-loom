package discovery

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store persists the set of hidden discovered workdirs as JSON. A missing file
// is an empty set. Writes replace the file atomically.
type Store struct {
	path string
	mu   sync.Mutex
	set  map[string]bool
}

type storeData struct {
	Hidden []string `json:"hidden"`
}

// NewStore opens the hidden-workdir store. A corrupt file is an error.
func NewStore(path string) (*Store, error) {
	store := &Store{path: path, set: make(map[string]bool)}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}

	var persisted storeData
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, err
	}
	for _, workdir := range persisted.Hidden {
		store.set[workdir] = true
	}
	return store, nil
}

// Hidden returns a sorted copy of the hidden workdirs.
func (s *Store) Hidden() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedSet(s.set)
}

// IsHidden reports whether workdir is hidden.
func (s *Store) IsHidden(workdir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set[workdir]
}

// Hide adds workdir to the hidden set and persists it.
func (s *Store) Hide(workdir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set[workdir] {
		return nil
	}

	next := cloneSet(s.set)
	next[workdir] = true
	if err := s.persist(next); err != nil {
		return err
	}
	s.set = next
	return nil
}

// Unhide removes workdir from the hidden set and persists it.
func (s *Store) Unhide(workdir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set[workdir] {
		return nil
	}

	next := cloneSet(s.set)
	delete(next, workdir)
	if err := s.persist(next); err != nil {
		return err
	}
	s.set = next
	return nil
}

func (s *Store) persist(set map[string]bool) error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(s.path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := json.NewEncoder(tmp).Encode(storeData{Hidden: sortedSet(set)}); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func cloneSet(set map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(set))
	for value := range set {
		cloned[value] = true
	}
	return cloned
}

func sortedSet(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
