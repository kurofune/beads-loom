package runs

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ansiPattern matches CSI escape sequences (colour, bold, reset) plus the
// simpler OSC and single-character escapes summoner's output can contain.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// StripANSI removes terminal escape sequences. The console log is an
// ANSI-coloured stream, and every field the parsers read is wrapped in colour
// codes — `closed: \x1b[2m0\x1b[0m` — so stripping has to happen before any
// value is parsed, not after.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	return ansiPattern.ReplaceAllString(s, "")
}

// stepLinePattern matches the per-step completion lines.
//
// Both real shapes are supported — with and without the iteration part:
//
//	✅ Done [verify-impl iter 2/3] (cost: $0.8135, ctx: 6%, dur=1m26.3s)
//	✅ Done [implement] (cost: $1.9275, ctx: 8%, dur=4m25s)
var stepLinePattern = regexp.MustCompile(
	`Done \[([^\]\s]+)(?:\s+iter\s+(\d+)/(\d+))?\]\s*\(cost:\s*\$([0-9.]+),\s*ctx:\s*(\d+)%,\s*dur=([^)]+)\)`)

// ParseStepLine extracts a StepCost from one console line, already
// ANSI-stripped or not.
func ParseStepLine(line string) (StepCost, bool) {
	m := stepLinePattern.FindStringSubmatch(StripANSI(line))
	if m == nil {
		return StepCost{}, false
	}
	sc := StepCost{Step: m[1], Duration: strings.TrimSpace(m[6])}
	if m[2] != "" {
		sc.Iter, _ = strconv.Atoi(m[2])
		sc.MaxIter, _ = strconv.Atoi(m[3])
	}
	sc.CostUSD, _ = strconv.ParseFloat(m[4], 64)
	sc.ContextPct, _ = strconv.Atoi(m[5])
	sc.DurationSeconds = parseDurationSeconds(sc.Duration)
	return sc, true
}

// summaryHeaderPattern matches the Session Summary banner. The real banner is
// drawn with U+2550 box characters: `═══ Session Summary ═══════════════`.
var summaryHeaderPattern = regexp.MustCompile(`Session Summary`)

// summaryFieldPattern matches an indented `label: value` row inside the block.
var summaryFieldPattern = regexp.MustCompile(`^\s{2,}([a-z][a-z ]*[a-z]):\s*(.*)$`)

// dispatchHeaderPattern matches `summoner: run summary — 1 bead(s) dispatched:`.
// The dash is a real em dash (U+2014) in the logs, so it is matched loosely.
var dispatchHeaderPattern = regexp.MustCompile(`summoner: run summary\s+\S+\s+(\d+) bead\(s\) dispatched:`)

// dispatchRowPattern matches the trailer rows: `  djinn-ipee.8\tFAILED`.
var dispatchRowPattern = regexp.MustCompile(`^\s+(\S+)\s+([A-Z]+)\s*$`)

// nitsFiledPattern extracts nits_filed=N from structured `formula step done`
// lines, which is the only real signal for filed follow-ups.
var nitsFiledPattern = regexp.MustCompile(`nits_filed=(\d+)`)

// skipWarningPattern matches summoner's scope warnings:
//
//	summoner: requested bead djinn-ipee.9 is not in the ready set (unknown or not ready); skipping
var skipWarningPattern = regexp.MustCompile(
	`summoner: requested bead (\S+) is not in the ready set \(([^)]*)\)`)

// ConsoleParse is everything extracted from a console log.
type ConsoleParse struct {
	Steps    []StepCost
	Summary  *SessionSummary
	Warnings []PreviewWarning
	// TotalCost is the sum of Steps' costs, used for live cost tracking before
	// a Session Summary exists.
	TotalCost float64
}

