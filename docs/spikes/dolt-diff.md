# Spike: can `dolt_diff_issues` replace `bd history` for the detail-panel timeline?

**Bead:** loom-rs5 · **Timebox:** ~10 min · **Date captured:** 2026-08-22
**Consuming surface under judgement:** `describeEvent` in `web/src/lib/beads.ts:160`,
which today renders a timeline line from an `events` row's `oldValue` / `newValue` /
`comment`.

**Verdict: REJECT** `dolt_diff_issues` as the source for the `describeEvent` timeline.
Keep `events`. Rationale in [Decision](#decision), resting on the captures below.

---

## How the captures were obtained

The Dolt server for this workspace was started the documented way:

```
$ bd dolt start -C ~/code/loom
Dolt server started (PID 67291, port 3311)
  Data: .beads/dolt
  Logs: .beads/dolt-server.log
```

Port and database were resolved the way `internal/loomtest/livedolt` resolves them —
`.beads/dolt-server.port` → `3311`, `.beads/metadata.json` → `"dolt_database": "loom"`.
No `mysql` client is installed on this machine and this `dolt` build has no
`sql-client` subcommand, so the SQL was issued with `dolt sql -q` run from
`.beads/dolt`, which proxies to the running server rather than opening the files
locally. Proof it is the live server and not a local read:

```
$ dolt sql -q "select @@version, @@port"
+-----------+--------+
| @@version | @@port |
+-----------+--------+
| 8.0.31    | 3311   |
+-----------+--------+
```

**Bead used for both captures:** `loom-8sm` — *"Graph: the blockers view replaces the
single canvas"*. Chosen because it is **closed** (closed work stays closed, so this
capture stays reproducible) and has a full lifecycle: create → update → claim → close.

All output below is real. Where a row was too wide to read, columns were selected or
truncated with `left(...)` / `length(...)` — every such trim is marked. **No row is
fabricated.**

---

## Surface A — `dolt_diff_issues`

```sql
SELECT d.diff_type, d.to_commit_date, d.from_status, d.to_status,
       l.committer, l.email, l.message
FROM loom.dolt_diff_issues d
JOIN loom.dolt_log l ON l.commit_hash = d.to_commit
WHERE coalesce(d.to_id, d.from_id) = 'loom-8sm'
ORDER BY d.to_commit_date;
```

```
+-----------+----------------------------+-------------+-------------+-----------+-------------+---------------------+
| diff_type | to_commit_date             | from_status | to_status   | committer | email       | message             |
+-----------+----------------------------+-------------+-------------+-----------+-------------+---------------------+
| added     | 2026-08-17 06:25:22.323000 | NULL        | open        | beads     | beads@local | bd: create loom-8sm |
| modified  | 2026-08-17 07:00:28.097000 | open        | open        | beads     | beads@local | bd: update loom-8sm |
| modified  | 2026-08-17 07:01:11.255000 | open        | in_progress | beads     | beads@local | bd: claim loom-8sm  |
| modified  | 2026-08-17 07:17:35.590000 | in_progress | closed      | beads     | beads@local | bd: close loom-8sm  |
+-----------+----------------------------+-------------+-------------+-----------+-------------+---------------------+
```

Four rows — exactly the four writes that ever touched this bead. Field-level
before/after, same query, other columns (AC bodies replaced by `length()` because they
are 1.5–2.2 KB each):

```
+----------------------------+----------------------------------+----------------------------------+-------------+-----------+---------------+-------------+
| to_commit_date             | from_title (trimmed)             | to_title (trimmed)               | from_ac_len | to_ac_len | from_assignee | to_assignee |
+----------------------------+----------------------------------+----------------------------------+-------------+-----------+---------------+-------------+
| 2026-08-17 06:25:22.323000 | NULL                             | Graph: a blockers view that s…   | NULL        | 1476      | NULL          | NULL        |
| 2026-08-17 07:00:28.097000 | Graph: a blockers view that s…   | Graph: the blockers view repl…   | 1476        | 2234      | NULL          | NULL        |
| 2026-08-17 07:01:11.255000 | Graph: the blockers view repl…   | Graph: the blockers view repl…   | 2234        | 2234      | NULL          | Nate Gross  |
| 2026-08-17 07:17:35.590000 | Graph: the blockers view repl…   | Graph: the blockers view repl…   | 2234        | 2234      | Nate Gross    | Nate Gross  |
+----------------------------+----------------------------------+----------------------------------+-------------+-----------+---------------+-------------+
```

