# Loom — Architecture

Loom is a manager's cockpit for beads (`bd`), the dependency-aware issue tracker backed
by Dolt. Agents (djinn workers) are the primary *consumers* of beads; Loom is for the
*human*: see the backlog, groom/prioritize, dispatch work to agents (summoner), and
follow along while they work.

## Verified ground truth (do NOT re-derive; this was inspected 2026-08-02, re-verified against bd 1.3.2 on 2026-10-31)

### bd (v1.3.2)

- **Statuses:** `open`, `in_progress`, `blocked`, `deferred`, `closed`, `pinned`, `hooked`.
  Categories: active / wip / done / frozen. Custom statuses live in table `custom_statuses`.
- **Types:** `task` (default), `bug`, `feature`, `chore`, `epic`, `decision`, `spike`,
  `story`, `milestone` + per-project custom types (table `custom_types`).
- **Priority:** int 0–4, 0 = highest. Render as P0…P4.
- **CRITICAL semantic:** dependency-blocked issues keep stored status `open`. The stored
  `blocked` status is a *manual* status. Readiness truth = the `ready_issues` SQL view
  (same semantics as `bd ready`). A card in todo whose deps are unmet is NOT ready.
- **Write verbs** (all writes go through `bd`, never raw SQL):
  - `bd update <id> -s <status> | -p <prio> | -a <assignee> | --title | -d | --design | --acceptance | --notes | --add-label | --remove-label | --defer <date> | --due <date> | --claim`
  - `bd close <id...> --reason "..."` / `bd reopen <id...>`. bd 1.3.2 refuses to close an
    issue whose assignee is not the acting user ("reclaim or use --force to override");
    Loom answers that refusal with a 409 naming the assignee and adds `--force` only
    after the user confirms "Close anyway?".
  - `bd comment <id> "text"` (or `--stdin`), `bd note <id> "text"` (appends to notes)
  - `bd dep add <Y> <X>` = "Y is blocked by X". `-t` type: `blocks|parent-child|discovered-from|related|…`. `bd dep remove`.
  - `bd create --title=… -t type -p N --description=… --acceptance=… --design=… --notes=… --parent=<id> -e <minutes>` (+ `bd tag <id> <label>` per label)
  - `bd set-state <id> <dim>=<val> --reason "…"` — creates an *event child bead* + label `<dim>:<val>`.
  - Global flags: `-C <dir>` (run in project workdir — Loom uses this everywhere), `--json` on most reads.
- `bd stale` — issues not updated recently; `bd history <id>` — version history;
  `bd status` — DB overview.
- `bd query "<expr>"` — query language: `field=value`, `!=,>,>=,<,<=`, AND/OR/NOT/(),
  fields: status, priority, type, assignee, owner, label, title, description, notes,
  created, updated, started, closed, id (wildcards), parent, pinned…; relative dates `7d`, `24h`.

### Dolt server topology (VERIFIED — differs from folklore)

- bd v1.3.2 in server mode runs **per-project** dolt sql-servers, auto-started, on **dynamic ports**.
  The port lives in `<workdir>/.beads/dolt-server.port` (primary source; the
  `dolt_server_port` key in metadata.json is deprecated). Example: two workspaces
  `app` and `api` get ports such as 3310 and 3309 and databases `app` and `api`;
  this repo → `.beads/dolt-server.port`, db `loom`.
- `~/.beads/shared-server/` can exist with a port file while its server is down.
  Loom must treat "server down" as a first-class, per-project degraded state.
- Database name = `dolt_database` in `<workdir>/.beads/metadata.json`.
- Connection: MySQL wire protocol, user `root`, empty password, no TLS.
- **Server resolution order for a local project:** read `<workdir>/.beads/dolt-server.port`
  + `<workdir>/.beads/metadata.json` at query time (ports change across restarts!);
  config-file `host:port` is only for `remote` servers. If the port file is missing or
  the dial fails, the project is `unreachable` — show the health chip, never block other
  projects. (Auto-starting a server via `bd dolt start -C <dir>` is offered as an
  explicit user action, not automatic.)
