#!/usr/bin/env bash
# Self-hosted GitHub Actions runners for this repository, in containers on this
# device's Docker (hack/gh-runner/Dockerfile). The workflows run on them when
# the repository variable RUNS_ON is self-hosted (docs/dev/testenv.md).
#
#   hack/gh-runner/gh-runner.sh up      build the image, register each runner once and start RUNNERS of them (make gh-runner RUNNERS=N)
#   hack/gh-runner/gh-runner.sh down    stop them all, remove them from GitHub, drop their registrations (make gh-runner-down)
#   hack/gh-runner/gh-runner.sh status  whether Docker answers, their containers and their status on GitHub,
#                                       and what to do; exit 1 unless all are online (make gh-runner-status)
#   hack/gh-runner/gh-runner.sh keep    start the runners the last up recorded whose container is gone or not up
#   hack/gh-runner/gh-runner.sh install the keeper: a systemd user timer that runs keep every 2 minutes for this
#                                       checkout; without systemd, a crontab line
#   hack/gh-runner/gh-runner.sh uninstall  remove the keeper
#
# Runner i is container c4factory-gh-runner-i, named c4factory-<hostname>-i on
# GitHub, with its registration and tool cache on volumes of its own and its own
# work directory. The Go, npm and golangci-lint caches are one volume they
# share: those tools lock what they write. The Makefile's tools (LOCALBIN) are
# kept there too, out of the checkout a job cleans. Only runner 1 carries the label
# c4factory-kind, which the jobs that make a Kind cluster ask for, so a device
# runs one of them at a time. up with fewer runners than are there removes the
# extra ones; a runner from before there were several (container
# c4factory-gh-runner) is removed and registered again as runner 1.
#
# Jobs reach the device's Docker through its socket, on the host's network, so
# a Kind cluster a job makes answers on 127.0.0.1. The work directory has the
# same path on the host and in the container, so a job's bind mounts resolve.
# The registrations survive docker restart, a reboot and a new image. Tokens
# come from the device's gh login and are never stored.
#
# up records RUNNERS, with the names, work directories and repository it used,
# in ~/.local/state/c4factory/gh-runner/runners; down removes the record. keep
# starts what the record names and nothing else: it never changes the count,
# starts nothing after down, and never recreates a container that is up, which
# would kill its job. It is for Docker Desktop recreating its data disk, which
# takes the containers, volumes and image with it, so --restart unless-stopped
# has nothing to restart. With Docker not reachable it starts nothing and logs
# that once. The record and the keeper's log (keep.log, next to it) are under
# $HOME, not $XDG_STATE_HOME, which ./dev points into .dev/; the keeper runs
# without ./dev run, and the work directories come from the record. up, down
# and keep take turns on a lock; keep skips its turn while another runs.
#
#   RUNNERS          how many runners up starts (default 1)
#   C4_GITHUB_REPO   the repository (default JairJosafath/c4factory)
#   GH_RUNNER_NAME   the runners' names on GitHub, before -i (default c4factory-<hostname>)
#   GH_RUNNER_WORK   their work directories, each in a subdirectory i (default ~/.cache/c4factory/gh-runner/work)
set -euo pipefail

repo=${C4_GITHUB_REPO:-JairJosafath/c4factory}
runners=${RUNNERS:-1}
base_name=${GH_RUNNER_NAME:-c4factory-$(hostname -s)}
base_work=${GH_RUNNER_WORK:-${XDG_CACHE_HOME:-$HOME/.cache}/c4factory/gh-runner/work}
sock=/var/run/docker.sock
image=c4factory/gh-runner:dev
prefix=c4factory-gh-runner
cache=$prefix-cache
kind_label=c4factory-kind
here=$(cd "$(dirname "$0")" && pwd)
rev_label='{{index .Config.Labels "c4factory.gh-runner.rev"}}'
state=$HOME/.local/state/c4factory/gh-runner
record=$state/runners
unit=c4factory-gh-runner-keep
units=$HOME/.config/systemd/user
cron_tag="# c4factory gh-runner keeper"
stamp= # keep's lines carry the time: they go to a log

say() { echo "${stamp:+$(date '+%F %T') }gh-runner: $*" >&2; }

