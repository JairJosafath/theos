#!/usr/bin/env bash
# Relay: runs fresh headless Claude sessions on the factory's own GitHub issues
# (JairJosafath/c4factory), so work carries on overnight without a person
# starting each session. Each session works one issue and hands off through a
# comment on it (the handoff skill); hack/lib/github.sh has the GitHub helpers.
# Product work stays in Gitea, where the Crews work it.
#
# It runs N workers side by side (docs/plans/done/parallel-agents.md). Worker k runs
# origin/main's code (see below) and starts its sessions, one after another, in its own git
# worktree .claude/worktrees/relay-k, switched to each issue's branch; the
# worktree's .relay/ holds its claim. Each round picks an issue
# (hack/lib/github.sh pick: a ready one this worker's last session filed with
# create --next, which reserves it for this worktree, else by priority/N, 1
# first and no label counting as 5; within a number, an in-progress one nobody
# holds, else the oldest ready one) and claims it (take); when another agent claims it
# first, it picks again, up to 5 times. Claims keep the workers and
# hand-started sessions off each other's issues. A session that ends still
# holding its claim gets it released. With nothing ready, a worker stops,
# without waiting in admit: it asks (pick is read-only) before admit as well
# as after, so an idle worker takes no session-start slot.
#
# Triage: hack/relay-watch.sh starts one triage session (--triage) on the
# open status/proposed issues, which relay sessions file their follow-ups as,
# whether or not issues are ready (#1191). It runs in its own worktree,
# .claude/worktrees/relay-triage, on origin/main for reading only, under its
# own lock (.relay/triage.lock), as the maintainer does: it needs no free
# worker and doesn't count against RELAY_MAX_WORKERS, and as it runs no make
# it takes no heavy-job slot. It goes through admit like any session. It runs
# one session that takes no issue and changes no code: the maintainer's triage
# (the triage skill, .claude/skills/triage/SKILL.md, has its rules). It readies
# each sound proposed issue, fills a gap it can write itself (a Depends on #M
# line) and readies the issue, comments on the others what they lack, readies
# one the design doesn't settle as design work (designing is ours, never a
# question for the reporter), escalates one that nothing it can write moves on
# (it needs a decision: something only the owner can give, or a direction it
# is unsure of, with the choice it would make) to the maintainer
# (status/needs-maintainer), closes duplicates, and ends with an account of
# what it did. It never takes an issue out of status/needs-maintainer or
# status/needs-human.
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
# GOVERNANCE.md reserves to the owner (docs/plans/done/maintainer.md). It runs
# once per project, not once per device (#526): before its session it takes the
# maintainer lease on the report issue (gh_maint_take in hack/lib/github.sh),
# keeps it fresh while the session runs and releases it at the end; while
# another device holds a live lease it starts nothing and logs the holder, and
# a lease past C4_LEASE_TTL is taken over. When it
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
# session to use one, and every issue is any worker's.
#
# Workers run origin/main's code, whatever branch this checkout has (#1126):
# before each pick a worker runs hack/pull.sh on this checkout, moves the
# worktree .claude/worktrees/relay-code to origin/main, and restarts itself on
# relay-code's copy when it runs from elsewhere or when that changed
# hack/relay.sh, hack/lib/github.sh, hack/lib/session-env.sh or
# hack/lib/relay-code.sh, so a relay that runs for hours never picks by stale
# rules, nor by a branch's (hack/lib/relay-code.sh). Its state (.relay/, the
# workers' worktrees) stays in this checkout. RELAY_PULL=0 turns this off and
# runs the code where it was started, to try relay changes from a branch.
#
# Started from a Claude Code or VS Code session or not, the relay and its
# sessions run as from a plain project shell: hack/lib/session-env.sh drops that
# session's variables (CLAUDECODE, CLAUDE_CODE_SESSION_ID, VSCODE_*, ...).
#
# Each session gets TMPDIR=~/.cache/c4factory/tmp/<its worktree> (#1269,
# #1307): on disk, not the device's /tmp, a small tmpfs every session shares,
# which one session's scratch, Go's build dirs and envtest's filled; and outside
# any git checkout, so a test that runs git in a mktemp -d directory finds no
# repository above it. The relay empties it before the session starts and
# removes it when the session ends.
#
#   hack/relay.sh                  start one worker
#   hack/relay.sh --workers N      start N workers (1-99); workers already running are left alone
#   hack/relay.sh --triage         start one triage session, unless one runs
#   hack/relay.sh --maintain       start one maintainer session, unless one runs
#
# On the device with the Kind cluster (C4_CLUSTER not none), memory is the
# limit: at most RELAY_MAX_WORKERS workers run (default 9, so the memory gate,
# not a count, holds them). On every device a worker waits before each pick
# until MemAvailable is at least RELAY_MIN_MEM_GIB (default 2) and the last
# session start is RELAY_ADMIT_GAP seconds old (default 120), and stops after
# waiting RELAY_MEM_WAIT seconds (default 3600). It also waits while a watched
# filesystem has less than RELAY_MIN_FREE_GB free (default 30): /, the
# checkouts' and, under WSL, the Windows drives holding the distros' .vhdx
# files, whose free space df / never shows (#1344); RELAY_DISK_PATHS overrides
# the list. Heavy jobs (make check, test,
# lint, deploy, ...) queue device-wide for HEAVY_JOBS slots (hack/heavy-slot.sh).
# A worker whose worktree has no git committer identity (#1289) starts no
# session, logs why, stops, and puts a line under the Maintainer report's
# Needs you naming the host and worktree, until the identity is back.
# A session that ends on a usage limit or a failed login, or exits non-zero
# before any API call (#1421), pauses the device: it writes
# .relay/paused-until (the reset time the message gives, else now plus
# RELAY_LIMIT_WAIT, default 30m), and until then every worker waits in admit
# without picking or claiming, and the watch starts nothing. A session that
# ends without a handoff and without a new commit on the issue's branch (or on
# <branch>-2 or <branch>-<name>, local or pushed) counts as failed: a strike, a
# comment on the issue. At RELAY_MAX_FAILS (default 3) strikes, from any device
# and none older than RELAY_STRIKE_TTL (default 24h), the relay escalates the
# issue to the maintainer (status/needs-maintainer) with one comment linking
# each. A session that leaves its issue in status/needs-maintainer,
# status/needs-human or status/waiting has finished, as one that closes it or
# hands it off; one that sets it back to status/ready or status/proposed isn't
# counted. A handoff or a new commit, on any device, resets the count; a paused
# session doesn't count.
# A session whose transcript (under $CLAUDE_CONFIG_DIR/projects/) hasn't
# changed for RELAY_IDLE (default 45m) is stuck: the relay ends it (SIGTERM,
# then SIGKILL after RELAY_IDLE_KILL) and counts it as failed, commit or not.
# Every session runs on RELAY_MODEL (default fable), and its prompt sends its
# subagents there. Before each session the worker runs the yolo round,
# hack/lib/github.sh yolo merge (#1534): while yolo mode is on, every open
# non-draft pull request to main merges at once, and what that says is logged.
#   RELAY_DRY_RUN=1 hack/relay.sh  print the issue and prompt the next session would get; change nothing
#   touch .relay/stop              stop every worker after its current session
#
# Log: .relay/relay-<k>.log, one line per event. Each session's closing account is
# in .relay/session-<k>-<n>-<time>.md; open a whole session with `claude --resume <id>`
# from the worker's worktree.
set -uo pipefail

