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
# what reads them; the Makefile, a script that selects the steps, FORCE,
# TEST_ALL, the branch main and a missing merge base run every step; a file
# make manifests writes selects the steps below it; a tier whose inputs passed
# on this device is answered from its cache, unless FORCE is set; a failing
# step ends the check, says so in the block and records no pass.
# Run: hack/check_test.sh
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GOWORK=off
unset TEST_ALL FORCE TEST_BASE GENERATED_PATHS THEOS_HEAVY_SLOT MAKE

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
# read templates/, those of internal/other config/.
printf 'templates internal/scaffold\nconfig internal/other\n' >"$T/readers"
export TEST_READERS=$T/readers

# A module as the tiers of hack/check-cache.sh know it: hack/render builds
# internal/scaffold, internal/other stands alone.
src=$T/src
git init -q -b main "$src"
cd "$src" || exit 1
mkdir -p hack/lib hack/render hack/runner-fence internal/scaffold internal/other ui config/crd/bases templates/x docs
cp "$HERE/check.sh" "$HERE/check-cache.sh" "$HERE/test-packages.sh" hack/
cp "$HERE/lib/change.sh" hack/lib/
printf 'module example.com/r\n\ngo 1.22\n' >go.mod
pkg() { # pkg DIR NAME
  echo "package $2" >"$1/$2.go"
  printf 'package %s\nimport "testing"\nfunc TestX(t *testing.T) {}\n' "$2" >"$1/$2_test.go"
}
pkg internal/scaffold scaffold
pkg internal/other other
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
# fmt, vet, lint, validate-yaml, test-hack, ui-test, test.
states() {
  sed -n '/^make check: \(passed\|FAILED\)/,$p' "$T/out" |
    sed -n 's/^  \(manifests generate\|[a-z-]*\)  *\(ran\|skipped\|cached\|FAILED\|not run\)  .*/\2/p' | paste -sd' '
}
calls() { tr '\n' ';' <"$T/calls"; }
m=example.com/r
none="skipped skipped skipped skipped skipped skipped skipped skipped"
every="ran ran ran ran ran ran ran ran"
all_pkgs="$m/hack/render $m/hack/runner-fence $m/internal/other $m/internal/scaffold"

change docs/notes.md
run; check "docs only: exit 0" $? 0
check "docs only: no step runs, on a device that never ran make check" "$(states)" "$none"
check "docs only: make is not called" "$(calls)" ""
check "docs only: the block says what the change is" \
  "$(grep -c '^make check: passed. 1 file changed since origin/main ([0-9a-f]\{12\}); the steps that change selects:$' "$T/out")" 1
check "docs only: says why vet is skipped" "$(grep -c '^  vet  *skipped  no changed file lies in a Go package$' "$T/out")" 1
check "docs only: says why the UI check is skipped" "$(grep -c '^  ui-test  *skipped  none of its inputs changed: ui/$' "$T/out")" 1
check "docs only: says why no package is tested" "$(grep -c '^  test  *skipped  no Go package changed since origin/main' "$T/out")" 1
check "docs only: the pass is recorded" "$(grep -c '^make check: passed on tree [0-9a-f]*; recorded$' "$T/out")" 1
check "the block ends the output" "$(tail -n 1 "$T/out" | cut -c1-8)" "  test  "
check "docs only: --idle says idle" "$(hack/check.sh --idle 2>&1)" "idle"

change internal/other/other.go
run
check "one Go package: generate, fmt, vet, lint and test" "$(states)" "ran ran ran ran skipped skipped skipped ran"
check "one Go package: that package alone" "$(calls)" \
  "manifests generate;fmt GO_PACKAGES=$m/internal/other;vet GO_PACKAGES=$m/internal/other;lint LINT_PACKAGES=./internal/other;test TEST_PACKAGES=$m/internal/other;"
check "one Go package: the block says which gta selects" \
  "$(grep -c '^  vet  *ran  *[0-9]*s; the package gta selects: internal/other$' "$T/out")" 1
check "one Go package: the block says which are tested" \
  "$(grep -c '^  test  *ran  *[0-9]*s; 1 of 4 packages, .*; changed: internal/other$' "$T/out")" 1