- **Embedded Dolt** (bd 1.3.2's `bd init` default): when metadata declares
  `"dolt_mode":"embedded"`, Loom reads `.beads/embeddeddolt/<db>` directly with
  `github.com/dolthub/driver`, in-process, with no server and no conversion step.
  This check runs before the port file is read, so a stale port file is ignored.
  Each `registry.Do` call opens the database read-only and closes the database
  and the connector before it returns; no handle outlives a request.
  Read-only comes from a shared flock on `.dolt/noms/LOCK`: dolt then cannot take
  its exclusive lock and opens the store in read-only mode. bd waits for that
  lock and does not fail. Loom admits one open at a time per store, with a 64ms
  gap between opens, so a waiting bd always gets the lock. Loom waits for its
  shared lock in a blocking flock (one per lock file), so the kernel grants it
  the moment bd releases; a polling reader missed the few-millisecond handoffs
  between queued bd writers and returned 500. An open that dolt
  reports as "locked by another dolt process" is retried at most 5 times (<2s).
  A read that still finds the store locked when the 1.5s open budget ends gets
  `registry.ErrStoreBusy`; the API answers it with **503 and `Retry-After: 1`**,
  never 500, and the project stays reachable. The web client retries 5xx.
  The SSE prober stats `.dolt/noms/manifest` (bd rewrites it on every write) and
  runs `dolt_hashof_db()` only when the manifest changes. `bd sql` and `bd serve`
  refuse embedded mode, so the driver is the only read path. Writes stay with `bd`.

### Dolt schema (tables in each project db; verified via SHOW CREATE)

- `issues`: id (varchar PK, e.g. `loom-3ft.2`), title, description, design,
  acceptance_criteria, notes (all longtext), status, priority (int), issue_type,
  assignee, owner, estimated_minutes, created_at/updated_at/closed_at/started_at,
  close_reason, defer_until, due_at, pinned tinyint, ephemeral tinyint, is_template,
  metadata json, content_hash, … (event-bead columns: event_kind, actor, target, payload).
- `dependencies`: id uuid, issue_id, type (`blocks` default | `parent-child` |
  `discovered-from` | …), depends_on_issue_id (also wisp/external variants), created_at.
  Direction: `issue_id` depends on `depends_on_issue_id`.
- `labels`: (issue_id, label) PK. `comments`: id, issue_id, author, text, created_at.
- `events`: id, issue_id, event_type, actor, old_value, new_value, comment, created_at —
  this is the history feed for the detail panel.
- **Views:** `ready_issues` (recursive CTE; open + not transitively blocked + not
  deferred + not ephemeral) and `blocked_issues`. USE THESE for readiness — free and
  identical to `bd ready` semantics.
- Hierarchy: child IDs are dotted (`loom-yqh.2`) AND `parent-child` rows exist in
  `dependencies`. Use the dependencies table as truth, not ID parsing.
- Change detection: `SELECT dolt_hashof_db()` per db — cheap; poll ≤2s for SSE.
- Wisp tables (`wisps`, `wisp_*`) are ephemeral infra — exclude everywhere by default.
  Exclude `ephemeral=1`, `is_template=1`, and event-kind rows from list views (bd list
  hides gate/agent/event infra beads; mirror that by excluding `issue_type` in
  (`gate`,`agent`,`role`,`message`,`event`) and `event_kind != ''`).

### Bead field conventions (djinn create-bead / plan-to-beads skills)

- Four prose fields, all markdown-rendered, shown DISTINCTLY: `description` (what+why),
  `acceptance_criteria` (**one criterion per non-blank line → render as checklist**),
  `design` (where to look; often file paths), `notes` (fit into larger plan).
- **Epic-as-brief:** parent epic's description carries the shared brief (Diagnostic
  Question, Reference Standard…). Child detail panel MUST surface parent title +
  one-click open; epic view shows children with status rollup.
- Dep types render differently: `blocks` = readiness-driving (badge + blocking chain),
  `parent-child` = hierarchy, `discovered-from` = provenance (audit trail, NOT a blocker).
- Labels are semantic: `phase-{n}` (grouping/swimlane dimension), `layer-{name}`
  (filter chip), `review-follow-up` (badge + filter in/out), `needs-operator`
  (LOUD warning in summoner dispatch — cannot finish unattended), `<dim>:<val>` state
  labels from `bd set-state` (e.g. `evidence_status:fail`) — surface on card/detail.

### summoner / djinn (verified --help)

