#!/usr/bin/env bash
# Relay: runs fresh headless Claude sessions on the factory's own GitHub issues
# (JairJosafath/c4factory), so work carries on overnight without a person
# starting each session. Each session works one issue and hands off through a
# comment on it (the handoff skill); hack/lib/github.sh has the GitHub helpers.
# Product work stays in Gitea, where the Crews work it.
#
# It runs N workers side by side (docs/plans/done/parallel-agents.md). Worker k runs
# from this checkout and starts its sessions, one after another, in its own git
# worktree .claude/worktrees/relay-k, switched to each issue's branch; the
# worktree's .relay/ holds its claim. Each round picks an issue
# (hack/lib/github.sh pick: an in-progress one nobody holds, else the oldest
# ready + urgent one, else the oldest ready one) and claims it (take); when
# another agent claims it first, it picks again, up to 5 times. Claims keep the
# workers and hand-started sessions off each other's issues. A session that ends
# still holding its claim or the cluster lease gets them released. With nothing
# ready, a worker stops, without waiting in admit: it asks (pick is read-only)
# before admit as well as after, so an idle worker takes no session-start slot.
#
# Triage: with nothing ready, hack/relay-watch.sh starts one triage session
# (--triage) on the open status/proposed issues, which relay sessions file
# their follow-ups as. Its worker switches its worktree to origin/main and runs
# one session that takes no issue and changes no code: it readies each sound
# proposed issue (clear, says when it is done, no duplicate, names what it
# waits on with Depends on #M), fills a gap it can write itself (a Depends on
# #M line) and readies the issue, comments on the
# others what they lack, readies one the design doesn't settle as design work
# (designing is ours, never a question for the user), escalates one that
# nothing it can write moves on (it needs a decision: something only the user
# can give, or a direction it is unsure of, with the choice it would make) to
# the maintainer (status/needs-maintainer), closes duplicates, and ends with an
# account of what it did. It never takes an issue out of
# status/needs-maintainer or status/needs-human.
#
# The maintainer: hack/relay-watch.sh starts one maintainer session
# (--maintain) every 2 hours when anything changed, acting as the project's
# maintainer on the owner's behalf (GOVERNANCE.md, the maintainer skill). It
# runs in its own worktree, .claude/worktrees/relay-maintainer, on origin/main
# for reading only, under its own lock (.relay/maintain.lock), and doesn't
# count against RELAY_MAX_WORKERS: it must run while every worker is busy. It
# goes through admit, and goes first: no worker takes a start slot while it
# waits (#262), though it waits for memory and the gap like any session. It
# takes no issue and commits nothing; it decides, corrects, answers and keeps
# the pinned Maintainer report
# (hack/lib/github.sh report). Agents escalate to it, with
# status/needs-maintainer; only it sets status/needs-human, for what
# GOVERNANCE.md reserves to the owner (docs/plans/done/maintainer.md). When it
# ends it writes .relay/maintained, `<epoch> <activity>`: when the sweep
# started (so an issue escalated while it ran is still new to the watch) and
# hack/lib/github.sh activity, read after the sweep so its own comments don't
# count as new activity; a sweep that didn't run to
# its end (no headroom, a limit, a crash, a timeout) leaves it as it was, and
# the watch tries again after WATCH_RETRY.
#
# Agents never lease, create or deploy to a Kind cluster: make check (Go
# tests, validate-yaml, envtest) is the gate, and Kind is a manual tier only a
# human runs (hack/lib/github.sh cluster acquire refuses without
# C4_HUMAN_CLUSTER=1, which the relay never sets). No prompt here tells a
# session to use one, and every issue is any worker's, needs/cluster or not.
#
# Workers load their code once, from this checkout. Before each pick a worker
# runs hack/pull.sh here, and when that changed hack/relay.sh or
# hack/lib/github.sh it restarts itself on the new code, so a relay that runs
# for hours never picks by stale rules. RELAY_PULL=0 turns this off, to try relay
# changes from a branch.
#
#   hack/relay.sh                  start one worker
#   hack/relay.sh --workers N      start N workers (1-99); workers already running are left alone
#   hack/relay.sh --triage [--workers N]
#                                  start one triage session in the first of workers 1-N that isn't running
#   hack/relay.sh --maintain       start one maintainer session, unless one runs
#
# On the device with the Kind cluster (C4_CLUSTER not none), memory is the
# limit: at most RELAY_MAX_WORKERS workers run (default 9, so the memory gate,
# not a count, holds them). On every device a worker waits before each pick
# until MemAvailable is at least RELAY_MIN_MEM_GIB (default 2) and the last
# session start is RELAY_ADMIT_GAP seconds old (default 120), and stops after
# waiting RELAY_MEM_WAIT seconds (default 3600). Heavy jobs (make check, test,
# lint, deploy, ...) queue device-wide for HEAVY_JOBS slots (hack/heavy-slot.sh).
# A session that ends on a usage limit or a failed login pauses the device: it
# writes .relay/paused-until (the reset time the message gives, else now plus
# RELAY_LIMIT_WAIT, default 30m), and until then every worker waits in admit
# without picking or claiming, and the watch starts nothing. A session that
# ends without a handoff and without a new commit on the issue's branch counts
# as failed, in .relay/fails-<N>; at RELAY_MAX_FAILS (default 3) in a row the
# relay escalates the issue to the maintainer (status/needs-maintainer) with a
# comment naming each one. A session that leaves its issue in
# status/needs-maintainer or status/needs-human has finished, as one that
# closes it or hands it off. A
# handoff or a new commit resets the count; a paused session doesn't count.
# A session whose transcript (under $CLAUDE_CONFIG_DIR/projects/) hasn't
# changed for RELAY_IDLE (default 45m) is stuck: the relay ends it (SIGTERM,
# then SIGKILL after RELAY_IDLE_KILL) and counts it as failed, commit or not.
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
TRIAGE=${C4_RELAY_TRIAGE:-}
MAINTAIN=${C4_RELAY_MAINTAIN:-}
DRY_RUN=${RELAY_DRY_RUN:-}
[ "$DRY_RUN" = 0 ] && DRY_RUN=
PULL=${RELAY_PULL:-1}

