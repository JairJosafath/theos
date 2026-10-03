#!/usr/bin/env bash
# Empties Go's build cache when it has grown past C4_GOCACHE_MAX_GB (default 4).
# Go only drops cache entries unused for five days, and builds from several
# worktrees fill it fast (14 GB on the cluster device). Both devcontainers run
# it on start; the next build refills what it needs.
set -uo pipefail
max=${C4_GOCACHE_MAX_GB:-4}
command -v go >/dev/null || exit 0
cache=$(go env GOCACHE)
[ -d "$cache" ] || exit 0
gb=$(($(du -sk "$cache" 2>/dev/null | cut -f1) / 1024 / 1024))
if [ "$gb" -ge "$max" ]; then
  go clean -cache && echo "go build cache was ${gb} GB (limit ${max} GB); emptied $cache"
fi
