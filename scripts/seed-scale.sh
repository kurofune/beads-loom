#!/usr/bin/env bash
# Seed a throwaway Loom scale project with 30 epics and 6,000 descendants.
# The common case is a 200-descendant, two-level subtree; three deliberately
# childless epics exercise the zero state while three larger subtrees keep the
# import at exactly 6,030 issues.
#
# This script never discovers or writes this repository's own .beads database.
set -euo pipefail

SCALE_PARENT=$(mktemp -d "${TMPDIR:-/tmp}/loom-scale.XXXXXX")
SCALE_DIR="$SCALE_PARENT/.scale"
JSONL="$SCALE_PARENT/scale.jsonl"

mkdir -p "$SCALE_DIR"
git init -q "$SCALE_DIR"

# `bd init` always initializes the CURRENT directory — its global -C flag
# targets an *existing* beads project, so it cannot bootstrap one. The cd is
# confined to a subshell so the rest of the script keeps this repo's cwd.
#
# --server is required, not cosmetic: bd defaults to an embedded Dolt engine,
# but Loom's registry dials a dolt sql-server over the MySQL wire protocol, so
# an embedded project is invisible to Loom (and `bd dolt start` refuses to run
# against it).
(cd "$SCALE_DIR" && bd init \
  --non-interactive \
  --server \
  --skip-agents \
  --skip-hooks \
  --prefix scale)

# 30 epics + 6,000 descendants. Epics 1-3 have 400 descendants, 4-27
# have 200, and 28-30 have none. Every non-empty subtree has direct children
# plus nine grandchildren per direct child.
awk '
function emit_epic(e, id, status, due, defer, assignee, updated) {
  id = sprintf("scale-%03d", e)
  status = (e % 11 == 0 ? "closed" : (e % 7 == 0 ? "deferred" : "open"))
  due = (e % 5 == 0 ? "" : sprintf(",\"due_at\":\"2026-09-%02dT00:00:00Z\"", (e % 28) + 1))
  defer = (status == "deferred" ? ",\"defer_until\":\"2030-01-01T00:00:00Z\"" : "")
  assignee = (e % 4 == 0 ? "" : sprintf("lead-%d", e % 4))
  updated = sprintf("2026-08-%02dT%02d:00:00Z", (e % 28) + 1, e % 24)
  printf("{\"id\":\"%s\",\"title\":\"Scale initiative %02d\",\"description\":\"Two-level scale fixture for the Initiatives view.\",\"issue_type\":\"epic\",\"status\":\"%s\",\"priority\":%d,\"assignee\":\"%s\",\"created_at\":\"2026-07-01T00:00:00Z\",\"updated_at\":\"%s\",\"labels\":[\"scale\",\"portfolio-%d\"]%s%s}\n", id, e, status, e % 5, assignee, updated, e % 3, due, defer)
}

function emit_direct(e, c, epic, id, status, deps, due, assignee, updated, blocker) {
  id = sprintf("%s.%03d", epic, c)
  status = (c % 5 == 0 ? "closed" : (c % 11 == 0 ? "in_progress" : "open"))
  deps = sprintf("{\"issue_id\":\"%s\",\"depends_on_id\":\"%s\",\"type\":\"parent-child\"}", id, epic)
  if (c % 10 == 0) {
    blocker = sprintf("%s.%03d", epic, 1)
    deps = deps sprintf(",{\"issue_id\":\"%s\",\"depends_on_id\":\"%s\",\"type\":\"blocks\"}", id, blocker)
  }
  due = (c % 6 == 0 ? "" : sprintf(",\"due_at\":\"2026-10-%02dT00:00:00Z\"", (c % 28) + 1))
  assignee = (c % 4 == 0 ? "" : sprintf("worker-%d", c % 4))
  updated = sprintf("2026-08-%02dT%02d:00:00Z", ((e + c) % 28) + 1, c % 24)
  printf("{\"id\":\"%s\",\"title\":\"Initiative %02d workstream %02d\",\"issue_type\":\"feature\",\"status\":\"%s\",\"priority\":%d,\"assignee\":\"%s\",\"created_at\":\"2026-07-02T00:00:00Z\",\"updated_at\":\"%s\",\"labels\":[\"scale\",\"layer-%s\"],\"dependencies\":[%s]%s}\n", id, e, c, status, c % 5, assignee, updated, (c % 2 ? "ui" : "service"), deps, due)
}

