package bdexec

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Client turns typed write requests into exact bd invocations.
//
// Every verb takes an explicit list of issue IDs. That is deliberate: `bd
// update` and `bd close` fall back to "the last touched issue" when given no
// ID, which would let an empty request mutate something the user never named.
type Client struct {
	runner Runner
}

// New returns a Client over the given runner.
func New(r Runner) *Client { return &Client{runner: r} }

// Runner exposes the underlying runner, for callers that need a verb this
// package does not model yet.
func (c *Client) Runner() Runner { return c.runner }

// UpdateFields is a partial update. Nil fields are left alone; a non-nil
// pointer to the empty string clears the field where bd supports that
// (--defer "" and --due "" clear the date).
type UpdateFields struct {
	Status   *string
	Priority *int
	Assignee *string
	Title    *string
	Type     *string
	Estimate *int

	Description *string
	Acceptance  *string
	Design      *string
	Notes       *string

	AddLabels    []string
	RemoveLabels []string

	Defer *string
	Due   *string
}

// IsEmpty reports whether the patch would produce a bd call with no flags.
func (f UpdateFields) IsEmpty() bool {
	return f.Status == nil && f.Priority == nil && f.Assignee == nil &&
		f.Title == nil && f.Type == nil && f.Estimate == nil &&
		f.Description == nil && f.Acceptance == nil && f.Design == nil && f.Notes == nil &&
		f.Defer == nil && f.Due == nil &&
		len(f.AddLabels) == 0 && len(f.RemoveLabels) == 0
}

// validate checks the parts of a patch Loom is responsible for.
//
// Labels become positional-adjacent tokens that bd matches strictly, so they
// keep the label pattern. Status, assignee, title, type, the dates and the
// prose fields (description, acceptance, design, notes) are flag *values*:
// pflag consumes them verbatim, so a title of "-wontfix" is a title, not a
// flag. Validating those would reject legitimate prose (see validate.go).
func (f UpdateFields) validate() error {
	if err := checkLabels("update", f.AddLabels); err != nil {
		return err
	}
	return checkLabels("update", f.RemoveLabels)
}

// flags renders the patch as bd flags in a fixed order, so the argv is
// deterministic and testable.
func (f UpdateFields) flags() []string {
	var args []string
	if f.Status != nil {
		args = append(args, "--status", *f.Status)
	}
	if f.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*f.Priority))
	}
	if f.Assignee != nil {
		args = append(args, "--assignee", *f.Assignee)
	}
	if f.Title != nil {
		args = append(args, "--title", *f.Title)
	}
	if f.Type != nil {
		args = append(args, "--type", *f.Type)
	}
	if f.Estimate != nil {
		args = append(args, "--estimate", strconv.Itoa(*f.Estimate))
	}
	if f.Description != nil {
		args = append(args, "--description", *f.Description)
	}
	if f.Acceptance != nil {
		args = append(args, "--acceptance", *f.Acceptance)
	}
	if f.Design != nil {
		args = append(args, "--design", *f.Design)
	}
	if f.Notes != nil {
		args = append(args, "--notes", *f.Notes)
	}
	for _, l := range f.AddLabels {
		args = append(args, "--add-label", l)
	}
	for _, l := range f.RemoveLabels {
		args = append(args, "--remove-label", l)
	}
	if f.Defer != nil {
		args = append(args, "--defer", *f.Defer)
	}
	if f.Due != nil {
		args = append(args, "--due", *f.Due)
	}
	return args
}

// Update applies one patch to one or more issues in a single `bd update`
// invocation — which is what makes the bulk endpoint atomic from bd's point of
// view rather than a loop of separate commands.
func (c *Client) Update(ctx context.Context, workdir string, ids []string, f UpdateFields) error {
	if err := checkIssueIDs("update", ids); err != nil {
		return err
	}
	if f.IsEmpty() {
		return ErrNoFields
	}
	if err := f.validate(); err != nil {
		return err
	}
	args := append([]string{"update"}, ids...)
	args = append(args, f.flags()...)
	return c.exec(ctx, workdir, args)
}

// CloseOptions shape one `bd close` invocation.
type CloseOptions struct {
	// Reason is shared by every id in the call.
	Reason string
	// Force passes --force, which bd 1.3+ requires to close an issue whose
	// assignee is another actor. Set it only after a person has seen that
	// assignee and confirmed: bd's refusal is the one place a manager learns
	// they are about to close a teammate's claimed work.
	Force bool
}

// Close closes one or more issues. Without opts.Force, bd 1.3+ refuses an
// issue claimed by another actor; ClaimedByAnother recognises that refusal.
func (c *Client) Close(ctx context.Context, workdir string, ids []string, opts CloseOptions) error {
	if err := checkIssueIDs("close", ids); err != nil {
		return err
	}
	args := append([]string{"close"}, ids...)
	if opts.Reason != "" {
		args = append(args, "--reason", opts.Reason)
	}
	if opts.Force {
		args = append(args, "--force")
	}
	return c.exec(ctx, workdir, args)
}

