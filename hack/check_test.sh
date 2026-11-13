#!/usr/bin/env bash
# Tests for hack/check.sh, the steps of make check (#127): each step runs only
# when the change since the merge base can have broken it, and the run ends
# with one block that says what ran, what was skipped and why. In a throwaway
# module and clone, on a device that never ran make check (an empty cache),
# with a make that only logs what it is asked, the real hack/check-cache.sh,
# hack/test-packages.sh and gta (hack/gta, built here once).
#
# A docs-only change runs no step, and --idle says so; a Go package runs
# manifests and generate, and fmt, vet, lint and test on the packages gta
# selects; a UI-only change runs the UI's check alone, a hack/ script the shell
# tests alone; a CRD, a template, go.mod and the linter's configuration run
# what reads them, and so do the Makefile and a script that selects the
# steps; FORCE, TEST_ALL and a missing merge base run every step; a file
# make manifests writes selects the steps below it; a tier whose inputs passed
# on this device is answered from its cache, unless FORCE is set; a failing
# step ends the check, says so in the block and records no pass.
# Every check is partial, on the branch main too: only a release runs every
# step, by asking. CHECK_STEPS takes a part of the check, as a workflow's job
# does, and test-gitea runs only when named and when the Gitea adapter, the
# seed or the local Gitea is what the change reaches.
# Run: hack/check_test.sh
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GOWORK=off
unset TEST_ALL FORCE TEST_BASE GENERATED_PATHS THEOS_HEAVY_SLOT MAKE CHECK_STEPS

(cd "$REPO" && go build -o "$T/gta" ./hack/gta) || { echo "FAIL hack/gta does not build"; exit 1; }
export THEOS_GTA=$T/gta

# A make that logs its goals and variables, without its flags; FAKE_FAILS is
# a goal it fails, FAKE_GENERATES a file make manifests writes.
cat >"$T/make" <<'EOF'
#!/usr/bin/env bash
args=() skip=
for a in "$@"; do
  if [ -n "$skip" ]; then skip=; continue; fi
  case $a in
    --no-print-directory) ;;
    -o) skip=1 ;;
    *) args+=("$a") ;;
  esac
done
echo "${args[*]}" >>"$CALLS"
[ -z "${CHECK_MAKE:-}${GENERATED_PATHS:-}" ] || echo "the recipe's variables reach a step" >>"$CALLS"
if [ "${args[*]}" = "manifests generate" ] && [ -n "${FAKE_GENERATES:-}" ]; then echo generated >>"$FAKE_GENERATES"; fi
[ -z "${FAKE_FAILS:-}" ] || case " ${args[*]} " in *" $FAKE_FAILS "*) exit 2 ;; esac
exit 0
EOF
chmod +x "$T/make"
export CHECK_MAKE=$T/make CALLS=$T/calls CHECK_CACHE_DIR=$T/checks

# The readers table of hack/test-packages.sh: the tests of internal/scaffold
# and of the Gitea adapter read templates/, those of internal/other config/.
printf 'templates internal/scaffold internal/adapters/gitea\nconfig internal/other\n' >"$T/readers"
export TEST_READERS=$T/readers

# A module as the tiers of hack/check-cache.sh know it: hack/render builds
# internal/scaffold, internal/other stands alone.
src=$T/src
git init -q -b main "$src"
cd "$src" || exit 1
mkdir -p hack/lib hack/render hack/runner-fence internal/scaffold internal/other internal/adapters/gitea ui config/crd/bases templates/x docs
cp "$HERE/check.sh" "$HERE/check-cache.sh" "$HERE/test-packages.sh" hack/
cp "$HERE/lib/change.sh" hack/lib/
printf 'module example.com/r\n\ngo 1.22\n' >go.mod
pkg() { # pkg DIR NAME
  echo "package $2" >"$1/$2.go"
  printf 'package %s\nimport "testing"\nfunc TestX(t *testing.T) {}\n' "$2" >"$1/$2_test.go"
}
pkg internal/scaffold scaffold
pkg internal/other other
pkg internal/adapters/gitea gitea
printf 'package main\n\nimport _ "example.com/r/internal/scaffold"\n\nfunc main() {}\n' >hack/render/main.go
printf 'package main\n\nfunc main() {}\n' >hack/runner-fence/main.go
for f in Makefile .golangci.yml ui/package.json ui/app.ts hack/x.sh config/crd/bases/a.yaml templates/x/a.txt docs/notes.md; do echo 1 >"$f"; done
git add -A && git commit -qm one
git clone -q --bare "$src" "$T/origin.git"
git clone -q "$T/origin.git" "$T/clone"
cd "$T/clone" || exit 1
git switch -qc feature

