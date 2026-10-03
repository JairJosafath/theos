#!/usr/bin/env bash
# Relay: runs fresh headless Claude sessions one after another on the factory's
# own GitHub issues (JairJosafath/c4factory), so work carries on overnight
# without a person starting each session. Sessions never run in parallel. Each
# session works one issue and hands off through a comment on it (the handoff
# skill); hack/lib/github.sh has the GitHub helpers. Product work stays in
# Gitea, where the Crews work it.
#
# Each round picks an issue (hack/lib/github.sh pick: an in-progress one nobody
# holds, else the oldest ready + urgent one, else the oldest ready one) and
# claims it (take); when another agent claims it first, it picks again, up to 5
# times. With nothing ready, hack/evaluate.sh (if present) runs at most once every
# 6 hours to file issues; the relay picks once more and stops if there is still
# nothing. After each session it runs `make backup-releases` if that target exists.
#
#   hack/relay.sh                  wait for the running session to hand off or close its issue, then start
#   hack/relay.sh --now            start right away
#   RELAY_DRY_RUN=1 hack/relay.sh  print the issue and prompt the next session would get; change nothing
#   touch .relay/stop              stop after the current session, or while waiting
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
EVALUATE="$REPO/hack/evaluate.sh"
EVALUATE_STAMP="$DIR/evaluate.last"
EVALUATE_EVERY=${EVALUATE_EVERY:-21600} # seconds: 6 hours
DRY_RUN=${RELAY_DRY_RUN:-}
[ "$DRY_RUN" = 0 ] && DRY_RUN=

# shellcheck source=hack/lib/github.sh
. "$REPO/hack/lib/github.sh"

