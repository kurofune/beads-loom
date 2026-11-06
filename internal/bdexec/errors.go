package bdexec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// BdError is a bd invocation that exited non-zero. Stderr is bd's own message
// and is meant to be shown to the user verbatim — the API surfaces it as a 422
// so the UI can put it straight in a toast.
type BdError struct {
	// Verb is the bd subcommand, e.g. "update".
	Verb string
	// Args is the full argv passed after the binary name, minus -C <workdir>.
	Args []string
	// Stderr is bd's error output, trimmed.
	Stderr string
	// Stdout is kept because bd sometimes explains itself on stdout.
	Stdout string
	// ExitCode is the process exit code, or -1 if bd never ran.
	ExitCode int

	err error
}

func (e *BdError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Stdout)
	}
	if msg == "" && e.err != nil {
		msg = e.err.Error()
	}
	if msg == "" {
		msg = "no output"
	}
	return fmt.Sprintf("bd %s failed: %s", e.Verb, msg)
}

func (e *BdError) Unwrap() error { return e.err }

// Message returns the text worth showing a user: bd's stderr when it said
// something, otherwise the underlying process error.
func (e *BdError) Message() string {
	if s := strings.TrimSpace(e.Stderr); s != "" {
		return s
	}
	if s := strings.TrimSpace(e.Stdout); s != "" {
		return s
	}
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

// AsBdError extracts a *BdError from an error chain.
func AsBdError(err error) (*BdError, bool) {
	var be *BdError
	if errors.As(err, &be) {
		return be, true
	}
	return nil, false
}

// newBdError wraps a failed invocation.
func newBdError(args []string, stdout, stderr string, err error) *BdError {
	verb := "command"
	if len(args) > 0 {
		verb = args[0]
	}
	return &BdError{
		Verb:     verb,
		Args:     args,
		Stderr:   stderr,
		Stdout:   stdout,
		ExitCode: exitCode(err),
		err:      err,
	}
}

// ErrNoFields is returned when an update would produce a bd invocation with no
// flags — a no-op that would silently retarget bd's "last touched issue".
var ErrNoFields = errors.New("no fields to update")

// claimedMarker is the phrase bd 1.3+ prints when `bd close` refuses an issue
// whose assignee is not the acting user.
const claimedMarker = "reclaim or use --force"

// claimedAssignee pulls the assignee out of that refusal:
// `cannot close X: assignee is "alice", actor is "bob"; reclaim or use --force to override`.
var claimedAssignee = regexp.MustCompile(`assignee is "([^"]*)"`)

// ClaimedByAnother reports whether err is bd refusing to close an issue that
// another actor holds, and returns the assignee bd named (empty when bd's text
// did not carry one). Only a close refusal counts: the caller turns it into a
// confirm step, and no other verb has one.
func ClaimedByAnother(err error) (assignee string, ok bool) {
	be, isBd := AsBdError(err)
	if !isBd || be.Verb != "close" {
		return "", false
	}
	msg := be.Message()
	if !strings.Contains(msg, claimedMarker) {
		return "", false
	}
	if m := claimedAssignee.FindStringSubmatch(msg); m != nil {
		return m[1], true
	}
	return "", true
}