WORKERS=1
while [ $# -gt 0 ]; do
  case $1 in
    --workers) WORKERS=${2:-}; shift 2 || shift ;;
    --workers=*) WORKERS=${1#*=}; shift ;;
    --triage) TRIAGE=1; shift ;;
    --maintain) MAINTAIN=1; shift ;;
    --now) shift ;; # the relay no longer waits for a handoff; kept for old habits
    *) echo "usage: hack/relay.sh [--triage | --maintain] [--workers N]" >&2; exit 64 ;;
  esac
done
[[ $WORKERS =~ ^[1-9][0-9]?$ ]] || { echo "relay: --workers takes 1 to 99" >&2; exit 64; }
MAX_WORKERS=${RELAY_MAX_WORKERS:-9}
MIN_MEM_GIB=${RELAY_MIN_MEM_GIB:-2}
ADMIT_GAP=${RELAY_ADMIT_GAP:-120}  # seconds between session starts across workers
MEM_WAIT=${RELAY_MEM_WAIT:-3600}   # seconds a worker waits for headroom before it stops
MEM_POLL=${RELAY_MEM_POLL:-60}
MEMINFO=${RELAY_MEMINFO:-/proc/meminfo}
LIMIT_WAIT=${RELAY_LIMIT_WAIT:-30m} # the pause after a limit whose message names no reset time
MAX_FAILS=${RELAY_MAX_FAILS:-3}     # failed sessions in a row before an issue goes to the user
IDLE=${RELAY_IDLE:-45m}             # a session whose transcript doesn't change this long is ended
IDLE_KILL=${RELAY_IDLE_KILL:-1m}    # from its SIGTERM to its SIGKILL
IDLE_POLL=${RELAY_IDLE_POLL:-60}    # seconds between looks at the transcript

K=${C4_RELAY_WORKER:-1}
# The maintainer is no numbered worker: its own worktree, log and lock.
[ -n "$MAINTAIN" ] && K=maintainer
WT="$REPO/.claude/worktrees/relay-$K"
LOG="$DIR/relay-$K.log"
LOCK="$DIR/lock-$K"
[ -n "$MAINTAIN" ] && LOCK="$DIR/maintain.lock"

