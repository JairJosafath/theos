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
# closes duplicates. It takes no issue and changes no code. Triage runs again,
# at most every WATCH_TRIAGE seconds (default 1800), when a proposed issue it
# hasn't seen appears, when one changed after it (its body, title, labels or a
# comment that isn't the triage's own), when one waits on an issue that closed
# after it (Depends on #M, or an #M in the triage's comment), and in any case
# after WATCH_RETRIAGE seconds (default 10800, 3h); .relay/triaged keeps its
# time and the issues it saw across restarts. status/needs-human stays the user's.
#
# On an idle round it also logs, once per change, what holds back the issues
# pick skips for an open "Depends on #M" when nobody works #M (hack/lib/github.sh
# blocked --unworked), and reports issues that wait on each other in a cycle:
# a comment on each, the newest to status/needs-human (cycles --report).
#
# Every WATCH_CI_EVERY seconds (default 900) it checks main's CI: each
# workflow in .github/workflows that runs on a push to main and whose two
# newest finished runs on main failed (hack/lib/github.sh red) gets an issue
# titled "main is red: <workflow>", status/ready and priority/urgent, so pick
# takes it first. hack/lib/github.sh create skips a title that is open, so a
# red main that lasts, or both devices' watches, file one issue; a single
# failure files nothing. The session that fixes main closes it.
#
# While the relay is paused for a usage limit or a failed login
# (.relay/paused-until, which hack/relay.sh writes), the watch starts nothing,
# not even a triage, and logs the pause once.
#
# Every 5 minutes it runs
# hack/pull.sh, so pick never reads stale rules, and restarts itself when that
# changed this script.
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
RETRIAGE=${WATCH_RETRIAGE:-10800}
PULL_EVERY=${WATCH_PULL_EVERY:-300}
CI_EVERY=${WATCH_CI_EVERY:-900}

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

# pick_next, proposed, blocked, cycles, run_relay and run_triage: what the
# watch asks and starts (faked in the tests).
pick_next() { "$REPO/hack/lib/github.sh" pick; }
blocked() { "$REPO/hack/lib/github.sh" blocked --unworked; }
cycles() { "$REPO/hack/lib/github.sh" cycles --report; }
proposed() { "$REPO/hack/lib/github.sh" proposed; }
run_relay() { "$REPO/hack/relay.sh" --workers "$WORKERS"; }
run_triage() { "$REPO/hack/relay.sh" --triage --workers "$WORKERS"; }
github() { "$REPO/hack/lib/github.sh" "$@"; }