// Reopen reopens closed issues, clearing closed_at and emitting a Reopened
// event — which `bd update --status open` would not do.
func (c *Client) Reopen(ctx context.Context, workdir string, ids []string, reason string) error {
	if err := checkIssueIDs("reopen", ids); err != nil {
		return err
	}
	args := append([]string{"reopen"}, ids...)
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	return c.exec(ctx, workdir, args)
}

// Comment adds a comment, piping the text through stdin so arbitrary prose —
// quotes, newlines, leading dashes — cannot be mistaken for flags.
func (c *Client) Comment(ctx context.Context, workdir, id, text string) error {
	if err := checkIssueID("comment", id); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("comment: empty text")
	}
	args := []string{"comment", id, "--stdin"}
	stdout, stderr, err := c.runner.RunStdin(ctx, workdir, text, args...)
	if err != nil {
		return newBdError(args, stdout, stderr, err)
	}
	return nil
}

// Dep is one dependency edge to create alongside a new issue.
type Dep struct {
	// ID is the issue the new one depends on.
	ID string
	// Type is a bd dependency type; empty means "blocks".
	Type string
}

// CreateSpec describes a new issue.
type CreateSpec struct {
	Title       string
	Type        string
	Priority    *int
	Description string
	Acceptance  string
	Design      string
	Notes       string
	Parent      string
	Labels      []string
	Estimate    *int
	Deps        []Dep
}

