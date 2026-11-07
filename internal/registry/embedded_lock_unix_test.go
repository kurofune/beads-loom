//go:build unix

package registry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
)

// holdExclusive takes the exclusive flock bd takes on path and returns its
// release.
func holdExclusive(t *testing.T, path string) func() {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("exclusive lock on %s: %v", path, err)
	}
	release := func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	t.Cleanup(release)
	return release
}

func lockFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "LOCK")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The shared lock, once held, keeps an exclusive one out until release.
func TestShareLockHoldsASharedLock(t *testing.T) {
	path := lockFile(t)
	release, err := shareLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("an exclusive lock succeeded while Loom held the shared one")
	}
	release()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("exclusive lock after release: %v", err)
	}
}

// A lock still held exclusively at the caller's deadline fails with dolt's
// locked error and hands back no release: the open does not go ahead unlocked.
func TestShareLockFailsClosedOnTimeout(t *testing.T) {
	path := lockFile(t)
	holdExclusive(t, path)
	const wait = 150 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	start := time.Now()
	release, err := shareLock(ctx, path)
	if err == nil || release != nil {
		t.Fatalf("shareLock = (%v, %v); want an error and no release", release != nil, err)
	}
	if !isEmbeddedLocked(err) {
		t.Fatalf("err = %v, want dolt's locked error so the open retries", err)
	}
	if waited := time.Since(start); waited < wait {
		t.Fatalf("gave up after %v, before the %v deadline", waited, wait)
	}
}

// bd writers hand the exclusive lock to each other with only a few
// milliseconds between them, so a reader that polls misses the handoff. The
// shared lock is taken as the exclusive one is released. A fixed wall-clock
// bound would measure scheduler latency on a loaded machine, so each trial
// sets shareLock against a 10ms poller whose next attempt falls at a random
// point after the same release: load delays both alike, and shareLock's
// median lead stays near half the interval, where a shareLock that polls
// itself has none. The writer holds by spinning, not sleeping: a timer
// release would wake with the pollers' timers and line their polls up with it.
func TestShareLockTakesTheLockAsTheWriterReleasesIt(t *testing.T) {
	path := lockFile(t)
	const (
		trials   = 40
		interval = 10 * time.Millisecond
	)
	leads := make([]time.Duration, 0, trials)
	for range trials {
		release := holdExclusive(t, path)
		shared := make(chan time.Time, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			unlock, err := shareLock(ctx, path)
			if err != nil {
				t.Errorf("shareLock: %v", err)
				shared <- time.Time{}
				return
			}
			at := time.Now()
			unlock()
			shared <- at
		}()
		hold := 20*time.Millisecond + rand.N(10*time.Millisecond)
		for start := time.Now(); time.Since(start) < hold; {
		}
		released := time.Now()
		release()
		polledAt, err := pollShare(path, released.Add(rand.N(interval)), interval, 2*time.Second)
		if err != nil {
			t.Fatalf("poller: %v", err)
		}
		sharedAt := <-shared
		if sharedAt.IsZero() {
			t.FailNow()
		}
		leads = append(leads, polledAt.Sub(sharedAt))
	}
	slices.Sort(leads)
	if median := leads[trials/2]; median < interval/4 {
		t.Fatalf("median lead of shareLock over a %v poller on the same release = %v, want at least %v; all: %v",
			interval, median, interval/4, leads)
	}
}

// pollShare takes a shared flock on path by a non-blocking attempt every
// interval, the first at first, and returns when it held the lock.
func pollShare(path string, first time.Time, interval, budget time.Duration) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	deadline := first.Add(budget)
	time.Sleep(time.Until(first))
	for {
		locked, err := tryShare(f)
		if err != nil {
			return time.Time{}, err
		}
		if locked {
			at := time.Now()
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			return at, nil
		}
		if time.Now().After(deadline) {
			return time.Time{}, errors.New("the lock was never released")
		}
		time.Sleep(interval)
	}
}

// Scopes that give up on a held lock leave at most one blocked waiter per
// lock file, the next scope reuses it, and once the exclusive lock goes the
// abandoned waiter lets go of the shared lock at once.
func TestShareLockKeepsOneWaiterPerFile(t *testing.T) {
	path := lockFile(t)
	release := holdExclusive(t, path)
	before := runtime.NumGoroutine()
	for range 5 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		unlock, err := shareLock(ctx, path)
		cancel()
		if err == nil {
			unlock()
			t.Fatal("shareLock succeeded while the lock was held exclusively")
		}
	}
	if grew := runtime.NumGoroutine() - before; grew > 1 {
		t.Fatalf("five abandoned scopes left %d goroutines, want at most one waiter", grew)
	}

	release()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	deadline := time.Now().Add(time.Second)
	for syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the abandoned waiter still holds the shared lock 1s after the writer released")
		}
		time.Sleep(time.Millisecond)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("the abandoned waiter goroutine never exited: %d > %d", runtime.NumGoroutine(), before)
		}
		time.Sleep(time.Millisecond)
	}
}

