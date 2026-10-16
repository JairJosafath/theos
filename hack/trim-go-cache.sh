#!/usr/bin/env bash
# Trims Go's build cache, and golangci-lint's, when one has grown past
# C4_GOCACHE_MAX_GB (default 4; C4_GOCACHE_MAX_MB overrides it, for tests).
# Go only drops cache entries unused for five days, and builds from several
# worktrees fill it fast (18 GB in 8 hours with nine relay workers). VS Code
# runs it when the folder opens (.vscode/tasks.json), and hack/relay-watch.sh
# every 30 minutes.
#
# It never empties a cache: Go refreshes an entry's mtime when a build uses it
# (at most once an hour), so while a cache is over the limit it deletes the
# entries untouched for 7 days, then 3, then 1, then 12, 6, 3 and 2 hours, and
# stops once it is under. An entry touched in the last 2 hours was used in the
# last one or two, so what builds use now stays, and worktrees don't all
# recompile client-go at once.
#
# Usage: hack/trim-go-cache.sh [DIR...]  (default: GOCACHE, the golangci-lint
# cache and Go's default cache ~/.cache/go-build, which go runs outside ./dev
# fill, when GOCACHE is elsewhere)
set -uo pipefail
max_kb=$((${C4_GOCACHE_MAX_MB:-$((${C4_GOCACHE_MAX_GB:-4} * 1024))} * 1024))

size_kb() { du -skH "$1" 2>/dev/null | cut -f1; }
mb() { echo "$(($1 / 1024)) MB"; }
# ago HOURS: "7+ days" or "12+ hours".
ago() { if [ $(($1 % 24)) = 0 ]; then echo "$(($1 / 24))+ days"; else echo "$1+ hours"; fi; }

# trim DIR: delete DIR's entries by age until it is under the limit. Entries
# sit one level down (DIR/00/...); files at the top, such as Go's README and
# trim.txt, stay.
trim() {
  local dir=$1 before kb hours dropped=0
  [ -d "$dir" ] || return 0
  before=$(size_kb "$dir")
  [ "$before" -lt "$max_kb" ] && return 0
  kb=$before
  for hours in 168 72 24 12 6 3 2; do
    dropped=$((dropped + $(find -H "$dir" -mindepth 2 -type f -mmin +$((hours * 60)) -printf '%k\n' -delete 2>/dev/null | awk '{s += $1} END {print s + 0}')))
    kb=$(size_kb "$dir")
    [ "$kb" -lt "$max_kb" ] && break
  done
  echo "trim-go-cache: $dir was $(mb "$before"), limit $(mb "$max_kb"); dropped $(mb "$dropped") of entries unused for $(ago "$hours"), now $(mb "$kb")"
  [ "$kb" -lt "$max_kb" ] || echo "trim-go-cache: $dir is still over the limit; what is left was used in the last 2 hours"
}

if [ $# -eq 0 ]; then
  command -v go >/dev/null || exit 0
  gocache=$(go env GOCACHE)
  set -- "$gocache" "${GOLANGCI_LINT_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/golangci-lint}"
  def=$HOME/.cache/go-build
  [ "$def" = "$gocache" ] || [ "$def" -ef "$gocache" ] || set -- "$@" "$def"
fi
for dir in "$@"; do trim "$dir"; done
