package runs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxVerdictBytes caps how much of a procedure output file is read. Verdicts
// are small JSON blobs; the same directories also hold multi-megabyte
// transcripts and .jsonl streams that must never be slurped into a response.
const maxVerdictBytes = 64 * 1024

// loopDirPattern matches a state-directory step: `review-impl-loop.iter-3`.
var loopDirPattern = regexp.MustCompile(`^(.+)\.iter-(\d+)$`)

// formulaOrder ranks loops by djinn's formula order:
// ac-quality → implement → reviews → verify → commit-push.
//
// Verified against real state directories by directory mtime:
// ac-quality-loop 12:41 → impl-loop 13:03 → review-*-loop 13:16 → verify-loop 14:06.
// freshness-loop and review-light-loop also occur in real data and are slotted
// where they run.
var formulaOrder = map[string]int{
	"freshness-loop":    10,
	"ac-quality-loop":   20,
	"impl-loop":         30,
	"review-impl-loop":  40,
	"review-tests-loop": 50,
	"review-light-loop": 60,
	"verify-loop":       70,
	"commit-push-loop":  80,
}

// unknownLoopOrder sorts loops the formula does not name after the known ones,
// rather than silently at the front.
const unknownLoopOrder = 900

// nonProcedureFiles are the sidecar files that live beside a procedure verdict
// in a loop directory and are not themselves verdicts.
var nonProcedureFiles = map[string]bool{
	"cost.json":        true,
	"sandbagging.json": true,
	"diff.patch":       true,
	"transcript.log":   true,
}

// isProcedureFile reports whether a file in a loop directory is the procedure's
// verdict output rather than a sidecar.
//
// Real loop directories contain, for example, `review-impl` (the verdict, JSON
// or plain text) alongside `review-impl.jsonl` (the raw agent transcript),
// `cost.json`, `diff.patch` and `transcript.log`. The verdict is the extensionless
// entry.
func isProcedureFile(name string) bool {
	if nonProcedureFiles[name] || strings.HasPrefix(name, ".") {
		return false
	}
	return filepath.Ext(name) == ""
}

// ReadForensics builds the per-bead state-directory view.
//
// stateDir is `<workdir>/.djinn/state`. A bead with no state directory yields
// an empty Forensics and no error — a bead that has not been worked on is a
// normal condition, not a failure.
func ReadForensics(stateDir, bead string) (*Forensics, error) {
	if !ValidID(bead) {
		return nil, fmt.Errorf("invalid bead id %q", bead)
	}
	dir := filepath.Join(stateDir, bead)

	f := &Forensics{Bead: bead, Timeline: []TimelineEntry{}, Verdicts: []Verdict{}}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, fmt.Errorf("read state dir for %s: %w", bead, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			if e.Name() == "claim.sha" {
				if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
					f.ClaimSHA = strings.TrimSpace(string(data))
				}
			}
			continue
		}

		m := loopDirPattern.FindStringSubmatch(e.Name())
		if m == nil {
			// Nested bead directories and other stray dirs occur in real state
			// trees; they are not steps.
			continue
		}
		loop := m[1]
		iter, _ := strconv.Atoi(m[2])

		entry := TimelineEntry{
			Loop:  loop,
			Step:  strings.TrimSuffix(loop, "-loop"),
			Iter:  iter,
			Order: loopRank(loop),
		}
		if info, err := e.Info(); err == nil {
			entry.ModTime = info.ModTime().UTC()
		}

		loopPath := filepath.Join(dir, e.Name())
		inner, err := os.ReadDir(loopPath)
		if err != nil {
			f.Timeline = append(f.Timeline, entry)
			continue
		}
		for _, in := range inner {
			if in.IsDir() || !isProcedureFile(in.Name()) {
				continue
			}
			entry.Procedure = in.Name()

			v := Verdict{Loop: loop, Iter: iter, Procedure: in.Name()}
			readVerdict(filepath.Join(loopPath, in.Name()), &v)
			f.Verdicts = append(f.Verdicts, v)
			break
		}
		f.Timeline = append(f.Timeline, entry)
	}

	sortTimeline(f.Timeline)
	sort.SliceStable(f.Verdicts, func(i, j int) bool {
		a, b := f.Verdicts[i], f.Verdicts[j]
		if ra, rb := loopRank(a.Loop), loopRank(b.Loop); ra != rb {
			return ra < rb
		}
		if a.Loop != b.Loop {
			return a.Loop < b.Loop
		}
		return a.Iter < b.Iter
	})
	return f, nil
}