CODE=$(cd "$(dirname "$0")/.." && pwd)
REPO=${C4_RELAY_REPO:-$CODE}
unset C4_RELAY_REPO
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
MIN_FREE_GB=${RELAY_MIN_FREE_GB:-30} # GB free each watched filesystem needs before a session starts (#1344)
DISK_PATHS=${RELAY_DISK_PATHS:-}     # the watched paths, space-separated; empty: disk_paths finds them
OSRELEASE=${RELAY_OSRELEASE:-/proc/sys/kernel/osrelease}
LIMIT_WAIT=${RELAY_LIMIT_WAIT:-30m} # the pause after a limit whose message names no reset time
MAX_FAILS=${RELAY_MAX_FAILS:-3}     # strikes (failed sessions) before an issue goes to the maintainer
STRIKE_TTL=${RELAY_STRIKE_TTL:-24h} # a strike older than this doesn't count
IDLE=${RELAY_IDLE:-45m}             # a session whose transcript doesn't change this long is ended
IDLE_KILL=${RELAY_IDLE_KILL:-1m}    # from its SIGTERM to its SIGKILL
IDLE_POLL=${RELAY_IDLE_POLL:-60}    # seconds between looks at the transcript
SCRATCH=${RELAY_SCRATCH:-$HOME/.cache/c4factory/tmp} # the sessions' TMPDIRs, outside any checkout (#1307); not XDG_CACHE_HOME, which ./dev puts in .dev/ (#1326)
RELAY_MODEL=${RELAY_MODEL:-fable} # the model every session runs on (claude -p --model), and names for its subagents (#1534)

K=${C4_RELAY_WORKER:-1}
# The maintainer and the triage are no numbered workers: each has its own
# worktree, log and lock.
[ -n "$TRIAGE" ] && K=triage
[ -n "$MAINTAIN" ] && K=maintainer
WT="$REPO/.claude/worktrees/relay-$K"
LOG="$DIR/relay-$K.log"
LOCK="$DIR/lock-$K"
[ -n "$TRIAGE" ] && LOCK="$DIR/triage.lock"
[ -n "$MAINTAIN" ] && LOCK="$DIR/maintain.lock"

# shellcheck source=hack/lib/github.sh
. "$CODE/hack/lib/github.sh"
# shellcheck source=hack/lib/session-env.sh
. "$CODE/hack/lib/session-env.sh"
# shellcheck source=hack/lib/relay-code.sh
. "$CODE/hack/lib/relay-code.sh"
# The worker's claim and unposted handoffs live in its worktree, where its
# sessions' hack/lib/github.sh and heartbeat hook look for them.
GH_PENDING_DIR="$WT/.relay"
GH_CLAIM_FILE="$GH_PENDING_DIR/claim"

