package queries

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyHealthRuleLadder(t *testing.T) {
	now := time.Date(2026, 8, 3, 23, 30, 0, 0, time.UTC)
	dueIn := func(days int) *time.Time {
		due := time.Date(2026, 8, 3+days, 0, 1, 0, 0, time.UTC)
		return &due
	}

	tests := []struct {
		name             string
		input            HealthInput
		wantState        string
		wantExplanation  string
		rateDrivenInputs []string
	}{
		{
			name:             "ratio exactly 1.0 is on track across a UTC date boundary",
			input:            HealthInput{Total: 10, Closed: 8, ClosedInWindow: 2, Due: dueIn(14), Now: now},
			wantState:        "on-track",
			rateDrivenInputs: []string{"closed 2 in 14d", "2 remain", "due in 14d"},
		},
		{
			name:             "ratio 0.995 remains at risk below the on-track boundary",
			input:            HealthInput{Total: 200, Closed: 0, ClosedInWindow: 199, Due: dueIn(14), Now: now},
			wantState:        "at-risk",
			rateDrivenInputs: []string{"closed 199 in 14d", "200 remain", "due in 14d"},
		},
		{
			name:             "ratio just under 1.0 is at risk",
			input:            HealthInput{Total: 20, Closed: 6, ClosedInWindow: 13, Due: dueIn(14), Now: now},
			wantState:        "at-risk",
			rateDrivenInputs: []string{"closed 13 in 14d", "14 remain", "due in 14d"},
		},
		{
			name:             "ratio exactly 0.5 is at risk",
			input:            HealthInput{Total: 10, Closed: 8, ClosedInWindow: 1, Due: dueIn(14), Now: now},
			wantState:        "at-risk",
			rateDrivenInputs: []string{"closed 1 in 14d", "2 remain", "due in 14d"},
		},
		{
			name:             "ratio just under 0.5 is off track",
			input:            HealthInput{Total: 20, Closed: 7, ClosedInWindow: 6, Due: dueIn(14), Now: now},
			wantState:        "off-track",
			rateDrivenInputs: []string{"closed 6 in 14d", "13 remain", "due in 14d"},
		},
		{
			name:             "ratio 0.49 stays off track just below the at-risk boundary",
			input:            HealthInput{Total: 200, Closed: 100, ClosedInWindow: 49, Due: dueIn(14), Now: now},
			wantState:        "off-track",
			rateDrivenInputs: []string{"closed 49 in 14d", "100 remain", "due in 14d"},
		},
		{
			name:             "zero trailing rate with work remaining is off track",
			input:            HealthInput{Total: 10, Closed: 1, ClosedInWindow: 0, Due: dueIn(30), Now: now},
			wantState:        "off-track",
			rateDrivenInputs: []string{"closed 0 in 14d", "9 remain", "due in 30d"},
		},
		{
			name:             "due today has one day of runway",
			input:            HealthInput{Total: 2, Closed: 1, ClosedInWindow: 14, Due: dueIn(0), Now: now},
			wantState:        "on-track",
			rateDrivenInputs: []string{"closed 14 in 14d", "1 remain", "due in 0d"},
		},
		{
			name:            "past due with work remaining is off track",
			input:           HealthInput{Total: 5, Closed: 4, ClosedInWindow: 14, Due: dueIn(-1), Now: now},
			wantState:       "off-track",
			wantExplanation: "Past due by 1d with 1 of 5 descendants still open",
		},
		{
			name:            "missing due date has no target",
			input:           HealthInput{Total: 5, Closed: 2, ClosedInWindow: 2, Due: nil, Now: now},
			wantState:       "no-target",
			wantExplanation: "No due date — no target to assess",
		},
		{
			name:            "empty epic has no scope",
			input:           HealthInput{Total: 0, Closed: 0, ClosedInWindow: 0, Due: dueIn(10), Now: now},
			wantState:       "no-scope",
			wantExplanation: "No descendants — nothing to burn down",
		},
		{
			name:            "empty epic without due date is still no scope",
			input:           HealthInput{Total: 0, Closed: 0, ClosedInWindow: 0, Due: nil, Now: now},
			wantState:       "no-scope",
			wantExplanation: "No descendants — nothing to burn down",
		},
		{
			name:             "high percent closed is off track when recent burn collapsed",
			input:            HealthInput{Total: 100, Closed: 95, ClosedInWindow: 0, Due: dueIn(10), Now: now},
			wantState:        "off-track",
			rateDrivenInputs: []string{"closed 0 in 14d", "5 remain", "due in 10d"},
		},
		{
			name:             "low percent closed is on track when recent burn is high",
			input:            HealthInput{Total: 100, Closed: 5, ClosedInWindow: 14, Due: dueIn(95), Now: now},
			wantState:        "on-track",
			rateDrivenInputs: []string{"closed 14 in 14d", "95 remain", "due in 95d"},
		},
		{
			name:            "overdue but complete is on track",
			input:           HealthInput{Total: 7, Closed: 7, ClosedInWindow: 0, Due: dueIn(-1), Now: now},
			wantState:       "on-track",
			wantExplanation: "All 7 descendants closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyHealth(tt.input)
			if got.State != tt.wantState {
				t.Fatalf("state = %q, want %q (explanation: %q)", got.State, tt.wantState, got.Explanation)
			}
			if tt.wantExplanation != "" && got.Explanation != tt.wantExplanation {
				t.Fatalf("explanation = %q, want %q", got.Explanation, tt.wantExplanation)
			}
			for _, input := range tt.rateDrivenInputs {
				if !strings.Contains(got.Explanation, input) {
					t.Errorf("explanation %q does not contain rate input %q", got.Explanation, input)
				}
			}
			if len(tt.rateDrivenInputs) > 0 && !strings.Contains(got.Explanation, "required rate") {
				t.Errorf("explanation %q does not name the required rate rule", got.Explanation)
			}
		})
	}
}
