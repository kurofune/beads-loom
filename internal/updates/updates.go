// Package updates renders the weekly initiative updates an agent stores in bd's
// key-value space. It is pure logic over values already read through `bd kv`:
// it performs no I/O, runs no command, and generates no content of its own.
//
// The producer is the /initiative-retro skill. Loom only reads what that skill
// wrote, checks it, and flips one field on publish — the division of labor
// mirrors internal/sweep, which assembles findings but never applies them.
package updates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// KeyPrefix is the namespace the retro skill writes under. One key per epic per
// ISO week: initiative-retro.<epic-id>.<YYYY-Www>.
const KeyPrefix = "initiative-retro."

// Key returns the storage key for one epic's week.
func Key(epic, week string) string { return KeyPrefix + epic + "." + week }

// weekPattern is the ISO week suffix of a key. It is what separates an update
// key from any other key sharing the prefix, and it is what keeps a child
// epic's keys (initiative-retro.loom-d1q.2.2026-W32) out of its parent's list.
var weekPattern = regexp.MustCompile(`^\d{4}-W\d{2}$`)

// ValidWeek reports whether s is a well-formed ISO week label.
func ValidWeek(s string) bool { return weekPattern.MatchString(s) }

// Update is one stored weekly update.
//
// Epic and Week always come from the key, which is the address the value was
// filed under; the remaining fields come from the value. A value Loom cannot
// parse still yields an Update — with ParseError set — so a single malformed
// write cannot make a whole epic's history disappear.
type Update struct {
	Epic        string `json:"epic"`
	Week        string `json:"week"`
	Status      string `json:"status"`
	GeneratedAt string `json:"generatedAt"`
	Body        string `json:"body"`
	Schema      int    `json:"schema"`
	// BodySHA is the publish guard: the client echoes the sha of the draft it
	// reviewed, and publish refuses when the stored body has moved since.
	BodySHA string `json:"bodySha"`
	// ParseError is bd's stored value refusing to decode, rendered as a row
	// rather than swallowed.
	ParseError string `json:"parseError"`
}

// storedUpdate is the wire shape the retro skill writes (SKILL.md §4). Decoding
// is deliberately lenient: unknown fields and unknown schema numbers are kept
// rather than rejected, because the body is what renders and the schema field
// exists so this reader can stay tolerant of the body's evolution.
type storedUpdate struct {
	Schema      int    `json:"schema"`
	Status      string `json:"status"`
	GeneratedAt string `json:"generated_at"`
	Body        string `json:"body"`
}

// FromKV returns one epic's updates, newest first.
//
// Weeks sort lexicographically because YYYY-Www is zero-padded, so a descending
// string sort is a descending chronological sort across the year boundary.
func FromKV(values map[string]string, epic string) []Update {
	prefix := KeyPrefix + epic + "."
	out := []Update{}
	for key, raw := range values {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		week := key[len(prefix):]
		if !ValidWeek(week) {
			continue
		}
		out = append(out, parse(epic, week, raw))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Week > out[j].Week })
	return out
}

func parse(epic, week, raw string) Update {
	u := Update{Epic: epic, Week: week}
	var stored storedUpdate
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		u.ParseError = err.Error()
		return u
	}
	u.Schema = stored.Schema
	u.Status = stored.Status
	u.GeneratedAt = stored.GeneratedAt
	u.Body = stored.Body
	u.BodySHA = BodySHA(stored.Body)
	return u
}

// Summary is the per-epic chip on the initiatives list: enough to show that an
// epic has updates and whether the newest one still needs publishing.
type Summary struct {
	Epic         string `json:"epic"`
	Total        int    `json:"total"`
	DraftCount   int    `json:"draftCount"`
	LatestWeek   string `json:"latestWeek"`
	LatestStatus string `json:"latestStatus"`
}

// Summaries counts stored updates per epic across every key in one KV read, so
// the list view joins by epic id instead of issuing a request per row.
func Summaries(values map[string]string) []Summary {
	byEpic := map[string]*Summary{}
	for key, raw := range values {
		epic, week, ok := splitKey(key)
		if !ok {
			continue
		}
		s := byEpic[epic]
		if s == nil {
			s = &Summary{Epic: epic}
			byEpic[epic] = s
		}
		s.Total++
		u := parse(epic, week, raw)
		if u.Status == "draft" {
			s.DraftCount++
		}
		if week > s.LatestWeek {
			s.LatestWeek = week
			s.LatestStatus = u.Status
		}
	}

	out := make([]Summary, 0, len(byEpic))
	for _, s := range byEpic {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Epic < out[j].Epic })
	return out
}

// splitKey pulls the epic and week out of an update key. The week is the final
// dot-segment, which is what lets a dotted child epic id survive the split.
func splitKey(key string) (epic, week string, ok bool) {
	if !strings.HasPrefix(key, KeyPrefix) {
		return "", "", false
	}
	rest := key[len(KeyPrefix):]
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 {
		return "", "", false
	}
	epic, week = rest[:dot], rest[dot+1:]
	if epic == "" || !ValidWeek(week) {
		return "", "", false
	}
	return epic, week, true
}

