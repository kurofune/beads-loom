package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewStore(t *testing.T) {
	tests := []struct {
		name    string
		content *string
		want    []string
		wantErr bool
	}{
		{name: "missing file is empty", want: []string{}},
		{name: "loads and sorts hidden workdirs", content: stringPtr(`{"hidden":["/z","/a"]}`), want: []string{"/a", "/z"}},
		{name: "corrupt file is an error", content: stringPtr(`{"hidden":`), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hidden.json")
			if tc.content != nil {
				if err := os.WriteFile(path, []byte(*tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			store, err := NewStore(path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("NewStore() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			if got := store.Hidden(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Hidden() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStoreRoundTripAndIdempotence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "hidden.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	operations := []struct {
		name string
		run  func() error
		want []string
	}{
		{name: "hide z", run: func() error { return store.Hide("/z") }, want: []string{"/z"}},
		{name: "hide a", run: func() error { return store.Hide("/a") }, want: []string{"/a", "/z"}},
		{name: "hide a again", run: func() error { return store.Hide("/a") }, want: []string{"/a", "/z"}},
		{name: "unhide z", run: func() error { return store.Unhide("/z") }, want: []string{"/a"}},
		{name: "unhide z again", run: func() error { return store.Unhide("/z") }, want: []string{"/a"}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err != nil {
				t.Fatalf("operation: %v", err)
			}
			if got := store.Hidden(); !reflect.DeepEqual(got, operation.want) {
				t.Errorf("Hidden() = %v, want %v", got, operation.want)
			}
		})
	}

	if !store.IsHidden("/a") || store.IsHidden("/z") {
		t.Errorf("IsHidden state does not match Hidden: %v", store.Hidden())
	}
	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got, want := reopened.Hidden(), []string{"/a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reopened Hidden() = %v, want %v", got, want)
	}
}

func TestStoreWriteIsReadableAfterHide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hidden.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Hide("/workspace"); err != nil {
		t.Fatalf("Hide: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after Hide: %v", err)
	}
	var persisted storeData
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("persisted file is not valid JSON: %v", err)
	}
	if want := []string{"/workspace"}; !reflect.DeepEqual(persisted.Hidden, want) {
		t.Errorf("persisted hidden = %v, want %v", persisted.Hidden, want)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Errorf("atomic write left unexpected files: %v", entries)
	}
}

func stringPtr(value string) *string {
	return &value
}
