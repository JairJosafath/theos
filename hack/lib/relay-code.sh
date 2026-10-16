# Sourced by hack/relay.sh and hack/relay-watch.sh (#1126).
#
# The relay runs origin/main's code, whatever branch the main checkout has. The
# watch and every worker run their scripts from a worktree of their own,
# .claude/worktrees/relay-code, kept detached at origin/main, and keep their
# state (.relay/, the workers' worktrees) in the checkout they were started
# from, which C4_RELAY_REPO names to the copy in relay-code. A script started
# from the checkout moves to its copy in relay-code at its first pull; after
# each pull, one whose copy there changed restarts on it. The scripts read
# C4_RELAY_REPO once and unset it, so no session inherits it, and pass it to
# the scripts they start.
#
# Each script sets CODE (the checkout it runs from), REPO (C4_RELAY_REPO, else
# CODE), DIR ($REPO/.relay), log and code_sum before calling these.

# relay_code: the worktree the relay runs its code from.
relay_code() { echo "$REPO/.claude/worktrees/relay-code"; }

# relay_code_sync: move relay-code to origin/main, adding the worktree when it
# is missing, with every tracked file as origin/main has it (an edit made there
# is undone), under a lock, as the watch and the workers share it. It doesn't
# fetch: hack/pull.sh, run just before, does. Exit 1: it couldn't, and stderr
# says why.
relay_code_sync() {
  local wt
  wt=$(relay_code)
  [ -e "$REPO/.git" ] || { echo "$REPO is no git checkout" >&2; return 1; }
  mkdir -p "$DIR" || return 1
  (
    flock -w 60 7 || { echo "another sync held $DIR/relay-code.lock for a minute" >&2; exit 1; }
    if [ ! -e "$wt/.git" ]; then
      # -f: a worktree deleted by hand is still registered.
      git -C "$REPO" worktree add -q -f --detach "$wt" origin/main || exit 1
    fi
    git -C "$wt" checkout -q -f --detach origin/main
  ) 7>"$DIR/relay-code.lock"
}

# relay_code_stale: move relay-code to origin/main; exit 0 when this script
# should restart on its copy there: it runs from elsewhere, or that copy is no
# longer the code it started with (code_sum isn't CODE_SUM). Exit 1: carry on
# where it runs; a sync that failed is logged.
relay_code_stale() {
  local out
  if ! out=$(relay_code_sync 2>&1); then
    log "could not move $(relay_code) to origin/main: ${out//$'\n'/; }; running the code in $CODE"
    return 1
  fi
  [ "$CODE" != "$(relay_code)" ] || [ "$(code_sum)" != "$CODE_SUM" ]
}