# lock [-n]: wait for the turn of up, down and keep, or with -n fail when
# another holds it.
lock() {
  mkdir -p "$state"
  exec 9>"$state/lock"
  flock "$@" 9
}

# read_record: the settings the last up recorded, in place of the
# environment's; fails when there is none.
read_record() {
  local k v
  [ -s "$record" ] || return 1
  runners=
  while IFS='=' read -r k v; do
    case $k in
      RUNNERS) runners=$v ;;
      GH_RUNNER_NAME) base_name=$v ;;
      GH_RUNNER_WORK) base_work=$v ;;
      C4_GITHUB_REPO) repo=$v ;;
    esac
  done <"$record"
  [[ $runners =~ ^[1-9][0-9]*$ ]]
}

# use I: the runner I acts on: its container, volumes, name, work directory
# and labels. use legacy is the single runner from before there were several.
use() {
  if [ "$1" = legacy ]; then
    container=$prefix name=$base_name work=$base_work labels=c4factory
  else
    container=$prefix-$1 name=$base_name-$1 work=$base_work/$1 labels=c4factory
    [ "$1" = 1 ] && labels=c4factory,$kind_label
  fi
  config=$container-config toolcache=$container-toolcache
}

# indices: the runners this device has, by a container or a registration volume.
indices() {
  {
    docker ps -a --format '{{.Names}}' | sed -n "s/^$prefix-\([0-9][0-9]*\)\$/\1/p"
    docker volume ls --format '{{.Name}}' | sed -n "s/^$prefix-\([0-9][0-9]*\)-config\$/\1/p"
  } | sort -n -u
}

# token KIND: a registration or remove token for the repository.
token() { gh api -X POST "repos/$repo/actions/runners/$1-token" --jq .token; }

# github_runner [JQ]: the runner in GitHub's list, as a line or as JQ picks it;
# empty when GitHub doesn't know it, failing when GitHub can't be asked.
github_runner() {
  local pick=${1:-'"\(.name) \(.status) busy=\(.busy) labels=\(.labels | map(.name) | join(","))"'}
  gh api --paginate "repos/$repo/actions/runners?per_page=100" \
    --jq ".runners[] | select(.name == \"$name\") | $pick"
}

# Its labels as GitHub lists them, without the ones the runner adds itself
# (self-hosted, Linux, X64), which GitHub marks read-only.
own_labels='[.labels[] | select(.type == "custom") | .name] | sort | join(",")'

# remove_container: stop the runner so it closes its session on GitHub (a
# killed one leaves it open, and the next start waits minutes for it to
# expire), then remove the container.
remove_container() {
  docker inspect "$container" >/dev/null 2>&1 || return 1
  docker stop "$container" >/dev/null
  docker rm "$container" >/dev/null
}

# oneshot CMD: run the image once on the registration volume (configure, remove, registered).
oneshot() {
  # Made here first: a bind mount Docker has to make itself is root's, and so
  # is every missing parent (.dev/cache/c4factory, where make check records).
  mkdir -p "$work"
  docker run --rm -e ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=false \
    -e RUNNER_TOKEN -e RUNNER_URL="https://github.com/$repo" -e RUNNER_NAME="$name" \
    -e RUNNER_LABELS="$labels" -e RUNNER_WORKDIR="$work" \
    -v "$config:/runner-state" -v "$work:$work" "$image" "$@"
}

# up_one I REV: register runner I unless it is, and start it.
up_one() {
  use "$1"
  local rev=$2
  mkdir -p "$work"
  docker volume create "$config" >/dev/null
  docker volume create "$toolcache" >/dev/null

  local registered=false known
  if oneshot registered; then
    registered=true
    # GitHub deletes a runner that stayed offline for 14 days; register again
    # then, and when its labels are not the ones it should carry.
    if known=$(github_runner "$own_labels") && [ "$known" != "$labels" ]; then
      if [ -z "$known" ]; then
        say "GitHub no longer lists $name; registering it again"
      else
        say "$name carries the labels $known, not $labels; registering it again"
      fi
      registered=false
    fi
  fi
  if ! $registered; then
    remove_container || true
    say "registering $name with $repo"
    export RUNNER_TOKEN
    RUNNER_TOKEN=$(token registration)
    oneshot configure
    unset RUNNER_TOKEN
  fi

  # A new image or a new registration needs a new container; otherwise the
  # running one stays, and a stopped one starts.
  if [ "$(docker inspect -f "$rev_label" "$container" 2>/dev/null)" != "$rev" ]; then
    remove_container || true
    say "starting container $container"
    docker run -d --name "$container" --restart unless-stopped --init --stop-timeout 60 \
      --network host --group-add "$(stat -c %g "$sock")" -v "$sock:/var/run/docker.sock" \
      -v "$config:/runner-state" -v "$toolcache:/opt/hostedtoolcache" \
      -v "$cache:/home/runner/.cache" -v "$work:$work" \
      "$image" >/dev/null
  else
    docker start "$container" >/dev/null
  fi
}

