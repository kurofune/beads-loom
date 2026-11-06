# go-icu-regex, pure Go

Loom replaces `github.com/dolthub/go-icu-regex` with this module (the `replace`
directive in the root `go.mod`). The upstream module wraps ICU through cgo, so
a plain `go build` of the embedded Dolt engine fails on any machine without ICU
headers on the default include path. A stock macOS with Homebrew `icu4c` is one
such machine.

`regex.go` is go-mysql-server's own pure-Go fallback
(`internal/regex/regex_pure.go`, used under its `gms_pure_go` build tag),
published under the upstream API. SQL `REGEXP` therefore follows the syntax of
Go's `regexp` package, not ICU's. Loom's reads use no `REGEXP`.

The engine still needs cgo for `github.com/dolthub/gozstd`, which compiles its
bundled zstd sources and needs no system headers. `CGO_ENABLED=0` does not
build.

License: Apache 2.0, copied from upstream in `LICENSE`.
