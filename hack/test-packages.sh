#!/usr/bin/env bash
# Prints the Go packages make test tests, one per line: those whose tests a
# change since the merge base with TEST_BASE can affect, else every package.
# Says on stderr which it chose and why, so a skipped package shows in the
# output (#1295).
#
# digitalocean/gta selects them (hack/gta, #127): a package is affected when a
# file in it changed (its directory, or one below it down to the next
# package, for the files it embeds or its tests read beside them) or it
# depends on one that did, through its tests too. A package whose _test.go
# files alone changed is tested alone: no other package links them. The
# change is everything since the merge base: commits, staged, unstaged and
# untracked files (hack/lib/change.sh). A changed non-Go file in a package, or
# a changed //go:embed line, also tests cmd, whose
# TestDockerContextHasEmbeddedFiles checks the embeds of every package a
# Dockerfile builds without linking them (#1321).
#
# gta knows the Go files and what they embed, nothing else. A file some tests
# read without importing it (templates/, config/, dist/, test/crds, the
# workflows, a Dockerfile, a few hack/ files) tests the packages whose tests
# read it, the readers table below, and only those: a reader's dependents
# don't read the file (#1523). The table is what the tests really open: it
# was checked against Go's own test log of every package (#127,
# docs/plans/done/selective-check.md). hack/test-packages_test.sh checks it
# against the _test.go files: one that climbs to such a path
# ("../../templates", filepath.Join("..", "..", "config", ...),
# filepath.Join(root, "dist", ...)) or names a hack/ path lies in a package
# the table lists.
#
# A file directly under test/e2e, the e2e suite, tests nothing; one in a
# package below it (test/e2e/standin) tests that package (#1321). A file
# below a package's testdata tests that package alone, as gta has it, and the
# other packages the table lists for it.
#
# Every test is partial, against main, on a device and on the branch main
# too: only a release tests every package, by asking (#127). So every package
# is tested only when:
#   - TEST_ALL or FORCE is set (FORCE=1 make check, which a release runs),
#   - no merge base with TEST_BASE is found: the change can't be known,
#   - gta fails: when in doubt, more is tested.
# No file tests every package. go.mod, go.sum and go.work test none, as gta
# has it and as schubergphilis/mcvs-golang-action runs it: a change to them
# alone is in no package, so a bump of a module or of the toolchain is tested
# at the next release, not on the pull request
# (docs/plans/done/go-mod-tests-nothing.md). They tested every package, and
# the controller suite alone is a quarter of an hour.
# A change to a file no Go test reads tests nothing: docs/, .claude/,
# .vscode/, ui/, the other .github/ files, the top-level *.md, the Makefile,
# the rest of hack/ outside its packages, and any file the table doesn't
# name. It used to test every package for the Makefile, a workflow, a
# Dockerfile, README.md and a file in no listed path, in case a test read
# them. The shell scripts in hack/ have their own gate, make test-hack
# (#1318).
#
#   TEST_BASE     the branch to diff against (default origin/main)
#   TEST_ALL      non-empty: every package
#   FORCE         non-empty: every package
#   TEST_READERS  a file replacing the readers table, a line per path:
#                 PATH PACKAGE-DIR... (for the tests of this script)
#   THEOS_GTA     the command that runs gta (default: go run ./hack/gta; the
#                 tests of this script build it once)
#   --readers     print the readers table in that format and exit
set -euo pipefail

# shellcheck source=hack/lib/change.sh
. "$(dirname "$0")/lib/change.sh"
say() { echo "make test: $*" >&2; }

# readers: a path (a directory or file, relative to the module root) Go tests
# read without importing it, and the directories of the packages whose tests
# do. A path with a * in it is a pattern for the whole path, as in a case
# statement. api/ holds the package api/v1beta1, whose dependents gta finds;
# internal/scaffold's tests read its License enum as a file (#1788),
# since the layering forbids the import. A listed directory that holds no
# package of the module is ignored. A file tests the readers of every path
# it matches.
#
# internal/ports reads every Go file of the repository: its TestHostNeutral
# counts what names Gitea outside the Gitea adapter. cmd reads every
# Dockerfile and .dockerignore: its TestDockerContextHasEmbeddedFiles. The
# tests of test/chart read the Release and Verify Release workflows, the
# guide and the UI's Dockerfile; those of internal/model the CloudFormation
# adapter's testdata and the report fixture the UI's tests share with them
# (#66), which cmd/theos-session's read too, for theos-session report's
# mapping, and test/e2e/standin the e2e fixtures.
declare -A readers=(
  [templates]="hack/template-go-kubebuilder internal/controller internal/adapters/gitea internal/model internal/scaffold test/chart"
  [config]="internal/controller internal/model internal/sessionclient test/chart test/compat"
  [api]="internal/scaffold"
  [dist]="internal/model internal/scaffold test/chart"
  [test/crds]="internal/controller internal/adapters/argocd"
  [test/e2e/testdata]="test/e2e/standin"
  [internal/adapters/cloudformation/testdata]="internal/model"
  [ui/src/model/testdata]="internal/model cmd/theos-session"
  ['*.go']="internal/ports"
  [Dockerfile]="cmd"
  ['*/Dockerfile']="cmd"
  ['*.Dockerfile']="cmd"
  [.dockerignore]="cmd"
  ['*.dockerignore']="cmd"
  [ui/Dockerfile]="test/chart"
  [.github/workflows]="test/chart"
  [docs/getting-started.md]="test/chart"
  [hack/seed]="test/chart"
  [hack/boilerplate.go.txt]="hack/template-go-kubebuilder internal/scaffold"
  [hack/boilerplate.go.txt.tmpl]="hack/template-go-kubebuilder"
  [hack/new.sh]="hack/template-go-kubebuilder"
  [hack/setup.sh]="internal/adapters/git"
  [hack/go-test.sh]="internal/controller"
  [hack/release.sh]="test/compat"
)
if [ -n "${TEST_READERS:-}" ]; then
  readers=()
  while read -r path pkgs; do
    [ -z "$path" ] || readers[$path]=$pkgs
  done <"$TEST_READERS"