- `summoner` flags: `--bead a,b` (exact-ID scope), `--epic <id>` (ready descendants,
  composes with --bead as intersection), `--dry-run` (prints ordered bead IDs that
  WOULD dispatch, no mutation), `--preflight` (per-bead LLM check for operator-required
  criteria; heavier; mutually exclusive with --dry-run), `--max-workers` (default 3),
  `--max-beads`, `--no-push`, `--poll-interval` (30s), `--max-retries`,
  `--log-dir` (default `.djinn/summoner-logs`).
- Summoner runs in the project's git workdir. It spawns djinn workers in temp worktrees.
- **Lifecycle log** `.djinn/summoner-logs/YYYY-MM-DD.log` — TSV:
  `RFC3339-local \t bead-id \t spawn` and
  `RFC3339 \t bead-id \t exit \t COMPLETE|FAILED \t closed|open|<halt reason>`.
- **Console log** `console-YYYY-MM-DD.log` — ANSI-colored stream; per-step lines like
  `✅ Done [verify-impl iter 2/3] (cost: $0.8135, ctx: 6%, dur=1m26.3s)` and an
  end-of-session block:
  ```
  ═══ Session Summary ═══════════════════════════
    iterations:     1
    closed:         0
    failures:       0
    total cost:     $5.74
    peak context:   0%
    ran:            9m47.9s
    halt:           iter 1: loop impl-loop exhausted …
    gate failures:  (none)
    sandbagging:    (no mutation data)
  ═══════════════════════════════════════════════
  ```
  plus `summoner: run summary — N bead(s) dispatched:` lines. Parse these into a
  structured run-summary card; keep raw log one click away. Strip/convert ANSI.
- **Per-bead forensics** `.djinn/state/<bead>/`: `claim.sha` file and
  `<step>.iter-N/<procedure>/` dirs. Step names observed: `ac-quality-loop`,
  `impl-loop`, `review-impl-loop`, `review-tests-loop`, `verify-loop` with inner
  procedure dirs (`ac-quality-review`, `review-impl`, `review-tests`, `verify-impl`).
  Formula order: ac-quality → implement → reviews → verify → commit-push.
- **Evidence YAML** `.djinn/evidence/<bead>.yml`: `bead`, `title`, `description?`,
  `criteria[]` with `text`, `status` (pass|partial|fail), `non_code?`, `reason?`,
  `implementation[]` (path:line), `verification`, `evidence.snapshots[]`,
  `evidence.observations[]`, `follow_up[]`. Render per-criterion in the detail panel.
- `.djinn/config.json` → `djinn.max_cost_usd_per_bead`, `djinn.max_cost_usd_per_session`
  (cost guardrails to chart against).
- No `.djinn/` dir ⇒ not a djinn project ⇒ hide run features for that project.

## Tech stack

- **Backend:** Go 1.24+, single module `github.com/kurofune/beads-loom`. Single binary;
  frontend embedded via `embed.FS`. Chi router (std-lib compatible), `go-sql-driver/mysql`,
  `dolthub/driver/v2` for embedded Dolt (cgo for zstd; ICU replaced by `third_party/go-icu-regex`).
- **Reads:** direct SQL against per-project dolt servers. One `*sql.DB` pool per
  reachable server; qualified queries (`USE db` per session is racy — instead qualify
  table names `db`.`issues` or open one pool per database DSN `.../dbname`). Pool per
  project database is simplest and fine at this scale.
- **Writes:** exec `bd` with `-C <project workdir>`; parse stdout/stderr; typed errors.
  If a write has no bd verb, fall back to SQL with a loud code comment.
- **Live updates:** SSE at `/api/events`. Backend polls `dolt_hashof_db()` per project
  (1s interval, skip unreachable) and emits `{type:"db-invalidated", project}` events;
  frontend (TanStack Query) invalidates queries. The same channel carries run events:
  `run-started`, `run-log`, `run-updated`, `run-finished`, `bead-state` (from watching
  `.djinn/state` + summoner logs via fsnotify).
- **Frontend:** React 18 + TypeScript + Vite, TanStack Query, dnd-kit, Tailwind CSS v4
  (CSS-variable theme tokens; no component framework). Dev: vite on 5173 proxying
  `/api` → Go on 7333. Prod: Go serves embedded `web/dist`.

## Repo layout

