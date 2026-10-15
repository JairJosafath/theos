#!/usr/bin/env bash
# Checks that every job of the GitHub workflows can run on the self-hosted
# runners (hack/gh-runner, docs/dev/testenv.md): its runs-on uses vars.RUNS_ON,
# and a job that makes a Kind cluster (kind create cluster, the e2e target)
# asks for the label c4factory-kind, which only runner 1 of a device carries,
# so a device runs one of them at a time. A workflow that runs on pushes to a
# branch keeps one pending run per branch: it carries the concurrency group
# ${{ github.workflow }}-${{ github.ref }} without cancel-in-progress, so a
# running job finishes and GitHub drops the superseded pending runs. Release
# (release.yml) has a step that dispatches verify-release.yml for a minor tag
# only, `if:` endsWith(github.ref_name, '.0'), in a job granted actions: write
# (#1105). Prints each job or workflow that doesn't and exits 1. hack/check-workflows_test.sh
# runs it in make check.
#
#   hack/check-workflows.sh [DIR]   the workflows in DIR (default .github/workflows)
set -euo pipefail

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
  if [ "$push" = push ]; then
    # shellcheck disable=SC2016 # the expression is GitHub's, not the shell's
    want='${{ github.workflow }}-${{ github.ref }}'
    group=${group//\'/} group=${group//\"/}
    if [ "$group" != "$want" ]; then
      echo "$f: runs on pushes to a branch without the concurrency group $want" >&2
      fail=1
    elif [ "$cancel" != - ] && [ "$cancel" != false ]; then
      echo "$f: cancels the run in progress (cancel-in-progress: $cancel); a running job on main must finish" >&2
      fail=1
    fi
  fi
done

# Release's dispatch of Verify Release: the step's if and run, and whether its
# job may write actions.
rel=$DIR/release.yml
if [ -e "$rel" ]; then
  IFS=$'\t' read -r found minor actions < <(awk '
    function flush() {
      if (run ~ /gh workflow run verify-release\.yml/) { found = 1; if (cond ~ /endsWith\(github\.ref_name, \x27\.0\x27\)/) { minor = 1; if (jobactions) actions = 1 } }
      cond = ""; run = ""
    }
    /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { flush(); jobactions = 0; perms = 0; next }
    /^    permissions:/ { perms = 1; next }
    perms && /^      actions:[[:space:]]*write/ { jobactions = 1 }
    /^    [^ ]/ { perms = 0 }
    /^      - / { flush() }
    /^      (- |  )if:/ { cond = $0 }
    { run = run "\n" $0 }
    END { flush(); print (found ? "yes" : "-") "\t" (minor ? "yes" : "-") "\t" (actions ? "yes" : "-") }
  ' "$rel")
  if [ "$found" != yes ]; then
    echo "$rel: no step dispatches verify-release.yml" >&2
    fail=1
  elif [ "$minor" != yes ]; then
    echo "$rel: the step that dispatches verify-release.yml doesn't run only for a minor, if: endsWith(github.ref_name, '.0')" >&2
    fail=1
  elif [ "$actions" != yes ]; then
    echo "$rel: the job that dispatches verify-release.yml has no permission actions: write" >&2
    fail=1
  fi
fi
exit $fail
