#!/usr/bin/env bash
# Kind helpers that keep the device's checkouts apart. They share one Docker
# daemon and one kubeconfig (.dev/kube/config of the main checkout), so:
#
#   hack/kind.sh checkout                 this checkout's image tag suffix, e.g. relay-2-1a2b3c
#   hack/kind.sh load CLUSTER IMAGE       load this checkout's build of IMAGE (IMAGE-<checkout>)
#                                         into Kind cluster CLUSTER under the name IMAGE
#   hack/kind.sh keep-context CMD ARGS..  run CMD (kind create cluster, kind export kubeconfig),
#                                         then put the kubeconfig's current context back
#   hack/kind.sh registry-hosts CLUSTER   write containerd's hosts.toml for Gitea into CLUSTER's nodes
#   hack/kind.sh gitea-service CLUSTER    delete CLUSTER's Gitea Service when its ClusterIP isn't the fixed one
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

# The in-cluster Gitea as a node pulls from it. The operator pushes a
# Container's image to Gitea's registry at GITEA_HOST and writes that name,
# with its digest, into the Deployment it ships (docs/plans/k8s-deploy.md,
# decision 7). The node has no cluster DNS, so containerd resolves the name
# through a hosts.toml that points it at the Service's fixed ClusterIP
# (config/gitea/service.yaml; Kind's service range is 10.96.0.0/16, .1 is the
# API server and .10 kube-dns). This is the test environment's shortcut: a
# real cluster pulls from a registry with a real name and TLS.
GITEA_HOST=gitea.gitea.svc.cluster.local:3000
GITEA_CLUSTER_IP=10.96.0.30

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

# keep_context CMD ARGS..: run CMD, then put the current context back. Two
# checkouts at once would each restore what the other switched to, so the
# save, run and restore hold a lock beside the kubeconfig (the first file of
# KUBECONFIG; not kubectl's own config.lock). CMD runs with the lock's fd
# closed, so nothing it leaves running holds the lock.
keep_context() {
  local before rc=0 cfg lockfd
  cfg=${KUBECONFIG:-$HOME/.kube/config}
  cfg=${cfg%%:*}
  mkdir -p "$(dirname "$cfg")"
  exec {lockfd}>"$cfg.keep-context.lock"
  flock "$lockfd"
  before=$("$KUBECTL" config current-context 2>/dev/null) || before=
  "$@" {lockfd}>&- || rc=$?
  if [ -n "$before" ]; then
    "$KUBECTL" config use-context "$before" >/dev/null
  else
    "$KUBECTL" config unset current-context >/dev/null 2>&1 || true
  fi
  exec {lockfd}>&-
  return "$rc"
}

# registry_hosts CLUSTER: write /etc/containerd/certs.d/GITEA_HOST/hosts.toml
# into every node of CLUSTER, so a pull of GITEA_HOST/... goes to Gitea's
# ClusterIP over plain HTTP. It lives here and not in hack/kind.yaml: kind's
# extraMounts needs an absolute host path, and the legacy registry.mirrors
# patch is ignored once containerd reads certs.d (kind's default). Idempotent,
# so `make cluster` runs it whether or not the cluster existed.
registry_hosts() {
  local cluster=$1 node nodes dir="/etc/containerd/certs.d/$GITEA_HOST"
  nodes=$("$KIND" get nodes --name "$cluster")
  [ -n "$nodes" ] || { echo "kind.sh: Kind cluster $cluster has no nodes" >&2; return 1; }
  for node in $nodes; do
    printf 'server = "http://%s"\n\n[host."http://%s"]\n  capabilities = ["pull", "resolve"]\n' \
      "$GITEA_CLUSTER_IP:${GITEA_HOST##*:}" "$GITEA_CLUSTER_IP:${GITEA_HOST##*:}" |
      "$CONTAINER_TOOL" exec -i "$node" sh -c 'mkdir -p "$1" && cat >"$1/hosts.toml"' sh "$dir"
  done
  echo "Mapped $GITEA_HOST to http://$GITEA_CLUSTER_IP:${GITEA_HOST##*:} in Kind cluster $cluster"
}

# gitea_service CLUSTER: delete Service gitea/gitea of CLUSTER when its
# ClusterIP isn't GITEA_CLUSTER_IP, so the apply of config/gitea that follows
# creates it with the fixed one. spec.clusterIP is immutable: on a pool
# cluster whose Gitea predates the pin the Service holds an allocated IP, and
# the apply failed with "field is immutable" (#1679). A Service that already
# has the fixed IP, or none at all, is left alone.
gitea_service() {
  local cluster=$1 ip
  ip=$("$KUBECTL" --context "kind-$cluster" -n gitea get svc gitea --ignore-not-found -o 'jsonpath={.spec.clusterIP}')
  if [ -n "$ip" ] && [ "$ip" != "$GITEA_CLUSTER_IP" ]; then
    echo "Deleting Service gitea/gitea of Kind cluster $cluster: its ClusterIP $ip is not the fixed $GITEA_CLUSTER_IP (immutable; the apply recreates it)"
    "$KUBECTL" --context "kind-$cluster" -n gitea delete svc gitea
  fi
}

main() {
  case "${1:-}" in
    checkout) checkout_tag ;;
    load) [ $# -eq 3 ] || { echo "usage: hack/kind.sh load CLUSTER IMAGE" >&2; return 64; }; load "$2" "$3" ;;
    keep-context) shift; [ $# -gt 0 ] || { echo "usage: hack/kind.sh keep-context CMD [ARGS...]" >&2; return 64; }; keep_context "$@" ;;
    registry-hosts) [ $# -eq 2 ] || { echo "usage: hack/kind.sh registry-hosts CLUSTER" >&2; return 64; }; registry_hosts "$2" ;;
    gitea-service) [ $# -eq 2 ] || { echo "usage: hack/kind.sh gitea-service CLUSTER" >&2; return 64; }; gitea_service "$2" ;;
    *) echo "usage: hack/kind.sh checkout | load CLUSTER IMAGE | keep-context CMD [ARGS...] | registry-hosts CLUSTER | gitea-service CLUSTER" >&2; return 64 ;;
  esac
}

# Sourced by its test; run, it does the command.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
