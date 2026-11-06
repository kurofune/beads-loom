package updates

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
)

func TestKeyJoinsEpicAndWeekUnderTheSharedPrefix(t *testing.T) {
	if got := Key("loom-d1q", "2026-W32"); got != "initiative-retro.loom-d1q.2026-W32" {
		t.Fatalf("Key = %q", got)
	}
}

func TestFromKVKeepsOnlyThisEpicsWeeklyKeys(t *testing.T) {
	values := map[string]string{
		Key("loom-d1q", "2026-W31"):   `{"schema":1,"epic":"loom-d1q","week":"2026-W31","status":"draft","generated_at":"2026-08-02T20:00:00Z","body":"# W31"}`,
		Key("loom-d1q", "2026-W32"):   `{"schema":1,"epic":"loom-d1q","week":"2026-W32","status":"published","generated_at":"2026-08-08T01:17:20Z","body":"# W32"}`,
		Key("loom-d1q.2", "2026-W32"): `{"schema":1,"epic":"loom-d1q.2","week":"2026-W32","status":"draft","generated_at":"2026-08-08T01:17:20Z","body":"# child"}`,
		Key("loom-yqh", "2026-W32"):   `{"schema":1,"epic":"loom-yqh","week":"2026-W32","status":"draft","generated_at":"2026-08-08T01:17:20Z","body":"# other"}`,
		// Not a weekly update: the trailing segment is not an ISO week.
		"initiative-retro.loom-d1q.notes": `{"body":"x"}`,
		"loom.sweep.dismiss.abc123":       `{"kind":"untracked-commit"}`,
		"memory.note":                     "remembered",
	}

	got := FromKV(values, "loom-d1q")

	if len(got) != 2 {
		t.Fatalf("FromKV returned %d updates, want 2: %#v", len(got), got)
	}
	// A child epic's key shares the parent's prefix; only the exact epic counts.
	for _, u := range got {
		if u.Epic != "loom-d1q" {
			t.Fatalf("epic = %q, want loom-d1q", u.Epic)
		}
	}
	if got[0].Week != "2026-W32" || got[1].Week != "2026-W31" {
		t.Fatalf("weeks = %q, %q; want newest first", got[0].Week, got[1].Week)
	}
	if got[0].Status != "published" || got[0].Body != "# W32" || got[0].Schema != 1 {
		t.Fatalf("newest = %#v", got[0])
	}
	if got[0].GeneratedAt != "2026-08-08T01:17:20Z" {
		t.Fatalf("generatedAt = %q", got[0].GeneratedAt)
	}
	if got[0].BodySHA != sha256Hex("# W32") {
		t.Fatalf("bodySha = %q, want %q", got[0].BodySHA, sha256Hex("# W32"))
	}
}

func TestFromKVSortsAcrossTheYearBoundary(t *testing.T) {
	values := map[string]string{
		Key("loom-d1q", "2025-W52"): body("a"),
		Key("loom-d1q", "2026-W01"): body("b"),
		Key("loom-d1q", "2026-W09"): body("c"),
		Key("loom-d1q", "2026-W10"): body("d"),
	}

	var weeks []string
	for _, u := range FromKV(values, "loom-d1q") {
		weeks = append(weeks, u.Week)
	}
	want := []string{"2026-W10", "2026-W09", "2026-W01", "2025-W52"}
	if !reflect.DeepEqual(weeks, want) {
		t.Fatalf("weeks = %v, want %v", weeks, want)
	}
}

func TestFromKVKeepsMalformedValuesAsParseErrorRows(t *testing.T) {
	values := map[string]string{
		Key("loom-d1q", "2026-W30"): "not json at all",
		Key("loom-d1q", "2026-W31"): body("fine"),
	}

	got := FromKV(values, "loom-d1q")
	if len(got) != 2 {
		t.Fatalf("FromKV returned %d updates, want both rows: %#v", len(got), got)
	}
	broken := got[1]
	if broken.Week != "2026-W30" || broken.Epic != "loom-d1q" {
		t.Fatalf("broken row lost its address: %#v", broken)
	}
	if broken.ParseError == "" {
		t.Fatalf("broken row has no parseError: %#v", broken)
	}
	if got[0].ParseError != "" {
		t.Fatalf("good row gained a parseError: %#v", got[0])
	}
}

