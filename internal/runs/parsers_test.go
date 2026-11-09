package runs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// --- lifecycle -------------------------------------------------------------

func TestParseLifecycleLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want *LifecycleEvent
	}{
		{
			name: "spawn",
			line: "2026-06-16T00:06:28-07:00\tdjinn-hmmp\tspawn",
			want: &LifecycleEvent{Bead: "djinn-hmmp", Kind: KindSpawn},
		},
		{
			name: "restart",
			line: "2026-06-16T00:17:06-07:00\tdjinn-zvjs\trestart",
			want: &LifecycleEvent{Bead: "djinn-zvjs", Kind: KindRestart},
		},
		{
			name: "exit with four fields",
			line: "2026-06-08T01:30:56-07:00\tdjinn-78l6\texit\tCOMPLETE",
			want: &LifecycleEvent{Bead: "djinn-78l6", Kind: KindExit, Verdict: VerdictComplete},
		},
		{
			name: "exit with detail",
			line: "2026-06-16T00:16:41-07:00\tdjinn-mxbm\texit\tCOMPLETE\tclosed",
			want: &LifecycleEvent{Bead: "djinn-mxbm", Kind: KindExit, Verdict: VerdictComplete, Detail: "closed"},
		},
		{
			name: "crashed with exit status",
			line: "2026-06-11T21:42:23-07:00\tdjinn-0e5h.1\texit\tCRASHED\texit status 3",
			want: &LifecycleEvent{Bead: "djinn-0e5h.1", Kind: KindExit, Verdict: VerdictCrashed, Detail: "exit status 3"},
		},
		{
			name: "crashed by signal",
			line: "2026-06-21T13:15:52-07:00\tdjinn-kt63.1\texit\tCRASHED\tsignal: killed",
			want: &LifecycleEvent{Bead: "djinn-kt63.1", Kind: KindExit, Verdict: VerdictCrashed, Detail: "signal: killed"},
		},
		{
			name: "failed with halt reason",
			line: "2026-06-16T08:28:35-07:00\tdjinn-onp7\texit\tFAILED\toperator-escalation halt (loop_exhausted)",
			want: &LifecycleEvent{Bead: "djinn-onp7", Kind: KindExit, Verdict: VerdictFailed,
				Detail: "operator-escalation halt (loop_exhausted)"},
		},
		{name: "blank line", line: "", want: nil},
		{name: "too few fields", line: "2026-06-16T00:06:28-07:00\tdjinn-hmmp", want: nil},
		{name: "bad timestamp", line: "not-a-time\tdjinn-hmmp\tspawn", want: nil},
		{name: "torn final line", line: "2026-06-16T00:06:2", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLifecycle(strings.NewReader(tc.line))
			if err != nil {
				t.Fatalf("ParseLifecycle: %v", err)
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("got %d events, want the line skipped: %+v", len(got), got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d events, want 1", len(got))
			}
			e := got[0]
			if e.Bead != tc.want.Bead || e.Kind != tc.want.Kind ||
				e.Verdict != tc.want.Verdict || e.Detail != tc.want.Detail {
				t.Errorf("event = %+v, want %+v", e, *tc.want)
			}
			if e.At.IsZero() {
				t.Error("timestamp not parsed")
			}
			if e.At.Location() != time.UTC {
				t.Errorf("timestamp not normalized to UTC: %v", e.At.Location())
			}
		})
	}
}

// TestParseLifecycleRealFixture runs against a slice copied verbatim from
// ~/Projects/djinn/.djinn/summoner-logs.
func TestParseLifecycleRealFixture(t *testing.T) {
	events, err := ParseLifecycle(strings.NewReader(string(mustReadFixture(t, "lifecycle-mixed.log"))))
	if err != nil {
		t.Fatalf("ParseLifecycle: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events parsed from the real fixture")
	}

	kinds := map[string]int{}
	verdicts := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
		if e.Verdict != "" {
			verdicts[e.Verdict]++
		}
		if e.Bead == "" {
			t.Errorf("event with empty bead: %+v", e)
		}
	}

	// The fixture was chosen to cover every shape real logs contain.
	for _, k := range []string{KindSpawn, KindExit, KindRestart} {
		if kinds[k] == 0 {
			t.Errorf("fixture produced no %s events; kinds=%v", k, kinds)
		}
	}
	for _, v := range []string{VerdictComplete, VerdictFailed, VerdictCrashed} {
		if verdicts[v] == 0 {
			t.Errorf("fixture produced no %s verdicts; verdicts=%v", v, verdicts)
		}
	}
}

