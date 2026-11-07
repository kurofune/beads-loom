// Package config loads Loom's YAML configuration.
//
// Loom is usable with no config file at all: Load returns a default
// configuration that discovers beads workspaces when the file is absent. Writing that default to disk is an explicit user action
// (--write-default-config), never a side effect of loading.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultListen is the address the API server binds when nothing overrides it.
const DefaultListen = "127.0.0.1:7333"

// ServerKind distinguishes auto-resolved per-project dolt servers from servers
// pinned to a fixed host:port in the config file.
const (
	KindLocal  = "local"
	KindRemote = "remote"
)

// Config is the whole of ~/.config/loom/config.yaml.
type Config struct {
	Listen    string     `yaml:"listen"`
	Servers   []Server   `yaml:"servers,omitempty"`
	Projects  []Project  `yaml:"projects"`
	Discovery *Discovery `yaml:"discovery,omitempty"`
	// Integrations holds the global switches for optional integrations.
	Integrations *Integrations `yaml:"integrations,omitempty"`

	// Path records where this config was loaded from. Empty when the config is
	// the built-in default (no file on disk).
	Path string `yaml:"-"`
}

// Discovery configures automatic project discovery. Nil or Enabled=false
// disables it entirely.
type Discovery struct {
	Enabled  bool     `yaml:"enabled"`
	Roots    []string `yaml:"roots,omitempty"`
	Depth    int      `yaml:"depth,omitempty"`
	Exclude  []string `yaml:"exclude,omitempty"`
	Interval string   `yaml:"interval,omitempty"`
}

// IntervalDuration returns the parsed interval (default 5m). Call only after
// successful Load/parse (validation guarantees it parses).
func (d *Discovery) IntervalDuration() time.Duration {
	if d == nil || d.Interval == "" {
		return 5 * time.Minute
	}
	duration, _ := time.ParseDuration(d.Interval)
	return duration
}

// Server is a dolt sql-server reachable at a fixed address. Only remote servers
// need an entry here; local projects resolve their port from .beads/ at query
// time because bd assigns ports dynamically.
type Server struct {
	Name     string `yaml:"name"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user,omitempty"`
	Password string `yaml:"password,omitempty"`
	Kind     string `yaml:"kind,omitempty"`
}

// Project is one beads workspace.
type Project struct {
	Name    string `yaml:"name"`
	Workdir string `yaml:"workdir"`
	// Database overrides dolt_database from <workdir>/.beads/metadata.json.
	Database string `yaml:"database,omitempty"`
	// Server names an entry in Config.Servers. Empty means "resolve locally
	// from <workdir>/.beads/dolt-server.port".
	Server string `yaml:"server,omitempty"`
	// Summoner is the legacy spelling of Integrations.Djinn. It still decodes;
	// DjinnSettings decides which one applies.
	Summoner     *Summoner            `yaml:"summoner,omitempty"`
	Integrations *ProjectIntegrations `yaml:"integrations,omitempty"`
}

// ProjectIntegrations holds one project's settings for each optional
// integration.
type ProjectIntegrations struct {
	Djinn *Summoner `yaml:"djinn,omitempty"`
}

// DjinnSettings returns the project's djinn dispatch defaults, or nil when it
// sets none. integrations.djinn wins over the legacy summoner block as a whole
// block: the two are never merged field by field, so what the file says under
// integrations.djinn is exactly what applies.
func (p Project) DjinnSettings() *Summoner {
	if p.Integrations != nil && p.Integrations.Djinn != nil {
		return p.Integrations.Djinn
	}
	return p.Summoner
}

// Values of integrations.djinn.enabled.
const (
	DjinnAuto = "auto"
	DjinnOn   = "true"
	DjinnOff  = "false"
)

// Integrations is the global integrations block.
type Integrations struct {
	Djinn *DjinnIntegration `yaml:"djinn,omitempty"`
}

// DjinnIntegration is the global djinn switch. Enabled is auto (the default),
// true or false. auto and true both turn the integration on, and each
// project's capabilities still depend on what that project has; false turns
// off its routes, watchers and capabilities everywhere.
// An omitted enabled key means auto; a key present with an empty or null value
// is a load error, because an operator who blanks it means off, not auto.
type DjinnIntegration struct {
	Enabled string `yaml:"enabled,omitempty"`

	enabledKeyPresent bool
}

// UnmarshalYAML decodes the block and records whether the enabled key was
// written, which a plain string cannot tell apart from an omitted key. The
// presence check decodes the same node into a map, so a key supplied through a
// YAML merge (<<) counts exactly as it does for the decoded value.
func (d *DjinnIntegration) UnmarshalYAML(node *yaml.Node) error {
	type plain DjinnIntegration
	if err := node.Decode((*plain)(d)); err != nil {
		return err
	}
	var keys map[string]any
	if err := node.Decode(&keys); err != nil {
		return err
	}
	_, d.enabledKeyPresent = keys["enabled"]
	return nil
}

