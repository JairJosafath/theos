#!/usr/bin/env bash
# Tests for hack/test-packages.sh: a change to a leaf package tests it alone,
# one to a package others import tests them too, a docs-only change tests
# nothing, and the go line of go.mod, no merge base, TEST_ALL and FORCE test
# every package (#1295), the branch main no longer does (#127: only a release tests
# everything); a package below test/e2e is tested, a non-Go file or
# a //go:embed line also tests cmd (#1321); in a throwaway module and clone.
# digitalocean/gta selects the packages (hack/gta, built here once): a test
# file tests its package alone, a package whose tests alone import a changed
# one is tested with its own dependents, and a gta that fails tests every
# package (#127).
# A path of the readers table (templates/, config/, api/, the hack/ files Go
# tests read) tests the packages whose tests read it and not every package,
# nor the readers' dependents (#1523). A hack/ script no Go test reads tests
# no package (#1318).
#
# Against this repository, hack/check-readers.sh: every package the readers
# table names exists, and a _test.go that climbs to a file outside its
# package or names a hack/ path lies in a package the table lists for it
# (#1523, #1318, #66). No file tests every package but go.work and the lines
# of go.mod each is built by (go, toolchain): a file no Go test reads tests
# nothing, a Dockerfile, a workflow and the like the packages that read them,
# and a module whose version changed in go.mod or go.sum the packages built
# from it (#127).
# Run: hack/test-packages_test.sh
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GOWORK=off
unset TEST_ALL FORCE TEST_BASE TEST_READERS

# gta, built from this checkout: the throwaway module has no hack/gta.
(cd "$REPO" && go build -o "$T/gta" ./hack/gta) || { echo "FAIL hack/gta does not build"; exit 1; }
export THEOS_GTA=$T/gta

# A module: leaf (no imports), mid imports leaf, top imports mid, other alone,
# api/v1 imported by the tests of hack/tool alone, a package under hack/, cmd
# alone (it guards embeds), test/e2e, which make test never runs, and
# test/e2e/standin, which it does. It builds on two modules, which lie below
# it and are replaced, so that no proxy is asked: other imports dep, and the
# tests of api/v1 alone import testdep.
src=$T/src
git init -q -b main "$src"
cd "$src" || exit 1
mkdir -p hack/tool hack/lib leaf mid top other api/v1 cmd config docs leaf/testdata leaf/assets test/e2e/standin
cp "$HERE/test-packages.sh" hack/
cp "$HERE/lib/change.sh" hack/lib/
cat >go.mod <<'MOD'
module example.com/m

go 1.22

require (
	example.com/dep v0.1.0
	example.com/testdep v0.1.0 // indirect
)

replace example.com/dep => ./third_party/dep

replace example.com/testdep => ./third_party/testdep
MOD
for d in dep testdep; do
  mkdir -p third_party/$d
  printf 'module example.com/%s\n\ngo 1.22\n' $d >third_party/$d/go.mod
  echo "package $d" >third_party/$d/$d.go
done
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
printf 'package tool\nimport (\n\t"testing"\n\t_ "example.com/m/api/v1"\n)\nfunc TestX(t *testing.T) {}\n' >hack/tool/tool_test.go
printf 'package other\nimport _ "embed"\n//go:embed o.txt\nvar o string\n' >other/embed.go
printf 'package other\nimport _ "example.com/dep"\n' >other/dep.go
printf 'package v1\nimport (\n\t"testing"\n\t_ "example.com/testdep"\n)\nfunc TestX(t *testing.T) {}\n' >api/v1/v1_test.go
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
check "#127 a test file: its package alone, no other links it" "$(run)" "mid"

change mid/mid_test.go mid/mid.go
check "#127 a test file and a Go file: the package and its dependents" "$(run)" "mid top"

change leaf/leaf.go
check "#127 gta fails: every package" "$(THEOS_GTA=false run)" "$every"
check "#127 gta fails: says why" "$(grep -c 'every package (8): gta could not select the packages' "$T/err")" 1

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