// ParseConsole reads an ANSI-coloured summoner console stream.
func ParseConsole(r io.Reader) (*ConsoleParse, error) {
	out := &ConsoleParse{}

	sc := bufio.NewScanner(r)
	// Console lines include full LLM tool payloads and can be very long.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	inSummary := false
	var summary *SessionSummary
	inDispatch := false
	nits := 0

	for sc.Scan() {
		raw := sc.Text()
		line := StripANSI(raw)

		if step, ok := ParseStepLine(line); ok {
			out.Steps = append(out.Steps, step)
			out.TotalCost += step.CostUSD
		}
		if m := nitsFiledPattern.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			nits += n
		}
		if m := skipWarningPattern.FindStringSubmatch(line); m != nil {
			out.Warnings = append(out.Warnings, PreviewWarning{Bead: m[1], Reason: m[2]})
		}

		switch {
		case summaryHeaderPattern.MatchString(line):
			inSummary = true
			summary = &SessionSummary{GateFailures: []string{}, Dispatched: []DispatchedBead{}}
			continue

		case inSummary:
			if m := summaryFieldPattern.FindStringSubmatch(line); m != nil {
				applySummaryField(summary, strings.TrimSpace(m[1]), strings.TrimSpace(m[2]))
				continue
			}
			// Any non-field line ends the block — including the closing banner.
			inSummary = false
		}

		if m := dispatchHeaderPattern.FindStringSubmatch(line); m != nil {
			inDispatch = true
			if summary == nil {
				summary = &SessionSummary{GateFailures: []string{}, Dispatched: []DispatchedBead{}}
			}
			continue
		}
		if inDispatch {
			if m := dispatchRowPattern.FindStringSubmatch(line); m != nil {
				summary.Dispatched = append(summary.Dispatched,
					DispatchedBead{Bead: m[1], Verdict: m[2]})
				continue
			}
			if strings.TrimSpace(line) != "" {
				inDispatch = false
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("read console log: %w", err)
	}

	if summary != nil {
		summary.FiledFollowUps = nits
		out.Summary = summary
	}
	return out, nil
}

// applySummaryField fills one row of the Session Summary block.
func applySummaryField(s *SessionSummary, label, value string) {
	if s == nil {
		return
	}
	switch label {
	case "iterations":
		s.Iterations, _ = strconv.Atoi(value)
	case "closed":
		s.Closed, _ = strconv.Atoi(value)
	case "failures":
		s.Failures, _ = strconv.Atoi(value)
	case "total cost":
		s.TotalCostUSD = parseUSD(value)
	case "peak context":
		s.PeakContext, _ = strconv.Atoi(strings.TrimSuffix(value, "%"))
	case "ran":
		s.Ran = value
		s.RanSeconds = parseDurationSeconds(value)
	case "halt":
		if !isNone(value) {
			s.Halt = value
		}
	case "gate failures":
		if !isNone(value) {
			for _, part := range strings.Split(value, ",") {
				if p := strings.TrimSpace(part); p != "" {
					s.GateFailures = append(s.GateFailures, p)
				}
			}
		}
	case "sandbagging":
		s.Sandbagging = value
	}
}

// isNone recognises summoner's empty-value placeholders.
func isNone(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || v == "(none)" || v == "none" || v == "-"
}

// parseUSD reads "$5.74" or "5.74".
func parseUSD(v string) float64 {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "$"))
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}

// parseDurationSeconds parses Go-style durations as printed by summoner
// ("9m47.9s", "1m26.3s", "21.5s", "4m25s"). It returns 0 when the value cannot
// be parsed, so a format change degrades the number rather than the whole parse.
func parseDurationSeconds(v string) float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d.Seconds()
	}
	return 0
}

// ParseDryRun reads the stdout and stderr of `summoner --dry-run`.
//
// Verified against the real binary: stdout carries bare bead IDs one per line,
// stderr carries the human summary and any warnings.
func ParseDryRun(stdout, stderr string) SummonPreview {
	p := SummonPreview{Ordered: []string{}, Warnings: []PreviewWarning{}, NeedsOperator: []string{}}

	for _, line := range strings.Split(StripANSI(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "summoner:") {
			continue
		}
		// Defensive: only accept things shaped like ids, so a future banner on
		// stdout cannot be mistaken for a bead.
		if !ValidID(line) {
			continue
		}
		p.Ordered = append(p.Ordered, line)
	}

	for _, line := range strings.Split(StripANSI(stderr), "\n") {
		if m := skipWarningPattern.FindStringSubmatch(line); m != nil {
			p.Warnings = append(p.Warnings, PreviewWarning{Bead: m[1], Reason: m[2]})
		}
	}
	return p
}

// ParsePreflight reads `summoner --preflight` output.
//
// Preflight prints the beads that have operator-required criteria. Its exact
// decoration was not captured empirically because every invocation costs real
// LLM spend, so the scan is tolerant about *where* an id appears.
//
// It is not tolerant about *what counts as an id*. Candidates are intersected
// with the beads actually in scope — the ordered set from stdout, plus whatever
// the caller requested. Without that, the phrase summoner itself uses,
// "operator-required", is an id-shaped hyphenated token and lands in the list as
// a phantom bead. The one screen whose job is precision before spending money
// must not invent beads out of prose.
func ParsePreflight(stdout, stderr string, requested []string) SummonPreview {
	p := ParseDryRun(stdout, stderr)
	p.Preflight = true

	inScope := make(map[string]bool, len(p.Ordered)+len(requested))
	for _, id := range p.Ordered {
		inScope[id] = true
	}
	for _, id := range requested {
		inScope[id] = true
	}
	if len(inScope) == 0 {
		return p
	}

	seen := map[string]bool{}
	for _, stream := range []string{stdout, stderr} {
		for _, line := range strings.Split(StripANSI(stream), "\n") {
			if !strings.Contains(strings.ToLower(line), "operator") {
				continue
			}
			for _, tok := range strings.FieldsFunc(line, func(r rune) bool {
				return r == ' ' || r == '\t' || r == ',' || r == ':' || r == ';'
			}) {
				tok = strings.Trim(tok, "\"'()[].")
				if inScope[tok] && !seen[tok] {
					seen[tok] = true
					p.NeedsOperator = append(p.NeedsOperator, tok)
				}
			}
		}
	}
	return p
}
