<!-- djinn init writes this file from its binary; edits are overwritten. -->

## Job 2 — Start the drain

### The workshop decides first

```bash
djinn workshop status
```

- **Workshop up → never start a drain yourself.** The next tick picks the work
  up.
- **Workshop down → offer `djinn workshop up` or a one-off bounded drain.** Run
  whichever the operator picks; a one-off drain follows the rest of Job 2.
- When `djinn workshop status` exits non-zero or reports an unknown command, the
  workshop is down and `djinn workshop up` does not exist here: offer only the
  one-off drain.

```say
It is queued — the workshop picks it up on its next tick.
```

```say
The workshop is off, so nothing drains this on its own. Say workshop up and I will
start it, or say go for a one-off drain of just this work.
```

```say
Nothing drains work here on its own. Say go and I will start a one-off drain of
just this work.
```

### Refuse a second drain

Before any start, run `djinn drain alive` and refuse on `alive` or `unknown`
— any exit but 0 — then the dead-drain survivor check, both in the lead kernel
(the djinn block of `AGENTS.md`). Its refusals send stop and pause through
`Stop one bead / pause the drain` (`.djinn/lead/status.md`).

### Guards, batch size, and budget

- `--max-retries <n>` is mandatory on every start — how many times one bead may
  be respawned (default 2). It is a stuck-bead guard, not a budget.
  A usage-limit refusal whose reset is known waits for the reset and spends no retry.
- `--max-beads <n>` is the batch size — how many beads this drain takes
  (default 5).
- No spending cap unless the operator asked for one.
- Once the Claude week reaches `pause_at_percent` (`workshop.claude_window`,
  default 85) with more than `unless_reset_within` (default 24h) to its reset,
  the tick starts no drain and only the kept duties run (`keep_duties`, default
  the lead's rule, digest and inbox); a running drain starts no new bead while
  its running ones finish. `djinn workshop spend` overrides it through the reset.
- Once the Codex week reads 100% and the account holds purchased credits
  (`workshop.codex_window.pause_past_limit`, default true), every further Codex
  step is paid from that balance: the tick starts no drain and a running drain
  starts no new bead while its running ones finish. `djinn workshop spend`
  overrides it through the reset.

```say
Starting the drain now: at most <max-beads> beads, <max-retries> retries per
bead. Whatever is already running finishes.
```

#### Only when the operator asked for a budget

Append `--max-cost <usd>` with their number to the start line, add
`and $<usd> total` to the start block, and record the budget in
`$seat/memory.md` until they lift it.

### The drain scope

**A start carries exactly one of `--epic` or `--bead`** — never both, and never
neither:

- `--epic <epic-id>` — the ready descendants of that epic.
- `--bead <id>,<id>` — an explicit list of bead ids, matched exactly against the
  ready set (repeatable, or comma-separated).

With neither flag the summoner drains the repo's **whole
ready set** up to `--max-beads`, which is almost never the work the operator
just agreed to. Job 1 tells you which you have: a new epic `/create-bead`
filed → `--epic`; beads filed under an existing epic, or a
hand-picked handful → `--bead` with exactly those ids, so the drain takes
none of that epic's other work.

### Which binary starts the drain

**Never start a drain by building the supervisor from a source path**
(`go` + `run` over `./cmd/summoner`). That path exists
only inside the djinn repo; in any other repo the very first thing the lead
does dies with `stat .../cmd/summoner: directory not
found`. Every start line below invokes the **installed**
`summoner` binary on `$PATH`
(`go install ./cmd/summoner` from a djinn checkout puts
it there).

The start line is the same in every repo — one scope flag plus the guards:

```bash
summoner --epic <epic-id> --max-workers <n> --max-beads <n> --max-retries <n>
```

**Which binary runs the worker.** Inside djinn's module (a root `go.mod` naming
`github.com/kurofune/djinn`) the summoner builds
`./cmd/djinn` from HEAD; in any other repo it runs the
`djinn` installed beside it, else the one on `$PATH`.
`--worker-bin <path>` overrides either; add it only when the operator asks.

At startup the summoner checks that the worker comes
from its own build and aborts "before any bead is claimed" when it does not —
a half-finished upgrade. The abort names the fix: reinstall both from one
build (`scripts/install.sh`). Relay that to the operator; never add the escape
hatch `--skip-worker-version-check` to a start line unless the operator asks
for it by name.

### Start it in the background

`--max-workers <n>` is the worker count: take it from the operator's request,
or **omit the flag entirely and let the summoner apply
its own default of 3**. Never invent a number. Start the drain as a
**background process** with exactly one of `--epic` or `--bead` — it writes
`summoner-state.json` and one log file per bead under
`$(git rev-parse --git-common-dir)/djinn/`, which status and the
wrap-up read:

```bash
summoner --detach --epic <epic-id> --max-workers <n> --max-beads <n> --max-retries <n> > .djinn/drain.out 2>&1
# …or, for a bead-list drain, the same command with the other scope flag:
summoner --detach --bead <id>,<id> --max-workers <n> --max-beads <n> --max-retries <n> > .djinn/drain.out 2>&1
```

Run the line as its own command, exactly as written: never add `nohup` or `&`.
`--detach` moves the drain into its own session, which outlives a harness that
kills the shell's process group when the command returns, and returns once the
drain holds the repo's lock. A non-zero exit means no drain started; relay the
last `summoner:` line of `.djinn/drain.out` to the operator.

When `HERDR_ENV` is `1`, run `djinn ops room --herdr` once after the start; it
finds or builds the ops room and changes nothing that is already there.
Otherwise run nothing more.

```say
The drain is running in the background. Ask me for status any time and I will
read it back. Per-bead output is in <log-dir>/<run-id>/<bead-id>.log.
```
