#!/usr/bin/env bash
# Relay: runs fresh headless Claude sessions one after another, each continuing
# from the newest handoff in docs/handoffs/, so long work carries on overnight
# without a person starting each session. Sessions never run in parallel.
#
#   hack/relay.sh          wait for the running session's next handoff commit, then start
#   hack/relay.sh --now    start from the newest handoff right away
#   touch .relay/stop      stop after the current session, or while waiting
#
# Log: .relay/relay.log, one line per event. Each session's closing account is in
# .relay/session-<n>-<time>.md; open a whole session with `claude --resume <id>`.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
DIR="$REPO/.relay"
LOG="$DIR/relay.log"
STOP="$DIR/stop"
MAX_SESSIONS=${MAX_SESSIONS:-8}
SESSION_TIMEOUT=${SESSION_TIMEOUT:-6h}

read -r -d '' PROMPT <<'EOF'
You run headless, started by hack/relay.sh while the user sleeps; no person is
watching and nobody can answer a question. Read the newest file in
docs/handoffs/ and continue its work, following AGENTS.md and the memories.

Rules for this headless run:
- Never use the Fable model: not for subagents, not for factory Agents, not
  for anything. Always name the model when dispatching (sonnet or opus).
- A headless session ends when you end your turn. Dispatch every subagent with
  run_in_background: false and never end your turn while one is running.
- Hand off between tasks, never inside one: finish the task in flight (its
  implementation, review and fix rounds) before handing off, even past 140k
  tokens. After each task, if your context is past about 100k tokens, hand off
  instead of starting the next one.
- Never wait on the factory (a Crew, a Task, an agent) longer than about 20
  minutes in one stretch; if it still isn't done, note it in the handoff and
  move on to other work or hand off.
- If an action is refused (permissions, auto mode), don't retry it: write it in
  the handoff under Decisions as open, for the user, and carry on.
- To hand off, use the handoff skill (commit and push it) and end your turn.
  The relay starts the next session from it.
- If all the work in the handoff is finished, delete the handoff in the commit
  that finishes it (as the handoff skill says) and end your turn; the relay
  then stops.
- End your turn with a short account of what you did and what is next; it is
  the relay's log of your session.
EOF

log() { echo "$(date -Is) $*" >>"$LOG"; }

mkdir -p "$DIR"
[ -e "$DIR/.gitignore" ] || echo '*' >"$DIR/.gitignore"

# Started from a terminal: check no relay runs, then detach and return.
if [ "${C4_RELAY_CHILD:-}" != 1 ]; then
  if ! flock -n "$DIR/lock" true; then
    echo "a relay is already running; see $LOG" >&2
    exit 1
  fi
  C4_RELAY_CHILD=1 setsid nohup bash "$0" "$@" >>"$DIR/relay.out" 2>&1 </dev/null &
  echo "relay started (pid $!)"
  echo "  log:  tail -f $LOG"
  echo "  stop: touch $STOP"
  exit 0
fi

exec 9>"$DIR/lock"
flock -n 9 || { log "another relay holds the lock; exiting"; exit 1; }
cd "$REPO" || exit 1
rm -f "$STOP"
log "relay started: pid $$, mode ${1:-wait}, HEAD $(git rev-parse --short HEAD), at most $MAX_SESSIONS sessions of $SESSION_TIMEOUT"

if [ "${1:-}" != --now ]; then
  start=$(git log -1 --format=%H -- docs/handoffs/)
  log "waiting for a handoff commit after ${start:0:7}"
  until [ "$(git log -1 --format=%H -- docs/handoffs/)" != "$start" ]; do
    [ -e "$STOP" ] && { log "stopped while waiting"; exit 0; }
    sleep 60
  done
  log "handoff seen: $(git log -1 --format='%h %s' -- docs/handoffs/); starting in 2 minutes"
  sleep 120 # let the handing-off session finish its last message
fi

for i in $(seq 1 "$MAX_SESSIONS"); do
  [ -e "$STOP" ] && { log "stop file found; stopping"; break; }
  newest=$(ls -t docs/handoffs/*.md 2>/dev/null | head -1)
  [ -z "$newest" ] && { log "no handoff left: the work is done"; break; }

  before=$(git log -1 --format=%H -- docs/handoffs/)
  id=$(cat /proc/sys/kernel/random/uuid)
  out="$DIR/session-$i-$(date +%Y%m%d-%H%M%S)"
  log "session $i start: id $id, from $newest, HEAD $(git rev-parse --short HEAD)"
  t0=$(date +%s)
  timeout "$SESSION_TIMEOUT" claude -p --permission-mode auto --model opus \
    --session-id "$id" --output-format json "$PROMPT" >"$out.json" 2>"$out.err"
  rc=$?
  mins=$((($(date +%s) - t0) / 60))

  if [ -s "$out.json" ] && jq -e .result "$out.json" >/dev/null 2>&1; then
    jq -r .result "$out.json" >"$out.md"
    log "session $i end: exit $rc after ${mins}m, $(jq -r '"\(.num_turns) turns, error \(.is_error)"' "$out.json"); account in $out.md"
  else
    log "session $i end: exit $rc after ${mins}m, no result; see $out.err"
  fi
  [ "$rc" = 124 ] && log "session $i hit the $SESSION_TIMEOUT timeout"
  [ "$rc" = 143 ] && log "session $i was killed (SIGTERM)"

  if [ "$(git log -1 --format=%H -- docs/handoffs/)" = "$before" ]; then
    log "session $i wrote no new handoff; stopping (claude --resume $id to see why)"
    break
  fi
  log "session $i handed off: $(git log -1 --format='%h %s' -- docs/handoffs/)"
done
log "relay finished"
