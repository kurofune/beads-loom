# Agent Instructions — Loom

Loom is a manager's cockpit for beads: a Go backend (chi router; SQL
reads from per-project Dolt servers; every write shells out to `bd`) with a
React frontend embedded via go:embed. Architecture: `docs/ARCHITECTURE.md`.
Wire-contract truth: `internal/httpapi` (Go) and `web/src/api/types.ts` (TS).

**Profile:** this repository opts into the **team-maintainer** context profile —
agents commit and push (git + `bd dolt push`) once gates pass; an explicit
do-not-commit/push instruction still wins.

**Read first:** `CURRENT.md` (live state — outranks every other doc on what we
are doing now), then `CONTEXT_MAP.md` (where things live) and `DECISIONS.md`
(settled choices). Update them when state moves, before the final push.

**Working a bead:** claim it with `bd update <id> --claim`, run `make check`,
and open a pull request (the maintainer pushes, per the profile above).

**UI work:** every `docs/SCARS.md` item is an acceptance criterion.

## Build & Test

```bash
make dev     # run backend + frontend dev servers (dev-api / dev-web to split)
make check   # go vet + go tests
make test    # go + web tests
make seed    # seed the test beads database
```

## Testing — live-database tests and the environment gate

Some tests query this repo's own seeded beads database over a running Dolt
server. One rule governs what they do when that server is not there:

> **An absent ENVIRONMENT skips. A missing FIXTURE inside a reachable database
> fails.**

- **Environment** = `.beads/dolt-server.port`, `.beads/metadata.json`, and a
  Dolt server answering on that port for that database. A fresh clone, a new
  git worktree, and CI all lack it, nothing in the checkout can conjure it, and
  the code under test does not produce it. So its absence **skips**, and the
  skip message **must name the command that makes the test run** —
  `bd dolt start -C <workdir>` — because a skip nobody knows how to un-skip is
  a test nobody ever runs.
- **Fixture** = anything *inside* that reachable database (a bead, an epic, a
  row, a column), and anything the repo itself ships (`testdata/`). Its absence
  is a finding about the seeded data or the code, so it **fails** — `t.Fatal`,
  never `t.Skip` and never `t.Log`.

**This does not weaken the standing rule** that "a test whose fixture can be
absent must `t.Fatal`, never `t.Log` or `t.Skip`". That rule exists so a test
cannot silently guard nothing, and it still holds for every fixture. The gate
covers exactly one thing — an environment no checkout can provide — and it is
held to the same standard: both implementations have a test proving the gate
*accepts* a complete, reachable environment (`TestProbeAcceptsAReachableEnvironment`,
`TestLiveGateAcceptsAReachableEnvironment`) and one proving the real probe
*rejects* a dead port (`TestPingFailsOnAClosedPort`, `TestLivePingFailsOnAClosedPort`),
so a gate that skipped unconditionally — guarding nothing, which is the failure
this rule is about — would go red.

**Reachability is the gate, not the files.** A stopped server leaves
`.beads/dolt-server.port` behind (the laptop-reboot state), so a gate that only
stats the port file waves a dead port through and the test fails downstream on a
connection error. Both implementations resolve the files and then *ping* the
resolved database.

**One implementation, not a convention to re-type:**
`internal/loomtest/livedolt` — `livedolt.Require(t, workdir)` for a test that
needs the environment, `livedolt.Probe(workdir)` for one that scans candidate
workspaces. Both entry points resolve the port and database the way the registry
does and prove reachability with a real connection. Sole exception: `internal/registry`'s
in-package live tests cannot import it (livedolt imports registry, so that is an
import cycle in their test binary). They use a local copy of the same gate —
`requireLive` / `probeLive` in `internal/registry/livegate_test.go` — that pings
the same way and phrases every skip through the same
`bd dolt start -C <workdir>` message. That duplication is load-bearing; it is
not a dedup target.

**The bar:** `go test ./...` exits 0 in a checkout with no Dolt server running —
whether the checkout never had a port file (fresh clone, new worktree, CI) or has
a stale one left by a stopped server. In both states every live-database test
reports SKIP naming `bd dolt start -C <workdir>`, and none of them FAIL. With a
server up they must actually run and pass rather than skip, which is what keeps
the skips from being vacuous. That is the bar this rule owns, and it is the bar
to hold new live tests to.

**With the server up, the seeded data is the other half of the contract.** A
live test that FAILs against a reachable database is reporting a fixture gap in
the seeded backlog — fix the seed or the assertion, do not convert it back to a
skip. Two rules keep those assertions honest (loom-392):

- **Assert against the database, not against remembered content.** The fixture
  lives in this repo's own live backlog, so "the seed wires a blocks
  dependency" decays into "…and every one of them has since been closed".
  Derive what a query must return from the same tables under the same filters,
  or anchor the claim on data that cannot move (closed work stays closed).
- **Never anchor a time window on Go's clock.** `events.created_at` holds the
  writing process's local wall-clock time, and Dolt's `CURDATE()` resolves in
  the server's system zone while `NOW()` follows the session zone. Read the
  date from the database the query reads. A ±1-day tolerance is not a fix; it
  is the assertion admitting it is reading the wrong clock.

## Git Commit Convention

Conventional Commits with the driving bead ID in the subject —
`feat(web): saved views (loom-3ft.5)`. Work that lands on main with no bead
behind it gets a retroactive one (`bd create` + `bd close --reason="shipped in <sha>"`).
