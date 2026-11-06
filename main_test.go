package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/sse"
)

func buildFor(t *testing.T, mode string) ([]string, []string) {
	t.Helper()
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".djinn"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_STATE_DIR", t.TempDir())
	cfg := &config.Config{
		Integrations: &config.Integrations{Djinn: &config.DjinnIntegration{Enabled: mode}},
		Projects:     []config.Project{{Name: "a", Workdir: workdir}},
	}
	hub := sse.NewHub()
	defer hub.Close()

	set := buildIntegrations(cfg, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	set.ProjectAdded(registry.Project{Name: "a", Workdir: workdir})

	var names, watching []string
	for _, p := range set {
		names = append(names, p.Name())
		if w, ok := p.(interface{ Watched() []string }); ok {
			watching = append(watching, w.Watched()...)
		}
	}
	return names, watching
}

func TestDisabledDjinnBuildsNoProviderAndNoWatcher(t *testing.T) {
	names, watching := buildFor(t, config.DjinnOff)
	if len(names) != 0 {
		t.Errorf("providers = %v, want none with integrations.djinn.enabled: false", names)
	}
	if len(watching) != 0 {
		t.Errorf("watching = %v, want no watcher for a .djinn project with djinn disabled", watching)
	}
}

func TestEnabledDjinnWatchesADjinnProject(t *testing.T) {
	for _, mode := range []string{"", config.DjinnAuto, config.DjinnOn} {
		names, watching := buildFor(t, mode)
		if !reflect.DeepEqual(names, []string{"djinn"}) {
			t.Errorf("mode %q: providers = %v, want [djinn]", mode, names)
		}
		if !reflect.DeepEqual(watching, []string{"a"}) {
			t.Errorf("mode %q: watching = %v, want [a]", mode, watching)
		}
	}
}
