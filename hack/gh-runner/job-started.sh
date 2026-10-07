#!/usr/bin/env bash
# The self-hosted runner's job-started hook (hack/gh-runner/Dockerfile sets
# ACTIONS_RUNNER_HOOK_JOB_STARTED to it): before every job it resets what an
# earlier job of the long-lived container may have left behind.
#
#   (no argument)   reset, as the hook
#   record          write the list of what the image put in the runner's home
#                   (the Dockerfile runs it once, at build time)
#
# The reset:
#   - makes the work directory writable, so actions/checkout's git clean can
#     empty it (a read-only directory, or one a container wrote as root, makes
#     it fail);
#   - removes from the runner's home everything the image didn't put there:
#     registry logins (~/.docker/config.json, ~/.config/helm/registry), the
#     kubeconfig (~/.kube), gh's login, and whatever else a job wrote.
# What stays: ~/.cache (the caches' volume), the runner itself (bin*,
# externals*, _diag, _work, its registration and .env/.path, all of which it
# may rewrite or update), and the image's own files.
#
# Best effort: what it can't reset is a warning in the job's log, not a failed
# job. The runner runs a hook with bash -e -o pipefail, so -e goes first.
# The runner runs a hook only when its path ends in .sh. Environment (for the tests):
#   GH_RUNNER_HOME_LIST   the list record writes (default /etc/gh-runner/home.list)
#   GH_RUNNER_WORK        the work directory (default workFolder of ~/.runner)
set +e -uo pipefail
export LC_ALL=C

home=${HOME:?}
list=${GH_RUNNER_HOME_LIST:-/etc/gh-runner/home.list}

warn() { echo "::warning::gh-runner job-started hook: $*"; }

# entries: every path under the home, relative to it, one per line, sorted;
# the caches and the runner's own top-level entries are left out with all
# under them. The runner's top-level scripts and templates are left out by
# pattern, so a file a self-update adds stays.
entries() {
  (cd "$home" && find . -mindepth 1 \( -path ./.cache \
    -o -path ./bin -o -path './bin.*' -o -path ./externals -o -path './externals.*' \
    -o -path ./_diag -o -path ./_work -o -path './_update*' \
    -o -path './.runner*' -o -path './.credentials*' -o -path ./.env -o -path ./.path \
    -o -path ./.service -o -path ./.setup_info \
    -o -regex '\./[^/]*\.\(sh\|template\)' \) -prune -o -printf '%P\n' 2>/dev/null) | sort
}

if [ "${1:-}" = record ]; then
  mkdir -p "$(dirname "$list")" && entries >"$list"
  exit
fi

# The work directory: directories a job made read-only get their write bit
# back, each before find reads it, so those under it are reached too; anything not the runner's (a container wrote it as root) becomes the
# runner's, through sudo.
work=${GH_RUNNER_WORK:-$(jq -r '.workFolder // empty' "$home/.runner" 2>/dev/null || true)}
if [ -n "$work" ] && [ -d "$work" ]; then
  find "$work" -type d ! -perm -u=rwx -exec chmod u+rwx {} \; 2>/dev/null
  if [ -n "$(find "$work" ! -user "$(id -u)" -print -quit 2>/dev/null)" ]; then
    sudo -n chown -R "$(id -u):$(id -g)" "$work" 2>/dev/null &&
      find "$work" -type d ! -perm -u=rwx -exec chmod u+rwx {} \; 2>/dev/null
  fi
  if [ -n "$(find "$work" \( ! -user "$(id -u)" -o -type d ! -perm -u=rwx \) -print -quit 2>/dev/null)" ]; then
    warn "$work keeps entries the runner can't delete; the checkout may fail to clean"
  fi
fi

# The home: everything the image's list doesn't name goes. Without the list
# nothing is known to be the image's, so nothing goes.
if [ ! -r "$list" ]; then
  warn "no $list; the runner's home is not reset"
  exit 0
fi
entries | comm -23 - "$list" | while IFS= read -r p; do
  [ -e "$home/$p" ] || [ -L "$home/$p" ] || continue # gone with its directory
  [ -L "$home/$p" ] || chmod -R u+rwX "$home/$p" 2>/dev/null
  rm -rf "${home:?}/$p" 2>/dev/null || sudo -n rm -rf "${home:?}/$p" 2>/dev/null ||
    warn "can't remove ~/$p"
done
exit 0