```
loom/                      # binary name
├── main.go                # flags, config load, server boot, bd version check
├── go.mod
├── Makefile               # dev / build / test / seed
├── internal/
│   ├── config/            # YAML config load/validate + defaults
│   ├── registry/          # project registry: server resolution (port file /
│   │                      #   metadata.json / remote), pools, health probes
│   ├── queries/           # read layer: issues, deps, labels, comments, events,
│   │                      #   ready set, stats, graph — all SQL
│   ├── bdexec/            # write layer: bd CLI runner (Runner interface for mocks)
│   ├── sse/               # event hub + dolt hash poller
│   ├── runs/              # summoner: dry-run, spawn/manage child, log tail+ANSI,
│   │                      #   log/state/evidence parsers, run history store
│   ├── integration/       # optional-integration seam: Provider, Capabilities,
│   │   │                  #   Deps, Set; shared JSON response helpers
│   │   └── djinn/         # the djinn provider: capabilities, /x/djinn routes,
│   │                      #   run watchers, summoner shutdown (only importer of runs
│   │                      #   besides main)
│   ├── httpapi/           # chi routes, JSON codecs
│   └── webembed/          # embed.FS of web/dist
├── web/                   # vite app
│   └── src/
│       ├── api/           # typed client + TanStack Query hooks + SSE wiring
│       ├── views/         # Backlog, Board, Issue detail, Runs, Now, Graph, Settings
│       ├── components/    # FilterBar, StatusSelect, Palette, …
│       ├── themes/        # tokens.css + loom-dark.css + loom-light.css (data-only)
│       └── keyboard/      # shortcut registry + cheat sheet
├── scripts/seed-testdata.sh
└── docs/ARCHITECTURE.md   # this file
```

## Config file

`~/.config/loom/config.yaml` (override with `--config`; `LOOM_CONFIG` env):

```yaml
listen: 127.0.0.1:7333
servers:                     # only needed for REMOTE servers; local projects
  - name: shared             #   auto-resolve via .beads/ files in workdir
    host: 127.0.0.1
    port: 3308
    user: root
    password: ""
    kind: remote             # local|remote — remote = fixed host:port from here
projects:                    # optional while discovery is enabled
  - name: myproject          # display name
    workdir: ~/code/myproject  # where .beads/ lives; ~ and relative paths expand
    # database: myproject    # default: dolt_database from .beads/metadata.json
    # server: shared         # set ⇒ use that configured server; unset ⇒ local auto-resolve
    integrations:
      djinn:                 # legacy spelling `summoner:` still decodes; this wins
        max_workers: 3
        max_beads: 0         # 0 = unbounded
        no_push: false
integrations:
  djinn:
    enabled: auto            # auto|true|false; false = no djinn routes, watchers, capabilities
discovery:                   # a file with no discovery: block has it off
  enabled: true
  roots:                     # required when enabled; ~ and "." are expanded
    - "."
    - "~"
  depth: 3                   # max dir depth below each root (1..10, default 3)
  exclude:                   # extra glob patterns; node_modules/vendor/dist/
    - "archive"              #   build/target/.Trash and dot-dirs always skipped
  interval: 5m               # rescan cadence (min 30s, default 5m)
```

The shipped default config (`DefaultYAML`, used when no file exists and written by
`--write-default-config`) lists no projects and enables discovery over `"."` (the
process working directory, resolved at load) and `"~"` at depth 3, so `loom` run
inside a `bd init` workspace shows that workspace with no config file.

### Project auto-discovery

When `discovery.enabled`, Loom scans the roots for directories containing
`.beads/metadata.json` (never descending into a found project), merges them into
the registry as `source: "discovered"` — explicit config wins on any name or
workdir collision — and rescans every `interval`. Membership changes are pushed
over SSE as `projects-changed`. Discovered projects can be hidden/unhidden from
Settings; the choice persists in `discovered-hidden.json` next to the config
file. Discovery never touches dolt: found projects resolve their server the
normal way (port file + metadata.json at query time). With discovery enabled,
`projects:` may be empty.

## HTTP API (all JSON; `{error: string}` + status code on failure)