func TestFromKVToleratesUnknownSchemaAndUnknownFields(t *testing.T) {
	values := map[string]string{
		Key("loom-d1q", "2026-W32"): `{"schema":7,"epic":"loom-d1q","week":"2026-W32","status":"draft",` +
			`"generated_at":"2026-08-08T01:17:20Z","body":"# still renders","reviewers":["nate"],"nested":{"a":1}}`,
	}

	got := FromKV(values, "loom-d1q")
	if len(got) != 1 {
		t.Fatalf("FromKV returned %d updates, want 1", len(got))
	}
	if got[0].ParseError != "" || got[0].Body != "# still renders" || got[0].Schema != 7 {
		t.Fatalf("update = %#v, want a tolerant parse", got[0])
	}
}

func TestSummariesCountEveryEpicInDeterministicOrder(t *testing.T) {
	values := map[string]string{
		Key("loom-d1q", "2026-W30"): body("a"),
		Key("loom-d1q", "2026-W31"): body("b"),
		Key("loom-d1q", "2026-W32"): published("c"),
		Key("loom-yqh", "2026-W32"): body("d"),
		Key("aaa-001", "2026-W01"):  "not json",
		"memory.note":               "remembered",
	}

	got := Summaries(values)
	want := []Summary{
		{Epic: "aaa-001", Total: 1, DraftCount: 0, LatestWeek: "2026-W01", LatestStatus: ""},
		{Epic: "loom-d1q", Total: 3, DraftCount: 2, LatestWeek: "2026-W32", LatestStatus: "published"},
		{Epic: "loom-yqh", Total: 1, DraftCount: 1, LatestWeek: "2026-W32", LatestStatus: "draft"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summaries = %#v, want %#v", got, want)
	}
}

func TestSummariesIsEmptyNotNilWhenNoUpdatesExist(t *testing.T) {
	got := Summaries(map[string]string{"memory.note": "remembered"})
	if got == nil || len(got) != 0 {
		t.Fatalf("Summaries = %#v, want an empty non-nil slice", got)
	}
}

func TestBodySHAIsTheFullHexSHA256OfTheBody(t *testing.T) {
	if got := BodySHA("# W32"); got != sha256Hex("# W32") {
		t.Fatalf("BodySHA = %q", got)
	}
	if len(BodySHA("")) != 64 {
		t.Fatalf("BodySHA length = %d, want 64", len(BodySHA("")))
	}
	if BodySHA("a") == BodySHA("b") {
		t.Fatal("BodySHA collided on different bodies")
	}
}

func TestValidateCitationsNamesEveryUncitedClaim(t *testing.T) {
	uncited := ValidateCitations("loom-d1q", `# loom-d1q — 2026-W32

## Closed
- loom-d1q.1 — sweep landed — shipped in b64f334
- We tidied up a few things along the way
  * The board felt faster afterwards

## Started
+ loom-d1q.3 — retro skill (loom-d1q.3)

Prose lines are not claims, even uncited ones.
`)

	want := []string{
		"- We tidied up a few things along the way",
		"* The board felt faster afterwards",
	}
	if !reflect.DeepEqual(uncited, want) {
		t.Fatalf("ValidateCitations = %#v, want %#v", uncited, want)
	}
}

func TestValidateCitationsAcceptsEveryCitationShape(t *testing.T) {
	cases := map[string]string{
		"bead id":        "- loom-d1q.1 closed",
		"short sha":      "- shipped in b64f334",
		"long sha":       "- shipped in b64f334a9b5f44647528e8cfef24faa2b503b4c8",
		"loom run id":    "- verified by run-1754270000000000000",
		"external run":   "- verified by ext-20260804T120000Z",
		"placeholder":    "- (none)",
		"placeholder na": "- n/a",
		"em dash":        "- —",
	}
	for name, line := range cases {
		if got := ValidateCitations("loom-d1q", line); len(got) != 0 {
			t.Errorf("%s: ValidateCitations(%q) = %#v, want none", name, line, got)
		}
	}
}

// An id-shaped English compound and a long run of digits are the two shapes the
// loose pattern used to accept. Both publish a claim that cites nothing.
func TestValidateCitationsRejectsIDShapedProseAndPlainDigitRuns(t *testing.T) {
	cases := map[string]string{
		"hyphenated compound": "- We shipped the read-only view",
		"another compound":    "- The follow-up work is queued",
		"digit run":           "- Improved throughput by 10000000 ops",
		"short digit run":     "- Cut the p99 to 1234567 nanoseconds",
	}
	for name, line := range cases {
		got := ValidateCitations("loom-d1q", line)
		if len(got) != 1 {
			t.Errorf("%s: ValidateCitations(%q) = %#v, want the claim named as uncited", name, line, got)
		}
	}
}

// bd mints three-character base36 suffixes, and plenty carry no digit at all
// (loom-cmm, loom-sna are real). The epic's own project prefix is what tells
// those from prose, so requiring a digit alone would refuse a real citation.
func TestValidateCitationsAcceptsAProjectIDWithNoDigitInIt(t *testing.T) {
	for _, line := range []string{"- loom-cmm closed", "- loom-sna discovered"} {
		if got := ValidateCitations("loom-d1q", line); len(got) != 0 {
			t.Errorf("ValidateCitations(%q) = %#v, want none", line, got)
		}
	}
	// The same token under a different project's epic is not that project's id.
	if got := ValidateCitations("djinn-a1b", "- loom-cmm closed"); len(got) != 1 {
		t.Errorf("ValidateCitations under a foreign epic = %#v, want the claim named", got)
	}
}

// A sha candidate must look like hex, not like a metric.
func TestValidateCitationsAcceptsAShaOnlyWhenItCarriesHexLetters(t *testing.T) {
	if got := ValidateCitations("loom-d1q", "- shipped in b64f334"); len(got) != 0 {
		t.Errorf("ValidateCitations = %#v, want the sha accepted", got)
	}
	if got := ValidateCitations("loom-d1q", "- shipped in 1000000"); len(got) != 1 {
		t.Errorf("ValidateCitations = %#v, want an all-digit run refused", got)
	}
}

func TestValidateCitationsIgnoresListItemsInsideFencedCode(t *testing.T) {
	got := ValidateCitations("loom-d1q", "## Closed\n\n```bash\n- this is shell output, not a claim\n```\n\n- and this one is (loom-d1q.1)\n")
	if len(got) != 0 {
		t.Fatalf("ValidateCitations = %#v, want none", got)
	}
}

func TestValidateCitationsAcceptsAQuietWeekWithNoBullets(t *testing.T) {
	got := ValidateCitations("loom-yqh", "# loom-yqh — 2026-W33\n\nQuiet week for loom-yqh: no beads closed, started, or discovered 2026-08-10–2026-08-16.\n\n## Reconciliation vs 2026-W32\nNothing was in flight.\n")
	if len(got) != 0 {
		t.Fatalf("ValidateCitations = %#v, want none", got)
	}
}

func TestCitedIssueIDsDedupesAcrossBodiesInStableOrder(t *testing.T) {
	got := CitedIssueIDs([]Update{
		{Body: "- loom-d1q.1 closed (loom-d1q.1)\n- loom-9dx discovered"},
		{Body: "- loom-9dx again\n- loom-d1q.2 started\n- sha b64f334 is not an id"},
		{Body: "", ParseError: "boom"},
	})
	want := []string{"loom-9dx", "loom-d1q.1", "loom-d1q.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CitedIssueIDs = %#v, want %#v", got, want)
	}
}

