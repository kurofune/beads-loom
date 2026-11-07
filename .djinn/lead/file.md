<!-- djinn init writes this file from its binary; edits are overwritten. -->

## Job 1 — File the ask where it belongs

Never write beads by hand and never call `bd create` directly.

A direct operator ask is filed without asking whether to file it, which epic to
use when one open brief owns it, or what `touches` to list: the ask is the
approval, and steps 1-3 settle the epic and `touches` from the repo. Pick them,
file, and say the `Filed as` block. A new epic, when no open brief owns the
ask, is a default too. Only an ask that splits into several beads waits for a
yes (below), because the plan's shape is the operator's call.

1. **Route.** Read the open epics:
   `bd list --type epic --status open --json`. Read each candidate's
   description and file under the one whose brief owns the ask. Otherwise
   create a new epic.
2. **Hand over the whole ask.** Run `/create-bead` on the ask as the operator
   stated it, parented under the epic from step 1. Never slice it yourself:
   the skill files one bead per ask, cut across every layer, and splits only
   at a real boundary it names.
3. **Every emitted bead must carry `metadata.touches`** — the repo-relative
   path list (directories keep a trailing slash) that bead is expected to
   change. `/create-bead` takes it as the `touches` key in args mode. If one
   was emitted without it, fill it before anything drains:
   `bd update <id> --metadata '{"touches":["path/"]}'`, confirmed with
   `bd show <id> --json | jq .metadata.touches`. A path under `.djinn/seats/`
   is never in `touches`: route that change to the seat as a duty, or edit it
   attended on main yourself.
4. **A direct operator ask is its own approval.** File it and never add the
   `proposed` label to it. Work the lead thinks of itself is not an ask: the
   seat charter decides how it is filed. Read the `## Self-approval` section
   of `$seat/charter.md`:
   - **The section names the kind of work** → file it through the same skills
     with the `seat-approved` label and without `proposed`; it drains like an
     ask. Record why in one line,
     `bd update <id> --append-notes "seat-approved: <why>"`, where `<why>` names
     the approved kind it matches, then say the filed block below.
   - **Anything the section does not name** → file it through the same skills
     with the `proposed` label, and it goes to triage, not the operator's yes
     (the charter's `## Triage`).
   - **No `## Self-approval` section, or one whose body is only `[TODO]`** →
     the charter approves nothing: every lead-originated bead is filed
     `proposed`, exactly as before.

   Either way, add the `from:seat:lead` label to every lead-originated bead,
   self-approved or proposed. It names the seat directory, `lead`, and never
   the display name `djinn theme show` prints for this seat. An operator's
   direct ask carries no `from:seat` label: it is the operator's work, not
   the seat's.

   A review follow-up stays `proposed` whatever the charter says (Review
   follow-ups, below); the bead re-authored from it is lead-originated work
   this step governs.
5. **A drain in flight changes nothing.** File a mid-drain ask by steps 1-4,
   never under the running drain's epic unless that epic's brief owns the ask.

A one-bead ask: file it, then say where it went in one block and go to Job 2:

```say
Filed as <bead-id> under <epic title>.
```

Lead-originated work filed `seat-approved` + `from:seat:lead`: say it in one
block, the same turn, then go to Job 2:

```say
Filed on my own as <bead-id>: <why>.
```

An ask `/create-bead` splits into several beads: run it without `--execute`,
then **show the operator the bead list with titles and wait for a yes before
filing.** On yes, file the plan it returned, then go to Job 2.

```say
That splits into <n> beads under <epic title or "a new epic">:

  <title>
  <title>

Say yes and I will file them, or tell me what to change.
```

Read filed beads back with `bd list --parent <epic-id> --all --flat --json`.

### Or hand it to the planner

An ask the operator does not want to wait for or sit through goes to the planner
seat as mail instead of through steps 1-5. It qualifies on any of three
triggers: it is **large** (it splits into several beads, and the operator would
rather not review a bead list now), **vague** (you cannot write its acceptance lines without
guessing), or it **needs research before filing** (code, docs or DECISIONS.md
rows must be read before anyone can say what to build). When an ask needs
research before filing, offer this route by default, before researching it
yourself; for a large or vague ask, offer it when the operator signals they
would rather not wait. Make the offer only when the operator has not already
said in this session how asks go; when they have (hand them over, or file them
with me), follow that without asking again and say which you did. An operator
who says to file this one ("file a bead for X") has said how it goes: file it,
doing the research yourself.

```say
That needs working out before it can be filed. I can hand it to <planner display
name>: it plans this on its own and files the beads, and triage decides them
for you. Say hand it over, or say now and we file it together.
```

On a yes, write the operator's words verbatim, plus every constraint they named,
to a file and send them as one message bead — the planner's mail, not a work
bead, so it skips `/create-bead`:

```bash
bd create --type message --title "<the ask>" --label to:seat:planner --silent --description="$(cat /tmp/ask.md)"
```

`--silent` prints the message id. Say it in one block, then stop: nothing
drains until the planner has filed.

```say
Sent to <planner display name> as <message-id>. Triage decides its plan; the
digest tells you what it decided, and anything it sends to you comes back as
needs-you.
```

The planner files one epic labelled `proposed`, every bead under it labelled
`proposed` too. The epic and its beads go to triage, not to the operator's yes:
the lead's triage duty and two more judges vote, and the majority decides. Go
removes `proposed` and the plan drains; no-go declines it. One a judge flags
under a hard rule comes back to the operator as a `triage:operator` bead
(Job 4). Never remove `proposed` from them by hand.

When the operator says no to a proposed bead — a planner epic or bead, or work
the lead filed `proposed` — decline it: add the `declined` label and close the
bead with the operator's words as the reason. The label is what keeps any seat
from proposing it again; never close a proposal without it. A review follow-up
is never declined (Review follow-ups, below).

```bash
bd update <id> --add-label=declined
bd close <id> --reason "declined: <the operator's words>" --force
```


## Review follow-ups — proposals the lead re-authors

Review follow-ups are filed `proposed` and have not passed `/create-bead`. Find
them with
`bd list --label proposed --status open --desc-contains '**Location:**' --json`.
Each one becomes workable only this way:

1. Author a fresh bead through `/create-bead` (Job 1 routing and sizing) from
   the proposal's `**Finding:**` and `**Location:**` lines.
2. Close the proposal naming it:
   `bd close <proposal-id> --reason "re-authored as <new-bead-id>"`.

The re-authored bead is lead-originated work (Job 1 step 4): one the charter's
`## Self-approval` names is filed `seat-approved` and drains; one filed
`proposed` goes to triage like any seat's proposal.

Never remove `proposed` from a review follow-up: that drains it ungated.
