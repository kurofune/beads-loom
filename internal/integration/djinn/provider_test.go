package djinn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/runs"
)

func djinnWorkdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".djinn"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeResolver stands in for exec.LookPath and counts its calls.
type fakeResolver struct {
	found bool
	calls atomic.Int64
}

func (f *fakeResolver) lookPath(name string) (string, error) {
	f.calls.Add(1)
	if f.found {
		return "/opt/bin/" + name, nil
	}
	return "", errors.New("executable file not found in $PATH")
}

func TestCapabilitiesWithoutSummonerAreRunsAndEvidenceWithAReason(t *testing.T) {
	res := &fakeResolver{found: false}
	p := New(Options{StateDir: t.TempDir(), LookPath: res.lookPath})

	caps := p.Capabilities(djinnWorkdir(t))
	if !caps.Present {
		t.Fatal("a workdir with .djinn must report the djinn integration present")
	}
	if !reflect.DeepEqual(caps.Capabilities, []string{CapRuns, CapEvidence}) {
		t.Errorf("capabilities = %v, want exactly [runs evidence]", caps.Capabilities)
	}
	if reason := caps.Reasons[CapDispatch]; !strings.Contains(reason, runs.SummonerBinary) {
		t.Errorf("reasons.dispatch = %q, want it to name the missing %s binary", reason, runs.SummonerBinary)
	}
}

func TestCapabilitiesWithSummonerIncludeDispatch(t *testing.T) {
	res := &fakeResolver{found: true}
	p := New(Options{StateDir: t.TempDir(), LookPath: res.lookPath})

	caps := p.Capabilities(djinnWorkdir(t))
	if !slices.Contains(caps.Capabilities, CapDispatch) {
		t.Errorf("capabilities = %v, want dispatch with summoner resolvable", caps.Capabilities)
	}
	if len(caps.Reasons) != 0 {
		t.Errorf("reasons = %v, want none with every capability working", caps.Reasons)
	}
}

func TestCapabilitiesWithoutDotDjinnAreAbsent(t *testing.T) {
	res := &fakeResolver{found: true}
	p := New(Options{StateDir: t.TempDir(), LookPath: res.lookPath})

	if caps := p.Capabilities(t.TempDir()); caps.Present || len(caps.Capabilities) != 0 {
		t.Errorf("capabilities = %+v, want absent without .djinn", caps)
	}
	if res.calls.Load() != 0 {
		t.Errorf("PATH looked up %d times for a workdir without .djinn, want 0", res.calls.Load())
	}
}

// TestCapabilitiesCacheThePathLookup: detection runs on every refresh tick for
// every project, so the PATH search is cached rather than repeated.
func TestCapabilitiesCacheThePathLookup(t *testing.T) {
	res := &fakeResolver{found: false}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := New(Options{StateDir: t.TempDir(), LookPath: res.lookPath, Now: func() time.Time { return now }})

	dir := djinnWorkdir(t)
	for range 50 {
		p.Capabilities(dir)
	}
	if res.calls.Load() != 1 {
		t.Fatalf("PATH looked up %d times in 50 detections, want 1", res.calls.Load())
	}

	// Installing summoner shows up once the cache expires.
	res.found = true
	now = now.Add(binaryCacheTTL)
	if caps := p.Capabilities(dir); !slices.Contains(caps.Capabilities, CapDispatch) {
		t.Errorf("capabilities = %v after the cache expired, want dispatch", caps.Capabilities)
	}
	if res.calls.Load() != 2 {
		t.Errorf("PATH looked up %d times, want 2 after expiry", res.calls.Load())
	}
}

// TestRegistryRefreshStoresDjinnCapabilities runs the real provider inside a
// real registry refresh.
func TestRegistryRefreshStoresDjinnCapabilities(t *testing.T) {
	res := &fakeResolver{found: true}
	p := New(Options{StateDir: t.TempDir(), LookPath: res.lookPath})
	cfg := &config.Config{Projects: []config.Project{
		{Name: "with", Workdir: djinnWorkdir(t)},
		{Name: "without", Workdir: t.TempDir()},
	}}
	reg := registry.New(cfg, p)
	defer reg.Close()
	reg.Refresh(context.Background())

	with, err := reg.Get("with")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := with.Integrations[Name]
	if !ok || !got.Has(CapRuns) || !got.Has(CapEvidence) || !got.Has(CapDispatch) {
		t.Errorf("with.Integrations = %+v, want djinn with runs, evidence and dispatch", with.Integrations)
	}
	without, err := reg.Get("without")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := without.Integrations[Name]; ok {
		t.Errorf("without.Integrations = %+v, want no djinn key", without.Integrations)
	}
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// TestLegacySummonerBlockReachesTheLaunchArgv: a config still written with the
// pre-seam summoner: block launches with its knobs.
func TestLegacySummonerBlockReachesTheLaunchArgv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "projects:\n  - name: a\n    workdir: /tmp/a\n    summoner:\n      max_workers: 3\n      no_push: true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	args := summonRequest(summonBody{}, cfg.Projects[0].DjinnSettings()).Args()
	if !hasPair(args, "-max-workers", "3") {
		t.Errorf("argv = %v, want -max-workers 3 from the legacy block", args)
	}
	if !slices.Contains(args, "-no-push") {
		t.Errorf("argv = %v, want -no-push from the legacy block", args)
	}
}

