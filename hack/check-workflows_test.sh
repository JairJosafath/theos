#!/usr/bin/env bash
# Tests for hack/check-workflows.sh: the repository's workflows pass, and a job
# off vars.RUNS_ON, without runs-on, without timeout-minutes (#1655), or
# making a Kind cluster without the theos-kind label fails, and so does a workflow on pushes to a branch
# without the concurrency group, or one that cancels the run in progress; and
# Lint and UI off pull requests or with other paths there, Lint without its
# test-hack job (#1101), and Tests off pull requests or off pushes to main
# (#1556) or without cancel-in-progress on pull requests fail. Every check
# but a release's is partial (#127): Lint or Tests asking for every step, on a
# shallow clone, without main's head before a push to compare with, grouped
# so that a push to main can lose its run or cancelling one, and Tests on a
# pushed tag fail, and so does a release.yml without a job that runs make
# check FORCE=1 or whose publishing job doesn't need it. So does a job on pull
# requests missing from .github/required-checks, a line there naming no job,
# or two checks with one name (#1380);
# and so does a release.yml that doesn't dispatch Verify Release for minors
# only (#1105); and so does a job on pull requests whose runs-on would let a
# fork's pull request run on a self-hosted runner, or a workflow on
# pull_request_target (#49).
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
timeout="    timeout-minutes: 10"
good="    runs-on: \${{ vars.RUNS_ON || 'ubuntu-latest' }}
$timeout"
kind="    runs-on: \${{ vars.RUNS_ON == 'self-hosted' && fromJSON('[\"self-hosted\", \"theos-kind\"]') || vars.RUNS_ON || 'ubuntu-latest' }}
$timeout"

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
$timeout
    steps:
      - run: true"
"$CHECK" "$T/wf" 2>/dev/null
check "no runs-on fails" "$?" 1

# #1655: a hung Lint job without a timeout held main's concurrency group for
# hours, and GitHub would have let it run 6.
wf no-timeout "  a:
    runs-on: \${{ vars.RUNS_ON || 'ubuntu-latest' }}
    steps:
      - run: make lint"
out=$("$CHECK" "$T/wf" 2>&1)
check "no timeout-minutes fails (#1655)" "$?" 1
check "no timeout-minutes names the job" "$(grep -c 'job a has no timeout-minutes' <<<"$out")" 1

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

pushwf by-sha "concurrency:
  group: \${{ github.workflow }}-\${{ github.sha }}
"
"$CHECK" "$T/wf" 2>/dev/null
check "push with the group by commit passes" "$?" 0

# An issue event or a tag push needs no group: each must run.
rm -rf "$T/wf" && mkdir "$T/wf" && printf 'name: x\non:\n  issues:\n    types: [opened]\n  push:\n    tags: [v1]\n\njobs:\n  a:\n%s\n' "$good" >"$T/wf/x.yml"
"$CHECK" "$T/wf" 2>/dev/null
check "issues and tag pushes need no group" "$?" 0

# real NAME SED: the repository's Lint, UI, Tests, Vulnerabilities, Secrets
# and CodeQL workflows and its required checks, alone in a directory, NAME.yml (or
# required-checks for NAME list) edited with the sed script SED.
REPO=$(cd "$(dirname "$0")/.." && pwd)
real() {
  rm -rf "$T/r" && mkdir -p "$T/r/wf" && cp "$REPO"/.github/workflows/{lint,ui,test,vulncheck,secrets,codeql}.yml "$T/r/wf/"
  cp "$REPO"/.github/required-checks "$T/r/"
  local f="$T/r/wf/$1.yml"
  [ "$1" = list ] && f=$T/r/required-checks
  sed -i.bak "$2" "$f" && rm "$f.bak"
}

real lint ''
"$CHECK" "$T/r/wf" 2>/dev/null
check "Lint, UI, Tests, Vulnerabilities, Secrets and CodeQL as they are pass" "$?" 0
for c in 'test.yml Tests' 'lint.yml Lint' 'lint.yml Shell tests' 'ui.yml UI' 'codeql.yml CodeQL Go' \
  'codeql.yml CodeQL TypeScript'; do
  check "the required checks list $c" "$(grep -cx "$c" "$REPO/.github/required-checks")" 1
