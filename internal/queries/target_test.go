package queries

import (
	"testing"
	"time"
)

func TestNeedsTargetFiresOnlyForTheChildrenButNoDueDateState(t *testing.T) {
	due := time.Date(2026, 8, 16, 15, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input TargetInput
		want  bool
	}{
		{
			name:  "open epic with descendants and no due date needs a target",
			input: TargetInput{Status: StatusOpen, Total: 3, Due: nil},
			want:  true,
		},
		{
			name:  "in_progress epic with descendants and no due date needs a target",
			input: TargetInput{Status: StatusInProgress, Total: 9, Due: nil},
			want:  true,
		},
		{
			name: "explicit no-target opt-out silences an otherwise-flagged epic",
			input: TargetInput{
				Status: StatusOpen, Total: 3, Due: nil,
				Labels: []string{"initiatives", NoTargetLabel},
			},
			want: false,
		},
		{
			name:  "an epic that already has a due date needs nothing",
			input: TargetInput{Status: StatusOpen, Total: 3, Due: &due},
			want:  false,
		},
		{
			// A dateless epic with no descendants reads "no scope", not
			// "no target": the missing thing is the work, not the date.
			name:  "an epic with no descendants is a scope problem, not a target problem",
			input: TargetInput{Status: StatusOpen, Total: 0, Due: nil},
			want:  false,
		},
		{
			// Nothing is left to land, so there is nothing to be on track for.
			name:  "a closed epic is never asked for a target",
			input: TargetInput{Status: StatusClosed, Total: 5, Due: nil},
			want:  false,
		},
		{
			name:  "an unrelated label does not count as the opt-out",
			input: TargetInput{Status: StatusOpen, Total: 3, Due: nil, Labels: []string{"no-target-yet"}},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsTarget(tt.input); got != tt.want {
				t.Fatalf("NeedsTarget(%+v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// The nudge and the health chip must agree about what "no target" means: any
// epic the nudge flags must be exactly an epic the health ladder could not
// classify for want of a date.
func TestNeedsTargetImpliesTheNoTargetHealthState(t *testing.T) {
	in := TargetInput{Status: StatusOpen, Total: 4, Due: nil}
	if !NeedsTarget(in) {
		t.Fatal("precondition: this input must need a target")
	}
	health := ClassifyHealth(HealthInput{
		Total: in.Total, Closed: 1, ClosedInWindow: 1,
		Due: in.Due, Now: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	})
	if health.State != "no-target" {
		t.Fatalf("health state = %q, want no-target", health.State)
	}
}
