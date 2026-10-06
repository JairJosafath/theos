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
# once the time has passed, not every minute.
#
# When nothing is ready and open issues wait in status/proposed, the watch
# starts one triage session (hack/relay.sh --triage, in a free worker's
# worktree): it reads each proposed issue against docs/design.md and the open
# issues, readies the sound ones, comments on the others what they lack and
# closes duplicates. It takes no issue and changes no code. Triage runs again
# only when a proposed issue it hasn't seen appears, and at most every
# WATCH_TRIAGE seconds (default 1800); .relay/triaged keeps what it saw across
# restarts. status/needs-human stays the user's.
#
# Every 5 minutes it runs
# hack/pull.sh, so pick never reads stale rules, and restarts itself when that
# changed this script. Every WATCH_TRIM_EVERY seconds (default 1800) it runs
# hack/trim-go-cache.sh: nine workers' builds grow Go's build cache by gigabytes
# an hour, faster than opening VS Code trims it.
#
#   hack/relay-watch.sh                start the watch, detached, for one worker
#   hack/relay-watch.sh --workers N    for N workers (1-99); on the cluster device RELAY_MAX_WORKERS (default 9)
#                                      caps them, and hack/relay.sh admits each session by memory
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
TRIAGE_EVERY=${WATCH_TRIAGE:-1800}
PULL_EVERY=${WATCH_PULL_EVERY:-300}
TRIM_EVERY=${WATCH_TRIM_EVERY:-1800}

WORKERS=1
while [ $# -gt 0 ]; do
  case $1 in
    --workers) WORKERS=${2:-}; shift 2 || shift ;;
    --workers=*) WORKERS=${1#*=}; shift ;;
    *) echo "usage: hack/relay-watch.sh [--workers N]" >&2; exit 64 ;;
  esac
done
[[ $WORKERS =~ ^[1-9][0-9]?$ ]] || { echo "relay-watch: --workers takes 1 to 99" >&2; exit 64; }

log() { echo "$(date -Is) $*" >>"$LOG"; }

# running K: whether relay worker K runs (it holds its lock).
running() { [ -e "$DIR/lock-$1" ] && ! flock -n "$DIR/lock-$1" true; }

# worker_free: whether one of the watch's workers doesn't run.
worker_free() {
  local k
  for k in $(seq 1 "$WORKERS"); do running "$k" || return 0; done
  return 1
}

# pick_next, proposed, run_relay, run_triage and trim_caches: what the watch
# asks and starts (faked in the tests).
pick_next() { "$REPO/hack/lib/github.sh" pick; }
proposed() { "$REPO/hack/lib/github.sh" proposed; }
run_relay() { "$REPO/hack/relay.sh" --workers "$WORKERS"; }
run_triage() { "$REPO/hack/relay.sh" --triage --workers "$WORKERS"; }
trim_caches() { "$REPO/hack/trim-go-cache.sh"; }

# trim NOW: trim the build caches when the last trim is TRIM_EVERY seconds old,
# and log what it dropped (it says nothing when a cache is under its limit).
TRIM_T=0
trim() {
  local out
  [ $(($1 - TRIM_T)) -ge "$TRIM_EVERY" ] || return 0
  TRIM_T=$1
  out=$(trim_caches 2>&1 8>&-)
  [ -n "$out" ] && log "${out//$'\n'/; }"
  return 0
}

# start WHY: log WHY and run hack/relay.sh for the workers that aren't running.
start() {
  local out rc
  log "$1; starting the relay"
  out=$(run_relay 2>&1 8>&-)
  rc=$?
  [ -n "$out" ] && log "relay: ${out//$'\n'/; }"
  [ "$rc" = 0 ] || log "hack/relay.sh failed (exit $rc)"
}

# triage NOW: with nothing ready, start a triage session when a proposed issue
# waits that the last triage didn't see, and that triage is TRIAGE_EVERY
# seconds old. $DIR/triaged holds the last triage's time and issues; when its
# session never ran, hack/relay.sh --triage keeps only the time there, so the
# same issues start it again.
TRIAGE_ERR=
triage() {
  local now=$1 list rc n new= out t=0 seen=
  [ -r "$DIR/triaged" ] && read -r t seen <"$DIR/triaged"
  list=$(proposed 2>"$DIR/proposed.err")
  rc=$?
  if [ "$rc" != 0 ]; then
    out="$list $(cat "$DIR/proposed.err" 2>/dev/null)"
    [ -n "$TRIAGE_ERR" ] || log "could not list the proposed issues (exit $rc): ${out//$'\n'/; }"
    TRIAGE_ERR=1
    return 0
  fi
  TRIAGE_ERR=
  list=$(grep -xE '[0-9]+' <<<"$list" | tr '\n' ' ')
  list=${list% }
  for n in $list; do
    [[ " $seen " == *" $n "* ]] || new+=" #$n"
  done
  [ -n "$new" ] && [ $((now - ${t:-0})) -ge "$TRIAGE_EVERY" ] || return 0
  echo "$now $list" >"$DIR/triaged"
  out=$(run_triage 2>&1 8>&-)
  rc=$?
  log "triage: proposed #${list// / #} (new:$new); hack/relay.sh --triage${out:+: ${out//$'\n'/; }}$([ "$rc" = 0 ] || echo " (failed, exit $rc; again after $((TRIAGE_EVERY / 60))m)")"
  [ "$rc" = 0 ] || echo "$now" >"$DIR/triaged"
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
      triage "$now"
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
  trim "$now"
  round
  sleep "$EVERY"
done
