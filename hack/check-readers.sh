#!/usr/bin/env bash
# Checks the readers table of hack/test-packages.sh against the Go tests of
# the repository: the table says which packages' tests read a file without
# importing it, and make test and make check test those packages and no
# others when the file changes (#127). No path tests every package any more,
# so a file a test reads and the table doesn't name would test nothing until
# a release. Prints each test the table misses and exits 1.
#
#   - Every package the table names has tests.
#   - A _test.go that climbs to a path of the table ("../../templates",
#     filepath.Join("..", "..", "config", ...), filepath.Join(root, "dist", ...))
#     lies in a package the table lists for it (#1523, #1716).
#   - Every path a Go test climbs to, in a string ("../x") or a
#     filepath.Join("..", ...), that is a file or directory of the repository
#     outside the test's own package is listed for that package: the report
#     fixture internal/model shares with the UI's tests was read by a Go test
#     and selected none (#66). A package's Go sources are gta's and the *.go
#     pattern's; .dev and bin hold tools.
#   - Every hack/ path a Go test names, in a string or a filepath.Join, lies
#     in a package or is listed for that test's package, so a new test that
#     reads a script can't be skipped (#1318). A directory gets a file in it.
#
# make check runs it when a _test.go or the table changed (hack/check.sh), and
# hack/test-packages_test.sh runs it with the shell tests.
#
#   hack/check-readers.sh   the repository this script lies in
#
#   TEST_READERS   a file replacing the table (hack/test-packages.sh)
set -uo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO" || exit 1
fail=0
bad() {
  echo "check-readers: $*"
  fail=1
}

mapfile -t table < <(hack/test-packages.sh --readers)
[ ${#table[@]} -gt 5 ] || bad "hack/test-packages.sh --readers prints ${#table[@]} paths; the table is gone"

# covers KEY PATH: the table's path KEY is PATH, holds it, or is a pattern
# PATH matches.
covers() {
  # shellcheck disable=SC2053 # A path with a * in it is a pattern.
  [ "$2" = "$1" ] || [[ "$2" == "$1"/* ]] || { [[ $1 == *'*'* ]] && [[ $2 == $1 ]]; }
}
# listed PATH DIR: a path of the table that covers PATH lists package DIR.
listed() {
  local line
  for line in "${table[@]}"; do
    covers "${line%% *}" "$1" || continue
    [[ " ${line#* } " == *" $2 "* ]] && return 0
  done
  return 1
}
# reads PATH: the directories of the _test.go files that climb to PATH:
# "../../PATH", or "..", "PATH-segments" or root, "PATH-segments".
reads() {
  local segs
  segs=$(sed 's|/|", *"|g' <<<"$1")
  git ls-files '*_test.go' |
    xargs grep -lE "\"(\.\./)+$1(/|\")|(\.\.\", *|root, *)\"$segs\"" | xargs -rn1 dirname | sort -u
}

for line in "${table[@]}"; do
  path=${line%% *}
  for p in ${line#* }; do
    ls "$p"/*_test.go >/dev/null 2>&1 || bad "$path: the table lists $p, which is no package with tests"
  done
  case "$path" in hack/* | *'*'*) continue ;; esac
  for d in $(reads "$path"); do
    listed "$path" "$d" || bad "$d's tests read $path, and the table doesn't list $d for it"
  done
done
[ "$(reads templates | grep -c '^internal/scaffold$')" = 1 ] ||
  bad "internal/scaffold's tests no longer show as climbing to templates/: the check reads the tests wrongly"

climbs=0
while IFS=: read -r file lit; do
  dir=$(dirname "$file")
  target=$(realpath -m --relative-to="$REPO" "$REPO/$dir/$lit")
  case $target in . | ..* | .dev | .dev/* | bin | bin/* | "$dir" | "$dir"/*) continue ;; esac
  [ -e "$target" ] || continue
  if [ -d "$target" ] && ls "$target"/*.go >/dev/null 2>&1; then continue; fi
  climbs=$((climbs + 1))
  listed "$target" "$dir" || bad "$dir's tests climb to $target, and the table doesn't list $dir for it"
done < <(git ls-files '*_test.go' |
  xargs grep -oE '"(\.\./)+[A-Za-z0-9._/-]+"|"\.\."(, *"[A-Za-z0-9._-]+")+' |
  sed -E 's/", *"/\//g' | tr -d '"' | sort -u)
[ "$climbs" -gt 5 ] || bad "only $climbs paths show as climbed to: the check reads the tests wrongly"

# in_package PATH: a directory of PATH or above it, under hack/, holds Go
# files other than tests.
in_package() {
  local d=$1
  [ -d "$d" ] || d=$(dirname "$d")
  while [ "$d" != hack ] && [ "$d" != . ]; do
    [ -z "$(find "$d" -maxdepth 1 -name '*.go' ! -name '*_test.go' 2>/dev/null)" ] || return 0
    d=$(dirname "$d")
  done
  return 1
}
named=0
while read -r d f; do
  named=$((named + 1))
  in_package "$f" && continue
  [ ! -d "$f" ] || f=$f/x
  listed "$f" "$d" || bad "$d's tests name $f, and the table doesn't list $d for it"
done < <(git ls-files '*_test.go' | xargs grep -oE \
  'hack/[A-Za-z0-9._/-]*|"hack"(, *"[A-Za-z0-9._-]+")+' |
  sed -E 's/", *"/\//g; s/"//g; s/\.+$//; s|/$|/x|; s|^([^:]*)/[^/:]*:|\1 |' | sort -u)
[ "$named" -gt 0 ] || bad "no Go test shows as naming a hack/ path: the check reads the tests wrongly"

[ "$fail" != 0 ] || echo "check-readers: the readers table holds what the Go tests read ($climbs paths climbed to, $named hack/ paths named)"
exit $fail
