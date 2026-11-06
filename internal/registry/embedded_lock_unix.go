//go:build unix

package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"syscall"
)

// shareResult is what a blocked waiter hands one scope: the file holding the
// shared lock, a flock error, or neither when the scope must try again.
type shareResult struct {
	f   *os.File
	err error
}

// shareWaiter is one blocking LOCK_SH flock on a lock file. The kernel wakes
// it the moment the exclusive holder releases, ahead of bd's next poll; a
// reader that polls instead misses handoffs between bd writers, which come
// only milliseconds apart. A blocking flock cannot be cancelled, so a waiter
// outlives the scopes that give up on it, and the next scope on that file
// reuses it: there is never more than one per file.
type shareWaiter struct {
	claims []chan shareResult // guarded by waitersMu
}

var (
	waitersMu sync.Mutex
	waiters   = map[string]*shareWaiter{}
)

// shareLock takes a shared flock on path and returns its release. It waits
// until ctx is done; openEmbedded's budget bounds that. It never creates the
// file. It fails, and the store must not be opened, when the file is missing
// or the lock is not held by then: without the shared lock dolt's exclusive
// attempt can succeed and open the store writable.
func shareLock(ctx context.Context, path string) (release func(), err error) {
	for {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("cannot take the shared store lock, so the store stays closed: %w", err)
		}
		locked, err := tryShare(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("shared lock on %s: %w", path, err)
		}
		if locked {
			return unlockFunc(f), nil
		}
		r, ok := awaitShare(ctx, path, f)
		if !ok {
			return nil, fmt.Errorf("%s: %s is held exclusively", embeddedLockedText, path)
		}
		if r.err != nil {
			return nil, fmt.Errorf("shared lock on %s: %w", path, r.err)
		}
		if r.f != nil {
			return unlockFunc(r.f), nil
		}
	}
}

// awaitShare queues the scope on path's waiter, starting one on f when none
// exists and closing f otherwise. ok is false when ctx ended first.
func awaitShare(ctx context.Context, path string, f *os.File) (r shareResult, ok bool) {
	claim := make(chan shareResult, 1)
	waitersMu.Lock()
	w := waiters[path]
	if w == nil {
		w = &shareWaiter{}
		waiters[path] = w
		go w.wait(path, f)
	} else {
		_ = f.Close()
	}
	w.claims = append(w.claims, claim)
	waitersMu.Unlock()

	select {
	case r := <-claim:
		return r, true
	case <-ctx.Done():
	}
	waitersMu.Lock()
	queued := slices.Contains(w.claims, claim)
	w.claims = slices.DeleteFunc(w.claims, func(c chan shareResult) bool { return c == claim })
	waitersMu.Unlock()
	if queued {
		return shareResult{}, false
	}
	r = <-claim
	return r, r.f != nil || r.err != nil
}

// wait blocks in flock until the shared lock is granted, then hands f to the
// first queued scope and tells the rest to try again. With no scope queued it
// releases the lock at once: an abandoned waiter must not keep bd out.
func (w *shareWaiter) wait(path string, f *os.File) {
	var err error
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	waitersMu.Lock()
	delete(waiters, path)
	claims := w.claims
	w.claims = nil
	waitersMu.Unlock()

	if err != nil {
		_ = f.Close()
		for _, c := range claims {
			c <- shareResult{err: err}
		}
		return
	}
	if len(claims) == 0 {
		unlockFunc(f)()
		return
	}
	claims[0] <- shareResult{f: f}
	for _, c := range claims[1:] {
		c <- shareResult{}
	}
}

// tryShare makes one non-blocking LOCK_SH attempt on f.
func tryShare(f *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return false, nil
		default:
			return false, err
		}
	}
}

func unlockFunc(f *os.File) func() {
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