# shellcheck source=hack/lib/github.sh
. "$REPO/hack/lib/github.sh"
# The worker's claim and unposted handoffs live in its worktree, where its
# sessions' hack/lib/github.sh and heartbeat hook look for them.
GH_PENDING_DIR="$WT/.relay"
GH_CLAIM_FILE="$GH_PENDING_DIR/claim"

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
    it with a merge commit once \`make check\` passes, and if the issue is still
    open, \`hack/lib/github.sh close $n FILE\`, FILE saying what landed (pull
    request, commit SHAs). The relay then picks the next issue.
  - Stuck on a decision: \`hack/lib/github.sh status $n needs-maintainer\` and
    a comment saying why and what you need. The relay then picks the next
    issue, and the maintainer decides it or takes it to the user. How to build
    it is yours to decide (AGENTS.md, "Designing is our job"): this is for
    what only the user can give (a tag, access, credentials, money), or a
    direction you are unsure of, asked with the choice you would make and
    why. Never set status/needs-human: only the maintainer does.
- Never lease, create or deploy to a Kind cluster, and never run make e2e,
  kind or factory-ops: make check (Go tests, validate-yaml, envtest) is the
  gate, and Kind is a tier only a human runs. When only a real node would show
  something, land the code with the tests the lower tiers can give and say in
  the pull request what a human run of Kind would check.
- Work you find outside #$n: file it with
  \`hack/lib/github.sh create TITLE FILE status/proposed kind/<kind>\`; don't
  start it.
- End your turn with a short account of what you did and what is next; it is
  the relay's log of your session.
EOF
}

# triage_prompt ISSUES: the triage session's prompt, for the proposed ISSUES (#1 #2 ...).
triage_prompt() {
  cat <<EOF
You run headless, started by hack/relay-watch.sh (hack/relay.sh --triage)
while the user sleeps; no person is watching and nobody can answer a question.
No issue on $C4_GITHUB_REPO is ready for the relay, and these wait in
status/proposed: $1. Your work is to triage them, so the relay has sound work:
ready each one that is sound, and say on the others what they lack. You take
no issue, change no code, commit nothing and open no pull request; this
worktree, relay-$K, is on origin/main for reading only.

Start with \`hack/lib/github.sh proposed\` (the list now; triage those) and
\`hack/lib/github.sh show N\` for each. Read docs/design.md and AGENTS.md, and
list the open issues to compare against: \`gh issue list --repo $C4_GITHUB_REPO
--state open --limit 200 --json number,title,labels,milestone\` (gh has the
token). \`hack/lib/github.sh milestones\` lists the open milestones, and
ROADMAP.md says what each one's outcome is.

Designing is our work, not the reporter's or the user's (AGENTS.md,
"Designing is our job"): an issue that wants something docs/design.md doesn't
settle yet lacks a design, which a session makes, not an answer from the user.
Never ask how to build something; ask the reporter only to elaborate what they
want.

For each proposed issue, decide one of these:
- Ready it, \`hack/lib/github.sh status N ready\`, when it is clear (one piece
  of work, its goal fits docs/design.md or says which part of the design it
  changes), says when it is done (a checkable "Done when"), isn't a duplicate
  of an open issue, and names what it waits on with a "Depends on #M" line in
  its body, or waits on nothing.
- Ready it as design work, when what it wants is clear but docs/design.md
  doesn't settle how, or it is more than one piece of work: comment (first
  line \`<!-- relay-triage -->\`) that its session makes the design choices,
  writes them and why in a plan in docs/plans/ (and the change to
  docs/design.md), and files the steps as status/proposed issues, each with
  its own "Done when"; that is its "Done when". Then ready it.
- Fill the gap and ready it, when the only thing missing is something you
  can write yourself: a "Depends on #M" line it lacks (add it to the body with
  \`gh issue edit N --repo $C4_GITHUB_REPO --body-file FILE\`, keeping the
  rest of the body as it is). Then ready it and say what
  you wrote in a comment (first line \`<!-- relay-triage -->\`).
- Leave it proposed, with a comment (\`hack/lib/github.sh comment N FILE\`,
  its first line \`<!-- relay-triage -->\`) saying what is missing, when it
  falls short of that in a way you can't fill (what it wants is unclear: ask
  the reporter to elaborate), or when it waits on something that can still
  move it (an open issue: name it as #M, so the watch reads it again when #M
  closes). When its newest triage comment still says what is
  missing and nothing changed since, leave it without a new comment.
- When nothing else can move it and it needs something only the user can give
  (a tag, access, credentials, money), or you are unsure which way the factory
  should go, escalate it to the maintainer with
  \`hack/lib/github.sh status N needs-maintainer\` and a comment (first line
  \`<!-- relay-triage -->\`) saying what it needs; for a direction, the comment
  says the choice you would make and why, never a list of open questions. The
  maintainer decides it or takes it to the user; never set status/needs-human
  yourself.
- Close it as a duplicate, \`hack/lib/github.sh duplicate N M\`, when open
  issue #M covers the same work.

When you ready an issue, any of the three ways, and a user of the release
would notice what it changes (the CRDs, the operator, the UI, the chart,
templates, docs a user reads), give it the open milestone whose outcome it
serves, if one fits: \`hack/lib/github.sh milestone N "TITLE"\`, the title
exactly as \`milestones\` prints it. When none fits, it gets none; never
create, rename or close a milestone. Work on the relay, \`hack/\`, the skills
or the governance files (AGENTS.md, GOVERNANCE.md, ROADMAP.md, docs/dev/)
gets no milestone (ROADMAP.md).

Rules for this headless run:
- Never use the Fable model: not for subagents, not for anything. Always name
  the model when dispatching (sonnet or opus). Dispatch every subagent with
  run_in_background: false and never end your turn while one is running.
- Touch only the issues listed as proposed: never set status/in-progress,
  never take an issue out of status/needs-maintainer or status/needs-human
  (needs-maintainer is the maintainer's, needs-human stays the user's), never take an issue, edit a body only to
  add a "Depends on #M" line, never a title, set a milestone only on an issue
  you ready, and file no new issues.
- If an action is refused (permissions, auto mode), don't retry it: say so in
  your account.
- End your turn with a short account, the relay's log of this triage: readied
  (#N, one line why, and its milestone or none), left proposed (#N, what is missing), closed as
  duplicates (#N of #M), sent to the user (#N, the question).
EOF
}

# maintain_prompt LAST: the maintainer session's prompt; LAST is the time of
# the last sweep, or "never".
maintain_prompt() {
  cat <<EOF
You run headless, started by hack/relay-watch.sh (hack/relay.sh --maintain)
while the user sleeps; no person is watching and nobody can answer a question.
You are the project's maintainer for $C4_GITHUB_REPO, acting on the owner's
behalf: follow GOVERNANCE.md and the maintainer skill (load it first), and run
its sweep. $([ "$1" = never ] && echo "No sweep ran before this one: look at everything open, and the
last week of closed issues and merged pull requests." || echo "The last sweep was at $1; look at what changed since then, plus
what the report still carries.")

You decide, correct, answer, review and release; you don't build. You take no
issue, change no code or docs, commit nothing and open no pull request; this
worktree, relay-maintainer, is on origin/main for reading only. What needs
building becomes an issue the relay works (\`hack/lib/github.sh create TITLE
FILE status/proposed kind/<kind>\`). The owner's word wins: an owner comment
is binding and you never reverse it. Only what GOVERNANCE.md reserves to the
owner, or a direction you are unsure of, goes to the owner, each item with the
choice you would make and why, in the report's *Needs you*; never a list of
open questions. Agents escalate to you with status/needs-maintainer: decide
each of those first (\`hack/lib/github.sh escalated\` lists them), and move
it on. Only you set status/needs-human. End the sweep by writing the report and setting it with
\`hack/lib/github.sh report FILE\` (the pinned *Maintainer report* issue).

Rules for this headless run:
- Never use the Fable model: not for subagents, not for anything. Always name
  the model when dispatching (sonnet or opus). Dispatch every subagent with
  run_in_background: false and never end your turn while one is running.
- Never wait on the factory or a run longer than about 20 minutes in one
  stretch; note it in the report and go on.
- If an action is refused (permissions, auto mode), don't retry it: say so in
  the report and your account.
- Keep the context small: filter with jq, grep and tail; delegate broad reads
  to a subagent.
- End your turn with a short account, the relay's log of this sweep: what you
  decided, corrected, filed and released, and what went to the owner.
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

# drop_lease WHEN: release the cluster lease a session of this worker left
# behind, and log it.
drop_lease() {
  local out
  out=$(gh_lease_release)
  [ -z "$out" ] || log "$out $1"
}

# handoff_id N: the id of N's newest handoff comment, or nothing.
handoff_id() {
  local json
  json=$(gh_latest_handoff "$1") || return 1
  [ -z "$json" ] || jq -r .id <<<"$json"
}

# outcome N BEFORE: how the session left issue N: closed, needs-maintainer,
# needs-human, handed-off (a handoff comment newer than BEFORE) or nothing.
outcome() {
  local n=$1 before=$2 state latest
  state=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n" \
    --jq '"\(.state) \(.labels | map(.name) | join(","))"') || return 1
  case $state in
    closed*) echo closed; return 0 ;;
    *status/needs-maintainer*) echo needs-maintainer; return 0 ;;
    *status/needs-human*) echo needs-human; return 0 ;;
  esac
  latest=$(handoff_id "$n") || return 1
  if [ -n "$latest" ] && [ "$latest" != "$before" ]; then echo handed-off; else echo nothing; fi
}

# running_workers: how many workers hold their lock now.
running_workers() {
  local f n=0
  for f in "$DIR"/lock-*; do
    [ -e "$f" ] && ! flock -n "$f" true && n=$((n + 1))
  done
  echo "$n"
}

# cap N: on the device with the Kind cluster, memory is the limit, so the
# workers running and those `--workers N` would start stay at most
# RELAY_MAX_WORKERS (default 9, every worker: admit's memory gate and the
# heavy-job slots hold them instead). Exit 1, with the reason on stderr, when N
# would pass it.
cap() {
  local n=$1 k running new=0
  [ "${C4_CLUSTER:-}" = none ] && return 0
  running=$(running_workers)
  for k in $(seq 1 "$n"); do
    { [ -e "$DIR/lock-$k" ] && ! flock -n "$DIR/lock-$k" true; } || new=$((new + 1))
  done
  [ $((running + new)) -le "$MAX_WORKERS" ] && return 0
  echo "relay: this device runs the Kind cluster, and memory is its limit: at most $MAX_WORKERS worker(s) (RELAY_MAX_WORKERS raises it on purpose); $running running, --workers $n would start $new more" >&2
  return 1
}

# mem_gib: MemAvailable in whole GiB, from RELAY_MEMINFO (default /proc/meminfo).
mem_gib() {
  awk '/^MemAvailable:/ { print int($2 / 1048576); found = 1 } END { exit !found }' "$MEMINFO"
}

# paused: print the time .relay/paused-until holds and why, and exit 0, while
# the device is paused for a limit.
paused() {
  local until why
  [ -r "$DIR/paused-until" ] && read -r until why <"$DIR/paused-until" || return 1
  [[ $until =~ ^[0-9]+$ ]] && [ "$until" -gt "$(date +%s)" ] || return 1
  echo "$until $why"
}

# limit_message OUT: the line that says session OUT ended on a usage limit or a
# failed login, from its error result, else (no result) the tail of OUT.err.
limit_message() {
  local out=$1
  {
    if jq -e 'has("result")' "$out.json" >/dev/null 2>&1; then
      jq -r 'select(.is_error == true) | .result // empty' "$out.json"
    else
      tail -n 20 "$out.err" 2>/dev/null
    fi
  } | grep -m1 -iE "you've hit your|you've reached your|you're out of usage|out of usage credits|usage limit reached|not logged in|please run /login|invalid api key|oauth token (has )?(expired|revoked)|authentication_error|credit balance (is )?too low"
}

# reset_time MESSAGE: the epoch second a limit MESSAGE says it resets at
# ("resets 3am (Europe/Amsterdam)", "resets Oct 7, 3:30pm", "...|1759712400"),
# else now plus RELAY_LIMIT_WAIT.
reset_time() {
  local msg=$1 now when tz t epoch='\|([0-9]{9,})$' resets='resets (at )?([^·(]*[0-9][^·(]*)(\(([^)]+)\))?'
  now=$(date +%s)
  if [[ $msg =~ $epoch ]] && [ "${BASH_REMATCH[1]}" -gt "$now" ]; then
    echo "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ $msg =~ $resets ]]; then
    when=${BASH_REMATCH[2]//,/}
    tz=${BASH_REMATCH[4]:-}
    if t=$(if [ -n "$tz" ]; then TZ=$tz date -d "$when" +%s; else date -d "$when" +%s; fi 2>/dev/null); then
      # A time of day alone that has passed today means tomorrow.
      [ "$t" -le "$now" ] && t=$((t + 86400))
      echo "$t"
      return 0
    fi
  fi
  echo $((now + $(gh_seconds "$LIMIT_WAIT" RELAY_LIMIT_WAIT || echo 1800)))
}

# pause MESSAGE: pause the device until MESSAGE's reset time; a later pause
# another worker wrote stands.
pause() {
  local t old
  t=$(reset_time "$1")
  old=$(paused) && [ "${old%% *}" -ge "$t" ] && return 0
  echo "$t ${1//$'\n'/ }" >"$DIR/paused-until"
  log "paused the relay until $(date -d "@$t" -Is): $1"
}

# admit: wait until the device has headroom for one more session: no pause for
# a limit (.relay/paused-until), MemAvailable at least RELAY_MIN_MEM_GIB, and
# the last session start across workers at least RELAY_ADMIT_GAP seconds ago,
# so one session's build shows in memory before the next starts. Polls every
# RELAY_MEM_POLL seconds; exit 1 when the stop file appears or the wait for
# memory passes RELAY_MEM_WAIT (a pause doesn't count).
# admit first: the maintainer's admit, which goes first (#262): while it waits,
# .relay/admit-first holds its PID and no other admit takes the start slot, so
# it starts as soon as memory and the gap allow, however many workers wait.
admit() {
  local rc
  [ "${1:-}" = first ] || { admit_wait; return; }
  echo "$BASHPID" >"$DIR/admit-first"
  admit_wait first
  rc=$?
  [ "$(cat "$DIR/admit-first" 2>/dev/null)" = "$BASHPID" ] && rm -f "$DIR/admit-first"
  return "$rc"
}

# first_waits: exit 0 when another admit goes first: .relay/admit-first names
# a live process other than this one. A crashed maintainer's file holds no one.
first_waits() {
  local pid
  read -r pid 2>/dev/null <"$DIR/admit-first" || return 1
  [[ $pid =~ ^[0-9]+$ ]] && [ "$pid" != "$BASHPID" ] && kill -0 "$pid" 2>/dev/null
}

admit_wait() {
  local t0 mem last now waited= p said= first
  t0=$(date +%s)
  while :; do
    if p=$(paused); then
      [ "$said" = "$p" ] || log "paused until $(date -d "@${p%% *}" -Is) (${p#* }); picking nothing"
      said=$p
      [ -n "$DRY_RUN" ] && return 1
      [ -e "$STOP" ] && { log "stop file found while paused"; return 1; }
      sleep "$MEM_POLL"
      t0=$(date +%s)
      continue
    fi
    [ -n "$said" ] && { log "the pause is over"; said=; }
    mem=$(mem_gib) || { log "could not read MemAvailable from $MEMINFO; starting anyway"; return 0; }
    now=$(date +%s)
    exec 8>"$DIR/admit.lock"
    flock 8
    last=$(stat -c %Y "$DIR/admitted" 2>/dev/null || echo 0)
    first=
    [ "${1:-}" != first ] && first_waits && first=1
    if [ -z "$first" ] && [ "$mem" -ge "$MIN_MEM_GIB" ] && [ $((now - last)) -ge "$ADMIT_GAP" ]; then
      [ -n "$DRY_RUN" ] || touch "$DIR/admitted"
      exec 8>&-
      [ -n "$waited" ] && log "headroom: ${mem} GiB available after $(((now - t0) / 60))m; starting"
      return 0
    fi
    exec 8>&-
    if [ -z "$waited" ]; then
      if [ -n "$first" ]; then
        log "waiting: the maintainer waits for a start, and it goes first"
      elif [ "$mem" -lt "$MIN_MEM_GIB" ]; then
        log "waiting for headroom: ${mem} GiB available, a session needs ${MIN_MEM_GIB} (RELAY_MIN_MEM_GIB)"
      else
        log "waiting: a session started $((now - last))s ago, and sessions start at least ${ADMIT_GAP}s apart (RELAY_ADMIT_GAP)"
      fi
      waited=1
    fi
    [ -n "$DRY_RUN" ] && return 1
    [ -e "$STOP" ] && { log "stop file found while waiting for headroom"; return 1; }
    [ $((now - t0)) -ge "$MEM_WAIT" ] && { log "no headroom after $((MEM_WAIT / 60))m (${mem} GiB available)"; return 1; }
    sleep "$MEM_POLL"
  done
}

# seconds DURATION: DURATION (45, 45s, 45m, 2h, 1d) in seconds; exit 1 if it
# isn't a whole number with one of those units.
seconds() {
  [[ $1 =~ ^([0-9]+)([smhd]?)$ ]] || return 1
  case ${BASH_REMATCH[2]} in
    m) echo $((BASH_REMATCH[1] * 60)) ;;
    h) echo $((BASH_REMATCH[1] * 3600)) ;;
    d) echo $((BASH_REMATCH[1] * 86400)) ;;
    *) echo "${BASH_REMATCH[1]}" ;;
  esac
}

# transcript ID: the file claude writes session ID's transcript to, as it works
# in the worktree: under projects/, the worktree's path with each character
# other than a letter or digit as -. Its subagents write theirs in the
# directory of the same name without .jsonl.
transcript() {
  echo "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/${WT//[^a-zA-Z0-9]/-}/$1.jsonl"
}

# idle_watch WHAT PID FILE MARK: end session WHAT (process group PID) once its
# transcript FILE and its subagents' haven't changed for RELAY_IDLE: SIGTERM,
# then SIGKILL after RELAY_IDLE_KILL, with MARK touched so the relay counts it
# as failed. A transcript that never appears leaves the session to
# SESSION_TIMEOUT.
idle_watch() {
  local what=$1 pid=$2 f=$3 idle kill_after t0 last m now missing= i
  if ! idle=$(seconds "$IDLE") || ! kill_after=$(seconds "$IDLE_KILL"); then
    log "RELAY_IDLE ($IDLE) or RELAY_IDLE_KILL ($IDLE_KILL) isn't like 45m; $what runs until the $SESSION_TIMEOUT timeout"
    return 0
  fi
  t0=$(date +%s) last=$t0
  while sleep "$IDLE_POLL"; do
    kill -0 "$pid" 2>/dev/null || return 0
    now=$(date +%s)
    if ! m=$(stat -c %Y "$f" 2>/dev/null); then
      if [ -z "$missing" ] && [ $((now - t0)) -ge "$idle" ]; then
        log "$what wrote no transcript in $IDLE ($f); it runs until the $SESSION_TIMEOUT timeout"
        missing=1
      fi
      continue
    fi
    m=$(find "$f" "${f%.jsonl}" -printf '%T@\n' 2>/dev/null | sort -n | tail -n 1)
    [ "${m%.*}" -gt "$last" ] && last=${m%.*}
    [ $((now - last)) -ge "$idle" ] || continue
    : >"$4"
    log "$what stopped: its transcript was idle for $IDLE"
    kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null
    for ((i = 0; i < kill_after; i++)); do
      kill -0 "$pid" 2>/dev/null || return 0
      sleep 1
    done
    log "$what still ran $IDLE_KILL after SIGTERM; SIGKILL"
    kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null
    return 0
  done
}

# session WHAT OUT ID PROMPT: run headless session ID in the worktree with
# PROMPT, keep its output in OUT.json and OUT.err and its account in OUT.md,
# and log its end as WHAT.
session() {
  local what=$1 out=$2 id=$3 rc t0 mins msg pid watch
  t0=$(date +%s)
  # timeout leads its own process group, so the idle watch can end claude and
  # every process it started. Neither gets the worker's lock (fd 9): a process
  # the session leaves behind would hold it after the worker exits, and the
  # watch would see the worker as running.
  (cd "$WT" && C4_HEADLESS=1 exec timeout "$SESSION_TIMEOUT" claude -p --permission-mode auto --model opus \
    --session-id "$id" --output-format json "$4") >"$out.json" 2>"$out.err" 9>&- &
  pid=$!
  rm -f "$out.idle"
  idle_watch "$what" "$pid" "$(transcript "$id")" "$out.idle" 9>&- &
  watch=$!
  wait "$pid"
  rc=$?
  kill "$watch" 2>/dev/null
  wait "$watch" 2>/dev/null
  S_IDLE=
  [ -e "$out.idle" ] && S_IDLE=1
  mins=$((($(date +%s) - t0) / 60))
  if [ -s "$out.json" ] && jq -e .result "$out.json" >/dev/null 2>&1; then
    jq -r .result "$out.json" >"$out.md"
    log "$what end: exit $rc after ${mins}m, $(jq -r '"\(.num_turns) turns, error \(.is_error)"' "$out.json"); account in $out.md"
  else
    log "$what end: exit $rc after ${mins}m, no result; see $out.err"
  fi
  [ "$rc" = 124 ] && log "$what hit the $SESSION_TIMEOUT timeout"
  [ "$rc" = 143 ] && log "$what was killed (SIGTERM)"
  S_RC=$rc S_MINS=$mins
  if msg=$(limit_message "$out"); then
    pause "$msg"
    return 3
  fi
  return 0
}

# failed N ID OUT: count session ID (output OUT, with S_RC and S_MINS) as one
# more failed session in a row on issue N, in .relay/fails-N; at
# RELAY_MAX_FAILS, escalate N to the maintainer (status/needs-maintainer) with
# a comment naming them.
failed() {
  local n=$1 f="$DIR/fails-$1" count body url
  printf '%s\t%s\t%s\t%s\n' "$2" "$S_RC" "$S_MINS" "$3" >>"$f"
  count=$(wc -l <"$f")
  if [ "$count" -lt "$MAX_FAILS" ]; then
    log "#$n: $count failed session(s) in a row; status/needs-maintainer at $MAX_FAILS (RELAY_MAX_FAILS)"
    return 0
  fi
  body=$(fail_comment "$n" "$f")
  if url=$(gh_comment "$n" - <<<"$body" 2>>"$LOG") && gh_set_status "$n" needs-maintainer >/dev/null 2>>"$LOG"; then
    log "#$n failed $count sessions in a row; set it to status/needs-maintainer: $url"
    rm -f "$f"
  else
    log "#$n failed $count sessions in a row, but setting it to status/needs-maintainer failed; trying again after the next"
  fi
}

# fail_comment N FILE: the comment that hands issue N to the maintainer, naming each
# failed session in FILE.
fail_comment() {
  local id rc mins out
  echo "The relay on host \`$(hostname)\` stopped working #$1: $(wc -l <"$2") sessions in a row ended without a handoff and without a new commit on its branch. Each session is below; \`claude --resume <id>\` in the worker's worktree opens it. Maintainer: find what stops them, fix it or file the fix, then set the issue back to \`status/ready\`; send it to the owner only if that needs them."
  while IFS=$'\t' read -r id rc mins out; do
    echo
    echo "### Session \`$id\`: exit $rc after ${mins}m"
    echo
    echo "stderr (\`$out.err\`), last lines:"
    echo '```'
    tail -n 10 "$out.err" 2>/dev/null | cut -c1-300
    echo '```'
    echo
    echo "Account (\`$out.md\`), last lines:"
    echo '```'
    tail -n 10 "$out.md" 2>/dev/null | cut -c1-300
    echo '```'
  done <"$2"
}

