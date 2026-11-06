#!/usr/bin/env bash
# Reject test for `make check-secrets`: runs the same gitleaks command the
# target runs, with this repo's .gitleaks.toml, against two throwaway git
# repos. A repo whose history holds an AWS access key id must fail and name the
# file; a clean repo must pass. A gitleaks that cannot fail guards nothing.
#
# The Makefile owns the pinned version and passes it in:
#   GITLEAKS_VERSION=vX.Y.Z ./scripts/check-secrets_test.sh
#
# The fake key is assembled at run time so the real scan of this repo does not
# flag this file. It must not contain EXAMPLE: gitleaks' default allowlist
# skips keys that do.
set -euo pipefail

: "${GITLEAKS_VERSION:?set GITLEAKS_VERSION (make check-secrets passes the Makefile pin)}"
GO=${GO:-go}
config=$(cd "$(dirname "$0")/.." && pwd)/.gitleaks.toml
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
failures=0
n=0

new_repo() {
  n=$((n + 1))
  repo=$root/case$n
  mkdir -p "$repo"
  git -C "$repo" init -q
}

commit_all() {
  git -C "$repo" add -A
  git -C "$repo" -c user.name=check-secrets -c user.email=check-secrets@example.invalid \
    -c commit.gpgsign=false commit -q -m "$1"
}

run_case() {
  local name=$1 want=$2 want_out=$3 rc=0 out ok=1
  out=$(cd "$repo" && "$GO" run "github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}" \
    detect --no-banner --verbose --redact --source . --config "$config" 2>&1) || rc=$?
  case $want in
    pass) [ "$rc" -eq 0 ] || ok=0 ;;
    fail) [ "$rc" -ne 0 ] || ok=0 ;;
  esac
  if [ -n "$want_out" ] && [[ "$out" != *"$want_out"* ]]; then ok=0; fi
  if [ "$ok" -eq 1 ]; then
    echo "ok   $name (gitleaks exit $rc)"
  else
    echo "FAIL $name: want $want with '$want_out', got exit $rc:" >&2
    printf '%s\n' "$out" | sed 's/^/    /' >&2
    failures=$((failures + 1))
  fi
}

fake_key="AK""IA""QX7RZ2M4KT9WB3VN"

new_repo
printf 'aws_access_key_id = %s\n' "$fake_key" >"$repo/deploy.env"
commit_all "add deploy settings"
run_case "committed AWS access key id fails" fail "deploy.env"

new_repo
printf 'aws_access_key_id = %s\n' "$fake_key" >"$repo/deploy.env"
commit_all "add deploy settings"
git -C "$repo" rm -q deploy.env
commit_all "remove deploy settings"
run_case "key deleted in a later commit still fails" fail "deploy.env"

new_repo
printf 'nothing secret here\n' >"$repo/README.md"
commit_all "add readme"
run_case "clean repo passes" pass "no leaks found"

if [ "$failures" -gt 0 ]; then
  echo "check-secrets_test: $failures case(s) failed" >&2
  exit 1
fi
echo "check-secrets_test: all $n cases passed (gitleaks ${GITLEAKS_VERSION})"