# ci_workflows: FILE<tab>NAME of each workflow that runs on a push to main.
ci_workflows() {
  local f
  for f in "$REPO"/.github/workflows/*.yml "$REPO"/.github/workflows/*.yaml; do
    [ -f "$f" ] || continue
    awk -v file="${f##*/}" '
      /^name:/ { n = $0; sub(/^name:[ \t]*/, "", n); gsub(/["\047]/, "", n) }
      /^on:/ { on = 1; next }
      on && /^[^ \t#]/ { on = 0 }
      on && /^  [a-z_]+:/ { push = ($0 ~ /^  push:/); br = 0; next }
      on && push && /^    [a-z_-]+:/ { br = ($0 ~ /^    branches:/) }
      on && push && br && (/[[ ,]main[],]/ || /^ +- *[\047"]?main[\047"]? *$/) { found = 1 }
      END { if (found) print file "\t" (n == "" ? file : n) }
    ' "$f"
  done
}

# ci_round: file an urgent issue for each workflow that is red on main.
# CI_SAID holds the workflows whose state was logged, so a state that lasts is
# logged once.
CI_SAID=
ci_round() {
  local file name body rc out said=
  while IFS=$'\t' read -r file name; do
    [ -n "$file" ] || continue
    body=$(github red "$file" 2>&1)
    rc=$?
    case $rc in
      0)
        out=$(github create "main is red: $name" - status/ready priority/urgent kind/bug source/dev <<<"$body" 2>&1)
        rc=$?
        if [ "$rc" != 0 ]; then
          said+=" fail:$file"
          [[ " $CI_SAID " == *" fail:$file "* ]] ||
            log "main is red: $name; could not file its issue (exit $rc): ${out//$'\n'/; }"
        else
          said+=" red:$file"
          [[ " $CI_SAID " == *" red:$file "* ]] ||
            log "main is red: $name; issue #${out##*$'\n'}"
        fi
        ;;
      1) ;;
      *)
        said+=" err:$file"
        [[ " $CI_SAID " == *" err:$file "* ]] ||
          log "could not read main's CI for $name (exit $rc): ${body//$'\n'/; }"
        ;;
    esac
  done < <(ci_workflows)
  CI_SAID=$said
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

# triage NOW: with nothing ready, start a triage session, at most every
# TRIAGE_EVERY seconds, when a proposed issue waits that the last triage didn't
# see, one changed after it (not by the triage's own comment), one waits on an
# issue that closed after it, or the last triage is RETRIAGE seconds old.
# $DIR/triaged holds the last triage's time and issues; when its session never
# ran, hack/relay.sh --triage keeps only the time there, so the same issues
# start it again.
TRIAGE_ERR=
triage() {
  local now=$1 list= rc n changed closed why=() new= moved= freed= out t=0 seen= since=()
  [ -r "$DIR/triaged" ] && read -r t seen <"$DIR/triaged"
  t=${t:-0}
  [ $((now - t)) -ge "$TRIAGE_EVERY" ] || return 0
  # Within RETRIAGE, ask what changed since the last triage too.
  [ "$t" -gt 0 ] && [ $((now - t)) -lt "$RETRIAGE" ] && since=(--since "$t")
  out=$(proposed "${since[@]}" 2>"$DIR/proposed.err")
  rc=$?
  if [ "$rc" != 0 ]; then
    out="$out $(cat "$DIR/proposed.err" 2>/dev/null)"
    [ -n "$TRIAGE_ERR" ] || log "could not list the proposed issues (exit $rc): ${out//$'\n'/; }"
    TRIAGE_ERR=1
    return 0
  fi
  TRIAGE_ERR=
  while read -r n changed closed; do
    [[ $n =~ ^[0-9]+$ ]] || continue
    list+=" $n"
    if [[ " $seen " != *" $n "* ]]; then
      new+=" #$n"
    elif [ "${changed:-0}" -gt "$t" ] 2>/dev/null; then
      moved+=" #$n"
    elif [ "${closed:-0}" -gt "$t" ] 2>/dev/null; then
      freed+=" #$n"
    fi
  done <<<"$out"
  list=${list# }
  [ -n "$list" ] || return 0
  [ -n "$new" ] && why+=("new:$new")
  [ -n "$moved" ] && why+=("changed:$moved")
  [ -n "$freed" ] && why+=("waited on a closed issue:$freed")
  [ ${#why[@]} -gt 0 ] || [ ${#since[@]} = 0 ] || return 0
  [ ${#why[@]} -gt 0 ] || why=("read again after $((RETRIAGE / 3600))h")
  echo "$now $list" >"$DIR/triaged"
  out=$(run_triage 2>&1 8>&-)
  rc=$?
  why=$(printf '%s; ' "${why[@]}")
  log "triage: proposed #${list// / #} (${why%; }); hack/relay.sh --triage${out:+: ${out//$'\n'/; }}$([ "$rc" = 0 ] || echo " (failed, exit $rc; again after $((TRIAGE_EVERY / 60))m)")"
  [ "$rc" = 0 ] || echo "$now" >"$DIR/triaged"
}

# held: on an idle round, log what holds back the issues pick skips whose
# blocker nobody works (hack/lib/github.sh blocked --unworked), once per
# change, and report the issues that wait on each other in a cycle (cycles
# --report: a comment on each, the newest to status/needs-human).
HELD= HELD_ERR= CYCLES_ERR=
held() {
  local out rc
  out=$(blocked 2>"$DIR/blocked.err")
  rc=$?
  if [ "$rc" != 0 ]; then
    out="$out $(cat "$DIR/blocked.err" 2>/dev/null)"
    [ -n "$HELD_ERR" ] || log "could not list what holds back the skipped issues (exit $rc): ${out//$'\n'/; }"
    HELD_ERR=1
    return 0
  fi
  HELD_ERR=
  if [ "$out" != "$HELD" ]; then
    HELD=$out
    if [ -n "$out" ]; then
      log "held back by issues nobody works: ${out//$'\n'/; }"
    else
      log "no skipped issue waits on an issue nobody works"
    fi
  fi
  out=$(cycles 2>&1)
  rc=$?
  [ "$rc" = 0 ] && [ -z "$out" ] && { CYCLES_ERR=; return 0; }
  [ "$rc" = 0 ] || { [ -z "$CYCLES_ERR" ] || return 0; CYCLES_ERR=1; }
  log "${out//$'\n'/; }$([ "$rc" = 0 ] || echo " (cycles --report failed, exit $rc)")"
}

# round: one look at GitHub, and the relay started when there is work for it.
# SAID is the last state logged, so a state that lasts is logged once.
SAID= LAST_N= LAST_T=0
round() {
  local row rc n now until why
  now=$(date +%s)
  if [ -r "$DIR/paused-until" ] && read -r until why <"$DIR/paused-until" &&
    [[ $until =~ ^[0-9]+$ ]] && [ "$until" -gt "$now" ]; then
    [ "$SAID" = "paused$until" ] ||
      log "the relay is paused until $(date -d "@$until" -Is) ($why); starting nothing"
    SAID=paused$until
    return 0
  fi
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
      held
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
PULL_T=0 PULLED= CI_T=0
while :; do
  [ -e "$STOP" ] && { log "stop file found; stopping"; exit 0; }
  now=$(date +%s)
  if [ $((now - CI_T)) -ge "$CI_EVERY" ]; then
    CI_T=$now
    ci_round
  fi
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
