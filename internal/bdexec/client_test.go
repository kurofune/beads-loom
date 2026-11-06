package bdexec

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

const wd = "/Users/me/Projects/loom"

// TestVerbArgv pins the exact argv each verb produces. These assertions are the
// contract with bd: a silently reordered or renamed flag is a data-corrupting
// bug that no read test would catch.
func TestVerbArgv(t *testing.T) {
	tests := []struct {
		name string
		call func(c *Client) error
		// want is the argv after the binary name, excluding the -C the runner
		// prepends.
		want []string
		// wantStdin is the text expected on the pipe.
		wantStdin string
	}{
		{
			name: "update status only",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Status: ptr("in_progress")})
			},
			want: []string{"update", "loom-1", "--status", "in_progress"},
		},
		{
			name: "update every field in a fixed order",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"}, UpdateFields{
					Status:       ptr("open"),
					Priority:     ptr(1),
					Assignee:     ptr("nate"),
					Title:        ptr("New title"),
					Type:         ptr("feature"),
					Estimate:     ptr(30),
					Description:  ptr("why"),
					Acceptance:   ptr("done when"),
					Design:       ptr("where"),
					Notes:        ptr("context"),
					AddLabels:    []string{"phase-2", "layer-ui"},
					RemoveLabels: []string{"stale"},
					Defer:        ptr("2026-09-01"),
					Due:          ptr("+2d"),
				})
			},
			want: []string{
				"update", "loom-1",
				"--status", "open",
				"--priority", "1",
				"--assignee", "nate",
				"--title", "New title",
				"--type", "feature",
				"--estimate", "30",
				"--description", "why",
				"--acceptance", "done when",
				"--design", "where",
				"--notes", "context",
				"--add-label", "phase-2",
				"--add-label", "layer-ui",
				"--remove-label", "stale",
				"--defer", "2026-09-01",
				"--due", "+2d",
			},
		},
		{
			name: "bulk update is a single invocation with every id",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd,
					[]string{"loom-a", "loom-b", "loom-c"},
					UpdateFields{Priority: ptr(0)})
			},
			want: []string{"update", "loom-a", "loom-b", "loom-c", "--priority", "0"},
		},
		{
			name: "clearing a date sends an empty value",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Due: ptr("")})
			},
			want: []string{"update", "loom-1", "--due", ""},
		},
		{
			name: "unassigning sends an empty assignee",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Assignee: ptr("")})
			},
			want: []string{"update", "loom-1", "--assignee", ""},
		},
		{
			name: "priority zero is not mistaken for unset",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Priority: ptr(0)})
			},
			want: []string{"update", "loom-1", "--priority", "0"},
		},
		{
			name: "update description",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Description: ptr("why this bead exists")})
			},
			want: []string{"update", "loom-1", "--description", "why this bead exists"},
		},
		{
			name: "update acceptance",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Acceptance: ptr("criterion one\ncriterion two")})
			},
			want: []string{"update", "loom-1", "--acceptance", "criterion one\ncriterion two"},
		},
		{
			name: "update design",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Design: ptr("look in internal/bdexec")})
			},
			want: []string{"update", "loom-1", "--design", "look in internal/bdexec"},
		},
		{
			name: "clearing a prose field sends an empty value",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Description: ptr(""), Notes: ptr("")})
			},
			want: []string{"update", "loom-1", "--description", "", "--notes", ""},
		},
		{
			name: "update notes",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Notes: ptr("how this fits the plan")})
			},
			want: []string{"update", "loom-1", "--notes", "how this fits the plan"},
		},
		{
			name: "close with reason",
			call: func(c *Client) error {
				return c.Close(context.Background(), wd, []string{"loom-1"}, CloseOptions{Reason: "shipped"})
			},
			want: []string{"close", "loom-1", "--reason", "shipped"},
		},
		{
			name: "close many with one reason",
			call: func(c *Client) error {
				return c.Close(context.Background(), wd, []string{"loom-1", "loom-2"}, CloseOptions{Reason: "batch"})
			},
			want: []string{"close", "loom-1", "loom-2", "--reason", "batch"},
		},
		{
			name: "close with force overrides another actor's claim",
			call: func(c *Client) error {
				return c.Close(context.Background(), wd, []string{"loom-1"}, CloseOptions{Reason: "shipped", Force: true})
			},
			want: []string{"close", "loom-1", "--reason", "shipped", "--force"},
		},
		{
			name: "reopen",
			call: func(c *Client) error {
				return c.Reopen(context.Background(), wd, []string{"loom-1"}, "")
			},
			want: []string{"reopen", "loom-1"},
		},
		{
			name: "reopen with reason",
			call: func(c *Client) error {
				return c.Reopen(context.Background(), wd, []string{"loom-1"}, "regressed")
			},
			want: []string{"reopen", "loom-1", "--reason", "regressed"},
		},
		{
			name: "comment pipes text through stdin",
			call: func(c *Client) error {
				return c.Comment(context.Background(), wd, "loom-1", "line one\nline two")
			},
			want:      []string{"comment", "loom-1", "--stdin"},
			wantStdin: "line one\nline two",
		},
		{
			name: "comment text that looks like flags stays on the pipe",
			call: func(c *Client) error {
				return c.Comment(context.Background(), wd, "loom-1", "--force -rm -rf")
			},
			want:      []string{"comment", "loom-1", "--stdin"},
			wantStdin: "--force -rm -rf",
		},
		{
			name: "tag",
			call: func(c *Client) error {
				return c.Tag(context.Background(), wd, "loom-1", "needs-operator")
			},
			want: []string{"tag", "loom-1", "needs-operator"},
		},
		{
			name: "dep add defaults to blocks",
			call: func(c *Client) error {
				return c.DepAdd(context.Background(), wd, "loom-2", "loom-1", "")
			},
			want: []string{"dep", "add", "loom-2", "loom-1"},
		},
		{
			name: "dep add with an explicit type",
			call: func(c *Client) error {
				return c.DepAdd(context.Background(), wd, "loom-2", "loom-1", "discovered-from")
			},
			want: []string{"dep", "add", "loom-2", "loom-1", "--type", "discovered-from"},
		},
		{
			name: "dep remove",
			call: func(c *Client) error {
				return c.DepRemove(context.Background(), wd, "loom-2", "loom-1")
			},
			want: []string{"dep", "remove", "loom-2", "loom-1"},
		},
		{
			name: "set-state",
			call: func(c *Client) error {
				return c.SetState(context.Background(), wd, "loom-1", "evidence_status", "fail", "AC 2 unmet")
			},
			want: []string{"set-state", "loom-1", "evidence_status=fail", "--reason", "AC 2 unmet"},
		},
		{
			name: "set-state without a reason",
			call: func(c *Client) error {
				return c.SetState(context.Background(), wd, "loom-1", "mode", "degraded", "")
			},
			want: []string{"set-state", "loom-1", "mode=degraded"},
		},
		{
			name: "delete always forces",
			call: func(c *Client) error {
				return c.Delete(context.Background(), wd, []string{"loom-scratch"})
			},
			want: []string{"delete", "loom-scratch", "--force"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := NewMockRunner()
			if err := tc.call(New(mock)); err != nil {
				t.Fatalf("call: %v", err)
			}
			calls := mock.Calls()
			if len(calls) != 1 {
				t.Fatalf("got %d invocations, want 1: %v", len(calls), calls)
			}
			if !reflect.DeepEqual(calls[0].Args, tc.want) {
				t.Errorf("argv mismatch\n got: %v\nwant: %v", calls[0].Args, tc.want)
			}
			if calls[0].Workdir != wd {
				t.Errorf("workdir = %q, want %q", calls[0].Workdir, wd)
			}
			if calls[0].Stdin != tc.wantStdin {
				t.Errorf("stdin = %q, want %q", calls[0].Stdin, tc.wantStdin)
			}
		})
	}
}