# change FILE...: the clone back at main, then each FILE changed by a comment.
change() {
  local f
  git reset -q --hard origin/main && git clean -qfd
  for f in "$@"; do
    mkdir -p "$(dirname "$f")"
    case $f in
      *.go | go.mod) echo "// c" >>"$f" ;;
      *) echo "# c" >>"$f" ;;
    esac
  done
}
# run: hack/check.sh, its output in $T/out and what it asked of make in $T/calls.
run() {
  : >"$T/calls"
  hack/check.sh >"$T/out" 2>&1
}
# states: how each step went, in the order of the block: manifests generate,
# fmt, vet, lint, validate-yaml, test-hack, ui-test, test. The three scans
# are left out of it; scans says how they went: doc-links, license-headers,
# readers.
SCANS='doc-links\|license-headers\|readers'
states() {
  sed -n '/^make check: \(passed\|FAILED\)/,$p' "$T/out" | grep -v "^  \($SCANS\) " |
    sed -n 's/^  \(manifests generate\|[a-z-]*\)  *\(ran\|skipped\|cached\|FAILED\|not run\)  .*/\2/p' | paste -sd' '
}
scans() {
  sed -n '/^make check: \(passed\|FAILED\)/,$p' "$T/out" |
    sed -n "s/^  \($SCANS\)  *\(ran\|skipped\|cached\|FAILED\|not run\)  .*/\2/p" | paste -sd' '
}
calls() { tr '\n' ';' <"$T/calls"; }
m=example.com/r
none="skipped skipped skipped skipped skipped skipped skipped skipped"
every="ran ran ran ran ran ran ran ran"
all_pkgs="$m/hack/render $m/hack/runner-fence $m/internal/adapters/gitea $m/internal/other $m/internal/scaffold"

change docs/notes.md
run; check "docs only: exit 0" $? 0
check "docs only: no step runs, on a device that never ran make check" "$(states)" "$none"
check "docs only: but the link check, the one scan that reads Markdown" "$(scans)" "ran skipped skipped"
check "docs only: make runs the link check and no shell test" "$(calls)" "doc-links;"
check "docs only: says which file" "$(grep -c '^  doc-links  *ran  *[0-9]*s; docs/notes.md changed$' "$T/out")" 1
check "docs only: the block says what the change is" \
  "$(grep -c '^make check: passed. 1 file changed since origin/main ([0-9a-f]\{12\}); the steps that change selects:$' "$T/out")" 1
check "docs only: says why vet is skipped" "$(grep -c '^  vet  *skipped  no changed file lies in a Go package$' "$T/out")" 1
check "docs only: says why the UI check is skipped" "$(grep -c '^  ui-test  *skipped  none of its inputs changed: ui/ and the CRDs$' "$T/out")" 1
check "docs only: says why no package is tested" "$(grep -c '^  test  *skipped  no Go package changed since origin/main' "$T/out")" 1
check "docs only: the pass is recorded" "$(grep -c '^make check: passed on tree [0-9a-f]*; recorded$' "$T/out")" 1
check "the block ends the output" "$(tail -n 1 "$T/out" | cut -c1-8)" "  test  "
check "docs only: --idle says idle, a scan takes no heavy-job slot" "$(hack/check.sh --idle 2>&1)" "idle"

change internal/other/other.go
run
check "one Go package: generate, fmt, vet, lint and test" "$(states)" "ran ran ran ran skipped skipped skipped ran"
check "one Go package: that package alone, and its license header" "$(calls)" \
  "manifests generate;fmt GO_PACKAGES=$m/internal/other;vet GO_PACKAGES=$m/internal/other;lint LINT_PACKAGES=./internal/other;license-headers;test TEST_PACKAGES=$m/internal/other;"
