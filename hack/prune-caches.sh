#!/usr/bin/env bash
# Prunes the device's build caches; hack/relay-watch.sh runs it every
# WATCH_TRIM_EVERY (30 minutes). WSL's and Docker Desktop's disk images grow
# and never shrink, so cache churn fills the Windows drive (#1343). Each prune
# logs one line when it drops something:
#
# 1. hack/trim-go-cache.sh: GOCACHE, golangci-lint's cache and
#    ~/.cache/go-build.
# 2. The GH runners' shared cache volume (theos-gh-runner-cache): the same
#    trim over its go-build and golangci-lint directories, with the same
#    limit, in a throwaway container of the runner image (bash and GNU find)
#    that mounts the volume. The trim keeps what was used in the last two
#    hours, so a running job keeps its cache. Skipped without docker, the
#    volume or the image. THEOS_PRUNE_RUNNER_CACHE=0 turns it off.
# 3. Docker's build cache older than THEOS_PRUNE_DOCKER_UNTIL (default 72h, so
#    the latest builds stay warm), then what is over THEOS_PRUNE_DOCKER_MAX_GB
#    (default 10, oldest first; 0 for no cap): a release's ten builds grew it
#    past 20 GB within 72h (#1636). Then the dangling images. Skipped without
#    docker. THEOS_PRUNE_DOCKER=0 turns it off.
#
# THEOS_TRIM_GO=0 turns the first off. THEOS_GOCACHE_MAX_GB sets the limit of the
# first two (hack/trim-go-cache.sh).
#
# Usage: hack/prune-caches.sh
set -uo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
VOLUME=theos-gh-runner-cache
IMAGE=theos/gh-runner:dev

[ "${THEOS_TRIM_GO:-1}" = 0 ] || "$HERE/trim-go-cache.sh"

command -v docker >/dev/null || exit 0

if [ "${THEOS_PRUNE_RUNNER_CACHE:-1}" != 0 ] && docker volume inspect "$VOLUME" >/dev/null 2>&1 &&
  docker image inspect "$IMAGE" >/dev/null 2>&1; then
  limit=(-e "THEOS_GOCACHE_MAX_GB=${THEOS_GOCACHE_MAX_GB:-4}")
  [ -z "${THEOS_GOCACHE_MAX_MB:-}" ] || limit+=(-e "THEOS_GOCACHE_MAX_MB=$THEOS_GOCACHE_MAX_MB")
  # The script goes in on stdin: no bind mount of the checkout's path.
  docker run --rm -i --entrypoint bash -v "$VOLUME:/cache" "${limit[@]}" \
    "$IMAGE" -s -- /cache/go-build /cache/golangci-lint <"$HERE/trim-go-cache.sh" 2>&1 |
    sed "s|/cache/|$VOLUME/|g"
fi

if [ "${THEOS_PRUNE_DOCKER:-1}" != 0 ]; then
  until=${THEOS_PRUNE_DOCKER_UNTIL:-72h}
  # Both end with "Total reclaimed space: N"; 0B means nothing went.
  freed() { sed -n 's/^Total reclaimed space: *//p' | tail -n 1; }
  b=$(docker builder prune -f --filter "until=$until" 2>/dev/null | freed)
  [ -n "$b" ] && [ "$b" != 0B ] && echo "prune-caches: docker build cache unused for $until: reclaimed $b"
  max=${THEOS_PRUNE_DOCKER_MAX_GB:-10}
  if [ "$max" != 0 ]; then
    # Docker 28 renamed --keep-storage; the help says which one this CLI has.
    case $(docker builder prune --help 2>/dev/null) in *--max-used-space*) cap=--max-used-space ;; *) cap=--keep-storage ;; esac
    c=$(docker builder prune -f "$cap=${max}GB" 2>/dev/null | freed)
    [ -n "$c" ] && [ "$c" != 0B ] && echo "prune-caches: docker build cache over ${max} GB: reclaimed $c"
  fi
  i=$(docker image prune -f 2>/dev/null | freed)
  [ -n "$i" ] && [ "$i" != 0B ] && echo "prune-caches: dangling docker images: reclaimed $i"
fi
exit 0
