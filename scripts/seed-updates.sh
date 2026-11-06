#!/usr/bin/env bash
# Seed one epic's weekly initiative updates into a beads key-value store.
#
#   scripts/seed-updates.sh <workdir> <epic-id> [weeks] [child-id...]
#
# Writes `weeks` keys of the form initiative-retro.<epic-id>.<YYYY-Www>, newest
# 2026-W32 and walking backwards across the year boundary. The set is shaped to
# exercise every state the Updates sheet has to render:
#
#   - the newest week is a DRAFT with fully cited claims — the publish path
#   - week 2 is a DRAFT with one uncited claim   — the publish refusal path
#   - week 3 is a quiet week                     — no filler, still reconciled
#   - week 4 is a deliberately malformed value   — the parse-error row
#   - every other week is PUBLISHED
#
# Every cited week names two runs: $LOOM_SEED_RUN_ID, which the fixture's run
# history is expected to have, and one that no run history has. They render
# differently on purpose — the known one is a link to the run detail route, the
# unknown one stays mono text — so the sheet's run-citation path is visible
# without a real drain. Give the fixture project a .djinn directory and write a
# run record for $LOOM_SEED_RUN_ID under $LOOM_STATE_DIR to make the first one
# resolve.
#
# WARNING: this writes to the beads database in <workdir>. Point it at a seed or
# throwaway project, never at a real backlog whose kv namespace you care about.
set -euo pipefail

WORKDIR=${1:?usage: seed-updates.sh <workdir> <epic-id> [weeks] [child-id...]}
EPIC=${2:?usage: seed-updates.sh <workdir> <epic-id> [weeks] [child-id...]}
WEEKS=${3:-52}
shift 3 2>/dev/null || shift $#
CHILDREN=("$@")
if [ ${#CHILDREN[@]} -eq 0 ]; then
  CHILDREN=("$EPIC")
fi

# One python3 call builds every key and value. Markdown is never hand-quoted
# into the shell — json.dumps owns the escaping — and the pairs come back
# NUL-separated so a body containing newlines survives the read loop.
RUN_ID=${LOOM_SEED_RUN_ID:-run-1785600000000000000}
UNKNOWN_RUN_ID=${LOOM_SEED_UNKNOWN_RUN_ID:-run-1600000000000000000}

python3 - "$EPIC" "$WEEKS" "$RUN_ID" "$UNKNOWN_RUN_ID" "${CHILDREN[@]}" <<'PY' > "${TMPDIR:-/tmp}/loom-seed-updates.$$"
import json, sys

epic, weeks = sys.argv[1], int(sys.argv[2])
run_id, unknown_run_id = sys.argv[3], sys.argv[4]
children = sys.argv[5:]
first, second = children[0], children[1 % len(children)]

def week_labels(year, week, count):
    out = []
    for _ in range(count):
        out.append(f"{year}-W{week:02d}")
        week -= 1
        if week == 0:
            year, week = year - 1, 52
    return out

def cited_body(week, index, uncited=False):
    # A seven-hex-digit name with no a-f in it is indistinguishable from a
    # measurement, and the publish gate refuses it as a citation (citesASHA in
    # internal/updates). Fixing the leading nibbles keeps every generated sha
    # both letter-bearing and a pure function of the index.
    sha = f"b64f{index:03x}"
    extra = "- We tidied up a few things along the way\n" if uncited else ""
    return (
        f"# {epic} — weekly update — {week}\n\n"
        f"## Closed\n"
        f"- {first} — reconciliation sweep — shipped in {sha}, verified by {run_id} ({first})\n"
        f"{extra}\n"
        f"## Started\n"
        f"- {second} — store and render weekly initiative updates ({second})\n\n"
        f"## Discovered\n"
        f"- {first} — follow-up filed — discovered from {epic} during {unknown_run_id}\n\n"
        f"## Reconciliation vs the prior week\n"
        f"- {second} — listed in flight last week; still in flight ({second}).\n"
    )

def quiet_body(week):
    return (
        f"# {epic} — weekly update — {week}\n\n"
        f"Quiet week for {epic}: no beads closed, started, or discovered.\n\n"
        f"## Reconciliation vs the prior week\n"
        f"- {second} — listed in flight last week; still in flight ({second}).\n"
    )

pairs = []
for index, week in enumerate(week_labels(2026, 32, weeks)):
    key = f"initiative-retro.{epic}.{week}"
    if index == 3:
        # A value bd stores happily and Loom cannot decode.
        pairs.append((key, '{"schema":1,"epic":"' + epic + '","week":"' + week + '","body":'))
        continue
    if index == 2:
        body, status = quiet_body(week), "published"
    elif index == 1:
        body, status = cited_body(week, index, uncited=True), "draft"
    elif index == 0:
        body, status = cited_body(week, index), "draft"
    else:
        body, status = cited_body(week, index), "published"
    pairs.append((key, json.dumps({
        "schema": 1, "epic": epic, "week": week, "status": status,
        "generated_at": f"2026-08-{max(1, 8 - index % 8):02d}T0{index % 10}:17:20Z",
        "body": body,
    }, separators=(",", ":"))))

for key, value in pairs:
    sys.stdout.write(key + "\0" + value + "\0")
PY

PAIRS="${TMPDIR:-/tmp}/loom-seed-updates.$$"
trap 'rm -f "$PAIRS"' EXIT

count=0
while IFS= read -r -d '' key && IFS= read -r -d '' value; do
  bd -C "$WORKDIR" kv set "$key" "$value" >/dev/null
  count=$((count + 1))
done < "$PAIRS"

echo "seeded $count weekly updates for $EPIC in $WORKDIR"