check "one Go package: the scan that reads Go files" "$(scans)" "skipped ran skipped"
check "one Go package: the block says which gta selects" \
  "$(grep -c '^  vet  *ran  *[0-9]*s; the package gta selects: internal/other$' "$T/out")" 1
check "one Go package: the block says which are tested" \
  "$(grep -c '^  test  *ran  *[0-9]*s; 1 of 5 packages, .*; changed: internal/other$' "$T/out")" 1
hack/check.sh --idle >"$T/idle" 2>&1; check "one Go package: --idle exits 1" $? 1
check "one Go package: --idle says nothing" "$(cat "$T/idle")" ""
check "--idle runs nothing" "$(: >"$T/calls"; hack/check.sh --idle >/dev/null 2>&1; calls)" ""

change internal/scaffold/scaffold.go
run
check "a package hack/render builds: its dependent, and validate-yaml" "$(states)" "ran ran ran ran ran skipped skipped ran"
check "a package hack/render builds: the block counts them" \
  "$(grep -c '^  vet  *ran  *[0-9]*s; the 2 packages gta selects; changed: internal/scaffold$' "$T/out")" 1
check "a package hack/render builds: both packages" "$(calls)" \
  "manifests generate;fmt GO_PACKAGES=$m/hack/render $m/internal/scaffold;vet GO_PACKAGES=$m/hack/render $m/internal/scaffold;lint LINT_PACKAGES=./hack/render ./internal/scaffold;validate-yaml;license-headers;test TEST_PACKAGES=$m/hack/render $m/internal/scaffold;"

change internal/other/other_test.go
run
check "a test file: its package alone" "$(grep -c "^test TEST_PACKAGES=$m/internal/other\$" "$T/calls")" 1
check "a test file: its license header, and the readers table against it" "$(scans)" "skipped ran ran"

# The scans the shell tests make of the whole repository run by themselves
# when a file they read changed (#127): the shell tests' own inputs leave out
# the Go and docs trees, and a Go file without a license header passed.
change
git rm -q ui/app.ts
run
check "a file that is gone: the link check, a link may name it" "$(grep -c '^  doc-links  *ran  *[0-9]*s; ui/app.ts is gone, which a link may name$' "$T/out")" 1
change hack/x.sh docs/notes.md internal/other/other_test.go
echo "# with the scans" >>hack/x.sh
run
check "the shell tests ran: they ran the scans, which don't run again" "$(scans)" "skipped skipped skipped"
check "the shell tests ran: says so" "$(grep -c '^  doc-links  *skipped  the shell tests just ran it$' "$T/out")" 1
check "the shell tests ran: make runs no scan" "$(grep -c '^\(doc-links\|license-headers\|readers\)$' "$T/calls")" 0
change internal/other/other.go
CHECK_STEPS="test-hack doc-links license-headers readers" run
check "the Shell tests job's part, a Go file: the license check alone" "$(calls)" "license-headers;"

change ui/app.ts
run
check "UI only: the UI's check and nothing of Go" "$(states)" "skipped skipped skipped skipped skipped skipped ran skipped"
check "UI only: make ui-test" "$(calls)" "ui-test;"
check "UI only: says which file" "$(grep -c '^  ui-test  *ran  *[0-9]*s; ui/app.ts changed$' "$T/out")" 1
run
check "UI again: answered from this device's cache" "$(states)" "skipped skipped skipped skipped skipped skipped cached skipped"
check "UI again: make is not called" "$(calls)" ""
check "UI again: --idle says idle" "$(hack/check.sh --idle 2>&1)" "idle"
FORCE=1 run
check "FORCE=1: every step runs, past the cache" "$(states)" "$every"
check "FORCE=1: says so" "$(grep -c '^make check: passed. Every step was selected: FORCE is set.$' "$T/out")" 1
check "FORCE=1: says why every package is tested" "$(grep -c '^  test  *ran  *[0-9]*s; every package: FORCE is set$' "$T/out")" 1
check "FORCE=1: every package" "$(calls)" \
  "manifests generate;fmt GO_PACKAGES=./...;vet GO_PACKAGES=./...;lint LINT_PACKAGES=;validate-yaml;test-hack;ui-test;test TEST_PACKAGES=$all_pkgs;"
TEST_ALL=1 run
check "TEST_ALL=1: every step is selected, and the tiers that passed are cached" "$(states)" "ran ran ran ran cached cached cached ran"

