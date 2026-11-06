// Package runs models summoner dispatch and djinn run monitoring: parsing the
// artifacts djinn leaves on disk, managing Loom-spawned summoner processes, and
// synthesizing runs that were started outside Loom.
//
// The guiding constraint is that Loom is a *window onto* djinn, not the only
// door into it. Everything here works on runs Loom never started.
package runs

import (
	"regexp"
	"time"
)

// RunState is the lifecycle of a run record.
type RunState string

const (
	// StateRunning means a managed process is alive, or an external run has
	// beads spawned and not yet exited.
	StateRunning RunState = "running"
	// StateStopping means SIGTERM was sent and the grace period is open.
	StateStopping RunState = "stopping"
	// StateStopped means Loom stopped the run.
	StateStopped RunState = "stopped"
	// StateFinished means the process exited on its own.
	StateFinished RunState = "finished"
	// StateFailed means the process exited non-zero.
	StateFailed RunState = "failed"
)

// RunSource records who started a run.
type RunSource string

const (
	// SourceLoom is a run Loom spawned and manages.
	SourceLoom RunSource = "loom"
	// SourceExternal is a run reconstructed from djinn's own logs, started by
	// someone at a terminal.
	SourceExternal RunSource = "external"
)

// Verdicts observed in real lifecycle logs.
//
// The work package lists COMPLETE and FAILED. Real logs from
// ~/Projects/djinn/.djinn/summoner-logs also contain CRASHED, so Verdict is a
// plain string and unknown values pass through rather than being dropped.
const (
	VerdictComplete = "COMPLETE"
	VerdictFailed   = "FAILED"
	VerdictCrashed  = "CRASHED"
)

// Run is one summoner invocation, managed or observed.
type Run struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Source  RunSource `json:"source"`

	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
	State     RunState   `json:"state"`

	// Args is the summoner argv Loom used. Empty for external runs, which is
	// how the UI knows not to offer a re-run button for them.
	Args []string `json:"args,omitempty"`

	// Summary is parsed from the console log's Session Summary block once the
	// run has produced one.
	Summary *SessionSummary `json:"summary,omitempty"`

	Beads []RunBead `json:"beads"`

	// CostSoFar is the sum of parsed per-step costs, in USD.
	CostSoFar float64 `json:"costSoFar"`
	// Caps are the project's configured guardrails, for charting cost against.
	Caps *CostCaps `json:"caps,omitempty"`

	// ExitError explains a failed managed run.
	ExitError string `json:"exitError,omitempty"`
}

// RunBead is one bead's participation in a run.
type RunBead struct {
	ID        string     `json:"id"`
	SpawnedAt time.Time  `json:"spawnedAt"`
	ExitedAt  *time.Time `json:"exitedAt"`
	// Verdict is COMPLETE, FAILED, CRASHED, or another value bd emits.
	Verdict string `json:"verdict,omitempty"`
	// FinalStatus is the detail column: the bead's bd status at exit
	// ("closed", "open"), or a halt reason such as
	// "operator-escalation halt (loop_exhausted)".
	FinalStatus string `json:"finalStatus,omitempty"`
	// Retries counts observed restart lines for this bead.
	Retries int `json:"retries"`
}

// Active reports whether this bead is still occupying a worker slot.
func (b RunBead) Active() bool { return b.ExitedAt == nil }

// RunDetail is a run plus the live view the detail page needs.
type RunDetail struct {
	Run
	// ActiveSlots are the beads spawned but not exited, with their current
	// step read from the state directory.
	ActiveSlots []WorkerSlot `json:"activeSlots"`
	// LogPath is where the console capture lives, if Loom captured one.
	LogPath string `json:"logPath,omitempty"`
}

// WorkerSlot is one in-flight bead.
type WorkerSlot struct {
	Bead      string    `json:"bead"`
	SpawnedAt time.Time `json:"spawnedAt"`
	// CurrentStep is the most recent state-dir entry, or nil if the worker has
	// not written any state yet.
	CurrentStep *TimelineEntry `json:"currentStep"`
}

// SessionSummary is the parsed `═══ Session Summary ═══` block.
type SessionSummary struct {
	Iterations   int     `json:"iterations"`
	Closed       int     `json:"closed"`
	Failures     int     `json:"failures"`
	TotalCostUSD float64 `json:"totalCostUsd"`
	// PeakContext is the percentage figure, 0-100.
	PeakContext int `json:"peakContext"`
	// Ran is the wall-clock duration as printed, e.g. "9m47.9s".
	Ran string `json:"ran"`
	// RanSeconds is Ran parsed, or 0 if it could not be.
	RanSeconds float64 `json:"ranSeconds"`
	// Halt is the halt reason, empty when the session ended cleanly.
	Halt string `json:"halt"`
	// GateFailures is empty when the log said "(none)".
	GateFailures []string `json:"gateFailures"`
	// Sandbagging is the raw sandbagging line, e.g. "(no mutation data)".
	Sandbagging string `json:"sandbagging,omitempty"`
	// Dispatched are the `run summary — N bead(s) dispatched:` trailer rows.
	Dispatched []DispatchedBead `json:"dispatched"`
	// FiledFollowUps counts nits filed, summed from structured
	// `nits_filed=N` fields in the console log.
	//
	// NOTE: the work package asks for filedFollowUps, but no such field exists
	// in a real Session Summary block (verified across every console log in
	// ~/Projects/djinn/.djinn/summoner-logs). nits_filed is the closest real
	// signal, so that is what this counts.
	FiledFollowUps int `json:"filedFollowUps"`
}

