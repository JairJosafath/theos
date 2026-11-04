#!/usr/bin/env bash
# The owner's one command for docs/plans/self-hosting.md, as a script. Run it
# once, from a plain shell in the main checkout, on the device that runs the
# relay and the Kind clusters (README.md next to it says what to check first):
#
#   docs/plans/self-hosting/start.sh           check what the plan needs, then start it
#   docs/plans/self-hosting/start.sh --check   only check, change nothing
#
# Starting it does three things, each only when the one before it worked:
#   1. step 0: a headless session files the plan's parent issue and steps,
#      readies the relay's steps and checks the relay runs;
#   2. your binding comment on the parent, from your account (it makes the
#      plan's decisions yours, GOVERNANCE.md "The owner's word wins");
#   3. the verifier watch: the systemd user timer theos-verifier, which
#      starts the cluster steps when they are due.
# Running it again is safe: it skips what is already done.
set -euo pipefail

root=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$root"
# Inside the project environment (./dev run): its tools on PATH.
if [ -z "${THEOS_DEV:-}" ]; then
  exec ./dev run "$root/docs/plans/self-hosting/start.sh" "$@"
fi

check_only=false
case "${1:-}" in
  "") ;;
  --check) check_only=true ;;
  *) echo "usage: docs/plans/self-hosting/start.sh [--check]" >&2; exit 64 ;;
esac

repo=JairJosafath/theos
here=docs/plans/self-hosting
parent_file=.dev/self-hosting-parent
units="$HOME/.config/systemd/user"
problems=0
ok() { printf '  ok    %s\n' "$1"; }
bad() { printf '  FIX   %s\n        %s\n' "$1" "$2"; problems=$((problems + 1)); }

echo "Checking what the plan needs:"

# The main checkout, on main, clean: step 0 refuses anything else.
if [ -d .git ]; then ok "the main checkout ($root)"; else
  bad "this is a worktree" "run it from the main checkout (cd ~/theos)"; fi
branch=$(git rev-parse --abbrev-ref HEAD)
if [ "$branch" = main ]; then ok "on main"; else
  bad "on branch $branch" "git switch main"; fi
hack/pull.sh >/dev/null 2>&1 || true
if [ -z "$(git status --short)" ]; then ok "nothing uncommitted"; else
  bad "uncommitted changes" "commit or stash them: git status --short lists them"; fi
if [ -f docs/plans/self-hosting.md ]; then ok "the plan is here"; else
  bad "docs/plans/self-hosting.md is missing" "git pull --ff-only origin main"; fi

# The relay's Claude login: step 0, the relay and the verifier sessions use it.
if [ -f "$HOME/.claude/.credentials.json" ]; then ok "Claude Code is logged in (~/.claude)"; else
  bad "Claude Code isn't logged in at ~/.claude" "run: env -u CLAUDE_CONFIG_DIR claude, then /login"; fi

# The agents' token, which step 8 stores in the cluster.
token_file=.dev/secrets/claude-token
if [ -f "$token_file" ]; then
  mode=$(stat -c %a "$token_file")
  if (((8#$mode & 8#077) == 0)); then ok "the agents' token ($token_file)"; else
    bad "$token_file has mode $mode" "chmod 600 $token_file"; fi
else
  bad "no agents' token at $token_file" "claude setup-token, then: install -m 600 /dev/stdin $token_file (paste it, then Ctrl-D)"
fi

# The GitHub token the relay uses, from the git credential helper.
if [ -z "${GH_TOKEN:-}" ]; then
  GH_TOKEN=$(printf 'protocol=https\nhost=github.com\n\n' |
    GIT_TERMINAL_PROMPT=0 timeout 30 git credential fill 2>/dev/null |
    sed -n 's/^password=//p') || true
fi
export GH_TOKEN
if [ -n "$GH_TOKEN" ] && login=$(gh api user -q .login 2>/dev/null); then
  ok "GitHub answers as $login"
else
  bad "no working GitHub token" "the git credential helper must give one: git push from this checkout should work without a prompt"
fi

# Docker, Kind, kubectl and Helm, for the cluster steps.
if docker info >/dev/null 2>&1; then ok "Docker answers"; else
  bad "Docker doesn't answer" "start Docker Desktop (or the Docker service)"; fi
for tool in kind kubectl helm; do
  if command -v "$tool" >/dev/null; then ok "$tool"; else
    bad "$tool is missing" "./dev setup"; fi
done

# The systemd user manager runs the watch; it must reach Docker (devices.md).
if systemctl --user show-environment >/dev/null 2>&1; then
  ok "the systemd user manager"
  manager=$(pgrep -u "$USER" -x systemd | head -1 || true)
  docker_gid=$(getent group docker | cut -d: -f3 || true)
  if [ -n "$manager" ] && [ -n "$docker_gid" ] &&
    ! grep -qE "^Groups:.*\b$docker_gid\b" "/proc/$manager/status"; then
    bad "the systemd user manager lacks the docker group" "on WSL, run wsl --shutdown from Windows, then open the distro again"
  fi
else
  bad "no systemd user manager" "enable systemd in /etc/wsl.conf ([boot] systemd=true), then wsl --shutdown from Windows"
fi

if [ "$problems" -gt 0 ]; then
  echo "Fix the $problems item(s) above, then run this again. Nothing was started."
  exit 1
fi
echo "Everything the plan needs is there."
$check_only && exit 0

# 1. Step 0, unless it already filed the parent.
parent=$(tr -dc 0-9 <"$parent_file" 2>/dev/null || true)
if [ -n "$parent" ] && gh issue view "$parent" -R "$repo" --json number >/dev/null 2>&1; then
  echo "Step 0 already ran: the plan's parent is #$parent."
else
  echo "Starting step 0 (a headless session; it takes a few minutes)..."
  env -u CLAUDE_CONFIG_DIR claude -p --permission-mode auto --model opus \
    "Run step 0 of docs/plans/self-hosting.md, then end. Project commands bare, as the plan says."
  parent=$(tr -dc 0-9 <"$parent_file" 2>/dev/null || true)
  if [ -z "$parent" ]; then
    echo "Step 0 ended without filing the plan's parent ($parent_file is empty). Its output is above; nothing else was started." >&2
    exit 1
  fi
  echo "Step 0 filed the plan's parent: #$parent."
fi

# 2. Your binding comment, once.
if gh api "repos/$repo/issues/$parent/comments" --paginate -q '.[].body' 2>/dev/null |
  grep -qx 'binding'; then
  echo "The parent already has your binding comment."
else
  gh issue comment "$parent" -R "$repo" --body binding >/dev/null
  echo "Posted your binding comment on #$parent."
fi

# 3. The verifier watch.
install -D -m 644 -t "$units" "$here/theos-verifier.service" "$here/theos-verifier.timer"
systemctl --user daemon-reload
systemctl --user enable --now theos-verifier.timer >/dev/null
loginctl enable-linger "$USER" >/dev/null 2>&1 ||
  echo "Note: loginctl enable-linger failed; the watch then runs only while you are logged in to this device."
echo "The verifier watch runs hourly (systemctl --user list-timers theos-verifier.timer)."

cat <<EOF

Started. From here nothing waits for you except a change to the fence.
  The plan's parent:  https://github.com/$repo/issues/$parent
  The watch's log:    $root/.dev/self-hosting-verifier.log
  Stop the watch:     systemctl --user disable --now theos-verifier.timer
  Stop the relay:     hack/relay-watch.sh --uninstall
EOF