# forget_fails N: a handoff, a commit or a finish ends issue N's failures.
forget_fails() { rm -f "$DIR/fails-$1"; }

# untriaged: tell the watch the triage session never ran: keep only the time
# in .relay/triaged, so it starts one again after WATCH_TRIAGE.
untriaged() { [ -n "$DRY_RUN" ] || date +%s >"$DIR/triaged"; }

# triage: one triage session on the open proposed issues (see the header).
triage() {
  local list id
  if [ -z "$DRY_RUN" ]; then
    worktree || { log "could not prepare the worktree $WT; no triage"; untriaged; return 1; }
    # What a dead session of this worker left behind isn't the triage's.
    drop_lease "when worker $K started a triage"
    if gh_claim_read; then
      log "$(gh_release "worker $K restarted" 2>&1 | tr '\n' ' ')"
    fi
  fi
  flush || { log "GitHub can't be reached; no triage"; untriaged; return 1; }
  # List before admit too, so a triage with nothing to do takes no start slot.
  list=$(gh_proposed 2>>"$LOG") || { log "could not list the proposed issues; no triage"; untriaged; return 1; }
  [ -n "$list" ] || { log "no proposed issues; no triage"; return 0; }
  admit || { log "no triage"; untriaged; return 1; }
  list=$(gh_proposed 2>>"$LOG") || { log "could not list the proposed issues; no triage"; untriaged; return 1; }
  [ -n "$list" ] || { log "no proposed issues; no triage"; return 0; }
  list="#$(echo $list | sed 's/ / #/g')"
  id=$(cat /proc/sys/kernel/random/uuid)
  if [ -n "$DRY_RUN" ]; then
    log "would switch $WT to origin/main and start triage session $id on $list, with this prompt:"
    echo "-----"
    triage_prompt "$list"
    echo "-----"
    return 0
  fi
  git -C "$WT" switch -q --detach origin/main 2>>"$LOG" ||
    { log "could not switch $WT to origin/main; no triage"; untriaged; return 1; }
  log "triage start: id $id, proposed $list, HEAD $(git -C "$WT" rev-parse --short HEAD)"
  session triage "$DIR/triage-$K-$(date +%Y%m%d-%H%M%S)" "$id" "$(triage_prompt "$list")"
}

