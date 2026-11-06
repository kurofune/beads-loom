#!/usr/bin/env bash
# Regression cases for scripts/check-public.sh. Each case builds a throwaway
# git repo holding a copy of the script, plants one tracked file, and asserts
# the exit code and the path:line the script names.
#
# The planted strings are split with '' so this file does not match itself.
set -euo pipefail

script=$(cd "$(dirname "$0")" && pwd)/check-public.sh
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
failures=0
n=0

new_repo() {
  n=$((n + 1))
  repo=$root/case$n
  mkdir -p "$repo/scripts"
  cp "$script" "$repo/scripts/check-public.sh"
  : >"$repo/scripts/check-public.allow"
  git -C "$repo" init -q
}

run_case() {
  local name=$1 want_rc=$2 want_out=$3 rc=0 out
  git -C "$repo" add -A
  out=$(cd "$repo" && ./scripts/check-public.sh 2>&1) || rc=$?
  if [ "$rc" -ne "$want_rc" ] || { [ -n "$want_out" ] && [[ "$out" != *"$want_out"* ]]; }; then
    echo "FAIL $name: want exit $want_rc with '$want_out', got exit $rc:" >&2
    printf '%s\n' "$out" | sed 's/^/    /' >&2
    failures=$((failures + 1))
  else
    echo "ok   $name"
  fi
}

new_repo
printf 'nothing here\n' >"$repo/clean.txt"
run_case "clean tree passes" 0 ""

new_repo
printf 'home is /Users''/me/code/loom\n' >"$repo/doc.md"
run_case "placeholder home passes" 0 ""

new_repo
printf 'line one\nsee /Users''/x/secret\n' >"$repo/doc.md"
run_case "plain home path fails" 1 "doc.md:2:"

new_repo
printf 'see /Users''/Jane Doe/secret\n' >"$repo/doc.md"
run_case "home name with a space fails" 1 "doc.md:1:"

new_repo
printf 'see /Users''/me /secret\n' >"$repo/doc.md"
run_case "near-placeholder home fails" 1 "doc.md:1:"

new_repo
printf '\x89PNG\0\0meta\nauthor /Users''/x/photos\0\n' >"$repo/shot.png"
run_case "binary-classified file fails" 1 "shot.png:2:"

new_repo
printf '*.dat binary\n' >"$repo/.gitattributes"
printf 'by nathan''gross\n' >"$repo/blob.dat"
run_case "file marked binary in gitattributes fails" 1 "blob.dat:1:"

new_repo
ln -s "/Users""/x/secret/config" "$repo/link"
run_case "absolute symlink target fails" 1 "link:1:/Users"

new_repo
ln -s "/Users""/x/secret/config" "$repo/café"
run_case "symlink with a non-ASCII name fails" 1 "café:1:/Users"

new_repo
ln -s "/Users""/x/secret/config" "$repo/quote\"link"
run_case "symlink with a quote in its name fails" 1 'quote"link:1:/Users'

new_repo
printf 'see /Users''/x/secret\n' >"$repo/café.md"
run_case "file with a non-ASCII name fails" 1 "café.md:1:"

new_repo
printf 'line one\nby nathan''gross\n' >"$repo/quote\"doc.md"
run_case "file with a quote in its name fails" 1 'quote"doc.md:2:'

new_repo
printf 'line one\nname: Project-Phe''onix\n' >"$repo/fixture.ts"
run_case "private project name fails" 1 "fixture.ts:2:"

new_repo
printf 'db: Project_Phe''onix\n' >"$repo/fixture.ts"
run_case "private project db spelling fails" 1 "fixture.ts:1:"

new_repo
printf 'path ~/Projects/Game''Ideas/x\n' >"$repo/doc.md"
run_case "private project folder fails" 1 "doc.md:1:"

new_repo
printf "name: 'game', workdir: '~/code/game'\n" >"$repo/fixture.ts"
run_case "generic game stand-in passes" 0 ""

new_repo
printf 'see /Users''/x/secret\n' >"$repo/café.md"
printf 'café.md:/x/secret\n' >"$repo/scripts/check-public.allow"
run_case "allowlisted hit in a non-ASCII name passes" 0 ""

new_repo
ln -s "../outside" "$repo/link"
run_case "plain symlink target passes" 0 ""

new_repo
printf 'see /Users''/x/secret\n' >"$repo/doc.md"
printf 'doc.md:/x/secret\n' >"$repo/scripts/check-public.allow"
run_case "allowlisted hit passes" 0 ""

if [ "$failures" -gt 0 ]; then
  echo "check-public_test: $failures case(s) failed" >&2
  exit 1
fi
echo "check-public_test: all $n cases passed"
