#!/usr/bin/env bash
# Trims Go's build cache, and golangci-lint's, when one has grown past
# C4_GOCACHE_MAX_GB (default 4; C4_GOCACHE_MAX_MB overrides it, for tests).
# Go only drops cache entries unused for five days, and builds from several
# worktrees fill it fast (15 GB on the cluster device). VS Code runs it when
# the folder opens (.vscode/tasks.json).
#
# It never empties a cache: Go refreshes an entry's mtime when a build uses it
# (at most once an hour), so while a cache is over the limit it deletes the
# entries unused for 7 days, then 3, then 1, and stops once it is under. What
# was used today stays, so worktrees don't all recompile client-go at once.
#
# Usage: hack/trim-go-cache.sh [DIR...]  (default: GOCACHE and the
# golangci-lint cache)
set -uo pipefail
max_kb=$((${C4_GOCACHE_MAX_MB:-$((${C4_GOCACHE_MAX_GB:-4} * 1024))} * 1024))

size_kb() { du -sk "$1" 2>/dev/null | cut -f1; }
mb() { echo "$(($1 / 1024)) MB"; }

# trim DIR: delete DIR's entries by age until it is under the limit. Entries
# sit one level down (DIR/00/...); files at the top, such as Go's README and
# trim.txt, stay.
trim() {
  local dir=$1 before kb days dropped=0
  [ -d "$dir" ] || return 0
  before=$(size_kb "$dir")
  [ "$before" -lt "$max_kb" ] && return 0
  kb=$before
  for days in 7 3 1; do
    dropped=$((dropped + $(find "$dir" -mindepth 2 -type f -mmin +$((days * 24 * 60)) -printf '%k\n' -delete 2>/dev/null | awk '{s += $1} END {print s + 0}')))
    kb=$(size_kb "$dir")
    [ "$kb" -lt "$max_kb" ] && break
  done
  echo "trim-go-cache: $dir was $(mb "$before"), limit $(mb "$max_kb"); dropped $(mb "$dropped") of entries unused for $days+ days, now $(mb "$kb")"
  [ "$kb" -lt "$max_kb" ] || echo "trim-go-cache: $dir is still over the limit; what is left was used in the last day"
}

if [ $# -eq 0 ]; then
  command -v go >/dev/null || exit 0
  set -- "$(go env GOCACHE)" "${GOLANGCI_LINT_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/golangci-lint}"
fi
for dir in "$@"; do trim "$dir"; done
