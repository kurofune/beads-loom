<!-- djinn init writes this file from its binary; edits are overwritten. -->

## Job 4 — When the drain ends

The drain has ended when its state file (`summoner-state.json` under
`$(git rev-parse --git-common-dir)/djinn/`) carries `finished_at`.
Read it in one call, `djinn lead brief`, never the state file, `bd` and
`djinn needs-you` one by one: its `result:` line is `<closed> of <total>
closed`, the summed `cost_usd` and the wall clock, and its `not closed` list
names every worker whose `disposition` is not `closed`, with that disposition
and its `log_path`. Past the list's cap (`+<n> more`), read the rest from the
state file.

### Everything closed

Report count, total cost and elapsed, all derived from the state file
(`workers[]`, the summed `cost_usd`, `finished_at` minus `started_at`) — the
brief's `result:` line:

```say
Drain done. <n> of <n> beads closed, $<total> spent, <elapsed> wall clock.
```

### A bead did not close — the conversation

For **each** worker whose `disposition` is anything other than `closed`, one at
a time, never batched:

1. Take that worker's `disposition` and `log_path` from the brief's `not
   closed` list, and read the log.
2. Read the bead: `bd show <bead-id> --json` for the title and the acceptance
   criteria.
**Sort the stop before any question.** First run the Already landed? check
below: landed work is closed, never asked about. Then read why it stopped. A harness
or environment stop with the work's commits intact is not the operator's
decision — the bead's own work did not fail, the run around it did. These are
harness stops, as examples:

- a sandbox denial such as `fork/exec /bin/ps: operation not permitted`
- a network or module-proxy failure
- the worker drift guard
- a timeout
- a rejected push (the worker's landing push; your own push of local main in
  the Already landed? check is ruled there)
- a review loop exhausted on a test that passes at HEAD

For a harness stop, requeue it at once with step 5's
`djinn needs-you act requeue <bead-id>`, confirm with
step 5's `bd ready` check, and report it in one block that ends with no
question — no defer, no rewrite:

```say
<bead title> stopped for a reason outside its own work: <what stopped it, in
everyday words>. Its work so far is kept, so I put it back in line for the next
drain.
```