# prompt N TITLE URL HOW NAME BRANCH: the headless session's prompt for issue
# N, worked by agent NAME on BRANCH.
prompt() {
  local n=$1 title=$2 url=$3 how=$4 name=$5 branch=$6 start
  case $how in
    in-progress) start="A session worked on it before you: continue from its newest handoff comment." ;;
    next) start="The session before you in this worktree found it and filed it with create --next: the issue body is the goal." ;;
    *) start="No session has worked on it yet: the issue body is the goal." ;;
  esac
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
- Run subagents on the $RELAY_MODEL model: name it when dispatching.
- A headless session ends when you end your turn, and nothing wakes it: a
  notification from a subagent or a background command never arrives. Dispatch
  every subagent with run_in_background: false and never end your turn while
  one is running. Never end your turn while a background command you started
  (\`land\`, \`make check\`, a CI rerun you follow) still runs: follow it, or
  poll it with a short command (\`tail\` of its log, never a bare \`sleep\`),
  until it ends, and only then write the account.
- Keep the context small: at about 80k tokens, finish the small step in
  flight, commit, push and hand off, mid-task if need be; never go on past
  120k. The next session continues #$n from your handoff, so say exactly
  where it stands. The context guard hook (hack/context-guard.sh) tells you
  when.
- Never wait on the factory (a Crew, a Task, an agent) longer than about 20
  minutes in one stretch; if it still isn't done, note it in the handoff and
  move on to other work or hand off.
- If an action is refused (permissions, auto mode), don't retry it and never
  do it through another command: say which command, once, in the pull request
  body and the handoff (the handoff skill, "Refused"), and carry on; when it
  blocks landing, escalate to the maintainer.
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
    it with a merge commit (\`hack/lib/github.sh land PR\`: it merges main in and runs make check, then
    takes the merge turn and merges, checking again only when main moved; its check is the check, so
    while you work run the targeted tests and run make check by hand only when land failed and you
    need the whole output; run land in the background and follow it, and never
    end your turn while it still runs; over 400
    changed lines it refuses until a review pass is posted: the handoff skill, \"Review before landing\"), and if the issue is still
    open, \`hack/lib/github.sh close $n FILE\`, FILE saying what landed (pull
    request, commit SHAs). The relay then picks the next issue. A pull
    request that only writes a plan and files its steps says \`Refs #$n\`
    instead, with no close, fix or resolve word next to #$n even in prose, and ends with a handoff, then \`hack/lib/github.sh status $n waiting\`
    (the handoff skill, "Plan only"); #$n closes itself when its last step does:
    never close it by hand.
  - Stuck on a decision: \`hack/lib/github.sh status $n needs-maintainer\` and
    a comment saying why and what you need. The relay then picks the next
    issue, and the maintainer decides it or takes it to the owner. How to build
    it is yours to decide (AGENTS.md, "Designing is our job"): this is for
    what only the owner can give (access, credentials, money), or a
    direction you are unsure of, asked with the choice you would make and
    why. Never set status/needs-human: only the maintainer does.
- Never lease, create or deploy to a Kind cluster, and never run make e2e,
  kind or factory-ops: make check (Go tests, validate-yaml, envtest) is the
  gate, and Kind is a tier only a human runs. When only a real node would show
  something, land the code with the tests the lower tiers can give and say in
  the pull request what a human run of Kind would check.
- Work you find outside #$n: under about 30 lines, in a file your pull
  request already changes and inside #$n's scope, fix it in the pull request
  and name it under \`Also fixes:\` in its body. Else, when it is decided (a
  bug whose failing test you can name, a chore or doc whose Done-when you can
  state), file it with \`hack/lib/github.sh create --next TITLE FILE
  kind/<bug|chore|docs>\`: it is ready and this worktree's next session starts
  on it. Anything that needs a decision: \`hack/lib/github.sh create TITLE
  FILE status/proposed kind/<kind>\`. Don't start either.
- End your turn with a short account of what you did and what is next; it is
  the relay's log of your session.
EOF
}

# triage_prompt ISSUES: the triage session's prompt, for the proposed ISSUES
# (#1 #2 ...). The rules are the triage skill's.
triage_prompt() {
  cat <<EOF
You run headless, started by hack/relay-watch.sh (hack/relay.sh --triage)
while the user sleeps; no person is watching and nobody can answer a question.
You are the maintainer's triage session for $C4_GITHUB_REPO, while the relay
works the ready issues or waits for one; these wait in status/proposed: $1.
Load the \`triage\` skill and follow it: ready each one that is sound, say on
the others what they lack, and send what needs a decision to the maintainer.
You take no issue, change no code, commit nothing and open no pull request;
this worktree, relay-$K, is on origin/main for reading only.

Rules for this headless run:
- Run subagents on the $RELAY_MODEL model: name it when dispatching. Dispatch
  every subagent with run_in_background: false and never end your turn while
  one is running. A headless session ends when you end your turn, and nothing
  wakes it: never end your turn while a background command you started still
  runs; follow it, or poll it with a short command, until it ends, and only
  then write the account.
- If an action is refused (permissions, auto mode), don't retry it: say so in
  your account.
- Keep the context small: past about 80k tokens, finish the issue in hand and
  end your turn; the next triage takes the rest. Never go on past 120k.
- End your turn with a short account, the relay's log of this triage: one
  line per issue, readied (why, and its milestone or none), left proposed
  (what is missing), closed as a duplicate (of #M) or sent to the maintainer
  (what it needs).
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
- Run subagents on the $RELAY_MODEL model: name it when dispatching. Dispatch
  every subagent with run_in_background: false and never end your turn while
  one is running. A headless session ends when you end your turn, and nothing
  wakes it: never end your turn while a background command you started still
  runs; follow it, or poll it with a short command, until it ends, and only
  then write the account.
- Never wait on the factory or a run longer than about 20 minutes in one
  stretch; note it in the report and go on.
- If an action is refused (permissions, auto mode), don't retry it: say so in
  the report and your account.
- Keep the context small: filter with jq, grep and tail; delegate broad reads
  to a subagent. Past about 80k tokens, finish the item in hand, write the
  report and end your turn; the next sweep takes the rest. Never go on past
  120k.
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

# pick: set ISSUE, TITLE, URL and HOW (next, in-progress or ready, at every
# priority) to the issue to work next; next is one this worker's last session
# reserved (create --next, .relay/next in its worktree).
# Exit 1: nothing to pick; exit 2: GitHub could not be asked.
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
  share_dev
  mkdir -p "$GH_PENDING_DIR"
  [ -e "$GH_PENDING_DIR/.gitignore" ] || echo '*' >"$GH_PENDING_DIR/.gitignore"
  if [ -n "$(git -C "$WT" status --porcelain)" ]; then
    git -C "$WT" stash push -q -u -m "relay-$K: left uncommitted on $(git -C "$WT" rev-parse --abbrev-ref HEAD), $(date -Is)" 2>>"$LOG" ||
      return 1
    log "stashed uncommitted changes left in $WT (git -C $WT stash list)"
  fi
}

# share_dev: link the worktree's .dev/ to the main checkout's, as ./dev setup
# links a worktree's. A real .dev/ there holds caches that make check made
# before hack/dev/env.sh pointed worktrees at the main checkout's (#1132):
# gigabytes no one reads, so it goes (#1246). The worker's lock is held and no
# session runs in the worktree.
share_dev() {
  local dev=$WT/.dev main=$REPO/.dev freed
  [ -L "$dev" ] && return 0
  [ -d "$main" ] || return 0
  if [ -e "$dev" ]; then
    freed=$(du -sh "$dev" 2>/dev/null | cut -f1)
    scratch_clear "$dev"
    [ -e "$dev" ] && { log "could not replace $dev with a link to $main"; return 0; }
    log "freed ${freed:-?} in $dev, a .dev/ of its own; linked it to $main (#1246)"
  fi
  ln -s "$main" "$dev" 2>>"$LOG" || log "could not link $dev to $main"
}

# can_commit: whether a session in the worktree could commit (#1289): git var
# GIT_COMMITTER_IDENT there, in the environment the session gets, sees the
# global, XDG and repository configs alike. When it can't, log git's reason
# and put a line under the Maintainer report's Needs you, once, naming the
# host and the worktree; when it can again, take the line away. The mark file
# remembers the note, so a worktree that can commit asks GitHub nothing.
can_commit() {
  local err key="ident-$(hostname)-${WT##*/}" mark=$DIR/no-ident-$K
  if err=$(git -C "$WT" var GIT_COMMITTER_IDENT 2>&1 >/dev/null); then
    if [ -e "$mark" ] && gh_report_note "$key" >/dev/null 2>>"$LOG"; then
      rm -f "$mark"
      log "git has a committer identity in $WT again; took the report note away"
    fi
    return 0
  fi
  log "git has no committer identity in $WT, so no session could commit: $(grep fatal <<<"$err" || tail -n1 <<<"$err")"
  if [ ! -e "$mark" ]; then
    if gh_report_note "$key" "\`$(hostname)\`: git has no committer identity in \`$WT\`; relay worker $K starts no session there" \
      "Set git's user.name and user.email on \`$(hostname)\` (\`git -C $WT var GIT_COMMITTER_IDENT\` must pass): relay worker $K starts no session in \`$WT\` until then" \
      >/dev/null 2>>"$LOG"; then
      : >"$mark"
    else
      log "could not put the missing identity in the Maintainer report"
    fi
  fi
  return 1
}

# code_sum: a checksum of the relay's own code, to see whether a pull changed it.
code_sum() {
  cat "$CODE/hack/relay.sh" "$CODE/hack/lib/github.sh" "$CODE/hack/lib/session-env.sh" "$CODE/hack/lib/relay-code.sh" 2>/dev/null | cksum
}

# update LEFT: run origin/main's hack/pull.sh on this checkout, move
# relay-code to origin/main, and restart the worker on relay-code's copy, with
# LEFT sessions, when it runs from elsewhere or that changed the relay's code.
update() {
  local left=$1 out
  [ "$PULL" = 0 ] && return 0
  out=$(C4_PULL_REPO=$REPO "$CODE/hack/pull.sh" 2>&1)
  [ -n "$out" ] && log "${out//$'\n'/; }"
  relay_code_stale || return 0
  [ -e "$STOP" ] && { log "stop file found; stopping"; exit 0; }
  if [ "$CODE" = "$(relay_code)" ]; then
    log "the relay's code changed ($(relay_code) at $(git -C "$(relay_code)" rev-parse --short HEAD)); restarting with $left sessions left"
  else
    log "moving to origin/main's code ($(relay_code) at $(git -C "$(relay_code)" rev-parse --short HEAD)) from $CODE; restarting with $left sessions left"
  fi
  # The new worker keeps this one's lock on fd 9.
  exec env MAX_SESSIONS="$left" C4_RELAY_RESTART=1 C4_RELAY_REPO="$REPO" bash "$(relay_code)/hack/relay.sh"
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

# handoff_id N: the id of N's newest handoff comment, or nothing.
handoff_id() {
  local json
  json=$(gh_latest_handoff "$1") || return 1
  [ -z "$json" ] || jq -r .id <<<"$json"
}

# outcome N BEFORE: how the session left issue N: closed, needs-maintainer,
# needs-human, waiting (a plan's parent, its steps filed), released (set back
# to ready or proposed), handed-off (a handoff comment newer than BEFORE) or
# nothing.
outcome() {
  local n=$1 before=$2 state latest
  state=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n" \
    --jq '"\(.state) \(.labels | map(.name) | join(","))"') || return 1
  case $state in
    closed*) echo closed; return 0 ;;
    *status/needs-maintainer*) echo needs-maintainer; return 0 ;;
    *status/needs-human*) echo needs-human; return 0 ;;
    *status/waiting*) echo waiting; return 0 ;;
  esac
  latest=$(handoff_id "$n") || return 1
  if [ -n "$latest" ] && [ "$latest" != "$before" ]; then
    echo handed-off
  else
    case $state in
      *status/ready* | *status/proposed*) echo released ;;
      *) echo nothing ;;
    esac
  fi
}

# branch_refs BRANCH: the issue's branch and its renamed copies (<branch>-2,
# <branch>-<name> after a takeover), local and on origin, each with its commit.
# A session that commits or pushes on any of them changes it.
branch_refs() {
  git -C "$WT" for-each-ref --format='%(refname) %(objectname)' \
    "refs/heads/$1" "refs/heads/$1-*" "refs/remotes/origin/$1" "refs/remotes/origin/$1-*"
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

# vhdx_dirs: under WSL, the Linux paths of the directories holding each
# distro's virtual disk (Docker Desktop's included): their BasePath under
# HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss, from reg.exe, mapped
# with wslpath. /mnt/c when neither gives one. Nothing outside WSL.
vhdx_dirs() {
  local base out=
  grep -qi microsoft "$OSRELEASE" 2>/dev/null || return 0
  while read -r base; do
    base=$(wslpath -u "${base#\\\\?\\}" 2>/dev/null) && [ -n "$base" ] && out+="$base"$'\n'
  done < <(timeout 20 reg.exe query 'HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss' /s /v BasePath 2>/dev/null |
    tr -d '\r' | sed -n 's/^ *BasePath  *REG_[A-Z_]*  *//p')
  if [ -n "$out" ]; then printf '%s' "$out"; else echo /mnt/c; fi
}

# disk_paths: the watched paths, one per line: RELAY_DISK_PATHS, else /, the
# checkouts' and vhdx_dirs. Looked up once per worker: reg.exe is slow.
disk_paths() {
  if [ -z "$DISK_PATHS" ]; then
    DISK_PATHS=$({ echo /; echo "$REPO"; vhdx_dirs; } | tr '\n' ' ')
  fi
  printf '%s\n' $DISK_PATHS
}

# disk_low: print PATH<tab>GB-free for each watched filesystem with less than
# RELAY_MIN_FREE_GB free, one per filesystem (the first path naming it). A
# path df can't read is skipped.
disk_low() {
  local p line mounts=$'\n' free mnt
  while read -r p; do
    line=$(df -Pk "$p" 2>/dev/null | tail -n +2 | tail -n 1) && [ -n "$line" ] || continue
    free=$(awk '{ print int($4 / 1048576) }' <<<"$line")
    mnt=$(awk '{ print $NF }' <<<"$line")
    [[ $mounts == *$'\n'"$mnt"$'\n'* ]] && continue
    mounts+="$mnt"$'\n'
    [ "$free" -lt "$MIN_FREE_GB" ] && printf '%s\t%s\n' "$p" "$free"
  done < <(disk_paths)
}

# trim_caches: prune the device's build caches (hack/prune-caches.sh, #1343),
# so a disk below RELAY_MIN_FREE_GB may pass the next check.
trim_caches() { "$CODE/hack/prune-caches.sh" 2>&1 | while read -r l; do log "$l"; done; }

# disk_wait: exit 0 while a watched filesystem is below RELAY_MIN_FREE_GB. The
# first time a path is low, and then at most once an hour across workers
# (.relay/disk-low-<path>), it logs the path, its free space and the minimum,
# and trims the caches at once.
disk_wait() {
  local p free low= mark now said= hint=
  grep -qi microsoft "$OSRELEASE" 2>/dev/null &&
    hint="; under WSL only compacting the .vhdx returns space to Windows: docs/dev/testenv.md, \"Disk space on WSL\""
  now=$(date +%s)
  while IFS=$'\t' read -r p free; do
    low=1
    mark="$DIR/disk-low-${p//[^a-zA-Z0-9]/-}"
    [ $((now - $(stat -c %Y "$mark" 2>/dev/null || echo 0))) -lt 3600 ] && continue
    [ -n "$DRY_RUN" ] || touch "$mark"
    log "waiting for disk: $p has ${free} GB free, a session needs ${MIN_FREE_GB} (RELAY_MIN_FREE_GB); starting no session$hint"
    said=1
  done < <(disk_low)
  [ -n "$said" ] && [ -z "$DRY_RUN" ] && trim_caches
  [ -n "$low" ]
}

# paused: print the time .relay/paused-until holds and why, and exit 0, while
# the device is paused for a limit.
paused() {
  local until why
  [ -r "$DIR/paused-until" ] && read -r until why <"$DIR/paused-until" || return 1
  [[ $until =~ ^[0-9]+$ ]] && [ "$until" -gt "$(date +%s)" ] || return 1
  echo "$until $why"
}

# LIMITS: the limits a session can end on, each as WORDS:REGEX, most specific
# first: REGEX matches the CLI's words for it (case-insensitive, extended), and
# WORDS name it where its claim is released (#1199), as an expired login needs
# another fix than a usage limit. The last is limit_message's own line for a
# session that failed before any API call (#1421).
LIMITS=(
  'OAuth session expired:oauth session expired'
  'OAuth token expired:oauth token (has )?expired'
  'OAuth token revoked:oauth token (has )?revoked'
  'invalid API key:invalid api key'
  'not logged in:not logged in|please run /login'
  'authentication failed:failed to authenticate|authentication_error'
  'credit balance too low:credit balance (is )?too low'
  "usage limit:you've hit your|you've reached your|you're out of usage|out of usage credits|usage limit reached"
  'no API call:^no API call: '
)

# limit_name MESSAGE: the WORDS of the first of LIMITS that MESSAGE matches.
# Exit 1: none does.
limit_name() {
  local l
  for l in "${LIMITS[@]}"; do
    grep -qiE -- "${l#*:}" <<<"$1" && { echo "${l%%:*}"; return 0; }
  done
  return 1
}

# limit_message OUT: the line that says session OUT ended on one of LIMITS.
# It reads the CLI's own words only, never the agent's final message (#655):
# the result when it is an error, when the session exited non-zero (S_RC)
# before any turn ran, or when it made no API call (duration_api_ms 0), as a
# login failure ends with num_turns 1 and is_error false on some CLI versions
# (#1396); the result of a session that exited non-zero when it is one line
# that starts with a limit's words, whatever is_error, num_turns and
# duration_api_ms say, as an agent's account never does (#1421); and the tail
# of OUT.err when there is no result or the session exited non-zero.
# A session that exited non-zero before any API call, with no limit's words,
# failed on the device, not on its issue (#1421): no result within its first
# minute, num_turns 0 or duration_api_ms 0, unless it timed out, was killed or
# was stopped idle (S_IDLE). Its line names "no API call", the exit and the
# last thing the CLI said.
limit_message() {
  local out=$1 l re= rc=${S_RC:-0} said=
  for l in "${LIMITS[@]}"; do re+=${re:+|}${l#*:}; done
  # Read from the output, not the pipeline's status: grep -m1 stops at the
  # first match, so under pipefail a tail still writing after it fails with
  # SIGPIPE, and a login result with the trust warning on stderr paused
  # nothing (#1421).
  said=$({
    if jq -e 'has("result")' "$out.json" >/dev/null 2>&1; then
      jq -r --arg rc "$rc" --arg re "^\\s*($re)" '
        select(.is_error == true or .duration_api_ms == 0 or ($rc != "0" and (.num_turns // 0) == 0)
          or ($rc != "0" and ((.result // "") | tostring | (split("\n") | map(select(test("\\S"))) | length) == 1 and test($re; "i"))))
        | .result // empty' "$out.json"
      [ "$rc" = 0 ] || tail -n 20 "$out.err" 2>/dev/null
    else
      tail -n 20 "$out.err" 2>/dev/null
    fi
  } | grep -m1 -iE -- "$re")
  [ -n "$said" ] && { echo "$said"; return 0; }
  case $rc in 0 | 124 | 137 | 143) return 1 ;; esac
  [ -z "${S_IDLE:-}" ] || return 1
  if jq -e 'has("result")' "$out.json" >/dev/null 2>&1; then
    jq -e '(.num_turns // 0) == 0 or .duration_api_ms == 0' "$out.json" >/dev/null 2>&1 || return 1
    said=$(jq -r '.result // empty | tostring' "$out.json" | grep . | head -n 1)
  else
    [ "${S_MINS:-0}" = 0 ] || return 1
  fi
  [ -n "$said" ] || said=$(grep . "$out.err" 2>/dev/null | tail -n 1)
  echo "no API call: the session exited $rc before any API call${said:+: $said}"
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
# a limit (.relay/paused-until), each watched disk at least RELAY_MIN_FREE_GB
# free (disk_wait), MemAvailable at least RELAY_MIN_MEM_GIB, and
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
    if disk_wait; then
      [ -n "$DRY_RUN" ] && return 1
      [ -e "$STOP" ] && { log "stop file found while waiting for disk"; return 1; }
      [ $(($(date +%s) - t0)) -ge "$MEM_WAIT" ] && { log "no disk space after $((MEM_WAIT / 60))m"; return 1; }
      sleep "$MEM_POLL"
      continue
    fi
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

# transcript ID: the file claude writes session ID's transcript to, as it works
# in the worktree: under projects/, the worktree's path with each character
# other than a letter or digit as -. Its subagents write theirs in the
# directory of the same name without .jsonl.
transcript() {
  echo "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/${WT//[^a-zA-Z0-9]/-}/$1.jsonl"
}

# idle_watch WHAT PID FILE MARK IDLE KILL: end session WHAT (process group PID)
# once its transcript FILE and its subagents' haven't changed for IDLE seconds
# (RELAY_IDLE): SIGTERM, then SIGKILL after KILL seconds (RELAY_IDLE_KILL),
# with MARK touched so the relay counts it as failed. A transcript that never
# appears leaves the session to SESSION_TIMEOUT.
idle_watch() {
  local what=$1 pid=$2 f=$3 idle=$5 kill_after=$6 t0 last m now missing= i
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
    # Rounded up: a write at 10.9 counts as 11, so a session is never ended
    # before it has been idle for all of IDLE.
    m=$((${m%.*} + 1))
    [ "$m" -gt "$last" ] && last=$m
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

# scratch_clear DIR: remove DIR, a session's TMPDIR. A Go module cache in it
# is read-only (go mod download makes its directories so), so it is made
# writable first. What can't be removed is logged, and the session goes on.
scratch_clear() {
  [ -e "$1" ] || return 0
  chmod -R u+w "$1" 2>/dev/null
  rm -rf "$1" 2>/dev/null || log "could not remove all of $1"
}

# stray WHAT OUT BEFORE: when session WHAT left the main checkout dirty, unlike
# BEFORE (its git status --porcelain when the session started), name the paths
# in the log and in its account OUT.md, so the next "pull: main has
# uncommitted changes" has a culprit (#1371). The files stay as they are.
stray() {
  local now paths
  now=$(git -C "$REPO" status --porcelain 2>/dev/null)
  [ -n "$now" ] && [ "$now" != "$3" ] || return 0
  paths=$(cut -c4- <<<"$now" | paste -sd' ')
  log "$1 left the main checkout $REPO with uncommitted changes, which stop hack/pull.sh: $paths"
  printf '\nThe relay: this session left the main checkout %s with uncommitted changes: %s\n' "$REPO" "$paths" >>"$2.md"
}

# proc_start PID: PID's start time in clock ticks since boot (/proc/PID/stat
# field 22), which tells a live process from a later one with the same pid.
# Nothing when PID is gone.
proc_start() {
  sed 's/.*) //' "/proc/$1/stat" 2>/dev/null | awk '{print $20}'
}

# stray_session: a worker that restarted (crashed, was killed, or the watch
# started it again) while its session still ran would release the session's
# claim and start another one in the same worktree: two agents in one
# checkout (#1341). So the worker records each session's pid and start time in
# .relay/session-K.pid while it runs, and a new worker, before it touches the
# worktree or the claim, ends the session the file names if it still lives:
# SIGTERM to its process group (timeout's), SIGKILL after RELAY_IDLE_KILL.
stray_session() {
  local f=$DIR/session-$K.pid pid start kill_after i
  read -r pid start 2>/dev/null <"$f" || return 0
  rm -f "$f"
  [[ $pid =~ ^[0-9]+$ ]] && [ -n "$start" ] && [ "$(proc_start "$pid")" = "$start" ] || return 0
  kill_after=$(gh_seconds "$IDLE_KILL" RELAY_IDLE_KILL 2>/dev/null) || kill_after=60
  log "worker $K restarted while its last session (pid $pid) still ran in $WT; stopping it before anything else (#1341)"
  kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null
  for ((i = 0; i < kill_after * 10; i++)); do
    [ "$(proc_start "$pid")" = "$start" ] || return 0
    sleep 0.1
  done
  log "the last session (pid $pid) still ran $IDLE_KILL after SIGTERM; SIGKILL"
  kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null
  for ((i = 0; i < 100; i++)); do
    [ "$(proc_start "$pid")" = "$start" ] || return 0
    sleep 0.1
  done
  log "the last session (pid $pid) survived SIGKILL; going on"
}

# session WHAT OUT ID PROMPT: run headless session ID in the worktree with
# PROMPT, keep its output in OUT.json and OUT.err and its account in OUT.md,
# and log its end as WHAT. Exit 3: it ended on one of LIMITS, whose message
# S_LIMIT holds.
session() {
  local what=$1 out=$2 id=$3 rc t0 mins msg pid watch= idle kill_after dirty tmp=$SCRATCH/${WT##*/}
  t0=$(date +%s)
  # Checked here, not in the watch: a session that ends before the watch runs
  # would take the log line with it.
  if ! idle=$(gh_seconds "$IDLE" RELAY_IDLE 2>/dev/null) || ! kill_after=$(gh_seconds "$IDLE_KILL" RELAY_IDLE_KILL 2>/dev/null); then
    log "RELAY_IDLE ($IDLE) or RELAY_IDLE_KILL ($IDLE_KILL) isn't like 45m; $what runs until the $SESSION_TIMEOUT timeout"
    idle=
  fi
  # timeout leads its own process group, so the idle watch can end claude and
  # every process it started. Neither gets the worker's lock (fd 9): a process
  # the session leaves behind would hold it after the worker exits, and the
  # watch would see the worker as running.
  # Started from a Claude Code session or not, the session gets the same
  # environment (#645). Its TMPDIR is under SCRATCH, not the shared /tmp
  # (#1269) nor in a checkout (#1307), and starts empty: what a dead session
  # left there goes first.
  # C4_RELAY_WORKER names the worker (1, 2, ..., triage, maintainer): create
  # --next reserves only for a numbered one's next session.
  # OLDPWD is the worktree too: the cd would leave it at the main checkout,
  # and an agent's cd - or cd "$OLDPWD" would write to main (#1371).
  scratch_clear "$tmp"
  mkdir -p "$tmp"
  dirty=$(git -C "$REPO" status --porcelain 2>/dev/null)
  (cd "$WT" && export OLDPWD=$WT && drop_session_env && TMPDIR=$tmp C4_HEADLESS=1 C4_RELAY_WORKER=$K exec timeout "$SESSION_TIMEOUT" claude -p --permission-mode auto --model "$RELAY_MODEL" \
    --session-id "$id" --output-format json "$4") >"$out.json" 2>"$out.err" 9>&- &
  pid=$!
  # For stray_session (#1341): the pid and its start time, while it runs.
  echo "$pid $(proc_start "$pid")" >"$DIR/session-$K.pid"
  rm -f "$out.idle"
  if [ -n "$idle" ]; then
    idle_watch "$what" "$pid" "$(transcript "$id")" "$out.idle" "$idle" "$kill_after" 9>&- &
    watch=$!
  fi
  wait "$pid"
  rc=$?
  rm -f "$DIR/session-$K.pid"
  if [ -n "$watch" ]; then
    kill "$watch" 2>/dev/null
    wait "$watch" 2>/dev/null
  fi
  scratch_clear "$tmp"
  S_IDLE=
  [ -e "$out.idle" ] && S_IDLE=1
  mins=$((($(date +%s) - t0) / 60))
  if [ -s "$out.json" ] && jq -e .result "$out.json" >/dev/null 2>&1; then
    jq -r .result "$out.json" >"$out.md"
    log "$what end: exit $rc after ${mins}m, $(jq -r '"\(.num_turns) turns, error \(.is_error)"' "$out.json"); account in $out.md"
  else
    log "$what end: exit $rc after ${mins}m, no result; see $out.err"
  fi
  stray "$what" "$out" "$dirty"
  [ "$rc" = 124 ] && log "$what hit the $SESSION_TIMEOUT timeout"
  [ "$rc" = 143 ] && log "$what was killed (SIGTERM)"
  S_RC=$rc S_MINS=$mins S_LIMIT=
  if msg=$(limit_message "$out"); then
    S_LIMIT=$msg
    pause "$msg"
    return 3
  fi
  return 0
}

# The relay's strike comments on an issue: one per failed session, one that
# resets the count, one that sends the issue to the maintainer. They are read
# back from the issue, so every device's strikes add up.
STRIKE_MARKER='<!-- relay-strike -->'
STRIKE_RESET_MARKER='<!-- relay-strike-reset -->'
STRIKE_ESCALATED_MARKER='<!-- relay-strike-escalated -->'

# strikes N: the URLs of issue N's strikes that count, oldest first: the
# relay's strike comments, from any device, newer than RELAY_STRIKE_TTL and
# after N's newest handoff, reset and escalation comments.
strikes() {
  local all ttl
  ttl=$(gh_seconds "$STRIKE_TTL" RELAY_STRIKE_TTL) || return 1
  all=$(gh_comments "$1") || return 1
  jq -r --arg s "$STRIKE_MARKER" --arg h "$GH_HANDOFF_MARKER" --arg r "$STRIKE_RESET_MARKER" \
    --arg e "$STRIKE_ESCALATED_MARKER" --argjson ttl "$ttl" '
    def starts($m): .body | sub("^\\s+"; "") | startswith($m);
    ([to_entries[] | select(.value | starts($h) or starts($r) or starts($e)) | .key] | last // -1) as $cut
    | .[$cut + 1:][] | select(starts($s) and (.created_at | fromdateiso8601) > now - $ttl) | .html_url' <<<"$all"
}

# failed N ID OUT: count session ID (output OUT, with S_RC and S_MINS) as a
# strike on issue N, a comment on N; at RELAY_MAX_FAILS strikes that count,
# escalate N to the maintainer (status/needs-maintainer) with one comment
# naming them. When that comment is posted but the status swap fails, the
# next failed session on any device retries the swap alone (unescalated).
failed() {
  local n=$1 url urls count
  if ! url=$(unescalated "$n" 2>>"$LOG"); then
    log "#$n: could not read whether its escalation is still to swap; counting the session"
  elif [ -n "$url" ]; then
    escalate "$n" "$url"
    return 0
  fi
  if ! url=$(strike_comment "$n" "$2" "$3" | gh_comment "$n" - 2>>"$LOG"); then
    log "#$n: could not post the strike for session $2; it isn't counted"
    return 0
  fi
  if ! urls=$(strikes "$n" 2>>"$LOG"); then
    log "#$n: could not read its strikes; posted $url"
    return 0
  fi
  count=$(grep -c . <<<"$urls")
  if [ "$count" -lt "$MAX_FAILS" ]; then
    log "#$n: $count strike(s) within $STRIKE_TTL, $url; status/needs-maintainer at $MAX_FAILS (RELAY_MAX_FAILS)"
    return 0
  fi
  if url=$(escalation_comment "$n" "$urls" | gh_comment "$n" - 2>>"$LOG"); then
    escalate "$n" "$url"
  else
    log "#$n has $count strikes, but posting the comment for the maintainer failed; trying again after the next"
  fi
}

# unescalated N: the URL of N's escalation comment when its status swap never
# happened, or nothing: the newest of N's handoff, reset and escalation
# comments is an escalation, and N wasn't labeled status/needs-maintainer at
# or after it. Read from the issue, so any device retries the swap (#1213);
# one the maintainer set back to ready after the swap isn't escalated again.
unescalated() {
  local all esc at url
  all=$(gh_comments "$1") || return 1
  esc=$(jq -r --arg h "$GH_HANDOFF_MARKER" --arg r "$STRIKE_RESET_MARKER" --arg e "$STRIKE_ESCALATED_MARKER" '
    def starts($m): .body | sub("^\\s+"; "") | startswith($m);
    [.[] | select(starts($h) or starts($r) or starts($e))] | last // empty
    | select(starts($e)) | "\(.created_at) \(.html_url)"' <<<"$all") || return 1
  [ -n "$esc" ] || return 0
  read -r at url <<<"$esc"
  all=$(label_events "$1") || return 1
  jq -e --arg at "$at" 'any(.[]; .event == "labeled" and .label.name == "status/needs-maintainer" and .created_at >= $at)' \
    <<<"$all" >/dev/null && return 0
  echo "$url"
}

# label_events N: issue N's events as one JSON array.
label_events() {
  local pages
  pages=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/$1/events?per_page=100") || return 1
  jq -c '[.[][] | {event, created_at, label: {name: .label.name}}]' <<<"$pages"
}

# escalate N URL: set N to status/needs-maintainer, its comment for the
# maintainer at URL posted; when that fails, the next failed session retries.
escalate() {
  if gh_set_status "$1" needs-maintainer >/dev/null 2>>"$LOG"; then
    log "#$1 reached $MAX_FAILS strikes; set it to status/needs-maintainer: $2"
  else
    log "#$1 reached $MAX_FAILS strikes and has its comment ($2), but setting it to status/needs-maintainer failed; trying again after the next failed session"
  fi
}

# strike_comment N ID OUT: the strike comment for session ID's failure on N,
# with the last lines of its stderr and account.
strike_comment() {
  echo "$STRIKE_MARKER"
  echo "**Strike:** session \`$2\` on host \`$(hostname)\` ended without a handoff and without a new commit on its branch: exit $S_RC after ${S_MINS}m. \`claude --resume $2\` in \`$WT\` on that host opens it. $MAX_FAILS strikes within $STRIKE_TTL, from any device, with no handoff, commit or finish between them, send the issue to the maintainer."
  echo
  echo "stderr (\`$3.err\`), last lines:"
  echo '```'
  tail -n 10 "$3.err" 2>/dev/null | cut -c1-300
  echo '```'
  echo
  echo "Account (\`$3.md\`), last lines:"
  echo '```'
  tail -n 10 "$3.md" 2>/dev/null | cut -c1-300
  echo '```'
}