# #127: a file no Go test reads tests nothing, on a device too: only a
# release tests every package. README.md, the Makefile and a workflow tested
# every package before, in case a test read them.
for f in README.md Makefile hack/testenv.mk .golangci.yml .github/workflows/t.yml; do
  change "$f"
  check "#127 $f, which no Go test reads: no package" "$(run)" ""
done

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

# #127: go.mod and go.sum test the packages built from a module whose version
# changed, not every package: gta's own command tests none for such a change,
# and every package was tested here.
change
sed -i 's|example.com/dep v0.1.0|example.com/dep v0.2.0|' go.mod
check "#127 go.mod, a version: the packages built from that module" "$(run)" "other"
check "#127 go.mod, a version: says which module" \
  "$(grep -c 'testing 1 of 8 packages.*built from the changed modules: example.com/dep$' "$T/err")" 1
change
sed -i 's|example.com/testdep v0.1.0|example.com/testdep v0.2.0|' go.mod
check "#127 go.mod, a module tests alone import: their package, not what imports it" "$(run)" "api/v1"
change leaf/leaf.go
sed -i 's|example.com/dep v0.1.0|example.com/dep v0.2.0|' go.mod
check "#127 go.mod and a Go file: the module's users, and what gta selects" "$(run)" "leaf mid other top"
change
echo 'example.com/dep v0.1.0 h1:x=' >go.sum
check "#127 go.sum, a module's files: the packages built from that module" "$(run)" "other"
change
echo 'example.com/dep v0.1.0/go.mod h1:x=' >go.sum
check "#127 go.sum, a module's go.mod alone: no package" "$(run)" ""
change go.mod
check "#127 go.mod, a comment: no package" "$(run)" ""
check "#127 go.mod, a comment: says why" "$(grep -c 'testing no package: none is built from a module go.mod changed' "$T/err")" 1
change
sed -i 's|^go 1.22$|go 1.23|' go.mod
check "go.mod, the go line: every package, each is built by it" "$(run)" "$every"
check "go.mod, the go line: says why" "$(grep -c "every package (8): the go line of go.mod changed" "$T/err")" 1
change go.work
check "go.work: every package" "$(run)" "$every"
check "go.work: says why" "$(grep -c "every package (8): go.work changed" "$T/err")" 1

change hack/agent/Dockerfile
check "#127 a Dockerfile: cmd, whose test reads every one" "$(run)" "cmd"
check "#127 a Dockerfile: says which pattern of the table" "$(grep -c 'testing 1 of 8 packages.*read by their tests: \*/Dockerfile' "$T/err")" 1
change leaf/testdata/in.txt
check "#127 a file below a package's testdata: that package alone, and cmd" "$(run)" "cmd leaf"

for f in hack/pull.sh hack/lib/github.sh; do
  change "$f"
  check "$f: no package (#1318)" "$(run)" ""
done

change hack/tool/assets/a.txt
check "a file in a package under hack/: that package and cmd (#1318, #1321)" "$(run)" "cmd hack/tool"

# The readers table (#1523): mid's and other's tests read templates/, top's
# config/, other's hack/seed/; nothing reads api/ as files; ghost holds no
# package.
cat >"$T/readers" <<EOF
templates mid other ghost
config top
api
hack/seed other
EOF
export TEST_READERS=$T/readers
check "the table prints" "$(hack/test-packages.sh --readers | paste -sd'|')" "api |config top|hack/seed other|templates mid other ghost"

change templates/x/a.txt
check "#1523 templates/<x>: the packages whose tests read templates, not their dependents" "$(run)" "mid other"
check "#1523 says which path" "$(grep -c 'testing 2 of 8 packages.*read by their tests: templates' "$T/err")" 1

change templates/x/a.txt templates/y/b.txt
check "#1523 two files under one path: its readers once" "$(run)" "mid other"

change templates/x/a.txt leaf/leaf.go
check "#1523 a read path and a changed package: the readers and the dependents" "$(run)" "leaf mid other top"
check "#1523 says both" "$(grep -c 'changed: leaf; read by their tests: templates' "$T/err")" 1

change config/crd.yaml
check "#1523 config/: its readers" "$(run)" "top"

change api/v1/v1.go
check "#1523 api/ has no readers: the package and the one whose tests import it, from gta (#127)" "$(run)" "api/v1 hack/tool"