change ui/new.ts
run
check "an untracked file under ui/ counts" "$(calls)" "ui-test;"

change hack/x.sh
run
check "a hack/ script: the shell tests alone" "$(states)" "skipped skipped skipped skipped skipped ran skipped skipped"

change config/crd/bases/a.yaml
run
check "a CRD: generate, validate-yaml, the shell tests, the UI's check, which reads the CRDs, and its readers" "$(states)" "ran skipped skipped skipped ran ran ran ran"
check "a CRD: the tests that read config/" "$(grep -c "^test TEST_PACKAGES=$m/internal/other\$" "$T/calls")" 1

change templates/x/a.txt
run
check "a template: validate-yaml and its readers" "$(states)" "skipped skipped skipped skipped ran skipped skipped ran"
check "a template: the tests that read templates/" \
  "$(grep -c "^test TEST_PACKAGES=$m/internal/adapters/gitea $m/internal/scaffold\$" "$T/calls")" 1

# #127: go.mod selects no Go package, as gta has it: no fmt, vet, lint or
# test. It ran them on every package; only a release does.
change go.mod
run
check "#127 go.mod: generate, the YAML and the shell tests, and no package" "$(states)" "ran skipped skipped skipped ran ran skipped skipped"
check "#127 go.mod: make is asked for no package" "$(calls)" "manifests generate;validate-yaml;test-hack;"
check "#127 go.mod: says why" "$(grep -c '^  test  *skipped  no Go package changed since origin/main' "$T/out")" 1
change go.mod internal/other/other.go
run
check "#127 go.mod and a Go file: the packages gta selects for the Go file" \
  "$(grep -c "^test TEST_PACKAGES=$m/internal/other\$" "$T/calls")" 1
check "#127 go.mod and a Go file: vet on that package, not on every one" "$(grep -c "^vet GO_PACKAGES=$m/internal/other\$" "$T/calls")" 1

change .golangci.yml
run
check "the linter's configuration: lint on every package, no fmt, vet or test" "$(states)" "skipped skipped skipped ran skipped ran skipped skipped"
check "the linter's configuration: says why" "$(grep -c '^  lint  *ran  *[0-9]*s; every package: .golangci.yml changed$' "$T/out")" 1
change .golangci.yml internal/other/other.go
run
check "the linter's configuration and a package: lint on every package" "$(grep -c '^lint LINT_PACKAGES=$' "$T/calls")" 1
check "the linter's configuration and a package: vet on that package" "$(grep -c "^vet GO_PACKAGES=$m/internal/other\$" "$T/calls")" 1

# #127: no file selects every step, on a device as in a workflow: only a
# release runs them all. The Makefile, a *.mk and the scripts that select the
# steps select what reads them: the steps whose tools the Makefile pins and
# the tiers whose inputs list the file. Each case starts without a cache.
# selects FILE STATES: a change to FILE alone runs the steps STATES says.
selects() {
  change "$1"
  [ "$1" != hack/gta/main.go ] || printf 'package main\n\nfunc main() {}\n' >"$1"
  rm -rf "$CHECK_CACHE_DIR"
  run
  check "#127 $1: what reads it, not every step" "$(states)" "$2"
}
selects Makefile "ran skipped ran ran ran ran ran skipped"
check "#127 Makefile: vet, whose flags it holds, reads every package" "$(grep -c '^vet GO_PACKAGES=./...$' "$T/calls")" 1
check "#127 Makefile: the linter it pins reads every package" "$(grep -c '^  lint  *ran  *[0-9]*s; every package: Makefile changed$' "$T/out")" 1
check "#127 Makefile: no Go test reads it" "$(grep -c '^test ' "$T/calls")" 0
selects hack/ui.mk "skipped skipped ran ran ran ran ran skipped"
selects hack/check-cache.sh "skipped skipped skipped skipped ran ran ran skipped"
selects hack/check.sh "skipped skipped skipped skipped skipped ran skipped skipped"
selects hack/test-packages.sh "skipped skipped skipped skipped skipped ran skipped skipped"
selects hack/lib/change.sh "skipped skipped skipped skipped skipped ran skipped skipped"
selects hack/gta/main.go "skipped ran ran ran skipped ran skipped ran"
check "#127 hack/gta: its own package" "$(grep -c "^test TEST_PACKAGES=$m/hack/gta\$" "$T/calls")" 1
selects README.md "skipped skipped skipped skipped skipped ran skipped skipped"

