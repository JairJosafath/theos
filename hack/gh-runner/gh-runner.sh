#!/usr/bin/env bash
# Self-hosted GitHub Actions runners for this repository, in containers on this
# device's Docker (hack/gh-runner/Dockerfile). The workflows run on them when
# the repository variable RUNS_ON is self-hosted (docs/dev/testenv.md).
#
#   hack/gh-runner/gh-runner.sh up      build the image, register each runner once and start RUNNERS of them (make gh-runner RUNNERS=N)
#   hack/gh-runner/gh-runner.sh down    stop them all, remove them from GitHub, drop their registrations (make gh-runner-down)
#   hack/gh-runner/gh-runner.sh status  their containers and their status on GitHub; exit 1 unless all are online (make gh-runner-status)
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

say() { echo "gh-runner: $*" >&2; }

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

# Its labels as GitHub lists them, without the ones the runner adds itself.
own_labels='.labels | map(.name) | map(select(. != "self-hosted" and . != "linux" and . != "x64")) | sort | join(",")'

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
  local since
  since=$(docker inspect -f '{{.State.StartedAt}}' "$container") || return 1
  docker logs --since "$since" "$container" 2>&1 | grep -q 'Listening for Jobs' &&
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

up() {
  if ! [[ $runners =~ ^[1-9][0-9]*$ ]]; then
    say "RUNNERS is $runners; it takes a number from 1"
    exit 64
  fi
  # The image is built again, and the containers made again, only when what
  # they are made from changed: a cached build still gets a new image ID.
  local rev i
  rev=$({ cat "$here/Dockerfile" "$here/entrypoint.sh" "$here/job-started.sh"; id -u; id -g; } | sha256sum | cut -c1-12)
  if [ "$(docker image inspect -f "$rev_label" "$image" 2>/dev/null)" != "$rev" ]; then
    say "building $image"
    docker build -q -t "$image" --label "c4factory.gh-runner.rev=$rev" \
      --build-arg RUNNER_UID="$(id -u)" --build-arg RUNNER_GID="$(id -g)" "$here" >/dev/null
  fi
  docker volume create "$cache" >/dev/null
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
  migrate
  for i in $(indices); do down_one "$i"; done
  say "the caches (volumes $prefix-*-toolcache and $cache) and $base_work stay"
}

status() {
  local i c r ok=true all
  all=$(indices)
  if [ -z "$all" ]; then
    echo "no runners on this device; make gh-runner RUNNERS=N starts N"
    return 1
  fi
  for i in $all; do
    use "$i"
    c=$(docker ps -a --format '{{.Names}}\t{{.Status}}\t{{.Image}}' | awk -F'\t' -v n="$container" '$1 == n {print $2 " (" $3 ")"}')
    echo "container: $container ${c:-does not exist}"
    r=$(github_runner) || { echo "github: can't list the runners of $repo"; return 1; }
    echo "github: ${r:-$name is not registered with $repo}"
    [[ $r == *" online "* ]] || ok=false
  done
  $ok
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  status) status ;;
  *) sed -n '2,/^set /{/^set /d;s/^# \{0,1\}//;p}' "$0"; exit 64 ;;
esac
