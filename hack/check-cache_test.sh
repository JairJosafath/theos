#!/usr/bin/env bash
# Tests for hack/check-cache.sh, the passing-tree cache of `make check`, and the
# Makefile's use of it, with the cache and the heavy-job slots in a temporary
# directory.
# Run: hack/check-cache_test.sh
T=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
CACHE=$HACK/check-cache.sh
export CHECK_CACHE_DIR=$T/checks HEAVY_SLOTS_DIR=$T/slots HEAVY_JOBS=1 HEAVY_POLL=0.2
unset FORCE THEOS_HEAVY_SLOT THEOS_AGENT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# A repository with a tracked file, an ignored one and an agent's claim.
R=$T/repo
git init -q "$R" && cd "$R" || exit 1
git config user.email t@example.com && git config user.name t
printf 'a\n' >tracked
printf 'ignored\n.relay/\n' >.gitignore
git add -A && git commit -qm init
mkdir .relay && echo '{"name": "otter-9"}' >.relay/claim

"$CACHE" lookup >/dev/null; check "nothing recorded: miss" $? 1
t=$("$CACHE" tree)
check "tree: of a clean checkout, HEAD's tree" "$t" "$(git rev-parse 'HEAD^{tree}')"
"$CACHE" record >/dev/null; check "record: exit 0" $? 0
out=$("$CACHE" lookup); check "recorded: hit" $? 0
[[ $out == "make check: passed on tree $t at "*" in $R by otter-9 (cached); FORCE=1 to rerun" ]]
check "hit: says the tree, when, where and who" $? 0
FORCE=1 "$CACHE" lookup >/dev/null; check "FORCE=1: miss" $? 1

# One changed byte misses, tracked or untracked; an ignored file doesn't count.
# The tracked change is racy git, forced: same size, and the file and the index
# share one mtime a minute back, so only the content tells them apart (#330).
git config core.trustctime false
touch -d '-1 min' tracked && git update-index -q --refresh
touch -r tracked "$(git rev-parse --git-path index)"
printf 'b\n' >tracked && touch -r "$(git rev-parse --git-path index)" tracked
"$CACHE" lookup >/dev/null; check "tracked byte changed: miss" $? 1
printf 'a\n' >tracked
"$CACHE" lookup >/dev/null; check "changed back: hit" $? 0
printf 'x' >new
"$CACHE" lookup >/dev/null; check "untracked file: miss" $? 1
check "lookup leaves the index alone" "$(git status --porcelain)" "?? new"
rm new
printf 'x' >ignored
"$CACHE" lookup >/dev/null; check "ignored file: hit" $? 0
printf 'c\n' >tracked; git add tracked; printf 'a\n' >tracked
"$CACHE" lookup >/dev/null; check "staged change, worktree back: hit (the worktree counts)" $? 0
git reset -q

# A cache dir that can't be written: record warns and exits 0, so a passing
# check still passes. (root writes anywhere, so the case needs a plain user.)
if [ "$(id -u)" != 0 ]; then
  mkdir "$T/ro" && chmod 555 "$T/ro"
  err=$(CHECK_CACHE_DIR=$T/ro/checks "$CACHE" record 2>&1 >/dev/null); check "unwritable cache: record exit 0" $? 0
  [[ $err == "make check: passed on tree $t; warning: $T/ro/checks is not writable, pass not recorded" ]]
  check "unwritable cache: warns on stderr" $? 0
  CHECK_CACHE_DIR=$T/ro/checks "$CACHE" lookup >/dev/null; check "unwritable cache: miss" $? 1
  chmod 755 "$T/ro"
fi

# A pass in one checkout is a hit in another on the same tree.
git worktree add -q "$T/other" HEAD 2>/dev/null
(cd "$T/other" && "$CACHE" lookup >/dev/null); check "other worktree, same tree: hit" $? 0

