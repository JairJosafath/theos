#!/usr/bin/env bash
# Kind helpers that keep the device's checkouts apart. They share one Docker
# daemon and one kubeconfig (.dev/kube/config of the main checkout), so:
#
#   hack/kind.sh checkout                 this checkout's image tag suffix, e.g. relay-2-1a2b3c
#   hack/kind.sh load CLUSTER IMAGE       load this checkout's build of IMAGE (IMAGE-<checkout>)
#                                         into Kind cluster CLUSTER under the name IMAGE
#   hack/kind.sh keep-context CMD ARGS..  run CMD (kind create cluster, kind export kubeconfig),
#                                         then put the kubeconfig's current context back
#
# A build tags IMAGE-<checkout> (c4factory/operator:dev-relay-2-1a2b3c), never
# the shared IMAGE, so another checkout's build can't replace it between the
# build and the load. The cluster's manifests keep naming IMAGE: the load tags
# the image IMAGE inside the cluster's nodes, where only this cluster's lease
# holder loads. kind sets the current context to the cluster it creates or
# exports; a context that silently moves sends plain kubectl calls of every
# other checkout to that cluster, so keep-context undoes it. Name the cluster
# with kubectl --context kind-<name> instead.
set -euo pipefail

KIND=${KIND:-kind}
KUBECTL=${KUBECTL:-kubectl}
CONTAINER_TOOL=${CONTAINER_TOOL:-docker}

# checkout_tag: the checkout's directory name and a hash of its path, as a tag
# suffix: two checkouts never share one, and the same checkout keeps its own.
checkout_tag() {
  local top base sum
  top=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
  base=$(basename "$top" | tr 'A-Z' 'a-z' | tr -c 'a-z0-9_.\n-' '-')
  sum=$(printf '%s' "$top" | sha1sum | cut -c1-6)
  printf '%s-%s\n' "${base:0:40}" "$sum"
}

# ref IMAGE: IMAGE as containerd names it, fully qualified (docker.io/...).
ref() {
  local first=${1%%/*}
  if [[ "$1" != */* ]]; then
    echo "docker.io/library/$1"
  elif [[ "$first" == *.* || "$first" == *:* || "$first" == localhost ]]; then
    echo "$1"
  else
    echo "docker.io/$1"
  fi
}

load() {
  local cluster=$1 image=$2 built node nodes
  # Untagged, IMAGE is IMAGE:latest; the checkout's tag goes after the tag.
  [[ "${image##*/}" == *:* ]] || image="$image:latest"
  built="$image-$(checkout_tag)"
  if ! "$CONTAINER_TOOL" image inspect "$built" >/dev/null 2>&1; then
    echo "kind.sh: no image $built: build this checkout's IMAGE with that tag first (make deploy, or docker build -t $built)" >&2
    return 1
  fi
  "$KIND" load docker-image "$built" --name "$cluster"
  nodes=$("$KIND" get nodes --name "$cluster")
  [ -n "$nodes" ] || { echo "kind.sh: Kind cluster $cluster has no nodes" >&2; return 1; }
  for node in $nodes; do
    "$CONTAINER_TOOL" exec "$node" ctr -n k8s.io images tag --force "$(ref "$built")" "$(ref "$image")" >/dev/null
    # The checkout's name was only the way in; IMAGE keeps the content.
    "$CONTAINER_TOOL" exec "$node" ctr -n k8s.io images rm "$(ref "$built")" >/dev/null
  done
  echo "Loaded $built into Kind cluster $cluster as $image"
}

keep_context() {
  local before rc=0
  before=$("$KUBECTL" config current-context 2>/dev/null) || before=
  "$@" || rc=$?
  if [ -n "$before" ]; then
    "$KUBECTL" config use-context "$before" >/dev/null
  else
    "$KUBECTL" config unset current-context >/dev/null 2>&1 || true
  fi
  return "$rc"
}

main() {
  case "${1:-}" in
    checkout) checkout_tag ;;
    load) [ $# -eq 3 ] || { echo "usage: hack/kind.sh load CLUSTER IMAGE" >&2; return 64; }; load "$2" "$3" ;;
    keep-context) shift; [ $# -gt 0 ] || { echo "usage: hack/kind.sh keep-context CMD [ARGS...]" >&2; return 64; }; keep_context "$@" ;;
    *) echo "usage: hack/kind.sh checkout | load CLUSTER IMAGE | keep-context CMD [ARGS...]" >&2; return 64 ;;
  esac
}

# Sourced by its test; run, it does the command.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