# escalation_comment N URLS: the comment that hands issue N to the maintainer,
# linking each strike in URLS.
escalation_comment() {
  echo "$STRIKE_ESCALATED_MARKER"
  echo "The relay stopped working #$1: $(grep -c . <<<"$2") sessions within $STRIKE_TTL ended without a handoff and without a new commit on its branch. Each strike names its session, host, stderr and account:"
  echo
  sed 's/^/- /' <<<"$2"
  echo
  echo "Maintainer: find what stops them, fix it or file the fix, then set the issue back to \`status/ready\`; send it to the owner only if that needs them. This comment resets the count."
}

# forget_fails N: a handoff, a commit or a finish ends issue N's strikes: post
# a reset comment when any count, so every device stops counting them.
forget_fails() {
  local urls
  urls=$(strikes "$1" 2>>"$LOG") || { log "#$1: could not read its strikes to reset them"; return 0; }
  [ -n "$urls" ] || return 0
  if printf '%s\n%s\n' "$STRIKE_RESET_MARKER" "The relay on host \`$(hostname)\` saw a handoff, a commit or a finish on #$1; the $(grep -c . <<<"$urls") strike(s) before this comment no longer count." |
    gh_comment "$1" - >/dev/null 2>>"$LOG"; then
    log "#$1: reset its strikes"
  else
    log "#$1: could not post the reset of its strikes"
  fi
}

