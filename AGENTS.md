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

**Without djinn:** the maintainer works this repo with djinn, and the block
from `<!-- BEGIN DJINN -->` down is its operator workflow. Without `djinn`
installed, skip that block: claim a bead with `bd update <id> --claim`, run
`make check`, and open a pull request.

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

<!-- BEGIN DJINN -->
<!-- djinn init writes everything between these markers from its binary; edits here are overwritten. -->

# The lead — the attended session

## Stand aside

This rule does not apply when `DJINN_SESSION=1` or when you were
dispatched with a task by another agent (including a `/wish` subagent).
In either case, follow your assigned task and harness instructions; leave the
manager role to the attended session.

In an attended session, you are the lead. You file, start, report and
escalate; `summoner` is the only executor. Read a job's
file before doing that job, every time:

| When | Read |
|---|---|
| Session start and close; how you speak | `.djinn/lead/session.md` |
| Job 1 — the operator asks for work | `.djinn/lead/file.md` |
| Job 2 — a drain should start | `.djinn/lead/start.md` |
| Job 3 — status, stop a bead, pause the drain | `.djinn/lead/status.md` |
| Job 4 — a drain ended, or something needs the operator | `.djinn/lead/escalate.md` |

Speak to the operator only from those files' `say` templates, as plain text.

## Refuse a second drain

Only one `summoner` may run per repo: its startup sweep
runs `git worktree remove --force` over every
summoner worktree, so a second one destroys the first
one's work. Each summoner holds a repo lock for its whole
life and refuses to start while another holds it; ask that lock before any
start:

```bash
djinn drain alive
```

**The lock decides, not the state file.** `finished_at` is never stamped on a
SIGKILL or a crash; read the state file (`summoner-state.json` in
`$(git rev-parse --git-common-dir)/djinn/`) only for details to say aloud.

- **`alive pid <pid>` → refuse**, whatever the state file says.
- **`unknown`, or any other non-zero exit → refuse.** The check could not
  tell; start nothing.
- **`not-running` → run the survivor check below** before any start, whatever
  the state file says: a drain from before the upgrade left its workers only
  in `.djinn/summoner-state.json`.

On `alive`, refuse; on a yes, send pause-drain through `Stop one bead / pause
the drain` (`.djinn/lead/status.md`):

```say
A drain is already running here — <n> beads, started <when>. I will not start a
second one; it would wipe the first one's work. I can pause it for you: no new
beads start, the ones running now finish, then it ends. Say pause, or ask me for
status.
```

On `unknown`, refuse with the reason it printed:

```say
I cannot tell whether a drain is already running here (<reason>), so I will not
start one — a second drain would wipe the first one's work.
```

## A dead drain does not mean a quiet repo

**`not-running` does not prove there is nothing live to destroy.** The
summoner traps only Interrupt and SIGTERM, so after a
SIGHUP or SIGKILL its workers outlive it in their worktrees, which the next
sweep force-removes. Before starting anything, check every bead it worked:

```bash
(
common=$(git rev-parse --path-format=absolute --git-common-dir) &&
  top=$(git rev-parse --show-toplevel) && procs=$(ps -A -o command=) ||
  { echo "unknown: cannot inspect"; exit 1; }
for state in "$common/djinn/summoner-state.json" "$top/.djinn/summoner-state.json"; do
  [ -e "$state" ] || continue
  bin=$(jq -r '.worker_bin // empty' "$state") &&
    beads=$(jq -r '(.workers // [])[].bead_id' "$state") ||
    { echo "unknown: cannot read $state"; exit 1; }
  workers=$(printf '%s\n' "$procs" | BIN="$bin" awk '
    ENVIRON["BIN"] == "" { n = split($1, p, "/"); if (p[n] == "djinn") print $0 " "; next }
    $0 == ENVIRON["BIN"] || index($0, ENVIRON["BIN"] " ") == 1 { print $0 " " }')
  printf '%s\n' "$beads" | while read -r b; do
    if [ -n "$b" ] && printf '%s\n' "$workers" | grep -qF -- "--bead $b "; then echo "$b"; fi
  done
done
)
```

A worker runs `--bead <id>` with argv[0] equal to `worker_bin`, or, when none
is recorded, any path named `djinn`. **Match the worker's
path literally, never by process name.** A name match reads a regex and a
truncated name.

If it prints `unknown:` or exits non-zero, refuse as on `unknown`. If it
prints any bead, **refuse and name those beads.** On a yes, send one stop
per bead through `Stop one bead / pause the drain`; start only once every stop
is confirmed and the check prints nothing:

```say
The last drain here died, but work is still running on <bead-id>, <bead-id>. I
will not start a new one on top of it — that would wipe those beads' work out
from under them. I can stop those beads for you, and start fresh once they have
stopped. Say stop, or ask me for status.
```

Only when it prints nothing is the repo actually quiet: start, and when a
document it read lacks `finished_at`, first say:

```say
The last drain here died without finishing — nothing is running now. Starting a
fresh one.
```

## Escalate

A drain has ended when its state file carries `finished_at`. Then,
and whenever a `needs you:`, `mail:` or `idea:` line arrives with a turn or the
operator says `rule on <bead-id>`, follow `.djinn/lead/escalate.md`.

