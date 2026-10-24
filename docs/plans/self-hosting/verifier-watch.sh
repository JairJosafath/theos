#!/usr/bin/env bash
# The verifier watch of docs/plans/self-hosting.md. The owner installs it once,
# with the plan's one command, as the systemd user timer c4factory-verifier,
# which runs it every hour inside the project environment (./dev run). Each
# session it starts gets C4_HUMAN_CLUSTER=1: the owner's opt-in for verifier
# sessions (AGENTS.md, "Agents Never Touch a Cluster"), given by installing
# the timer (the plan's rule 3).
#
#   ./dev run docs/plans/self-hosting/verifier-watch.sh        start the due step's session, if any
#   ./dev run docs/plans/self-hosting/verifier-watch.sh --due  print the due step's number, if any
#
# A step is due when it is an open issue titled "Verifier: ..." and every
# "Depends on #S" in its body is closed; the lowest number goes first. One
# session runs at a time (flock). A step still open after 48 sessions is set
# status/needs-maintainer with one comment, and the watch skips it (the plan's
# rule 6). When the plan's parent is closed, the watch turns its timer off.
# Every comment it posts carries an Agent: line, so none reads as the owner's.
set -euo pipefail

root=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$root"
repo=JairJosafath/c4factory
state_dir=.dev/self-hosting
parent_file=.dev/self-hosting-parent
log=.dev/self-hosting-verifier.log
max_attempts=${SELF_HOSTING_MAX_ATTEMPTS:-48}

# Step 0 hasn't filed the plan yet: nothing to do.
[ -s "$parent_file" ] || exit 0
parent=$(tr -dc 0-9 <"$parent_file")
[ -n "$parent" ] || exit 0
mkdir -p "$state_dir"

exec 9>"$state_dir/lock"
flock -n 9 || exit 0

# The token hack/lib/github.sh uses: GH_TOKEN, else the git credential helper.
if [ -z "${GH_TOKEN:-}" ]; then
  GH_TOKEN=$(printf 'protocol=https\nhost=github.com\n\n' |
    GIT_TERMINAL_PROMPT=0 timeout 30 git credential fill 2>/dev/null |
    sed -n 's/^password=//p') || true
fi
if [ -z "${GH_TOKEN:-}" ]; then
  echo "$(date -Is) no GitHub token: GH_TOKEN is unset and the git credential helper gave none" >>"$log"
  exit 0
fi
export GH_TOKEN

state() { gh issue view "$1" -R "$repo" --json state -q .state; }

if [ "$(state "$parent")" = CLOSED ]; then
  echo "$(date -Is) parent #$parent is closed; turning the timer off" >>"$log"
  systemctl --user disable --now c4factory-verifier.timer >/dev/null 2>&1 || true
  exit 0
fi

# NUMBER DEPENDENCIES..., one open "Verifier:" step per line, lowest first.
if ! steps=$(gh issue list -R "$repo" --state open --limit 200 --json number,title,body -q '
  [.[] | select(.title | startswith("Verifier:"))] | sort_by(.number) | .[]
  | "\(.number) \([(.body // "") | scan("[Dd]epends on #([0-9]+)") | .[0]] | join(" "))"'); then
  echo "$(date -Is) GitHub can't be asked for the open issues; trying again next hour" >>"$log"
  exit 0
fi

due=""
while read -r n deps; do
  [ -n "$n" ] || continue
  [ -e "$state_dir/gave-up-$n" ] && continue
  ready=1
  for d in $deps; do
    if [ "$(state "$d")" != CLOSED ]; then ready=0; break; fi
  done
  if [ "$ready" = 1 ]; then due=$n; break; fi
done <<<"$steps"

if [ "${1:-}" = --due ]; then
  [ -z "$due" ] || echo "$due"
  exit 0
fi
[ -n "$due" ] || exit 0

attempts_file="$state_dir/attempts-$due"
attempts=$(cat "$attempts_file" 2>/dev/null || echo 0)
if [ "$attempts" -ge "$max_attempts" ]; then
  body=$(mktemp)
  printf '%s\n\n%s\n' \
    "The verifier watch started $attempts sessions for this step and it is still open, so it stops starting them (docs/plans/self-hosting.md, rule 6). The newest comments above say what stopped each one; the log is .dev/self-hosting-verifier.log on the device." \
    "Agent: self-hosting-verifier-watch" >"$body"
  gh issue comment "$due" -R "$repo" --body-file "$body" >/dev/null || true
  rm -f "$body"
  hack/lib/github.sh status "$due" needs-maintainer >/dev/null 2>&1 || true
  touch "$state_dir/gave-up-$due"
  echo "$(date -Is) #$due: gave up after $attempts sessions" >>"$log"
  exit 0
fi

hack/pull.sh >/dev/null 2>&1 || true
echo "$(date -Is) #$due: verifier session $((attempts + 1))" >>"$log"
# The ~/.claude login: unset, not set to ~/.claude, which would move the
# config to ~/.claude/.claude.json, where the workspace isn't trusted and
# .claude/settings.json's allow rules are ignored.
env -u CLAUDE_CONFIG_DIR C4_HUMAN_CLUSTER=1 \
  claude -p --permission-mode auto --model opus \
  "You are the verifier session of docs/plans/self-hosting.md for issue #$due. Do its step as the plan says, close the issue when its Done when holds, or comment on it what stopped you, then end. Project commands bare, as the plan says." \
  >>"$log" 2>&1 || echo "$(date -Is) #$due: the session exited $?" >>"$log"

if [ "$(state "$due")" != CLOSED ]; then
  echo $((attempts + 1)) >"$attempts_file"
fi
