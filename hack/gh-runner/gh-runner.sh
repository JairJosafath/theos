#!/usr/bin/env bash
# A self-hosted GitHub Actions runner for this repository, in a container on
# this device's Docker (hack/gh-runner/Dockerfile). The workflows run on it when
# the repository variable RUNS_ON is self-hosted (docs/dev/testenv.md).
#
#   hack/gh-runner/gh-runner.sh up      build the image, register once and start the runner (make gh-runner)
#   hack/gh-runner/gh-runner.sh down    stop it, remove it from GitHub, drop its registration (make gh-runner-down)
#   hack/gh-runner/gh-runner.sh status  its container and its status on GitHub; exit 1 unless online (make gh-runner-status)
#
# Jobs reach the device's Docker through its socket, on the host's network, so
# a Kind cluster a job makes answers on 127.0.0.1. The work directory has the
# same path on the host and in the container, so a job's bind mounts resolve.
# The registration, the tool cache and the Go and npm caches live on volumes;
# the registration survives docker restart, a reboot and a new image. Tokens
# come from the device's gh login and are never stored.
#
#   C4_GITHUB_REPO   the repository (default JairJosafath/c4factory)
#   GH_RUNNER_NAME   the runner's name on GitHub (default c4factory-<hostname>)
#   GH_RUNNER_WORK   the work directory (default ~/.cache/c4factory/gh-runner/work)
set -euo pipefail

repo=${C4_GITHUB_REPO:-JairJosafath/c4factory}
name=${GH_RUNNER_NAME:-c4factory-$(hostname -s)}
work=${GH_RUNNER_WORK:-${XDG_CACHE_HOME:-$HOME/.cache}/c4factory/gh-runner/work}
sock=/var/run/docker.sock
image=c4factory/gh-runner:dev
container=c4factory-gh-runner
volume=c4factory-gh-runner
here=$(cd "$(dirname "$0")" && pwd)
rev_label='{{index .Config.Labels "c4factory.gh-runner.rev"}}'

say() { echo "gh-runner: $*" >&2; }

# token KIND: a registration or remove token for the repository.
token() { gh api -X POST "repos/$repo/actions/runners/$1-token" --jq .token; }

# github_runner [JQ]: the runner in GitHub's list, as a line or as JQ picks it;
# empty when GitHub doesn't know it, failing when GitHub can't be asked.
github_runner() {
  local pick=${1:-'"\(.name) \(.status) busy=\(.busy) labels=\(.labels | map(.name) | join(","))"'}
  gh api --paginate "repos/$repo/actions/runners?per_page=100" \
    --jq ".runners[] | select(.name == \"$name\") | $pick"
}

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
  docker run --rm -e ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=false \
    -e RUNNER_TOKEN -e RUNNER_URL="https://github.com/$repo" -e RUNNER_NAME="$name" \
    -e RUNNER_LABELS=c4factory -e RUNNER_WORKDIR="$work" \
    -v "$volume-config:/runner-state" -v "$work:$work" "$image" "$@"
}

up() {
  mkdir -p "$work"
  # The image is built again, and the container made again, only when what
  # they are made from changed: a cached build still gets a new image ID.
  local rev
  rev=$({ cat "$here/Dockerfile" "$here/entrypoint.sh"; id -u; id -g; } | sha256sum | cut -c1-12)
  if [ "$(docker image inspect -f "$rev_label" "$image" 2>/dev/null)" != "$rev" ]; then
    say "building $image"
    docker build -q -t "$image" --label "c4factory.gh-runner.rev=$rev" \
      --build-arg RUNNER_UID="$(id -u)" --build-arg RUNNER_GID="$(id -g)" "$here" >/dev/null
  fi
  local v
  for v in config toolcache cache; do docker volume create "$volume-$v" >/dev/null; done

  local registered=false known
  if oneshot registered; then
    registered=true
    # GitHub deletes a runner that stayed offline for 14 days; register again then.
    if known=$(github_runner) && [ -z "$known" ]; then
      say "GitHub no longer lists $name; registering it again"
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
      -v "$volume-config:/runner-state" -v "$volume-toolcache:/opt/hostedtoolcache" \
      -v "$volume-cache:/home/runner/.cache" -v "$work:$work" \
      "$image" >/dev/null
  else
    docker start "$container" >/dev/null
  fi

  # Until this container listens and GitHub has seen it: a session the last
  # one left open can hold it off for a minute or two.
  local _ since
  since=$(docker inspect -f '{{.State.StartedAt}}' "$container")
  for _ in $(seq 1 90); do
    docker logs --since "$since" "$container" 2>&1 | grep -q 'Listening for Jobs' &&
      status >/dev/null 2>&1 && break
    sleep 2
  done
  status
}

down() {
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
  docker volume rm "$volume-config" >/dev/null 2>&1 || true
  say "$name is removed; the caches (volumes $volume-toolcache and $volume-cache) and $work stay"
}

status() {
  local c r
  c=$(docker ps -a --format '{{.Names}}\t{{.Status}}\t{{.Image}}' | awk -F'\t' -v n="$container" '$1 == n {print $2 " (" $3 ")"}')
  echo "container: $container ${c:-does not exist}"
  r=$(github_runner) || { echo "github: can't list the runners of $repo"; return 1; }
  echo "github: ${r:-$name is not registered with $repo}"
  [[ $r == *" online "* ]]
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  status) status ;;
  *) sed -n '2,/^set /{/^set /d;s/^# \{0,1\}//;p}' "$0"; exit 64 ;;
esac
