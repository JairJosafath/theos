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
# Waiters get slots first come, first served: each holds a numbered ticket
# (wait-NNNNNNNNNN, flock'd while it waits) and takes a free slot only when
# fewer older live waiters are ahead of it than slots are free. A waiter that
# dies drops its ticket's lock, and the next waiter to look removes it.
#
#   hack/heavy-slot.sh acquire PID JOB   wait for a slot and hold it while PID lives; once per PID
#   hack/heavy-slot.sh status            who holds the slots and who waits; exit 1 when a new job would wait
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

# waiters: the live tickets, oldest first; removes the dead ones. Call it with
# the queue lock held.
waiters() {
  local t
  for t in "$DIR"/wait-*; do
    [ -e "$t" ] || continue
    if flock -n "$t" true; then rm -f "$t"; else echo "$t"; fi
  done
}

# free_slots: how many slots are free.
free_slots() {
  local i free=0
  for ((i = 1; i <= JOBS; i++)); do taken "$i" || free=$((free + 1)); done
  echo "$free"
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
    [ -n "$QFD" ] && exec {QFD}>&-
    [ -n "$TFD" ] && exec {TFD}>&-
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
  # Take a ticket: the next number, under the queue lock, locked before it
  # shows up under its name so no one takes it for a dead waiter's.
  exec {QFD}>>"$DIR/queue" || return 1
  flock "$QFD"
  local seq tmp ticket
  seq=$(($(cat "$DIR/queue.seq" 2>/dev/null || echo 0) + 1))
  echo "$seq" >"$DIR/queue.seq"
  ticket=$(printf '%s/wait-%010d' "$DIR" "$seq")
  tmp=$DIR/.ticket-$$
  exec {TFD}>"$tmp" && flock -n "$TFD" || { flock -u "$QFD"; return 1; }
  printf 'pid %s in %s, `%s`, since %s\n' "$pid" "$CHECKOUT" "$job" "$(date '+%H:%M')" >&"$TFD"
  mv "$tmp" "$ticket"
  trap 'rm -f "$ticket"' EXIT
  local ahead free q
  while :; do
    # Under the queue lock: count the live waiters ahead, and take a slot only
    # when fewer of them wait than slots are free.
    flock "$QFD"
    ahead=0
    while read -r q; do
      [ "$q" = "$ticket" ] && break
      ahead=$((ahead + 1))
    done < <(waiters)
    free=$(free_slots)
    if [ "$ahead" -lt "$free" ]; then
      for ((i = 1; i <= JOBS; i++)); do
        if hold "$i" "$pid" "$job"; then
          rm -f "$ticket"
          flock -u "$QFD"
          [ -n "$seen" ] && echo "heavy-slot: got slot $i after $(($(date +%s) - t0))s; \`$job\` starts" >&2
          return 0
        fi
      done
    fi
    flock -u "$QFD"
    kill -0 "$pid" 2>/dev/null || return 1
    now="$(holders)"$'\n'"$ahead"
    if [ "$now" != "$seen" ]; then
      if [ "$free" -gt 0 ]; then
        printf 'heavy-slot: %s of %s heavy-job slots free, but %s older job(s) wait ahead of `%s`; it waits its turn. Waiting is not a hang. Held by:\n%s\n' \
          "$free" "$JOBS" "$ahead" "$job" "$(holders)" >&2
      else
        printf 'heavy-slot: all %s heavy-job slots on this device are taken (HEAVY_JOBS); `%s` waits for one, with %s job(s) waiting ahead of it. Waiting is not a hang. Held by:\n%s\n' \
          "$JOBS" "$job" "$ahead" "$(holders)" >&2
      fi
      seen=$now
    fi
    sleep "$POLL"
  done
}

status() {
  local free t n=0 waiting=
  [ "$JOBS" -eq 0 ] && { echo "heavy-slot: the queue is off (HEAVY_JOBS=0)"; return 0; }
  free=$(free_slots)
  # Waiters are listed without the queue lock: status only reads.
  for t in "$DIR"/wait-*; do
    [ -e "$t" ] && ! flock -n "$t" true || continue
    n=$((n + 1))
    waiting+="  waiting $n: $(cat "$t" 2>/dev/null || echo unknown)"$'\n'
  done
  echo "heavy-slot: $free of $JOBS slots free, $n waiting"
  holders
  printf '%s' "$waiting"
  [ "$free" -gt "$n" ]
}

DIR=$(slots_dir) || { echo "heavy-slot: no git checkout here and no HEAVY_SLOTS_DIR; not queueing" >&2; exit 0; }
QFD= TFD=
CHECKOUT=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null || echo "$PWD")
case ${1:-} in
  acquire) shift; acquire "$@" ;;
  status) status ;;
  *) echo "usage: hack/heavy-slot.sh acquire PID JOB | status" >&2; exit 64 ;;
esac
