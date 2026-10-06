#!/usr/bin/env bash
# Brings a stopped factory back: the Docker daemon, the Kind node, the API, and
# the factory's pods. After a stop, pods start before the node's service network
# is back, crash on "dial tcp 10.96.0.1:443: i/o timeout", and then sit in
# back-off for up to five minutes; this restarts them once the network is up,
# then waits for every Deployment and runs the doctor.
#   make up     (make down stops it again; both work on KIND_CLUSTER, one cluster of the pool)
# For a human: agents never lease, build or deploy to a Kind cluster.
set -euo pipefail

# Default: the pool cluster this checkout leased (hack/lib/github.sh cluster name), else c4factory.
CLUSTER="${KIND_CLUSTER:-$("$(dirname "${BASH_SOURCE[0]}")/lib/github.sh" cluster name 2>/dev/null || echo c4factory)}"
NODE="${CLUSTER}-control-plane"
KUBECTL=(kubectl --context "kind-${CLUSTER}" --request-timeout=10s)
NAMESPACES=(kube-system local-path-storage gitea agent-sandbox-system c4-factory-system c4-factory-ui)

# Progress goes to fd 3, so wait_for can silence the commands it runs.
exec 3>&1
say() { printf '\033[1m==> %s\033[0m\n' "$*"; }

# wait_for <seconds> <description> <command...>
wait_for() {
  local seconds=$1 what=$2
  shift 2
  local deadline=$((SECONDS + seconds))
  until "$@" >/dev/null 2>&1; do
    if ((SECONDS > deadline)); then
      echo "Gave up waiting for $what after ${seconds}s" >&2
      return 1
    fi
    sleep 3
  done
}

if ! docker info >/dev/null 2>&1; then
  echo "The Docker daemon is not running; start Docker Desktop (or the Docker service) and try again" >&2
  exit 1
fi

started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
state=$(docker inspect -f '{{.State.Status}}' "$NODE" 2>/dev/null) || state=missing
if [[ "$state" == missing ]]; then
  echo "Kind cluster $CLUSTER does not exist: a human builds one with C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire; agents never do" >&2
  exit 1
fi
if [[ "$state" != running ]]; then
  say "Starting Kind node $NODE"
  docker start "$NODE" >/dev/null
fi

# A fresh .dev/ (./dev nuke, a new checkout) has no kubeconfig for the cluster.
if ! kubectl config get-contexts "kind-${CLUSTER}" >/dev/null 2>&1; then
  say "Restoring the kubeconfig for kind-${CLUSTER}"
  "$(dirname "$0")/kind.sh" keep-context kind export kubeconfig --name "$CLUSTER" >/dev/null
fi

say "Waiting for the Kubernetes API"
wait_for 180 "the API" "${KUBECTL[@]}" get --raw /readyz

# Right after a start, the API still shows the node and pods as they were before
# the stop. Wait until the kubelet has renewed its lease since, before trusting it.
fresh_heartbeat() {
  local beat
  beat=$("${KUBECTL[@]}" -n kube-node-lease get lease "$NODE" -o jsonpath='{.spec.renewTime}')
  [[ -n "$beat" && "$beat" > "$started" ]]
}
wait_for 180 "the kubelet's first heartbeat" fresh_heartbeat
wait_for 120 "the node" "${KUBECTL[@]}" wait --for=condition=Ready "node/$NODE" --timeout=5s

say "Waiting for the service network (kube-proxy and DNS)"
wait_for 180 "kube-proxy" "${KUBECTL[@]}" -n kube-system rollout status daemonset/kube-proxy --timeout=5s
wait_for 180 "DNS" "${KUBECTL[@]}" -n kube-system wait --for=condition=Ready pod -l k8s-app=kube-dns --timeout=5s

# Pods of Deployments that crashed before the network was back: restart them
# rather than wait out their back-off. Their Deployment replaces each one.
restart_stuck() {
  local ns pod reasons owner
  for ns in "${NAMESPACES[@]}"; do
    while read -r pod reasons owner; do
      [[ -z "$pod" || "$owner" != ReplicaSet ]] && continue
      if [[ "$reasons" =~ (CrashLoopBackOff|Error|CreateContainerConfigError) ]]; then
        echo "    restarting $ns/$pod ($reasons)" >&3
        "${KUBECTL[@]}" -n "$ns" delete pod "$pod" --wait=false >/dev/null
      fi
    done < <("${KUBECTL[@]}" -n "$ns" get pods -o \
      jsonpath='{range .items[*]}{.metadata.name}{" "}{range .status.containerStatuses[*]}{.state.waiting.reason}{.state.terminated.reason}{","}{end}{" "}{.metadata.ownerReferences[0].kind}{"\n"}{end}' 2>/dev/null)
  done
}

# True when every running pod of the factory is Ready; restarts stuck ones meanwhile.
all_ready() {
  restart_stuck
  local ns
  for ns in "${NAMESPACES[@]}"; do
    "${KUBECTL[@]}" get namespace "$ns" >/dev/null 2>&1 || continue
    "${KUBECTL[@]}" -n "$ns" wait --for=condition=Ready pods --all \
      --field-selector=status.phase!=Succeeded,status.phase!=Failed --timeout=5s >/dev/null 2>&1 || return 1
  done
}

say "Waiting for the factory's pods"
wait_for 600 "the factory's pods" all_ready || true

exec "$(dirname "$0")/doctor.sh"
