#!/usr/bin/env bash
# Exits 0 when Go can have changed since the merge base with TEST_BASE (default
# origin/main): a *.go file, go.mod, go.sum, .golangci.yml or .custom-gcl.yml
# changed, committed, staged, unstaged or untracked; exits 1 when none did.
# Exits 0 as well, saying why on stderr, when TEST_ALL or FORCE is set or there
# is no merge base, so nothing is skipped by accident (#2058). make check runs
# it, for the lint, when LINT_CHANGED_ONLY is set (the factory's own CI sets it).
#
#   TEST_BASE   the commit or branch to diff against (default origin/main)
#   TEST_ALL, FORCE   non-empty: Go is taken as changed
set -euo pipefail

BASE=${TEST_BASE:-origin/main}
say() { echo "make lint: $*" >&2; }

[ -z "${TEST_ALL:-}" ] || { say "TEST_ALL is set"; exit 0; }
[ -z "${FORCE:-}" ] || { say "FORCE is set"; exit 0; }
base=$(git merge-base "$BASE" HEAD 2>/dev/null) || { say "no merge base with $BASE"; exit 0; }

while IFS= read -r f; do
  case "$f" in
    *.go | go.mod | go.sum | .golangci.yml | .custom-gcl.yml)
      say "$f changed since $BASE (${base:0:12})"
      exit 0
      ;;
  esac
done < <({
  git diff --name-only --no-renames "$base" --
  git ls-files --others --exclude-standard
} | sort -u)
exit 1
