package bdexec

import (
	"context"
	"strings"
	"sync"
)

// Call is one recorded invocation.
type Call struct {
	Workdir string
	Args    []string
	Stdin   string
}

// Argv renders the call the way it would appear on a command line, including
// the -C the runner prepends. Useful for readable test assertions.
func (c Call) Argv() string {
	return strings.Join(append([]string{"bd", "-C", c.Workdir}, c.Args...), " ")
}

// MockRunner records invocations and replays canned results. It is safe for
// concurrent use.
//
// This lives in the package rather than a _test.go file so httpapi tests can
// drive the write endpoints without a real bd on PATH.
type MockRunner struct {
	mu    sync.Mutex
	calls []Call

	// Stdout is returned by every call unless a Handler is set.
	Stdout string
	// Stderr is returned by every call unless a Handler is set.
	Stderr string
	// Err is returned by every call unless a Handler is set.
	Err error

	// Handler, when set, produces the result for each call and takes
	// precedence over Stdout/Stderr/Err.
	Handler func(call Call) (stdout, stderr string, err error)
}

// NewMockRunner returns a runner that succeeds silently.
func NewMockRunner() *MockRunner { return &MockRunner{} }

func (m *MockRunner) Run(ctx context.Context, workdir string, args ...string) (string, string, error) {
	return m.record(Call{Workdir: workdir, Args: args})
}

func (m *MockRunner) RunStdin(ctx context.Context, workdir, stdin string, args ...string) (string, string, error) {
	return m.record(Call{Workdir: workdir, Args: args, Stdin: stdin})
}

func (m *MockRunner) record(call Call) (string, string, error) {
	m.mu.Lock()
	m.calls = append(m.calls, call)
	handler := m.Handler
	stdout, stderr, err := m.Stdout, m.Stderr, m.Err
	m.mu.Unlock()

	if handler != nil {
		return handler(call)
	}
	return stdout, stderr, err
}

// Calls returns every recorded invocation, in order.
func (m *MockRunner) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Call(nil), m.calls...)
}

// Last returns the most recent call, and whether there was one.
func (m *MockRunner) Last() (Call, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return Call{}, false
	}
	return m.calls[len(m.calls)-1], true
}

// Reset clears the recorded calls.
func (m *MockRunner) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}