// BodySHA is the hex sha256 of a body, used as the publish TOCTOU guard the way
// sweep's materialDigest guards apply/dismiss.
func BodySHA(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// beadIDPattern is the shape of a bd issue id inside prose. It is deliberately
// loose, and it has exactly one job: proposing candidate ids whose existence is
// then confirmed against the project database. A candidate that names nothing
// costs one row in an IN clause and is dropped, so a false positive here is
// free. The publish gate uses the stricter citationIDPattern below.
var beadIDPattern = regexp.MustCompile(`\b[a-z][a-z0-9]*-[a-z0-9]+(?:\.[0-9]+)*\b`)

// citationIDPattern is the same shape narrowed to what bd actually mints: a
// project prefix, a two-to-four character base36 suffix, and optional dotted
// child segments. The submatches are the prefix, the suffix and the dotted
// tail, because the accept/reject rule in citesAnID needs all three.
var citationIDPattern = regexp.MustCompile(`\b([a-z][a-z0-9]*)-([a-z0-9]{2,4})((?:\.[0-9]+)*)\b`)

// shaPattern is a short or full git object name.
var shaPattern = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

// runIDPattern matches both run id shapes Loom mints: run-<unixnano> for its
// own runs (runs.NewRunID) and ext-<timestamp> for runs it synthesized from a
// djinn lifecycle log (runs.ExternalRunID).
var runIDPattern = regexp.MustCompile(`\b(?:run|ext)-[A-Za-z0-9._-]+\b`)

// listItemPattern matches a markdown list item at any indent, capturing its
// text so an error message can name the claim the way the author wrote it.
var listItemPattern = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)

// placeholderPattern matches the bullets the retro skill emits for an empty
// section. They assert nothing, so they need no citation.
var placeholderPattern = regexp.MustCompile(`^\(?(?:none|n/?a|nothing)\)?\.?$|^[—–-]$`)

// fencePattern matches a fenced code block delimiter.
var fencePattern = regexp.MustCompile("^\\s*(?:```|~~~)")

// ValidateCitations returns every claim in the epic's body that carries no
// citation, in the order they appear, each as the source line it came from.
//
// A claim is a markdown list item outside a fenced code block, excluding the
// empty-section placeholders ("(none)", "n/a", "—"). It passes when it contains
// a bead id, a git sha, or a run id.
//
// What this enforces is that no claim is uncited — not that every citation
// resolves. Resolving would need a database read, and publish must not depend
// on one; the epic id is the only project knowledge this gate uses, and it is
// the address the body was filed under.
func ValidateCitations(epic, body string) []string {
	prefix := projectPrefix(epic)
	uncited := []string{}
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if fencePattern.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := listItemPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[1])
		if text == "" || placeholderPattern.MatchString(strings.ToLower(text)) {
			continue
		}
		if citesAnID(text, prefix) || citesASHA(text) || runIDPattern.MatchString(text) {
			continue
		}
		uncited = append(uncited, strings.TrimSpace(line))
	}
	return uncited
}

// projectPrefix is the part of an id before its first hyphen — "loom" for
// loom-d1q. It is "" for a token with no hyphen, which then matches nothing.
func projectPrefix(id string) string {
	hyphen := strings.Index(id, "-")
	if hyphen <= 0 {
		return ""
	}
	return id[:hyphen]
}

// citesAnID reports whether a claim names something that could be a bead id in
// this epic's project.
//
// Shape alone is not enough: "read-only" and "follow-up" are id-shaped English,
// and accepting them let an uncited claim publish. A candidate counts when it
// carries a digit or a dotted child segment — no English compound does — or
// when its prefix is the epic's own project prefix, which is what keeps a real
// digitless id like loom-cmm from being refused.
//
// The trade-off is deliberate and one-sided: a citation of another project's
// digitless id (phoenix-abcd from a loom epic) is refused, and the author adds
// a sha or a local id. Accepting prose as a citation would be the worse error —
// it is the failure this gate exists to prevent.
func citesAnID(text, prefix string) bool {
	for _, m := range citationIDPattern.FindAllStringSubmatch(text, -1) {
		candidate, suffix, dotted := m[1], m[2], m[3]
		if dotted != "" || strings.ContainsAny(suffix, "0123456789") {
			return true
		}
		if prefix != "" && candidate == prefix {
			return true
		}
	}
	return false
}

// citesASHA reports whether a claim names a git object.
//
// A hex name is only distinguishable from a measurement by its letters: "10000000
// ops" is seven-plus hex-legal characters and cited nothing. Requiring at least
// one of a-f refuses the roughly one short sha in thirty that happens to be all
// digits; that author cites the bead id instead.
func citesASHA(text string) bool {
	for _, sha := range shaPattern.FindAllString(text, -1) {
		if strings.ContainsAny(sha, "abcdef") {
			return true
		}
	}
	return false
}

// CitedIssueIDs returns every bead-id-shaped token across a set of updates,
// deduplicated and sorted. These are candidates: the caller confirms which ones
// exist before rendering any of them as a link.
func CitedIssueIDs(list []Update) []string {
	return citedTokens(list, beadIDPattern)
}

// CitedRunIDs returns every run-id-shaped token across a set of updates, the
// same way CitedIssueIDs does for beads. These too are candidates: the caller
// confirms them against the project's run history before linking any of them.
func CitedRunIDs(list []Update) []string {
	return citedTokens(list, runIDPattern)
}

func citedTokens(list []Update, pattern *regexp.Regexp) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, u := range list {
		for _, token := range pattern.FindAllString(u.Body, -1) {
			if seen[token] {
				continue
			}
			seen[token] = true
			out = append(out, token)
		}
	}
	sort.Strings(out)
	return out
}