// readVerdict loads a procedure output, parsing it as JSON when it is JSON.
//
// Real state dirs hold both: `verify-impl` is
// `{"complete":true,"verdict":"APPROVE",...}` while `review-impl` is the plain
// line `overall: SHIP-WITH-FIXES (0 BLOCKER, 1 IMPORTANT fix, ...)`.
func readVerdict(path string, v *Verdict) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()

	buf := make([]byte, maxVerdictBytes+1)
	n, _ := fh.Read(buf)
	if n <= 0 {
		return
	}
	data := buf[:n]
	if n > maxVerdictBytes {
		data = data[:maxVerdictBytes]
		v.Truncated = true
	}

	trimmed := strings.TrimSpace(string(data))
	if !v.Truncated {
		candidate := unfence(trimmed)
		if strings.HasPrefix(candidate, "{") || strings.HasPrefix(candidate, "[") {
			var parsed any
			if err := json.Unmarshal([]byte(candidate), &parsed); err == nil {
				v.JSON = parsed
				return
			}
		}
	}
	v.Text = trimmed
}

// unfence strips a markdown code fence around a verdict body.
//
// Real ac-quality-review files are written by an LLM and arrive wrapped in
// ```json … ```, while verify-impl writes bare JSON. Unwrapping means both end
// up as structured data instead of one silently degrading to text.
func unfence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line (which may carry a language tag).
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	} else {
		return s
	}
	if end := strings.LastIndex(s, "```"); end >= 0 {
		s = s[:end]
	}
	return strings.TrimSpace(s)
}

// sortTimeline orders steps by formula position, then iteration, then mtime,
// so the timeline reads the way the work actually happened.
func sortTimeline(t []TimelineEntry) {
	sort.SliceStable(t, func(i, j int) bool {
		a, b := t[i], t[j]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		if a.Loop != b.Loop {
			return a.Loop < b.Loop
		}
		if a.Iter != b.Iter {
			return a.Iter < b.Iter
		}
		return a.ModTime.Before(b.ModTime)
	})
}

func loopRank(loop string) int {
	if r, ok := formulaOrder[loop]; ok {
		return r
	}
	return unknownLoopOrder
}

// LatestStep returns the most recently modified timeline entry, which is the
// step a live worker is on.
func LatestStep(timeline []TimelineEntry) *TimelineEntry {
	if len(timeline) == 0 {
		return nil
	}
	best := timeline[0]
	for _, e := range timeline[1:] {
		if e.ModTime.After(best.ModTime) {
			best = e
		}
	}
	return &best
}

// ReadCostCaps reads the guardrails from `<workdir>/.djinn/config.json`.
//
// Both keys are optional — the real config.json in ~/Projects/djinn/.djinn
// carries neither — so a missing file or missing key yields nil fields rather
// than a zero cap that the UI would render as "budget exhausted".
func ReadCostCaps(djinnDir string) (*CostCaps, error) {
	path := filepath.Join(djinnDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &CostCaps{}, nil
		}
		return nil, fmt.Errorf("read djinn config: %w", err)
	}

	var cfg struct {
		Djinn struct {
			MaxCostUSDPerBead    *float64 `json:"max_cost_usd_per_bead"`
			MaxCostUSDPerSession *float64 `json:"max_cost_usd_per_session"`
		} `json:"djinn"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse djinn config: %w", err)
	}
	return &CostCaps{
		MaxCostUSDPerBead:    cfg.Djinn.MaxCostUSDPerBead,
		MaxCostUSDPerSession: cfg.Djinn.MaxCostUSDPerSession,
	}, nil
}