func TestIsLifecycleLog(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"2026-06-16.log", true},
		{"2026-06-24.log", true},
		{"/abs/path/2026-06-16.log", true},
		{"console-2026-06-13.log", false},
		{"rerun-uy0d-mslc-1781850236.out", false},
		{"notes.log", false},
		{"2026-06-16.txt", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLifecycleLog(tc.name); got != tc.want {
				t.Errorf("IsLifecycleLog(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v.UTC()
}

func TestGroupRuns(t *testing.T) {
	mk := func(ts, bead, kind string) LifecycleEvent {
		return LifecycleEvent{At: at(t, ts), Bead: bead, Kind: kind}
	}

	tests := []struct {
		name   string
		events []LifecycleEvent
		want   int
	}{
		{name: "empty", events: nil, want: 0},
		{
			name:   "single event",
			events: []LifecycleEvent{mk("2026-06-16T00:00:00Z", "a", KindSpawn)},
			want:   1,
		},
		{
			name: "contiguous activity is one run",
			events: []LifecycleEvent{
				mk("2026-06-16T00:00:00Z", "a", KindSpawn),
				mk("2026-06-16T00:05:00Z", "a", KindExit),
				mk("2026-06-16T00:09:00Z", "b", KindSpawn),
			},
			want: 1,
		},
		{
			// The first bead exited, so nothing is in flight across the gap.
			name: "a gap over the threshold splits when no worker is in flight",
			events: []LifecycleEvent{
				mk("2026-06-16T00:00:00Z", "a", KindSpawn),
				mk("2026-06-16T00:01:00Z", "a", KindExit),
				mk("2026-06-16T00:20:00Z", "b", KindSpawn),
			},
			want: 2,
		},
		{
			// Real behaviour: a worker can run silently for longer than the
			// gap, and its own exit must stay in the same run as its spawn.
			name: "a long-running worker holds the run open across the gap",
			events: []LifecycleEvent{
				mk("2026-06-16T00:00:00Z", "a", KindSpawn),
				mk("2026-06-16T00:30:00Z", "a", KindExit),
			},
			want: 1,
		},
		{
			name: "an unexited worker keeps later activity in the same run",
			events: []LifecycleEvent{
				mk("2026-06-16T00:00:00Z", "a", KindSpawn),
				mk("2026-06-16T01:00:00Z", "b", KindSpawn),
			},
			want: 1,
		},
		{
			name: "exactly the threshold does not split",
			events: []LifecycleEvent{
				mk("2026-06-16T00:00:00Z", "a", KindSpawn),
				mk("2026-06-16T00:10:00Z", "b", KindSpawn),
			},
			want: 1,
		},
		{
			name: "out-of-order rows are sorted, not split",
			events: []LifecycleEvent{
				mk("2026-06-16T00:05:00Z", "b", KindSpawn),
				mk("2026-06-16T00:00:00Z", "a", KindSpawn),
			},
			want: 1,
		},
		{
			name: "three clusters, each fully drained",
			events: []LifecycleEvent{
				mk("2026-06-16T00:00:00Z", "a", KindSpawn),
				mk("2026-06-16T00:01:00Z", "a", KindExit),
				mk("2026-06-16T01:00:00Z", "b", KindSpawn),
				mk("2026-06-16T01:01:00Z", "b", KindExit),
				mk("2026-06-16T02:00:00Z", "c", KindSpawn),
				mk("2026-06-16T02:01:00Z", "c", KindExit),
			},
			want: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GroupRuns(tc.events, ExternalRunGap)
			if len(got) != tc.want {
				t.Fatalf("got %d groups, want %d", len(got), tc.want)
			}
			total := 0
			for _, g := range got {
				total += len(g)
			}
			if total != len(tc.events) {
				t.Errorf("grouping lost events: %d in, %d out", len(tc.events), total)
			}
		})
	}
}

// TestGroupRunsRealFixture: the 2026-06-16 log has a genuine multi-hour gap
// (00:43 → 08:28), so it must split.
func TestGroupRunsRealFixture(t *testing.T) {
	events, err := ParseLifecycle(strings.NewReader(string(mustReadFixture(t, "lifecycle-gaps.log"))))
	if err != nil {
		t.Fatal(err)
	}
	groups := GroupRuns(events, ExternalRunGap)
	if len(groups) < 2 {
		t.Fatalf("got %d groups from a log with a multi-hour gap, want at least 2", len(groups))
	}
	for i, g := range groups {
		if len(g) == 0 {
			t.Errorf("group %d is empty", i)
		}
	}
}

func TestBuildRun(t *testing.T) {
	mk := func(ts, bead, kind, verdict, detail string) LifecycleEvent {
		return LifecycleEvent{At: at(t, ts), Bead: bead, Kind: kind, Verdict: verdict, Detail: detail}
	}

	t.Run("completed run", func(t *testing.T) {
		run := BuildRun("r1", "djinn", SourceExternal, []LifecycleEvent{
			mk("2026-06-24T00:42:33Z", "djinn-gggc", KindSpawn, "", ""),
			mk("2026-06-24T00:55:50Z", "djinn-gggc", KindExit, VerdictComplete, "closed"),
		})
		if run.State != StateFinished {
			t.Errorf("state = %q, want finished", run.State)
		}
		if run.EndedAt == nil {
			t.Fatal("EndedAt should be set for a finished run")
		}
		if len(run.Beads) != 1 {
			t.Fatalf("got %d beads, want 1", len(run.Beads))
		}
		b := run.Beads[0]
		if b.Verdict != VerdictComplete || b.FinalStatus != "closed" {
			t.Errorf("bead = %+v", b)
		}
		if b.Active() {
			t.Error("exited bead should not be active")
		}
	})

	t.Run("run still going", func(t *testing.T) {
		run := BuildRun("r2", "djinn", SourceExternal, []LifecycleEvent{
			mk("2026-06-24T00:42:33Z", "djinn-a", KindSpawn, "", ""),
			mk("2026-06-24T00:43:00Z", "djinn-b", KindSpawn, "", ""),
			mk("2026-06-24T00:55:50Z", "djinn-a", KindExit, VerdictComplete, "closed"),
		})
		if run.State != StateRunning {
			t.Errorf("state = %q, want running while a bead is unexited", run.State)
		}
		if run.EndedAt != nil {
			t.Error("EndedAt must be nil while the run is going")
		}
	})

	t.Run("restart counts as a retry", func(t *testing.T) {
		run := BuildRun("r3", "djinn", SourceExternal, []LifecycleEvent{
			mk("2026-06-16T00:06:28Z", "djinn-zvjs", KindSpawn, "", ""),
			mk("2026-06-16T00:17:06Z", "djinn-zvjs", KindRestart, "", ""),
			mk("2026-06-16T00:30:44Z", "djinn-zvjs", KindRestart, "", ""),
			mk("2026-06-16T00:43:57Z", "djinn-zvjs", KindExit, VerdictComplete, "closed"),
		})
		if len(run.Beads) != 1 {
			t.Fatalf("got %d beads, want 1", len(run.Beads))
		}
		if run.Beads[0].Retries != 2 {
			t.Errorf("retries = %d, want 2", run.Beads[0].Retries)
		}
		if run.Beads[0].Verdict != VerdictComplete {
			t.Errorf("verdict = %q; the final exit must win over the restarts", run.Beads[0].Verdict)
		}
	})

	t.Run("re-dispatch after exit reopens the bead", func(t *testing.T) {
		run := BuildRun("r4", "djinn", SourceExternal, []LifecycleEvent{
			mk("2026-06-16T00:00:00Z", "djinn-a", KindSpawn, "", ""),
			mk("2026-06-16T00:01:00Z", "djinn-a", KindExit, VerdictCrashed, "exit status 3"),
			mk("2026-06-16T00:02:00Z", "djinn-a", KindSpawn, "", ""),
		})
		b := run.Beads[0]
		if !b.Active() {
			t.Error("bead re-spawned after a crash should be active again")
		}
		if b.Retries != 1 {
			t.Errorf("retries = %d, want 1", b.Retries)
		}
		if run.State != StateRunning {
			t.Errorf("state = %q, want running", run.State)
		}
	})

	t.Run("empty events", func(t *testing.T) {
		run := BuildRun("r5", "djinn", SourceExternal, nil)
		if run.Beads == nil {
			t.Error("Beads must serialize as [] not null")
		}
	})
}

func TestExternalRunIDIsStable(t *testing.T) {
	ts := at(t, "2026-06-16T08:28:02Z")
	a := ExternalRunID("djinn", ts)
	b := ExternalRunID("djinn", ts)
	if a != b {
		t.Errorf("ids differ across calls: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "ext-20260616T082802Z-") || !ValidID(a) {
		t.Errorf("id = %q, want a valid ext-<timestamp>-<project> id", a)
	}
	if c := ExternalRunID("djinn", ts.Add(time.Second)); c == a {
		t.Error("different start times must yield different ids")
	}
	if c := ExternalRunID("loom", ts); c == a {
		t.Error("different projects must yield different ids for the same start")
	}
	if c := ExternalRunID("acme/web app", ts); !ValidID(c) {
		t.Errorf("id %q for a name with a slash and a space is not a valid id", c)
	}
}

// --- console ---------------------------------------------------------------

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"colour and reset", "\x1b[32m\x1b[1mDone\x1b[0m", "Done"},
		{"dim value", "  closed:         \x1b[2m0\x1b[0m", "  closed:         0"},
		{"red halt", "  halt:  \x1b[31miter 1: loop exhausted\x1b[0m", "  halt:  iter 1: loop exhausted"},
		{"no escapes short-circuits", "plain text", "plain text"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripANSI(tc.in); got != tc.want {
				t.Errorf("StripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseStepLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantOK   bool
		wantStep string
		wantIter int
		wantMax  int
		wantCost float64
		wantCtx  int
		wantDur  string
		wantSecs float64
	}{
		{
			name:     "with iteration",
			line:     "\x1b[32m\x1b[1m✅ Done [verify-impl iter 2/3] (cost: $0.8135, ctx: 6%, dur=1m26.3s)\x1b[0m",
			wantOK:   true,
			wantStep: "verify-impl", wantIter: 2, wantMax: 3,
			wantCost: 0.8135, wantCtx: 6, wantDur: "1m26.3s", wantSecs: 86.3,
		},
		{
			// Real logs contain steps with no iter part; the work package's
			// pattern alone would drop these.
			name:     "without iteration",
			line:     "✅ Done [implement] (cost: $1.9275, ctx: 8%, dur=4m25s)",
			wantOK:   true,
			wantStep: "implement", wantIter: 0, wantMax: 0,
			wantCost: 1.9275, wantCtx: 8, wantDur: "4m25s", wantSecs: 265,
		},
		{
			name:     "sub-minute duration",
			line:     "✅ Done [ac-quality-review iter 1/2] (cost: $0.5775, ctx: 4%, dur=21.5s)",
			wantOK:   true,
			wantStep: "ac-quality-review", wantIter: 1, wantMax: 2,
			wantCost: 0.5775, wantCtx: 4, wantDur: "21.5s", wantSecs: 21.5,
		},
		{
			name:     "hyphenated fix step",
			line:     "✅ Done [fix-verify-impl iter 3/3] (cost: $0.3193, ctx: 4%, dur=18.2s)",
			wantOK:   true,
			wantStep: "fix-verify-impl", wantIter: 3, wantMax: 3,
			wantCost: 0.3193, wantCtx: 4, wantDur: "18.2s", wantSecs: 18.2,
		},
		{name: "unrelated line", line: "summoner: polling bd ready every 30s", wantOK: false},
		{name: "structured log line", line: `time=2026-06-13T00:46:09.350-07:00 level=INFO msg="formula loaded"`, wantOK: false},
		{name: "empty", line: "", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseStepLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got.Step != tc.wantStep {
				t.Errorf("step = %q, want %q", got.Step, tc.wantStep)
			}
			if got.Iter != tc.wantIter || got.MaxIter != tc.wantMax {
				t.Errorf("iter = %d/%d, want %d/%d", got.Iter, got.MaxIter, tc.wantIter, tc.wantMax)
			}
			if got.CostUSD != tc.wantCost {
				t.Errorf("cost = %v, want %v", got.CostUSD, tc.wantCost)
			}
			if got.ContextPct != tc.wantCtx {
				t.Errorf("ctx = %d, want %d", got.ContextPct, tc.wantCtx)
			}
			if got.Duration != tc.wantDur {
				t.Errorf("duration = %q, want %q", got.Duration, tc.wantDur)
			}
			if got.DurationSeconds != tc.wantSecs {
				t.Errorf("durationSeconds = %v, want %v", got.DurationSeconds, tc.wantSecs)
			}
		})
	}
}

// TestParseConsoleRealFixture parses a slice copied from a real console log,
// ANSI and all.
func TestParseConsoleRealFixture(t *testing.T) {
	got, err := ParseConsole(strings.NewReader(string(mustReadFixture(t, "console-real.log"))))
	if err != nil {
		t.Fatalf("ParseConsole: %v", err)
	}

	if len(got.Steps) == 0 {
		t.Fatal("no step lines parsed from the real console fixture")
	}
	var sum float64
	for _, s := range got.Steps {
		if s.Step == "" {
			t.Errorf("step with empty name: %+v", s)
		}
		if s.CostUSD <= 0 {
			t.Errorf("step %s has no cost", s.Step)
		}
		sum += s.CostUSD
	}
	if got.TotalCost != sum {
		t.Errorf("TotalCost = %v, want the sum of steps %v", got.TotalCost, sum)
	}

	if got.Summary == nil {
		t.Fatal("Session Summary block was not parsed")
	}
	s := got.Summary
	// Values verified by eye against the real block.
	if s.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", s.Iterations)
	}
	if s.Closed != 0 {
		t.Errorf("closed = %d, want 0", s.Closed)
	}
	if s.Failures != 0 {
		t.Errorf("failures = %d, want 0", s.Failures)
	}
	if s.TotalCostUSD != 5.74 {
		t.Errorf("totalCost = %v, want 5.74", s.TotalCostUSD)
	}
	if s.PeakContext != 0 {
		t.Errorf("peakContext = %d, want 0", s.PeakContext)
	}
	if s.Ran != "9m47.9s" {
		t.Errorf("ran = %q, want 9m47.9s", s.Ran)
	}
	if s.RanSeconds != 587.9 {
		t.Errorf("ranSeconds = %v, want 587.9", s.RanSeconds)
	}
	if !strings.Contains(s.Halt, "loop impl-loop exhausted") {
		t.Errorf("halt = %q, want the real halt reason", s.Halt)
	}
	// "(none)" must become an empty list, not a one-element list of "(none)".
	if len(s.GateFailures) != 0 {
		t.Errorf("gateFailures = %v, want empty for '(none)'", s.GateFailures)
	}
	if s.Sandbagging != "(no mutation data)" {
		t.Errorf("sandbagging = %q", s.Sandbagging)
	}

	// The run-summary trailer.
	if len(s.Dispatched) != 1 {
		t.Fatalf("dispatched = %+v, want 1 row", s.Dispatched)
	}
	if s.Dispatched[0].Bead != "djinn-ipee.8" || s.Dispatched[0].Verdict != "FAILED" {
		t.Errorf("dispatched[0] = %+v", s.Dispatched[0])
	}

	// The skip warnings summoner printed for beads outside the ready set.
	if len(got.Warnings) == 0 {
		t.Error("expected the 'not in the ready set' warnings to be captured")
	}
	for _, w := range got.Warnings {
		if w.Bead == "" || w.Reason == "" {
			t.Errorf("incomplete warning %+v", w)
		}
	}
}

