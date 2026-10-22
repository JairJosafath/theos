#!/usr/bin/env bash
# Prints the Go packages make test tests, one per line: those whose tests a
# change since the merge base with TEST_BASE can affect, else every package.
# Says on stderr which it chose and why, so a skipped package shows in the
# output (#1295).
#
# A package is affected when a file in it changed (its directory, or one below
# it, for embedded files) or its test binary links one that did, so its
# dependents are tested too. The change is everything since the merge base:
# commits, staged, unstaged and untracked files. A changed non-Go file in a
# package, or a changed //go:embed line, also tests cmd, whose
# TestDockerContextHasEmbeddedFiles checks the embeds of every package a
# Dockerfile builds without linking them (#1321).
#
# A file some tests read without importing it (templates/, config/, dist/,
# test/crds, a few hack/ files) tests the packages whose tests read it, the
# readers table below, and only those: a reader's dependents don't read the
# file (#1523). hack/test-packages_test.sh checks the table against the
# _test.go files: one that climbs to such a path ("../../templates",
# filepath.Join("..", "..", "config", ...), filepath.Join(root, "dist", ...))
# or names a hack/ path lies in a package the table lists.
#
# A file directly under test/e2e, the e2e suite, tests nothing; one in a
# package below it (test/e2e/standin) tests that package (#1321).
#
# Every package is tested when:
#   - TEST_ALL or FORCE is set (FORCE=1 make check reruns all of it),
#   - the branch is main, or no merge base with TEST_BASE is found,
#   - a file changed that a Go test may read without importing it and the
#     readers table doesn't map: go.mod, go.sum, the Makefile, testdata, the
#     workflows, a Dockerfile, docs/getting-started.md, README.md, or any file
#     outside the paths below.
# A change only to paths no Go test reads (docs/, .claude/, .vscode/, ui/,
# the other .github/ files, the top-level *.md but README.md, the rest of
# hack/ outside its packages) tests nothing. The shell scripts in hack/ have
# their own gate, make test-hack (#1318).
#
# The digitalocean/gta tool does the same selection, but loads every package
# of every module in the build list (go list ...), which this module's go.mod
# can't do without changes (go mod tidy wants more requirements) and which
# takes a minute where go list -test ./... takes half a second.
#
#   TEST_BASE     the branch to diff against (default origin/main)
#   TEST_ALL      non-empty: every package
#   FORCE         non-empty: every package
#   TEST_READERS  a file replacing the readers table, a line per path:
#                 PATH PACKAGE-DIR... (for the tests of this script)
#   --readers     print the readers table in that format and exit
set -euo pipefail

BASE=${TEST_BASE:-origin/main}
say() { echo "make test: $*" >&2; }