done

real lint '/^  pull_request:/,/^  workflow_dispatch:/{/^  workflow_dispatch:/!d;}'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Lint off pull requests fails" "$?" 1
check "Lint off pull requests says so" "$(grep -c "lint.yml: doesn't run on pull requests to main" <<<"$out")" 1

real ui '/^  pull_request:/,/^  workflow_dispatch:/s/branches: \[main\]/branches: [dev]/'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "UI on pull requests to another branch fails" "$?" 1
check "UI on pull requests to another branch says so" "$(grep -c "ui.yml: doesn't run on pull requests to main" <<<"$out")" 1

real lint '/^  pull_request:/,/^  workflow_dispatch:/{/- hack\/\*\*/d;}'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Lint with fewer paths on pull requests fails" "$?" 1
check "Lint with fewer paths says so" "$(grep -c 'lint.yml: runs on pull requests with other paths' <<<"$out")" 1

real lint 's/run: make check CHECK_STEPS=test-hack/run: make help/'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Lint without the shell tests' part of make check fails" "$?" 1
check "Lint without the shell tests says so" "$(grep -c 'lint.yml: has no test-hack job' <<<"$out")" 1

real test '/^  pull_request:/,/^  workflow_dispatch:/{/^  workflow_dispatch:/!d;}'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Tests off pull requests fails" "$?" 1
check "Tests off pull requests says so" "$(grep -c "test.yml: doesn't run on pull requests to main" <<<"$out")" 1