func TestApplySummaryField(t *testing.T) {
	tests := []struct {
		label string
		value string
		check func(t *testing.T, s *SessionSummary)
	}{
		{"iterations", "3", func(t *testing.T, s *SessionSummary) {
			if s.Iterations != 3 {
				t.Errorf("iterations = %d", s.Iterations)
			}
		}},
		{"total cost", "$12.50", func(t *testing.T, s *SessionSummary) {
			if s.TotalCostUSD != 12.5 {
				t.Errorf("cost = %v", s.TotalCostUSD)
			}
		}},
		{"total cost", "12.50", func(t *testing.T, s *SessionSummary) {
			if s.TotalCostUSD != 12.5 {
				t.Errorf("cost without $ = %v", s.TotalCostUSD)
			}
		}},
		{"peak context", "42%", func(t *testing.T, s *SessionSummary) {
			if s.PeakContext != 42 {
				t.Errorf("peakContext = %d", s.PeakContext)
			}
		}},
		{"halt", "(none)", func(t *testing.T, s *SessionSummary) {
			if s.Halt != "" {
				t.Errorf("halt = %q, want empty for (none)", s.Halt)
			}
		}},
		{"gate failures", "gate-a, gate-b", func(t *testing.T, s *SessionSummary) {
			if len(s.GateFailures) != 2 {
				t.Errorf("gateFailures = %v", s.GateFailures)
			}
		}},
		{"gate failures", "(none)", func(t *testing.T, s *SessionSummary) {
			if len(s.GateFailures) != 0 {
				t.Errorf("gateFailures = %v, want empty", s.GateFailures)
			}
		}},
		{"ran", "9m47.9s", func(t *testing.T, s *SessionSummary) {
			if s.RanSeconds != 587.9 {
				t.Errorf("ranSeconds = %v", s.RanSeconds)
			}
		}},
		{"ran", "not-a-duration", func(t *testing.T, s *SessionSummary) {
			if s.RanSeconds != 0 || s.Ran != "not-a-duration" {
				t.Errorf("an unparseable duration should keep the raw string and zero the seconds: %+v", s)
			}
		}},
		{"unknown label", "whatever", func(t *testing.T, s *SessionSummary) {}},
	}

	for _, tc := range tests {
		t.Run(tc.label+"="+tc.value, func(t *testing.T) {
			s := &SessionSummary{GateFailures: []string{}}
			applySummaryField(s, tc.label, tc.value)
			tc.check(t, s)
		})
	}
}