// createdIDPattern matches a bd issue ID such as loom-3ft or loom-3ft.2.
var createdIDPattern = regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9_]*-[a-z0-9]+(?:\.[0-9]+)*)\b`)

// Create creates an issue, then applies labels and dependency edges.
//
// It returns the new issue ID even when a follow-up step fails, so the caller
// can report exactly which bead was left half-configured rather than leaking an
// orphan the user cannot find.
func (c *Client) Create(ctx context.Context, workdir string, spec CreateSpec) (string, error) {
	if strings.TrimSpace(spec.Title) == "" {
		return "", errors.New("create: title is required")
	}
	if spec.Parent != "" {
		if err := checkIssueID("create", spec.Parent); err != nil {
			return "", err
		}
	}
	if spec.Type != "" && !ValidToken(spec.Type) {
		return "", fmt.Errorf("create: %w: type %q", ErrInvalidID, spec.Type)
	}
	if err := checkLabels("create", spec.Labels); err != nil {
		return "", err
	}
	for _, d := range spec.Deps {
		if err := checkIssueID("create", d.ID); err != nil {
			return "", err
		}
		if d.Type != "" && !ValidToken(d.Type) {
			return "", fmt.Errorf("create: %w: dep type %q", ErrInvalidID, d.Type)
		}
	}
	args := []string{"create", "--title", spec.Title}
	if spec.Type != "" {
		args = append(args, "--type", spec.Type)
	}
	if spec.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*spec.Priority))
	}
	if spec.Description != "" {
		args = append(args, "--description", spec.Description)
	}
	if spec.Acceptance != "" {
		args = append(args, "--acceptance", spec.Acceptance)
	}
	if spec.Design != "" {
		args = append(args, "--design", spec.Design)
	}
	if spec.Notes != "" {
		args = append(args, "--notes", spec.Notes)
	}
	if spec.Parent != "" {
		args = append(args, "--parent", spec.Parent)
	}
	if spec.Estimate != nil {
		args = append(args, "--estimate", strconv.Itoa(*spec.Estimate))
	}
	// --silent makes bd print only the new ID, so the caller does not depend
	// on the shape of the human-readable confirmation line.
	args = append(args, "--silent")

	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)
	if err != nil {
		return "", newBdError(args, stdout, stderr, err)
	}
	id := ParseCreatedID(stdout)
	if id == "" {
		return "", fmt.Errorf("create: could not find an issue id in bd output: %q", strings.TrimSpace(stdout))
	}

	// Labels go on with `bd tag` rather than `bd create --labels`, keeping one
	// label per invocation so a single bad label cannot fail the whole create.
	for _, label := range spec.Labels {
		if strings.TrimSpace(label) == "" {
			continue
		}
		if err := c.Tag(ctx, workdir, id, label); err != nil {
			return id, err
		}
	}

	for _, dep := range spec.Deps {
		if strings.TrimSpace(dep.ID) == "" {
			continue
		}
		if err := c.DepAdd(ctx, workdir, id, dep.ID, dep.Type); err != nil {
			return id, err
		}
	}
	return id, nil
}

// ParseCreatedID pulls an issue ID out of bd create output. With --silent that
// is the whole of stdout; the regex fallback keeps this working if bd's output
// mode changes.
func ParseCreatedID(stdout string) string {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return ""
	}
	// Fast path: --silent prints exactly the id.
	if !strings.ContainsAny(trimmed, " \t\n") && createdIDPattern.MatchString(trimmed) {
		return trimmed
	}
	for _, line := range strings.Split(trimmed, "\n") {
		if m := createdIDPattern.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// Tag adds one label to an issue.
func (c *Client) Tag(ctx context.Context, workdir, id, label string) error {
	if err := checkIssueID("tag", id); err != nil {
		return err
	}
	if err := checkLabels("tag", []string{label}); err != nil {
		return err
	}
	return c.exec(ctx, workdir, []string{"tag", id, label})
}

// DepAdd records that `from` depends on (is blocked by) `to`.
//
// Direction matters and is easy to get backwards: this mirrors
// `bd dep add <blocked> <blocker>`.
func (c *Client) DepAdd(ctx context.Context, workdir, from, to, depType string) error {
	if err := checkIssueID("dep add", from); err != nil {
		return err
	}
	if err := checkIssueID("dep add", to); err != nil {
		return err
	}
	if from == to {
		return errors.New("dep add: an issue cannot depend on itself")
	}
	if depType != "" && !ValidToken(depType) {
		return fmt.Errorf("dep add: %w: type %q", ErrInvalidID, depType)
	}
	args := []string{"dep", "add", from, to}
	if depType != "" {
		args = append(args, "--type", depType)
	}
	return c.exec(ctx, workdir, args)
}

// DepRemove removes the dependency edge from `from` to `to`.
func (c *Client) DepRemove(ctx context.Context, workdir, from, to string) error {
	if err := checkIssueID("dep remove", from); err != nil {
		return err
	}
	if err := checkIssueID("dep remove", to); err != nil {
		return err
	}
	return c.exec(ctx, workdir, []string{"dep", "remove", from, to})
}

// SetState records an operational state change: bd writes an event bead and
// swaps the <dimension>:<value> label in one atomic step.
func (c *Client) SetState(ctx context.Context, workdir, id, dim, val, reason string) error {
	if err := checkIssueID("set-state", id); err != nil {
		return err
	}
	if !ValidToken(dim) || !ValidToken(val) {
		return fmt.Errorf("set-state: %w: dimension %q value %q", ErrInvalidID, dim, val)
	}
	args := []string{"set-state", id, dim + "=" + val}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	return c.exec(ctx, workdir, args)
}

// Delete permanently removes issues.
//
// This is destructive and irreversible, and is deliberately NOT exposed over
// HTTP. It exists so integration tests can clean up the scratch beads they
// create instead of leaving them in a real backlog.
func (c *Client) Delete(ctx context.Context, workdir string, ids []string) error {
	if err := checkIssueIDs("delete", ids); err != nil {
		return err
	}
	args := append([]string{"delete"}, ids...)
	// Without --force bd only prints a preview and exits 0, which would make a
	// failed cleanup look like a success.
	args = append(args, "--force")
	return c.exec(ctx, workdir, args)
}

func (c *Client) exec(ctx context.Context, workdir string, args []string) error {
	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)
	if err != nil {
		return newBdError(args, stdout, stderr, err)
	}
	return nil
}

// DoltPush pushes committed dolt changes to the project's remote. It returns
// bd's combined output so the caller can show the user what happened.
func (c *Client) DoltPush(ctx context.Context, workdir string) (string, error) {
	return c.doltSync(ctx, workdir, "push")
}

// DoltPull pulls from the project's remote.
func (c *Client) DoltPull(ctx context.Context, workdir string) (string, error) {
	return c.doltSync(ctx, workdir, "pull")
}

func (c *Client) doltSync(ctx context.Context, workdir, verb string) (string, error) {
	args := []string{"dolt", verb}
	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)
	output := strings.TrimSpace(stdout)
	if s := strings.TrimSpace(stderr); s != "" {
		if output != "" {
			output += "\n"
		}
		output += s
	}
	if err != nil {
		return output, newBdError(args, stdout, stderr, err)
	}
	return output, nil
}

// conflictMarkers are the phrases dolt and bd use when a sync cannot proceed
// without a human. Loom surfaces these loudly and never auto-resolves.
var conflictMarkers = []string{
	"conflict",
	"merge conflict",
	"would be overwritten",
	"non-fast-forward",
	"fast forward",
	"diverged",
	"needs merge",
	"cannot be merged",
}

// IsConflict reports whether a failed dolt sync looks like a conflict the user
// must resolve, as opposed to a transient or configuration error.
func IsConflict(err error) bool {
	be, ok := AsBdError(err)
	if !ok {
		return false
	}
	text := strings.ToLower(be.Message())
	for _, m := range conflictMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}
