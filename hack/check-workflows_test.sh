#!/usr/bin/env bash
# Tests for hack/check-workflows.sh: the repository's workflows pass, and a job
# off vars.RUNS_ON, without runs-on, or making a Kind cluster without the
# c4factory-kind label fails.
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

exit $fail