# #1556: Tests off pushes to main left main untested until a tag.
real test '/^  push:/,/^  pull_request:/{/^    branches: \[main\]/d;}'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Tests off pushes to main fails (#1556)" "$?" 1
check "Tests off pushes to main says so" "$(grep -c "test.yml: doesn't run on pushes to main" <<<"$out")" 1

# #127: every check but a release's is partial, against main. A release's run
# of every step is Release's own job, so Tests leaves the tags alone (it ran
# every package on them, #1525).
real test "/^  push:/,/^  pull_request:/s/^    branches: \[main\]$/    branches: [main]\n    tags: ['v[0-9]+.[0-9]+.[0-9]+']/"
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Tests on a pushed tag fails (#127)" "$?" 1
check "Tests on a pushed tag says so" "$(grep -c 'test.yml: runs on a pushed tag' <<<"$out")" 1

for w in test lint; do
  real $w 's/^  TEST_BASE: .*/&\n  TEST_ALL: "1"/'
  out=$("$CHECK" "$T/r/wf" 2>&1)
  check "$w.yml asking for every step fails (#127)" "$?" 1
  check "$w.yml asking for every step says so" "$(grep -c "$w.yml: asks for every step (TEST_ALL or FORCE)" <<<"$out")" 1

  real $w 's/run: make check CHECK_STEPS=\(.*\)$/run: make check FORCE=1 CHECK_STEPS=\1/'
  out=$("$CHECK" "$T/r/wf" 2>&1)
  check "$w.yml forcing its part of the check fails (#127)" "$(grep -c "$w.yml: asks for every step (TEST_ALL or FORCE)" <<<"$out")" 1

  # A push to main is compared with the head before it: on a shallow clone
  # there is no merge base, and without that head it is compared with itself.
  real $w '0,/^          fetch-depth: 0$/{/^          fetch-depth: 0$/d;}'
  out=$("$CHECK" "$T/r/wf" 2>&1)
  check "$w.yml on a shallow clone fails (#127)" "$?" 1
  check "$w.yml on a shallow clone says so" "$(grep -c "$w.yml: a job clones without fetch-depth: 0" <<<"$out")" 1

  real $w 's/github\.event\.before/github.sha/'
  out=$("$CHECK" "$T/r/wf" 2>&1)
  check "$w.yml without main's head before a push fails (#127)" "$?" 1
  check "$w.yml without main's head before a push says so" "$(grep -c "$w.yml: doesn't compare a push with main's head before it" <<<"$out")" 1

  # Each push to main needs its own run: grouped by ref, GitHub drops a
  # pending run for a newer push, and its change is then never checked.
  real $w 's/^  group: .*/  group: ${{ github.workflow }}-${{ github.ref }}/'
  out=$("$CHECK" "$T/r/wf" 2>&1)
  check "$w.yml grouped by ref fails (#127)" "$?" 1
  check "$w.yml grouped by ref says so" "$(grep -c "$w.yml: groups runs by" <<<"$out")" 1

  # Grouped by commit alone, a new push to a pull request replaces no run.
  real $w 's/^  group: .*/  group: ${{ github.workflow }}-${{ github.sha }}/'
  out=$("$CHECK" "$T/r/wf" 2>&1)
  check "$w.yml grouped by commit fails" "$?" 1
  check "$w.yml grouped by commit says so" "$(grep -c "$w.yml: groups runs by" <<<"$out")" 1
done

# Tests cancelled a superseded head of main while each run tested every
# package (#1556); a partial run that is cancelled leaves its change untested.
real test "s/^  cancel-in-progress: .*/  cancel-in-progress: \${{ github.event_name == 'pull_request' || github.ref == 'refs\/heads\/main' }}/"
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Tests cancelling a run on main fails (#127)" "$?" 1
check "Tests cancelling a run on main says so" "$(grep -c 'test.yml: cancels the run in progress' <<<"$out")" 1

# #66: Tests left out all of ui/, where the Go tests read the fixtures the
# UI's tests share with them, so a pull request that changed a UI rule and
# the fixture's report ran no Go test.
real test '/^      - ui\/src\/model\/testdata\/\*\*$/d'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Tests without the fixtures under ui/ fails (#66)" "$?" 1
check "Tests without the fixtures under ui/ says so" "$(grep -c "test.yml: leaves out ui/\*\* without taking ui/src/model/testdata/\*\* back in after it" <<<"$out")" 1

# A later pattern wins: taken back in before the exclusion, they are left out.
real test "/^      - ui\/src\/model\/testdata\/\*\*$/d; s|^      - '!docs/\*\*'$|      - ui/src/model/testdata/**\n&|"
check "the edit takes the fixtures in before the exclusion, on pushes and pull requests" \
  "$(grep -A1 '^      - ui/src/model/testdata/\*\*$' "$T/r/wf/test.yml" | grep -c "^      - '!docs/\*\*'$")" 2
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Tests taking the fixtures in before leaving ui/ out fails (#66)" "$?" 1
check "Tests taking the fixtures in before leaving ui/ out says so" "$(grep -c "test.yml: leaves out ui/\*\* without taking" <<<"$out")" 1

real ui '/^  cancel-in-progress:/d'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "UI without cancel-in-progress fails" "$?" 1
check "UI without cancel-in-progress says so" "$(grep -c 'ui.yml: runs on pull requests without cancel-in-progress' <<<"$out")" 1

# Lint, not Tests: Tests alone may cancel a superseded head of main (#1556).
real lint 's/^  cancel-in-progress:.*/  cancel-in-progress: true/'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Lint cancelling main's runs fails" "$?" 1
check "Lint cancelling main's runs says so" "$(grep -c 'lint.yml: cancels the run in progress' <<<"$out")" 1

real list '/^ui.yml UI$/d'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "a pull-request job missing from the list fails" "$?" 1
check "a pull-request job missing says so" "$(grep -c "ui.yml: job check (UI) runs on pull requests but isn't in" <<<"$out")" 1

real list '$a\
release.yml Build and publish'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "a listed check naming no pull-request job fails" "$?" 1
check "a listed check naming no job says so" "$(grep -c 'release.yml Build and publish is no job' <<<"$out")" 1

real ui 's/^    name: UI$/    name: Lint/'
sed -i 's/^ui.yml UI$/ui.yml Lint/' "$T/r/required-checks"
out=$("$CHECK" "$T/r/wf" 2>&1)
check "two checks with one name fail" "$?" 1
check "two checks with one name say so" "$(grep -c 'two required checks are named Lint' <<<"$out")" 1

real lint '/^  test-hack:/,$s/^    name: .*//'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "a pull-request job without a name fails" "$?" 1
check "a pull-request job without a name says so" "$(grep -c 'job test-hack runs on pull requests without a name' <<<"$out")" 1

# relwf PERMS IF [RM]: release.yml whose first job runs every step of make
# check (#127) and whose second, which needs it, grants PERMS, pushes the
# images and, under IF, dispatches Verify Release, then removes the images
# under RM (default if: always(), #1636).
relwf() {
  rm -rf "$T/wf" && mkdir "$T/wf"
  printf 'name: Release\non:\n  push:\n    tags: [v1]\n\njobs:\n  check:\n%s\n    steps:\n      - name: Every step\n        run: make check FORCE=1\n  release:\n    needs: check\n    permissions:\n%s\n%s\n    steps:\n      - run: true\n      - name: Push\n        run: docker push x\n      - name: Dispatch\n%s\n        run: gh workflow run verify-release.yml -f tag=x\n      - name: Remove\n%s\n        run: docker rmi -f x\n' \
    "$good" "$1" "$good" "$2" "${3:-        if: always()}" >"$T/wf/release.yml"
}
minor="        if: env.PUBLISH == 'true' && endsWith(github.ref_name, '.0')"

relwf "      actions: write
      contents: write" "$minor"
"$CHECK" "$T/wf" 2>/dev/null
check "release dispatching for minors passes" "$?" 0

# #127: a release is the one place every step runs, and nothing ships unless
# they pass.
check "the repository's Release runs every step first" \
  "$(grep -c '^        run: make check FORCE=1$' "$REPO/.github/workflows/release.yml")" 1
relwf "      actions: write" "$minor"
sed -i 's/run: make check FORCE=1/run: make check/' "$T/wf/release.yml"
out=$("$CHECK" "$T/wf" 2>&1)
check "release without a run of every step fails (#127)" "$?" 1
check "release without a run of every step says so" "$(grep -c 'release.yml: no job runs make check FORCE=1' <<<"$out")" 1
relwf "      actions: write" "$minor"
sed -i '/^    needs: check$/d' "$T/wf/release.yml"
out=$("$CHECK" "$T/wf" 2>&1)
check "release that publishes without waiting for it fails (#127)" "$?" 1
check "release that publishes without waiting says so" \
  "$(grep -c "release.yml: the job that pushes the images doesn't need the job that runs make check FORCE=1 (needs: check)" <<<"$out")" 1
relwf "      actions: write" "$minor"
sed -i 's/^    needs: check$/    needs: [other, check]/' "$T/wf/release.yml"
"$CHECK" "$T/wf" 2>/dev/null
check "release needing it among others passes" "$?" 0

relwf "      contents: write" "$minor"
out=$("$CHECK" "$T/wf" 2>&1)
check "release without actions: write fails" "$?" 1
check "release without actions: write says so" "$(grep -c 'no permission actions: write' <<<"$out")" 1

relwf "      actions: write" "        if: env.PUBLISH == 'true'"
out=$("$CHECK" "$T/wf" 2>&1)
check "release dispatching for patches fails" "$?" 1
check "release dispatching for patches says so" "$(grep -c "doesn't run only for a minor" <<<"$out")" 1

relwf "      actions: write" "        if: endsWith(github.ref_name, '.0')"
sed -i 's/gh workflow run verify-release.yml -f tag=x/true/' "$T/wf/release.yml"
out=$("$CHECK" "$T/wf" 2>&1)
check "release without the dispatch fails" "$?" 1
check "release without the dispatch says so" "$(grep -c 'no step dispatches verify-release.yml' <<<"$out")" 1

# #1636: the images a release built stayed on the device, 13 GB a release.
perms="      actions: write"
relwf "$perms" "$minor" "        if: success()"
out=$("$CHECK" "$T/wf" 2>&1)
check "release removing the images only on success fails (#1636)" "$?" 1
check "release removing the images only on success says so" "$(grep -c 'release.yml: no step under if: always() removes the images it built' <<<"$out")" 1

relwf "$perms" "$minor"
sed -i 's/docker rmi -f x/true/' "$T/wf/release.yml"
out=$("$CHECK" "$T/wf" 2>&1)
check "release without the removal fails (#1636)" "$?" 1
check "release without the removal says so" "$(grep -c 'release.yml: no step under if: always() removes the images it built' <<<"$out")" 1

relwf "$perms" "$minor"
printf 'name: Release\non:\n  push:\n    tags: [v1]\n\njobs:\n  release:\n    permissions:\n%s\n%s\n    steps:\n      - name: Remove\n        if: always()\n        run: docker rmi -f x\n      - name: Push\n        run: docker push x\n      - name: Dispatch\n%s\n        run: gh workflow run verify-release.yml -f tag=x\n' \
  "$perms" "$good" "$minor" >"$T/wf/release.yml"
out=$("$CHECK" "$T/wf" 2>&1)
check "release removing the images before the push fails (#1636)" "$?" 1
check "release removing the images before the push says so" "$(grep -c 'release.yml: the step that removes the images (docker rmi) comes before the one that pushes them' <<<"$out")" 1

# verwf RM: verify-release.yml whose last step, under RM, removes the images.
verwf() {
  rm -rf "$T/wf" && mkdir "$T/wf"
  printf 'name: Verify Release\non:\n  workflow_dispatch:\n\njobs:\n  verify-release:\n%s\n    steps:\n      - run: docker pull x\n      - name: Remove\n%s\n        run: docker rmi -f x\n' \
    "$good" "$1" >"$T/wf/verify-release.yml"
}
verwf "        if: always()"
"$CHECK" "$T/wf" 2>/dev/null
check "verify-release removing the images always passes (#1636)" "$?" 0
verwf "        if: failure()"
out=$("$CHECK" "$T/wf" 2>&1)
check "verify-release removing the images only on failure fails (#1636)" "$?" 1
check "verify-release removing the images only on failure says so" "$(grep -c 'verify-release.yml: no step under if: always() removes the images it pulled' <<<"$out")" 1

# #49: once the repository is public anyone's fork opens pull requests, and
# with RUNS_ON=self-hosted its make targets and tests would run on the owner's
# devices. A fork's pull request gets the hosted runner whatever RUNS_ON says.
real ui "s/^    runs-on: .*/    runs-on: \${{ vars.RUNS_ON || 'ubuntu-latest' }}/"
out=$("$CHECK" "$T/r/wf" 2>&1)
check "a pull request job that a fork could run on a device fails (#49)" "$?" 1
check "it names the job and the rule (#49)" \
  "$(grep -c "ui.yml: job check runs on pull requests, so its runs-on must start \${{ github.event.pull_request.head.repo.fork && 'ubuntu-latest' ||" <<<"$out")" 1
for w in lint ui test vulncheck; do
  check "$w.yml sends a fork's pull request to the hosted runner (#49)" \
    "$(grep -c "^    runs-on: \${{ github.event.pull_request.head.repo.fork && 'ubuntu-latest' || vars.RUNS_ON || 'ubuntu-latest' }}$" "$REPO/.github/workflows/$w.yml")" \
    "$(grep -c '^    runs-on:' "$REPO/.github/workflows/$w.yml")"
done
real ui 's/^  pull_request:/  pull_request_target:/'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "a workflow on pull_request_target fails (#49)" "$?" 1
check "it says why (#49)" "$(grep -c "ui.yml: runs on pull_request_target, which gives a fork's pull request the repository's token" <<<"$out")" 1

# #52: Secrets scans every pull request and push to main, from a full clone.
real secrets '/^  pull_request:/,/^  workflow_dispatch:/s/^    branches: \[main\]$/    branches: [main]\n    paths:\n      - "**\/*.go"/'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Secrets with paths on pull requests fails (#52)" "$?" 1
check "it says a commit can reach main unscanned (#52)" "$(grep -c 'secrets.yml: .*reach main unscanned (#52)' <<<"$out")" 1
real secrets '/fetch-depth: 0/d'
out=$("$CHECK" "$T/r/wf" 2>&1)
check "Secrets on a shallow clone fails (#52)" "$?" 1
check "it says it scans one commit (#52)" "$(grep -c 'secrets.yml: .*scans one commit, not the history (#52)' <<<"$out")" 1
real secrets 's/run: make secrets-scan/run: make help/'
"$CHECK" "$T/r/wf" 2>/dev/null
check "Secrets without make secrets-scan fails (#52)" "$?" 1

exit $fail