# .relay/triaged: the last triage's time and the issues it saw, which the
# watch reads; only these two write it, for a triage the watch started or one
# started from a terminal alike.
# untriaged: tell the watch the triage session never ran: keep only the time,
# so the same issues start one again after WATCH_TRIAGE.
untriaged() { [ -n "$DRY_RUN" ] || date +%s >"$DIR/triaged"; }

# triaged LIST: tell the watch a triage session starts now on LIST (#1 #2 ...),
# so it starts no other on the same issues.
triaged() { [ -n "$DRY_RUN" ] || echo "$(date +%s) ${1//#/}" >"$DIR/triaged"; }

# triage: one triage session on the open proposed issues (see the header).
triage() {
  local list id
  if [ -z "$DRY_RUN" ]; then
    worktree || { log "could not prepare the worktree $WT; no triage"; untriaged; return 1; }
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
  triaged "$list"
  session triage "$DIR/triage-$(date +%Y%m%d-%H%M%S)" "$id" "$(triage_prompt "$list")"
}

# start_triage SCRIPT: start one triage session, detached, by running SCRIPT
# (hack/relay.sh) in the background, in its own worktree under its own lock,
# however many workers run (see the header). Exit 1 when one runs already (its
# record in .relay/triaged stands), or with no GitHub token (untriaged, so the
# watch tries again for its issues after WATCH_TRIAGE).
start_triage() {
  [ -e "$STOP" ] && { echo "the stop file is there; no triage"; return 0; }
  flock -n "$LOCK" true || { echo "relay: a triage runs already; no second one: tail -f $LOG" >&2; return 1; }
  gh_token || { untriaged; return 1; }
  C4_RELAY_CHILD=1 C4_RELAY_TRIAGE=1 C4_RELAY_REPO="$REPO" setsid nohup bash "$1" >>"$DIR/relay-$K.out" 2>&1 </dev/null &
  echo "triage started (pid $!): tail -f $LOG"
}

