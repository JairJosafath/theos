#!/usr/bin/env bash
# make check's steps, each run only when the change can have broken it, the way
# digitalocean/gta selects Go packages (#127, docs/plans/done/selective-check.md).
# The change is everything that differs from the merge base with TEST_BASE:
# commits, staged, unstaged and untracked files (hack/lib/change.sh). The
# Makefile's check recipe runs this script.
#
#   hack/check.sh          run the steps the change selects, then say what ran
#   hack/check.sh --idle   print "idle" when no step but a scan would run, and
#                          run nothing: such a make check takes no heavy-job
#                          slot, the scans taking seconds
#
# The steps, in order, and what selects each:
#
#   manifests generate   a Go file under api/, cmd/ or internal/, whose markers
#                        and types controller-gen reads, go.mod, go.sum, a file
#                        they write (config/crd, config/rbac, the chart's
#                        copies), a script they run or the Makefile, which
#                        pins controller-gen
#   fmt, vet, lint       the packages gta says the change affects (hack/gta):
#                        those a changed file lies in and those that depend on
#                        them. After a change to go.mod or go.sum, the
#                        packages built from a module whose version changed;
#                        every package only for a line each is built by (go,
#                        toolchain). Every package for vet and lint after a
#                        change to the Makefile or a *.mk, which hold vet's
#                        flags and pin the linter; for lint after one to its
#                        configuration
#   validate-yaml,       a changed file among the tier's inputs
#   test-hack, ui-test   (hack/check-cache.sh changed). A tier the change selects
#                        is still answered from the device's cache when those
#                        inputs passed here before
#   doc-links,           three scans of the whole repository that the shell
#   license-headers,     tests make: a changed Markdown file, or a file that
#   readers              is gone, which a link may name; a changed Go file; a
#                        changed Go test or readers table. Each runs by
#                        itself, in seconds, unless the shell tests just ran
#                        them all. A docs-only change runs the link check and
#                        no shell test
#   test                 the packages hack/test-packages.sh prints: gta's, and
#                        the readers of the files Go tests read without
#                        importing them
#   test-gitea           only when CHECK_STEPS names it: internal/adapters/gitea
#                        is among the packages make test tests, or the seed
#                        or the local Gitea's script changed
#
# Every check is partial, against main, on a device as in a workflow. Every
# step runs only when someone asks for it (TEST_ALL or FORCE is set, which a
# release does and nothing else) or when the change can't be known (no merge
# base). FORCE=1 also passes by every cache. No file selects every step: the
# Makefile, a *.mk and the scripts that select the steps are read by the steps
# named above and by the tiers whose inputs list them, and select those. When
# in doubt a step runs: a tier whose inputs can't be compared, or packages gta
# can't select.
#
# The run ends with one block that names each step and says whether it ran,
# was skipped, was answered from a cache or failed, and why. A step that fails
# ends the check; the block then lists the later steps as not run.
#
#   TEST_BASE, TEST_ALL, FORCE   as in hack/lib/change.sh
#   CHECK_STEPS       the steps to take, by name: generate fmt vet lint
#                     validate-yaml test-hack doc-links license-headers
#                     readers ui-test test test-gitea. Empty: all but
#                     test-gitea. A workflow's job takes its part of
#                     the check with it; such a run records no pass of the
#                     tree
#   CHECK_MAKE        the make that runs the steps (default make)
#   GENERATED_PATHS   what make manifests and make generate write (the
#                     Makefile's list), for the note on uncommitted ones
#   THEOS_GTA         the command that runs gta (default: go run ./hack/gta)
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1
# shellcheck source=hack/lib/change.sh
. hack/lib/change.sh

MAKE=${CHECK_MAKE:-make}
GENERATED_PATHS=${GENERATED_PATHS:-config/crd config/rbac api dist/chart/templates/crd dist/chart/templates/rbac}
# The steps run in the environment make check was started in, without what the
# recipe passes to this script.
export -n CHECK_MAKE GENERATED_PATHS CHECK_STEPS
idle=
[ "${1:-}" != --idle ] || idle=1

# The steps asked for. manifests names the step generate too, as its goals do.
DEFAULT_STEPS="generate fmt vet lint validate-yaml test-hack doc-links license-headers readers ui-test test"
steps=${CHECK_STEPS:-}
steps=" ${steps//manifests/generate} "
[ -n "${steps// /}" ] || steps=" $DEFAULT_STEPS "
for s in $steps; do
  case $s in
    generate | fmt | vet | lint | validate-yaml | test-hack | doc-links | license-headers | readers | \
      ui-test | test | test-gitea) ;;
    *)
      echo "make check: CHECK_STEPS names $s, which is no step of: $DEFAULT_STEPS test-gitea" >&2
      exit 2
      ;;
  esac
