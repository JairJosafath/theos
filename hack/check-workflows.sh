#!/usr/bin/env bash
# Checks that every job of the GitHub workflows can run on the self-hosted
# runners (hack/gh-runner, docs/dev/devices.md): its runs-on uses vars.RUNS_ON,
# and a job that makes a Kind cluster (kind create cluster, the e2e target)
# asks for the label theos-kind, which only runner 1 of a device carries,
# so a device runs one of them at a time. A job of a workflow that runs on
# pull requests starts its runs-on with github.event.pull_request.head.repo.fork
# && 'ubuntu-latest' ||, so a fork's pull request runs on GitHub's hosted
# runner and never on a device (#49), and no workflow runs on
# pull_request_target. A workflow that runs on pushes to a
# branch carries the concurrency group ${{ github.workflow }}-${{ github.ref }}
# (one pending run per branch; GitHub drops the superseded pending runs),
# ${{ github.workflow }}-${{ github.sha }} (one run per commit) or
# ${{ github.workflow }}-${{ github.event_name == 'pull_request' && github.ref
# || github.sha }} (one per pull request, one per commit elsewhere), without
# cancel-in-progress on a branch, so a running job on main finishes. The pull
# requests (#1380): a workflow that runs on pull requests to main does so with
# the same paths as on pushes, and carries cancel-in-progress
# ${{ github.event_name == 'pull_request' }}, so a new push to a pull request
# replaces its run and main's runs still finish. Lint (lint.yml), UI (ui.yml)
# and Tests (test.yml) run on pull requests to main, and Lint has a test-hack
# job running the shell tests' part of make check. Secrets (secrets.yml) runs
# make secrets-scan from a full clone on every pull request and push to main,
# without paths (#52). Tests must run on pushes to main, since yolo mode
# merges a pull request unchecked and main's run is what shows a red main
# (#1556).
# Every check is partial, against main, and only a release runs every step
# (#127). So Lint and Tests clone the whole history (fetch-depth: 0, or there
# is no merge base and everything runs), name what a run is compared with
# (TEST_BASE: main's head before a push, github.event.before), and carry the
# third group above: a push to main is compared with the head before it, so
# each push needs its own run, and one GitHub dropped or cancelled for a
# newer push would leave its change unchecked until a release. No workflow
# but Release asks for every step (TEST_ALL, FORCE), and Tests no longer runs
# on a pushed tag: Release (release.yml) has a job that runs make check
# FORCE=1, and the job that pushes the images needs it, so nothing ships
# unless every step passed (it replaces the tag's run of Tests, #1522, #1525).
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
# One group per pull request and one per commit elsewhere: what a workflow
# carries whose run on main is compared with the head before the push (#127).
# shellcheck disable=SC2016
BY_PR_OR_SHA='${{ github.workflow }}-${{ github.event_name == '"'pull_request'"' && github.ref || github.sha }}'

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

