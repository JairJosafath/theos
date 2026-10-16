#!/usr/bin/env bash
# Tests for hack/test-packages.sh: a change to a leaf package tests it alone,
# one to a package others import tests them too, a docs-only change tests
# nothing, and go.mod, api/, config/, the hack/ files Go tests read,
# testdata, main, no merge base, TEST_ALL and FORCE test every package
# (#1295); a package below test/e2e is tested, a non-Go file or a //go:embed
# line also tests cmd (#1321); in a throwaway module and clone. A hack/ script
# no Go test reads tests no package, and every hack/ path a Go test of this
# repository names tests every package or lies in a package (#1318).
# Run: hack/test-packages_test.sh
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GOWORK=off
unset TEST_ALL FORCE TEST_BASE

# A module: leaf (no imports), mid imports leaf, top imports mid, other alone,
# api imported by nothing, cmd alone (it guards embeds), hack/tool a package
# under hack/, test/e2e, which make test never runs, and test/e2e/standin,
# which it does.
src=$T/src
git init -q -b main "$src"
cd "$src" || exit 1
mkdir -p hack/tool leaf mid top other api/v1 cmd config docs leaf/testdata leaf/assets test/e2e/standin
cp "$HERE/test-packages.sh" hack/
printf 'module example.com/m\n\ngo 1.22\n' >go.mod
pkg() { # pkg DIR NAME [IMPORT]
  { echo "package $2"
    [ -z "${3:-}" ] || echo "import _ \"example.com/m/$3\""; } >"$1/$2.go"
  printf 'package %s\nimport "testing"\nfunc TestX(t *testing.T) {}\n' "$2" >"$1/$2_test.go"
}
pkg leaf leaf
pkg mid mid leaf
pkg top top mid
pkg other other
pkg api/v1 v1
pkg hack/tool tool
pkg test/e2e e2e
pkg test/e2e/standin standin
pkg cmd main
printf 'package other\nimport _ "embed"\n//go:embed o.txt\nvar o string\n' >other/embed.go
echo x >other/o.txt
echo x >config/crd.yaml
echo x >docs/guide.md
echo x >leaf/testdata/in.txt
echo x >leaf/assets/a.txt
git add -A && git commit -qm one
git clone -q --bare "$src" "$T/origin.git"
git clone -q "$T/origin.git" "$T/clone"
cd "$T/clone" || exit 1
git switch -qc feature

# run: the packages, short names, space-separated; the reason in $T/err.
run() { hack/test-packages.sh 2>"$T/err" | sed 's|^example.com/m/||' | paste -sd' '; }
every="api/v1 cmd hack/tool leaf mid other test/e2e/standin top"
# change FILE...: the clone back at main, then each FILE changed.
change() {
  git reset -q --hard origin/main && git clean -qfd
  for f in "$@"; do mkdir -p "$(dirname "$f")"; echo "// c" >>"$f"; done
}

change top/top.go
check "leaf of the graph: itself" "$(run)" "top"
check "says which and why" "$(grep -c 'testing 1 of 8 packages.*changed: top' "$T/err")" 1

change leaf/leaf.go
check "imported package: its dependents too" "$(run)" "leaf mid top"

change mid/mid_test.go
check "a test file: its package and dependents" "$(run)" "mid top"

change leaf/assets/a.txt
check "#1321 a non-Go file below a package: that package and cmd" "$(run)" "cmd leaf mid top"

change
echo 'package other' >other/new.go
check "an untracked file counts" "$(run)" "other"

change top/top.go
git commit -qam two
check "a commit counts" "$(run)" "top"

change docs/guide.md
check "docs only: no package" "$(run)" ""
check "says no package" "$(grep -c 'testing no package' "$T/err")" 1

change README.md
check "README.md: every package" "$(run)" "$every"

change test/e2e/e2e.go
check "e2e only: no package" "$(run)" ""

change test/e2e/standin/standin.go
check "#1321 a package below test/e2e: itself" "$(run)" "test/e2e/standin"

change other/embed.go
check "#1321 a Go file whose //go:embed is unchanged: no cmd" "$(run)" "other"

change other/o.txt
check "#1321 an embedded file: its package and cmd" "$(run)" "cmd other"

change
sed -i 's|o.txt|*.txt|' other/embed.go
check "#1321 a changed //go:embed line: its package and cmd" "$(run)" "cmd other"

change
printf 'package top\nimport _ "embed"\n//go:embed top.go\nvar s string\n' >top/new.go
check "#1321 a new file with //go:embed: its package and cmd" "$(run)" "cmd top"

for f in go.mod api/v1/v1.go config/crd.yaml hack/seed/catalog.sh hack/boilerplate.go.txt hack/agent/Dockerfile leaf/testdata/in.txt Makefile .github/workflows/t.yml; do
  change "$f"
  check "$f: every package" "$(run)" "$every"
  check "$f: says why" "$(grep -c "every package (8): $f changed" "$T/err")" 1
done

for f in hack/relay.sh hack/lib/github.sh; do
  change "$f"
  check "$f: no package (#1318)" "$(run)" ""
done

change hack/tool/assets/a.txt
check "a file in a package under hack/: that package and cmd (#1318, #1321)" "$(run)" "cmd hack/tool"

# Every hack/ path a Go test of this repository names, in a string or a
# filepath.Join, tests every package or lies in a package, so a new test that
# reads a script can't be skipped (#1318). A directory gets a file in it.
mapfile -t named < <(cd "$REPO" && git ls-files '*_test.go' | xargs grep -ohE \
  'hack/[A-Za-z0-9._/-]*|"hack"(, *"[A-Za-z0-9._-]+")+' |
  sed -E 's/", *"/\//g; s/"//g; s/\.+$//; s|/$|/x|' | sort -u)
check "Go tests name hack/ paths" "$([ ${#named[@]} -gt 0 ] && echo yes)" yes
# in_package PATH: a directory of PATH or above it, under hack/, holds Go
# files other than tests.
in_package() {
  local d=$1
  [ -d "$REPO/$d" ] || d=$(dirname "$d")
  while [ "$d" != hack ] && [ "$d" != . ]; do
    if find "$REPO/$d" -maxdepth 1 -name '*.go' ! -name '*_test.go' | grep -q .; then return 0; fi
    d=$(dirname "$d")
  done
  return 1
}
for f in "${named[@]}"; do
  if in_package "$f"; then echo "ok   $f: in a package"; continue; fi
  [ ! -d "$REPO/$f" ] || f=$f/x
  change "$f"
  check "$f, named by a Go test: every package" "$(run)" "$every"
done

change top/top.go
check "TEST_ALL: every package" "$(TEST_ALL=1 run)" "$every"
check "FORCE: every package" "$(FORCE=1 run)" "$every"
check "no merge base: every package" "$(TEST_BASE=nope run)" "$every"
check "says no merge base" "$(grep -c 'no merge base with nope' "$T/err")" 1
git switch -q main
check "main: every package" "$(run)" "$every"

exit $fail