// DjinnMode returns integrations.djinn.enabled, defaulting to auto.
func (c *Config) DjinnMode() string {
	if c.Integrations == nil || c.Integrations.Djinn == nil || c.Integrations.Djinn.Enabled == "" {
		return DjinnAuto
	}
	return c.Integrations.Djinn.Enabled
}

// DjinnEnabled reports whether the djinn integration is on at all.
func (c *Config) DjinnEnabled() bool { return c.DjinnMode() != DjinnOff }

// Summoner holds per-project defaults for dispatching djinn workers.
type Summoner struct {
	MaxWorkers int  `yaml:"max_workers,omitempty"`
	MaxBeads   int  `yaml:"max_beads,omitempty"`
	NoPush     bool `yaml:"no_push,omitempty"`
}

// DefaultYAML is the config shipped when the user has none. It names no
// projects: discovery finds the beads workspaces under the directory Loom
// starts in and under the home directory. --write-default-config writes exactly
// these bytes, comments included, so the file is a usable starting point.
const DefaultYAML = `# Loom configuration. See README.md, "Configuration reference".
listen: 127.0.0.1:7333

# servers: only needed for REMOTE dolt servers at a fixed host:port.
# Local projects auto-resolve their port from <workdir>/.beads/dolt-server.port
# at query time, because bd assigns ports dynamically across restarts.
#
# servers:
#   - name: shared
#     host: 127.0.0.1
#     port: 3308
#     user: root
#     password: ""
#     kind: remote

# projects: optional. Discovery (below) already finds every beads workspace it
# can reach. List a project here to pin its name, or to add one outside the
# discovery roots.
#
# projects:
#   - name: myproject
#     workdir: ~/code/myproject
#     # database: myproject  # default: dolt_database from .beads/metadata.json
#     # server: shared       # set => use that configured server

discovery:
  enabled: true
  roots:
    - "."      # the directory loom was started in
    - "~"      # your home directory
  depth: 3     # directory levels below each root
  interval: 5m
`

// EnvConfigPath is the environment variable that overrides the config location.
const EnvConfigPath = "LOOM_CONFIG"

// DefaultPath returns the config path to use when the user passed no --config:
// $LOOM_CONFIG if set, else ~/.config/loom/config.yaml.
func DefaultPath() string {
	if p := os.Getenv(EnvConfigPath); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "loom", "config.yaml")
	}
	return filepath.Join(home, ".config", "loom", "config.yaml")
}

// Default returns the built-in configuration (DefaultYAML, parsed and
// normalized). It never touches the filesystem beyond expanding ~ and
// resolving relative roots against the working directory.
func Default() *Config {
	cfg, err := parse([]byte(DefaultYAML), "")
	if err != nil {
		// DefaultYAML is a compile-time constant; a failure here is a bug.
		panic("config: built-in default is invalid: " + err.Error())
	}
	return cfg
}

// Load reads the config at path. A missing file is not an error: it yields the
// built-in default and nothing is written to disk. Parse and validation errors
// always carry path so the user knows which file to fix.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	expanded, err := ExpandPath(path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return nil, fmt.Errorf("config %s: %w", expanded, err)
	}
	return parse(data, expanded)
}

// WriteDefault writes DefaultYAML to path, creating parent directories. It
// refuses to clobber an existing file.
func WriteDefault(path string) (string, error) {
	if path == "" {
		path = DefaultPath()
	}
	expanded, err := ExpandPath(path)
	if err != nil {
		return "", fmt.Errorf("config %s: %w", path, err)
	}
	if _, err := os.Stat(expanded); err == nil {
		return expanded, fmt.Errorf("config %s: already exists, refusing to overwrite", expanded)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("config %s: %w", expanded, err)
	}
	if err := os.MkdirAll(filepath.Dir(expanded), 0o755); err != nil {
		return "", fmt.Errorf("config %s: %w", expanded, err)
	}
	if err := os.WriteFile(expanded, []byte(DefaultYAML), 0o644); err != nil {
		return "", fmt.Errorf("config %s: %w", expanded, err)
	}
	return expanded, nil
}