# What the runs-on of a job that runs on pull requests starts with: a pull
# request from a fork gets GitHub's hosted runner whatever vars.RUNS_ON says,
# so nobody's fork runs code on the owner's devices (#49).
FORK_HOSTED="github.event.pull_request.head.repo.fork && 'ubuntu-latest' ||"

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
  # pull_request_target runs the base's workflow with the repository's token
  # and secrets on a stranger's pull request; no workflow here needs it (#49).
  if [ "$(trigger "$f" pull_request_target)" != none ]; then
    echo "$f: runs on pull_request_target, which gives a fork's pull request the repository's token (#49)" >&2
    fail=1
  fi
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
    elif [ "$pr" != none ] && [[ $runs_on != *"$FORK_HOSTED"* ]]; then
      echo "$f: job $job runs on pull requests, so its runs-on must start \${{ $FORK_HOSTED ... }}: a fork's code never runs on a self-hosted runner (#49)" >&2
      fail=1
    elif [ "$kind" = kind ] && [[ $runs_on != *theos-kind* ]]; then
      echo "$f: job $job makes a Kind cluster but doesn't ask for the label theos-kind" >&2
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
  if [ "$push" = push ]; then
    if [ "$group" != "$BY_REF" ] && [ "$group" != "$BY_SHA" ] && [ "$group" != "$BY_PR_OR_SHA" ]; then
      echo "$f: runs on pushes to a branch without the concurrency group $BY_REF, $BY_SHA or $BY_PR_OR_SHA" >&2
      fail=1
    elif [ "$cancel" != - ] && [ "$cancel" != false ] && [ "$cancel" != "$ON_PR" ]; then
      echo "$f: cancels the run in progress (cancel-in-progress: $cancel); a running job on main must finish" >&2
      fail=1
    fi
  fi
  # #127: only a release runs every step, and Release is the one to ask.
  if [ "${f##*/}" != release.yml ] && grep -v '^[[:space:]]*#' "$f" | grep -E '(^|[^A-Z_])(TEST_ALL|FORCE)[=:]' >/dev/null; then
    echo "$f: asks for every step (TEST_ALL or FORCE); every check but a release's is partial, against main (#127)" >&2
    fail=1
  fi
  if [ "$pr" != none ]; then
    if ! grep -qx 'branches \[main\]' <<<"$pr"; then
      echo "$f: doesn't run on pull requests to main" >&2
      fail=1
    elif [ "$push" = push ] && [ "$(grep '^path ' <<<"$pr")" != "$(trigger "$f" push | grep '^path ')" ]; then
      echo "$f: runs on pull requests with other paths than on pushes" >&2
      fail=1
    fi
    if [ "$cancel" != "$ON_PR" ]; then
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
    lint.yml | test.yml)
      # #127: their runs are partial, a push to main against the head before it.
      if [ "$group" != "$BY_PR_OR_SHA" ]; then
        echo "$f: groups runs by $group, not $BY_PR_OR_SHA: a push to main is compared with the head before it, so each push needs its own run (#127)" >&2
        fail=1
      fi
      if ! grep -q '^  TEST_BASE: .*github\.event\.before' "$f"; then
        echo "$f: doesn't compare a push with main's head before it (env: TEST_BASE: ... github.event.before ...), so a push to main is compared with itself and nothing runs (#127)" >&2
        fail=1
      fi
      if [ "$(grep -c 'uses: actions/checkout@' "$f")" != "$(grep -c '^          fetch-depth: 0$' "$f")" ]; then
        echo "$f: a job clones without fetch-depth: 0, so there is no merge base and every step runs (#127)" >&2
        fail=1
      fi
      ;;&
    lint.yml)
      if ! grep -q '^  test-hack:' "$f" || ! grep -q 'run: make check CHECK_STEPS=test-hack$' "$f"; then
        echo "$f: has no test-hack job running make check CHECK_STEPS=test-hack" >&2
        fail=1
      fi
      ;;
    test.yml)
      if ! grep -qx 'branches \[main\]' <<<"$(trigger "$f" push)"; then
        echo "$f: doesn't run on pushes to main, so nothing tests main until a release (#1556)" >&2
        fail=1
      fi
      if grep -q '^tags ' <<<"$(trigger "$f" push)"; then
        echo "$f: runs on a pushed tag; a release's run of every step is Release's own job, which gates the release (#127)" >&2
        fail=1
      fi
      ;;
    secrets.yml)
      # #52: every pull request and every push to main, whatever it changes,
      # scanned from a full clone, so no commit reaches main unscanned.
      if [ "$pr" != 'branches [main]' ] || [ "$(trigger "$f" push)" != 'branches [main]' ]; then
        echo "$f: doesn't run on every pull request and push to main, without paths, so a commit can reach main unscanned (#52)" >&2
        fail=1
      fi
      if ! grep -q '^          fetch-depth: 0$' "$f" || ! grep -q 'run: make secrets-scan$' "$f"; then
        echo "$f: doesn't run make secrets-scan on a full clone (fetch-depth: 0), so it scans one commit, not the history (#52)" >&2
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

# Release's full check (#127): the job with a step that runs make check
# FORCE=1, and whether the job that pushes the images needs it.
if [ -e "$rel" ]; then
  IFS=$'\t' read -r full needed < <(awk '
    /^jobs:/ { injobs = 1; next }
    injobs && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { job = $1; sub(/:$/, "", job); next }
    job != "" && /run: make check FORCE=1[[:space:]]*$/ { full = job }
    job != "" && /^    needs:/ { n = $0; sub(/^    needs:[[:space:]]*/, "", n); gsub(/[\[\],]/, " ", n); needs[job] = n }
    job != "" && /docker push / { pusher = job }
    END {
      ok = 0
      if (full != "" && pusher != "") { c = split(needs[pusher], a, " "); for (i = 1; i <= c; i++) if (a[i] == full) ok = 1 }
      print (full != "" ? full : "-") "\t" (ok ? "yes" : "-")
    }
  ' "$rel")
  if [ "$full" = - ]; then
    echo "$rel: no job runs make check FORCE=1, so a release ships without a run of every step, and every other check is partial (#127)" >&2
    fail=1
  elif [ "$needed" != yes ]; then
    echo "$rel: the job that pushes the images doesn't need the job that runs make check FORCE=1 (needs: $full), so a release ships whatever the full check says (#127)" >&2
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