# online I: runner I's container listens for jobs and GitHub sees it online.
online() {
  use "$1"
  local since logs
  since=$(docker inspect -f '{{.State.StartedAt}}' "$container") || return 1
  # The logs are read whole: grep -q on docker's pipe can exit while docker
  # still writes, and pipefail turns its SIGPIPE into a "no".
  logs=$(docker logs --since "$since" "$container" 2>&1) || return 1
  [[ $logs == *'Listening for Jobs'* ]] &&
    [[ $(github_runner 2>/dev/null) == *" online "* ]]
}

# down_one I: stop runner I and remove it from GitHub; its tool cache stays
# unless DROP_CACHE is true.
down_one() {
  use "$1"
  remove_container && say "removed container $container"
  local removed=false id
  if docker image inspect "$image" >/dev/null 2>&1 && oneshot registered; then
    export RUNNER_TOKEN
    RUNNER_TOKEN=$(token remove)
    oneshot remove && removed=true
    unset RUNNER_TOKEN
  fi
  if ! $removed; then
    id=$(github_runner .id)
    if [ -n "$id" ]; then
      say "removing $name (id $id) through the API"
      gh api -X DELETE "repos/$repo/actions/runners/$id"
    fi
  fi
  docker volume rm "$config" >/dev/null 2>&1 || true
  if ${DROP_CACHE:-false}; then docker volume rm "$toolcache" >/dev/null 2>&1 || true; fi
  say "$name is removed"
}

# migrate: the single runner from before there were several goes; runner 1
# takes its place.
migrate() {
  use legacy
  if docker inspect "$container" >/dev/null 2>&1 || docker volume inspect "$config" >/dev/null 2>&1; then
    say "replacing $name, the runner from before there were several, with runner 1"
    DROP_CACHE=true down_one legacy
  fi
}

# build: set rev, the revision of what the image is made from, and build the
# image again only when it changed: a cached build still gets a new image ID,
# and a new image makes up make the containers again.
build() {
  rev=$({ cat "$here/Dockerfile" "$here/entrypoint.sh" "$here/job-started.sh"; id -u; id -g; } | sha256sum | cut -c1-12)
  if [ "$(docker image inspect -f "$rev_label" "$image" 2>/dev/null)" != "$rev" ]; then
    say "building $image"
    docker build -q -t "$image" --label "c4factory.gh-runner.rev=$rev" \
      --build-arg RUNNER_UID="$(id -u)" --build-arg RUNNER_GID="$(id -g)" "$here" >/dev/null
  fi
  docker volume create "$cache" >/dev/null
}

up() {
  if ! [[ $runners =~ ^[1-9][0-9]*$ ]]; then
    say "RUNNERS is $runners; it takes a number from 1"
    exit 64
  fi
  local rev i
  lock
  printf '%s\n' "RUNNERS=$runners" "GH_RUNNER_NAME=$base_name" "GH_RUNNER_WORK=$base_work" \
    "C4_GITHUB_REPO=$repo" >"$record"
  build
  migrate
  for i in $(indices); do
    if [ "$i" -gt "$runners" ]; then DROP_CACHE=true down_one "$i"; fi
  done
  for i in $(seq 1 "$runners"); do up_one "$i" "$rev"; done

  # Until each container listens and GitHub has seen it: a session the last
  # one left open can hold it off for a minute or two.
  local _
  for i in $(seq 1 "$runners"); do
    for _ in $(seq 1 90); do
      online "$i" && break
      sleep 2
    done
  done
  status
}