(Titles truncated at 30 chars for width; the full values are the two title strings
named above.) `to_close_reason` on the last row carries
`shipped in c640811 — blockers view replaces the ca…`.

**Labels are not in this table.** They live in `dolt_diff_labels`, whose shape is
`to_issue_id / to_label / diff_type / to_commit_date` — a separate union:

```
+-----------+---------------+-------------+------------+----------+----------------------------+
| diff_type | from_issue_id | to_issue_id | from_label | to_label | to_commit_date             |
+-----------+---------------+-------------+------------+----------+----------------------------+
| added     | NULL          | loom-8sm    | NULL       | graph    | 2026-08-17 06:25:22.323000 |
| added     | NULL          | loom-8sm    | NULL       | web      | 2026-08-17 06:25:22.323000 |
+-----------+---------------+-------------+------------+----------+----------------------------+
```

---

## Surface B — `bd history loom-8sm`

```
$ bd history loom-8sm | head
📜 History for loom-8sm (415 entries)

ctqsnbr6 2026-08-23 00:05:49
  Author: beads
  ✓ loom-8sm: Graph: the blockers view replaces the single canvas [P2 - closed]

tbjt85ei 2026-08-23 00:05:49
  Author: beads
  ✓ loom-8sm: Graph: the blockers view replaces the single canvas [P2 - closed]

r9ssjc2b 2026-08-23 00:05:49
  Author: beads
  ✓ loom-8sm: Graph: the blockers view replaces the single canvas [P2 - closed]
```

…and the tail, 414 entries later:

```
$ bd history loom-8sm | tail -5
jmco2ri5 2026-08-17 06:25:22
  Author: beads
  ○ loom-8sm: Graph: a blockers view that shows only what actually blocks [P2 - open]
```

Counted, not eyeballed — 415 entries rendering **4 distinct lines**:

```
$ bd history loom-8sm | grep -E "^  " | grep -v "^  Author:" | sort | uniq -c | sort -rn
 321   ✓ loom-8sm: Graph: the blockers view replaces the single canvas [P2 - closed]
  82   ◐ loom-8sm: Graph: the blockers view replaces the single canvas [P2 - in_progress]
   6   ○ loom-8sm: Graph: the blockers view replaces the single canvas [P2 - open]
   6   ○ loom-8sm: Graph: a blockers view that shows only what actually blocks [P2 - open]
```

`bd history` lists **every Dolt commit in which the bead exists** — 415 of them, all but
4 made by unrelated writes to other beads — and re-renders the bead's *state at that
commit*. `--json` confirms the shape: each entry is `{CommitHash, Committer,
CommitDate, Issue{…full snapshot…}}`, with **no `from`/`to` pair anywhere** and
`"Committer": "beads"` on every entry.

---

## Side by side, on the three axes the bead asks about

| | `dolt_diff_issues` | `bd history` | Winner |
|---|---|---|---|
| **Field-level before/after** | `from_*` / `to_*` for all 54 issue columns. `open → in_progress`, title A → title B, `from_ac_len 1476 → to_ac_len 2234`, all in one row. 4 rows = 4 real changes. | None. A full post-state snapshot per commit; the reader must diff 415 snapshots to find the 4 changes. | **`dolt_diff_issues`** |
| **Timestamps** | `to_commit_date` — real commit time, microsecond precision (`07:01:11.255000`). | `CommitDate` — the same commit clock, but attached to 415 rows where only 4 mean anything. Times of unrelated writes (`2026-08-23 00:05:49`) sit at the top of a bead that was closed on 2026-08-17. | **`dolt_diff_issues`** |
| **Actor attribution** | None of its own. Requires the `dolt_log` join above, which returns `committer = beads`, `email = beads@local` for **every** write. The real actor survives only where it happens to be a *column* (`to_assignee → Nate Gross`). | Also `Author: beads` on every entry. | **Neither** — and `events` beats both (below) |

The third row is the one that decides this spike, so it is worth stating plainly: **the
actor is not recoverable from the Dolt commit graph.** Every write goes in as `beads /
beads@local`, whoever made it. The `events` table is the only place the human's name is
recorded:

```sql
SELECT event_type, actor, created_at, left(new_value,45) AS new_v, comment
FROM loom.events WHERE issue_id = 'loom-8sm' ORDER BY created_at;
```