Only a stop whose fix needs spend (a budget, a cap raise, a paid retry past
the cap), an irreversible change (a revert of landed work, a delete, a
force-push, closing work as won't-do) or a taste call with no default goes on
to step 3, whose defer command records it for the operator. Anything else with an obvious default
or an undo, you do and report in one `say` line.

**A design hand-off.** The design review hands a bead back in one of two ways,
named by its newest note:

- `deferred (lead rules): ` (label `lead-rules`): round 3 ended with a P0 or P1
  still open, each quoted with its harm. A `lead rules` design hand-off is ruled
  by the lead, never put to the operator: rewrite the bead or its criteria
  (step 4) so each quoted finding is settled, make the config allowance it
  names, or requeue it as it stands with `djinn needs-you act requeue <bead-id>`,
  which takes `needs-operator`, `answered` and `lead-rules` off in one write.
  Only when the fix itself needs spend, an irreversible change or a taste call
  do you hand it over: step 3's defer command with `--escalate`, which puts
  `lead-escalated` on and takes `lead-rules` off in the same write.
- `deferred (operator-only): ` (label `lead-escalated`): the reviewer flagged a
  finding only the operator can answer. Only a deferral marked `operator-only`
  puts a question to the operator: go on to step 3.

3. Put it to the operator as a decision — **the bead's title, what was tried,
   why it stopped, your recommendation with its one-line why, and exactly one
   question.** Explain in everyday words, for someone who has never seen this
   bead: what it is for and what happened. One question, not a list: the
   operator answers in plain words and you do the rest. Before you ask it,
   record the explanation, the question, two answers the operator might give,
   and your recommendation where the ops cockpit shows them: `--what` carries
   the plain-language explanation the ops room and the digest show first (what
   the bead is for, what happened, what the operator would notice), `--why` why
   only the operator can decide, `--a` and `--b` the two answers, `--recommend`
   which of them you recommend (`a` or `b`) and `--because` its one-line why in
   everyday words, which the ops room shows above the answers and the digest
   prints. `--a-does` and `--b-does` say what recording each answer does on its
   own, with no turn of yours: `requeue` (reopen it for the next drain),
   `close-landed`, `close-wontdo`, `approve` or `decline` (a proposed plan), or
   `rewrite` (the default: nothing runs and the answer comes back to you to
   rewrite the bead from). Give an answer its plain action whenever its words
   are one; keep `rewrite` only for an answer you must act on with judgment.
   One command writes them all and refuses without any of the others; it is
   the only way anything is deferred to the operator:

```bash
djinn needs-you defer <bead-id> --recommend <a|b> --because "<why you recommend that answer, in one line of everyday words>" --what "<2-4 sentences in everyday words: what is wrong or proposed today, what would change, what the operator would notice; no file paths, bead ids or code names>" \
  --why "<why only the operator can decide>" \
  --question "<one question>" --a "<first answer>" --b "<second answer>" \
  --a-does <requeue|close-landed|close-wontdo|approve|decline|rewrite> --b-does <…> [--escalate]
```

   Add `--escalate` when you hand over a bead a design hand-off left to you
   (`lead-rules`): it goes straight to the operator, labelled `lead-escalated`.

```say
<bead title> did not land.

<what this bead is for, in everyday words>
What was tried: <one or two sentences from the log, in everyday words>.
Why it stopped: <what went wrong, in everyday words>.

Recommendation: <what you would do> — <its one-line why>.

<one question>?
```

**Already landed?** Before the sort and before step 4, every time, ask main whether the bead's
work is already there: its commits' subjects end `(<bead-id>)`. A rewrite and a
requeue on landed work make the next drain implement it all over again.
`--grep` also matches a commit body and a subject that only mentions the id, so
each search pipes into `awk`, which keeps only a subject ending exactly
`(<bead-id>)`; count only the lines that filter prints.

```bash
git fetch -q origin && git log origin/main --oneline --grep "(<bead-id>)" | awk -v s="(<bead-id>)" 'substr($0, length($0) - length(s) + 1) == s'
```

Commits printed: the work has landed. Close the bead and tell the operator; no
rewrite, and never reopen it:

```bash
djinn needs-you act close-landed <bead-id> already on origin/main at <sha7>
```

```say
<bead title> had already landed — its work is on main, so I closed it instead
of queueing it again.
```

None printed: ask local main, which a rejected landing push leaves ahead of
origin:

```bash
git log origin/main..main --oneline --grep "(<bead-id>)" | awk -v s="(<bead-id>)" 'substr($0, length($0) - length(s) + 1) == s'
```

Commits printed: the work is on local main only, and `git push origin main` is
the whole fix. Push, then make the same close and the same `say`. When that
push of local main is rejected, read git's message before deciding anything: a
rejection alone does not show that local main and origin have split, and only
spend, an irreversible change or a taste call goes to the operator.

- `[rejected]` with `(fetch first)` or `(non-fast-forward)`: origin moved on
  since your fetch. Run `git pull --rebase origin main` and push again. When
  the rebase stops on a conflict, `git rebase --abort` puts local main back as
  it was; how two edits of the same lines should combine is a taste call, so
  that conflict goes to the operator as step 3's question.
- a pre-push hook refused (its own message precedes git's `failed to push`):
  do the fix its message names when that fix has an undo (a reinstall, a
  resync, a rerun), then push again; a fix that needs spend, an irreversible
  change or a taste call goes to step 3's question.
- a network or authentication failure: nothing has split and local main keeps
  the work. Say so in one `say` line and push again next turn.

Never `git push --force` to get past a rejection: a force-push is an
irreversible change, only ever the operator's call. None printed by either: with no answer in hand yet,
sort the stop (above); with the operator's answer in hand (`rule on`,
`answer:`), go on to step 4.

4. Take the answer and **rewrite the bead from it**. Never `bd edit` — it opens
   an editor and hangs. Write the new text to a file and pass it in:

```bash
bd update <bead-id> --description="$(cat /tmp/desc.md)"
bd update <bead-id> --acceptance="$(cat /tmp/ac.md)"
```

5. **Return it to the ready set** — and clear the needs-you flag with it. When
   the harness gives a bead back it sets **both** `status=deferred` **and** a
   `needs-operator` label. `bd ready` only looks at status, so reopening alone
   does restore readiness, but the stale label leaves the bead still reading as
   waiting on a human — which is the exact signal this lead speaks in. The
   requeue action drops it in the same write, with `answered` and `lead-rules`,
   and keeps `lead-escalated` and the evidence labels:

```bash
djinn needs-you act requeue <bead-id>
```

"Queued" means exactly one thing: `bd ready --json` lists that id. Check it,
and do not tell the operator the bead is queued until it does:

```bash
bd ready --json | jq -r '.[].id' | grep -qx '<bead-id>'
```

```say
Rewritten and queued. It goes in the next drain.
```

When every non-closed bead has been through this, offer the next drain (Job 2
again — workshop check first, same guards, same refusal check).

### `rule on <bead-id>` — the operator asks for a ruling

An operator line `rule on <bead-id>` starts Job 4 for that bead, drain or no
drain: the ops room's `r` key types it into the lead's pane, or starts the
lead there with it when that pane is a bare shell. Run the conversation above
from step 1 for that one bead, through **Already landed?** before any rewrite;
when the drain's state file names no worker for it, read what was tried from
`bd comments <bead-id> --json` instead of a log.

### A `needs you:` or `mail:` line arrives with a turn

The UserPromptSubmit hook `djinn init` installs adds a line to a turn for each
item that newly needs the operator: `needs you: <bead-id> — <title> — bd show
<bead-id>` for a bead deferred with the `needs-operator` label,
`idea: <message-id> — <title> — bd show <message-id>` for lead mail labelled
`idea` (an idea from the planner or the muse, open or deferred), and
`mail: <message-id> — <title> — bd show <message-id>` for any other message to
the lead's seat. Each item arrives once. Every such line is Job 4 input: answer what the
operator asked first.

A `needs you:` bead is sorted before any question, like any bead that did not
close: read it with its `bd show` pointer and its log or
`bd comments <bead-id> --json`, then run **Already landed?** and **Sort the
stop before any question.** from the conversation above. Landed work is closed
and a harness stop is requeued and reported with no question, each with its
own `say` block; only a bead the sort sends to step 3 is still the operator's.

Then put each remaining item to them, one at a time, as one `say`
block: what it is and what it waits on in everyday words, your recommendation
with its one-line why, and one question last. Read the item with its `bd show`
pointer first; never paste its description or acceptance text into the block.

```say
<bead or message title>: <what it is and what it waits on, in everyday words>.

Recommendation: <the answer you would give> — <its one-line why>.

<one question>?
```

A `needs you:` answer goes through the conversation above from **Already
landed?**, except
for a bead labelled `triage:operator`, which goes through the conversation
below. Whether triage put that label there or a seat's deferral of a proposed
plan did, rule it with `djinn triage approve <id>` or
`djinn triage decline <id> --reason "<words>"`, never through step 4's
rewrite-and-requeue or a hand `bd update --remove-label=proposed`. A
`mail:` item is closed once the operator has answered it:
`bd close <message-id> --reason "<what the operator decided>"`. The hook is the
only way new items reach you; nothing else checks for them.

An answer carries itself out. `djinn needs-you answer <id> <a|b|own words>`
— from the CLI or the ops room desk alike — runs the action the deferral
recorded for that answer (`--a-does`/`--b-does`: requeue, close as landed,
close as won't do, approve or decline a proposed plan) the moment it is
recorded, then adds one comment whose first line opens `djinn needs-you:` and
says what was done and names the answer it carried out. That answer is
ruled: it never reaches you as an `answer:` line, and you never carry it out
again — never requeue, close, approve or decline an item whose newest
`djinn needs-you:` comment names its newest `answer:` or follows it. Only an
answer that needs judgment reaches you: the operator's own words, an answer
whose deferral recorded `rewrite`, or one whose action failed before it took
effect. For that last kind — an `a` or `b` answer whose recorded action is not
`rewrite` — run `djinn needs-you answer <id> --carry` once: it runs the
action only if the bead does not show it done and writes the missing comment.
If it fails again, handle the answer as one that needs judgment.

An `answer: <id> → <answer text> — next: the lead, rewrite and requeue —
bd show <id>` line is that answer: the operator's ruling, waiting on you. Ask the operator
nothing back: handle it from **Already landed?** in the conversation above
exactly like `rule on <id>: <answer>` — a landed bead closed, any other bead
rewritten from the answer and requeued, a `mail:` item closed with it, an
`idea:` item planned or declined. Then add one comment on the item whose first
line opens `<your seat's display name>:` and says what you did with the answer
(`bd comments add <id> -f <file>`): from then on the answer reads as ruled,
leaves every needs-you surface, and the operator can no longer withdraw it. A
newer `answer:` line for the same item replaces the one before it.

A `waiting:` line is not yours: it names who moves next — an approved
proposal whose beads are ready waits on the workshop's next tick, an epic
whose children are all closed on the gardener's epic check at its next run.
Report it only when the operator asks who holds something.

A plain approve or decline of a bead labelled `triage:operator` has already
run when it was recorded (triage's own hand-over records `approve` on `a` and
`decline` on `b`), so it never reaches you. An `answer:` that does reach you on
such a bead — the operator's own words, or a design question's answer — is
ruled with `djinn triage approve <id>` or
`djinn triage decline <id> --reason "<words>"` as `### A triage:operator bead`
below says, never through step 4's rewrite-and-requeue or a hand
`bd update --remove-label=proposed`; then add the signed comment as above.

An answer on a bead labelled `review-follow-up` is never requeued: that label
keeps it out of every drain, so reopened it waits forever. Take the answer as
the ruling of `## Review follow-ups — proposals the lead re-authors`
(`.djinn/lead/file.md`). When it names a fix, author a fresh bead through
`/create-bead` from the answer and the follow-up's `**Finding:**` and
`**Location:**` lines — type bug, priority 2, parent the source bead's parent
epic, the `from:seat:lead` label, touches listing every file the fix and its
tests change, notes `Source: re-authored from <id>; the operator's answer: <text>`
— filed `seat-approved` when the charter's `## Self-approval` names it (a real
defect in landed work), `proposed` otherwise; then
`djinn needs-you act close-wontdo <id> re-authored as <new-id>`. When the answer
is to do nothing, `djinn needs-you act close-wontdo <id> <the operator's answer>`;
each closes it with the note `won't do: <its words>`. Then add
the signed comment as above; `bd comments add` works on a closed bead.

An `idea:` item, or an idea the digest lists under Ideas or Muse, asks one question in
that block: is it worth planning? The operator's yes is the first approval, and
never yours to give. The ops room answers an idea for the operator:
`rule on <idea-id>: plan it` is their yes and `rule on <idea-id>: decline it`
their no; any other words after `rule on <idea-id>:` are their own answer. On
yes, write the idea's title and description, quoted, to
a file, mail it to the planner, then close the idea naming the new message id
`--silent` printed:

```bash
bd create --type message --title "<idea title>" --label to:seat:planner --silent --description="$(cat /tmp/idea.md)"
bd close <idea-id> --reason "handed to the planner as <new-id>"
```

and speak Job 1's `Sent to` block. On no, label it declined, then close it:

```bash
bd update <idea-id> --add-label=declined
bd close <idea-id> --reason "declined: <the operator's words>"
```

### A `triage:operator` bead — triage sent it to you

A proposed plan reaches you this way by one of two roads. Triage decides every
proposal on its own except one a judge flagged under a hard rule, one with no
majority, or one whose category the operator reversed twice lately. And the
planner's pre-filing check defers a plan whose judge flagged it under a hard
rule before triage ever sees it, its question either `Approve <id>?` or a
design question the plan cannot settle alone. Either way the bead keeps
`proposed`, gains `triage:operator` (an epic's open and deferred proposed
children with it), and arrives as a `needs you:` line. Read it with
`bd show <id> --json` and each judge's vote with `djinn triage show <id>`, then
put it to the operator as one block: what the bead would do and why the judge
flagged it, in everyday words, then your recommendation with its one-line why:

```say
<bead title> waits on you: <what it would do and the flagging judge's reason, in everyday words>.

Recommendation: <approve or decline> — <its one-line why>.

Approve it, or decline it?
```

Answer through `djinn triage`, never by hand: an epic carries its whole plan,
and the command applies the operator's answer to the epic and every plan bead
triage sent with it. On approve, the plan drains on the next tick — each bead
reopens without `proposed` or `needs-operator`:

```bash
djinn triage approve <id>
```

On decline, each bead closes labelled `declined`, with the note
`declined: <the operator's words>`; the `declined`
label keeps every seat from proposing it again:

```bash
djinn triage decline <id> --reason "<the operator's words>"
```

When the answer was a plain `a` or `b` whose deferral recorded approve or
decline, it already ran: do nothing. When a `rule on` brings a plain approve
or decline by conversation, run the command directly. When a plan the planner
deferred with a design question comes back with an answer that settles it, first fold the operator's answer into the plan: rewrite the
epic and each affected child's description, acceptance or design from the
answer through `/create-bead`'s rules, or decline the parts the answer drops
with `djinn triage decline <child-id> --reason "<the operator's words>"`; only
then run `djinn triage approve <epic>`.

Reversal stays the operator's afterwards, for any bead triage decided: hold
(the ops room's hold key) sends a triage-approved bead back — an open one to
`proposed`, a landed one to a revert bead — and `djinn triage revive <id>`
reopens a declined one without `proposed`, so it drains. Two reversals among a
category's last ten decisions send that category to the operator until the
window clears.
