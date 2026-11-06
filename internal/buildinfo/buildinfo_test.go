package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestInfoDefaults(t *testing.T) {
	b := Info()
	if b.Version == "" {
		t.Error("Version must never be empty")
	}
	if b.Go != runtime.Version() {
		t.Errorf("Go = %q, want %q", b.Go, runtime.Version())
	}
	if b.OS != runtime.GOOS || b.Arch != runtime.GOARCH {
		t.Errorf("platform = %s/%s, want %s/%s", b.OS, b.Arch, runtime.GOOS, runtime.GOARCH)
	}
}

func TestShortCommit(t *testing.T) {
	tests := []struct {
		commit string
		want   string
	}{
		{"", ""},
		{"abc123", "abc123"},
		{"feb8614c537b9b6c056aeb4085adfb58ee8b2fae", "feb8614c537b"},
		{"123456789012", "123456789012"},
	}
	for _, tc := range tests {
		t.Run(tc.commit, func(t *testing.T) {
			if got := (Build{Commit: tc.commit}).ShortCommit(); got != tc.want {
				t.Errorf("ShortCommit() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildString(t *testing.T) {
	tests := []struct {
		name        string
		build       Build
		wantHas     []string
		wantMissing []string
	}{
		{
			name:    "fully stamped release",
			build:   Build{Version: "v0.1.0", Commit: "feb8614c537b9b6c", Date: "2026-08-01T18:44:49Z", Go: "go1.26.4", OS: "darwin", Arch: "arm64"},
			wantHas: []string{"loom v0.1.0", "feb8614c537b", "2026-08-01T18:44:49Z", "go1.26.4 darwin/arm64"},
			// A clean tree must not claim to be dirty.
			wantMissing: []string{"dirty"},
		},
		{
			name:    "dirty tree is called out",
			build:   Build{Version: "dev", Commit: "abc123def456", Dirty: true, Go: "go1.26.4", OS: "linux", Arch: "amd64"},
			wantHas: []string{"loom dev", "abc123def456", "dirty", "linux/amd64"},
		},
		{
			name:        "unstamped build still renders",
			build:       Build{Version: "dev", Go: "go1.26.4", OS: "darwin", Arch: "arm64"},
			wantHas:     []string{"loom dev", "go1.26.4 darwin/arm64"},
			wantMissing: []string{"("},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.build.String()
			for _, want := range tc.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("String() = %q, missing %q", got, want)
				}
			}
			for _, unwanted := range tc.wantMissing {
				if strings.Contains(got, unwanted) {
					t.Errorf("String() = %q, should not contain %q", got, unwanted)
				}
			}
		})
	}
}
