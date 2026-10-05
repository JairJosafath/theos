#!/usr/bin/env bash
# Heavy-job slots: heavy jobs queue device-wide instead of piling up. Every
# checkout on the device (the main one and its git worktrees) shares HEAVY_JOBS
# slots (default 2), flock'd files beside the checkouts in the git common dir.
# The Makefile takes one for `make check`, `test`, `lint`, `lint-fix`,
# `docker-build`, `deploy`, `e2e` and `test-e2e` before it builds anything,
# waiting for a free one and printing who holds them while it waits. A slot is
# held by a small watcher that lives as long as the job's process: a job that
# ends, fails or is killed frees its slot within a second, and a slot whose
# watcher dies too (a reboot) is free, since the kernel drops its lock.
#
#   hack/heavy-slot.sh acquire PID JOB   wait for a slot and hold it while PID lives; once per PID
#   hack/heavy-slot.sh status            who holds the slots; exit 1 when every slot is taken
#
#   HEAVY_JOBS       slots on the device (default 2); 0 turns the queue off
#   HEAVY_SLOTS_DIR  where the slots live (default <git common dir>/heavy-slots; the tests set it)
#   HEAVY_POLL       seconds between tries while waiting (default 5)
set -uo pipefail

JOBS=${HEAVY_JOBS:-2}
[[ $JOBS =~ ^[0-9]+$ ]] || JOBS=2
POLL=${HEAVY_POLL:-5}

slots_dir() {
  local d=${HEAVY_SLOTS_DIR:-}
  if [ -z "$d" ]; then
    d=$(git -C "$(dirname "$0")" rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || return 1
    d=$d/heavy-slots
  fi
  mkdir -p "$d" && echo "$d"
}

# taken I: whether slot I's lock is held.
taken() { [ -e "$DIR/slot-$1" ] && ! flock -n "$DIR/slot-$1" true; }

# holders: one line per taken slot: who holds it.
holders() {
  local i who
  for ((i = 1; i <= JOBS; i++)); do
    taken "$i" || continue
    { who=$(<"$DIR/slot-$i.who"); } 2>/dev/null || who="unknown"
    echo "  slot $i: $who"
  done
}

# hold I PID JOB: take slot I if it is free and keep it while PID lives.
hold() {
  local i=$1 pid=$2 job=$3 fd
  exec {fd}>>"$DIR/slot-$i" || return 1
  if ! flock -n "$fd"; then
    exec {fd}>&-
    return 1
  fi
  printf 'pid %s in %s, `%s`, since %s\n' "$pid" "$CHECKOUT" "$job" "$(date '+%H:%M')" >"$DIR/slot-$i.who"
  # The watcher keeps the lock's file open; nothing else of ours, so whoever
  # waits on our output (make's $(shell)) is not held up by it.
  (
    exec </dev/null >/dev/null 2>&1
    while kill -0 "$pid" 2>/dev/null; do sleep 1; done
  ) &
  exec {fd}>&-
}

acquire() {
  local pid=${1:?usage: acquire PID JOB} job=${2:-a heavy job} i who seen= now t0
  [ "$JOBS" -eq 0 ] && return 0
  # Once per PID: make can read its Makefile twice, and must not wait on itself.
  for ((i = 1; i <= JOBS; i++)); do
    { who=$(<"$DIR/slot-$i.who"); } 2>/dev/null || continue
    [[ $who == "pid $pid "* ]] && taken "$i" && return 0
  done
  t0=$(date +%s)
  while :; do
    for ((i = 1; i <= JOBS; i++)); do
      if hold "$i" "$pid" "$job"; then
        [ -n "$seen" ] && echo "heavy-slot: got slot $i after $(($(date +%s) - t0))s; \`$job\` starts" >&2
        return 0
      fi
    done
    kill -0 "$pid" 2>/dev/null || return 1
    now=$(holders)
    if [ "$now" != "$seen" ]; then
      printf 'heavy-slot: all %s heavy-job slots on this device are taken (HEAVY_JOBS); `%s` waits for one. Waiting is not a hang. Held by:\n%s\n' \
        "$JOBS" "$job" "$now" >&2
      seen=$now
    fi
    sleep "$POLL"
  done
}

status() {
  local i free=0
  [ "$JOBS" -eq 0 ] && { echo "heavy-slot: the queue is off (HEAVY_JOBS=0)"; return 0; }
  for ((i = 1; i <= JOBS; i++)); do taken "$i" || free=$((free + 1)); done
  echo "heavy-slot: $free of $JOBS slots free"
  holders
  [ "$free" -gt 0 ]
}

DIR=$(slots_dir) || { echo "heavy-slot: no git checkout here and no HEAVY_SLOTS_DIR; not queueing" >&2; exit 0; }
CHECKOUT=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null || echo "$PWD")
case ${1:-} in
  acquire) shift; acquire "$@" ;;
  status) status ;;
  *) echo "usage: hack/heavy-slot.sh acquire PID JOB | status" >&2; exit 64 ;;
esac
