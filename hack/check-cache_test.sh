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
unset FORCE C4_HEAVY_SLOT C4_AGENT
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

# The Makefile: a cached plain `make check` answers without a heavy slot; a
# miss or FORCE=1 queues for one. The one slot is held while it runs.
cd "$HACK/.." || exit 1
sleep 60 & holder=$!
"$HACK/heavy-slot.sh" acquire "$holder" "holder" 2>/dev/null
timeout 5 make -n check >/dev/null 2>&1; check "make check, not cached: waits for a slot" $? 124
"$CACHE" record >/dev/null
out=$(timeout 5 make check 2>&1); check "make check, cached: exit 0 without a slot" $? 0
check "make check, cached: says so" "$(grep -c '(cached); FORCE=1 to rerun' <<<"$out")" 1
FORCE=1 timeout 5 make -n check >/dev/null 2>&1; check "FORCE=1 make check: waits for a slot" $? 124
timeout 5 make -n manifests check >/dev/null 2>&1; check "make manifests check: not answered from the cache" $? 124
kill "$holder"

# The Makefile's tier rule: a tier whose key passed answers without running
# it (NPM=false would fail it), and a dry run records nothing.
skip="-o manifests -o generate -o fmt"
"$CACHE" run ui-test true >/dev/null
out=$(timeout 30 make -s $skip check-tier-ui-test NPM=false 2>&1); check "make check-tier-ui-test, cached: exit 0" $? 0
check "make check-tier-ui-test, cached: says so" "$(grep -c '^ui-test: cached (ui/ unchanged since ' <<<"$out")" 1
rm -rf "$CHECK_CACHE_DIR/tiers"
timeout 30 make -n $skip check-tier-ui-test >/dev/null 2>&1
[ ! -e "$CHECK_CACHE_DIR/tiers" ]; check "make -n check-tier-ui-test: records nothing" $? 0
rule=$(make -pq help 2>/dev/null | grep -m1 '^check:')
for t in validate-yaml test-hack; do
  [[ " $rule " == *" check-tier-$t "* ]]; check "check runs $t through the tier cache" $? 0
done

exit $fail
