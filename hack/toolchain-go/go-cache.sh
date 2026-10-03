#!/usr/bin/env bash
# c4-go-cache DIR [MAX_GB]: readies a Go build cache kept between pods on the
# Kind node: DIR/build for GOCACHE and DIR/golangci-lint for GOLANGCI_LINT_CACHE.
# Agent sandboxes run it in an init container, the go runner on start and hourly.
#
# - Seeds an empty cache from the image's warm one (C4_GO_CACHE_SEED, default
#   /root/.cache), so the Kubernetes CEL libraries aren't compiled cold again.
# - Bounds it to MAX_GB (default C4_GO_CACHE_MAX_GB, else 8): Go only drops
#   entries unused for five days. Past the bound it drops the entries unused for
#   a day (Go refreshes an entry's time when it uses it, at most hourly), and if
#   that is not enough, all of them, then seeds again.
#
# It only deletes entry files, never their directories, so a build running in
# another pod at the same time sees a miss, not an error. Pods on the node take
# turns through a lock in DIR.
set -euo pipefail
dir=${1:?usage: c4-go-cache DIR [MAX_GB]}
max=${2:-${C4_GO_CACHE_MAX_GB:-8}}
seed=${C4_GO_CACHE_SEED:-/root/.cache}

mkdir -p "$dir/build" "$dir/golangci-lint"
exec 9>"$dir/.lock"
flock 9

size_gb() { echo $(($(du -sk "$dir" | cut -f1) / 1024 / 1024)); }
# entries [FIND ARGS...]: the cache entry files, in the two-hex-digit directories.
entries() { find "$dir/build" "$dir/golangci-lint" -mindepth 2 -maxdepth 2 -path '*/[0-9a-f][0-9a-f]/*' -type f "$@"; }

gb=$(size_gb)
if [ "$gb" -ge "$max" ]; then
  entries -mmin +1440 -delete
  echo "c4-go-cache: $dir was ${gb} GB (limit ${max} GB); dropped entries unused for a day, now $(size_gb) GB"
  if [ "$(size_gb)" -ge "$max" ]; then
    entries -delete
    rm -f "$dir/.seeded"
    echo "c4-go-cache: still over the limit; emptied $dir"
  fi
fi

if [ ! -e "$dir/.seeded" ]; then
  for c in build:go-build golangci-lint:golangci-lint; do
    if [ -d "$seed/${c#*:}" ]; then
      # Some coreutils versions exit 1 when --no-clobber skips a file.
      cp -a --no-clobber "$seed/${c#*:}/." "$dir/${c%%:*}/" || echo "c4-go-cache: copying $seed/${c#*:} reported an error"
    fi
  done
  touch "$dir/.seeded"
  echo "c4-go-cache: seeded $dir from $seed ($(size_gb) GB)"
fi