```
GET  /api/projects                          → [{name, db, reachable, health:{ready,inProgress,blocked,total}, integrations:{<provider>:{capabilities[], reasons?{<capability>:why}}}, doltSync, source:"config"|"discovered"}]
GET  /api/projects/{p}/issues?<filters>       accepted keys: internal/httpapi/api.go `knownIssueParams`
     → {issues:[Issue], groups?} — Issue includes labels[], depCounts{blockedBy,blocks},
       ready:boolean (from ready_issues), parent{id,title}?, childStats{total,closed}?;
       epic scopes to transitive descendants and excludes the epic row
GET  /api/projects/{p}/issues/{id}          → full Issue + deps{blocks[],blockedBy[],children[],discoveredFrom[],parent}
                                              + comments[] + events[]
POST /api/projects/{p}/issues               → create (bd create + bd tag)
PATCH /api/projects/{p}/issues/{id}         → {status?|priority?|assignee?|title?|labelsAdd?|labelsRemove?|…} via bd update
POST /api/projects/{p}/issues/{id}/close    → {reason} ; /reopen ; /comments {text}
POST /api/projects/{p}/issues/bulk          → {ids[], patch} (single bd update id... invocation)
GET  /api/projects/{p}/graph?epic=          → {nodes[], edges[{from,to,type}]} for dep graph
GET  /api/projects/{p}/stats                → open/closed trend (events table), ready count, blocked count
GET  /api/projects/{p}/query?q=<raw>        → bd query passthrough (`bd query <q> --json`)
GET  /api/projects/{p}/initiatives?includeClosed= → {initiatives:[Initiative]} with full-subtree rollups
GET  /api/now                               → cross-project in_progress [{project, issue, assignee, inStatusSince, stale:boolean, runRef?}]
GET  /api/events                            → SSE stream (db-invalidated | project-health | projects-changed | run-* | bead-state)

# discovery (POSTs 404 when discovery is disabled; GET always answers)
GET  /api/discovery                         → {enabled, roots[], depth, exclude[], intervalSeconds, lastScanAt|null, hidden:[{workdir,name}]}
POST /api/discovery/hide                    → {workdir} → refreshed discovery status (200)
POST /api/discovery/unhide                  → {workdir} → refreshed discovery status (200)
POST /api/discovery/rescan                  → {} → {added:[names], removed:[names]}

# integrations: /api/projects/{p}/x/{provider}/…, inside the same middleware
# chain as core; 404 unless the project's integrations list the provider
# djinn (internal/integration/djinn; 404 without .djinn or with enabled: false).
GET  /api/projects/{p}/x/djinn/runs                 → history (parsed per-day logs + persisted GUI-spawned runs)
GET  /api/projects/{p}/x/djinn/runs/{runId}         → summary card + worker slots + per-bead timeline
GET  /api/projects/{p}/x/djinn/runs/{runId}/log?offset=  → raw log chunk (ANSI-stripped; ?ansi=1 raw)
POST /api/projects/{p}/x/djinn/summon/preview       → {beads[]?, epic?} runs summoner --dry-run (+optional --preflight) → {ordered[], warnings[], needsOperator[]}
POST /api/projects/{p}/x/djinn/summon               → {beads[], epic?, maxWorkers, maxBeads, noPush} → spawns managed summoner, returns runId  (NEVER without explicit user confirm in UI)
POST /api/projects/{p}/x/djinn/runs/{runId}/stop    → graceful SIGTERM
GET  /api/projects/{p}/x/djinn/beads/{id}/forensics → parsed .djinn/state/<bead>/ timeline
GET  /api/projects/{p}/x/djinn/beads/{id}/evidence  → {evidence: parsed .djinn/evidence/<id>.yml | null}
GET  /api/projects/{p}/x/djinn/spend                → {last7dUsd: number | null}
POST /api/projects/{p}/dolt/{push|pull}     → bd dolt push/pull
```

Readiness batching: the issues list query LEFT JOINs `ready_issues` in one statement —
never N+1. Dep counts via grouped subselects in the same round trip.

## Weekly initiative updates

Loom renders weekly updates that an agent or a person stores in bd's key-value
store; it drafts none. `internal/updates` is pure logic over values read through
`bd kv list --json` (`internal/bdexec/kv.go`); `internal/httpapi/updates.go`
serves them. bd 1.3.2 stores each pair as a `kv.<key>` row in the project's Dolt
`config` table, but Loom reads and writes only through `bd kv`.

- **Key:** `initiative-retro.<epic-id>.<YYYY-Www>` (`updates.KeyPrefix`,
  `updates.Key`). The ISO week is the last dot-segment, so dotted child epic ids
  split correctly; a key whose last segment is not `YYYY-Www` is skipped.
