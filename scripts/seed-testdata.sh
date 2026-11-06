#!/usr/bin/env bash
# Seed the loom test database with a realistic beads backlog.
# Mirrors the conventions in ~/Projects/djinn/core/skills/create-bead:
# epic-as-brief parents, phase-{n} / layer-{name} labels, review-follow-up,
# needs-operator, blocking + discovered-from deps, all statuses & priorities.
#
# Idempotency: run against a fresh `bd init` database only.
set -euo pipefail
cd "$(dirname "$0")/.."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

create() { # create <outvar> <args...> -> captures loom-<id>
  local _out
  _out=$(bd create "${@:2}" 2>&1) || { echo "CREATE FAILED: $_out" >&2; exit 1; }
  local _id
  _id=$(echo "$_out" | grep -oE 'loom-[a-z0-9.]+' | head -1)
  printf -v "$1" '%s' "$_id"
  echo "created $_id  ${_out#*— }" | head -1
}

# ─────────────────────────────────────────────────────────────────────
# Epic A — phase 1 foundations (epic-as-brief; mostly done)
# ─────────────────────────────────────────────────────────────────────
cat > "$TMP/epicA.md" <<'EOF'
Foundations for the Loom management GUI: config loading, dolt server registry, and the read-only backlog view.

**Diagnostic Question:** Can a manager see the entire backlog of a project, grouped and filterable, within 2 seconds of opening the app?

**Reference Standard:** Linear's issue list — density, keyboard navigation, instant filtering.

**Architecture:** Go backend reads dolt over the MySQL wire protocol; writes go through the bd CLI. Frontend is React + TypeScript + Vite with TanStack Query.

Source: docs/PLAN.md §Phase 1
EOF
create EPIC_A --title="[Foundations] Config, server registry, read-only backlog" \
  -t epic -p 1 --description="$(cat "$TMP/epicA.md")" \
  --acceptance="All phase-1 children closed
make dev serves the backlog view against the seeded test db" \
  --notes="Phase gate: phase-2 work must not start until this closes."
bd label add "$EPIC_A" phase-1 2>/dev/null || bd tag "$EPIC_A" phase-1

create A1 --title="[Foundations] Config loader with dolt server registry" \
  -t task -p 1 --parent="$EPIC_A" \
  --description="YAML config at ~/.config/loom/config.yaml listing dolt servers (name, host, port, local|remote) and per-project settings (git workdir, default summoner flags). The registry is what every read query and health chip hangs off." \
  --acceptance="Config parses the reference file in testdata/config.yaml
Missing file falls back to zero-project defaults without crashing
Unit test covers malformed YAML with a line-numbered error" \
  --design="Use gopkg.in/yaml.v3. Pattern: single Load(path) returning a validated Config struct." \
  --notes="Context: parent epic $EPIC_A carries the shared brief." \
  -e 4
bd tag "$A1" phase-1; bd tag "$A1" layer-service

create A2 --title="[Foundations] Dolt connection pool per configured server" \
  -t task -p 1 --parent="$EPIC_A" \
  --description="One sql.DB pool per configured dolt server, lazily opened, with USE <db> per project query. A dead remote server must degrade to an error chip, never block other projects." \
  --acceptance="Pool opens lazily on first query
Dead server returns a typed ErrServerDown within 2s
Integration test passes against the local loom db" \
  --design="go-sql-driver/mysql; interpolateParams=true. See internal/doltpool/." \
  --notes="Context: parent epic $EPIC_A carries the shared brief." \
  -e 6
bd tag "$A2" phase-1; bd tag "$A2" layer-service

create A3 --title="[Foundations] Backlog table view with status grouping" \
  -t feature -p 1 --parent="$EPIC_A" \
  --description="The dense Linear-style backlog table, grouped by status, with j/k row navigation. First user-visible surface." \
  --acceptance="Rows grouped by status with collapsible group headers
j/k moves selection, Enter opens detail panel
Priority renders as P0-P4 badge with per-priority color token
Remains scrollable and readable with 500 seeded issues
Empty project shows a designed empty state, not a blank table" \
  --design="web/src/views/Backlog.tsx; TanStack Query for fetch; virtualized list." \
  --notes="Context: parent epic $EPIC_A carries the shared brief." \
  -e 15
bd tag "$A3" phase-1; bd tag "$A3" layer-ui