func TestParseConsoleEmptyAndPartial(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		got, err := ParseConsole(strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		if got.Summary != nil || len(got.Steps) != 0 {
			t.Errorf("empty input produced %+v", got)
		}
	})

	t.Run("steps but no summary", func(t *testing.T) {
		got, err := ParseConsole(strings.NewReader(
			"✅ Done [implement] (cost: $1.00, ctx: 5%, dur=1m0s)\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Steps) != 1 {
			t.Fatalf("steps = %+v", got.Steps)
		}
		if got.Summary != nil {
			t.Error("a log with no summary block must not synthesize one")
		}
		if got.TotalCost != 1.0 {
			t.Errorf("totalCost = %v", got.TotalCost)
		}
	})

	t.Run("truncated summary block", func(t *testing.T) {
		got, err := ParseConsole(strings.NewReader(
			"═══ Session Summary ═══\n  iterations:     2\n  closed:         1\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got.Summary == nil {
			t.Fatal("a partial block should still parse what is there")
		}
		if got.Summary.Iterations != 2 || got.Summary.Closed != 1 {
			t.Errorf("summary = %+v", got.Summary)
		}
	})
}

func TestParseDryRun(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		stderr    string
		wantOrder []string
		wantWarn  int
	}{
		{
			name:      "real dry-run output",
			stdout:    "djinn-aa5oc\ndjinn-p0juu\ndjinn-y9bru\n",
			stderr:    "summoner: --dry-run: 3 bead(s) would be dispatched for this scope (no workers spawned, no remote contacted, no bd mutation)\n",
			wantOrder: []string{"djinn-aa5oc", "djinn-p0juu", "djinn-y9bru"},
		},
		{
			name:      "empty scope",
			stdout:    "",
			stderr:    "summoner: --dry-run: 0 bead(s) would be dispatched for this scope\n",
			wantOrder: []string{},
		},
		{
			name:   "skip warnings on stderr",
			stdout: "djinn-ipee.8\n",
			stderr: "summoner: requested bead djinn-ipee.9 is not in the ready set (unknown or not ready); skipping\n" +
				"summoner: requested bead djinn-ipee.10 is not in the ready set (unknown or not ready); skipping\n",
			wantOrder: []string{"djinn-ipee.8"},
			wantWarn:  2,
		},
		{
			name:      "a banner on stdout is not mistaken for a bead",
			stdout:    "summoner: something\ndjinn-abc\n",
			wantOrder: []string{"djinn-abc"},
		},
		{
			name:      "non-id junk is ignored",
			stdout:    "djinn-abc\nnot a bead id\n/etc/passwd\n",
			wantOrder: []string{"djinn-abc"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDryRun(tc.stdout, tc.stderr)
			if len(got.Ordered) != len(tc.wantOrder) {
				t.Fatalf("ordered = %v, want %v", got.Ordered, tc.wantOrder)
			}
			for i, want := range tc.wantOrder {
				if got.Ordered[i] != want {
					t.Errorf("ordered[%d] = %q, want %q", i, got.Ordered[i], want)
				}
			}
			if len(got.Warnings) != tc.wantWarn {
				t.Errorf("warnings = %+v, want %d", got.Warnings, tc.wantWarn)
			}
			if got.Preflight {
				t.Error("dry-run must not claim preflight ran")
			}
		})
	}
}