# maintained START ACTIVITY: tell the watch, and the next sweep's prompt, that
# a sweep that started at START (epoch seconds) ran to its end and saw
# ACTIVITY. A sweep that didn't run to its end leaves the file as it is, so the
# next one still looks back to the last full sweep.
maintained() { [ -n "$DRY_RUN" ] || echo "$1 $2" >"$DIR/maintained"; }

# maintain: one maintainer session (see the header).
maintain() {
  local id last t act rc out start lease beat
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
  # One maintainer per project (#526): another device's live lease means no sweep here.
  lease=$(gh_maint_take 2>>"$LOG")
  case $? in
    0) ;;
    4) log "another device's maintainer holds the lease: $lease; no sweep"; return 0 ;;
    *) log "could not take the maintainer lease; no sweep"; return 1 ;;
  esac
  log "maintainer start: id $id, last sweep $last, HEAD $(git -C "$WT" rev-parse --short HEAD), lease $lease"
  out="$DIR/maintain-$(date +%Y%m%d-%H%M%S)"
  start=$(date +%s)
  # The heartbeat; its sleep ends with it.
  (s=
    trap 'kill $s 2>/dev/null; exit 0' TERM
    while :; do
      sleep "$GH_BEAT_EVERY" &
      s=$!
      wait "$s"
      gh_maint_beat "$lease" 2>>"$LOG" || log "could not refresh the maintainer lease $lease"
    done) 9>&- &
  beat=$!
  session maintainer "$out" "$id" "$(maintain_prompt "$last")"
  rc=$?
  kill "$beat" 2>/dev/null
  wait "$beat" 2>/dev/null
  if gh_delete_comment "$lease" 2>>"$LOG"; then
    log "released the maintainer lease $lease"
  else
    log "could not release the maintainer lease $lease; it goes stale after $C4_LEASE_TTL"
  fi
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
  local i id rc try claim name branch before head refs out limited limit result reason released l
  for i in $(seq 1 "$MAX_SESSIONS"); do
    [ -e "$STOP" ] && { log "stop file found; stopping"; break; }
    if [ -z "$DRY_RUN" ]; then
      update $((MAX_SESSIONS - i + 1))
      worktree || { log "could not prepare the worktree $WT; stopping"; break; }
      # The session that held it is gone: stray_session ran at the start (#1341).
      if [ "$i" = 1 ] && gh_claim_read; then
        log "$(gh_release "worker $K restarted" 2>&1 | tr '\n' ' ')"
      fi
      can_commit || { log "stopping: a session here couldn't commit"; break; }
    fi
    flush || { log "GitHub can't be reached; stopping"; break; }
    # The yolo round (#1534): while yolo mode is on, every open non-draft pull
    # request merges now; off or unknown, gh_yolo merge says and does nothing.
    if [ -z "$DRY_RUN" ]; then
      gh_yolo merge 2>&1 | while read -r l; do log "yolo: $l"; done
    fi

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
      log "would switch $WT to the issue's branch and start session $i there: claude -p --permission-mode auto --model $RELAY_MODEL --session-id $id, with this prompt:"
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
    refs=$(branch_refs "$branch")
    log "session $i start: id $id, $name on #$ISSUE ($HOW) $TITLE, branch $branch, HEAD ${head:0:7}"
    out="$DIR/session-$K-$i-$(date +%Y%m%d-%H%M%S)"
    session "session $i" "$out" "$id" "$(prompt "$ISSUE" "$TITLE" "$URL" "$HOW" "$name" "$branch")"
    limited=$?

    flush || { log "GitHub can't be reached; stopping"; break; }
    result=$(outcome "$ISSUE" "$before") || result=unknown
    if gh_claim_read; then
      case $result in
        closed) reason=finished ;;
        handed-off) reason="handed off" ;;
        waiting) reason="waiting on its steps" ;;
        released) reason="set back to ready or proposed" ;;
        *)
          reason="session ended without a handoff"
          if [ "$limited" = 3 ]; then
            limit=$(limit_name "$S_LIMIT") || limit=
            reason+=" (a limit${limit:+: $limit})"
          fi
          ;;
      esac
      if released=$(gh_release "$reason" 2>&1); then
        log "${released//$'\n'/; }"
      else
        log "could not release the claim on #$ISSUE; it expires after $C4_LEASE_TTL: ${released//$'\n'/; }"
      fi
    fi
    case $result in
      closed | needs-maintainer | needs-human | waiting | handed-off) forget_fails "$ISSUE" ;;
    esac
    case $result in
      closed) log "session $i closed #$ISSUE" ;;
      needs-maintainer) log "session $i escalated #$ISSUE to the maintainer (status/needs-maintainer)" ;;
      needs-human) log "session $i left #$ISSUE waiting for the owner (status/needs-human)" ;;
      handed-off) log "session $i handed off #$ISSUE" ;;
      waiting) log "session $i left #$ISSUE waiting on its steps (status/waiting)" ;;
      released)
        # Given back, not failed: no strike, and the count stands.
        log "session $i set #$ISSUE back to ready or proposed without a handoff; not counted against it; stopping (claude --resume $id in $WT to see why)"
        break
        ;;
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
        elif [ "$(branch_refs "$branch")" != "$refs" ]; then
          forget_fails "$ISSUE"
          log "session $i left no handoff on #$ISSUE but committed on $branch or a copy of it; stopping (claude --resume $id in $WT to see why)"
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
drop_session_env
# Headless, and its sessions too: no cluster lease code (gh_headless).
export C4_HEADLESS=1