done
# asked STEP: whether the run takes STEP.
asked() { [[ $steps == *" $1 "* ]]; }

# What make manifests and make generate read, run and write; the Makefile
# pins controller-gen and names what it reads. In a case pattern * matches a
# / too, so cmd/*.go is every Go file below cmd/.
GENERATE=('api/*' 'cmd/*.go' 'internal/*.go' go.mod go.sum go.work go.work.sum
  'config/crd/*' 'config/rbac/*' 'dist/chart/templates/crd/*' 'dist/chart/templates/rbac/*'
  hack/chart-crds.sh hack/chart-role.sh hack/keep-mtime.sh hack/boilerplate.go.txt Makefile)
# A change to one of these reaches every package: each is built from them.
GO_ALL=(go.work go.work.sum)
# These name the modules the packages are built from: hack/gta compares them
# with the merge base's and says which packages a changed module reaches, or
# that a line every package is built by changed.
GO_MODULES=(go.mod go.sum)
# The Makefile and what it includes hold vet's flags and pin the linter: a
# change to one reaches every package for those two.
MAKE_FILES=(Makefile '*.mk')
# The linter's configuration.
LINT_ALL=(.golangci.yml .custom-gcl.yml)

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

# first PATTERN...: the first changed file that matches one of the case
# patterns; exit 1 when none does.
first() {
  local f p
  for f in "${files[@]}"; do
    for p in "$@"; do
      # shellcheck disable=SC2254 # The patterns are patterns.
      case $f in $p)
        echo "$f"
        return 0
        ;;
      esac
    done
  done
  return 1
}

# load: the changed files into files, and into all the reason every step
# runs, if someone asked for that or the change can't be known. Called again
# after a step that writes files.
load() {
  files=()
  all=$CHANGE_ALL
  [ -n "$all" ] || mapfile -t files < <(change_files)
}