func TestParsePreflight(t *testing.T) {
	got := ParsePreflight(
		"djinn-abc\ndjinn-def\n",
		"summoner: bead djinn-abc has operator-required criteria\n",
		nil,
	)
	if !got.Preflight {
		t.Error("Preflight flag not set")
	}
	if len(got.Ordered) != 2 {
		t.Errorf("ordered = %v", got.Ordered)
	}
	found := false
	for _, b := range got.NeedsOperator {
		if b == "djinn-abc" {
			found = true
		}
	}
	if !found {
		t.Errorf("needsOperator = %v, want it to include djinn-abc", got.NeedsOperator)
	}
}

// TestI1PreflightDoesNotInventBeads is the regression test for review finding
// I1. "operator-required" is the phrase summoner itself uses and is an
// id-shaped hyphenated token; it used to land in NeedsOperator as a phantom
// bead on the one screen whose job is precision before spending money.
func TestI1PreflightDoesNotInventBeads(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		stderr    string
		requested []string
		want      []string
	}{
		{
			// The reviewer's reproduction, verbatim.
			name:   "operator-required is prose, not a bead",
			stdout: "djinn-abc\n",
			stderr: "summoner: bead djinn-abc has operator-required criteria\n",
			want:   []string{"djinn-abc"},
		},
		{
			name:   "several prose tokens, none in scope",
			stdout: "djinn-abc\n",
			stderr: "summoner: operator-required criteria found; cannot-finish-unattended: djinn-abc\n",
			want:   []string{"djinn-abc"},
		},
		{
			// A bead named only in the request still counts: the caller asked
			// about it, so it is in scope even when summoner did not order it.
			name:      "requested but unordered bead is in scope",
			stdout:    "djinn-abc\n",
			stderr:    "summoner: bead djinn-zzz has operator-required criteria\n",
			requested: []string{"djinn-zzz"},
			want:      []string{"djinn-zzz"},
		},
		{
			name:   "a bead not in scope anywhere is ignored",
			stdout: "djinn-abc\n",
			stderr: "summoner: bead djinn-elsewhere has operator-required criteria\n",
			want:   []string{},
		},
		{
			name:   "no operator lines at all",
			stdout: "djinn-abc\ndjinn-def\n",
			stderr: "summoner: --preflight: 2 bead(s) checked\n",
			want:   []string{},
		},
		{
			name:   "multiple real beads flagged",
			stdout: "djinn-abc\ndjinn-def\ndjinn-ghi\n",
			stderr: "summoner: operator-required: djinn-abc, djinn-ghi\n",
			want:   []string{"djinn-abc", "djinn-ghi"},
		},
		{
			name:   "trailing punctuation is trimmed",
			stdout: "djinn-abc\n",
			stderr: "summoner: bead (djinn-abc) has operator-required criteria.\n",
			want:   []string{"djinn-abc"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParsePreflight(tc.stdout, tc.stderr, tc.requested)

			if len(got.NeedsOperator) != len(tc.want) {
				t.Fatalf("needsOperator = %v, want %v", got.NeedsOperator, tc.want)
			}
			for i, want := range tc.want {
				if got.NeedsOperator[i] != want {
					t.Errorf("needsOperator[%d] = %q, want %q", i, got.NeedsOperator[i], want)
				}
			}
			// Whatever survives must be a bead the caller could act on.
			inScope := map[string]bool{}
			for _, id := range got.Ordered {
				inScope[id] = true
			}
			for _, id := range tc.requested {
				inScope[id] = true
			}
			for _, id := range got.NeedsOperator {
				if !inScope[id] {
					t.Errorf("needsOperator contains %q, which is not a bead in scope", id)
				}
			}
		})
	}
}