mkdir -p "$DIR"
[ -e "$DIR/.gitignore" ] || echo '*' >"$DIR/.gitignore"
[ "${C4_RELAY_CHILD:-}" = 1 ] || [ -n "$TRIAGE" ] || [ -n "$MAINTAIN" ] || cap "$WORKERS" || exit 1

# --maintain from a terminal or the watch: start the maintainer, detached,
# unless it runs. It doesn't count against RELAY_MAX_WORKERS.
if [ -n "$MAINTAIN" ] && [ -z "$DRY_RUN" ] && [ "${C4_RELAY_CHILD:-}" != 1 ]; then
  [ -e "$STOP" ] && { echo "the stop file is there; no sweep"; exit 0; }
  flock -n "$LOCK" true || { echo "the maintainer is already running: tail -f $LOG"; exit 0; }
  gh_token || exit 1
  C4_RELAY_CHILD=1 C4_RELAY_MAINTAIN=1 C4_RELAY_REPO="$REPO" setsid nohup bash "$0" >>"$DIR/relay-$K.out" 2>&1 </dev/null &
  echo "maintainer started (pid $!): tail -f $LOG"
  exit 0
fi

# --triage from a terminal or the watch: start one triage session, detached.
if [ -n "$TRIAGE" ] && [ -z "$DRY_RUN" ] && [ "${C4_RELAY_CHILD:-}" != 1 ]; then
  start_triage "$0"
  exit
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
    C4_RELAY_CHILD=1 C4_RELAY_WORKER=$k C4_RELAY_REPO="$REPO" setsid nohup bash "$0" >>"$DIR/relay-$k.out" 2>&1 </dev/null &
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
# Before the worktree, the claim or a session is touched (#1341).
[ -n "$DRY_RUN" ] || stray_session
CODE_SUM=$(code_sum)
gh_token 2>/dev/null || { log "no GitHub token; stopping"; [ -z "$TRIAGE" ] || untriaged; exit 1; }
log "worker $K started: pid $$${DRY_RUN:+, dry run}${C4_CLUSTER:+, C4_CLUSTER=$C4_CLUSTER}, worktree $WT, code $CODE at $(git -C "$CODE" rev-parse --short HEAD), at most $MAX_SESSIONS sessions of $SESSION_TIMEOUT, issues on $C4_GITHUB_REPO"
if [ -n "$MAINTAIN" ]; then
  maintain
  log "the maintainer finished its sweep"
  exit 0
fi
if [ -n "$TRIAGE" ]; then
  triage
  log "the triage finished"
  exit 0
fi
# Workers started together would all race for the same first issue.
[ -n "$DRY_RUN" ] || [ "${C4_RELAY_RESTART:-}" = 1 ] || sleep $(((K - 1) * 20))

work
log "worker $K finished"
