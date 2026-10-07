#!/usr/bin/env bash
# Checks that every job of the GitHub workflows can run on the self-hosted
# runners (hack/gh-runner, docs/dev/testenv.md): its runs-on uses vars.RUNS_ON,
# and a job that makes a Kind cluster (kind create cluster, the e2e target)
# asks for the label c4factory-kind, which only runner 1 of a device carries,
# so a device runs one of them at a time. Prints each job that doesn't and
# exits 1. hack/check-workflows_test.sh runs it in make check.
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
done
exit $fail
