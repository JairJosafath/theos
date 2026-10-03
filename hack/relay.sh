#!/usr/bin/env bash
# Relay: runs fresh headless Claude sessions on the factory's own GitHub issues
# (JairJosafath/c4factory), so work carries on overnight without a person
# starting each session. Each session works one issue and hands off through a
# comment on it (the handoff skill); hack/lib/github.sh has the GitHub helpers.
# Product work stays in Gitea, where the Crews work it.
#
# It runs N workers side by side (docs/plans/parallel-agents.md). Worker k runs
# from this checkout and starts its sessions, one after another, in its own git
# worktree .claude/worktrees/relay-k, switched to each issue's branch; the
# worktree's .relay/ holds its claim. Each round picks an issue
# (hack/lib/github.sh pick: an in-progress one nobody holds, else the oldest
# ready + urgent one, else the oldest ready one) and claims it (take); when
# another agent claims it first, it picks again, up to 5 times. Claims keep the
# workers and hand-started sessions off each other's issues. A session that ends
# still holding its claim gets it released. With nothing ready, hack/evaluate.sh
# (if present) runs when the newest evaluation on the "Factory evaluations"
# issue is 6 hours old, under a claim on that issue, so it runs once across
# workers and devices; the worker picks once more and stops if there is still
# nothing. After each session it runs `make backup-releases` if that target exists.
#
# Issues that need the Kind cluster carry needs/cluster. On a device without
# one, C4_CLUSTER=none (the light devcontainer sets it): the workers skip those
# issues, and a session that finds its issue needs the cluster lands what it
# can, labels it and hands it back for a device with one. On a device with the
# cluster, one checkout at a time works a needs/cluster issue.
#
# Workers load their code once, from this checkout. Before each pick a worker
# runs hack/pull.sh here, and when that changed hack/relay.sh or
# hack/lib/github.sh it restarts itself on the new code, so a relay that runs
# for hours never picks by stale rules. RELAY_PULL=0 turns this off, to try relay
# changes from a branch.
#
#   hack/relay.sh                  start one worker
#   hack/relay.sh --workers N      start N workers (1-9); workers already running are left alone
#   RELAY_DRY_RUN=1 hack/relay.sh  print the issue and prompt the next session would get; change nothing
#   touch .relay/stop              stop every worker after its current session
#
# Log: .relay/relay-<k>.log, one line per event. Each session's closing account is
# in .relay/session-<k>-<n>-<time>.md; open a whole session with `claude --resume <id>`
# from the worker's worktree.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
DIR="$REPO/.relay"
STOP="$DIR/stop"
MAX_SESSIONS=${MAX_SESSIONS:-8}
SESSION_TIMEOUT=${SESSION_TIMEOUT:-6h}
EVALUATIONS_TITLE="Factory evaluations"
EVALUATE_EVERY=${EVALUATE_EVERY:-21600} # seconds: 6 hours
DRY_RUN=${RELAY_DRY_RUN:-}
[ "$DRY_RUN" = 0 ] && DRY_RUN=
PULL=${RELAY_PULL:-1}

WORKERS=1
while [ $# -gt 0 ]; do
  case $1 in
    --workers) WORKERS=${2:-}; shift 2 || shift ;;
    --workers=*) WORKERS=${1#*=}; shift ;;
    --now) shift ;; # the relay no longer waits for a handoff; kept for old habits
    *) echo "usage: hack/relay.sh [--workers N]" >&2; exit 64 ;;
  esac
done
[[ $WORKERS =~ ^[1-9]$ ]] || { echo "relay: --workers takes 1 to 9" >&2; exit 64; }

K=${C4_RELAY_WORKER:-1}
WT="$REPO/.claude/worktrees/relay-$K"
LOG="$DIR/relay-$K.log"

# shellcheck source=hack/lib/github.sh
. "$REPO/hack/lib/github.sh"
# The worker's claim and unposted handoffs live in its worktree, where its
# sessions' hack/lib/github.sh and heartbeat hook look for them.
GH_PENDING_DIR="$WT/.relay"
GH_CLAIM_FILE="$GH_PENDING_DIR/claim"