create A4 --title="[Foundations] Filter bar: status, type, priority, label, free text" \
  -t feature -p 2 --parent="$EPIC_A" \
  --description="Fast filter chips over the backlog plus a raw bd-query escape hatch for power users." \
  --acceptance="Each chip filters the table within 100ms of click
Free-text matches title substring case-insensitively
Raw query box accepts bd query syntax and shows parse errors inline
Cleared filters restore the unfiltered view" \
  --design="web/src/components/FilterBar.tsx; server-side WHERE composition in internal/queries/." \
  --notes="Context: parent epic $EPIC_A carries the shared brief." \
  -e 10
bd tag "$A4" phase-1; bd tag "$A4" layer-ui

create A5 --title="[Foundations] Saved views persisted per project" \
  -t feature -p 2 --parent="$EPIC_A" \
  --description="Persist a named filter+grouping combination so the manager's grooming views survive restarts." \
  --acceptance="Save current filters under a name via toolbar button
Saved views listed in sidebar and applied on click
Deleting a view asks for confirmation
Views persist across app restart in local storage" \
  --design="web/src/stores/savedViews.ts" \
  --notes="Context: parent epic $EPIC_A carries the shared brief." \
  -e 8
bd tag "$A5" phase-1; bd tag "$A5" layer-ui

# ─────────────────────────────────────────────────────────────────────
# Epic B — phase 2 kanban + writes
# ─────────────────────────────────────────────────────────────────────
cat > "$TMP/epicB.md" <<'EOF'
Kanban board and the bd-CLI write path. Dragging a card is the manager's primary grooming gesture; every write must go through bd so invariants (status transitions, events, dolt commits) hold.

**Diagnostic Question:** Can a manager re-prioritize and stage a day of agent work without touching a terminal?

**Reference Standard:** Linear board drag semantics; Jira swimlanes (by epic).

Source: docs/PLAN.md §Phase 2
EOF
create EPIC_B --title="[Board] Kanban with bd-backed writes" \
  -t epic -p 1 --description="$(cat "$TMP/epicB.md")" \
  --acceptance="All phase-2 children closed
Drag between columns persists through bd and survives refresh" \
  --notes="Phase gate: blocked by the Foundations epic."
bd tag "$EPIC_B" phase-2
bd dep add "$EPIC_B" "$EPIC_A"

create B3 --title="[Board] bd exec write layer with typed verbs" \
  -t task -p 1 --parent="$EPIC_B" \
  --description="All mutations shell out to bd with the project's working directory: update status/priority/assignee, close with reason, reopen, comment, label add/remove. Never raw SQL for writes — bd owns invariants." \
  --acceptance="Each verb maps to one bd invocation with -C <workdir>
Non-zero bd exit surfaces stderr to the caller as a typed error
Unit tests mock the exec boundary and assert exact argv" \
  --design="internal/bdexec/; pattern: Runner interface for test mocking." \
  --notes="Context: parent epic $EPIC_B carries the shared brief." \
  -e 8
bd tag "$B3" phase-2; bd tag "$B3" layer-service

create B1 --title="[Board] Kanban board with status columns and WIP counts" \
  -t feature -p 1 --parent="$EPIC_B" \
  --description="Columns = bd statuses. Cards show id, title, type, priority, labels, dependency badges. Dep-blocked cards in todo must look visibly different from ready ones — bd ready is the truth." \
  --acceptance="One column per active status with WIP count in header
Card shows id, type icon, priority badge, label chips, blocked-by/blocks counts
Dep-blocked cards render dimmed with a blocked badge
Swimlane toggle by epic
Readable with 200 cards across 5 columns" \
  --design="web/src/views/Board.tsx; dnd-kit; readiness from ready_issues view." \
  --notes="Context: parent epic $EPIC_B carries the shared brief." \
  -e 15
bd tag "$B1" phase-2; bd tag "$B1" layer-ui
bd dep add "$B1" "$A3"

create B2 --title="[Board] Drag-and-drop status transitions via bd" \
  -t feature -p 1 --parent="$EPIC_B" \
  --description="Dropping a card in a new column runs the right bd verb (update -s, close, reopen) optimistically, rolling back on failure." \
  --acceptance="Drop into closed column runs bd close and prompts for a reason