// DispatchedBead is one row of the run-summary trailer.
type DispatchedBead struct {
	Bead    string `json:"bead"`
	Verdict string `json:"verdict"`
}

// StepCost is one `✅ Done [...]` line from the console log.
type StepCost struct {
	// Step is the procedure name, e.g. "verify-impl" or "implement".
	Step string `json:"step"`
	// Iter and MaxIter are 0 when the line carried no `iter N/M` part —
	// which real logs do produce, e.g. `Done [implement]`.
	Iter    int `json:"iter"`
	MaxIter int `json:"maxIter"`

	CostUSD    float64 `json:"costUsd"`
	ContextPct int     `json:"contextPct"`
	// Duration is the raw string, e.g. "1m26.3s".
	Duration string `json:"duration"`
	// DurationSeconds is Duration parsed, or 0.
	DurationSeconds float64 `json:"durationSeconds"`
}

// TimelineEntry is one state-directory step for a bead.
type TimelineEntry struct {
	// Loop is the loop directory name, e.g. "review-impl-loop".
	Loop string `json:"loop"`
	// Step is Loop with the "-loop" suffix removed, for display.
	Step string `json:"step"`
	Iter int    `json:"iter"`
	// Procedure is the verdict file inside the loop directory, e.g.
	// "review-impl". Empty when the directory holds no procedure output yet.
	Procedure string    `json:"procedure"`
	ModTime   time.Time `json:"mtime"`
	// Order is the formula position used for sorting; see formulaOrder.
	Order int `json:"-"`
}

// Forensics is the per-bead state-directory view.
type Forensics struct {
	Bead string `json:"bead"`
	// ClaimSHA is the contents of claim.sha, if present.
	ClaimSHA string          `json:"claimSha,omitempty"`
	Timeline []TimelineEntry `json:"timeline"`
	// Verdicts holds each procedure's output, parsed as JSON when it is JSON
	// and returned as raw text otherwise — real state dirs contain both.
	Verdicts []Verdict `json:"verdicts"`
}

// Verdict is one procedure output file.
type Verdict struct {
	Loop      string `json:"loop"`
	Iter      int    `json:"iter"`
	Procedure string `json:"procedure"`
	// JSON is set when the file parsed as JSON.
	JSON any `json:"json,omitempty"`
	// Text is set when it did not.
	Text string `json:"text,omitempty"`
	// Truncated reports that the file was larger than the read cap.
	Truncated bool `json:"truncated,omitempty"`
}

// CostCaps are the guardrails from .djinn/config.json.
//
// Both fields are optional: the real config.json in ~/Projects/djinn/.djinn
// carries neither, so a nil pointer means "no cap configured" rather than zero.
type CostCaps struct {
	MaxCostUSDPerBead    *float64 `json:"maxCostUsdPerBead"`
	MaxCostUSDPerSession *float64 `json:"maxCostUsdPerSession"`
}

// SummonPreview is the result of a dry-run or preflight.
type SummonPreview struct {
	// Ordered is the bead IDs summoner would dispatch, in order.
	Ordered []string `json:"ordered"`
	// Warnings explain beads that were asked for but will not run.
	Warnings []PreviewWarning `json:"warnings"`
	// NeedsOperator are beads that cannot finish unattended.
	NeedsOperator []string `json:"needsOperator"`
	// Preflight reports whether the deeper LLM pass ran.
	Preflight bool `json:"preflight"`
}

// PreviewWarning is one bead that will not be dispatched.
type PreviewWarning struct {
	Bead   string `json:"bead"`
	Reason string `json:"reason"`
}

// safeID guards every path segment derived from a bead or run id before it
// touches the filesystem, and every id that reaches summoner's argv.
//
// The leading character class is load-bearing twice over: it stops `..`
// traversal, and it stops a flag-shaped value like `--dry-run` from being
// passed as `-bead --dry-run` and silently changing what summoner does — the
// same class of defect as review finding B1 in bdexec.
var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidID reports whether s is safe to use as a path segment and as a summoner
// argument. It rejects empty strings, separators, the "." / ".." traversal
// forms, and anything beginning with "-".
func ValidID(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return safeID.MatchString(s)
}
