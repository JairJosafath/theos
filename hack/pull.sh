#!/usr/bin/env bash
# Pull: keep this checkout's main up to date with origin/main without touching
# work in progress, so nobody has to remember to pull. It fetches, then
# fast-forwards local main only when that is safe:
#   - main is checked out here with no tracked changes: git merge --ff-only
#   - main is checked out in no worktree: the ref moves (git update-ref)
# Anything else (main has commits origin/main lacks, tracked changes on main,
# main checked out or being rebased in another worktree, a merge, rebase,
# cherry-pick or bisect in progress here) leaves main alone and says why. It
# never merges, rebases, stashes or touches another branch.
#
#   hack/pull.sh             once; silent when main is already current
#   hack/pull.sh --every N   every N seconds until killed; repeats a reason only when it changes
#
# Every device runs it: VS Code from .vscode/tasks.json while the folder is open,
# Claude Code at session start (.claude/settings.json), and hack/relay.sh before
# each session.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO" || exit 1
export GIT_TERMINAL_PROMPT=0
LAST=

# say MSG: print MSG with the time, unless it repeats the last message.
say() {
  [ "$1" = "$LAST" ] && return 0
  LAST=$1
  echo "$(date +%H:%M) pull: $1"
}

# busy: a merge, rebase, cherry-pick, revert or bisect is in progress in this
# worktree.
busy() {
  local f
  for f in MERGE_HEAD CHERRY_PICK_HEAD REVERT_HEAD BISECT_LOG rebase-merge rebase-apply; do
    [ -e "$(git rev-parse --git-path "$f")" ] && return 0
  done
  return 1
}

# rebasing_main: some worktree is rebasing main. A rebase detaches HEAD and
# moves its branch only when it ends, so main looks checked out nowhere.
rebasing_main() {
  local common d
  common=$(git rev-parse --git-common-dir)
  for d in "$common" "$common"/worktrees/*; do
    grep -qsx refs/heads/main "$d/rebase-merge/head-name" "$d/rebase-apply/head-name" && return 0
  done
  return 1
}

# pull: pull_once under a lock, so the VS Code task, a session hook and the
# relay never pull at the same time; when another pull runs, skip.
pull() {
  local git_dir rc
  git_dir=$(git rev-parse --git-common-dir) || return 1
  exec 8>"$git_dir/c4-pull.lock"
  if ! flock -n 8; then
    exec 8>&-
    return 0
  fi
  pull_once
  rc=$?
  exec 8>&-
  return "$rc"
}

# pull_once: fetch, then fast-forward main if it is safe.
pull_once() {
  local before remote branch n worktrees
  if ! timeout 60 git fetch -q --prune origin 2>/dev/null; then
    say "could not fetch origin; main left as it is"
    return 1
  fi
  before=$(git rev-parse -q --verify refs/heads/main) || { say "no local main"; return 0; }
  remote=$(git rev-parse -q --verify refs/remotes/origin/main) || { say "no origin/main"; return 0; }
  if [ "$before" = "$remote" ]; then
    LAST=
    return 0
  fi
  if ! git merge-base --is-ancestor "$before" "$remote"; then
    say "main has commits origin/main lacks; left alone"
    return 0
  fi
  branch=$(git symbolic-ref -q --short HEAD)
  if [ "$branch" = main ]; then
    if busy; then
      say "a merge, rebase, cherry-pick or bisect is in progress; main left alone"
      return 0
    fi
    git update-index -q --refresh >/dev/null 2>&1
    if ! git diff-index --quiet HEAD --; then
      say "main has uncommitted changes; left alone"
      return 0
    fi
    if ! git merge -q --ff-only refs/remotes/origin/main >/dev/null 2>&1; then
      say "could not fast-forward main (untracked files in the way?); left alone"
      return 0
    fi
  elif ! worktrees=$(git worktree list --porcelain); then
    say "could not list the worktrees; main left alone"
    return 0
  elif grep -qx 'branch refs/heads/main' <<<"$worktrees"; then
    # Read whole first: grep -q on git's pipe can exit while git still writes
    # (one write per 64 KiB, so many worktrees), and pipefail turns its
    # SIGPIPE into a "no".
    say "main is checked out in another worktree; its own pull moves it"
    return 0
  elif rebasing_main; then
    say "main is being rebased in another worktree; left alone"
    return 0
  else
    git update-ref -m "pull: fast-forward" refs/heads/main "$remote" "$before" || return 1
  fi
  n=$(git rev-list --count "$before..$remote")
  say "main ${before:0:7}..${remote:0:7} ($n commit$([ "$n" = 1 ] || echo s))${branch:+, on $branch}"
}

case ${1:-} in
  --every)
    every=${2:?usage: hack/pull.sh --every SECONDS}
    while :; do
      pull
      sleep "$every"
    done
    ;;
  "") pull ;;
  *)
    echo "usage: hack/pull.sh [--every SECONDS]" >&2
    exit 2
    ;;
esac
