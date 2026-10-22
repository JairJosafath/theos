#!/usr/bin/env bash
# Checks that every job of the GitHub workflows can run on the self-hosted
# runners (hack/gh-runner, docs/dev/testenv.md): its runs-on uses vars.RUNS_ON,
# and a job that makes a Kind cluster (kind create cluster, the e2e target)
# asks for the label c4factory-kind, which only runner 1 of a device carries,
# so a device runs one of them at a time. A workflow that runs on pushes to a
# branch carries the concurrency group ${{ github.workflow }}-${{ github.ref }}
# (one pending run per branch; GitHub drops the superseded pending runs) or
# ${{ github.workflow }}-${{ github.sha }} (one run per commit), without
# cancel-in-progress, so a running job on main finishes. The pull requests
# (#1380): a workflow that runs on pull requests to main does so with the
# same paths as on pushes, and carries cancel-in-progress
# ${{ github.event_name == 'pull_request' }}, so a new push to a pull request
# replaces its run and main's runs still finish. Lint (lint.yml), UI (ui.yml)
# and Tests (test.yml) run on pull requests to main, and Lint has a test-hack
# job running make test-hack. Tests is the one workflow that runs on pushes
# to main with cancel-in-progress there: it groups by ref with
# cancel-in-progress ${{ github.event_name == 'pull_request' || github.ref ==
# 'refs/heads/main' }}, so only the newest head of main is tested (the
# runners are the owner's devices), and it must run on pushes to main, since
# yolo mode merges a pull request unchecked and main's run is what shows a
# red main (#1556). Tests also runs on a pushed release tag, the
# 'v[0-9]+.[0-9]+.[0-9]+' tags Release (release.yml) runs on, and on a manual
# run; the tag's run tests every package before the release ships (#1522),
# so test.yml must keep that trigger (#1525).
# Every job of a workflow that runs on pull requests is listed, by its
# workflow file and its name:, in .github/required-checks (the checks
# hack/lib/github.sh land waits for), the names are distinct, and every line
# there names such a job. Release (release.yml) has a step that
# dispatches verify-release.yml for a minor tag only, `if:`
# endsWith(github.ref_name, '.0'), in a job granted actions: write (#1105).
# A self-hosted runner builds and pulls on the device's Docker, whose disk
# never shrinks (#1636): Release has a step under if: always() that removes
# the images (docker rmi), after the step that pushes them, and Verify
# Release (verify-release.yml) has one too, so the images go whatever the
# steps before did. Every job that runs on a runner (not one that calls a
# reusable workflow, uses:) sets timeout-minutes, a few times its normal
# duration: without one GitHub lets a hung job run 6 hours, and a hung Lint
# job on main held its concurrency group, so every later run on main waited
# behind it and nothing said whether main was green (#1655).
# Prints each job or workflow that doesn't and exits 1.
# hack/check-workflows_test.sh runs it in make check.
#
#   hack/check-workflows.sh [DIR]   the workflows in DIR (default .github/workflows),
#                                   the list in DIR/../required-checks
set -euo pipefail

# shellcheck disable=SC2016 # the expressions are GitHub's, not the shell's
BY_REF='${{ github.workflow }}-${{ github.ref }}' BY_SHA='${{ github.workflow }}-${{ github.sha }}'
# shellcheck disable=SC2016
ON_PR='${{ github.event_name == '"'pull_request'"' }}'
# shellcheck disable=SC2016
ON_PR_OR_MAIN='${{ github.event_name == '"'pull_request'"' || github.ref == '"'refs/heads/main'"' }}'
# The tags Release runs on; Tests runs on them too (#1525).
RELEASE_TAGS="['v[0-9]+.[0-9]+.[0-9]+']"

