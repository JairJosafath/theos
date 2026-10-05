#!/usr/bin/env bash
# Relay watch: keeps the relay ready for new work. hack/relay.sh works the
# issues that are ready and stops when there are none, so an issue marked ready
# or handed back later waits until someone starts the relay again. The watch
# stays up instead: every WATCH_EVERY seconds (default 60) it asks GitHub for
# the next issue (hack/lib/github.sh pick, which changes nothing) and, when
# there is one and a worker is free, runs hack/relay.sh --workers N, which
# starts the workers that aren't running and leaves the others alone. Asking
# pick first keeps the relay's logs free of a worker that starts, finds nothing
# and stops every minute.
#
# When pick names the same issue again within
# WATCH_RETRY seconds (default 600), nobody claimed it, so the relay stopped
# without it or hack/relay.sh failed: the watch starts it again for that issue
# once the time has passed, not every minute. Every 5 minutes it runs
# hack/pull.sh, so pick never reads stale rules, and restarts itself when that
# changed this script.
#
#   hack/relay-watch.sh                start the watch, detached, for one worker
#   hack/relay-watch.sh --workers N    for N workers; on the cluster device RELAY_MAX_WORKERS caps them
#   touch .relay/stop                  stop the watch, and every worker after its current session
#
# Log: .relay/watch.log, one line per change; the workers log as usual, to
# .relay/relay-<k>.log.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
DIR="$REPO/.relay"
STOP="$DIR/stop"
LOG="$DIR/watch.log"
EVERY=${WATCH_EVERY:-60}
RETRY=${WATCH_RETRY:-600}
PULL_EVERY=${WATCH_PULL_EVERY:-300}

WORKERS=1
while [ $# -gt 0 ]; do
  case $1 in
    --workers) WORKERS=${2:-}; shift 2 || shift ;;
    --workers=*) WORKERS=${1#*=}; shift ;;
    *) echo "usage: hack/relay-watch.sh [--workers N]" >&2; exit 64 ;;
  esac
done
[[ $WORKERS =~ ^[1-9]$ ]] || { echo "relay-watch: --workers takes 1 to 9" >&2; exit 64; }

log() { echo "$(date -Is) $*" >>"$LOG"; }

# running K: whether relay worker K runs (it holds its lock).
running() { [ -e "$DIR/lock-$1" ] && ! flock -n "$DIR/lock-$1" true; }

# worker_free: whether one of the watch's workers doesn't run.
worker_free() {
  local k
  for k in $(seq 1 "$WORKERS"); do running "$k" || return 0; done
  return 1
}

# pick_next and run_relay: what the watch asks and starts (faked in the tests).
pick_next() { "$REPO/hack/lib/github.sh" pick; }
run_relay() { "$REPO/hack/relay.sh" --workers "$WORKERS"; }

# start WHY: log WHY and run hack/relay.sh for the workers that aren't running.
start() {
  local out rc
  log "$1; starting the relay"
  out=$(run_relay 2>&1 8>&-)
  rc=$?
  [ -n "$out" ] && log "relay: ${out//$'\n'/; }"
  [ "$rc" = 0 ] || log "hack/relay.sh failed (exit $rc)"
}

# round: one look at GitHub, and the relay started when there is work for it.
# SAID is the last state logged, so a state that lasts is logged once.
SAID= LAST_N= LAST_T=0
round() {
  local row rc n now
  worker_free || { SAID=busy; return 0; }
  row=$(pick_next 2>&1)
  rc=$?
  now=$(date +%s)
  case $rc in
    0)
      n=${row%%$'\t'*}
      if [ "$n" = "$LAST_N" ] && [ $((now - LAST_T)) -lt "$RETRY" ]; then
        [ "$SAID" = "retry$n" ] ||
          log "#$n is still unclaimed; starting the relay for it again after $((RETRY / 60))m (see .relay/relay-*.log)"
        SAID=retry$n
        return 0
      fi
      LAST_N=$n LAST_T=$now SAID=
      start "ready: ${row//$'\t'/ | }"
      ;;
    1)
      [ "$SAID" = idle ] || log "nothing ready; waiting"
      SAID=idle
      ;;
    *)
      [ "$SAID" = err ] || log "could not ask GitHub for the next issue (exit $rc): ${row//$'\n'/; }"
      SAID=err
      ;;
  esac
}

code_sum() { cksum <"$REPO/hack/relay-watch.sh"; }

# Sourced (hack/relay-watch_test.sh): the functions only.
[ "${BASH_SOURCE[0]}" = "$0" ] || return 0

mkdir -p "$DIR"
[ -e "$DIR/.gitignore" ] || echo '*' >"$DIR/.gitignore"

# Started from a terminal: start the watch, detached, and return.
if [ "${C4_RELAY_WATCH_CHILD:-}" != 1 ]; then
  if ! flock -n "$DIR/watch.lock" true; then
    echo "the relay watch is already running; see $LOG"
    exit 0
  fi
  rm -f "$STOP"
  C4_RELAY_WATCH_CHILD=1 setsid nohup bash "$0" --workers "$WORKERS" >>"$DIR/watch.out" 2>&1 </dev/null &
  echo "relay watch started (pid $!) for $WORKERS worker(s): tail -f $LOG"
  echo "stop: touch $STOP"
  exit 0
fi

cd "$REPO" || exit 1
if [ "${C4_RELAY_WATCH_RESTART:-}" = 1 ]; then
  flock -n 8 || { log "restarted without the watch's lock; exiting"; exit 1; }
else
  exec 8>"$DIR/watch.lock"
  flock -n 8 || { log "another watch holds the lock; exiting"; exit 1; }
fi
# The workers and their sessions don't need to know they were started by a watch.
unset C4_RELAY_WATCH_CHILD C4_RELAY_WATCH_RESTART
CODE_SUM=$(code_sum)
log "watch started: pid $$${C4_CLUSTER:+, C4_CLUSTER=$C4_CLUSTER}, HEAD $(git rev-parse --short HEAD), $WORKERS worker(s), asks GitHub every ${EVERY}s"
PULL_T=0 PULLED=
while :; do
  [ -e "$STOP" ] && { log "stop file found; stopping"; exit 0; }
  now=$(date +%s)
  if [ $((now - PULL_T)) -ge "$PULL_EVERY" ]; then
    PULL_T=$now
    out=$("$REPO/hack/pull.sh" 2>&1)
    # A reason pull.sh repeats (its lines start with the time) is logged once.
    key=$(sed 's/^[0-9][0-9]:[0-9][0-9] //' <<<"$out")
    [ -n "$out" ] && [ "$key" != "$PULLED" ] && log "${out//$'\n'/; }"
    PULLED=$key
    if [ "$(code_sum)" != "$CODE_SUM" ]; then
      log "the watch's code changed (HEAD $(git rev-parse --short HEAD)); restarting"
      # The new watch keeps this one's lock on fd 8.
      exec env C4_RELAY_WATCH_CHILD=1 C4_RELAY_WATCH_RESTART=1 bash "$REPO/hack/relay-watch.sh" --workers "$WORKERS"
    fi
  fi
  round
  sleep "$EVERY"
done
