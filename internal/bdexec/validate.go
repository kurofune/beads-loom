package bdexec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidID is returned when an identifier would not be safe to place in
// bd's argv. Callers map it to 400.
var ErrInvalidID = errors.New("invalid identifier")

// issueIDPattern is the strict shape of a bd issue ID: a prefix, a hyphen, a
// hash, and optional dotted child segments — `loom-3ft`, `loom-3ft.2.1`,
// `djinn-ipee.8`. A wisp carries a literal `-wisp` segment: `loom-wisp-4v8`.
//
// The leading `[A-Za-z]` is the load-bearing part. bd puts positional ids
// *before* flags and offers no `--` terminator, so an id beginning with `-` is
// consumed by bd's flag parser instead: `bd update --claim --priority 1` leaves
// no positional id at all, and bd then falls back to "the last touched issue"
// and mutates a bead the caller never named. Validation is the only defence.
var issueIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(?:-wisp)?-[a-z0-9]+(?:\.[0-9]+)*$`)

// labelPattern is the shape of a bd label. Real labels include `phase-1`,
// `layer-ui`, `review-follow-up` and `<dim>:<val>` state labels such as
// `evidence_status:fail`, so `:` is allowed — but a label may never begin with
// `-`, for the same argv reason as ids.
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

// tokenPattern is the shape of a set-state dimension or value.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidIssueID reports whether s is a well-formed bd issue ID.
func ValidIssueID(s string) bool { return issueIDPattern.MatchString(s) }

// ValidLabel reports whether s is a well-formed bd label.
func ValidLabel(s string) bool { return labelPattern.MatchString(s) }

// ValidToken reports whether s is safe as a set-state dimension or value.
func ValidToken(s string) bool { return tokenPattern.MatchString(s) }

// checkIssueID returns a typed error for a bad id.
func checkIssueID(verb, s string) error {
	if ValidIssueID(s) {
		return nil
	}
	return fmt.Errorf("%s: %w: issue id %q", verb, ErrInvalidID, s)
}

// checkIssueIDs validates every id in a list.
func checkIssueIDs(verb string, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%s: no issue ids", verb)
	}
	for _, id := range ids {
		if err := checkIssueID(verb, id); err != nil {
			return err
		}
	}
	return nil
}

// checkLabels validates every label in a list.
func checkLabels(verb string, labels []string) error {
	for _, l := range labels {
		if !ValidLabel(l) {
			return fmt.Errorf("%s: %w: label %q", verb, ErrInvalidID, l)
		}
	}
	return nil
}

// Flag VALUES are deliberately not validated.
//
// The B1 fix originally rejected any flag value beginning with "-", on the
// theory that bd's parser would consume it as a flag. That theory is wrong for
// values: bd is cobra/pflag, which takes the argument after a flag
// unconditionally. Verified against the real binary with --dry-run:
//
//	bd create --title "-wontfix title" --description "- first bullet\n- second"
//	→ Title: -wontfix title
//	→ Description: - first bullet\n- second
//
// and even `--description "--ephemeral"` arrives as the literal string rather
// than setting the --ephemeral flag. Rejecting these turned the most natural
// shape for a description — a markdown bullet list — into a 400.
//
// The vector B1 actually closes is POSITIONAL arguments: ids and labels, which
// bd places before flags with no "--" terminator available. Those keep their
// strict patterns above, and that is what makes `bd update --claim` impossible
// to reach.

// checkPositionalText guards free-text that is passed POSITIONALLY rather than
// as a flag value — today just the `bd query <expr>` expression.
//
// Unlike a flag value, a positional beginning with "-" really is consumed by
// pflag as a flag, so this one stays. It is a much narrower rule than the
// blanket flag-value check it replaced.
func checkPositionalText(verb, name, value string) error {
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s: %w: %s is positional and may not begin with '-' (got %q)",
			verb, ErrInvalidID, name, value)
	}
	return nil
}