# prompt N TITLE URL HOW NAME BRANCH: the headless session's prompt for issue
# N, worked by agent NAME on BRANCH.
prompt() {
  local n=$1 title=$2 url=$3 how=$4 name=$5 branch=$6 start
  if [ "$how" = in-progress ]; then
    start="A session worked on it before you: continue from its newest handoff comment."
  else
    start="No session has worked on it yet: the issue body is the goal."
  fi
  cat <<EOF
You run headless, started by hack/relay.sh while the user sleeps; no person is
watching and nobody can answer a question. Your work is GitHub issue #$n on
$C4_GITHUB_REPO, "$title" ($url), and only that issue. The relay has claimed
it for you and labeled it status/in-progress: you are $name, and the issue's
branch is $branch. $start
Start with \`hack/lib/github.sh show $n\`: it prints the issue, its newest
handoff comment and the comments after it. Follow AGENTS.md, the handoff skill
and the memories.

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
- Work on $branch, never on main: \`git fetch origin\`, then
  \`git switch $branch\` if origin has it (an earlier agent pushed it), else
  \`git switch -c $branch origin/main\`. Push after every commit.
- Sign your work as $name: an \`Agent: $name\` trailer on every commit, an
  \`Agent: $name\` line in the pull request body and in your handoff.
- If a tool call's hook says your claim was taken over, do what it says (push
  to $branch-$name, comment that branch on #$n) and end the session.
- End the session in one of three ways; the relay reads which from the issue:
  - Hand off: the handoff skill posts a handoff comment on #$n
    (\`hack/lib/github.sh handoff $n FILE\`). The next session continues #$n.
  - Finished: open a pull request to main whose body says \`Closes #$n\`, merge
    it with a merge commit once its checks are green, and if the issue is still
    open, \`hack/lib/github.sh close $n FILE\`, FILE saying what landed (pull
    request, commit SHAs). The relay then picks the next issue.
  - Stuck without the user: \`hack/lib/github.sh status $n needs-human\` and a
    comment saying why and what you need. The relay then picks the next issue.
- Work you find outside #$n: file it with
  \`hack/lib/github.sh create TITLE FILE status/proposed kind/<kind>\`; don't
  start it.
- End your turn with a short account of what you did and what is next; it is
  the relay's log of your session.
EOF
}

log() {
  if [ -n "$DRY_RUN" ]; then
    echo "dry run: $*"
  else
    echo "$(date -Is) $*" >>"$LOG"
  fi
}

# flush: post the handoffs kept in .relay/ while GitHub was away, before any
# session reads the issues. Exit 1 if some are still unposted.
flush() {
  local out
  compgen -G "$DIR/pending-handoff-*.md" >/dev/null || return 0
  if [ -n "$DRY_RUN" ]; then
    log "would post the pending handoffs: $(cd "$DIR" && echo pending-handoff-*.md)"
    return 0
  fi
  if out=$(gh_flush 2>&1); then
    log "${out//$'\n'/; }"
    return 0
  fi
  log "could not post the pending handoffs in $DIR: ${out//$'\n'/; }"
  return 1
}

# pick: set ISSUE, TITLE, URL and HOW (in-progress, ready-urgent, ready) to the
# issue to work next. Exit 1: nothing to pick; exit 2: GitHub could not be asked.
pick() {
  local row rc
  row=$(gh_pick 2>"$DIR/pick.err")
  rc=$?
  [ -s "$DIR/pick.err" ] && log "pick: $(tr '\n' ' ' <"$DIR/pick.err")"
  [ "$rc" = 0 ] || return "$rc"
  IFS=$'\t' read -r ISSUE TITLE URL HOW <<<"$row"
}

# evaluate: run hack/evaluate.sh if it exists and hasn't run in the last 6
# hours. Exit 1 when it didn't run.
evaluate() {
  local last age out rc t0
  if [ ! -x "$EVALUATE" ]; then
    log "no issue ready, and hack/evaluate.sh is missing or not executable"
    return 1
  fi
  last=$(cat "$EVALUATE_STAMP" 2>/dev/null) || last=0
  [[ $last =~ ^[0-9]+$ ]] || last=0
  age=$(($(date +%s) - last))
  if [ "$age" -lt "$EVALUATE_EVERY" ]; then
    log "no issue ready; the evaluator ran $((age / 60))m ago and runs at most every $((EVALUATE_EVERY / 60))m"
    return 1
  fi
  if [ -n "$DRY_RUN" ]; then
    log "no issue ready; would run hack/evaluate.sh, then pick once more"
    return 1
  fi
  date +%s >"$EVALUATE_STAMP"
  out="$DIR/evaluate-$(date +%Y%m%d-%H%M%S).out"
  log "no issue ready; running hack/evaluate.sh, output in $out"
  t0=$(date +%s)
  timeout "$SESSION_TIMEOUT" "$EVALUATE" >"$out" 2>&1
  rc=$?
  log "evaluator end: exit $rc after $((($(date +%s) - t0) / 60))m"
}

# backup: run `make backup-releases` if the Makefile has that target.
backup() {
  local out="$DIR/backup-releases.out"
  make -n backup-releases >/dev/null 2>&1 || return 0
  if [ -n "$DRY_RUN" ]; then
    log "would run make backup-releases after the session"
    return 0
  fi
  if timeout 30m make backup-releases >"$out" 2>&1; then
    log "backup-releases: done"
  else
    log "backup-releases: failed with exit $?; see $out"
  fi
}

# handed_off_since TIME: a handoff comment was posted or kept, or an issue
# closed, at or after TIME (UTC, 2006-01-02T15:04:05Z).
handed_off_since() {
  local since=$1 json
  compgen -G "$DIR/pending-handoff-*.md" >/dev/null && return 0
  json=$(gh_api "repos/$C4_GITHUB_REPO/issues/comments?since=$since&per_page=100") || return 1
  jq -e --arg s "$since" --arg m "$GH_HANDOFF_MARKER" \
    'any(.[]; .created_at >= $s and (.body | sub("^\\s+"; "") | startswith($m)))' <<<"$json" >/dev/null &&
    return 0
  json=$(gh_api "repos/$C4_GITHUB_REPO/issues?state=closed&since=$since&per_page=100") || return 1
  jq -e --arg s "$since" 'any(.[]; (.pull_request | not) and .closed_at >= $s)' <<<"$json" >/dev/null
}

# handoff_id N: the id of N's newest handoff comment, or nothing.
handoff_id() {
  local json
  json=$(gh_latest_handoff "$1") || return 1
  [ -z "$json" ] || jq -r .id <<<"$json"
}

# outcome N BEFORE: how the session left issue N: closed, needs-human,
# handed-off (a handoff comment newer than BEFORE) or nothing.
outcome() {
  local n=$1 before=$2 state latest
  state=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n" \
    --jq '"\(.state) \(.labels | map(.name) | join(","))"') || return 1
  case $state in
    closed*) echo closed; return 0 ;;
    *status/needs-human*) echo needs-human; return 0 ;;
  esac
  latest=$(handoff_id "$n") || return 1
  if [ -n "$latest" ] && [ "$latest" != "$before" ]; then echo handed-off; else echo nothing; fi
}

mkdir -p "$DIR"
[ -e "$DIR/.gitignore" ] || echo '*' >"$DIR/.gitignore"

# Started from a terminal: check no relay runs, then detach and return. The
# token is read here, where the git credential helper answers; the detached
# relay and its sessions inherit it in the environment.
if [ -z "$DRY_RUN" ] && [ "${C4_RELAY_CHILD:-}" != 1 ]; then
  if ! flock -n "$DIR/lock" true; then
    echo "a relay is already running; see $LOG" >&2
    exit 1
  fi
  gh_token || exit 1
  C4_RELAY_CHILD=1 setsid nohup bash "$0" "$@" >>"$DIR/relay.out" 2>&1 </dev/null &
  echo "relay started (pid $!)"
  echo "  log:  tail -f $LOG"
  echo "  stop: touch $STOP"
  exit 0
fi

cd "$REPO" || exit 1
if [ -z "$DRY_RUN" ]; then
  exec 9>"$DIR/lock"
  flock -n 9 || { log "another relay holds the lock; exiting"; exit 1; }
  rm -f "$STOP"
fi
gh_token 2>/dev/null || { log "no GitHub token; stopping"; exit 1; }
log "relay started: pid $$, mode ${DRY_RUN:+dry run, }${1:-wait}, HEAD $(git rev-parse --short HEAD), at most $MAX_SESSIONS sessions of $SESSION_TIMEOUT, issues on $C4_GITHUB_REPO"

if [ -z "$DRY_RUN" ] && [ "${1:-}" != --now ]; then
  since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  log "waiting for a handoff comment or a closed issue after $since"
  until handed_off_since "$since"; do
    [ -e "$STOP" ] && { log "stopped while waiting"; exit 0; }
    sleep 60
  done
  log "handoff seen; starting in 2 minutes"
  sleep 120 # let the handing-off session finish its last message
fi

for i in $(seq 1 "$MAX_SESSIONS"); do
  [ -e "$STOP" ] && { log "stop file found; stopping"; break; }
  flush || { log "GitHub can't be reached; stopping"; break; }

  pick
  rc=$?
  if [ "$rc" = 1 ] && evaluate; then
    pick
    rc=$?
    [ "$rc" = 1 ] && { log "still no issue ready after the evaluator; stopping"; break; }
  fi
  [ "$rc" = 1 ] && { log "no issue ready; stopping"; break; }
  [ "$rc" = 0 ] || { log "could not ask GitHub for the next issue; stopping"; break; }

  id=$(cat /proc/sys/kernel/random/uuid)
  if [ -n "$DRY_RUN" ]; then
    log "would claim #$ISSUE ($HOW) for session $id: $TITLE, $URL"
    log "would start session $i: claude -p --permission-mode auto --model opus --session-id $id, with this prompt:"
    echo "-----"
    prompt "$ISSUE" "$TITLE" "$URL" "$HOW" "<name>" "<branch>"
    echo "-----"
    backup
    exit 0
  fi

  for try in 1 2 3 4 5; do
    claim=$(C4_SESSION_ID=$id gh_take "$ISSUE" 2>>"$LOG")
    rc=$?
    [ "$rc" = 4 ] || break
    log "#$ISSUE is claimed by another agent (try $try); picking again"
    pick || break
  done
  [ "$rc" = 0 ] || { log "could not claim an issue (exit $rc); stopping"; break; }
  IFS=$'\t' read -r name branch <<<"$claim"
  before=$(handoff_id "$ISSUE") || { log "could not read #$ISSUE's handoffs; stopping"; break; }
  out="$DIR/session-$i-$(date +%Y%m%d-%H%M%S)"
  log "session $i start: id $id, $name on #$ISSUE ($HOW) $TITLE, branch $branch, HEAD $(git rev-parse --short HEAD)"
  t0=$(date +%s)
  timeout "$SESSION_TIMEOUT" claude -p --permission-mode auto --model opus \
    --session-id "$id" --output-format json "$(prompt "$ISSUE" "$TITLE" "$URL" "$HOW" "$name" "$branch")" \
    >"$out.json" 2>"$out.err"
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

  backup
  flush || { log "GitHub can't be reached; stopping"; break; }
  result=$(outcome "$ISSUE" "$before") || { log "could not read #$ISSUE after session $i; stopping"; break; }
  case $result in
    closed) log "session $i closed #$ISSUE" ;;
    needs-human) log "session $i left #$ISSUE waiting for the user (status/needs-human)" ;;
    handed-off) log "session $i handed off #$ISSUE" ;;
    *)
      log "session $i left no handoff on #$ISSUE; stopping (claude --resume $id to see why)"
      break
      ;;
  esac
done
log "relay finished"
