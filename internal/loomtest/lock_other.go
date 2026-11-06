//go:build !unix

package loomtest

// lockFixture is a no-op where flock is unavailable. Live-database tests then
// rely on not being run concurrently, which is the single-developer case.
func lockFixture(key string) (release func(), err error) {
	return func() {}, nil
}