func TestCreateArgv(t *testing.T) {
	mock := NewMockRunner()
	mock.Stdout = "loom-abc\n"
	c := New(mock)

	id, err := c.Create(context.Background(), wd, CreateSpec{
		Title:       "[Scratch] test bead",
		Type:        "task",
		Priority:    ptr(3),
		Description: "why this exists",
		Acceptance:  "criterion one\ncriterion two",
		Design:      "internal/bdexec",
		Notes:       "context",
		Parent:      "loom-3ft",
		Estimate:    ptr(15),
		Labels:      []string{"phase-2", "layer-service"},
		Deps:        []Dep{{ID: "loom-1"}, {ID: "loom-2", Type: "discovered-from"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "loom-abc" {
		t.Errorf("id = %q, want loom-abc", id)
	}

	calls := mock.Calls()
	if len(calls) != 5 {
		t.Fatalf("got %d invocations, want 5 (create + 2 tags + 2 deps): %v", len(calls), calls)
	}

	wantCreate := []string{
		"create",
		"--title", "[Scratch] test bead",
		"--type", "task",
		"--priority", "3",
		"--description", "why this exists",
		"--acceptance", "criterion one\ncriterion two",
		"--design", "internal/bdexec",
		"--notes", "context",
		"--parent", "loom-3ft",
		"--estimate", "15",
		"--silent",
	}
	if !reflect.DeepEqual(calls[0].Args, wantCreate) {
		t.Errorf("create argv mismatch\n got: %v\nwant: %v", calls[0].Args, wantCreate)
	}

	wantRest := [][]string{
		{"tag", "loom-abc", "phase-2"},
		{"tag", "loom-abc", "layer-service"},
		{"dep", "add", "loom-abc", "loom-1"},
		{"dep", "add", "loom-abc", "loom-2", "--type", "discovered-from"},
	}
	for i, want := range wantRest {
		if !reflect.DeepEqual(calls[i+1].Args, want) {
			t.Errorf("call %d argv mismatch\n got: %v\nwant: %v", i+1, calls[i+1].Args, want)
		}
	}
}

func TestCreateMinimal(t *testing.T) {
	mock := NewMockRunner()
	mock.Stdout = "loom-xyz"
	id, err := New(mock).Create(context.Background(), wd, CreateSpec{Title: "just a title"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "loom-xyz" {
		t.Errorf("id = %q", id)
	}
	want := []string{"create", "--title", "just a title", "--silent"}
	if calls := mock.Calls(); !reflect.DeepEqual(calls[0].Args, want) {
		t.Errorf("argv mismatch\n got: %v\nwant: %v", calls[0].Args, want)
	}
}

// TestCreateReturnsIDWhenAFollowUpFails: a create that succeeds but whose
// tagging fails must still surface the ID, or the user is left with a bead they
// cannot find.
func TestCreateReturnsIDWhenAFollowUpFails(t *testing.T) {
	mock := NewMockRunner()
	mock.Handler = func(call Call) (string, string, error) {
		if call.Args[0] == "create" {
			return "loom-abc\n", "", nil
		}
		return "", "unknown label", &exec.ExitError{}
	}
	id, err := New(mock).Create(context.Background(), wd, CreateSpec{
		Title:  "t",
		Labels: []string{"rejected-by-bd"},
	})
	if err == nil {
		t.Fatal("expected the tag failure to surface")
	}
	if id != "loom-abc" {
		t.Errorf("id = %q, want the created id to come back alongside the error", id)
	}
}

func TestParseCreatedID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"silent output", "loom-abc\n", "loom-abc"},
		{"silent output no newline", "loom-abc", "loom-abc"},
		{"dotted child id", "loom-3ft.2\n", "loom-3ft.2"},
		{"deeply dotted", "loom-3ft.2.1", "loom-3ft.2.1"},
		{"human confirmation line", "Created issue loom-9kd — [Scratch] thing", "loom-9kd"},
		{"prefixed project name", "phoenix-qgss", "phoenix-qgss"},
		{"empty", "", ""},
		{"no id present", "something went sideways", ""},
		{"multiline picks the first id", "Created issue loom-1\nnext: loom-2", "loom-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCreatedID(tc.in); got != tc.want {
				t.Errorf("ParseCreatedID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestGuardsAgainstUnscopedInvocations: bd falls back to "the last touched
// issue" when given no ID, so an empty request must fail here rather than
// mutate something the user never named.
func TestGuardsAgainstUnscopedInvocations(t *testing.T) {
	mock := NewMockRunner()
	c := New(mock)
	ctx := context.Background()

	tests := []struct {
		name string
		err  error
	}{
		{"update with no ids", c.Update(ctx, wd, nil, UpdateFields{Status: ptr("open")})},
		{"update with no fields", c.Update(ctx, wd, []string{"loom-1"}, UpdateFields{})},
		{"close with no ids", c.Close(ctx, wd, nil, CloseOptions{Reason: "r"})},
		{"reopen with no ids", c.Reopen(ctx, wd, nil, "")},
		{"comment with no id", c.Comment(ctx, wd, "", "text")},
		{"comment with blank text", c.Comment(ctx, wd, "loom-1", "   ")},
		{"tag with no label", c.Tag(ctx, wd, "loom-1", "")},
		{"dep add missing target", c.DepAdd(ctx, wd, "loom-1", "", "")},
		{"dep add self-dependency", c.DepAdd(ctx, wd, "loom-1", "loom-1", "")},
		{"dep remove missing target", c.DepRemove(ctx, wd, "loom-1", "")},
		{"set-state missing value", c.SetState(ctx, wd, "loom-1", "dim", "", "")},
		{"delete with no ids", c.Delete(ctx, wd, nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Error("expected an error")
			}
		})
	}

	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("rejected calls still reached bd: %v", calls)
	}

	if _, err := New(mock).Create(ctx, wd, CreateSpec{Title: "  "}); err == nil {
		t.Error("create with a blank title should fail")
	}
}

func TestUpdateNoFieldsIsTyped(t *testing.T) {
	err := New(NewMockRunner()).Update(context.Background(), wd, []string{"loom-1"}, UpdateFields{})
	if !errors.Is(err, ErrNoFields) {
		t.Errorf("error = %v, want ErrNoFields", err)
	}
}

func TestUpdateFieldsIsEmpty(t *testing.T) {
	tests := []struct {
		name string
		f    UpdateFields
		want bool
	}{
		{"zero", UpdateFields{}, true},
		{"status", UpdateFields{Status: ptr("open")}, false},
		{"priority zero", UpdateFields{Priority: ptr(0)}, false},
		{"empty assignee is still a change", UpdateFields{Assignee: ptr("")}, false},
		{"description", UpdateFields{Description: ptr("d")}, false},
		{"acceptance", UpdateFields{Acceptance: ptr("a")}, false},
		{"design", UpdateFields{Design: ptr("d")}, false},
		{"notes", UpdateFields{Notes: ptr("n")}, false},
		{"empty description is still a change", UpdateFields{Description: ptr("")}, false},
		{"only add labels", UpdateFields{AddLabels: []string{"x"}}, false},
		{"only remove labels", UpdateFields{RemoveLabels: []string{"x"}}, false},
		{"empty label slices", UpdateFields{AddLabels: []string{}, RemoveLabels: []string{}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.IsEmpty(); got != tc.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBdErrorCarriesStderr(t *testing.T) {
	mock := NewMockRunner()
	mock.Stderr = "Error: issue loom-999 not found\n"
	mock.Err = &exec.ExitError{}

	err := New(mock).Close(context.Background(), wd, []string{"loom-999"}, CloseOptions{Reason: "done"})
	if err == nil {
		t.Fatal("expected an error")
	}

	be, ok := AsBdError(err)
	if !ok {
		t.Fatalf("error is not a *BdError: %T", err)
	}
	if be.Verb != "close" {
		t.Errorf("Verb = %q, want close", be.Verb)
	}
	if be.Message() != "Error: issue loom-999 not found" {
		t.Errorf("Message() = %q", be.Message())
	}
	if !strings.Contains(err.Error(), "bd close failed") {
		t.Errorf("Error() = %q", err.Error())
	}
	if !strings.Contains(strings.Join(be.Args, " "), "loom-999") {
		t.Errorf("Args = %v, want them to record the invocation", be.Args)
	}
}

func TestBdErrorFallsBackToStdout(t *testing.T) {
	mock := NewMockRunner()
	mock.Stdout = "something explained on stdout"
	mock.Err = &exec.ExitError{}

	err := New(mock).Tag(context.Background(), wd, "loom-1", "x")
	be, ok := AsBdError(err)
	if !ok {
		t.Fatalf("not a BdError: %v", err)
	}
	if be.Message() != "something explained on stdout" {
		t.Errorf("Message() = %q", be.Message())
	}
}

func TestCallArgv(t *testing.T) {
	c := Call{Workdir: "/tmp/p", Args: []string{"update", "loom-1", "--status", "open"}}
	want := "bd -C /tmp/p update loom-1 --status open"
	if got := c.Argv(); got != want {
		t.Errorf("Argv() = %q, want %q", got, want)
	}
}

// bd13ClaimedStderr is bd 1.3.2's refusal to close an issue another actor
// holds, verbatim from a live run.
const bd13ClaimedStderr = `cannot close loom-f6m: assignee is "loom-integration-test", actor is "Nate Gross"; reclaim or use --force to override` + "\n"

func TestCloseWithoutForceNeverSendsForce(t *testing.T) {
	mock := NewMockRunner()
	if err := New(mock).Close(context.Background(), wd, []string{"loom-1"}, CloseOptions{Reason: "done"}); err != nil {
		t.Fatal(err)
	}
	for _, call := range mock.Calls() {
		for _, a := range call.Args {
			if a == "--force" {
				t.Fatalf("argv %v carries --force although Force was not set", call.Args)
			}
		}
	}
}

func TestClaimedByAnotherRecognisesBD13Refusal(t *testing.T) {
	mock := NewMockRunner()
	mock.Stderr = bd13ClaimedStderr
	mock.Err = &exec.ExitError{}

	err := New(mock).Close(context.Background(), wd, []string{"loom-f6m"}, CloseOptions{Reason: "done"})
	assignee, ok := ClaimedByAnother(err)
	if !ok {
		t.Fatalf("ClaimedByAnother(%v) = false, want true", err)
	}
	if assignee != "loom-integration-test" {
		t.Errorf("assignee = %q, want loom-integration-test", assignee)
	}
}

func TestClaimedByAnotherRejectsOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"not a bd error", errors.New(`reclaim or use --force`)},
		{"another bd failure", newBdError([]string{"close", "loom-1"}, "", "Error: issue loom-1 not found\n", &exec.ExitError{})},
		{"the refusal from a verb other than close", newBdError([]string{"update", "loom-1"}, "", bd13ClaimedStderr, &exec.ExitError{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ClaimedByAnother(tc.err); ok {
				t.Errorf("ClaimedByAnother(%v) = true, want false", tc.err)
			}
		})
	}
}
