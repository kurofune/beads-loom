package sse

import (
	"sync"
	"time"
)

// Clock is the time source the poller loop uses, so tests can drive it without
// sleeping.
type Clock interface {
	Now() time.Time
	// After returns a channel that receives once d has elapsed.
	After(d time.Duration) <-chan time.Time
}

// realClock is the production implementation.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// SystemClock is the default Clock.
var SystemClock Clock = realClock{}

// FakeClock is a manually advanced Clock for tests.
//
// Every After call registers a waiter; Advance fires the ones whose deadline
// has passed. WaitForSleepers lets a test synchronize with a loop that is about
// to sleep, which is what makes poller tests deterministic instead of timing
// dependent.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*fakeWaiter
	sleepCh chan struct{}
}

type fakeWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

// NewFakeClock returns a clock started at the given time.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, sleepCh: make(chan struct{}, 1024)}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	w := &fakeWaiter{deadline: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()

	// Signal that someone is now waiting, so tests can advance at the right
	// moment rather than racing the loop.
	select {
	case c.sleepCh <- struct{}{}:
	default:
	}
	return w.ch
}

// Advance moves time forward and fires every waiter whose deadline has passed.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now

	remaining := c.waiters[:0]
	var fire []*fakeWaiter
	for _, w := range c.waiters {
		if !w.deadline.After(now) {
			fire = append(fire, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()

	for _, w := range fire {
		w.ch <- now
	}
}

// WaitForSleeper blocks until a goroutine calls After, or the timeout elapses.
// It reports whether a sleeper appeared.
func (c *FakeClock) WaitForSleeper(timeout time.Duration) bool {
	select {
	case <-c.sleepCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Sleepers returns how many waiters are currently pending.
func (c *FakeClock) Sleepers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}
