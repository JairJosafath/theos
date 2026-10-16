#!/usr/bin/env bash
# Prints the Go packages make test tests, one per line: those whose tests a
# change since the merge base with TEST_BASE can affect, else every package.
# Says on stderr which it chose and why, so a skipped package shows in the
# output (#1295).
#
# A package is affected when a file in it changed (its directory, or one below
# it, for embedded files) or its test binary links one that did, so its
# dependents are tested too. The change is everything since the merge base:
# commits, staged, unstaged and untracked files.
#
# Every package is tested when:
#   - TEST_ALL or FORCE is set (FORCE=1 make check reruns all of it),
#   - the branch is main, or no merge base with TEST_BASE is found,
#   - a file changed that a Go test may read without importing it: go.mod,
#     go.sum, the Makefile, hack/, config/ (the CRDs and RBAC envtest loads),
#     api/, dist/, templates/, test/crds, testdata, the workflows, a Dockerfile,
#     docs/getting-started.md, or any file outside the paths below.
# A change only to paths no Go test reads (docs/, .claude/, .vscode/, ui/,
# the other .github/ files, the top-level *.md but README.md) tests nothing.
#
# The digitalocean/gta tool does the same selection, but loads every package
# of every module in the build list (go list ...), which this module's go.mod
# can't do without changes (go mod tidy wants more requirements) and which
# takes a minute where go list -test ./... takes half a second.
#
#   TEST_BASE   the branch to diff against (default origin/main)
#   TEST_ALL    non-empty: every package
#   FORCE       non-empty: every package
set -euo pipefail

BASE=${TEST_BASE:-origin/main}
say() { echo "make test: $*" >&2; }

# The module's packages, but the e2e tests, which make e2e runs.
list=$(go list ./...)
mapfile -t all < <(grep -v '/test/e2e$' <<<"$list")

full() {
  say "testing every package (${#all[@]}): $1"
  printf '%s\n' "${all[@]}"
  exit 0
}

[ -z "${TEST_ALL:-}" ] || full "TEST_ALL is set"
[ -z "${FORCE:-}" ] || full "FORCE is set"
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
[ "$branch" != main ] || full "the branch is main"
base=$(git merge-base "$BASE" HEAD 2>/dev/null) || full "no merge base with $BASE"

mapfile -t files < <({
  git diff --name-only --no-renames "$base" --
  git ls-files --others --exclude-standard
} | sort -u)

# The module's directories that hold a package, relative to the top.
module=$(go list -m)
declare -A dirs=()
for p in "${all[@]}"; do
  d=${p#"$module"}
  d=${d#/}
  dirs[${d:-.}]=$p
done

declare -A changed=()
for f in "${files[@]}"; do
  case "$f" in
    go.mod | go.sum | go.work | go.work.sum | Makefile | *.mk | hack/* | config/* | api/* | dist/* | \
      templates/* | test/crds/* | testdata/* | */testdata/* | .github/workflows/* | Dockerfile | \
      */Dockerfile | *.Dockerfile | .dockerignore | docs/getting-started.md | README.md)
      full "$f changed, which a Go test may read without importing it"
      ;;
    test/e2e/*) continue ;;
  esac
  # The nearest directory holding a package owns the file: a Go file, or one
  # it embeds or its tests read.
  d=$(dirname "$f")
  while [ -z "${dirs[$d]:-}" ] && [ "$d" != . ]; do d=$(dirname "$d"); done
  if [ -n "${dirs[$d]:-}" ]; then
    changed[${dirs[$d]}]=1
    continue
  fi
  case "$f" in
    # A Go file in no package: a deleted package's, whose importers changed too.
    *.go) [ -e "$f" ] || continue ;;
    docs/* | .claude/* | .vscode/* | ui/* | .github/*) continue ;;
  esac
  [[ "$f" == */* || "$f" != *.md ]] || continue
  full "$f changed, which a Go test may read without importing it"
done

if [ ${#changed[@]} -eq 0 ]; then
  say "testing no package: no Go package changed since $BASE (${base:0:12})"
  exit 0
fi

# Each test binary (P.test) and every package it links, test variants
# ("P [P.test]") under their plain names.
graph=$(go list -test -f '{{.ImportPath}}{{range .Deps}} {{.}}{{end}}' "${all[@]}") ||
  full "go list -test failed"
picked=()
while read -r bin deps; do
  p=${bin%.test}
  hit=${changed[$p]:-}
  if [ -z "$hit" ]; then
    for dep in $deps; do
      if [ -n "${changed[$dep]:-}" ]; then
        hit=1
        break
      fi
    done
  fi
  [ -z "$hit" ] || picked+=("$p")
done < <(grep '^[^ ]*\.test\( \|$\)' <<<"$graph" | sed -E 's/ \[[^]]*\]//g')

say "testing ${#picked[@]} of ${#all[@]} packages, those the change since $BASE (${base:0:12}) affects; changed: $(printf '%s\n' "${!changed[@]}" | sort | sed "s|^$module/||" | paste -sd' ')"
[ ${#picked[@]} -eq 0 ] || printf '%s\n' "${picked[@]}"
