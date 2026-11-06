package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	cfg := Default()
	if cfg.Listen != DefaultListen {
		t.Errorf("listen = %q, want %q", cfg.Listen, DefaultListen)
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty for built-in default", cfg.Path)
	}
	if len(cfg.Projects) != 0 {
		t.Errorf("got %d projects, want none: the default must not name anyone's projects", len(cfg.Projects))
	}
	d := cfg.Discovery
	if d == nil || !d.Enabled {
		t.Fatalf("discovery = %+v, want enabled", d)
	}
	if want := []string{cwd, home}; !slices.Equal(d.Roots, want) {
		t.Errorf("discovery roots = %q, want %q", d.Roots, want)
	}
	if d.Depth != 3 {
		t.Errorf("discovery depth = %d, want 3", d.Depth)
	}
}

// The shipped default is what a stranger gets. It must not carry the paths or
// project names of the machine it was written on.
func TestWriteDefaultNamesNoOneElsesProjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := WriteDefault(path); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, banned := range []string{"/Users/", "~/Projects/", "nathangross", "djinn", "Project-Pheonix"} {
		if strings.Contains(string(data), banned) {
			t.Errorf("default config contains %q:\n%s", banned, data)
		}
	}
}

// README's configuration reference documents the shipped default. Deriving the
// block from DefaultYAML keeps the two from drifting apart.
func TestReadmeShowsTheDefaultDiscoveryBlock(t *testing.T) {
	start := strings.Index(DefaultYAML, "\ndiscovery:\n")
	if start < 0 {
		t.Fatal("DefaultYAML has no top-level discovery: block")
	}
	block := DefaultYAML[start+1:]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end+1]
	}
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("ReadFile README.md: %v", err)
	}
	if !strings.Contains(string(readme), block) {
		t.Errorf("README.md does not contain DefaultYAML's discovery block verbatim:\n%s", block)
	}
}

