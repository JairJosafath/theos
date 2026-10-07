#!/usr/bin/env bash
# Tests for hack/check-workflows.sh: the repository's workflows pass, and a job
# off vars.RUNS_ON, without runs-on, or making a Kind cluster without the
# c4factory-kind label fails, and so does a workflow on pushes to a branch
# without the concurrency group, or one that cancels the run in progress.
# Run: hack/check-workflows_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
CHECK=$(cd "$(dirname "$0")" && pwd)/check-workflows.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

"$CHECK"
check "the repository's workflows pass" "$?" 0

# wf NAME BODY: a workflow with BODY as its jobs, alone in a directory.
wf() { rm -rf "$T/wf" && mkdir "$T/wf" && printf 'name: x\non:\n  push:\n\njobs:\n%s\n' "$2" >"$T/wf/$1.yml"; }
good="    runs-on: \${{ vars.RUNS_ON || 'ubuntu-latest' }}"
kind="    runs-on: \${{ vars.RUNS_ON == 'self-hosted' && fromJSON('[\"self-hosted\", \"c4factory-kind\"]') || vars.RUNS_ON || 'ubuntu-latest' }}"

wf ok "  a:
$good
    steps:
      - run: make test
  b:
    uses: ./.github/workflows/other.yml"
"$CHECK" "$T/wf" 2>/dev/null
check "RUNS_ON and a reusable workflow pass" "$?" 0

wf hosted "  a:
$good
  b:
    runs-on: ubuntu-latest"
out=$("$CHECK" "$T/wf" 2>&1)
check "hosted runs-on fails" "$?" 1
check "hosted runs-on names the job" "$(grep -c 'job b runs on ubuntu-latest' <<<"$out")" 1

wf none "  a:
    steps:
      - run: true"
"$CHECK" "$T/wf" 2>/dev/null
check "no runs-on fails" "$?" 1

wf kind "  a:
$good
    steps:
      - run: kind create cluster --wait 120s"
out=$("$CHECK" "$T/wf" 2>&1)
check "Kind without the label fails" "$?" 1
check "Kind without the label says so" "$(grep -c 'job a makes a Kind cluster' <<<"$out")" 1

wf kind-ok "  a:
$kind
    steps:
      - run: kind create cluster --wait 120s"
"$CHECK" "$T/wf" 2>/dev/null
check "Kind with the label passes" "$?" 0

# pushwf NAME CONCURRENCY: a workflow on pushes to main with CONCURRENCY as its
# top-level lines, alone in a directory.
pushwf() { rm -rf "$T/wf" && mkdir "$T/wf" && printf 'name: x\non:\n  push:\n    branches: [main]\n  workflow_dispatch:\n\n%s\njobs:\n  a:\n%s\n' "$2" "$good" >"$T/wf/$1.yml"; }
group='  group: ${{ github.workflow }}-${{ github.ref }}'

pushwf grouped "concurrency:
$group
"
"$CHECK" "$T/wf" 2>/dev/null
check "push with the group passes" "$?" 0

pushwf inline "concurrency: '\${{ github.workflow }}-\${{ github.ref }}'
"
"$CHECK" "$T/wf" 2>/dev/null
check "push with the group inline passes" "$?" 0

pushwf ungrouped ""
out=$("$CHECK" "$T/wf" 2>&1)
check "push without the group fails" "$?" 1
check "push without the group says so" "$(grep -c 'without the concurrency group' <<<"$out")" 1

pushwf other "concurrency:
  group: ci
"
"$CHECK" "$T/wf" 2>/dev/null
check "push with another group fails" "$?" 1

pushwf cancel "concurrency:
$group
  cancel-in-progress: true
"
out=$("$CHECK" "$T/wf" 2>&1)
check "cancel-in-progress fails" "$?" 1
check "cancel-in-progress says so" "$(grep -c 'cancels the run in progress' <<<"$out")" 1

# An issue event or a tag push needs no group: each must run.
rm -rf "$T/wf" && mkdir "$T/wf" && printf 'name: x\non:\n  issues:\n    types: [opened]\n  push:\n    tags: [v1]\n\njobs:\n  a:\n%s\n' "$good" >"$T/wf/x.yml"
"$CHECK" "$T/wf" 2>/dev/null
check "issues and tag pushes need no group" "$?" 0

exit $fail