hack/check.sh --idle >"$T/idle" 2>&1; check "one Go package: --idle exits 1" $? 1
check "one Go package: --idle says nothing" "$(cat "$T/idle")" ""
check "--idle runs nothing" "$(: >"$T/calls"; hack/check.sh --idle >/dev/null 2>&1; calls)" ""

change internal/scaffold/scaffold.go
run
check "a package hack/render builds: its dependent, and validate-yaml" "$(states)" "ran ran ran ran ran skipped skipped ran"
check "a package hack/render builds: the block counts them" \
  "$(grep -c '^  vet  *ran  *[0-9]*s; the 2 packages gta selects; changed: internal/scaffold$' "$T/out")" 1
check "a package hack/render builds: both packages" "$(calls)" \
  "manifests generate;fmt GO_PACKAGES=$m/hack/render $m/internal/scaffold;vet GO_PACKAGES=$m/hack/render $m/internal/scaffold;lint LINT_PACKAGES=./hack/render ./internal/scaffold;validate-yaml;test TEST_PACKAGES=$m/hack/render $m/internal/scaffold;"

change internal/other/other_test.go
run
check "a test file: its package alone" "$(grep -c "^test TEST_PACKAGES=$m/internal/other\$" "$T/calls")" 1

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
check "a CRD: generate, validate-yaml, the shell tests and its readers" "$(states)" "ran skipped skipped skipped ran ran skipped ran"
check "a CRD: the tests that read config/" "$(grep -c "^test TEST_PACKAGES=$m/internal/other\$" "$T/calls")" 1

change templates/x/a.txt
run
check "a template: validate-yaml and its readers" "$(states)" "skipped skipped skipped skipped ran skipped skipped ran"
check "a template: the tests that read templates/" "$(grep -c "^test TEST_PACKAGES=$m/internal/scaffold\$" "$T/calls")" 1

change go.mod
run
check "go.mod: all of Go, the YAML and the shell tests" "$(states)" "ran ran ran ran ran ran skipped ran"
check "go.mod: every package" "$(calls)" \
  "manifests generate;fmt GO_PACKAGES=./...;vet GO_PACKAGES=./...;lint LINT_PACKAGES=;validate-yaml;test-hack;test TEST_PACKAGES=$all_pkgs;"
check "go.mod: says why" "$(grep -c '^  vet  *ran  *[0-9]*s; every package: go.mod changed$' "$T/out")" 1

change .golangci.yml
run
check "the linter's configuration: lint on every package, no fmt or vet" "$(states)" "skipped skipped skipped ran skipped ran skipped ran"
check "the linter's configuration: says why" "$(grep -c '^  lint  *ran  *[0-9]*s; every package: .golangci.yml changed$' "$T/out")" 1
change .golangci.yml internal/other/other.go
run
check "the linter's configuration and a package: lint on every package" "$(grep -c '^lint LINT_PACKAGES=$' "$T/calls")" 1
check "the linter's configuration and a package: vet on that package" "$(grep -c "^vet GO_PACKAGES=$m/internal/other\$" "$T/calls")" 1

# A tier that every step selects is still answered from the cache, so each
# case starts on a device without one.
for f in Makefile hack/ui.mk hack/check.sh hack/check-cache.sh hack/test-packages.sh hack/lib/change.sh hack/gta/main.go; do
  change "$f"
  [ "$f" != hack/gta/main.go ] || printf 'package main\n\nfunc main() {}\n' >"$f"
  rm -rf "$CHECK_CACHE_DIR"
  run
  check "$f: every step" "$(states)" "$every"
  check "$f: says why" "$(grep -c "^  vet  *ran  *[0-9]*s; every package: $f changed, which selects or runs the steps\$" "$T/out")" 1
done

# What make manifests writes counts for the steps below it.
change internal/other/other.go
FAKE_GENERATES=config/crd/bases/a.yaml run
check "a CRD make manifests regenerates: validate-yaml and the shell tests too" "$(states)" "ran ran ran ran ran ran skipped ran"

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
git switch -q main
rm -rf "$CHECK_CACHE_DIR"
run
check "the branch main: every step" "$(states)" "$every"

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