func TestLoad(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		name string
		// yaml is written to a temp file. When absent is true no file is
		// written and the path is passed anyway.
		yaml    string
		absent  bool
		wantErr string // substring; empty means success expected
		check   func(t *testing.T, c *Config)
	}{
		{
			name:   "missing file falls back to default",
			absent: true,
			check: func(t *testing.T, c *Config) {
				if len(c.Projects) != 0 || c.Discovery == nil || !c.Discovery.Enabled {
					t.Errorf("got %d projects, discovery %+v; want the default: none, discovery on",
						len(c.Projects), c.Discovery)
				}
			},
		},
		{
			name: "minimal config gets default listen",
			yaml: "projects:\n  - name: a\n    workdir: /tmp/a\n",
			check: func(t *testing.T, c *Config) {
				if c.Listen != DefaultListen {
					t.Errorf("listen = %q, want %q", c.Listen, DefaultListen)
				}
				if c.Path == "" {
					t.Error("Path should record the file it was loaded from")
				}
			},
		},
		{
			name: "tilde in workdir is expanded",
			yaml: "projects:\n  - name: a\n    workdir: ~/Projects/a\n",
			check: func(t *testing.T, c *Config) {
				want := filepath.Join(home, "Projects", "a")
				if c.Projects[0].Workdir != want {
					t.Errorf("workdir = %q, want %q", c.Projects[0].Workdir, want)
				}
			},
		},
		{
			// Unquoted ~ is YAML null, so home has to be written as "~".
			name: "quoted bare tilde expands to home",
			yaml: "projects:\n  - name: a\n    workdir: \"~\"\n",
			check: func(t *testing.T, c *Config) {
				if c.Projects[0].Workdir != home {
					t.Errorf("workdir = %q, want %q", c.Projects[0].Workdir, home)
				}
			},
		},
		{
			name: "server defaults are filled in",
			yaml: "servers:\n  - name: shared\n    port: 3308\nprojects:\n  - name: a\n    workdir: /tmp/a\n    server: shared\n",
			check: func(t *testing.T, c *Config) {
				s, ok := c.ServerByName("shared")
				if !ok {
					t.Fatal("server shared not found")
				}
				if s.Kind != KindRemote {
					t.Errorf("kind = %q, want %q", s.Kind, KindRemote)
				}
				if s.Host != "127.0.0.1" {
					t.Errorf("host = %q, want 127.0.0.1", s.Host)
				}
				if s.User != "root" {
					t.Errorf("user = %q, want root", s.User)
				}
			},
		},
		{
			name: "explicit listen is preserved",
			yaml: "listen: 0.0.0.0:9000\nprojects:\n  - name: a\n    workdir: /tmp/a\n",
			check: func(t *testing.T, c *Config) {
				if c.Listen != "0.0.0.0:9000" {
					t.Errorf("listen = %q", c.Listen)
				}
			},
		},
		{
			name: "discovery block is parsed and roots are expanded",
			yaml: "projects: []\ndiscovery:\n  enabled: true\n  roots:\n    - ~/Projects\n  depth: 4\n  exclude:\n    - archived/*\n  interval: 45s\n",
			check: func(t *testing.T, c *Config) {
				if c.Discovery == nil {
					t.Fatal("Discovery is nil")
				}
				wantRoot := filepath.Join(home, "Projects")
				if len(c.Discovery.Roots) != 1 || c.Discovery.Roots[0] != wantRoot {
					t.Errorf("roots = %v, want [%s]", c.Discovery.Roots, wantRoot)
				}
				if c.Discovery.Depth != 4 || c.Discovery.IntervalDuration() != 45*time.Second {
					t.Errorf("discovery = %+v", c.Discovery)
				}
				if len(c.Discovery.Exclude) != 1 || c.Discovery.Exclude[0] != "archived/*" {
					t.Errorf("exclude = %v", c.Discovery.Exclude)
				}
			},
		},
		{
			name: "discovery defaults allow zero projects",
			yaml: "projects: []\ndiscovery:\n  enabled: true\n  roots: [/tmp/projects]\n",
			check: func(t *testing.T, c *Config) {
				if c.Discovery.Depth != 3 {
					t.Errorf("depth = %d, want 3", c.Discovery.Depth)
				}
				if c.Discovery.Interval != "5m" || c.Discovery.IntervalDuration() != 5*time.Minute {
					t.Errorf("interval = %q (%s), want 5m", c.Discovery.Interval, c.Discovery.IntervalDuration())
				}
			},
		},
		{
			name: "disabled discovery skips validation",
			yaml: "projects:\n  - name: a\n    workdir: /tmp/a\ndiscovery:\n  enabled: false\n  depth: 11\n  interval: nonsense\n",
		},
		{
			name:    "malformed yaml",
			yaml:    "listen: [unclosed\nprojects:\n",
			wantErr: "parse",
		},
		{
			name:    "wrong type for projects",
			yaml:    "projects: 12\n",
			wantErr: "parse",
		},
		{
			name:    "no projects",
			yaml:    "listen: 127.0.0.1:1\nprojects: []\n",
			wantErr: "no projects configured",
		},
		{
			name:    "discovery enabled without roots",
			yaml:    "projects: []\ndiscovery:\n  enabled: true\n",
			wantErr: "at least one root is required",
		},
		{
			name:    "discovery depth too large",
			yaml:    "projects: []\ndiscovery:\n  enabled: true\n  roots: [/tmp]\n  depth: 11\n",
			wantErr: "depth must be between 1 and 10",
		},
		{
			name:    "discovery interval is invalid",
			yaml:    "projects: []\ndiscovery:\n  enabled: true\n  roots: [/tmp]\n  interval: later\n",
			wantErr: "interval",
		},
		{
			name:    "discovery interval is too short",
			yaml:    "projects: []\ndiscovery:\n  enabled: true\n  roots: [/tmp]\n  interval: 29s\n",
			wantErr: "at least 30s",
		},
		{
			name:    "project without workdir",
			yaml:    "projects:\n  - name: a\n",
			wantErr: "workdir is required",
		},
		{
			name:    "project without name",
			yaml:    "projects:\n  - workdir: /tmp/a\n",
			wantErr: "empty name",
		},
		{
			name:    "duplicate project names",
			yaml:    "projects:\n  - name: a\n    workdir: /tmp/a\n  - name: a\n    workdir: /tmp/b\n",
			wantErr: `duplicate project "a"`,
		},
		{
			name:    "unknown server reference",
			yaml:    "projects:\n  - name: a\n    workdir: /tmp/a\n    server: nope\n",
			wantErr: `unknown server "nope"`,
		},
		{
			name:    "bad server kind",
			yaml:    "servers:\n  - name: s\n    port: 1\n    kind: sideways\nprojects:\n  - name: a\n    workdir: /tmp/a\n",
			wantErr: "kind must be",
		},
		{
			name:    "remote server without port",
			yaml:    "servers:\n  - name: s\n    kind: remote\nprojects:\n  - name: a\n    workdir: /tmp/a\n",
			wantErr: "port 0 out of range",
		},
		{
			name:    "duplicate server names",
			yaml:    "servers:\n  - name: s\n    port: 1\n  - name: s\n    port: 2\nprojects:\n  - name: a\n    workdir: /tmp/a\n",
			wantErr: `duplicate server "s"`,
		},
		{
			name:    "unsupported ~user workdir",
			yaml:    "projects:\n  - name: a\n    workdir: ~someone/x\n",
			wantErr: "~user paths are not supported",
		},
		{
			name:    "negative max_workers",
			yaml:    "projects:\n  - name: a\n    workdir: /tmp/a\n    summoner:\n      max_workers: -1\n",
			wantErr: "max_workers must be >= 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if !tc.absent {
				if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			}

			cfg, err := Load(path)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() = nil error, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				// Every error must name the file the user has to fix.
				if !strings.Contains(err.Error(), path) {
					t.Errorf("error = %q, want it to carry the config path %q", err, path)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}

func TestLoadUsesEnvWhenPathEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.yaml")
	if err := os.WriteFile(path, []byte("listen: 1.2.3.4:5\nprojects:\n  - name: z\n    workdir: /tmp/z\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(EnvConfigPath, path)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "1.2.3.4:5" {
		t.Errorf("listen = %q, want 1.2.3.4:5", cfg.Listen)
	}
}

func TestLoadDoesNotWriteWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Load wrote %d entries to disk; it must write nothing", len(entries))
	}
}

func TestWriteDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	written, err := WriteDefault(path)
	if err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	if written != path {
		t.Errorf("written = %q, want %q", written, path)
	}

	// What we wrote must load back into the same config Default() produces.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after WriteDefault: %v", err)
	}
	def := Default()
	if !reflect.DeepEqual(cfg.Projects, def.Projects) || !reflect.DeepEqual(cfg.Discovery, def.Discovery) {
		t.Errorf("round-trip = projects %+v discovery %+v, want projects %+v discovery %+v",
			cfg.Projects, cfg.Discovery, def.Projects, def.Discovery)
	}

	if _, err := WriteDefault(path); err == nil {
		t.Error("WriteDefault over an existing file should fail")
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: "~", want: home},
		{in: "~/a/b", want: filepath.Join(home, "a", "b")},
		{in: "/abs/path/", want: "/abs/path"},
		{in: "rel/path", want: filepath.Join(cwd, "rel", "path")},
		{in: ".", want: cwd},
		{in: "~root/x", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ExpandPath(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ExpandPath(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExpandPath(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