fi
if [ "${1:-}" = --readers ]; then
  for k in "${!readers[@]}"; do echo "$k ${readers[$k]}"; done | sort
  exit 0
fi

# The module's packages, but the e2e tests, which the e2e target runs.
list=$(go list ./...)
mapfile -t all < <(grep -v '/test/e2e$' <<<"$list")

full() {
  say "testing every package (${#all[@]}): $1"
  printf '%s\n' "${all[@]}"
  exit 0
}

change_base
[ -z "$CHANGE_ALL" ] || full "$CHANGE_ALL"
BASE=$CHANGE_FROM
base=$CHANGE_BASE
mapfile -t files < <(change_files)

# The module's directories that hold a package, relative to the top.
module=$(go list -m)
declare -A dirs=()
for p in "${all[@]}"; do
  d=${p#"$module"}
  d=${d#/}
  dirs[${d:-.}]=$p
done

# embeds FILE: a //go:embed line of the Go FILE changed since the merge base,
# or FILE is new and has one.
embeds() {
  if git cat-file -e "$base:$1" 2>/dev/null; then
    git diff "$base" -- "$1" | grep '^[-+][[:space:]]*//go:embed' >/dev/null
  else
    grep -q '^[[:space:]]*//go:embed' "$1" 2>/dev/null
  fi
}

# owned: the changed files that lie in a package, for gta to say which
# packages they affect. guard: one of them makes cmd check the embeds.
declare -A read=() readpaths=()
owned=()
guard=
for f in "${files[@]}"; do
  # The paths of the readers table the file matches: their readers, and
  # nothing more unless the file lies in a package.
  for k in "${!readers[@]}"; do
    # shellcheck disable=SC2053 # A path with a * in it is a pattern.
    if [ "$f" = "$k" ] || [[ "$f" == "$k"/* ]] || { [[ $k == *'*'* ]] && [[ $f == $k ]]; }; then
      for r in ${readers[$k]}; do
        if [ -n "${dirs[$r]:-}" ]; then
          read[${dirs[$r]}]=1
          readpaths[$k]=1
        fi
      done
    fi
  done
  # The nearest directory holding a package owns the file: a Go file, or one
  # it embeds or its tests read. The e2e suite's directory owns what is
  # directly under it, which tests nothing.
  d=$(dirname "$f")
  while [ -z "${dirs[$d]:-}" ] && [ "$d" != . ] && [ "$d" != test/e2e ]; do d=$(dirname "$d"); done
  if [ -n "${dirs[$d]:-}" ]; then
    owned+=("$f")
    if [ -n "${dirs[cmd]:-}" ] && { [[ "$f" != *.go ]] || embeds "$f"; }; then
      guard=1
    fi
  fi
  # A file in no package and in no path of the table: no Go test reads it.
done

if [ ${#owned[@]} -eq 0 ] && [ ${#read[@]} -eq 0 ]; then
  say "testing no package: no Go package changed since $BASE (${base:0:12})"
  exit 0
fi

# The readers of the changed paths, and what gta selects for the files in
# packages: the packages they lie in ("changed") and the packages that depend
# on those ("dependent"). The e2e suite is not among the packages tested.
declare -A known=() changed=() picked=()
for p in "${all[@]}"; do known[$p]=1; done
for p in "${!read[@]}"; do picked[$p]=1; done
if [ ${#owned[@]} -gt 0 ]; then
  # shellcheck disable=SC2086 # THEOS_GTA is a command with its arguments.
  selected=$(printf '%s\n' "${owned[@]}" | ${THEOS_GTA:-go run ./hack/gta}) ||
    full "gta could not select the packages"
  while read -r kind p; do
    [ -n "$p" ] && [ -n "${known[$p]:-}" ] || continue
    picked[$p]=1
    [ "$kind" != changed ] || changed[$p]=1
  done <<<"$selected"
  if [ -n "$guard" ]; then
    changed[${dirs[cmd]}]=1
    picked[${dirs[cmd]}]=1
  fi
fi

short() { printf '%s\n' "$@" | sort | sed "s|^$module/||" | paste -sd' '; }
why=()
[ ${#changed[@]} -eq 0 ] || why+=("changed: $(short "${!changed[@]}")")
[ ${#read[@]} -eq 0 ] || why+=("read by their tests: $(short "${!readpaths[@]}")")
if [ ${#picked[@]} -eq 0 ]; then
  say "testing no package: the files changed since $BASE (${base:0:12}) are in no package that is left"
  exit 0
fi
say "testing ${#picked[@]} of ${#all[@]} packages, those the change since $BASE (${base:0:12}) affects; $(printf '%s; ' "${why[@]}" | sed 's/; $//')"
printf '%s\n' "${!picked[@]}" | sort