# readers: a path (a directory or file, relative to the module root) Go tests
# read without importing it, and the directories of the packages whose tests
# do. api/ holds only the package api/v1alpha1, whose importers the graph
# finds; no test reads it as files. A listed directory that holds no package
# of the module is ignored.
declare -A readers=(
  [templates]="hack/template-go-kubebuilder internal/controller internal/gitprovider internal/scaffold test/chart"
  [config]="internal/controller internal/model internal/sessionclient test/chart test/compat"
  [api]=""
  [dist]="internal/model test/chart"
  [test/crds]="internal/controller internal/adapters/argocd"
  [hack/seed]="test/chart"
  [hack/boilerplate.go.txt]="hack/template-go-kubebuilder"
  [hack/new.sh]="hack/template-go-kubebuilder"
  [hack/setup.sh]="internal/gitprovider"
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

[ -z "${TEST_ALL:-}" ] || full "TEST_ALL is set"
[ -z "${FORCE:-}" ] || full "FORCE is set"
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
[ "$branch" != main ] || full "the branch is main"
base=$(git merge-base "$BASE" HEAD 2>/dev/null) || full "no merge base with $BASE"

mapfile -t files < <({
  git diff --name-only --no-renames "$base" --
  git ls-files --others --exclude-standard
} | sort -u)

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

declare -A changed=() read=() readpaths=()
for f in "${files[@]}"; do
  case "$f" in
    go.mod | go.sum | go.work | go.work.sum | Makefile | *.mk | testdata/* | */testdata/* | \
      .github/workflows/* | Dockerfile | */Dockerfile | *.Dockerfile | .dockerignore | \
      docs/getting-started.md | README.md)
      full "$f changed, which a Go test may read without importing it"
      ;;
  esac
  # A path of the readers table: its readers, and nothing more unless the file
  # lies in a package.
  tabled=
  for k in "${!readers[@]}"; do
    if [ "$f" = "$k" ] || [[ "$f" == "$k"/* ]]; then
      tabled=$k
      readpaths[$k]=1
      for r in ${readers[$k]}; do
        [ -z "${dirs[$r]:-}" ] || read[${dirs[$r]}]=1
      done
      break
    fi
  done
  # The nearest directory holding a package owns the file: a Go file, or one
  # it embeds or its tests read. The e2e suite's directory owns what is
  # directly under it, which tests nothing.
  d=$(dirname "$f")
  while [ -z "${dirs[$d]:-}" ] && [ "$d" != . ] && [ "$d" != test/e2e ]; do d=$(dirname "$d"); done
  if [ -n "${dirs[$d]:-}" ]; then
    changed[${dirs[$d]}]=1
    if [ -n "${dirs[cmd]:-}" ] && { [[ "$f" != *.go ]] || embeds "$f"; }; then
      changed[${dirs[cmd]}]=1
    fi
    continue
  fi
  [ -z "$tabled" ] || continue
  case "$f" in
    test/e2e/*) continue ;;
    # A Go file in no package: a deleted package's, whose importers changed too.
    *.go) [ -e "$f" ] || continue ;;
    docs/* | .claude/* | .vscode/* | ui/* | .github/* | hack/*) continue ;;
  esac
  [[ "$f" == */* || "$f" != *.md ]] || continue
  full "$f changed, which a Go test may read without importing it"
done

if [ ${#changed[@]} -eq 0 ] && [ ${#read[@]} -eq 0 ]; then
  say "testing no package: no Go package changed since $BASE (${base:0:12})"
  exit 0
fi

# The readers of the changed paths, and each test binary (P.test) linking a
# changed package; the graph lists every package a binary links, test
# variants ("P [P.test]") under their plain names.
declare -A picked=()
for p in "${!read[@]}"; do picked[$p]=1; done
if [ ${#changed[@]} -gt 0 ]; then
  graph=$(go list -test -f '{{.ImportPath}}{{range .Deps}} {{.}}{{end}}' "${all[@]}") ||
    full "go list -test failed"
  while read -r bin deps; do
    p=${bin%.test}
    hit=${changed[$p]:-}
    if [ -z "$hit" ]; then
      for dep in $deps; do
        if [ -n "${changed[$dep]:-}" ]; then
          hit=1
          break
        fi
      done
    fi
    [ -z "$hit" ] || picked[$p]=1
  done < <(grep '^[^ ]*\.test\( \|$\)' <<<"$graph" | sed -E 's/ \[[^]]*\]//g')
fi

short() { printf '%s\n' "$@" | sort | sed "s|^$module/||" | paste -sd' '; }
why=()
[ ${#changed[@]} -eq 0 ] || why+=("changed: $(short "${!changed[@]}")")
[ ${#read[@]} -eq 0 ] || why+=("read by their tests: $(short "${!readpaths[@]}")")
say "testing ${#picked[@]} of ${#all[@]} packages, those the change since $BASE (${base:0:12}) affects; $(printf '%s; ' "${why[@]}" | sed 's/; $//')"
[ ${#picked[@]} -eq 0 ] || printf '%s\n' "${!picked[@]}" | sort