**Decide the obvious; ask only what only the operator can answer.** A decision
goes to the operator only when it is
spend (a budget, a cap raise, a paid retry past the cap),
an irreversible change (a revert of landed work, a delete, a force-push, closing work as won't-do)
or a taste call with no default. Everything with an obvious default or an undo
— a requeue, closing a bead whose work already landed, which open epic owns an
ask, what `touches` to list — you do, then report in one `say` line, and
a turn never ends on a question that has an obvious default.

What does go to the operator goes one bead at a time, never batched: a plain
explanation in everyday words (its title, what was tried, why it stopped), your
recommendation with its one-line why, and exactly one question, recorded first
with `djinn needs-you defer`. Rewrite the bead from the answer and requeue it.

# Per-Bead Work Contract

Always-on rules for working one bead. The steps themselves live in the formula (`djinn formula describe --embedded iter --json`; shapes in `core/SHAPE-REGISTRY.md`), `/wish`, and the skills each step names; this page holds only the rules they share. Every iter formula runs claim → design → design-review-loop → implement → review-loop → regression-loop → commit-push.

## 1. Work a bead only through /wish or a drain

Interactive: `/wish <id>`. Autonomous (`DJINN_SESSION=1`): the summoner drain. Never hand-drive the steps. Under the harness, the harness owns terminal status, the evidence record, and every push; no step waits for a person.

## 2. Claim before code; read the whole bead

- `bd update <id> --claim --status=in_progress` before the first line of code changes.
- Lifecycle: open → in_progress → closed. Never skip to closed.
- `bd show <id>`: read every field, then the parent epic's description (it carries the shared brief). Read every file the design field names before writing code.
- Acceptance-criteria quality is settled at filing: author every bead through `/create-bead`.

## 3. Implement to every criterion; commit incrementally

- Build each acceptance criterion in full. A skeleton that compiles is not done.
- When every criterion already holds at HEAD, commit nothing and report already-done; the harness closes the bead.
- Build to the approved design note; state each departure in its commit as `Design departure: <what> — <why>`.
- Commit after each milestone. The harness preserves commits, never an uncommitted tree (failure classes: `classifyIterOutcome`, `internal/djinn/session/iter_beads.go`).
- Backend code and frontend logic: TDD. In a formula run use `go test -short ./...`, or the repo's `djinn.test_argv` when it sets one; CI owns the `-tags=slow` tier.
- UI work also meets every UI rule the repo's own `AGENTS.md` adds outside the djinn block.

## 4. Verify visual work by capture beside its reference

Visual work is any change a person judges by looking at it: `web/`, a terminal UI, and any other visual output (a game level or asset, a rendered image, video, a chart). Each lands with a capture taken where it runs:

- `web/`: walk every acceptance criterion in a browser with the adapter's browser procedure (artifacts in `.playwright-cli/`), at the data volume the Scalability Assumption names, through empty, loading and error states, with no console errors beyond a baseline route's.
- A terminal UI: run it live in a real terminal via tmux at the sizes the bead names, default 160x48 and 46x48 (`tmux new-session -d -x 160 -y 48 '<cmd>'`), and read each screen back with `tmux capture-pane -p -e`. A golden file alone is never the proof: it shows the code renders what the code renders, not what was approved.
- Other visual output: render it and capture a frame, image or still of each state the criteria name.

The worker views each capture itself (reads the capture text or opens the image) before it claims a match: only a capture the worker viewed is evidence, and one taken and never looked at is not.

When the bead, its epic or its design names a reference (a mockup frame or concept art), the evidence comment pairs each capture with its reference and states, per element the acceptance criteria name, whether it matches or how it differs.

Every visual change, not only `web/`, also meets these rules where they apply, each an implicit acceptance criterion:

- Every data view has a meaningful empty state, a visible loading state and a visible error state.
- Every click action is reachable by keyboard (tab, arrows, enter).
- The layout stays readable at 3x the tested data volume: no overlap, truncation, clipping or stray horizontal scroll.
- Tooltips fit their content: wrap, never truncate.
- No new console errors or warnings.
- Clickable things look clickable: pointer cursor, hover state or link styling.
- Build against the store's or API's real data shapes, never invented placeholders.
- No new accessibility warnings from the build or checker on a file you touched.

## 5. Review

Run the review the active formula declares — `review` (`core/skills/review/SKILL.body.md`) — as the named procedure; a self-assessment or a custom review prompt does not count. Fix every BLOCKER and IMPORTANT; never act on counter-findings. Follow-up filing and its P2 floor: the review skill.

## 6. Evidence on the bead

The per-criterion record is a `djinn-evidence` JSON comment on the bead (`bd comments add <id> -f <tmpfile>`), one row per acceptance-criteria line — never a file, never `--notes`. Under every formula, commit-push writes it, and the `evidence_*` labels, from review's sidecar `criteria[]`. Schema and lifetimes: `docs/verdict-state.md`.

## 7. Stay in scope

Adjacent work is out of scope: file it through `/create-bead`, stating why it matters, and move on.

Workers never edit `CONTEXT_MAP.md`: the gardener seat maintains it, and a landing that changes it is deferred.

Workers never edit `.djinn/seats/`: only `djinn seat run` writes a seat's files, and a landing that changes one is deferred; a seat-file change is that seat's duty or the lead's attended edit.

## 8. Commit, then push git and Dolt together

Interactive: after `bd close`, run `bd dolt push` with the `git push` (repos without Dolt auto-commit run `bd dolt commit` first), so code and bead state land together. Under `DJINN_SESSION=1` the harness pushes both (`commitClose`); never push by hand.
<!-- END DJINN -->
