#!/usr/bin/env bash
# Runs go test on the packages make test chose, the controller envtest suite
# in THEOS_TEST_PROCS processes at once (#1283). The suite runs its specs one
# after another, so it is the long pole of every make test; each process is a
# go test of the package with THEOS_TEST_SHARD=i/n, which runs the top-level
# containers of part i of n against its own control plane and manager
# (internal/controller/suite_test.go). go test's cache answers each part on
# its own, as it answers every other package. The other packages run in one
# go test beside them. Each go test's output is printed when it ends; the
# exit code is 1 when any failed.
#
# With -coverprofile FILE, each go test writes its own profile and they are
# merged into FILE. With THEOS_SLOWEST_SPECS=FILE, each part writes FILE.i and
# they are merged into FILE: the 10 slowest specs of the whole suite.
#
#   hack/go-test.sh [GO TEST FLAGS] -- PACKAGE...
#
#   THEOS_TEST_PROCS        processes the controller suite runs in (default 2;
#                        1 runs it in one go test, as before)
#   THEOS_TEST_MIN_MEM_GIB  below this much MemAvailable an unset THEOS_TEST_PROCS
#                        means 1 (default 4): each part takes about a GiB more
#                        (docs/dev/devices.md, "Memory on the cluster device")
#   RELAY_MEMINFO        file to read instead of /proc/meminfo (the test fakes it)
#   GO                   the go command (default go; the test fakes it)
set -uo pipefail

GO=${GO:-go}
SHARDED=github.com/JairJosafath/theos/internal/controller
say() { echo "make test: $*" >&2; }

flags=() pkgs=() cover=
while [ $# -gt 0 ] && [ "$1" != -- ]; do
  case $1 in
    -coverprofile) cover=$2; shift ;;
    -coverprofile=*) cover=${1#*=} ;;
    *) flags+=("$1") ;;
  esac
  shift
done
[ $# -gt 0 ] && shift
pkgs=("$@")
[ ${#pkgs[@]} -gt 0 ] || { say "no packages"; exit 2; }

procs=${THEOS_TEST_PROCS:-}
if [ -z "$procs" ]; then
  procs=2
  avail=$(awk '/^MemAvailable:/ {print int($2 / 1048576); exit}' "${RELAY_MEMINFO:-/proc/meminfo}" 2>/dev/null)
  if [ -n "$avail" ] && [ "$avail" -lt "${THEOS_TEST_MIN_MEM_GIB:-4}" ]; then
    say "${avail} GiB available, under THEOS_TEST_MIN_MEM_GIB=${THEOS_TEST_MIN_MEM_GIB:-4}: the controller suite runs in 1 process"
    procs=1
  fi
fi
[[ $procs =~ ^[1-9][0-9]*$ ]] || { say "THEOS_TEST_PROCS=$procs: want a number from 1"; exit 2; }

rest=() sharded=
for p in "${pkgs[@]}"; do
  if [ "$p" = "$SHARDED" ] && [ "$procs" -gt 1 ]; then sharded=1; else rest+=("$p"); fi
done

if [ -z "$sharded" ]; then
  set -x
  exec "$GO" test "${flags[@]}" ${cover:+-coverprofile "$cover"} "${rest[@]}"
fi

work=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$work"' EXIT

# start NAME [ENV=VALUE...] -- PACKAGE...: a go test in the background, its
# output in $work/NAME.out, its profile in $work/NAME.cover.
declare -A names=()
start() {
  local name=$1 env=()
  shift
  while [ "$1" != -- ]; do env+=("$1"); shift; done
  shift
  (
    set -x
    env "${env[@]}" "$GO" test "${flags[@]}" ${cover:+-coverprofile "$work/$name.cover"} "$@"
  ) >"$work/$name.out" 2>&1 &
  names[$!]=$name
}

say "the controller suite runs in $procs processes (THEOS_TEST_PROCS)"
[ ${#rest[@]} -eq 0 ] || start rest -- "${rest[@]}"
# A part an earlier run left must not be merged into this one's report.
[ -z "${THEOS_SLOWEST_SPECS:-}" ] || rm -f "$THEOS_SLOWEST_SPECS".*
# The package's plain Go tests run in the first part only.
start shard-1 THEOS_TEST_SHARD=1/"$procs" -- "$SHARDED"
for i in $(seq 2 "$procs"); do start "shard-$i" "THEOS_TEST_SHARD=$i/$procs" -- -run '^TestControllers$' "$SHARDED"; done

rc=0
while [ ${#names[@]} -gt 0 ]; do
  wait -n -p pid
  status=$?
  name=${names[$pid]}
  unset "names[$pid]"
  [ "$name" = rest ] || say "controller suite part ${name#shard-}/$procs:"
  cat "$work/$name.out"
  [ "$status" = 0 ] || rc=1
done

if [ -n "$cover" ]; then
  # One mode line, from one process: a pipe into head -n 1 under pipefail
  # could SIGPIPE its writer and add a second one (#1378).
  covers=("$work"/*.cover)
  [ -e "${covers[0]}" ] || covers=()
  awk 'FNR == 1 && /^mode:/ { if (mode == "") mode = $0; next }
       { blocks[n++] = $0 }
       END { print (mode == "" ? "mode: set" : mode); for (i = 0; i < n; i++) print blocks[i] }' \
    "${covers[@]}" </dev/null >"$cover"
fi

# The parts' slowest specs as one report: the specs and the longest part's
# time from their first lines, the 10 slowest of their other lines. A part
# go test's cache answered ran nothing and wrote nothing; the report says so.
if [ -n "${THEOS_SLOWEST_SPECS:-}" ]; then
  parts=() missing=() specs=0 secs=0
  for i in $(seq "$procs"); do
    f=$THEOS_SLOWEST_SPECS.$i
    if [ ! -f "$f" ]; then missing+=("$i"); continue; fi
    parts+=("$f")
    read -r n t < <(sed -nE '1s/^.*: ([0-9]+) specs in ([0-9]+)s;.*$/\1 \2/p' "$f")
    specs=$((specs + ${n:-0}))
    [ "${t:-0}" -le "$secs" ] || secs=$t
  done
  if [ ${#parts[@]} -gt 0 ]; then
    {
      echo -n "Controller Suite: $specs specs in ${#parts[@]} of $procs processes, the longest ${secs}s"
      [ ${#missing[@]} -eq 0 ] || echo -n " (parts ${missing[*]} cached, not here)"
      echo "; the 10 slowest:"
      tail -q -n +2 "${parts[@]}" | sort -rn | head -n 10
    } >"$THEOS_SLOWEST_SPECS"
    rm -f "${parts[@]}"
  fi
fi
exit "$rc"
