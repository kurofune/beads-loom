package queries

import "time"

// NoTargetLabel is the explicit opt-out for the missing-target nudge: an epic
// carrying it is stating that it is deliberately not time-boxed, so Loom stops
// asking for a due date.
//
// It is a bd label rather than Loom-local state on purpose. The label is
// visible to `bd show`, settable from the CLI (`bd tag <epic> no-target`), and
// syncs with the rest of the beads DB, so a human at a terminal and an agent
// reading the bead see the same opt-out the cockpit does.
const NoTargetLabel = "no-target"

// TargetInput is the complete input to the missing-target rule.
type TargetInput struct {
	Status string
	// Total is the transitive descendant count — the same denominator the
	// Progress bar and ClassifyHealth use, so the nudge cannot disagree with
	// the health chip about whether an epic has scope.
	Total  int
	Due    *time.Time
	Labels []string
}

// NeedsTarget reports whether an epic is in the children-but-no-due-date state
// that the Initiatives nudge asks the manager to resolve.
//
// It is deliberately narrower than health's "no-target" state, which fires for
// any dateless epic. Three cases read as "no target" but are not defects, and
// none of them is worth interrupting a manager over:
//
//   - a CLOSED epic — nothing is left to land, so there is no date to be on
//     track for;
//   - an epic with no descendants — that reads "no scope" on the health chip,
//     and the missing thing is the work, not the date;
//   - an epic carrying NoTargetLabel — the manager has already answered.
func NeedsTarget(in TargetInput) bool {
	if in.Status == StatusClosed || in.Total == 0 || in.Due != nil {
		return false
	}
	for _, label := range in.Labels {
		if label == NoTargetLabel {
			return false
		}
	}
	return true
}
