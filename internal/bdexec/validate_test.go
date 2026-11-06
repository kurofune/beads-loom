package bdexec

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestValidIssueID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		// Real shapes from the seeded db and ~/Projects/djinn.
		{"loom-3ft", true},
		{"loom-3ft.2", true},
		{"loom-3ft.2.1", true},
		{"djinn-ipee.8", true},
		{"djinn-0e5h.1", true},
		{"phoenix-qgss", true},
		{"a_b-1", true},
		// bd 1.3.2 ids a wisp (every `message` create) as <prefix>-wisp-<hash>.
		{"loom-wisp-4v8", true},

		// The B1 attack shapes.
		{"--claim", false},
		{"--ephemeral", false},
		{"-f", false},
		{"-", false},
		{"--", false},

		// Other malformed shapes.
		{"", false},
		{"loom", false},
		{"loom-", false},
		{"-loom-3ft", false},
		{"loom-3ft.x", false},
		{"loom-3FT", false},
		{"loom/3ft", false},
		{"../etc/passwd", false},
		{"loom-3ft ", false},
		{"loom 3ft", false},
		{"loom-3ft;rm", false},
		{"loom-wisp-", false},
		{"loom-other-4v8", false},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			if got := ValidIssueID(tc.id); got != tc.want {
				t.Errorf("ValidIssueID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestValidLabel(t *testing.T) {
	tests := []struct {
		label string
		want  bool
	}{
		// Real labels from the seeded db.
		{"phase-1", true},
		{"layer-ui", true},
		{"needs-operator", true},
		{"review-follow-up", true},
		{"evidence_status:fail", true},

		{"--ephemeral", false},
		{"-x", false},
		{"", false},
		{"has space", false},
		{"semi;colon", false},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			if got := ValidLabel(tc.label); got != tc.want {
				t.Errorf("ValidLabel(%q) = %v, want %v", tc.label, got, tc.want)
			}
		})
	}
}

// TestB1BdexecRejectsFlagShapedArgs is the defence-in-depth half of B1: even a
// caller that skips the HTTP validation must not be able to build a flag-shaped
// argv.
func TestB1BdexecRejectsFlagShapedArgs(t *testing.T) {
	ctx := context.Background()
	mock := NewMockRunner()
	c := New(mock)

	cases := []struct {
		name string
		err  error
	}{
		{"update with a flag id", c.Update(ctx, wd, []string{"--claim"}, UpdateFields{Priority: ptr(1)})},
		{"update with a flag id among valid ones",
			c.Update(ctx, wd, []string{"--ephemeral", "loom-abc"}, UpdateFields{Status: ptr("open")})},
		{"update with a flag label",
			c.Update(ctx, wd, []string{"loom-1"}, UpdateFields{AddLabels: []string{"--ephemeral"}})},
		{"close with a flag id", c.Close(ctx, wd, []string{"-f"}, CloseOptions{Reason: "r"})},
		{"reopen with a flag id", c.Reopen(ctx, wd, []string{"--force"}, "")},
		{"comment with a flag id", c.Comment(ctx, wd, "--stdin", "text")},
		{"tag with a flag id", c.Tag(ctx, wd, "--claim", "phase-1")},
		{"tag with a flag label", c.Tag(ctx, wd, "loom-1", "--ephemeral")},
		{"dep add with a flag from", c.DepAdd(ctx, wd, "-t", "loom-1", "")},
		{"dep add with a flag to", c.DepAdd(ctx, wd, "loom-1", "--file", "")},
		{"dep add with a flag type", c.DepAdd(ctx, wd, "loom-1", "loom-2", "--no-cycle-check")},
		{"dep remove with a flag id", c.DepRemove(ctx, wd, "--rm", "loom-1")},
		{"set-state with a flag id", c.SetState(ctx, wd, "-x", "dim", "val", "")},
		{"set-state with a flag dimension", c.SetState(ctx, wd, "loom-1", "--reason", "val", "")},
		{"delete with a flag id", c.Delete(ctx, wd, []string{"--cascade"})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("expected rejection")
			}
			if !errors.Is(tc.err, ErrInvalidID) {
				t.Errorf("error = %v, want ErrInvalidID", tc.err)
			}
		})
	}

	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("bd was invoked despite rejection: %v", calls)
	}

	// Create's positional-adjacent fields too.
	for _, spec := range []CreateSpec{
		{Title: "t", Parent: "--ephemeral"},
		{Title: "t", Labels: []string{"--no-history"}},
		{Title: "t", Deps: []Dep{{ID: "--force"}}},
		{Title: "t", Type: "--x"},
	} {
		if _, err := New(mock).Create(ctx, wd, spec); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Create(%+v) error = %v, want ErrInvalidID", spec, err)
		}
	}
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("create invoked bd despite rejection: %v", calls)
	}
}