change hack/seed/catalog.sh
check "#1523 a hack/ file of the table: its readers" "$(run)" "other"

unset TEST_READERS
change config/crd.yaml
check "#1523 the table's readers not in the module: no package" "$(run)" ""

# A path of the table with a * is a pattern, and a file tests the readers of
# every path it matches (#127).
cat >"$T/readers" <<EOF
*.go top
hack/seed other
*/Dockerfile mid
EOF
export TEST_READERS=$T/readers
change leaf/leaf_test.go
check "#127 a pattern of the table: its readers" "$(run)" "leaf top"
change hack/seed/Dockerfile
check "#127 a file two paths of the table match: the readers of both" "$(run)" "mid other"
change docs/guide.md
check "#127 a pattern the file doesn't match: no package" "$(run)" ""
unset TEST_READERS

# Against this repository: hack/check-readers.sh holds the table to the Go
# tests (#1523, #1318, #127). It passes on the table as it is, and fails on
# one without the fixture internal/model's tests share with the UI's (#66),
# naming the test that climbs to it.
out=$("$HERE/check-readers.sh" 2>&1)
check "the readers table holds what this repository's Go tests read" "$?" 0
grep '^check-readers: ' <<<"$out" | sed 's/^/     /'
(cd "$REPO" && hack/test-packages.sh --readers | grep -v '^ui/src/model/testdata ') >"$T/readers"
check "the table without that fixture is one line shorter" \
  "$(wc -l <"$T/readers")" "$(($(cd "$REPO" && hack/test-packages.sh --readers | wc -l) - 1))"
out=$(TEST_READERS=$T/readers "$HERE/check-readers.sh" 2>&1)
check "#66 a test that climbs to a file the table doesn't name fails the check" "$?" 1
check "#66 it names the test and the file" \
  "$(grep -c "internal/model's tests climb to ui/src/model/testdata/report, and the table doesn't list internal/model for it" <<<"$out")" 1
(cd "$REPO" && hack/test-packages.sh --readers | sed 's|^hack/go-test.sh .*|hack/go-test.sh cmd|') >"$T/readers"
out=$(TEST_READERS=$T/readers "$HERE/check-readers.sh" 2>&1)
check "#1318 a hack/ path a test names, listed for another package: fails" "$?" 1
check "#1318 it names the test and the path" \
  "$(grep -c "internal/controller's tests name hack/go-test.sh, and the table doesn't list internal/controller for it" <<<"$out")" 1

change top/top.go
check "TEST_ALL: every package" "$(TEST_ALL=1 run)" "$every"
check "FORCE: every package" "$(FORCE=1 run)" "$every"
check "no merge base: every package" "$(TEST_BASE=nope run)" "$every"
check "says no merge base" "$(grep -c 'no merge base with nope' "$T/err")" 1
# #127: every test is partial, against main, on the branch main too: there it
# is what main holds that origin/main doesn't. Only a release tests everything.
git switch -q main
check "#127 main, an uncommitted change: that change" "$(run)" "top"
check "#127 main: says what it compares with" "$(grep -c 'testing 1 of 8 packages.*since origin/main' "$T/err")" 1

# #2058: the factory's CI checks out a detached commit; a push to main sets
# TEST_BASE to main's previous head, so only what the push changed is tested.
change top/top.go
git commit -qam three
check "#127 main, a commit origin/main lacks: that change" "$(run)" "top"
head=$(git rev-parse HEAD)
git reset -q --hard origin/main
check "#127 main, as origin/main: no package" "$(run)" ""
git reset -q --hard "$head"
git switch -q --detach "$head"
check "#2058 detached, TEST_BASE a commit: what changed since it" "$(TEST_BASE=HEAD~1 run)" "top"
check "#2058 detached, says the base" "$(grep -c 'testing 1 of 8 packages.*since HEAD~1' "$T/err")" 1
check "#2058 detached, TEST_BASE the head itself: no package" "$(TEST_BASE=$head run)" ""
check "#2058 detached, TEST_BASE a missing commit: every package" "$(TEST_BASE=0000000000000000000000000000000000000000 run)" "$every"
git switch -q main

exit $fail
