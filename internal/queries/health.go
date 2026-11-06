package queries

import (
	"fmt"
	"time"
)

const (
	// HealthWindowDays is two weeks: long enough to smooth one quiet week
	// (on-call or holiday), but short enough to turn a stalled epic red within
	// one sprint. Seven days flickers; 30 lets an old burst mask a current stall.
	HealthWindowDays = 14
	// AtRiskRateRatio is the last recoverable point: achieving half the needed
	// rate projects delivery at about twice the remaining runway.
	AtRiskRateRatio = 0.5
	// OnTrackRateRatio is definitional: meeting the required rate is on track.
	OnTrackRateRatio = 1.0
)

// HealthInput is the complete deterministic input to initiative health.
type HealthInput struct {
	Total          int
	Closed         int
	ClosedInWindow int
	Due            *time.Time
	Now            time.Time
}

// InitiativeHealth is the compact, server-explained initiative health signal.
type InitiativeHealth struct {
	State       string `json:"state"`
	Explanation string `json:"explanation"`
}

// ClassifyHealth applies the first-match initiative-health rule ladder.
func ClassifyHealth(in HealthInput) InitiativeHealth {
	if in.Total == 0 {
		return InitiativeHealth{State: "no-scope", Explanation: "No descendants — nothing to burn down"}
	}
	if in.Due == nil {
		return InitiativeHealth{State: "no-target", Explanation: "No due date — no target to assess"}
	}

	remaining := in.Total - in.Closed
	if remaining == 0 {
		return InitiativeHealth{
			State:       "on-track",
			Explanation: fmt.Sprintf("All %d descendants closed", in.Total),
		}
	}

	daysToDue := utcCalendarDays(in.Now, *in.Due)
	if daysToDue < 0 {
		return InitiativeHealth{
			State: "off-track",
			Explanation: fmt.Sprintf(
				"Past due by %dd with %d of %d descendants still open",
				-daysToDue, remaining, in.Total,
			),
		}
	}

	runwayDays := daysToDue
	if runwayDays < 1 {
		runwayDays = 1
	}
	rate := float64(in.ClosedInWindow) / HealthWindowDays
	required := float64(remaining) / float64(runwayDays)
	ratio := rate / required
	state := "off-track"
	if ratio >= OnTrackRateRatio {
		state = "on-track"
	} else if ratio >= AtRiskRateRatio {
		state = "at-risk"
	}

	return InitiativeHealth{
		State: state,
		Explanation: fmt.Sprintf(
			"closed %d in %dd, %d remain, due in %dd — required rate %.2f/day; achieving %.0f%% of it (%s)",
			in.ClosedInWindow, HealthWindowDays, remaining, daysToDue, required, ratio*100, state,
		),
	}
}

func utcCalendarDays(now, due time.Time) int {
	now = now.UTC()
	due = due.UTC()
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dueDate := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, time.UTC)
	return int(dueDate.Sub(nowDate) / (24 * time.Hour))
}