# What make manifests writes counts for the steps below it.
change internal/other/other.go
FAKE_GENERATES=config/crd/bases/a.yaml run
check "a CRD make manifests regenerates: validate-yaml, the shell tests and the UI's check too" "$(states)" "ran ran ran ran ran ran ran ran"

change internal/other/other.go
THEOS_GTA=false run
check "gta fails: every package is formatted, vetted and linted" "$(grep -c '^vet GO_PACKAGES=./...$' "$T/calls")" 1
check "gta fails: every package is tested" "$(grep -c "^test TEST_PACKAGES=$all_pkgs\$" "$T/calls")" 1
check "gta fails: says so" "$(grep -c '^  vet  *ran  *[0-9]*s; every package: gta could not select them' "$T/out")" 1

change internal/scaffold/scaffold.go
FAKE_FAILS=vet run; check "a failing step: its exit code" $? 2
check "a failing step: the later ones are not run" "$(states)" "ran ran FAILED not run not run not run not run not run"
check "a failing step: make is not called again" "$(calls)" "manifests generate;fmt GO_PACKAGES=$m/hack/render $m/internal/scaffold;vet GO_PACKAGES=$m/hack/render $m/internal/scaffold;"
check "a failing step: the block says where" "$(grep -c '^make check: FAILED at vet\. ' "$T/out")" 1
check "a failing step: says which step failed the others" "$(grep -c '^  test  *not run  vet failed$' "$T/out")" 1
check "a failing step: no pass is recorded" "$(grep -c 'passed on tree' "$T/out")" 0
echo "// again" >>internal/scaffold/scaffold.go
FAKE_FAILS=validate-yaml run; check "a failing tier: its exit code" $? 2
check "a failing tier: the later ones are not run" "$(states)" "ran ran ran ran FAILED not run not run not run"
run
check "a failing tier: runs again, nothing was cached" "$(states)" "ran ran ran ran ran skipped skipped ran"

change docs/notes.md
rm -rf "$CHECK_CACHE_DIR"
TEST_BASE=nope run
check "no merge base: every step" "$(states)" "$every"
check "no merge base: says so" "$(grep -c '^make check: passed. Every step was selected: no merge base with nope.$' "$T/out")" 1
git commit -qam docs
head=$(git rev-parse HEAD)
git switch -q --detach "$head"
TEST_BASE=HEAD~1 run
check "#2058 detached, TEST_BASE the commit before a docs change: no step" "$(states)" "$none"

# #127: every check is partial, on the branch main too: what main holds that
# origin/main doesn't. Only a release runs every step, by asking (FORCE=1).
git switch -q main
rm -rf "$CHECK_CACHE_DIR"
run
check "#127 the branch main, as origin/main: no step" "$(states)" "$none"
echo "// c" >>internal/other/other.go
run
check "#127 the branch main, a change: the steps it selects" "$(states)" "ran ran ran ran skipped skipped skipped ran"
git commit -qam other
run
check "#127 the branch main, a commit origin/main lacks: the same" "$(states)" "ran ran ran ran skipped skipped skipped ran"
git switch -q feature

