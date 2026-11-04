#!/usr/bin/env bash
# Tests for hack/go-changed.sh (#2058): a change to a Go file, go.mod, go.sum or
# a linter config counts, a docs-only change does not, and TEST_ALL, FORCE and a
# missing merge base count as a change; on a detached checkout with TEST_BASE a
# commit, as the factory's CI has on a push to main.
# Run: hack/go-changed_test.sh
HERE=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
unset TEST_ALL FORCE TEST_BASE

git init -q -b main "$T/r"
cd "$T/r" || exit 1
mkdir -p docs hack pkg
cp "$HERE/go-changed.sh" hack/
echo x >docs/a.md
echo x >pkg/a.go
git add -A && git commit -qm one
git update-ref refs/remotes/origin/main HEAD
git switch -qc feature

# changed FILE: 1 when hack/go-changed.sh says Go changed, else 0.
changed() { if hack/go-changed.sh 2>"$T/err"; then echo 1; else echo 0; fi; }
reset() { git reset -q --hard origin/main && git clean -qfd; }
touch_file() { mkdir -p "$(dirname "$1")"; echo c >>"$1"; }

reset; touch_file docs/a.md
check "docs only: no lint" "$(changed)" 0
for f in pkg/a.go go.mod go.sum .golangci.yml .custom-gcl.yml; do
  reset; touch_file "$f"
  check "$f: lint" "$(changed)" 1
done
reset; touch_file pkg/new.go
check "an untracked Go file counts" "$(changed)" 1
reset; touch_file pkg/a.go; git commit -qam two
check "a commit counts" "$(changed)" 1
check "TEST_BASE nope: lint, says no merge base" "$(TEST_BASE=nope changed)" 1
check "says no merge base" "$(grep -c 'no merge base with nope' "$T/err")" 1
reset; touch_file docs/a.md
check "TEST_ALL: lint" "$(TEST_ALL=1 changed)" 1
check "FORCE: lint" "$(FORCE=1 changed)" 1

reset; touch_file pkg/a.go; git commit -qam two
touch_file docs/a.md; git commit -qam three
git switch -q --detach HEAD
check "detached, TEST_BASE the commit before a docs change: no lint" "$(TEST_BASE=HEAD~1 changed)" 0
check "detached, TEST_BASE two commits back: lint" "$(TEST_BASE=HEAD~2 changed)" 1

exit $fail
