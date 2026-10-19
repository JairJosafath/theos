#!/usr/bin/env bash
# Tests for hack/load-images.sh, against a fake docker and kind that log each
# call to $T/log and fail the first $T/fail-pull pulls and $T/fail-load loads.
# Run: hack/load-images_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
LOAD="$(cd "$(dirname "$0")" && pwd)/load-images.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

mkdir -p "$T/bin"
# fake NAME COUNTER: a command that logs its arguments and fails while
# $T/COUNTER, when its first argument names it, is above 0.
fake() {
  cat >"$T/bin/$1" <<FAKE
#!/usr/bin/env bash
echo "$1 \$*" >>"\$T/log"
if [ "\$1" = "$2" ] && [ "\$(cat "\$T/fail-$2")" -gt 0 ]; then
  echo \$((\$(cat "\$T/fail-$2") - 1)) >"\$T/fail-$2"
  echo "$1: failed to extract layer: failed to Lchown: no such file or directory" >&2
  exit 1
fi
FAKE
  chmod +x "$T/bin/$1"
}
fake docker pull
fake kind load
export T

# run PULLS LOADS ARG...: the script with the first PULLS pulls and LOADS loads
# failing; its calls in $T/log, its exit code in $rc, its stderr in $T/err.
run() {
  echo "$1" >"$T/fail-pull"
  echo "$2" >"$T/fail-load"
  shift 2
  : >"$T/log"
  ATTEMPTS=3 DELAY=0 PATH="$T/bin:$PATH" "$LOAD" "$@" 2>"$T/err" >/dev/null
  rc=$?
}

run 0 0 c img:1 img:2
check "each image is pulled, loaded and removed" "$(cat "$T/log")" "docker pull img:1
kind load docker-image --name c img:1
docker rmi img:1
docker pull img:2
kind load docker-image --name c img:2
docker rmi img:2"
check "  and it passes" "$rc" 0

# #1408: the runner's containerd lost a snapshot during the first pull.
run 2 0 c img:1
check "#1408: a failed pull is tried again" "$(grep -c '^docker pull img:1$' "$T/log")" 3
check "  and it passes" "$rc" 0
check "  saying so" "$(grep -c 'docker pull img:1 failed (try' "$T/err")" 2
check "  then loads it" "$(grep -c '^kind load' "$T/log")" 1

run 1 0 c img:1
check "a pull that passes on its second try isn't tried a third time" "$(grep -c '^docker pull' "$T/log")" 2

run 3 0 c img:1
check "a pull that fails every try fails" "$rc" 1
check "  after ATTEMPTS tries" "$(grep -c '^docker pull' "$T/log")" 3
check "  loading nothing" "$(grep -c '^kind' "$T/log")" 0
check "  and says it gave up" "$(grep -c 'docker pull img:1 failed 3 times, giving up' "$T/err")" 1

run 0 1 c img:1
check "a failed load is tried again" "$(grep -c '^kind load' "$T/log")" 2
check "  and it passes" "$rc" 0

run 0 3 c img:1 img:2
check "a load that fails every try fails" "$rc" 1
check "  before the next image" "$(grep -c 'img:2' "$T/log")" 0

run 0 0 c
check "no image is a usage error" "$rc" 2

exit $fail