# The tiers (#1285): each key moves with its own inputs only. A module whose
# hack/render builds internal/scaffold, as validate-yaml's go run does.
mkdir -p ui hack/render hack/runner-fence internal/scaffold internal/other config docs/dev
printf 'module example.com/r\n\ngo 1.22\n' >go.mod
printf 'package scaffold\n' >internal/scaffold/s.go
printf 'package other\n' >internal/other/o.go
printf 'package main\n\nimport _ "example.com/r/internal/scaffold"\n\nfunc main() {}\n' >hack/render/main.go
printf 'package main\n\nfunc main() {}\n' >hack/runner-fence/main.go
for f in ui/app.ts hack/x.sh config/a.yaml docs/notes.md docs/dev/testenv.md; do echo 1 >"$f"; done
git add -A && git commit -qm tiers
keys() { local t; for t in ui-test test-hack validate-yaml; do "$CACHE" key "$t" || echo "$t: no key"; done; }
k0=$(keys)
check "key: three distinct keys" "$(sort -u <<<"$k0" | wc -l)" 3
check "key: stable" "$(keys)" "$k0"
# moved FILE: which tiers' keys a change to FILE moves, then FILE back.
moved() {
  local before k i=0 out=
  before=$(cat "$1")
  echo 2 >>"$1"
  for k in $(keys); do
    [ "$k" != "$(sed -n "$((i + 1))p" <<<"$k0")" ] && out+=$(sed -n "$((i + 1))p" <<<$'ui-test\ntest-hack\nvalidate-yaml')" "
    i=$((i + 1))
  done
  printf '%s\n' "$before" >"$1"
  echo "${out% }"
}
check "ui/ changed: only ui-test" "$(moved ui/app.ts)" "ui-test"
check "hack/ changed: only test-hack" "$(moved hack/x.sh)" "test-hack"
check "config/ changed: test-hack and validate-yaml" "$(moved config/a.yaml)" "test-hack validate-yaml"
check "docs/ changed: none" "$(moved docs/notes.md)" ""
check "docs/dev/testenv.md changed: test-hack, whose tests read it" "$(moved docs/dev/testenv.md)" "test-hack"
check "a package hack/render builds changed: validate-yaml" "$(moved internal/scaffold/s.go)" "validate-yaml"
check "a package nothing renders with changed: none" "$(moved internal/other/o.go)" ""
echo x >ui/new.ts
check "untracked file under ui/: ui-test" "$([ "$("$CACHE" key ui-test)" != "$(head -1 <<<"$k0")" ] && echo moved)" moved
rm ui/new.ts
# A file outside a tier's inputs that git can't add, as a parallel test's
# scratch that vanishes mid-add, leaves the key as it was: the key adds only
# the tier's files, and make check's ui-test once missed and ran (#1814).
if [ "$(id -u)" != 0 ]; then
  echo x >docs/unreadable.md && chmod 000 docs/unreadable.md
  check "unreadable file outside ui/: ui-test keys as before" "$("$CACHE" key ui-test)" "$(head -1 <<<"$k0")"
  rm -f docs/unreadable.md
fi
"$CACHE" key nope >/dev/null 2>&1; check "key: unknown tier fails" $? 64
# A tool a tier runs that the device lacks is part of its key, not a failure:
# CI's shell-test job has no Node, and the key of ui-test failed there (#1285).
mkdir -p "$T/bin" && printf '#!/bin/sh\nexit 127\n' >"$T/bin/node" && chmod +x "$T/bin/node"
PATH=$T/bin:$PATH "$CACHE" key ui-test >/dev/null; check "key: a missing tool still keys" $? 0
# A tool not on PATH keys the same however the script is called: make calls
# hack/check-cache.sh, the tests a full path, and CI's cached tier missed (#1399).
mkdir -p "$T/nonode" && for t in git bash sh cp mktemp rm head; do ln -sf "$(command -v "$t")" "$T/nonode/$t"; done
ln -s "$CACHE" "$T/check-cache.sh"
check "key: a tool not on PATH keys the same by any path" \
  "$(PATH=$T/nonode ../check-cache.sh key ui-test)" "$(PATH=$T/nonode "$CACHE" key ui-test)"

# run: a miss runs and records, a hit doesn't run, FORCE=1 runs, a failure
# records nothing and keeps its exit code.
ran() { rm -f "$T/ran"; "$CACHE" run "$1" sh -c "touch '$T/ran'; exit ${2:-0}"; }
out=$(ran ui-test); check "run, miss: exit 0" $? 0
[ -e "$T/ran" ]; check "run, miss: runs the command" $? 0
out=$(ran ui-test); check "run, hit: exit 0" $? 0
[ ! -e "$T/ran" ]; check "run, hit: doesn't run it" $? 0
[[ $out == "ui-test: cached (ui/ unchanged since "*", passed in $R); FORCE=1 to rerun" ]]
check "run, hit: says so in one line" $? 0
FORCE=1 ran ui-test >/dev/null; [ -e "$T/ran" ]; check "run, FORCE=1: runs it" $? 0
echo 2 >>hack/x.sh
ran test-hack 3 >/dev/null; check "run, failing: its exit code" $? 3
ran test-hack >/dev/null; [ -e "$T/ran" ]; check "run, after a failure: runs it again" $? 0
echo 2 >>ui/app.ts
ran ui-test >/dev/null; [ -e "$T/ran" ]; check "run, ui/ changed since: runs it" $? 0
git checkout -q -- hack/x.sh ui/app.ts
ran ui-test >/dev/null; [ ! -e "$T/ran" ]; check "run, back to inputs that passed before: cached" $? 0