// TestI2ProseFlagValuesAreAccepted is the regression test for review finding I2.
//
// The B1 fix over-rejected: it treated flag VALUES the way it treats positional
// arguments. bd is cobra/pflag, which consumes the argument after a flag
// unconditionally — verified against the real binary with --dry-run, where
// `--title "-wontfix title"` yields exactly that title and
// `--description "--ephemeral"` yields that literal string rather than setting
// the flag. Rejecting these turned a markdown bullet list, the most natural
// shape for a description, into a 400.
func TestI2ProseFlagValuesAreAccepted(t *testing.T) {
	const bullets = "- add the endpoint\n- wire the UI"

	tests := []struct {
		name string
		call func(c *Client) error
		want []string
	}{
		{
			name: "description starting with a markdown bullet",
			call: func(c *Client) error {
				_, err := c.Create(context.Background(), wd, CreateSpec{
					Title: "t", Description: bullets,
				})
				return err
			},
			want: []string{"create", "--title", "t", "--description", bullets, "--silent"},
		},
		{
			name: "title starting with a dash",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Title: ptr("-wontfix title")})
			},
			want: []string{"update", "loom-1", "--title", "-wontfix title"},
		},
		{
			name: "close reason starting with a dash",
			call: func(c *Client) error {
				return c.Close(context.Background(), wd, []string{"loom-1"}, CloseOptions{Reason: "-wontfix"})
			},
			want: []string{"close", "loom-1", "--reason", "-wontfix"},
		},
		{
			name: "reopen reason starting with a dash",
			call: func(c *Client) error {
				return c.Reopen(context.Background(), wd, []string{"loom-1"}, "-regressed")
			},
			want: []string{"reopen", "loom-1", "--reason", "-regressed"},
		},
		{
			name: "assignee starting with a dash",
			call: func(c *Client) error {
				return c.Update(context.Background(), wd, []string{"loom-1"},
					UpdateFields{Assignee: ptr("-nate")})
			},
			want: []string{"update", "loom-1", "--assignee", "-nate"},
		},
		{
			// The worst-looking case: a value identical to a real bd flag.
			// pflag still takes it as the value.
			name: "description that is literally a flag name",
			call: func(c *Client) error {
				_, err := c.Create(context.Background(), wd, CreateSpec{
					Title: "t", Description: "--ephemeral",
				})
				return err
			},
			want: []string{"create", "--title", "t", "--description", "--ephemeral", "--silent"},
		},
		{
			name: "acceptance, design and notes all bulleted",
			call: func(c *Client) error {
				_, err := c.Create(context.Background(), wd, CreateSpec{
					Title: "t", Acceptance: bullets, Design: "-see internal/x", Notes: "-context",
				})
				return err
			},
			want: []string{"create", "--title", "t", "--acceptance", bullets,
				"--design", "-see internal/x", "--notes", "-context", "--silent"},
		},
		{
			name: "set-state reason starting with a dash",
			call: func(c *Client) error {
				return c.SetState(context.Background(), wd, "loom-1", "mode", "degraded", "-why")
			},
			want: []string{"set-state", "loom-1", "mode=degraded", "--reason", "-why"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := NewMockRunner()
			mock.Stdout = "loom-abc\n"
			if err := tc.call(New(mock)); err != nil {
				t.Fatalf("legitimate prose was rejected: %v", err)
			}
			calls := mock.Calls()
			if len(calls) == 0 {
				t.Fatal("bd was never invoked")
			}
			if !reflect.DeepEqual(calls[0].Args, tc.want) {
				t.Errorf("argv mismatch\n got: %v\nwant: %v", calls[0].Args, tc.want)
			}
		})
	}
}

// TestI2PositionalStrictnessSurvives: relaxing flag values must not weaken the
// positional guards, which are the part that actually closes B1.
func TestI2PositionalStrictnessSurvives(t *testing.T) {
	mock := NewMockRunner()
	c := New(mock)
	ctx := context.Background()

	// Ids and labels are positional or positional-adjacent: still strict.
	if err := c.Update(ctx, wd, []string{"--claim"}, UpdateFields{Priority: ptr(1)}); err == nil {
		t.Error("a flag-shaped id must still be rejected")
	}
	if err := c.Update(ctx, wd, []string{"loom-1"}, UpdateFields{AddLabels: []string{"--ephemeral"}}); err == nil {
		t.Error("a flag-shaped label must still be rejected")
	}
	if err := c.Tag(ctx, wd, "loom-1", "-x"); err == nil {
		t.Error("a flag-shaped label must still be rejected on tag")
	}
	if err := c.DepAdd(ctx, wd, "-t", "loom-1", ""); err == nil {
		t.Error("a flag-shaped dep id must still be rejected")
	}

	// And the one genuine positional free-text case: the query expression.
	if _, err := c.Query(ctx, wd, "--json"); err == nil {
		t.Error("a flag-shaped query expression must still be rejected")
	}

	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("bd was invoked despite rejection: %v", calls)
	}
}
