//go:build unix

package loomtest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockFixture takes an exclusive flock on a file keyed by the repo path.
//
// flock is released automatically when the process exits, so a crashed test
// binary cannot leave a stale lock behind — which is why this uses flock rather
// than an O_EXCL lock file.
func lockFixture(key string) (release func(), err error) {
	sum := sha256.Sum256([]byte(key))
	path := filepath.Join(os.TempDir(), "loom-fixture-"+hex.EncodeToString(sum[:8])+".lock")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}

	deadline := time.Now().Add(lockTimeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timed out after %s waiting for the fixture lock at %s", lockTimeout, path)
		}
		time.Sleep(25 * time.Millisecond)
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