# maintained START ACTIVITY: tell the watch, and the next sweep's prompt, that
# a sweep that started at START (epoch seconds) ran to its end and saw
# ACTIVITY. A sweep that didn't run to its end leaves the file as it is, so the
# next one still looks back to the last full sweep.
maintained() { [ -n "$DRY_RUN" ] || echo "$1 $2" >"$DIR/maintained"; }

# maintain: one maintainer session (see the header).
maintain() {
  local id last t act rc out start
  if [ -z "$DRY_RUN" ]; then
    worktree || { log "could not prepare the worktree $WT; no sweep"; return 1; }
  fi
  admit first || { log "no sweep"; return 1; }
  last=never t=
  [ -r "$DIR/maintained" ] && read -r t _ <"$DIR/maintained"
  [[ $t =~ ^[0-9]+$ ]] && last=$(date -d "@$t" -Is)
  id=$(cat /proc/sys/kernel/random/uuid)
  if [ -n "$DRY_RUN" ]; then
    log "would switch $WT to origin/main and start maintainer session $id, with this prompt:"
    echo "-----"
    maintain_prompt "$last"
    echo "-----"
    return 0
  fi
  git -C "$WT" switch -q --detach origin/main 2>>"$LOG" ||
    { log "could not switch $WT to origin/main; no sweep"; return 1; }
  log "maintainer start: id $id, last sweep $last, HEAD $(git -C "$WT" rev-parse --short HEAD)"
  out="$DIR/maintain-$(date +%Y%m%d-%H%M%S)"
  start=$(date +%s)
  session maintainer "$out" "$id" "$(maintain_prompt "$last")"
  rc=$?
  # A limit, a crash, a timeout or an idle kill cut the sweep short: not recorded.
  if [ "$rc" != 0 ] || [ "$S_RC" != 0 ] || [ -n "$S_IDLE" ] || [ ! -s "$out.md" ]; then
    log "the sweep didn't run to its end; not recorded, so the watch starts one again"
    return 1
  fi
  act=$(gh_activity 2>>"$LOG") || { log "could not read the activity after the sweep; not recorded"; return 1; }
  maintained "$start" "$act"
}

