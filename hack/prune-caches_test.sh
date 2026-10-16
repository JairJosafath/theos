#!/usr/bin/env bash
# Tests for hack/prune-caches.sh with a stub docker that logs its calls, says
# whether the runner cache volume and image exist (STUB_VOLUME, STUB_IMAGE), and keeps
# what docker run gets on stdin. A stub go points GOCACHE at a fake cache.
# Run: hack/prune-caches_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HERE="$(cd "$(dirname "$0")" && pwd)"
PRUNE=$HERE/prune-caches.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

mkdir -p "$T/bin" "$T/nodocker" "$T/home"
cat >"$T/bin/docker" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"$STUB_LOG"
case "$1 $2" in
  "volume inspect") [ "$STUB_VOLUME" = 1 ] ;;
  "image inspect") [ "$STUB_IMAGE" = 1 ] ;;
  "run "*) cat >"$STUB_STDIN"; echo "trim-go-cache: /cache/go-build was 9 MB, limit 4 MB; dropped 6 MB of entries unused for 2+ hours, now 3 MB" ;;
  "builder prune") echo "Total reclaimed space: ${BUILDER_FREED:-1.5GB}" ;;
  "image prune") echo "Total reclaimed space: ${IMAGES_FREED:-0B}" ;;
esac
EOF
chmod +x "$T/bin/docker"
for tool in bash dirname sed tail cat du cut find awk; do
  p=$(command -v "$tool") && ln -sf "$p" "$T/nodocker/$tool"
done
# A stub go names GOCACHE, so no test touches the device's cache.
mkdir -p "$T/gocache"
printf '#!/bin/sh\necho %s\n' "$T/gocache" >"$T/bin/go" && chmod +x "$T/bin/go"
export STUB_LOG=$T/log STUB_STDIN=$T/stdin HOME=$T/home GOLANGCI_LINT_CACHE=$T/lint XDG_CACHE_HOME=
# prune [ENV=VALUE...]: run the script with the stub on PATH; its calls go to $T/log.
prune() { rm -f "$T/log" "$T/stdin"; touch "$T/log"; env STUB_VOLUME=1 STUB_IMAGE=1 PATH="$T/bin:$T/nodocker" "$@" "$PRUNE"; }
called() { grep -c -- "$1" "$T/log"; }

out=$(prune)
check "runner cache: one throwaway container" "$(called '^run --rm -i --entrypoint bash -v c4factory-gh-runner-cache:/cache ')" 1
check "runner cache: over go-build and golangci-lint" "$(called 'c4factory/gh-runner:dev -s -- /cache/go-build /cache/golangci-lint$')" 1
check "runner cache: the default limit" "$(called '-e C4_GOCACHE_MAX_GB=4 ')" 1
check "runner cache: trim-go-cache.sh on stdin" "$(cmp -s "$T/stdin" "$HERE/trim-go-cache.sh" && echo same)" same
check "runner cache: logs what it dropped, naming the volume" "$(echo "$out" | grep -c '^trim-go-cache: c4factory-gh-runner-cache/go-build was 9 MB')" 1
check "build cache: pruned with the until filter, 72h by default" "$(called '^builder prune -f --filter until=72h$')" 1
check "build cache: logs what it reclaimed" "$(echo "$out" | grep -c '^prune-caches: docker build cache unused for 72h: reclaimed 1.5GB$')" 1
check "images: dangling ones pruned" "$(called '^image prune -f$')" 1
check "images: nothing reclaimed, nothing logged" "$(echo "$out" | grep -c 'dangling')" 0
check "one line for each prune" "$(echo "$out" | wc -l | tr -d ' ')" 2

out=$(prune IMAGES_FREED=300MB BUILDER_FREED=0B)
check "images: logs what they reclaimed" "$(echo "$out" | grep -c '^prune-caches: dangling docker images: reclaimed 300MB$')" 1
check "build cache: nothing reclaimed, nothing logged" "$(echo "$out" | grep -c 'build cache')" 0

prune C4_GOCACHE_MAX_GB=2 C4_GOCACHE_MAX_MB=5 C4_PRUNE_DOCKER_UNTIL=24h >/dev/null
check "runner cache: the limit passes in" "$(called '-e C4_GOCACHE_MAX_GB=2 -e C4_GOCACHE_MAX_MB=5 ')" 1
check "build cache: C4_PRUNE_DOCKER_UNTIL sets the age" "$(called '^builder prune -f --filter until=24h$')" 1

prune STUB_VOLUME=0 >/dev/null
check "no volume: no container" "$(called '^run ')" 0
check "no volume: docker still pruned" "$(called '^builder prune')" 1
prune STUB_IMAGE=0 >/dev/null
check "no runner image: no container" "$(called '^run ')" 0

prune C4_PRUNE_RUNNER_CACHE=0 >/dev/null
check "C4_PRUNE_RUNNER_CACHE=0: no container" "$(called '^run ')" 0
check "C4_PRUNE_RUNNER_CACHE=0: docker still pruned" "$(called ' prune ')" 2
prune C4_PRUNE_DOCKER=0 >/dev/null
check "C4_PRUNE_DOCKER=0: no docker prune" "$(called ' prune ')" 0
check "C4_PRUNE_DOCKER=0: runner cache still trimmed" "$(called '^run ')" 1

rm -f "$T/log"; touch "$T/log"
out=$(env PATH="$T/nodocker" "$PRUNE"; echo "rc=$?")
check "no docker: nothing but exit 0" "$out" "rc=0"
check "no docker: no call" "$(called .)" 0

# The Go trim runs first, on GOCACHE; C4_TRIM_GO=0 turns it off.
mkdir -p "$T/gocache/00" && head -c 2097152 /dev/zero >"$T/gocache/00/old" && touch -d '10 days ago' "$T/gocache/00/old"
out=$(prune C4_TRIM_GO=0 C4_GOCACHE_MAX_MB=1)
check "C4_TRIM_GO=0: GOCACHE stays" "$(ls "$T/gocache/00")" old
out=$(prune C4_GOCACHE_MAX_MB=1)
check "Go trim: GOCACHE trimmed" "$(ls "$T/gocache/00")" ""
check "Go trim: logged" "$(echo "$out" | grep -c "^trim-go-cache: $T/gocache was")" 1

[ "$fail" = 0 ] && echo PASS || echo FAIL
exit "$fail"