down() {
  local i
  lock
  rm -f "$record"
  migrate
  for i in $(indices); do down_one "$i"; done
  say "the caches (volumes $prefix-*-toolcache and $cache) and $base_work stay; the keeper starts nothing until the next up"
}

# keep: start each runner the record names whose container is gone or not up,
# from the image as the checkout makes it; a container that is up stays, even
# from an older image. Docker not reachable is logged once, until it answers.
keep() {
  local i rev up_now missing=
  stamp=1
  lock -n || return 0
  read_record || return 0
  if ! docker info >/dev/null 2>&1; then
    if [ ! -e "$state/docker-down" ]; then
      touch "$state/docker-down"
      say "Docker is not reachable; starting nothing until it answers (make gh-runner-status)"
    fi
    return 0
  fi
  if [ -e "$state/docker-down" ]; then
    rm -f "$state/docker-down"
    say "Docker answers again"
  fi
  up_now=$(docker ps -a --format '{{.Names}}\t{{.Status}}' | awk -F'\t' '$2 ~ /^Up/ {print $1}')
  for i in $(seq 1 "$runners"); do
    grep -qxF "$prefix-$i" <<<"$up_now" || missing+=" $i"
  done
  [ -n "$missing" ] || return 0
  say "runner(s)$missing of $runners not up; starting them"
  build
  for i in $missing; do up_one "$i" "$rev"; done
}

# has_systemd: whether this user has a systemd user manager to run the keeper.
has_systemd() { command -v systemctl >/dev/null && systemctl --user show-environment >/dev/null 2>&1; }

# in_worktree: whether this checkout is a linked git worktree. The keeper runs
# the main checkout's script: a worktree changes branches, and goes.
in_worktree() {
  local git common
  git=$(git -C "$here" rev-parse --absolute-git-dir 2>/dev/null) || return 1
  common=$(cd "$here" && cd "$(git rev-parse --git-common-dir)" && pwd)
  [ "$git" != "$common" ]
}

# without_cron: remove the keeper's crontab line; fails when there is none.
without_cron() {
  local tab
  command -v crontab >/dev/null || return 1
  tab=$(crontab -l 2>/dev/null) || return 1
  grep -qF "$cron_tag" <<<"$tab" || return 1
  { grep -vF "$cron_tag" <<<"$tab" || true; } | crontab -
}

# docker_group: warn when the systemd user manager lacks the docker group the
# user has. A process keeps the groups it started with, so a manager that
# started before the user joined docker runs a keeper that can't reach Docker.
docker_group() {
  local gid pid
  gid=$(getent group docker | cut -d: -f3) || return 0
  [ -n "$gid" ] && [[ " $(id -G "$(id -un)") " == *" $gid "* ]] || return 0
  pid=$(pgrep -u "$(id -u)" -x systemd | head -n 1) && [ -n "$pid" ] || return 0
  [[ " $(awk '/^Groups:/ { $1 = ""; print }' "/proc/$pid/status" 2>/dev/null) " == *" $gid "* ]] && return 0
  say "the systemd user manager lacks the docker group, so the keeper can't reach Docker: it started before $(id -un) joined it. Restart it: log out and in again, or on WSL run wsl --shutdown from Windows. Check: grep Groups /proc/\$(pgrep -u \"\$USER\" -x systemd)/status"
}