func parse(data []byte, path string) (*Config, error) {
	label := path
	if label == "" {
		label = "<built-in default>"
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config %s: parse: %w", label, err)
	}
	cfg.Path = path

	if strings.TrimSpace(cfg.Listen) == "" {
		cfg.Listen = DefaultListen
	}

	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		if s.Kind == "" {
			s.Kind = KindRemote
		}
		if s.Host == "" {
			s.Host = "127.0.0.1"
		}
		if s.User == "" {
			s.User = "root"
		}
	}

	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		wd, err := ExpandPath(p.Workdir)
		if err != nil {
			return nil, fmt.Errorf("config %s: project %q: workdir: %w", label, p.Name, err)
		}
		p.Workdir = wd
	}

	if cfg.Discovery != nil {
		for i := range cfg.Discovery.Roots {
			root, err := ExpandPath(cfg.Discovery.Roots[i])
			if err != nil {
				return nil, fmt.Errorf("config %s: discovery root %q: %w", label, cfg.Discovery.Roots[i], err)
			}
			cfg.Discovery.Roots[i] = root
		}
		if cfg.Discovery.Depth == 0 {
			cfg.Discovery.Depth = 3
		}
		if cfg.Discovery.Interval == "" {
			cfg.Discovery.Interval = "5m"
		}
	}

	if err := cfg.validate(label); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate(label string) error {
	if d := c.Integrations; d != nil && d.Djinn != nil && d.Djinn.enabledKeyPresent && d.Djinn.Enabled == "" {
		return fmt.Errorf("config %s: integrations.djinn.enabled is empty; set it to %q, %q or %q, or remove the key for %q",
			label, DjinnAuto, DjinnOn, DjinnOff, DjinnAuto)
	}
	switch mode := c.DjinnMode(); mode {
	case DjinnAuto, DjinnOn, DjinnOff:
	default:
		return fmt.Errorf("config %s: integrations.djinn.enabled must be %q, %q or %q, got %q",
			label, DjinnAuto, DjinnOn, DjinnOff, mode)
	}

	if c.Discovery != nil && c.Discovery.Enabled {
		if len(c.Discovery.Roots) == 0 {
			return fmt.Errorf("config %s: discovery: at least one root is required", label)
		}
		if c.Discovery.Depth < 1 || c.Discovery.Depth > 10 {
			return fmt.Errorf("config %s: discovery: depth must be between 1 and 10", label)
		}
		interval, err := time.ParseDuration(c.Discovery.Interval)
		if err != nil {
			return fmt.Errorf("config %s: discovery: interval: %w", label, err)
		}
		if interval < 30*time.Second {
			return fmt.Errorf("config %s: discovery: interval must be at least 30s", label)
		}
	}

	seenServer := map[string]bool{}
	for _, s := range c.Servers {
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("config %s: server with empty name", label)
		}
		if seenServer[s.Name] {
			return fmt.Errorf("config %s: duplicate server %q", label, s.Name)
		}
		seenServer[s.Name] = true
		if s.Kind != KindLocal && s.Kind != KindRemote {
			return fmt.Errorf("config %s: server %q: kind must be %q or %q, got %q",
				label, s.Name, KindLocal, KindRemote, s.Kind)
		}
		if s.Kind == KindRemote && (s.Port <= 0 || s.Port > 65535) {
			return fmt.Errorf("config %s: server %q: port %d out of range", label, s.Name, s.Port)
		}
	}

	if len(c.Projects) == 0 && (c.Discovery == nil || !c.Discovery.Enabled) {
		return fmt.Errorf("config %s: no projects configured", label)
	}
	seenProject := map[string]bool{}
	for _, p := range c.Projects {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("config %s: project with empty name", label)
		}
		if seenProject[p.Name] {
			return fmt.Errorf("config %s: duplicate project %q", label, p.Name)
		}
		seenProject[p.Name] = true
		if strings.TrimSpace(p.Workdir) == "" {
			return fmt.Errorf("config %s: project %q: workdir is required", label, p.Name)
		}
		if p.Server != "" && !seenServer[p.Server] {
			return fmt.Errorf("config %s: project %q: unknown server %q", label, p.Name, p.Server)
		}
		if err := p.Summoner.validate("summoner"); err != nil {
			return fmt.Errorf("config %s: project %q: %w", label, p.Name, err)
		}
		if p.Integrations != nil {
			if err := p.Integrations.Djinn.validate("integrations.djinn"); err != nil {
				return fmt.Errorf("config %s: project %q: %w", label, p.Name, err)
			}
		}
	}
	return nil
}

func (s *Summoner) validate(field string) error {
	if s == nil {
		return nil
	}
	if s.MaxWorkers < 0 {
		return fmt.Errorf("%s.max_workers must be >= 0", field)
	}
	if s.MaxBeads < 0 {
		return fmt.Errorf("%s.max_beads must be >= 0", field)
	}
	return nil
}

// ServerByName returns the configured server with the given name.
func (c *Config) ServerByName(name string) (Server, bool) {
	for _, s := range c.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// ExpandPath expands a leading ~ or ~/ to the user's home directory, resolves
// a relative path against the process working directory, and cleans the
// result. An empty path stays empty.
func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand %q: %w", p, err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	if strings.HasPrefix(p, "~") {
		// ~otheruser is not supported; expanding it wrong is worse than failing.
		return "", fmt.Errorf("cannot expand %q: ~user paths are not supported", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %q: %w", p, err)
	}
	return abs, nil
}
