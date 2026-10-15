#!/usr/bin/env bash
# Checks that every job of the GitHub workflows can run on the self-hosted
# runners (hack/gh-runner, docs/dev/testenv.md): its runs-on uses vars.RUNS_ON,
# and a job that makes a Kind cluster (kind create cluster, the e2e target)
# asks for the label c4factory-kind, which only runner 1 of a device carries,
# so a device runs one of them at a time. A workflow that runs on pushes to a
# branch carries the concurrency group ${{ github.workflow }}-${{ github.ref }}
# (one pending run per branch; GitHub drops the superseded pending runs) or
# ${{ github.workflow }}-${{ github.sha }} (one run per commit), without
# cancel-in-progress, so a running job finishes. And the triggers (#1101):
# Lint (lint.yml) and UI (ui.yml) run on pull requests to main with the same
# paths as on pushes, Lint has a test-hack job running make test-hack, and
# Tests (test.yml) runs on no pull request and groups by commit, so every
# merge commit on main gets a verdict. Prints each job or workflow that
# doesn't and exits 1. hack/check-workflows_test.sh runs it in make check.
#
#   hack/check-workflows.sh [DIR]   the workflows in DIR (default .github/workflows)
set -euo pipefail

# shellcheck disable=SC2016 # the expressions are GitHub's, not the shell's
BY_REF='${{ github.workflow }}-${{ github.ref }}' BY_SHA='${{ github.workflow }}-${{ github.sha }}'

# trigger FILE EVENT: the EVENT trigger of the workflow FILE, one line per
# setting: "branches <list>" and "path <pattern>" for each path, or "none"
# when the workflow has no such trigger.
trigger() {
  awk -v ev="$2" '
    /^[^ #]/ { top = $0; sub(/:.*/, "", top); inev = 0; next }
    top == "on" && $0 ~ "^  " ev ":" { inev = 1; found = 1; inpaths = 0; next }
    top == "on" && /^  [^ ]/ { inev = 0 }
    inev && /^    [^ ]/ { inpaths = 0 }
    inev && /^    branches:/ { b = $0; sub(/^    branches:[[:space:]]*/, "", b); print "branches " b }
    inev && /^    paths:/ { inpaths = 1; next }
    inev && inpaths && /^      - / { p = $0; sub(/^      - /, "", p); print "path " p }
    END { if (!found) print "none" }
  ' "$1"
}

DIR=${1:-$(cd "$(dirname "$0")/.." && pwd)/.github/workflows}
fail=0
for f in "$DIR"/*.yml "$DIR"/*.yaml; do
  [ -e "$f" ] || continue
  # One line per job: its name, its runs-on (or -), and kind when it makes a
  # Kind cluster. A job that calls a reusable workflow (uses:) has no runs-on.
  while IFS=$'\t' read -r job runs_on kind; do
    if [ "$runs_on" = - ]; then
      echo "$f: job $job has no runs-on" >&2
      fail=1
    elif [ "$runs_on" != uses ] && [[ $runs_on != *vars.RUNS_ON* ]]; then
      echo "$f: job $job runs on $runs_on, not \${{ vars.RUNS_ON || ... }}" >&2
      fail=1
    elif [ "$kind" = kind ] && [[ $runs_on != *c4factory-kind* ]]; then
      echo "$f: job $job makes a Kind cluster but doesn't ask for the label c4factory-kind" >&2
      fail=1
    fi
  done < <(awk '
    function flush() { if (job != "") print job "\t" (runs ? runs : "-") "\t" (kind ? "kind" : "-"); job = ""; runs = ""; kind = 0 }
    /^[^ #]/ { flush(); injobs = ($0 ~ /^jobs:/); next }
    injobs && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { flush(); job = $1; sub(/:$/, "", job); next }
    job != "" && /^    runs-on:/ { runs = $0; sub(/^    runs-on:[[:space:]]*/, "", runs) }
    job != "" && /^    uses:/ { runs = "uses" }
    job != "" && /kind create cluster|make e2e/ { kind = 1 }
    END { flush() }
  ' "$f")

  # The workflow's push branches (push or -) and its concurrency group and
  # cancel-in-progress (- when absent).
  IFS=$'\t' read -r push group cancel < <(awk '
    /^[^ #]/ { top = $0; sub(/:.*/, "", top); inpush = 0 }
    /^concurrency:[[:space:]]*[^[:space:]]/ { group = $0; sub(/^concurrency:[[:space:]]*/, "", group) }
    top == "on" && /^  push:/ { inpush = 1; next }
    top == "on" && /^  [^ ]/ { inpush = 0 }
    inpush && /^    branches:/ { push = 1 }
    top == "concurrency" && /^  group:/ { group = $0; sub(/^  group:[[:space:]]*/, "", group) }
    top == "concurrency" && /^  cancel-in-progress:/ { cancel = $0; sub(/^  cancel-in-progress:[[:space:]]*/, "", cancel) }
    END { print (push ? "push" : "-") "\t" (group != "" ? group : "-") "\t" (cancel != "" ? cancel : "-") }
  ' "$f")
  group=${group//\'/} group=${group//\"/}
  if [ "$push" = push ]; then
    if [ "$group" != "$BY_REF" ] && [ "$group" != "$BY_SHA" ]; then
      echo "$f: runs on pushes to a branch without the concurrency group $BY_REF or $BY_SHA" >&2
      fail=1
    elif [ "$cancel" != - ] && [ "$cancel" != false ]; then
      echo "$f: cancels the run in progress (cancel-in-progress: $cancel); a running job on main must finish" >&2
      fail=1
    fi
  fi

  case ${f##*/} in
    lint.yml | ui.yml)
      if ! grep -qx 'branches \[main\]' <<<"$(trigger "$f" pull_request)"; then
        echo "$f: doesn't run on pull requests to main" >&2
        fail=1
      elif [ "$(trigger "$f" pull_request | grep '^path ')" != "$(trigger "$f" push | grep '^path ')" ]; then
        echo "$f: runs on pull requests with other paths than on pushes" >&2
        fail=1
      fi
      ;;&
    lint.yml)
      if ! grep -q '^  test-hack:' "$f" || ! grep -q 'run: make test-hack$' "$f"; then
        echo "$f: has no test-hack job running make test-hack" >&2
        fail=1
      fi
      ;;
    test.yml)
      if [ "$(trigger "$f" pull_request)" != none ]; then
        echo "$f: runs on pull requests; hack/lib/github.sh land runs make check instead" >&2
        fail=1
      fi
      if [ "$group" != "$BY_SHA" ]; then
        echo "$f: groups runs by $group, not $BY_SHA, so a merge commit on main can go without a verdict" >&2
        fail=1
      fi
      ;;
  esac
done
exit $fail