func TestCitedRunIDsDedupesBothRunShapesInStableOrder(t *testing.T) {
	got := CitedRunIDs([]Update{
		{Body: "- closed by run-1754270000000000000\n- and by ext-20260804T120000Z"},
		{Body: "- run-1754270000000000000 again\n- sha b64f334 is not a run id"},
		{Body: "", ParseError: "boom"},
	})
	want := []string{"ext-20260804T120000Z", "run-1754270000000000000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CitedRunIDs = %#v, want %#v", got, want)
	}
}

func TestValidWeekAcceptsOnlyISOWeekKeys(t *testing.T) {
	for _, ok := range []string{"2026-W01", "2026-W53", "1999-W09"} {
		if !ValidWeek(ok) {
			t.Errorf("ValidWeek(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"2026-W1", "2026W01", "26-W01", "2026-w01", "", "2026-W001", "notes"} {
		if ValidWeek(bad) {
			t.Errorf("ValidWeek(%q) = true, want false", bad)
		}
	}
}

// --- helpers --------------------------------------------------------------

func body(text string) string {
	return `{"schema":1,"status":"draft","generated_at":"2026-08-08T01:17:20Z","body":"` + text + `"}`
}

func published(text string) string {
	return `{"schema":1,"status":"published","generated_at":"2026-08-08T01:17:20Z","body":"` + text + `"}`
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