install_keeper() {
  in_worktree && { say "install the keeper from the main checkout, not a worktree"; exit 1; }
  mkdir -p "$state"
  if has_systemd; then
    mkdir -p "$units"
    # A oneshot run of keep, under the installer's PATH (the user manager's
    # lacks gh), and without ./dev run.
    cat >"$units/$unit.service" <<EOF
[Unit]
Description=C4 Factory GitHub Actions runner keeper ($here)

[Service]
Type=oneshot
Environment="PATH=$PATH"
ExecStart="$here/gh-runner.sh" keep
TimeoutStartSec=30min
StandardOutput=append:$state/keep.log
StandardError=append:$state/keep.log
EOF
    cat >"$units/$unit.timer" <<EOF
[Unit]
Description=Start the C4 Factory GitHub Actions runners that are gone, every 2 minutes

[Timer]
OnCalendar=*:0/2
AccuracySec=10s

[Install]
WantedBy=timers.target
EOF
    without_cron || true
    systemctl --user daemon-reload && systemctl --user enable --now "$unit.timer" ||
      { say "systemctl failed"; exit 1; }
    # Linger starts the user's manager, and so the timer, at boot without a login.
    loginctl enable-linger "$(id -un)" 2>/dev/null ||
      say "loginctl enable-linger failed; the keeper starts at the first login, not at boot"
    say "keeper installed as $units/$unit.timer: keep every 2 minutes; log: $state/keep.log"
    docker_group
  else
    command -v crontab >/dev/null || { say "neither systemd nor crontab here"; exit 1; }
    { crontab -l 2>/dev/null | grep -vF "$cron_tag" || true
      echo "*/2 * * * * PATH=$(printf %q "$PATH") $(printf %q "$here/gh-runner.sh") keep >>$(printf %q "$state/keep.log") 2>&1 $cron_tag"
    } | crontab - || { say "crontab failed"; exit 1; }
    say "keeper installed in the crontab: keep every 2 minutes; log: $state/keep.log"
  fi
}

uninstall_keeper() {
  if [ -e "$units/$unit.timer" ]; then
    has_systemd && systemctl --user disable --now "$unit.timer" ||
      { say "systemctl --user failed; $units/$unit.timer stays"; exit 1; }
    rm -f "$units/$unit.timer" "$units/$unit.service"
    systemctl --user daemon-reload
    say "removed the keeper's timer"
  fi
  if without_cron; then say "removed the keeper's crontab line"; fi
}

# status: whether this device's Docker answers, then each runner's container
# and how GitHub sees it, with what to do when one is down. The runners run on
# the device's Docker: on WSL that is Docker Desktop with WSL integration on
# for this distro, and they stop whenever it does (DEVELOPMENT.md).
status() {
  local i c r ok=true all where=${WSL_DISTRO_NAME:+WSL distro $WSL_DISTRO_NAME}
  if ! docker info >/dev/null 2>&1; then
    echo "docker: not reachable from ${where:-this host}; the runners stop with it"
    # GitHub still says which of this device's runners it sees.
    r=$(gh api --paginate "repos/$repo/actions/runners?per_page=100" \
      --jq ".runners[] | select(.name | startswith(\"$base_name-\")) | \"\(.name) \(.status) busy=\(.busy)\"") ||
      r="can't list the runners of $repo"
    echo "github: ${r:-no runner named $base_name-N is registered with $repo}" | sed '2,$s/^/github: /'
    echo "fix: start Docker Desktop, with WSL integration on for ${WSL_DISTRO_NAME:-this distro} (Settings > Resources > WSL integration); the runners restart with it. Then make gh-runner-status again (DEVELOPMENT.md)."
    return 1
  fi
  echo "docker: reachable from ${where:-this host}"
  all=$(indices)
  if [ -z "$all" ]; then
    echo "no runners on this device; make gh-runner RUNNERS=N starts N"
    return 1
  fi
  for i in $all; do
    use "$i"
    c=$(docker ps -a --format '{{.Names}}\t{{.Status}}\t{{.Image}}' | awk -F'\t' -v n="$container" '$1 == n {print $2 ", " $3}')
    case $c in
      Up*) echo "container: $container up ($c)" ;;
      '') echo "container: $container does not exist"; ok=false ;;
      *) echo "container: $container down ($c)"; ok=false ;;
    esac
    r=$(github_runner) || { echo "github: can't list the runners of $repo"; return 1; }
    echo "github: ${r:-$name is not registered with $repo}"
    [[ $r == *" online "* ]] || ok=false
  done
  $ok && return 0
  echo "fix: make gh-runner RUNNERS=$(wc -w <<<"$all") starts the containers that are down and registers again the runners GitHub lost; a container that is up while GitHub sees it offline says why in docker logs --tail 50 NAME (DEVELOPMENT.md)."
  return 1
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  status) status ;;
  keep) keep ;;
  install) install_keeper ;;
  uninstall) uninstall_keeper ;;
  *) sed -n '2,/^set /{/^set /d;s/^# \{0,1\}//;p}' "$0"; exit 64 ;;
esac