// Two scopes waiting on one file at once both get the shared lock when the
// writer releases it.
func TestShareLockServesConcurrentWaiters(t *testing.T) {
	path := lockFile(t)
	release := holdExclusive(t, path)
	errs := make(chan error, 2)
	unlocks := make(chan func(), 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			unlock, err := shareLock(ctx, path)
			errs <- err
			if err == nil {
				unlocks <- unlock
			}
		}()
	}
	time.Sleep(30 * time.Millisecond)
	release()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("shareLock: %v", err)
		}
	}
	(<-unlocks)()
	(<-unlocks)()
}

// A cancelled context ends the wait early, still without the lock.
func TestShareLockFailsClosedOnCancel(t *testing.T) {
	path := lockFile(t)
	holdExclusive(t, path)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	release, err := shareLock(ctx, path)
	if err == nil || release != nil {
		t.Fatalf("shareLock = (%v, %v); want an error and no release", release != nil, err)
	}
	if waited := time.Since(start); waited >= 500*time.Millisecond {
		t.Fatalf("waited %v after a cancel at 50ms; a cancelled context should end the wait", waited)
	}
}

// A missing lock file fails and is not created.
func TestShareLockFailsClosedOnMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LOCK")
	release, err := shareLock(context.Background(), path)
	if err == nil || release != nil {
		t.Fatalf("shareLock = (%v, %v); want an error and no release", release != nil, err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("shareLock created %s: %v", path, statErr)
	}
}

// The production opener never reaches the driver without the shared lock:
// a store bd still holds reports locked and leaves the project reachable, and
// a store with no LOCK file is unreachable with nothing written.
func TestEmbeddedOpenRefusedWithoutSharedLock(t *testing.T) {
	workdir := embeddedWorkdir(t, "fx")
	reg := New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: workdir}}})
	t.Cleanup(func() { _ = reg.Close() })
	tg, _, _ := readEmbedded(workdir, "")
	noms := filepath.Join(tg.Dir, tg.Database, ".dolt", "noms")

	before := listTree(t, workdir)
	reg.Refresh(context.Background())
	p, _ := reg.Get("fx")
	if p.Reachable || !strings.Contains(p.Reason, "shared store lock") {
		t.Fatalf("missing LOCK: project = %+v; want unreachable naming the lock", p)
	}
	if after := listTree(t, workdir); !slices.Equal(before, after) {
		t.Fatalf("an open with no LOCK file changed the tree:\nbefore %v\nafter  %v", before, after)
	}

	lock := filepath.Join(noms, "LOCK")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	holdExclusive(t, lock)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	opened, err := runEmbedded(ctx, openDoltEmbedded, tg, func(context.Context, *sql.DB) error {
		t.Error("fn ran on a store opened without the shared lock")
		return nil
	})
	if opened || !isEmbeddedLocked(err) {
		t.Fatalf("held LOCK: runEmbedded = (%v, %v); want not opened, dolt's locked error", opened, err)
	}
}

// With bd holding the exclusive lock for the whole call, the production open
// sequence gives up inside the 2s read budget, after at most five attempts,
// never opens the store unlocked, and returns dolt's locked error.
func TestEmbeddedOpenBoundedWhileExclusivelyLocked(t *testing.T) {
	workdir := embeddedWorkdir(t, "fx")
	tg, _, _ := readEmbedded(workdir, "")
	lock := filepath.Join(tg.Dir, tg.Database, ".dolt", "noms", "LOCK")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	holdExclusive(t, lock)

	var attempts, unlocked int
	countingLock := func(ctx context.Context, path string) (func(), error) {
		attempts++
		release, err := shareLock(ctx, path)
		if err == nil {
			unlocked++
		}
		return release, err
	}
	open := func(ctx context.Context, tg embeddedTarget) (driver.Connector, error) {
		return openDoltEmbeddedLocked(ctx, tg, countingLock)
	}

	start := time.Now()
	db, closeDB, err := openEmbedded(context.Background(), open, tg)
	elapsed := time.Since(start)
	if db != nil || closeDB != nil {
		_ = closeDB()
		t.Fatal("openEmbedded returned a database while the store was held exclusively")
	}
	if !isEmbeddedLocked(err) {
		t.Fatalf("err = %v, want dolt's locked error", err)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("open sequence took %v, want under 2s", elapsed)
	}
	if attempts < 1 || attempts > len(embeddedRetryDelays)+1 {
		t.Fatalf("attempts = %d, want 1..%d", attempts, len(embeddedRetryDelays)+1)
	}
	if unlocked != 0 {
		t.Fatalf("shared lock taken %d times while bd held the exclusive one", unlocked)
	}
}
