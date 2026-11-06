//go:build !unix

package registry

import "context"

// shareLock always fails where flock does not exist: without the shared lock
// nothing keeps dolt from opening the store writable.
func shareLock(ctx context.Context, path string) (release func(), err error) {
	return refuseShareLock(ctx, path)
}