# work: the worker's sessions, one after another (see the header).
work() {
  local i id rc try claim name branch before head out limited result reason released
  for i in $(seq 1 "$MAX_SESSIONS"); do
    [ -e "$STOP" ] && { log "stop file found; stopping"; break; }
    if [ -z "$DRY_RUN" ]; then
      update $((MAX_SESSIONS - i + 1))
      worktree || { log "could not prepare the worktree $WT; stopping"; break; }
      [ "$i" = 1 ] && drop_lease "when worker $K restarted"
      if [ "$i" = 1 ] && gh_claim_read; then
        log "$(gh_release "worker $K restarted" 2>&1 | tr '\n' ' ')"
      fi
    fi
    flush || { log "GitHub can't be reached; stopping"; break; }

    # Ask before admit too, so a worker with nothing to do stops without
    # taking the start slot from a triage or a worker that has an issue; ask
    # again after it, as the issue can be taken during the wait. While paused,
    # wait in admit instead.
    if ! paused >/dev/null; then
      pick
      rc=$?
      [ "$rc" = 1 ] && { log "no issue ready; stopping"; break; }
      [ "$rc" = 0 ] || { log "could not ask GitHub for the next issue; stopping"; break; }
    fi
    admit || { log "stopping"; break; }
    pick
    rc=$?
    [ "$rc" = 1 ] && { log "no issue ready; stopping"; break; }
    [ "$rc" = 0 ] || { log "could not ask GitHub for the next issue; stopping"; break; }

    id=$(cat /proc/sys/kernel/random/uuid)
    if [ -n "$DRY_RUN" ]; then
      log "would claim #$ISSUE ($HOW) for session $id: $TITLE, $URL"
      log "would switch $WT to the issue's branch and start session $i there: claude -p --permission-mode auto --model opus --session-id $id, with this prompt:"
      echo "-----"
      prompt "$ISSUE" "$TITLE" "$URL" "$HOW" "<name>" "<branch>"
      echo "-----"
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
    head=$(git -C "$WT" rev-parse -q --verify "refs/heads/$branch")
    log "session $i start: id $id, $name on #$ISSUE ($HOW) $TITLE, branch $branch, HEAD ${head:0:7}"
    out="$DIR/session-$K-$i-$(date +%Y%m%d-%H%M%S)"
    session "session $i" "$out" "$id" "$(prompt "$ISSUE" "$TITLE" "$URL" "$HOW" "$name" "$branch")"
    limited=$?

    drop_lease "that session $i left behind"
    flush || { log "GitHub can't be reached; stopping"; break; }
    result=$(outcome "$ISSUE" "$before") || result=unknown
    if gh_claim_read; then
      case $result in
        closed) reason=finished ;;
        handed-off) reason="handed off" ;;
        *) reason="session ended without a handoff$([ "$limited" != 3 ] || echo " (a limit)")" ;;
      esac
      if released=$(gh_release "$reason" 2>&1); then
        log "${released//$'\n'/; }"
      else
        log "could not release the claim on #$ISSUE; it expires after $C4_LEASE_TTL: ${released//$'\n'/; }"
      fi
    fi
    case $result in
      closed | needs-maintainer | needs-human | handed-off) forget_fails "$ISSUE" ;;
    esac
    case $result in
      closed) log "session $i closed #$ISSUE" ;;
      needs-maintainer) log "session $i escalated #$ISSUE to the maintainer (status/needs-maintainer)" ;;
      needs-human) log "session $i left #$ISSUE waiting for the user (status/needs-human)" ;;
      handed-off) log "session $i handed off #$ISSUE" ;;
      unknown) log "could not read #$ISSUE after session $i; stopping"; break ;;
      *)
        # A limit pauses the device and isn't the issue's failure: wait it out in admit.
        if [ "$limited" = 3 ]; then
          log "session $i hit a limit on #$ISSUE; not counted against it"
          continue
        fi
        if [ -n "$S_IDLE" ]; then
          log "session $i was stopped idle on #$ISSUE; stopping (claude --resume $id in $WT to see why)"
          failed "$ISSUE" "$id" "$out"
        elif [ "$(git -C "$WT" rev-parse -q --verify "refs/heads/$branch")" != "$head" ]; then
          forget_fails "$ISSUE"
          log "session $i left no handoff on #$ISSUE but committed on $branch; stopping (claude --resume $id in $WT to see why)"
        else
          log "session $i left no handoff on #$ISSUE and no commit; stopping (claude --resume $id in $WT to see why)"
          failed "$ISSUE" "$id" "$out"
        fi
        break
        ;;
    esac
  done
}

