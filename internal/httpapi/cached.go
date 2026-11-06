package httpapi

import (
	"context"
	"database/sql"
	"net/url"

	"github.com/kurofune/beads-loom/internal/readcache"
)

// cachedRead runs read against project's database through the result cache:
// a request whose project hash has not moved since the last read gets that
// read's result without running it. params must capture every input that
// changes the result; with no cache configured every call reads.
func cachedRead[T any](
	ctx context.Context,
	s *Server,
	project, endpoint, params string,
	read func(context.Context, *sql.DB) (T, error),
) (T, error) {
	return readcache.Load(ctx, s.cache, project, endpoint, params, func(ctx context.Context) (T, error) {
		var out T
		err := s.queryDB(ctx, project, func(ctx context.Context, db *sql.DB) error {
			var err error
			out, err = read(ctx, db)
			return err
		})
		return out, err
	})
}

// cacheParams is the canonical form of a request's query string: the same
// parameters in any order give the same key.
func cacheParams(q url.Values) string { return q.Encode() }
