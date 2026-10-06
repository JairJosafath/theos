#!/usr/bin/env bash
# The passing-tree cache of `make check`: a check that passed is recorded under
# the git tree hash of the working state (tracked and untracked non-ignored
# files alike), shared by every checkout on the device. `make check` on a tree
# that already passed says so and exits 0 without taking a heavy-job slot.
#
# The pass is recorded under the tree as it is after the check, so after
# manifests, generate and fmt rewrote what they rewrite. A tree whose generated
# or formatted files are stale never matches a recorded pass: running the check
# on it would change those files, so its hash differs from any tree that passed.
#
#   hack/check-cache.sh tree     print the tree hash of the working state
#   hack/check-cache.sh lookup   print the cached-pass line and exit 0 on a hit; exit 1 on a miss or FORCE=1
#   hack/check-cache.sh record   record a pass of the working state; warn and exit 0 when it can't
#
#   CHECK_CACHE_DIR  where passes live (default $XDG_CACHE_HOME/c4factory/checks, which ./dev puts in .dev/cache)
#   FORCE            non-empty: lookup always misses
set -uo pipefail

DIR=${CHECK_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/c4factory/checks}

# tree: the hash git would give the working state if it were all added: a copy
# of the index, so git add only rehashes what changed, takes every change.
tree() {
  local index tmp
  index=$(git rev-parse --path-format=absolute --git-path index) || return 1
  tmp=$(mktemp) || return 1
  if [ -f "$index" ]; then cp "$index" "$tmp"; else rm -f "$tmp"; fi
  GIT_INDEX_FILE=$tmp git add -A >/dev/null 2>&1 &&
    GIT_INDEX_FILE=$tmp git write-tree
  local rc=$?
  rm -f "$tmp"
  return $rc
}

lookup() {
  local t when= checkout= agent= key val
  [ -n "${FORCE:-}" ] && return 1
  t=$(tree) || return 1
  [ -f "$DIR/$t" ] || return 1
  while IFS='=' read -r key val; do
    case $key in
      when) when=$val ;;
      checkout) checkout=$val ;;
      agent) agent=$val ;;
    esac
  done <"$DIR/$t"
  echo "make check: passed on tree $t at $when in $checkout${agent:+ by $agent} (cached); FORCE=1 to rerun"
}

# record is best effort: the check passed, so a cache it can't write (a
# sandboxed session may only write inside its own worktree) warns and exits 0.
record() {
  local t agent= claim
  if ! t=$(tree); then
    echo "make check: passed; warning: could not hash the tree, pass not recorded" >&2
    return 0
  fi
  claim=$(git rev-parse --show-toplevel)/.relay/claim
  [ -s "$claim" ] && agent=$(jq -r '.name // empty' "$claim" 2>/dev/null)
  if ! { mkdir -p "$DIR" &&
    printf 'when=%s\ncheckout=%s\nagent=%s\n' \
      "$(date '+%Y-%m-%d %H:%M')" "$(git rev-parse --show-toplevel)" "${agent:-${C4_AGENT:-}}" >"$DIR/$t.$$" &&
    mv "$DIR/$t.$$" "$DIR/$t"; } 2>/dev/null; then
    rm -f "$DIR/$t.$$" 2>/dev/null
    echo "make check: passed on tree $t; warning: $DIR is not writable, pass not recorded" >&2
    return 0
  fi
  echo "make check: passed on tree $t; recorded"
}

case ${1:-} in
  tree) tree ;;
  lookup) lookup ;;
  record) record ;;
  *) echo "usage: hack/check-cache.sh tree | lookup | record" >&2; exit 64 ;;
esac