# prompt N TITLE URL HOW NAME BRANCH: the headless session's prompt for issue
# N, worked by agent NAME on BRANCH.
prompt() {
  local n=$1 title=$2 url=$3 how=$4 name=$5 branch=$6 start cluster
  if [ "${C4_CLUSTER:-}" = none ]; then
    cluster="- This device has no Kind cluster (C4_CLUSTER=none): no deploys,
  factory-ops, Crews or make e2e; make check works (envtest needs no cluster).
  If the issue turns out to need the cluster, land what you can (the code pull
  request, with unit and envtest tests), label it
  (\`hack/lib/github.sh label $n needs/cluster\`) and hand off with what is
  left (deploy and verify): a device with the cluster takes it from there."
  else
    cluster="- This device's Kind cluster is shared by its checkouts, one
  needs/cluster issue at a time. If the issue needs the cluster and lacks the
  label, add it before you use the cluster
  (\`hack/lib/github.sh label $n needs/cluster\`); exit 4 means another
  checkout holds the cluster: land what needs no cluster and hand off."
  fi
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
branch is $branch, checked out in this worktree, relay-$K. Other agents work
other issues at the same time in other checkouts; stay in this one. $start
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
- Work on $branch, never on main; the relay has switched this worktree to it
  (from origin if an earlier agent pushed it, else from origin/main). Push
  after every commit: \`git push -u origin HEAD\`.
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
$cluster
- Work you find outside #$n: file it with
  \`hack/lib/github.sh create TITLE FILE status/proposed kind/<kind>\`; don't
  start it; add needs/cluster to its labels if it needs the cluster.
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

# flush: post the handoffs the worker's sessions kept while GitHub was away,
# before any session reads the issues. Exit 1 if some are still unposted.
flush() {
  local out
  compgen -G "$GH_PENDING_DIR/pending-handoff-*.md" >/dev/null || return 0
  if [ -n "$DRY_RUN" ]; then
    log "would post the pending handoffs: $(cd "$GH_PENDING_DIR" && echo pending-handoff-*.md)"
    return 0
  fi
  if out=$(gh_flush 2>&1); then
    log "${out//$'\n'/; }"
    return 0
  fi
  log "could not post the pending handoffs in $GH_PENDING_DIR: ${out//$'\n'/; }"
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

# evaluate: run hack/evaluate.sh if the newest evaluation on the "Factory
# evaluations" issue is at least 6 hours old, under a claim on that issue so
# no other worker or device evaluates at the same time. Exit 1 when it didn't run.
evaluate() {
  local issue last age out rc t0
  if [ "${C4_CLUSTER:-}" = none ]; then
    log "no issue ready; the evaluator reads the cluster, and this device has none (C4_CLUSTER=none)"
    return 1
  fi
  if [ ! -x "$REPO/hack/evaluate.sh" ]; then
    log "no issue ready, and hack/evaluate.sh is missing or not executable"
    return 1
  fi
  issue=$(gh_api --paginate "repos/$C4_GITHUB_REPO/issues?state=open&per_page=100" \
    --jq ".[] | select((.pull_request | not) and .title == \"$EVALUATIONS_TITLE\") | .number" |
    head -1) || { log "no issue ready; could not look for the $EVALUATIONS_TITLE issue"; return 1; }
  if [ -n "$issue" ]; then
    last=$(gh_comments "$issue" | jq -r '[.[] | select(.body | contains("<!-- factory-evaluator"))] | last | .created_at // empty') ||
      { log "no issue ready; could not read #$issue's evaluations"; return 1; }
    if [ -n "$last" ]; then
      age=$(($(date +%s) - $(date -d "$last" +%s)))
      if [ "$age" -lt "$EVALUATE_EVERY" ]; then
        log "no issue ready; the newest evaluation on #$issue is $((age / 60))m old, and the evaluator runs at most every $((EVALUATE_EVERY / 60))m"
        return 1
      fi
    fi
  fi
  if [ -n "$DRY_RUN" ]; then
    log "no issue ready; would claim #${issue:-?} and run hack/evaluate.sh, then pick once more"
    return 1
  fi
  if [ -n "$issue" ]; then
    GH_TAKE_KEEP_STATUS=1 gh_take "$issue" >/dev/null 2>>"$LOG"
    rc=$?
    [ "$rc" = 4 ] && { log "no issue ready; another agent is evaluating (#$issue is claimed)"; return 1; }
    [ "$rc" = 0 ] || { log "no issue ready; could not claim #$issue to evaluate (exit $rc)"; return 1; }
  fi
  if ! git -C "$WT" switch -q --detach origin/main 2>>"$LOG"; then
    log "could not switch $WT to origin/main to evaluate"
    gh_release "relay could not prepare its worktree" >/dev/null 2>>"$LOG"
    return 1
  fi
  out="$DIR/evaluate-$(date +%Y%m%d-%H%M%S).out"
  log "no issue ready; running hack/evaluate.sh in relay-$K, output in $out"
  t0=$(date +%s)
  (cd "$WT" && RELAY_DIR="$DIR" timeout "$SESSION_TIMEOUT" hack/evaluate.sh) >"$out" 2>&1
  rc=$?
  log "evaluator end: exit $rc after $((($(date +%s) - t0) / 60))m"
  gh_release evaluated >/dev/null 2>>"$LOG"
}

# worktree: create the worker's worktree if it is missing, fetch, and stash
# whatever a dead session left uncommitted in it, so nothing is lost.
worktree() {
  git -C "$REPO" fetch -q origin 2>>"$LOG" || { log "git fetch failed"; return 1; }
  if [ ! -e "$WT/.git" ]; then
    git -C "$REPO" worktree add -q --detach "$WT" origin/main 2>>"$LOG" || return 1
    log "created the worktree $WT"
  fi
  mkdir -p "$GH_PENDING_DIR"
  [ -e "$GH_PENDING_DIR/.gitignore" ] || echo '*' >"$GH_PENDING_DIR/.gitignore"
  if [ -n "$(git -C "$WT" status --porcelain)" ]; then
    git -C "$WT" stash push -q -u -m "relay-$K: left uncommitted on $(git -C "$WT" rev-parse --abbrev-ref HEAD), $(date -Is)" 2>>"$LOG" ||
      return 1
    log "stashed uncommitted changes left in $WT (git -C $WT stash list)"
  fi
}

# code_sum: a checksum of the relay's own code, to see whether a pull changed it.
code_sum() {
  cat "$REPO/hack/relay.sh" "$REPO/hack/lib/github.sh" 2>/dev/null | cksum
}

# update LEFT: run hack/pull.sh in this checkout, and restart the worker with
# LEFT sessions when that changed the relay's code.
update() {
  local left=$1 out
  [ "$PULL" = 0 ] && return 0
  out=$("$REPO/hack/pull.sh" 2>&1)
  [ -n "$out" ] && log "${out//$'\n'/; }"
  [ "$(code_sum)" = "$CODE_SUM" ] && return 0
  [ -e "$STOP" ] && { log "stop file found; stopping"; exit 0; }
  log "the relay's code changed (HEAD $(git rev-parse --short HEAD)); restarting with $left sessions left"
  # The new worker keeps this one's lock on fd 9.
  exec env MAX_SESSIONS="$left" C4_RELAY_RESTART=1 bash "$REPO/hack/relay.sh"
}

# switch_branch BRANCH: switch the worktree to BRANCH: the local branch
# (fast-forwarded to origin's), else origin's, else a new one from origin/main.
switch_branch() {
  local b=$1 g=(git -C "$WT")
  if "${g[@]}" show-ref -q --verify "refs/heads/$b"; then
    "${g[@]}" switch -q --ignore-other-worktrees "$b" 2>>"$LOG" || return 1
    if "${g[@]}" show-ref -q --verify "refs/remotes/origin/$b"; then
      "${g[@]}" merge -q --ff-only "origin/$b" 2>>"$LOG" ||
        log "local $b and origin/$b have diverged; the session sorts it out"
    fi
  elif "${g[@]}" show-ref -q --verify "refs/remotes/origin/$b"; then
    "${g[@]}" switch -q -c "$b" --track "origin/$b" 2>>"$LOG"
  else
    "${g[@]}" switch -q -c "$b" --no-track origin/main 2>>"$LOG"
  fi
}

# backup: run `make backup-releases` if the Makefile has that target and no
# other worker is running it.
backup() {
  local out="$DIR/backup-releases.out"
  make -n backup-releases >/dev/null 2>&1 || return 0
  if [ -n "$DRY_RUN" ]; then
    log "would run make backup-releases after the session"
    return 0
  fi
  if flock -n "$DIR/backup.lock" timeout 30m make backup-releases >"$out" 2>&1; then
    log "backup-releases: done"
  else
    log "backup-releases: failed with exit $?; see $out"
  fi
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

# Started from a terminal: start the workers that aren't running, detached,
# and return. The token is read here, where the git credential helper answers;
# the workers and their sessions inherit it in the environment.
if [ -z "$DRY_RUN" ] && [ "${C4_RELAY_CHILD:-}" != 1 ]; then
  gh_token || exit 1
  rm -f "$STOP"
  for k in $(seq 1 "$WORKERS"); do
    if ! flock -n "$DIR/lock-$k" true; then
      echo "worker $k is already running; see $DIR/relay-$k.log"
      continue
    fi
    C4_RELAY_CHILD=1 C4_RELAY_WORKER=$k setsid nohup bash "$0" >>"$DIR/relay-$k.out" 2>&1 </dev/null &
    echo "worker $k started (pid $!): tail -f $DIR/relay-$k.log"
  done
  echo "stop: touch $STOP"
  exit 0
fi

cd "$REPO" || exit 1
if [ -z "$DRY_RUN" ]; then
  if [ "${C4_RELAY_RESTART:-}" = 1 ]; then
    flock -n 9 || { log "restarted without the lock of worker $K; exiting"; exit 1; }
  else
    exec 9>"$DIR/lock-$K"
    flock -n 9 || { log "another worker $K holds the lock; exiting"; exit 1; }
  fi
fi
CODE_SUM=$(code_sum)
gh_token 2>/dev/null || { log "no GitHub token; stopping"; exit 1; }
log "worker $K started: pid $$${DRY_RUN:+, dry run}${C4_CLUSTER:+, C4_CLUSTER=$C4_CLUSTER}, worktree $WT, HEAD $(git rev-parse --short HEAD), at most $MAX_SESSIONS sessions of $SESSION_TIMEOUT, issues on $C4_GITHUB_REPO"
# Workers started together would all race for the same first issue.
[ -n "$DRY_RUN" ] || [ "${C4_RELAY_RESTART:-}" = 1 ] || sleep $(((K - 1) * 20))

for i in $(seq 1 "$MAX_SESSIONS"); do
  [ -e "$STOP" ] && { log "stop file found; stopping"; break; }
  if [ -z "$DRY_RUN" ]; then
    update $((MAX_SESSIONS - i + 1))
    worktree || { log "could not prepare the worktree $WT; stopping"; break; }
    if [ "$i" = 1 ] && gh_claim_read; then
      log "$(gh_release "worker $K restarted" 2>&1 | tr '\n' ' ')"
    fi
  fi
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
    log "would switch $WT to the issue's branch and start session $i there: claude -p --permission-mode auto --model opus --session-id $id, with this prompt:"
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
  if ! switch_branch "$branch"; then
    log "could not switch $WT to $branch; releasing #$ISSUE and stopping"
    gh_release "relay could not prepare its worktree" >/dev/null 2>>"$LOG"
    break
  fi
  before=$(handoff_id "$ISSUE") || {
    log "could not read #$ISSUE's handoffs; stopping"
    gh_release "relay could not read the issue" >/dev/null 2>>"$LOG"
    break
  }
  out="$DIR/session-$K-$i-$(date +%Y%m%d-%H%M%S)"
  log "session $i start: id $id, $name on #$ISSUE ($HOW) $TITLE, branch $branch, HEAD $(git -C "$WT" rev-parse --short HEAD)"
  t0=$(date +%s)
  (cd "$WT" && timeout "$SESSION_TIMEOUT" claude -p --permission-mode auto --model opus \
    --session-id "$id" --output-format json "$(prompt "$ISSUE" "$TITLE" "$URL" "$HOW" "$name" "$branch")") \
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
  result=$(outcome "$ISSUE" "$before") || result=unknown
  if gh_claim_read; then
    case $result in
      closed) reason=finished ;;
      handed-off) reason="handed off" ;;
      *) reason="session ended without a handoff" ;;
    esac
    if released=$(gh_release "$reason" 2>&1); then
      log "${released//$'\n'/; }"
    else
      log "could not release the claim on #$ISSUE; it expires after $C4_LEASE_TTL: ${released//$'\n'/; }"
    fi
  fi
  case $result in
    closed) log "session $i closed #$ISSUE" ;;
    needs-human) log "session $i left #$ISSUE waiting for the user (status/needs-human)" ;;
    handed-off) log "session $i handed off #$ISSUE" ;;
    unknown) log "could not read #$ISSUE after session $i; stopping"; break ;;
    *)
      log "session $i left no handoff on #$ISSUE; stopping (claude --resume $id in $WT to see why)"
      break
      ;;
  esac
done
log "worker $K finished"