```
+----------------+------------+---------------------+-----------------------------------------------+--------------------+
| event_type     | actor      | created_at          | new_v (trimmed)                               | comment            |
+----------------+------------+---------------------+-----------------------------------------------+--------------------+
| created        | Nate Gross | 2026-08-16 23:25:22 |                                               | NULL               |
| label_added    | Nate Gross | 2026-08-16 23:25:22 | NULL                                          | Added label: graph |
| label_added    | Nate Gross | 2026-08-16 23:25:22 | NULL                                          | Added label: web   |
| updated        | Nate Gross | 2026-08-17 00:00:28 | {"acceptance_criteria":"The graph route ren…  | NULL               |
| claimed        | Nate Gross | 2026-08-17 00:01:11 | {"assignee":"Nate Gross","status":"in_progr…  | NULL               |
| status_changed | Nate Gross | 2026-08-17 00:01:11 | {"status":"in_progress"}                      | NULL               |
| closed         | Nate Gross | 2026-08-17 00:17:35 | shipped in c640811 — blockers view replaces…  | NULL               |
+----------------+------------+---------------------+-----------------------------------------------+--------------------+
```

Seven semantic events against four diff rows: `created` + two `label_added` collapse
into commit 1, and `claimed` + `status_changed` collapse into commit 3. There is **no
commit-hash column on `events`** (`describe loom.events` → `id, issue_id, event_type,
actor, old_value, new_value, comment, created_at`), so there is no key on which to join
a diff row's before/after onto an event row and keep both halves.

Two further things the captures show, worth recording because they cut in opposite
directions:

- **`events` timestamps are the writing process's local wall clock.** `created_at
  00:01:11` for the same write that Dolt commits at `07:01:11` — a 7-hour offset, the
  exact hazard `AGENTS.md` already warns about under the live-test rules. `dolt_diff`
  timestamps are the honest ones.
- **The `events` snapshot is not a reliable "from" side.** `status_changed`'s
  `old_value` snapshot reads `"status": "in_progress"`, `"assignee": "Nate Gross"` —
  the values *after* the write, because `claimed` had already applied them inside the
  same transaction. Deriving before/after from that snapshot would render
  `in_progress → in_progress`. `describeEvent` avoids the trap today only because it
  renders the *patch* (`newValue`) and never reads the snapshot as a from-side
  (`web/src/lib/beads.ts:167-172`). `dolt_diff_issues` has no such defect: for that
  same write it reports `open → in_progress`.

Cost, measured (each figure includes ~0.1 s of `dolt` CLI startup, so treat the ratio,
not the absolute, as the signal):

```
select count(*) from loom.dolt_diff_issues where coalesce(to_id,from_id)='loom-8sm'  → 0.50s
select count(*) from loom.events           where issue_id='loom-8sm'                 → 0.15s
```

---

## Decision

**REJECT `dolt_diff_issues` as the source for `describeEvent`'s timeline. `events`
stays the source.**

The rationale is the third row of the table above. `dolt_diff_issues` wins the
before/after axis outright and wins timestamps too — but the join that would give it an
actor returns `beads / beads@local` on all four of `loom-8sm`'s writes, while `events`
names `Nate Gross` on all seven. A manager's cockpit timeline whose every line reads
"beads changed status" has lost the thing a timeline is for. That is not a gap a better
query closes: the name is absent from the commit graph, not merely awkward to reach.

Nor can the two be blended. `events` has no commit-hash column, and the collapse is
many-to-one in a way that is not reconstructible — commit 1 carries three events
(`created`, two `label_added`), commit 3 carries two (`claimed`, `status_changed`).
There is no key, and no derivable key, on which to graft `open → in_progress` onto the
row that knows who did it.

Set against that, what adoption would buy is small. The awkwardness that motivated the
spike — the snapshot in `oldValue`, the patch in `newValue`, the human text in
`comment` — costs `describeEvent` roughly 40 lines including its two helpers
(`web/src/lib/beads.ts:160-199`), and its patch-rendering branch
already produces a correct line (`status → in_progress`, `assignee → Nate Gross ·
status → in_progress`). It is ugly, not wrong.

Follow-on notes, deliberately not acted on in this spike:

1. **`bd history` is not a candidate for anything here.** 415 entries, 4 distinct
   states, one snapshot per unrelated commit. Whatever the timeline's source becomes,
   it is not this.
2. **`dolt_diff_issues` is the right tool for a different question** — "what exactly
   changed in this one write", including the long fields (`from_ac_len 1476 →
   to_ac_len 2234`). If Loom ever grows a per-write field-level detail expansion, this
   table is where it should read from, joined on `to_commit`, with the actor still
   coming from `events`.
3. **The `events` snapshot post-write hazard is real and undocumented.** Nothing reads
   the snapshot as a from-side today, and nothing should start without confirming the
   ordering per event type. Worth its own bead if anyone proposes to.