# trigger FILE EVENT: the EVENT trigger of the workflow FILE, one line per
# setting: "branches <list>", "tags <list>" and "path <pattern>" for each
# path, or "none" when the workflow has no such trigger.
trigger() {
  awk -v ev="$2" '
    /^[^ #]/ { top = $0; sub(/:.*/, "", top); inev = 0; next }
    top == "on" && $0 ~ "^  " ev ":" { inev = 1; found = 1; inpaths = 0; next }
    top == "on" && /^  [^ ]/ { inev = 0 }
    inev && /^    [^ ]/ { inpaths = 0 }
    inev && /^    branches:/ { b = $0; sub(/^    branches:[[:space:]]*/, "", b); print "branches " b }
    inev && /^    tags:/ { t = $0; sub(/^    tags:[[:space:]]*/, "", t); print "tags " t }
    inev && /^    paths:/ { inpaths = 1; next }
    inev && inpaths && /^      - / { p = $0; sub(/^      - /, "", p); print "path " p }
    END { if (!found) print "none" }
  ' "$1"
}

DIR=${1:-$(cd "$(dirname "$0")/.." && pwd)/.github/workflows}
LIST=$(dirname "$DIR")/required-checks
# The required checks, "FILE<TAB>NAME" per line, and the jobs of the workflows
# that run on pull requests, in the same form.
required=$(sed -e 's/#.*//' -e '/^[[:space:]]*$/d' -e 's/[[:space:]]*$//' -e 's/^\([^[:space:]]*\)[[:space:]][[:space:]]*/\1\t/' "$LIST" 2>/dev/null || true)
pr_jobs=
fail=0
for f in "$DIR"/*.yml "$DIR"/*.yaml; do
  [ -e "$f" ] || continue
  pr=$(trigger "$f" pull_request)
  # One line per job: its id, its runs-on (or -), kind when it makes a Kind
  # cluster, its name: (or -) and its timeout-minutes (or -). A job that
  # calls a reusable workflow (uses:) has no runs-on and no timeout.
  while IFS=$'\t' read -r job runs_on kind name timeout; do
    if [ "$pr" != none ]; then
      pr_jobs+="${f##*/}"$'\t'"$name"$'\n'
      if [ "$name" = - ]; then
        echo "$f: job $job runs on pull requests without a name:" >&2
        fail=1
      elif ! grep -qxF "${f##*/}"$'\t'"$name" <<<"$required"; then
        echo "$f: job $job ($name) runs on pull requests but isn't in $LIST" >&2
        fail=1
      fi
    fi
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
    if [ "$runs_on" != uses ] && [ "$timeout" = - ]; then
      echo "$f: job $job has no timeout-minutes, so a hung job runs 6 hours and holds the concurrency group (#1655)" >&2
      fail=1
    fi
  done < <(awk '
    function flush() { if (job != "") print job "\t" (runs ? runs : "-") "\t" (kind ? "kind" : "-") "\t" (name != "" ? name : "-") "\t" (timeout != "" ? timeout : "-"); job = ""; runs = ""; kind = 0; name = ""; timeout = "" }
    /^[^ #]/ { flush(); injobs = ($0 ~ /^jobs:/); next }
    injobs && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { flush(); job = $1; sub(/:$/, "", job); next }
    job != "" && /^    runs-on:/ { runs = $0; sub(/^    runs-on:[[:space:]]*/, "", runs) }
    job != "" && /^    uses:/ { runs = "uses" }
    job != "" && /^    timeout-minutes:/ { timeout = $0; sub(/^    timeout-minutes:[[:space:]]*/, "", timeout) }
    job != "" && /^    name:/ { name = $0; sub(/^    name:[[:space:]]*/, "", name); gsub(/^["\x27]|["\x27]$/, "", name) }
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
  group=${group#[\'\"]} group=${group%[\'\"]} cancel=${cancel#[\'\"]} cancel=${cancel%[\'\"]}
  # Tests alone may cancel a run on main (its case below says what it must).
  if [ "$push" = push ]; then
    if [ "$group" != "$BY_REF" ] && [ "$group" != "$BY_SHA" ]; then
      echo "$f: runs on pushes to a branch without the concurrency group $BY_REF or $BY_SHA" >&2
      fail=1
    elif [ "${f##*/}" != test.yml ] && [ "$cancel" != - ] && [ "$cancel" != false ] && [ "$cancel" != "$ON_PR" ]; then
      echo "$f: cancels the run in progress (cancel-in-progress: $cancel); a running job on main must finish" >&2
      fail=1
    fi
  fi
  if [ "$pr" != none ]; then
    if ! grep -qx 'branches \[main\]' <<<"$pr"; then
      echo "$f: doesn't run on pull requests to main" >&2
      fail=1
    elif [ "$push" = push ] && [ "$(grep '^path ' <<<"$pr")" != "$(trigger "$f" push | grep '^path ')" ]; then
      echo "$f: runs on pull requests with other paths than on pushes" >&2
      fail=1
    fi
    if [ "$cancel" != "$ON_PR" ] && [ "$cancel" != "$ON_PR_OR_MAIN" ]; then
      echo "$f: runs on pull requests without cancel-in-progress: $ON_PR, so a new push doesn't replace the old run" >&2
      fail=1
    fi
  fi

  case ${f##*/} in
    lint.yml | ui.yml | test.yml)
      if [ "$pr" = none ]; then
        echo "$f: doesn't run on pull requests to main" >&2
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
      if ! grep -qx 'branches \[main\]' <<<"$(trigger "$f" push)"; then
        echo "$f: doesn't run on pushes to main, so nothing tests main until a tag (#1556)" >&2
        fail=1
      fi
      if ! grep -qxF "tags $RELEASE_TAGS" <<<"$(trigger "$f" push)"; then
        echo "$f: doesn't run on a pushed release tag, push: tags: $RELEASE_TAGS as Release does, so a release ships without a run of every package (#1525)" >&2
        fail=1
      fi
      if [ "$group" != "$BY_REF" ]; then
        echo "$f: groups runs by $group, not $BY_REF, so a push to main doesn't replace the run of the head it supersedes (#1556)" >&2
        fail=1
      fi
      if [ "$cancel" != "$ON_PR_OR_MAIN" ]; then
        echo "$f: cancel-in-progress: $cancel, not $ON_PR_OR_MAIN, so a superseded head of main keeps its runner (#1556)" >&2
        fail=1
      fi
      ;;
  esac
done

# Each required check names a job of a workflow that runs on pull requests,
# and no two of them share a name, so a ruleset can tell them apart.
while IFS=$'\t' read -r file name; do
  [ -n "$file" ] || continue
  if ! grep -qxF "$file"$'\t'"$name" <<<"$pr_jobs"; then
    echo "$LIST: $file $name is no job of a workflow that runs on pull requests" >&2
    fail=1
  fi
done <<<"$required"
for name in $(cut -f2 <<<"$required" | sort | uniq -d | tr ' ' '\001'); do
  echo "$LIST: two required checks are named ${name//$'\001'/ }" >&2
  fail=1
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

# removal FILE: "yes" when a step of FILE under if: always() runs docker rmi,
# then "yes" when it comes after the last step that runs docker push (or
# there is none), for the workflows that leave images on the device (#1636).
removal() {
  awk '
    function flush() {
      n++
      if (run ~ /docker push /) push = n
      if (run ~ /docker rmi / && cond ~ /always\(\)/) rmi = n
      cond = ""; run = ""
    }
    /^      - / { flush() }
    /^      (- |  )if:/ { cond = $0 }
    { run = run "\n" $0 }
    END { flush(); print (rmi ? "yes" : "-") "\t" (rmi && rmi > push ? "yes" : "-") }
  ' "$1"
}
if [ -e "$rel" ]; then
  IFS=$'\t' read -r found after < <(removal "$rel")
  if [ "$found" != yes ]; then
    echo "$rel: no step under if: always() removes the images it built (docker rmi), so a release leaves them on the device (#1636)" >&2
    fail=1
  elif [ "$after" != yes ]; then
    echo "$rel: the step that removes the images (docker rmi) comes before the one that pushes them" >&2
    fail=1
  fi
fi
ver=$DIR/verify-release.yml
if [ -e "$ver" ]; then
  IFS=$'\t' read -r found _ < <(removal "$ver")
  if [ "$found" != yes ]; then
    echo "$ver: no step under if: always() removes the images it pulled (docker rmi), so a check leaves them on the device (#1636)" >&2
    fail=1
  fi
fi
exit $fail
