#!/usr/bin/env bash
# Pulls each IMAGE into Docker, loads it into the Kind cluster CLUSTER and
# removes it from Docker again, for disk. Verify Release loads the agent images
# with it: they run in Sandboxes outside the chart, so its pull secret doesn't
# cover them.
#
# A pull or a load that fails is tried again, up to ATTEMPTS times in all, a
# DELAY apart: a runner's containerd can lose a snapshot while it extracts a
# layer (`failed to Lchown … no such file or directory`, #1408), which says
# nothing about the image, and the next try pulls it. The last failure fails.
#
#   hack/load-images.sh CLUSTER IMAGE...
#
#   ATTEMPTS  tries per pull and per load (default 3)
#   DELAY     seconds between tries (default 10)
set -euo pipefail

if [ $# -lt 2 ]; then
  echo "usage: hack/load-images.sh CLUSTER IMAGE..." >&2
  exit 2
fi
CLUSTER=$1
shift
ATTEMPTS=${ATTEMPTS:-3}
DELAY=${DELAY:-10}

# try WHAT CMD...: CMD, again after a failure, up to ATTEMPTS times.
try() {
  local what=$1 i
  shift
  for ((i = 1; ; i++)); do
    "$@" && return 0
    if [ "$i" -ge "$ATTEMPTS" ]; then
      echo "load-images: $what failed $i times, giving up" >&2
      return 1
    fi
    echo "load-images: $what failed (try $i of $ATTEMPTS), again in ${DELAY}s" >&2
    sleep "$DELAY"
  done
}

for image in "$@"; do
  try "docker pull $image" docker pull "$image"
  try "kind load $image" kind load docker-image --name "$CLUSTER" "$image"
  docker rmi "$image"
done