- **Value** (`storedUpdate`): JSON with `schema` (number, `1`), `status`
  (`draft` | `published`), `generated_at` (RFC 3339) and `body` (markdown).
  Decoding is lenient: unknown fields and schema numbers are kept. A value that
  is not JSON still yields a row with `parseError`, never a missing week.
- **`bodySha`:** hex sha256 of `body`, computed by Loom on read. Publish takes it
  back as the guard: the stored body must still hash to the reviewed one, or
  publish answers `409`. Publish also refuses a published update (`409`) and a
  body with an uncited list item (`updates.ValidateCitations`), then rewrites the
  value with `status: "published"` through `bd kv set`.
- **Routes:** `GET /api/projects/{p}/initiative-updates` → `{updates:[Summary]}`
  (one `bd kv list` for the whole table); `GET /initiatives/{epic}/updates` →
  `{updates:[Update], knownIssueIds, knownRunIds}`;
  `POST /initiatives/{epic}/updates/{week}/publish` `{bodySha}`.
- **UI:** the Initiatives grid shows its Updates column only when the summaries
  response holds at least one epic; a failed summaries read hides the column and
  leaves the grid.

Write one by hand:

```bash
bd kv set initiative-retro.bd-a3f8.2026-W44 '{"schema":1,"status":"draft","generated_at":"2026-10-31T17:00:00Z","body":"## Shipped\n- First slice merged (bd-a3f8.1, 4c1e9f2)\n"}'
```

## Theming

Semantic CSS custom properties only — components NEVER use raw hex:

```
--surface, --surface-raised, --surface-overlay, --border, --border-focus
--text, --text-muted, --text-faint, --accent, --accent-text, --danger, --warn, --ok
--status-open, --status-in-progress, --status-blocked, --status-deferred,
--status-closed, --status-pinned, --status-hooked
--priority-p0 … --priority-p4
--font-mono (ids, costs), radii/spacing scale
```

Two shipped themes, both WCAG AA for status/priority-on-surface: `loom-dark` (default;
focused ops console — near-black surfaces, restrained saturated accents) and
`loom-light` (paper-like, warm white, ink text). Theme = one CSS file assigning tokens
under `[data-theme="…"]`; adding a third theme is data-only. Selection in Settings,
persisted to localStorage, `prefers-color-scheme` as first-run default.

## Keyboard model (Linear-inspired)

`j/k` move · `x` select · `Enter` open · `Esc` close/clear · `c` quick-create ·
`s` status · `p` priority · `a` assignee · `l` labels · `Cmd+K` palette ·
`g b` board · `g l` backlog · `g r` runs · `g n` now · `?` cheat sheet.
Single registry in `web/src/keyboard/`; every shortcut also reachable by mouse.

## Phase plan (runnable at the end of every phase)

1. **Read-only foundation** — config, registry+health, queries, backlog view, detail
   panel (4 fields, checklist ACs, parent brief, deps typed), theme scaffold, Makefile.
2. **Writes** — bdexec, kanban board (dnd, blocked-distinct, WIP, swimlanes), inline
   edits, bulk actions, quick create, close/reopen/comment.
3. **Multi-project + live** — sidebar switcher + health chips, SSE invalidation,
   graceful dead-server degradation, dolt sync status.
4. **Summoner + runs** — staging tray, dry-run preview + needs-operator warnings,
   confirm with knobs, managed child process, live dashboard, log tail, forensics
   timeline, evidence render, run history, external-run detection.
5. **Extras** — Now view, dependency graph, command palette, saved views, stats strip,
   `bd query` escape hatch, keyboard everywhere + cheat sheet, staleness warnings.
6. **Polish** — both themes finalized (AA-checked), empty/error states designed,
   README + screenshots, version-mismatch banner, tests green.

## Quality bar

- Go tests: table-driven for config/queries/parsers; bdexec mocked at Runner interface;
  integration tests against the seeded `loom` db (skip cleanly when server down).
- Frontend: vitest for stores/parsers where logic warrants; no snapshot noise.
- `make dev` (Go + vite, hot reload), `make build` (vite build → embed → single binary),
  `make test`, `make seed`.
- Never hardcode ports/paths from this doc in code — read port files / metadata at runtime.
- bd version printed at startup; warn (don't die) on unknown version.
