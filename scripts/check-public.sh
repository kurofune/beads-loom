#!/usr/bin/env bash
# Fail when a tracked file carries a personal path or name, so CI blocks a
# leak before it reaches the public repo. Scope: every tracked file outside
# .beads/ (the backlog) and .djinn/seats/ (seat files the lead owns), binary
# files included, and the target text of every tracked symlink (the link is
# never followed).
#
# A hit fails unless scripts/check-public.allow lists it as `path:substring`
# (the hit is in `path` and its line contains `substring`). `/Users/me/` is
# the placeholder home and never fails.
#
# The patterns below are split with '' so this file does not match itself.
set -euo pipefail
export LC_ALL=C
cd "$(git rev-parse --show-toplevel)"

allow=scripts/check-public.allow
users_re='/Users''/[^/]+/'
names_re='nathan''gross|outcast''games|game''ideas|project[-_]phe''onix'
scope=(-- . ':!.beads' ':!.djinn/seats')

tmp=$(mktemp)
found=$(mktemp)
trap 'rm -f "$tmp" "$found"' EXIT

git_or_die() {
  local rc=0
  "$@" || rc=$?
  if [ "$rc" -gt "${ok_rc:-0}" ]; then
    echo "check-public: $1 $2 failed (exit $rc)" >&2
    exit 2
  fi
}

# Git quotes unusual path names ("caf\303\251") in its normal output, which
# names no file. Every path here comes from a NUL-delimited listing (-z),
# which Git never quotes, and is matched back as a literal pathspec.
grep_tracked() {
  local path
  ok_rc=1 git_or_die git grep -l -z --text "$@" "${scope[@]}" >"$tmp"
  while IFS= read -r -d '' path; do
    ok_rc=1 git_or_die git grep -h -n --text "$@" -- ":(literal)$path" |
      tr '\000' '.' |
      while IFS= read -r line; do printf '%s:%s\n' "$path" "$line"; done
  done <"$tmp"
}

symlink_targets() {
  local entry path target
  git_or_die git ls-files -s -z "${scope[@]}" >"$tmp"
  while IFS= read -r -d '' entry; do
    [ "${entry%% *}" = 120000 ] || continue
    path=${entry#*$'\t'}
    [ -L "./$path" ] || continue
    target=$(readlink -- "./$path") || {
      echo "check-public: readlink failed on symlink $path" >&2
      exit 2
    }
    printf '%s:1:%s\n' "$path" "$target"
  done <"$tmp"
}

grep_tracked -E "$users_re" >"$found"
grep_tracked -i -E "$names_re" >>"$found"
symlink_targets >>"$found"
hits=$(sort -u "$found")

[ -n "$hits" ] || { echo "check-public: no personal paths or names"; exit 0; }

violations=$(printf '%s\n' "$hits" | awk -v users_re="$users_re" -v names_re="$names_re" -v allow="$allow" '
  BEGIN {
    while ((getline entry < allow) > 0) {
      if (entry ~ /^[[:space:]]*(#|$)/) continue
      i = index(entry, ":")
      if (i == 0) continue
      n++; allow_path[n] = substr(entry, 1, i - 1); allow_sub[n] = substr(entry, i + 1)
    }
  }
  {
    i = index($0, ":"); path = substr($0, 1, i - 1); rest = substr($0, i + 1)
    j = index(rest, ":"); content = substr(rest, j + 1)
    stripped = content
    gsub("/Users/me/", "", stripped)
    if (stripped !~ users_re && tolower(content) !~ names_re) next
    for (k = 1; k <= n; k++)
      if (path == allow_path[k] && index(content, allow_sub[k]) > 0) next
    print
  }')

[ -n "$violations" ] || { echo "check-public: no personal paths or names (allowlisted hits only)"; exit 0; }

echo "check-public: personal path or name in a tracked file:" >&2
printf '%s\n' "$violations" | sed 's/^/  /' >&2
echo "Replace it with a generic path (~/code/loom, /Users/me/) or, if the hit is intended, add path:substring to $allow." >&2
exit 1