Drop from closed to open runs bd reopen
Failed write rolls the card back and shows the bd stderr in a toast
Multi-select drag moves all selected cards" \
  --design="web/src/views/Board.tsx + internal/bdexec/" \
  --notes="Context: parent epic $EPIC_B carries the shared brief." \
  -e 10
bd tag "$B2" phase-2; bd tag "$B2" layer-ui
bd dep add "$B2" "$B1"; bd dep add "$B2" "$B3"

create B4 --title="[Board] Operator smoke-test of destructive write paths" \
  -t task -p 2 --parent="$EPIC_B" \
  --description="A human must exercise close/reopen/delete against a scratch project before the write layer ships — agents cannot verify destructive UX judgement calls unattended." \
  --acceptance="Operator confirms close-with-reason round-trips in the UI, verified by dogfooding
Operator confirms a failed write shows a readable error, verified by dogfooding" \
  --notes="Context: parent epic $EPIC_B carries the shared brief. Requires a human at the keyboard." \
  -e 5
bd tag "$B4" phase-2; bd tag "$B4" needs-operator
bd dep add "$B4" "$B2"

create B5 --title="[Board] Status dropdown loses selection on refetch" \
  -t bug -p 0 \
  --description="When the SSE invalidation lands mid-interaction, the inline status dropdown closes and drops the pending selection. Manager loses the edit — worst possible grooming UX." \
  --acceptance="Open dropdown survives a background refetch
Regression test simulates invalidation while the menu is open" \
  --design="Suspect: key on the row remounts on data change. web/src/components/StatusSelect.tsx" \
  -e 6
bd tag "$B5" layer-ui
bd update "$B5" -s in_progress -a "djinn-worker-2" >/dev/null

create B6 --title="[Board] Bulk-edit toolbar for multi-select" \
  -t feature -p 2 --parent="$EPIC_B" \
  --description="x toggles selection; a floating toolbar offers status/priority/label bulk actions over the selection." \
  --acceptance="x toggles selection on the focused row
Toolbar appears when selection is non-empty and shows count
Bulk status change issues one bd update with all ids
Escape clears the selection" \
  --notes="Context: parent epic $EPIC_B carries the shared brief." \
  -e 8
bd tag "$B6" phase-2; bd tag "$B6" layer-ui
bd update "$B6" -s blocked >/dev/null
bd note "$B6" "Blocked on design decision: does bulk close prompt for one shared reason or per-issue reasons?" >/dev/null

# ─────────────────────────────────────────────────────────────────────
# Epic C — phase 3 runs monitoring
# ─────────────────────────────────────────────────────────────────────
cat > "$TMP/epicC.md" <<'EOF'
Follow-along monitoring for summoner/djinn runs: live log tail, per-bead step timelines, cost accumulation, run history.

**Diagnostic Question:** What did last night's drain do, what did it cost, and where is it stuck right now?

Source: docs/PLAN.md §Phase 4
EOF
create EPIC_C --title="[Runs] Djinn run monitoring" \
  -t epic -p 2 --description="$(cat "$TMP/epicC.md")" \
  --acceptance="All phase-3 children closed
A run started outside the GUI still appears in the Runs view" \
  --notes="Phase gate: blocked by the Board epic."
bd tag "$EPIC_C" phase-3
bd dep add "$EPIC_C" "$EPIC_B"

create C1 --title="[Runs] SSE channel with dolt head-hash polling" \
  -t task -p 1 --parent="$EPIC_C" \
  --description="Single SSE endpoint carrying db-invalidation events (dolt_hashof_db per project, <2s staleness) and djinn run events." \
  --acceptance="Client receives invalidation within 2s of a bd write
Dead project server pauses its poller without killing the stream
Integration test drives a bd update and asserts the event arrives" \
  --design="internal/sse/; poll SELECT dolt_hashof_db() per project pool." \
  --notes="Context: parent epic $EPIC_C carries the shared brief." \
  -e 8
bd tag "$C1" phase-3; bd tag "$C1" layer-service
bd dep add "$C1" "$B3"

create C2 --title="[Runs] Historical cost analytics dashboard" \
  -t feature -p 3 --parent="$EPIC_C" \
  --description="Spend-over-time charts across runs. Deliberately iced until run history has accumulated real data." \
  --acceptance="Cost per run charted over the trailing 30 days
Per-bead cost drill-down from a run row" \
  --notes="Deferred until run-history persistence has a month of data." \
  -e 12
