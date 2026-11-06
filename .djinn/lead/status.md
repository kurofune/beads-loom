<!-- djinn init writes this file from its binary; edits are overwritten. -->

## Job 3 — Status on demand

One command, every time:

```bash
djinn ops snapshot --json
```

Answer from four of its sections:

- `drain` — the run and its `workers[]`: each worker's `bead_id`, `step`,
  `step_started_at`, `cost_usd` and `disposition` (empty while it works).
- `ready` — `count` beads still to go and the `next` few in dispatch order.
- `needs_you` — every bead and message waiting on the operator.
- `epics` — per epic, its beads `done` before this run, landed `tonight`,
  `doing` now and still `todo`.

Read `warnings` first: a null section is one whose source could not be read,
not an empty one. Each warning names its `source` and carries a `message`.

If `drain` is null and its `drain` warning ends `no such file or directory`,
no drain has run here — say exactly that and stop; there is nothing to report:

```say
Nothing is running here — no drain has been started in this repo yet. Tell me
what you want and I will file it.
```

If `drain` is null under any other `drain` warning, the drain state exists but
is unreadable — a drain may well be running. Never say none has started; say
this and stop:

```say
I cannot read the drain state right now: <warning message>. A drain may still
be running — I will check again when you ask.
```

A `bd` warning nulls `ready` and `epics` and leaves only red-main items in
`needs_you` (null when main is not red): the beads are unknown, not empty. Put
any red-main item to the operator as usual, drop the to-go count and the epic
line, never say nothing needs you, and end the answer with this instead of its
last line:

```say
I cannot read the bead list right now (<warning message>), so I cannot tell
what is still to go or whether anything needs you.
```

Otherwise answer in plain words. Never paste the JSON.

**Status answer template** (never more than five lines, plus one "Filed on my own" line per seat-approved bead):

```say
<n> beads in flight, <n> done, <n> to go.
Working now: <bead-id> (<elapsed>), <bead-id> (<elapsed>).
<epic title>: <n> of <n> beads landed.
Filed on my own: <bead-id> — <why>
Spent $<total> so far.
Nothing needs you yet.
```

The "Filed on my own" line repeats once for each `seat-approved` bead created
since the lead's last status answer this session, or since session start for
the first, and is dropped when there is none. Read them with
`bd list --label seat-approved --json --all -n 0 --created-after "<since>"` (RFC3339);
`<why>` is the bead's `seat-approved:` notes line. A bead whose `from:seat:<dir>`
label names another seat adds ` (by <that seat's display name>)` after `<why>`.

In flight is the workers with an empty `disposition`, done those `closed`, to
go `ready.count`; elapsed runs from `step_started_at`; the epic line is the
`epics` entry the drain works on; spent is the summed `cost_usd`. The last line
reads `needs_you`, and it is the one that matters: when something does need the
operator, say what and drop straight into Job 4.

### Stop one bead / pause the drain

"Stop that bead", "tell that bead", "pause the drain" and "run 5 workers" are messages: a bead of
type `message` the running drain reads and closes. No djinn or summoner
command sends them.

| The operator says | File |
|---|---|
| stop that bead | `bd create --type message --title "<why>" --label to:<bead-id> --label directive:stop --silent` |
| tell that bead | `bd create --type message --title "<note>" --description "<detail>" --label to:<bead-id> --label directive:note --silent` |
| pause the drain | `bd create --type message --title "<why>" --label to:drain --label directive:pause-drain --silent` |
| run <n> workers | `bd create --type message --title "<why>" --label to:drain --label directive:set-workers --label workers:<n> --silent` |

- **stop lands at the bead's next step boundary**, never mid-step — a long step
  runs to its end first. It leaves the bead open — not closed, not failed —
  with a comment naming where it stopped, and the drain carries on with the rest
  of the work; the bead is not retried in this drain.
- **a note lands at the bead's next step boundary** too, and the worker keeps
  going: from then on every later prompt of that run carries it under
  "Follow-ups since you started", title first, detail below. Use it to correct a
  worker without stopping it.
- **pause-drain lands at the drain's next poll.** No new bead is dispatched;
  running workers finish their beads, then the drain ends. It does not resume:
  starting again is a fresh Job 2 start.
- **set-workers lands at the drain's next poll.** The drain's worker count
  becomes `<n>`: raising it starts more beads on that poll; lowering it stops
  no running bead — the drain starts new ones only once fewer than `<n>` run.
  `<n>` is 1-16; for any other number tell the operator the count must be 1-16
  and file nothing.
- Send stop only to a bead the snapshot's `drain` shows in flight or the survivor check
  in the lead kernel names, and pause-drain and set-workers only while a drain
  is running (`djinn drain alive` prints `alive`). Nothing reads a message nobody is working on; it
  waits open.
- `--title` carries the operator's reason in their words. A message bead has no
  acceptance criteria and never goes through `/create-bead`.
- If `bd create` rejects the type as unknown, `djinn init` never registered it
  in this repo. Register it once and file again:

  ```bash
  bd config get types.custom
  bd config set types.custom "<existing>,message"
  ```

**Confirm delivery before saying anything happened.** `--silent` prints the
message id. Poll it:

```bash
bd show <message-id> --json
```

until `status` is `closed` and the closing comment
`djinn inbox: directive <directive> consumed at <boundary> by <consumer>` is
present (it is the `close_reason`, and `bd comments <message-id> --json` carries
it too). A closing comment reading `unknown directive` means nothing was acted
on: check the labels and file again. One reading `rejected` names what was
wrong with the labels; nothing changed. Never say work stopped before that comment
exists: until the drain consumes the message it can still start new work. While
you wait, say only that it is sent and takes effect when picked up:

```say
Sent. It takes effect when the drain picks it up — I will tell you when
<bead-id> has taken the stop.
```

```say
Sent. It takes effect when the drain picks it up — I will tell you when the
drain has taken the pause.
```

```say
Sent. It takes effect when the drain picks it up — I will tell you when the
drain has taken the new worker count.
```

Once the comment exists, one block:

```say
Stopped <bead-id>. It is still open with its work so far kept; the drain carries
on with the rest.
```

```say
The drain is paused. No new beads start; <n> running now will finish, then it
ends. Say go when you want a fresh one.
```

```say
The drain now runs up to <n> beads at once. Any running past that finish first;
none is cut short.
```