# in_go_tree: whether a changed file lies where a Go package can own or embed
# it: under a top-level directory that holds Go files, or is go.mod or go.sum.
# gta loads the module's packages, which takes a second or two a docs-only
# change need not spend.
in_go_tree() {
  local tops f
  ! first "${GO_MODULES[@]}" >/dev/null || return 0
  tops=$(git ls-files -co --exclude-standard -- '*.go' | awk -F/ '{ print (NF > 1 ? $1 : ".") }' | sort -u)
  for f in "${files[@]}"; do
    [[ $f == */* ]] || f=.
    grep -qxF -e "${f%%/*}" -e . <<<"$tops" && return 0
  done
  return 1
}

# go_scope: the packages fmt, vet and lint read, into go_pkgs: ./... for
# every package, the import paths gta selects, or none; go_why says which.
go_scope() {
  local f out kind pkg module changed=() modules=() n
  go_pkgs=()
  go_why=
  if [ -n "$failed" ]; then
    return 0
  elif [ -n "$all" ]; then
    go_pkgs=(./...)
    go_why="every package: $all"
  elif f=$(first "${GO_ALL[@]}"); then
    go_pkgs=(./...)
    go_why="every package: $f changed"
  elif ! in_go_tree; then
    go_why="no changed file lies in a Go package"
  elif ! out=$(printf '%s\n' "${files[@]}" | ${THEOS_GTA:-go run ./hack/gta} -base "$CHANGE_BASE" 2>"$T/gta.err"); then
    go_pkgs=(./...)
    go_why="every package: gta could not select them ($(tail -n 1 "$T/gta.err"))"
  elif [ -z "$out" ]; then
    go_why="gta finds no Go package the change affects"
  else
    module=$(go list -m 2>/dev/null)
    while read -r kind pkg; do
      case $kind in
        all)
          go_pkgs=(./...)
          go_why="every package: $pkg"
          return 0
          ;;
        modules) read -ra modules <<<"$pkg" ;;
        changed)
          go_pkgs+=("$pkg")
          changed+=("${pkg#"$module/"}")
          ;;
        *) go_pkgs+=("$pkg") ;;
      esac
    done <<<"$out"
    n=${#go_pkgs[@]}
    if [ "$n" = 1 ]; then
      go_why="the package gta selects${changed[*]:+: ${changed[*]}}"
    else
      go_why="the $n packages gta selects${changed[*]:+; changed: ${changed[*]}}"
    fi
    if [ ${#modules[@]} -gt 0 ]; then
      go_why+="; built from the changed modules: ${modules[*]:0:3}"
      [ ${#modules[@]} -le 3 ] || go_why+=" and $((${#modules[@]} - 3)) more"
    fi
  fi
}

# lint_dirs: go_pkgs as the directories golangci-lint takes, into dirs; empty
# for every package, which is what it lints when given none.
lint_dirs() {
  local module p
  dirs=()
  [ "${go_pkgs[*]}" != ./... ] || return 0
  module=$(go list -m 2>/dev/null)
  for p in "${go_pkgs[@]}"; do
    p=${p#"$module"}
    dirs+=(".${p:-/}")
  done
}

names=() states=() details=()
note() {
  names+=("$1")
  states+=("$2")
  details+=("$3")
}

# took SECONDS: as 41s or 3m41s.
took() { if [ "$1" -ge 60 ]; then echo "$(($1 / 60))m$(($1 % 60))s"; else echo "$1s"; fi; }

# step NAME CMD...: runs CMD when why says the change selects the step, and
# notes how it went; notes skip, or the cached answer, when it does not run.
# After a step failed the later ones are noted as not run. With --idle the
# first step that would run ends the script: the check is not idle.
failed=
step() {
  local name=$1 start=$SECONDS rc
  shift
  if [ -n "$failed" ]; then
    note "$name" "not run" "$failed failed"
    return 0
  fi
  if [ -z "$why" ]; then
    note "$name" skipped "$skip"
    return 0
  fi
  if [ -n "$cached" ]; then
    note "$name" cached "$cached"
    return 0
  fi
  # A scan takes seconds and no heavy-job slot: it leaves the check idle.
  if [ -n "$idle" ]; then
    [ -n "$light" ] || exit 1
    return 0
  fi
  echo "make check: $name: $why"
  "$@"
  rc=$?
  if [ "$rc" != 0 ]; then
    failed=$name
    rc_failed=$rc
    note "$name" FAILED "exit $rc after $(took $((SECONDS - start))); $why"
    return 0
  fi
  note "$name" ran "$(took $((SECONDS - start))); $why"
  ran+=("$name")
}

# inputs NAME: selects the tier or scan NAME by its inputs
# (hack/check-cache.sh): why when one changed, else skip.
inputs() {
  local out n
  why= skip= cached=
  if [ -n "$all" ]; then
    why=$all
    return 0
  fi
  out=$(hack/check-cache.sh changed "$1" "$CHANGE_BASE" 2>/dev/null)
  case $? in
    0)
      n=$(wc -l <<<"$out")
      why="${out%%$'\n'*} changed"
      [ "$n" -le 1 ] || why+=", and $((n - 1)) more of its inputs"
      ;;
    1) skip="none of its inputs changed: $(hack/check-cache.sh inputs "$1")" ;;
    *) why="its inputs could not be compared with $CHANGE_FROM" ;;
  esac
}

# tier NAME: selects the tier by its inputs and runs it through the device's
# cache of tier passes.
tier() {
  local line
  if [ -n "$failed" ]; then
    step "$1"
    return 0
  fi
  inputs "$1"
  if [ -n "$why" ] && line=$(hack/check-cache.sh cached "$1"); then
    [ -n "$idle" ] || echo "$line"
    line=${line#"$1: cached ("}
    cached="$why; this device's cache answers: ${line%"); FORCE=1 to rerun"}"
  fi
  step "$1" hack/check-cache.sh run "$1" "$MAKE" --no-print-directory -o manifests "$1"
}

# scan NAME: one of the scans of the whole repository the shell tests make,
# by itself: when a file it reads changed, and the shell tests did not just
# run it with the others. It takes seconds, so no cache answers it.
scan() {
  if [ -n "$failed" ]; then
    step "$1"
    return 0
  fi
  inputs "$1"
  # A link names a file: one that is gone can break a link no Markdown
  # change made.
  if [ "$1" = doc-links ] && [ -z "$why" ] && [ -n "$gone" ]; then
    why="$gone is gone, which a link may name"
  fi
  if [ -n "$why" ] && [ -n "$shell_tests_ran" ]; then
    why=
    skip="the shell tests just ran it"
  fi
  light=1
  step "$1" "$MAKE" --no-print-directory "$1"
  light=
}

# report: the block the run ends with.
report() {
  local i head n=${#files[@]}
  if [ -n "$failed" ]; then head="make check: FAILED at $failed."; else head="make check: passed."; fi
  if [ -n "$CHANGE_ALL" ]; then
    head+=" Every step${CHECK_STEPS:+ asked for (CHECK_STEPS)} was selected: $CHANGE_ALL."
  else
    if [ "$n" = 1 ]; then head+=" 1 file"; else head+=" $n files"; fi
    head+=" changed since $CHANGE_FROM (${CHANGE_BASE:0:12});"
    head+=" ${CHECK_STEPS:+of the steps asked for (CHECK_STEPS), }the steps that change selects:"
  fi
  echo
  echo "$head"
  for i in "${!names[@]}"; do
    printf '  %-18s  %-7s  %s\n' "${names[$i]}" "${states[$i]}" "${details[$i]}"
  done
}

change_base
ran=()
rc_failed=0
cached=
light=
shell_tests_ran=
load
# The first file the change removes, if any.
gone=
[ -n "$all" ] || gone=$(git diff --name-only --no-renames --diff-filter=D "$CHANGE_BASE" -- 2>/dev/null)
gone=${gone%%$'\n'*}

if asked generate; then
  why= skip=
  if [ -n "$all" ]; then
    why=$all
  elif f=$(first "${GENERATE[@]}"); then
    why="$f changed"
  else
    skip="no Go file under api/, cmd/ or internal/ changed, nor go.mod, go.sum or a generated file"
  fi
  step "manifests generate" "$MAKE" --no-print-directory manifests generate
  # What they wrote counts for the steps below: a regenerated CRD selects
  # validate-yaml and the tests that load it.
  [ ${#ran[@]} -eq 0 ] || load
fi

if asked fmt || asked vet || asked lint; then
  go_scope
  why= skip=
  if [ ${#go_pkgs[@]} -gt 0 ]; then why=$go_why; else skip=$go_why; fi
  ! asked fmt || step fmt "$MAKE" --no-print-directory fmt GO_PACKAGES="${go_pkgs[*]}"
  if [ "${go_pkgs[*]}" != ./... ] && f=$(first "${MAKE_FILES[@]}"); then
    go_pkgs=(./...)
    why="every package: $f changed"
  fi
  ! asked vet || step vet "$MAKE" --no-print-directory vet GO_PACKAGES="${go_pkgs[*]}"
  if asked lint; then
    # The linter's configuration reaches every package, whatever gta selects.
    if [ "${go_pkgs[*]}" != ./... ] && f=$(first "${LINT_ALL[@]}"); then
      go_pkgs=(./...)
      why="every package: $f changed"
    fi
    [ -n "$why" ] || skip+=", and no linter configuration changed"
    lint_dirs
    step lint "$MAKE" --no-print-directory lint LINT_PACKAGES="${dirs[*]}"
  fi
fi

! asked validate-yaml || tier validate-yaml
! asked test-hack || tier test-hack
# The shell tests run the three scans below with the rest.
[ "${ran[*]:+${ran[-1]}}" != test-hack ] || shell_tests_ran=1
! asked doc-links || scan doc-links
! asked license-headers || scan license-headers
! asked readers || scan readers
! asked ui-test || [ ! -f ui/package.json ] || tier ui-test

if asked test || asked test-gitea; then
  # hack/test-packages.sh says on stderr which packages it chose and why. When
  # every step runs it tests every package, whatever it would choose.
  why= skip= cached= unselected=
  if [ -n "$failed" ]; then
    pkgs=
  elif pkgs=$(TEST_ALL=${TEST_ALL:-${all:+1}} hack/test-packages.sh 2>"$T/test.err"); then
    said=$(sed -n 's/^make test: testing //p' "$T/test.err" | tail -n 1)
    [ -z "$all" ] || said="every package: $all"
    if [ -n "$pkgs" ]; then why=$said; else skip=${said#no package: }; fi
  else
    cat "$T/test.err" >&2
    unselected="hack/test-packages.sh could not select the packages"
    why=$unselected
  fi
  ! asked test ||
    step test "$MAKE" --no-print-directory -o manifests -o generate -o fmt -o vet test TEST_PACKAGES="${pkgs//$'\n'/ }"
  if asked test-gitea; then
    # The Gitea adapter's tests against a real Gitea, seeded with templates/:
    # when make test tests that package (its code, what it builds on or the
    # templates changed), or what seeds and starts the local Gitea changed.
    gitea=internal/adapters/gitea
    why= skip=
    if [ -n "$all" ]; then
      why=$all
    elif [ -n "$unselected" ]; then
      why=$unselected
    elif f=$(first hack/gitea-local.sh 'hack/seed/*'); then
      why="$f changed"
    elif grep -q "/$gitea\$" <<<"$pkgs"; then
      why="make test tests $gitea"
    else
      skip="the change reaches neither $gitea, the seed nor the local Gitea's script"
    fi
    step test-gitea "$MAKE" --no-print-directory test-gitea
  fi
fi

if [ -n "$idle" ]; then
  echo idle
  exit 0
fi
if [ -z "$failed" ]; then
  if asked generate; then
    # shellcheck disable=SC2086 # GENERATED_PATHS is a list of paths.
    dirty=$(git status --porcelain --untracked-files=all -- $GENERATED_PATHS 2>/dev/null)
    [ -z "$dirty" ] || printf 'Generated files changed; commit them with your change:\n%s\n' "$dirty"
  fi
  # A pass of the tree is a pass of every step: a part of them records none.
  [ -n "${CHECK_STEPS:-}" ] || hack/check-cache.sh record
fi
report
exit "$rc_failed"