func TestSummonRequestFallsBackOnlyForUnsetKnobs(t *testing.T) {
	defaults := &config.Summoner{MaxWorkers: 3, MaxBeads: 5, NoPush: true}
	no := false
	req := summonRequest(summonBody{MaxWorkers: 2, NoPush: &no}, defaults)
	if req.MaxWorkers != 2 || req.MaxBeads != 5 || req.NoPush {
		t.Errorf("request = %+v, want the body's 2 and false, the default's 5", req)
	}
	if req := summonRequest(summonBody{}, nil); req.MaxWorkers != 0 || req.MaxBeads != 0 || req.NoPush {
		t.Errorf("request = %+v, want zero knobs with no defaults", req)
	}
}

// TestCloseStopsLiveRuns: shutdown of managed summoner runs belongs to the
// adapter.
func TestCloseStopsLiveRuns(t *testing.T) {
	fake, err := filepath.Abs(filepath.Join("..", "..", "runs", "testdata", "bin", "fake-summoner"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SUMMONER_MODE", "long")
	p := New(Options{StateDir: t.TempDir(), Binary: fake, StopGrace: 500 * time.Millisecond})

	run, err := p.Manager().Summon(context.Background(), "a", djinnWorkdir(t), runs.SummonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Manager().Active("a"); !ok {
		t.Fatal("run is not active after summon")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Close(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := p.Manager().Active("a"); !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s still active after Close", run.ID)
}

func TestProjectAddedWatchesOnlyDjinnProjects(t *testing.T) {
	p := New(Options{StateDir: t.TempDir()})
	p.ProjectAdded(registry.Project{Name: "with", Workdir: djinnWorkdir(t)})
	p.ProjectAdded(registry.Project{Name: "without", Workdir: t.TempDir()})
	if got := p.Watched(); !reflect.DeepEqual(got, []string{"with"}) {
		t.Errorf("watched = %v, want [with]", got)
	}
	p.ProjectRemoved("with")
	if got := p.Watched(); len(got) != 0 {
		t.Errorf("watched = %v after removal, want none", got)
	}
}

// TestSpendCountsRunsThatEndedInTheWindow covers review finding N7: bounding
// on start time alone dropped a long drain that began before the window and
// finished inside it.
func TestSpendCountsRunsThatEndedInTheWindow(t *testing.T) {
	p := New(Options{StateDir: t.TempDir()})
	proj := runs.Project{Name: "djinn", Workdir: djinnWorkdir(t)}
	now := time.Now().UTC()

	mk := func(id string, startedDaysAgo, endedDaysAgo float64, cost float64) runs.Run {
		start := now.Add(-time.Duration(startedDaysAgo * 24 * float64(time.Hour)))
		end := now.Add(-time.Duration(endedDaysAgo * 24 * float64(time.Hour)))
		return runs.Run{
			ID: id, Project: "djinn", Source: runs.SourceLoom,
			StartedAt: start, EndedAt: &end, State: runs.StateFinished,
			CostSoFar: cost,
		}
	}
	for _, r := range []runs.Run{
		mk("run-long", 8, 1, 5.00),   // started 8 days ago, finished yesterday
		mk("run-recent", 2, 2, 3.00), // inside the window
		mk("run-old", 30, 29, 99.00), // outside it
	} {
		if err := p.Store().Save(r); err != nil {
			t.Fatal(err)
		}
	}

	got := p.spendRecent(proj, now)
	if got == nil {
		t.Fatal("spendRecent = nil, want a total")
	}
	if *got < 7.99 || *got > 8.01 {
		t.Errorf("spendRecent = %v, want 8.00 (the long run plus the recent one)", *got)
	}
}

func TestEventNamesAreTheRunEvents(t *testing.T) {
	got := New(Options{StateDir: t.TempDir()}).EventNames()
	want := []string{runs.EventRunStarted, runs.EventRunUpdated, runs.EventRunFinished, runs.EventRunLog, runs.EventBeadState}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EventNames() = %v, want %v", got, want)
	}
}
