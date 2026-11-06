package queries

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSafeRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"main", true},
		{"remotes/origin/main", true},
		{"feature/loom-3ft.2", true},
		{"release-1.0", true},
		{"", false},
		{"main;DROP TABLE issues", false},
		{"main' OR '1'='1", false},
		{"main..main'", false},
		{"branch name", false},
		{"a\nb", false},
		{"back\\slash", false},
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			if got := safeRef.MatchString(tc.ref); got != tc.want {
				t.Errorf("safeRef(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestDoltStatusJSONShape pins the wire contract, especially the difference
// between "no remote" (ahead/behind omitted) and "zero ahead".
func TestDoltStatusJSONShape(t *testing.T) {
	origin := "origin"
	zero, two := 0, 2
	when := time.Date(2026, 8, 1, 17, 19, 46, 0, time.UTC)

	tests := []struct {
		name        string
		in          DoltStatus
		wantHas     []string
		wantMissing []string
	}{
		{
			name: "no remote omits ahead and behind and nulls remote",
			in: DoltStatus{
				Dirty:      true,
				Branch:     "main",
				LastCommit: &DoltCommit{Hash: "abc", Message: "bd: create loom-1", When: when},
			},
			wantHas:     []string{`"dirty":true`, `"remote":null`, `"branch":"main"`, `"lastCommit"`},
			wantMissing: []string{`"ahead"`, `"behind"`},
		},
		{
			name: "remote with zero ahead still reports the numbers",
			in: DoltStatus{
				Dirty:  false,
				Remote: &origin,
				Branch: "main",
				Ahead:  &zero,
				Behind: &two,
			},
			wantHas: []string{`"dirty":false`, `"remote":"origin"`, `"ahead":0`, `"behind":2`},
		},
		{
			name:    "no commits yields a null lastCommit",
			in:      DoltStatus{Branch: "main"},
			wantHas: []string{`"lastCommit":null`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(data)
			for _, want := range tc.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("JSON missing %s\ngot: %s", want, got)
				}
			}
			for _, unwanted := range tc.wantMissing {
				if strings.Contains(got, unwanted) {
					t.Errorf("JSON unexpectedly contains %s\ngot: %s", unwanted, got)
				}
			}
		})
	}
}

// TestZeroAheadIsNotOmitted guards the specific pointer-vs-omitempty trap: a
// plain int with omitempty would silently drop a legitimate ahead:0.
func TestZeroAheadIsNotOmitted(t *testing.T) {
	zero := 0
	origin := "origin"
	data, err := json.Marshal(DoltStatus{Remote: &origin, Ahead: &zero, Behind: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ahead":0`) || !strings.Contains(string(data), `"behind":0`) {
		t.Errorf("ahead/behind of 0 must be reported, got: %s", data)
	}
}

func TestDoltCommitTimeIsUTC(t *testing.T) {
	loc := time.FixedZone("JST", 9*3600)
	c := DoltCommit{When: time.Date(2026, 8, 2, 2, 19, 46, 0, loc)}
	c.When = c.When.UTC()

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "2026-08-01T17:19:46Z") {
		t.Errorf("timestamp must serialize as UTC, got: %s", data)
	}
}

func TestStaleAfterIsDocumented(t *testing.T) {
	if StaleAfter != 24*time.Hour {
		t.Errorf("StaleAfter = %s, want 24h as documented in the work package", StaleAfter)
	}
}