function emit_grandchild(e, c, g, direct, id, status, deps, updated, blocker) {
  id = sprintf("%s.%03d", direct, g)
  status = (g == 3 || g == 7 ? "closed" : (g == 5 ? "in_progress" : "open"))
  deps = sprintf("{\"issue_id\":\"%s\",\"depends_on_id\":\"%s\",\"type\":\"parent-child\"}", id, direct)
  if (g == 9 && c > 1) {
    blocker = sprintf("scale-%03d.%03d.001", e, c - 1)
    deps = deps sprintf(",{\"issue_id\":\"%s\",\"depends_on_id\":\"%s\",\"type\":\"blocks\"}", id, blocker)
  }
  updated = sprintf("2026-08-%02dT%02d:%02d:00Z", ((e + c + g) % 28) + 1, (c + g) % 24, g * 5)
  printf("{\"id\":\"%s\",\"title\":\"Initiative %02d workstream %02d task %02d\",\"issue_type\":\"task\",\"status\":\"%s\",\"priority\":%d,\"assignee\":\"worker-%d\",\"created_at\":\"2026-07-03T00:00:00Z\",\"updated_at\":\"%s\",\"labels\":[\"scale\"],\"dependencies\":[%s]}\n", id, e, c, g, status, (c + g) % 5, (c + g) % 4, updated, deps)
}

BEGIN {
  for (e = 1; e <= 30; e++) {
    epic = sprintf("scale-%03d", e)
    emit_epic(e)
    direct_count = (e <= 3 ? 40 : (e <= 27 ? 20 : 0))
    for (c = 1; c <= direct_count; c++) {
      direct = sprintf("%s.%03d", epic, c)
      emit_direct(e, c, epic)
      for (g = 1; g <= 9; g++) emit_grandchild(e, c, g, direct)
    }
  }
}
' > "$JSONL"

LINES=$(wc -l < "$JSONL" | tr -d " ")
if [[ "$LINES" != "6030" ]]; then
  echo "scale generator produced $LINES issues; expected 6030" >&2
  exit 1
fi

# Import in chunks — never a 6,000-command bd create loop, but not one giant
# invocation either. bd recomputes is_blocked across the whole table at the end
# of every import, and that query runs against the dolt sql-server under a
# ~15s client read timeout. A single 6,030-issue import blows through it and
# rolls the entire batch back ("recompute is_blocked (mark): invalid
# connection"), leaving zero rows. Chunking keeps each recompute inside the
# timeout. The generator emits every parent before its children, so splitting
# the file in file order never orphans a parent-child dependency.
CHUNK_DIR="$SCALE_PARENT/chunks"
mkdir -p "$CHUNK_DIR"
split -l 250 "$JSONL" "$CHUNK_DIR/part-"

for chunk in "$CHUNK_DIR"/part-*; do
  bd import "$chunk" -C "$SCALE_DIR" >/dev/null
  printf '.'
done
echo

bd dolt start -C "$SCALE_DIR" 2>/dev/null || true

DB_NAME=$(sed -n 's/.*"dolt_database"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$SCALE_DIR/.beads/metadata.json")
PORT=$(tr -d "[:space:]" < "$SCALE_DIR/.beads/dolt-server.port")

echo
echo "Scale project ready: $SCALE_DIR"
echo "Imported: $LINES issues (30 epics, 6,000 descendants)"
echo "Dolt: 127.0.0.1:$PORT / $DB_NAME"
echo
echo "Add this under projects: in ~/.config/loom/config.yaml:"
cat <<EOF
  - name: loom-scale
    workdir: $SCALE_DIR
    database: $DB_NAME
EOF
echo
echo "Tear down when finished:"
printf '  bd dolt stop -C %q\n' "$SCALE_DIR"
printf '  rm -rf %q\n' "$SCALE_PARENT"