# cached (#127): the cached line of a tier whose inputs passed, without
# running anything; a miss on other inputs and with FORCE=1.
out=$("$CACHE" cached ui-test); check "cached, inputs that passed: exit 0" $? 0
[[ $out == "ui-test: cached (ui/ unchanged since "*", passed in $R); FORCE=1 to rerun" ]]
check "cached: the line run prints" $? 0
FORCE=1 "$CACHE" cached ui-test >/dev/null; check "cached, FORCE=1: miss" $? 1
echo 3 >>ui/app.ts
"$CACHE" cached ui-test >/dev/null; check "cached, ui/ changed since: miss" $? 1
git checkout -q -- ui/app.ts
check "inputs: what a tier reads, in a few words" "$("$CACHE" inputs ui-test)" "ui/"

# changed (#127): the files among a tier's inputs that differ from a commit,
# committed, uncommitted or untracked: what make check selects the tier by,
# on a device that holds no pass of it too.
base=$(git rev-parse HEAD)
# differs TIER: the changed inputs of TIER, space-separated, and the exit code.
differs() {
  local out rc
  out=$("$CACHE" changed "$1" "$base")
  rc=$?
  echo "$(paste -sd' ' <<<"$out") [$rc]"
}
check "changed, nothing changed: exit 1" "$(differs ui-test)" " [1]"
echo 2 >>docs/notes.md
check "changed, docs/ changed: no tier's inputs" "$(differs ui-test; differs test-hack; differs validate-yaml)" " [1]"$'\n'" [1]"$'\n'" [1]"
git checkout -q -- docs/notes.md
echo 2 >>ui/app.ts && echo x >ui/new.ts
check "changed, ui/: the changed file and the untracked one" "$(differs ui-test)" "ui/app.ts ui/new.ts [0]"
check "changed, ui/: not an input of the shell tests" "$(differs test-hack)" " [1]"
git add -A && git commit -qm ui
check "changed, committed: still differs from the base" "$(differs ui-test)" "ui/app.ts ui/new.ts [0]"
echo 2 >>config/a.yaml
check "changed, config/: an input of the shell tests" "$(differs test-hack)" "config/a.yaml [0]"
check "changed, config/: and of validate-yaml" "$(differs validate-yaml)" "config/a.yaml [0]"
git checkout -q -- config/a.yaml
echo 2 >>internal/scaffold/s.go
check "changed, a package hack/render builds: validate-yaml" "$(differs validate-yaml)" "internal/scaffold/s.go [0]"
git checkout -q -- internal/scaffold/s.go
echo 2 >>internal/other/o.go
check "changed, a package nothing renders with: not validate-yaml" "$(differs validate-yaml)" " [1]"
git checkout -q -- internal/other/o.go
"$CACHE" changed ui-test nope >/dev/null 2>&1; check "changed, a base that is no commit: exit 2" $? 2
"$CACHE" changed nope "$base" >/dev/null 2>&1; check "changed, unknown tier: exit 2" $? 2

# The Makefile: a cached plain `make check` answers without a heavy slot; a
# miss or FORCE=1 queues for one. The one slot is held while it runs.
cd "$HACK/.." || exit 1
# The holder outlives any load, and the cached run's timeout only ends a
# broken one: it answers at once, but a loaded device took over 5s (#1409).
sleep 3600 & holder=$!
"$HACK/heavy-slot.sh" acquire "$holder" "holder" 2>/dev/null
# TEST_ALL=1 selects every step: a make check whose change selects none takes
# no slot either (#127, hack/check_test.sh), whatever this checkout holds.
TEST_ALL=1 timeout 5 make -n check >/dev/null 2>&1; check "make check, not cached: waits for a slot" $? 124
"$CACHE" record >/dev/null
out=$(timeout 120 make check 2>&1); check "make check, cached: exit 0 without a slot" $? 0
check "make check, cached: says so" "$(grep -c '(cached); FORCE=1 to rerun' <<<"$out")" 1
FORCE=1 timeout 5 make -n check >/dev/null 2>&1; check "FORCE=1 make check: waits for a slot" $? 124
# As a variable of the command line too, which make before 4.4 keeps from $(shell) (#127).
timeout 5 make -n check FORCE=1 >/dev/null 2>&1; check "make check FORCE=1: waits for a slot" $? 124
timeout 5 make -n manifests check >/dev/null 2>&1; check "make manifests check: not answered from the cache" $? 124
kill "$holder"

# make check runs the tiers through this cache: hack/check.sh, whose test
# shows a cached tier answered without make and a dry run recording nothing.
for t in validate-yaml test-hack ui-test; do
  check "hack/check.sh runs $t through the tier cache" "$(grep -c "tier $t\$" "$HACK/check.sh")" 1
done
check "hack/check.sh: a tier runs under check-cache.sh run" "$(grep -c 'step "$1" hack/check-cache.sh run "$1" "$MAKE"' "$HACK/check.sh")" 1

exit $fail
