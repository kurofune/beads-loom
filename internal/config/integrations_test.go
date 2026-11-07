package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadYAML(t *testing.T, body string) (*Config, string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	return cfg, path, err
}

func TestProjectIntegrationsDjinnBlockDecodes(t *testing.T) {
	cfg, _, err := loadYAML(t, `projects:
  - name: a
    workdir: /tmp/a
    integrations:
      djinn:
        max_workers: 4
        max_beads: 9
        no_push: true
`)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Projects[0].DjinnSettings()
	if got == nil || *got != (Summoner{MaxWorkers: 4, MaxBeads: 9, NoPush: true}) {
		t.Fatalf("DjinnSettings() = %+v, want {4 9 true}", got)
	}
}

func TestLegacySummonerBlockDecodesIntoTheSameSettings(t *testing.T) {
	cfg, _, err := loadYAML(t, `projects:
  - name: a
    workdir: /tmp/a
    summoner:
      max_workers: 3
      no_push: true
`)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Projects[0].DjinnSettings()
	if got == nil || *got != (Summoner{MaxWorkers: 3, NoPush: true}) {
		t.Fatalf("DjinnSettings() = %+v, want the legacy block {3 0 true}", got)
	}
}

func TestIntegrationsDjinnWinsOverLegacySummoner(t *testing.T) {
	cfg, _, err := loadYAML(t, `projects:
  - name: a
    workdir: /tmp/a
    summoner:
      max_workers: 3
      no_push: true
    integrations:
      djinn:
        max_workers: 7
`)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Projects[0].DjinnSettings()
	if got == nil || *got != (Summoner{MaxWorkers: 7}) {
		t.Fatalf("DjinnSettings() = %+v, want integrations.djinn {7 0 false} to win whole", got)
	}
}

func TestNoDjinnSettingsIsNil(t *testing.T) {
	cfg, _, err := loadYAML(t, "projects:\n  - name: a\n    workdir: /tmp/a\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Projects[0].DjinnSettings(); got != nil {
		t.Fatalf("DjinnSettings() = %+v, want nil with neither block", got)
	}
}

func TestDjinnEnabledAcceptsAutoTrueFalse(t *testing.T) {
	cases := []struct {
		yaml        string
		wantMode    string
		wantEnabled bool
	}{
		{"", DjinnAuto, true},
		{"integrations:\n  djinn:\n    enabled: auto\n", DjinnAuto, true},
		{"integrations:\n  djinn:\n    enabled: true\n", DjinnOn, true},
		{"integrations:\n  djinn:\n    enabled: false\n", DjinnOff, false},
		{"integrations:\n  djinn: {}\n", DjinnAuto, true},
	}
	for _, tc := range cases {
		t.Run(tc.wantMode+"/"+tc.yaml, func(t *testing.T) {
			cfg, _, err := loadYAML(t, tc.yaml+"projects:\n  - name: a\n    workdir: /tmp/a\n")
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.DjinnMode(); got != tc.wantMode {
				t.Errorf("DjinnMode() = %q, want %q", got, tc.wantMode)
			}
			if got := cfg.DjinnEnabled(); got != tc.wantEnabled {
				t.Errorf("DjinnEnabled() = %v, want %v", got, tc.wantEnabled)
			}
		})
	}
}

func TestDjinnEnabledRejectsAnyOtherValueNamingTheField(t *testing.T) {
	for _, bad := range []string{"yes", "on", "maybe", "1"} {
		t.Run(bad, func(t *testing.T) {
			_, path, err := loadYAML(t, "integrations:\n  djinn:\n    enabled: "+bad+
				"\nprojects:\n  - name: a\n    workdir: /tmp/a\n")
			if err == nil {
				t.Fatalf("Load() accepted enabled: %s", bad)
			}
			if !strings.Contains(err.Error(), "integrations.djinn.enabled") {
				t.Errorf("error = %q, want it to name integrations.djinn.enabled", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to carry the config path", err)
			}
		})
	}
}

func TestNegativeDjinnKnobsFailLoad(t *testing.T) {
	for _, block := range []string{
		"    summoner:\n      max_workers: -1\n",
		"    integrations:\n      djinn:\n        max_workers: -1\n",
		"    integrations:\n      djinn:\n        max_beads: -2\n",
	} {
		_, _, err := loadYAML(t, "projects:\n  - name: a\n    workdir: /tmp/a\n"+block)
		if err == nil {
			t.Errorf("Load() accepted a negative knob:\n%s", block)
		}
	}
}

func TestDjinnEnabledRejectsAnExplicitEmptyValue(t *testing.T) {
	for _, blank := range []string{`""`, `''`, ``, `~`, `null`} {
		t.Run(blank, func(t *testing.T) {
			_, path, err := loadYAML(t, "integrations:\n  djinn:\n    enabled: "+blank+
				"\nprojects:\n  - name: a\n    workdir: /tmp/a\n")
			if err == nil {
				t.Fatalf("Load() accepted enabled: %s; an explicit empty value must not read as auto", blank)
			}
			if !strings.Contains(err.Error(), "integrations.djinn.enabled") {
				t.Errorf("error = %q, want it to name integrations.djinn.enabled", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to carry the config path", err)
			}
		})
	}
}

func TestDjinnEnabledOmittedKeyStaysAuto(t *testing.T) {
	for _, head := range []string{"", "integrations:\n  djinn: {}\n", "integrations: {}\n"} {
		t.Run(head, func(t *testing.T) {
			cfg, _, err := loadYAML(t, head+"projects:\n  - name: a\n    workdir: /tmp/a\n")
			if err != nil {
				t.Fatalf("Load() with no integrations.djinn.enabled key: %v", err)
			}
			if got := cfg.DjinnMode(); got != DjinnAuto {
				t.Errorf("DjinnMode() = %q, want %q", got, DjinnAuto)
			}
		})
	}
}

func TestDjinnEnabledRejectsAnEmptyValueSuppliedByAMerge(t *testing.T) {
	for name, djinn := range map[string]string{
		"inline":   "  djinn: {<<: {enabled: \"\"}}\n",
		"null":     "  djinn: {<<: {enabled: ~}}\n",
		"sequence": "  djinn: {<<: [{}, {enabled: \"\"}]}\n",
		"anchor":   "  base: &b {enabled: \"\"}\n  djinn:\n    <<: *b\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := loadYAML(t, "integrations:\n"+djinn+"projects:\n  - name: a\n    workdir: /tmp/a\n")
			if err == nil {
				t.Fatalf("Load() accepted a merged empty enabled; it must not read as auto")
			}
			if !strings.Contains(err.Error(), "integrations.djinn.enabled") {
				t.Errorf("error = %q, want it to name integrations.djinn.enabled", err)
			}
		})
	}
}

func TestDjinnEnabledExplicitKeyOverridesAMergedValue(t *testing.T) {
	for name, tc := range map[string]struct{ djinn, want string }{
		"explicit over merged empty": {"  djinn: {<<: {enabled: \"\"}, enabled: \"false\"}\n", DjinnOff},
		"explicit after merge":       {"  djinn: {enabled: \"false\", <<: {enabled: \"\"}}\n", DjinnOff},
		"merged valid value":         {"  djinn: {<<: {enabled: \"false\"}}\n", DjinnOff},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, _, err := loadYAML(t, "integrations:\n"+tc.djinn+"projects:\n  - name: a\n    workdir: /tmp/a\n")
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			if got := cfg.DjinnMode(); got != tc.want {
				t.Errorf("DjinnMode() = %q, want %q", got, tc.want)
			}
		})
	}
}