// --- state dir -------------------------------------------------------------

// TestReadForensicsRealFixture runs against a byte-for-byte mirror of two real
// state directories.
func TestReadForensicsRealFixture(t *testing.T) {
	stateDir := filepath.Join("testdata", "statedir")

	t.Run("djinn-0jxm", func(t *testing.T) {
		f, err := ReadForensics(stateDir, "djinn-0jxm")
		if err != nil {
			t.Fatalf("ReadForensics: %v", err)
		}
		if f.ClaimSHA != "a4ec01ebb10a02059728b0e6ff5cebab642720bf" {
			t.Errorf("claimSha = %q", f.ClaimSHA)
		}
		if len(f.Timeline) == 0 {
			t.Fatal("no timeline entries")
		}

		// The loops this mirror actually contains, in formula order. The run it
		// was captured from was reopened rather than verified, so there is no
		// verify-loop.iter-N here and never was — asserting one made this test
		// red in every checkout (loom-390). verify-loop is covered by the
		// djinn-79t3b mirror below; the ordering *rule* (which directory order
		// happens to agree with for these names) is covered by
		// TestReadForensicsOrdersLoopsByFormulaNotName.
		wantOrder := []string{"ac-quality-loop", "impl-loop", "review-impl-loop", "review-tests-loop"}
		var seen []string
		for _, e := range f.Timeline {
			if len(seen) == 0 || seen[len(seen)-1] != e.Loop {
				seen = append(seen, e.Loop)
			}
		}
		if len(seen) != len(wantOrder) {
			t.Fatalf("loops = %v, want %v", seen, wantOrder)
		}
		for i, want := range wantOrder {
			if seen[i] != want {
				t.Errorf("loop[%d] = %q, want %q (formula order)", i, seen[i], want)
			}
		}

		// Iterations ascend within a loop.
		for i := 1; i < len(f.Timeline); i++ {
			a, b := f.Timeline[i-1], f.Timeline[i]
			if a.Loop == b.Loop && a.Iter > b.Iter {
				t.Errorf("%s iterations out of order: %d then %d", a.Loop, a.Iter, b.Iter)
			}
		}

		// Step is the loop name without the -loop suffix.
		for _, e := range f.Timeline {
			if e.Step != strings.TrimSuffix(e.Loop, "-loop") {
				t.Errorf("step = %q for loop %q", e.Step, e.Loop)
			}
		}

		// Both verdict shapes must come back: JSON and plain text.
		var sawJSON, sawText bool
		for _, v := range f.Verdicts {
			if v.JSON != nil {
				sawJSON = true
			}
			if v.Text != "" {
				sawText = true
			}
			if v.Procedure == "" {
				t.Errorf("verdict with no procedure: %+v", v)
			}
		}
		if !sawJSON {
			t.Error("expected at least one JSON verdict (impl-loop/verify-impl)")
		}
		if !sawText {
			t.Error("expected at least one plain-text verdict (review-impl-loop/review-impl)")
		}
	})

	t.Run("djinn-79t3b covers review-light", func(t *testing.T) {
		f, err := ReadForensics(stateDir, "djinn-79t3b")
		if err != nil {
			t.Fatal(err)
		}
		var loops []string
		for _, e := range f.Timeline {
			loops = append(loops, e.Loop)
		}
		if len(loops) != 3 {
			t.Fatalf("loops = %v, want 3", loops)
		}
		// ac-quality-loop → review-light-loop → verify-loop
		if loops[0] != "ac-quality-loop" || loops[len(loops)-1] != "verify-loop" {
			t.Errorf("loops = %v, want ac-quality first and verify last", loops)
		}
	})

	t.Run("bead with no state dir", func(t *testing.T) {
		f, err := ReadForensics(stateDir, "djinn-nonexistent")
		if err != nil {
			t.Fatalf("a bead with no state dir must not error: %v", err)
		}
		if len(f.Timeline) != 0 || len(f.Verdicts) != 0 {
			t.Errorf("expected empty forensics, got %+v", f)
		}
		if f.Timeline == nil || f.Verdicts == nil {
			t.Error("slices must serialize as [] not null")
		}
	})

	t.Run("path traversal is rejected", func(t *testing.T) {
		for _, bad := range []string{"../../etc/passwd", "..", ".", "", "a/b", "a\\b"} {
			if _, err := ReadForensics(stateDir, bad); err == nil {
				t.Errorf("ReadForensics(%q) should have been rejected", bad)
			}
		}
	})
}

