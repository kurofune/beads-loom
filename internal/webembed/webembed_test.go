package webembed

import "testing"

// TestFSTolerAtesEmptyDist is the property that matters: the package must
// compile and behave sanely when no frontend has been built, so `go build ./...`
// and `go test ./...` never depend on npm.
func TestFSToleratesEmptyDist(t *testing.T) {
	fsys, present := FS()
	if !present && fsys != nil {
		t.Error("FS returned a filesystem while reporting present=false")
	}
	if present && fsys == nil {
		t.Error("FS reported present=true with a nil filesystem")
	}
}