bd tag "$C2" phase-3; bd tag "$C2" layer-ui
bd update "$C2" -s deferred --defer "+30d" >/dev/null

create C3 --title="[Runs] Rotate and cap summoner log ingestion buffers" \
  -t chore -p 4 --parent="$EPIC_C" \
  --description="Tail buffers for summoner logs grow unbounded on long drains; cap at 10k lines with ring-buffer semantics." \
  --acceptance="Ring buffer capped at 10k lines, verified by unit test with a 20k-line fixture" \
  --notes="Context: parent epic $EPIC_C carries the shared brief." \
  -e 3
bd tag "$C3" phase-3; bd tag "$C3" layer-service

# ─────────────────────────────────────────────────────────────────────
# Standalone beads: conventions pin, spike, closed chore, review follow-up
# ─────────────────────────────────────────────────────────────────────
create PIN --title="[Meta] Loom project conventions" \
  -t decision -p 2 \
  --description="Living record of conventions: semantic labels (phase-{n}, layer-{name}, review-follow-up, needs-operator), theme tokens only (no hex in components), writes through bd only, reads through dolt SQL only." \
  --acceptance="Conventions doc stays current as decisions land" \
  --notes="Pinned so it never leaves the backlog."
bd update "$PIN" -s pinned >/dev/null

create SPIKE --title="[Meta] Spike: dolt_diff tables for issue history rendering" \
  -t spike -p 2 \
  --description="Timeboxed: can dolt_diff_issues replace bd history for the detail panel timeline? Answer determines whether history rendering needs bd at all." \
  --acceptance="Written recommendation in docs/spikes/dolt-diff.md comparing dolt_diff_issues vs bd history output
Decision recorded: adopt or reject with rationale" \
  -e 10
bd tag "$SPIKE" layer-service

create CHORE_DONE --title="[Meta] Repo scaffolding: go.mod, Makefile, vite workspace" \
  -t chore -p 1 \
  --description="Initial repo layout so every later bead lands in a runnable tree." \
  --acceptance="make dev boots backend and frontend
make build produces a single binary" \
  -e 5
bd close "$CHORE_DONE" --reason "Scaffolding landed; make dev and make build verified." >/dev/null

# review follow-up discovered while doing A2
create FOLLOW --title="[Foundations] Pool: add exponential backoff on reconnect" \
  -t task -p 3 \
  --description="Review of the connection pool flagged hot-looping reconnects against a dead server. Add jittered exponential backoff." \
  --acceptance="Reconnect attempts back off 1s→30s with jitter, verified by unit test with a fake clock" \
  --notes="Filed from review of the dolt connection pool bead." \
  -e 4
bd tag "$FOLLOW" review-follow-up; bd tag "$FOLLOW" layer-service
bd dep add "$FOLLOW" "$A2" -t discovered-from

# ─────────────────────────────────────────────────────────────────────
# Status/assignee/comment dressing
# ─────────────────────────────────────────────────────────────────────
bd close "$A1" --reason "Config loader merged with table-driven tests." >/dev/null
bd close "$A2" --reason "Pool merged; integration test green against local db." >/dev/null
bd update "$A3" -s in_progress -a "djinn-worker-1" >/dev/null
bd comment "$A3" "Claimed. Virtualization via @tanstack/react-virtual; group headers sticky." >/dev/null
bd comment "$A3" "Status grouping done; keyboard nav next." >/dev/null
bd comment "$B5" "Reproduced: remount caused by unstable row key. Fix in progress." >/dev/null
bd comment "$EPIC_B" "Board drag semantics reviewed against Linear; adopting their column-drop affordance." >/dev/null
bd update "$A4" -a "nate" >/dev/null
bd update "$B5" --due "+2d" >/dev/null

# operational state label on the bug (evidence trail convention)
bd set-state "$B5" evidence_status=fail --reason "Verify-acceptance found the regression test missing." >/dev/null 2>&1 || true

# ─────────────────────────────────────────────────────────────────────
# A year of weekly initiative updates for the Foundations epic, so the
# Initiatives Updates column and its sheet have something to render at the
# volume they must stay readable at.
# ─────────────────────────────────────────────────────────────────────
./scripts/seed-updates.sh . "$EPIC_A" 52 "$A1" "$A3"

echo
echo "Seed complete."
bd status 2>/dev/null || true