// TestReadForensicsOrdersLoopsByFormulaNotName pins the ordering rule itself.
//
// Neither real mirror can do that job: os.ReadDir already returns their loop
// directories in the order the formula runs them, so those assertions would
// still pass if sortTimeline were deleted outright. These four loops are the
// pairs where the two orders disagree — freshness runs FIRST but sorts after
// ac-quality by name, and review-tests runs BEFORE review-light but sorts after
// it by name — so the timeline here is neither alphabetical nor directory
// order, and the test goes red the moment ordering degrades to either.
func TestReadForensicsOrdersLoopsByFormulaNotName(t *testing.T) {
	stateDir := t.TempDir()
	beadDir := filepath.Join(stateDir, "djinn-ord1")
	for _, loop := range []string{
		"ac-quality-loop.iter-1",
		"freshness-loop.iter-1",
		"review-light-loop.iter-1",
		"review-tests-loop.iter-1",
	} {
		if err := os.MkdirAll(filepath.Join(beadDir, loop), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	f, err := ReadForensics(stateDir, "djinn-ord1")
	if err != nil {
		t.Fatalf("ReadForensics: %v", err)
	}

	var seen []string
	for _, e := range f.Timeline {
		seen = append(seen, e.Loop)
	}
	want := []string{"freshness-loop", "ac-quality-loop", "review-tests-loop", "review-light-loop"}
	if len(seen) != len(want) {
		t.Fatalf("loops = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("loops = %v, want %v (formula order, not name order)", seen, want)
		}
	}
}

func TestIsProcedureFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"review-impl", true},
		{"verify-impl", true},
		{"ac-quality-review", true},
		{"review-light", true},
		{"review-impl.jsonl", false},
		{"cost.json", false},
		{"sandbagging.json", false},
		{"diff.patch", false},
		{"transcript.log", false},
		{".DS_Store", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isProcedureFile(tc.name); got != tc.want {
				t.Errorf("isProcedureFile(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestLoopDirPattern(t *testing.T) {
	tests := []struct {
		dir      string
		wantLoop string
		wantIter int
		wantOK   bool
	}{
		{"impl-loop.iter-1", "impl-loop", 1, true},
		{"review-impl-loop.iter-3", "review-impl-loop", 3, true},
		{"ac-quality-loop.iter-12", "ac-quality-loop", 12, true},
		{"claim.sha", "", 0, false},
		{"djinn-wtlu", "", 0, false},
		{"impl-loop", "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			m := loopDirPattern.FindStringSubmatch(tc.dir)
			if (m != nil) != tc.wantOK {
				t.Fatalf("match = %v, want %v", m != nil, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if m[1] != tc.wantLoop {
				t.Errorf("loop = %q, want %q", m[1], tc.wantLoop)
			}
		})
	}
}

func TestLoopRankOrdersUnknownLast(t *testing.T) {
	if loopRank("ac-quality-loop") >= loopRank("verify-loop") {
		t.Error("ac-quality must sort before verify")
	}
	if loopRank("something-new-loop") != unknownLoopOrder {
		t.Error("unknown loops should get the fallback rank")
	}
	if loopRank("something-new-loop") <= loopRank("verify-loop") {
		t.Error("unknown loops must sort after known ones")
	}
}

func TestReadCostCaps(t *testing.T) {
	t.Run("missing file yields no caps", func(t *testing.T) {
		caps, err := ReadCostCaps(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if caps.MaxCostUSDPerBead != nil || caps.MaxCostUSDPerSession != nil {
			t.Errorf("caps = %+v, want both nil", caps)
		}
	})

	t.Run("real config with no cost keys", func(t *testing.T) {
		// Byte-for-byte the shape of ~/Projects/djinn/.djinn/config.json.
		dir := t.TempDir()
		body := `{"djinn":{"author_model":"opus[1m]","author_effort":"high","push_retries":3}}`
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		caps, err := ReadCostCaps(dir)
		if err != nil {
			t.Fatal(err)
		}
		if caps.MaxCostUSDPerBead != nil || caps.MaxCostUSDPerSession != nil {
			t.Errorf("caps = %+v; absent keys must stay nil, not become 0", caps)
		}
	})

	t.Run("caps present", func(t *testing.T) {
		dir := t.TempDir()
		body := `{"djinn":{"max_cost_usd_per_bead":5.5,"max_cost_usd_per_session":40}}`
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		caps, err := ReadCostCaps(dir)
		if err != nil {
			t.Fatal(err)
		}
		if caps.MaxCostUSDPerBead == nil || *caps.MaxCostUSDPerBead != 5.5 {
			t.Errorf("perBead = %v", caps.MaxCostUSDPerBead)
		}
		if caps.MaxCostUSDPerSession == nil || *caps.MaxCostUSDPerSession != 40 {
			t.Errorf("perSession = %v", caps.MaxCostUSDPerSession)
		}
	})

	t.Run("malformed json errors", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadCostCaps(dir); err == nil {
			t.Error("expected an error for malformed config.json")
		}
	})
}

// --- evidence --------------------------------------------------------------

// TestParseEvidenceRealFixture parses two evidence files copied verbatim.
func TestParseEvidenceRealFixture(t *testing.T) {
	t.Run("rich file", func(t *testing.T) {
		e, err := ParseEvidence(mustReadFixture(t, "evidence-rich.yml"))
		if err != nil {
			t.Fatalf("ParseEvidence: %v", err)
		}
		if e.Bead == "" {
			t.Error("bead not parsed")
		}
		if e.Title == "" {
			t.Error("title not parsed")
		}
		if len(e.Criteria) == 0 {
			t.Fatal("no criteria parsed")
		}

		var withImpl, withVerification, withObservations int
		for i, c := range e.Criteria {
			if c.Text == "" {
				t.Errorf("criterion %d has no text", i)
			}
			if c.Status == "" {
				t.Errorf("criterion %d has no status", i)
			}
			if len(c.Implementation) > 0 {
				withImpl++
				for _, ref := range c.Implementation {
					if !strings.Contains(ref, ":") {
						t.Errorf("implementation ref %q is not path:line", ref)
					}
				}
			}
			if c.Verification != "" {
				withVerification++
			}
			if len(c.Evidence.Observations) > 0 {
				withObservations++
			}
			// Normalization: never nil.
			if c.Implementation == nil || c.Evidence.Observations == nil || c.Evidence.Snapshots == nil {
				t.Errorf("criterion %d has a nil slice; must serialize as []", i)
			}
		}
		if withImpl == 0 {
			t.Error("expected implementation references")
		}
		if withVerification == 0 {
			t.Error("expected verification commands")
		}
		if withObservations == 0 {
			t.Error("expected observations")
		}
	})

	t.Run("file with follow_up", func(t *testing.T) {
		e, err := ParseEvidence(mustReadFixture(t, "evidence-followup.yml"))
		if err != nil {
			t.Fatal(err)
		}
		if len(e.FollowUp) == 0 {
			t.Error("expected follow_up bead ids")
		}
		for _, id := range e.FollowUp {
			if !ValidID(id) {
				t.Errorf("follow_up id %q is not id-shaped", id)
			}
		}
	})
}

func TestParseEvidenceTolerance(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		check func(t *testing.T, e *Evidence)
	}{
		{
			name: "unknown fields are ignored",
			yaml: "bead: a-1\ntitle: t\nsome_future_field: 42\ncriteria: []\n",
			check: func(t *testing.T, e *Evidence) {
				if e.Bead != "a-1" {
					t.Errorf("bead = %q", e.Bead)
				}
			},
		},
		{
			name: "missing optionals are empty not nil",
			yaml: "bead: a-1\ntitle: t\ncriteria:\n  - text: c\n    status: pass\n",
			check: func(t *testing.T, e *Evidence) {
				if e.FollowUp == nil {
					t.Error("FollowUp must be []")
				}
				c := e.Criteria[0]
				if c.Implementation == nil || c.Evidence.Observations == nil {
					t.Error("criterion slices must be []")
				}
				if c.Reason != "" || c.Verification != "" {
					t.Errorf("absent optionals should be empty: %+v", c)
				}
			},
		},
		{
			name: "partial and fail statuses",
			yaml: "bead: a-1\ncriteria:\n  - text: x\n    status: partial\n    reason: half done\n  - text: y\n    status: fail\n",
			check: func(t *testing.T, e *Evidence) {
				pass, partial, fail, _ := e.Counts()
				if pass != 0 || partial != 1 || fail != 1 {
					t.Errorf("counts = %d/%d/%d", pass, partial, fail)
				}
				if e.Passed() {
					t.Error("Passed() must be false with a failing criterion")
				}
				if e.Criteria[0].Reason != "half done" {
					t.Errorf("reason = %q", e.Criteria[0].Reason)
				}
			},
		},
		{
			name: "non_code flag",
			yaml: "bead: a-1\ncriteria:\n  - text: x\n    status: pass\n    non_code: true\n",
			check: func(t *testing.T, e *Evidence) {
				if !e.Criteria[0].NonCode {
					t.Error("non_code not parsed")
				}
			},
		},
		{
			name: "empty document",
			yaml: "",
			check: func(t *testing.T, e *Evidence) {
				if e.Criteria == nil || e.FollowUp == nil {
					t.Error("empty document must still normalize slices")
				}
				if e.Passed() {
					t.Error("Passed() on empty criteria must be false")
				}
			},
		},
		{
			name: "snapshots when present",
			yaml: "bead: a-1\ncriteria:\n  - text: x\n    status: pass\n    evidence:\n      snapshots:\n        - shot.png\n",
			check: func(t *testing.T, e *Evidence) {
				if len(e.Criteria[0].Evidence.Snapshots) != 1 {
					t.Errorf("snapshots = %v", e.Criteria[0].Evidence.Snapshots)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := ParseEvidence([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("ParseEvidence: %v", err)
			}
			tc.check(t, e)
		})
	}
}

func TestParseEvidenceMalformed(t *testing.T) {
	if _, err := ParseEvidence([]byte("bead: [unclosed\n")); err == nil {
		t.Error("expected an error for malformed YAML")
	}
}

func TestReadEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "evidence"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evidence", "a-1.yml"),
		mustReadFixture(t, "evidence-rich.yml"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadEvidence(dir, "a-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected evidence")
	}

	// The common case: no evidence file is not an error.
	missing, err := ReadEvidence(dir, "a-2")
	if err != nil {
		t.Errorf("a missing evidence file must not error: %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil evidence, got %+v", missing)
	}

	for _, bad := range []string{"../secrets", "..", "a/b", ""} {
		if _, err := ReadEvidence(dir, bad); err == nil {
			t.Errorf("ReadEvidence(%q) should have been rejected", bad)
		}
	}
}

func TestValidID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"djinn-abc", true},
		{"djinn-ipee.8", true},
		{"loom-3ft.2.1", true},
		{"a_b-1", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../etc/passwd", false},
		// Flag-shaped values must not reach summoner's argv (review B1).
		{"--dry-run", false},
		{"-bead", false},
		{"-", false},
		{"a/b", false},
		{"a\\b", false},
		{"a b", false},
		{"a;b", false},
		{"a\nb", false},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			if got := ValidID(tc.id); got != tc.want {
				t.Errorf("ValidID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestUnfence(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare json untouched", `{"a":1}`, `{"a":1}`},
		{"json fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"bare fence", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"unterminated fence", "```json\n{\"a\":1}", `{"a":1}`},
		{"fence with no newline", "```json", "```json"},
		{"plain text untouched", "overall: SHIP", "overall: SHIP"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := unfence(tc.in); got != tc.want {
				t.Errorf("unfence(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestReadVerdictUnwrapsFencedJSON: real ac-quality-review files arrive wrapped
// in a markdown fence and must still come back as structured data.
func TestReadVerdictUnwrapsFencedJSON(t *testing.T) {
	dir := t.TempDir()
	loop := filepath.Join(dir, "bead-1", "ac-quality-loop.iter-1")
	if err := os.MkdirAll(loop, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "```json\n{\n  \"verdict\": \"CONCRETE\",\n  \"criteria_count\": 4\n}\n```\n"
	if err := os.WriteFile(filepath.Join(loop, "ac-quality-review"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := ReadForensics(dir, "bead-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Verdicts) != 1 {
		t.Fatalf("verdicts = %+v", f.Verdicts)
	}
	v := f.Verdicts[0]
	if v.JSON == nil {
		t.Fatalf("fenced JSON was not unwrapped; text = %q", v.Text)
	}
	m, ok := v.JSON.(map[string]any)
	if !ok || m["verdict"] != "CONCRETE" {
		t.Errorf("json = %#v", v.JSON)
	}
	if v.Text != "" {
		t.Error("Text should be empty once JSON parsed")
	}
}