# CHECK_STEPS takes a part of the check: a workflow's job runs its part,
# selected the same way, and records no pass of the tree.
change internal/scaffold/scaffold.go
echo "// part" >>internal/scaffold/scaffold.go
CHECK_STEPS="generate lint validate-yaml" run; check "a part of the steps: exit 0" $? 0
check "a part: those steps alone" "$(calls)" "manifests generate;lint LINT_PACKAGES=./hack/render ./internal/scaffold;validate-yaml;"
check "a part: the block lists them alone" "$(states)" "ran ran ran"
check "a part: the block says so" "$(grep -c '; of the steps asked for (CHECK_STEPS), the steps that change selects:$' "$T/out")" 1
check "a part: no pass of the tree is recorded" "$(grep -c 'passed on tree' "$T/out")" 0
CHECK_STEPS=manifests run
check "a part: manifests names the step generate" "$(calls)" "manifests generate;"
change docs/notes.md
CHECK_STEPS=test-hack run
check "a part, docs only: the shell tests are skipped" "$(states)" "skipped"
check "a part, docs only: --idle says idle" "$(CHECK_STEPS="lint test-hack" hack/check.sh --idle 2>&1)" "idle"
change hack/x.sh
echo "# part" >>hack/x.sh
CHECK_STEPS=test-hack run
check "a part, a hack/ script: the shell tests" "$(calls)" "test-hack;"
FORCE=1 CHECK_STEPS="vet test-hack" run
check "a part, FORCE=1: every step asked for" "$(calls)" "vet GO_PACKAGES=./...;test-hack;"
check "a part, FORCE=1: says so" "$(grep -c '^make check: passed. Every step asked for (CHECK_STEPS) was selected: FORCE is set.$' "$T/out")" 1
CHECK_STEPS="lint nope" run; check "a part naming no step: exit 2" $? 2
check "a part naming no step: says which" "$(grep -c 'CHECK_STEPS names nope, which is no step' "$T/out")" 1
check "a part naming no step: runs nothing" "$(calls)" ""

# test-gitea, the Gitea adapter against a real Gitea, runs only when named:
# when make test tests the adapter, or the seed or the local Gitea changed.
change internal/adapters/gitea/gitea.go
CHECK_STEPS="test test-gitea" run
check "test-gitea, the adapter changed: its tests and the integration tests" "$(calls)" \
  "test TEST_PACKAGES=$m/internal/adapters/gitea;test-gitea;"
change templates/x/a.txt
CHECK_STEPS=test-gitea run
check "test-gitea, a template the adapter's tests read: runs" "$(calls)" "test-gitea;"
change hack/seed/seed.sh
CHECK_STEPS="test test-gitea" run
check "test-gitea, the seed changed: runs, with no package to test" "$(states)" "skipped ran"
check "test-gitea, the seed changed: says which file" "$(grep -c '^  test-gitea  *ran  *[0-9]*s; hack/seed/seed.sh changed$' "$T/out")" 1
change internal/other/other.go
CHECK_STEPS="test test-gitea" run
check "test-gitea, another package changed: skipped" "$(states)" "ran skipped"
check "test-gitea, skipped: says why" \
  "$(grep -c '^  test-gitea  *skipped  the change reaches neither internal/adapters/gitea, the seed nor' "$T/out")" 1
FORCE=1 CHECK_STEPS=test-gitea run
check "test-gitea, FORCE=1: runs" "$(calls)" "test-gitea;"
change internal/adapters/gitea/gitea.go
run
check "test-gitea is no step of a plain make check" "$(grep -c '^test-gitea' "$T/calls")" 0

# The Makefile of this repository: make check runs hack/check.sh, a dry run
# runs nothing of it, and a check that is idle takes no heavy-job slot.
cd "$REPO" || exit 1
unset CHECK_MAKE CALLS TEST_READERS THEOS_GTA
export HEAVY_SLOTS_DIR=$T/slots HEAVY_JOBS=1 HEAVY_POLL=0.2 CHECK_CACHE_DIR=$T/checks-make
out=$(HEAVY_JOBS=0 TEST_ALL=1 timeout 120 make -n check 2>&1)
check "make -n check: prints the recipe, hack/check.sh" "$(grep -c 'hack/check.sh$' <<<"$out")" 1
check "make -n check: runs no step" "$(grep -c '^make check: ' <<<"$out")" 0
[ ! -e "$CHECK_CACHE_DIR" ]; check "make -n check: records nothing" $? 0
check "the Makefile asks hack/check.sh --idle before it takes a slot" "$(grep -c '^CHECK_IDLE := .*hack/check.sh --idle' Makefile)" 1
sleep 3600 &
holder=$!
"$HERE/heavy-slot.sh" acquire "$holder" "holder" 2>/dev/null
TEST_ALL=1 timeout 5 make -n check >/dev/null 2>&1; check "make check, steps to run: waits for a slot" $? 124
timeout 5 make -n check TEST_ALL=1 >/dev/null 2>&1; check "make check TEST_ALL=1, a variable of the command line: waits too" $? 124
timeout 60 make -n check CHECK_IDLE=idle >/dev/null 2>&1; check "make check, idle: takes no slot" $? 0
kill "$holder"

exit $fail
