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

exit $fail