# Sourced (hack/relay_test.sh): the functions only.
[ "${BASH_SOURCE[0]}" = "$0" ] || return 0

mkdir -p "$DIR"
[ -e "$DIR/.gitignore" ] || echo '*' >"$DIR/.gitignore"
[ "${C4_RELAY_CHILD:-}" = 1 ] || [ -n "$TRIAGE" ] || [ -n "$MAINTAIN" ] || cap "$WORKERS" || exit 1

# --maintain from a terminal or the watch: start the maintainer, detached,
# unless it runs. It doesn't count against RELAY_MAX_WORKERS.
if [ -n "$MAINTAIN" ] && [ -z "$DRY_RUN" ] && [ "${C4_RELAY_CHILD:-}" != 1 ]; then
  [ -e "$STOP" ] && { echo "the stop file is there; no sweep"; exit 0; }
  flock -n "$LOCK" true || { echo "the maintainer is already running: tail -f $LOG"; exit 0; }
  gh_token || exit 1
  C4_RELAY_CHILD=1 C4_RELAY_MAINTAIN=1 setsid nohup bash "$0" >>"$DIR/relay-$K.out" 2>&1 </dev/null &
  echo "maintainer started (pid $!): tail -f $LOG"
  exit 0
fi

# --triage from a terminal or the watch: start one triage session, detached,
# in the first worker that isn't running.
if [ -n "$TRIAGE" ] && [ -z "$DRY_RUN" ] && [ "${C4_RELAY_CHILD:-}" != 1 ]; then
  [ -e "$STOP" ] && { echo "the stop file is there; no triage"; exit 0; }
  # On the device with the Kind cluster, the triage counts against RELAY_MAX_WORKERS too.
  if [ "${C4_CLUSTER:-}" != none ] && [ "$(running_workers)" -ge "$MAX_WORKERS" ]; then
    echo "relay: $MAX_WORKERS worker(s) run, RELAY_MAX_WORKERS; no triage" >&2
    exit 1
  fi
  gh_token || exit 1
  for k in $(seq 1 "$WORKERS"); do
    flock -n "$DIR/lock-$k" true || continue
    C4_RELAY_CHILD=1 C4_RELAY_WORKER=$k C4_RELAY_TRIAGE=1 setsid nohup bash "$0" >>"$DIR/relay-$k.out" 2>&1 </dev/null &
    echo "triage started in worker $k (pid $!): tail -f $DIR/relay-$k.log"
    exit 0
  done
  echo "relay: workers 1-$WORKERS all run; no triage" >&2
  exit 1
fi

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
    exec 9>"$LOCK"
    flock -n 9 || { log "another worker $K holds the lock; exiting"; [ -z "$TRIAGE" ] || untriaged; exit 1; }
  fi
fi
CODE_SUM=$(code_sum)
gh_token 2>/dev/null || { log "no GitHub token; stopping"; exit 1; }
log "worker $K started: pid $$${DRY_RUN:+, dry run}${C4_CLUSTER:+, C4_CLUSTER=$C4_CLUSTER}, worktree $WT, HEAD $(git rev-parse --short HEAD), at most $MAX_SESSIONS sessions of $SESSION_TIMEOUT, issues on $C4_GITHUB_REPO"
if [ -n "$MAINTAIN" ]; then
  maintain
  log "the maintainer finished its sweep"
  exit 0
fi
if [ -n "$TRIAGE" ]; then
  triage
  log "worker $K finished the triage"
  exit 0
fi
# Workers started together would all race for the same first issue.
[ -n "$DRY_RUN" ] || [ "${C4_RELAY_RESTART:-}" = 1 ] || sleep $(((K - 1) * 20))

work
log "worker $K finished"
