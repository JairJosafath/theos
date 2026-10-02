#!/usr/bin/env bash
# Checks the factory in the Kind test environment and says what is wrong, from
# the Docker daemon up to the Agents' credentials. Works when the cluster or the
# UI is down, which is when it is needed.
#   make doctor            (KIND_CLUSTER=c4factory by default)
# Exits 1 when a required part is broken; optional parts only warn.
set -uo pipefail

CLUSTER="${KIND_CLUSTER:-c4factory}"
NODE="${CLUSTER}-control-plane"
KUBECTL=(kubectl --context "kind-${CLUSTER}" --request-timeout=10s)

failed=0
ok() { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
bad() {
  printf '  \033[31m✗\033[0m %s\n' "$*"
  failed=1
}
hint() { printf '      %s\n' "$*"; }
section() { printf '\n\033[1m%s\033[0m\n' "$*"; }

section "Machine"
if ! docker info >/dev/null 2>&1; then
  bad "Docker daemon is not running"
  hint "make up starts it (in the devcontainer: sudo /usr/local/share/docker-init.sh)"
  exit 1
fi
ok "Docker daemon is running"
state=$(docker inspect -f '{{.State.Status}}' "$NODE" 2>/dev/null || echo missing)
case "$state" in
running) ok "Kind node $NODE is running" ;;
missing)
  bad "Kind cluster $CLUSTER does not exist"
  hint "make cluster gitea seed deploy ... (see make e2e)"
  exit 1
  ;;
*)
  bad "Kind node $NODE is $state"
  hint "make up starts it"
  exit 1
  ;;
esac

section "Cluster"
if ! kubectl config get-contexts "kind-$CLUSTER" >/dev/null 2>&1; then
  bad "No kubeconfig context kind-$CLUSTER (a rebuilt devcontainer loses it)"
  hint "make up restores it (kind export kubeconfig --name $CLUSTER)"
  exit 1
fi
if ! "${KUBECTL[@]}" get --raw /readyz >/dev/null 2>&1; then
  bad "The Kubernetes API does not answer"
  hint "It takes a minute after a start; make up waits for it"
  exit 1
fi
ok "Kubernetes API is ready"
if [[ $("${KUBECTL[@]}" get node "$NODE" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null) == True ]]; then
  ok "Node is Ready"
else
  bad "Node is not Ready"
fi
pressure=$("${KUBECTL[@]}" get node "$NODE" -o jsonpath='{range .status.conditions[?(@.status=="True")]}{.type}{" "}{end}' 2>/dev/null | sed 's/Ready//')
[[ -n "${pressure// /}" ]] && bad "Node reports:${pressure}"
if "${KUBECTL[@]}" get crd tasks.c4.factory.io >/dev/null 2>&1; then
  ok "C4 Factory CRDs are installed"
else
  bad "C4 Factory CRDs are missing"
  hint "make deploy"
fi

# deployment <namespace> <name> <what> [optional]: counts the pods that are Ready
# now, not the Deployment's status, which lags right after a restart.
deployment() {
  local ns=$1 name=$2 what=$3 optional=${4:-}
  local sel
  if ! sel=$("${KUBECTL[@]}" -n "$ns" get deployment "$name" \
    -o go-template='{{range $k, $v := .spec.selector.matchLabels}}{{$k}}={{$v}},{{end}}' 2>/dev/null); then
    if [[ -n "$optional" ]]; then
      warn "$what is not installed ($optional)"
    else
      bad "$what is not installed ($ns/$name)"
    fi
    return
  fi
  local pods ready=0 total=0 restarts=0 waiting=""
  pods=$("${KUBECTL[@]}" -n "$ns" get pods -l "${sel%,}" --field-selector=status.phase!=Succeeded,status.phase!=Failed \
    -o jsonpath='{range .items[*]}{range .status.containerStatuses[*]}{.ready}{" "}{.restartCount}{" "}{.state.waiting.reason}{"\n"}{end}{end}' 2>/dev/null)
  while read -r r n w; do
    [[ -z "$r" ]] && continue
    total=$((total + 1))
    [[ "$r" == true ]] && ready=$((ready + 1))
    restarts=$((restarts + n))
    [[ -n "$w" ]] && waiting="$w"
  done <<<"$pods"
  if ((total > 0 && ready == total)); then
    ok "$what is ready ($ready/$total, $restarts restarts)"
  elif ((total == 0)); then
    warn "$what has no pods"
  else
    bad "$what is not ready ($ready/$total)${waiting:+: $waiting}"
    hint "kubectl --context kind-$CLUSTER -n $ns logs deployment/$name"
  fi
}

section "Factory"
deployment c4-factory-system c4-factory-controller-manager "Operator"
deployment gitea gitea "Gitea"
deployment gitea gitea-runner "CI runner" "make runner"
deployment gitea gitea-runner-android "Release runner" "optional: make runner-android"
deployment agent-sandbox-system agent-sandbox-controller "Agent Sandbox" "make agent-sandbox"
deployment kube-system coredns "Cluster DNS"
deployment kube-system metrics-server "metrics-server" "make metrics-server"
deployment c4-factory-ui c4-factory-ui "UI" "make ui"
if "${KUBECTL[@]}" get --raw /apis/metrics.k8s.io/v1beta1/nodes >/dev/null 2>&1; then
  ok "CPU and memory figures are available"
else
  warn "No CPU and memory figures (metrics-server missing or starting)"
fi

section "Projects"
projects=$("${KUBECTL[@]}" get projects.c4.factory.io -A -o jsonpath='{range .items[*]}{.metadata.namespace}{"\n"}{end}' 2>/dev/null)
if [[ -z "$projects" ]]; then
  warn "No Projects yet (make sample, make toolbox)"
fi
for ns in $projects; do
  # Each GitProvider's credentials.
  while read -r provider secret; do
    [[ -z "$provider" ]] && continue
    if "${KUBECTL[@]}" -n "$ns" get secret "$secret" >/dev/null 2>&1; then
      ok "$ns: GitProvider $provider has its Secret $secret"
    else
      bad "$ns: GitProvider $provider is missing Secret $secret"
      hint "make seed"
    fi
  done < <("${KUBECTL[@]}" -n "$ns" get gitproviders.c4.factory.io \
    -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.credentialsSecretRef.name}{"\n"}{end}' 2>/dev/null)
  # Each Agent's credentials, if it needs any.
  while read -r agent secret; do
    [[ -z "$agent" || -z "$secret" ]] && continue
    if "${KUBECTL[@]}" -n "$ns" get secret "$secret" >/dev/null 2>&1; then
      ok "$ns: Agent $agent has its Secret $secret"
    else
      warn "$ns: Agent $agent is missing Secret $secret; its Tasks wait"
      hint "make claude-secret CLAUDE_TOKEN=..."
    fi
  done < <("${KUBECTL[@]}" -n "$ns" get agents.c4.factory.io \
    -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.credentialsSecretRef.name}{"\n"}{end}' 2>/dev/null)
  failed_containers=$("${KUBECTL[@]}" -n "$ns" get containers.c4.factory.io \
    -o jsonpath='{range .items[?(@.status.phase=="Failed")]}{.metadata.name}{" "}{end}' 2>/dev/null | xargs)
  [[ -n "$failed_containers" ]] && warn "$ns: Failed Containers: $failed_containers"
  active=$("${KUBECTL[@]}" -n "$ns" get tasks.c4.factory.io \
    -o jsonpath='{range .items[*]}{.status.phase}{"\n"}{end}' 2>/dev/null | grep -cvE '^(Merged|Failed)$')
  [[ "$active" -gt 0 ]] && ok "$ns: $active active Task(s)"
done

echo
if [[ $failed == 0 ]]; then
  printf '\033[32mThe factory is healthy.\033[0m Watch it with make ui-forward, then open http://localhost:8080/?view=factory\n'
else
  printf '\033[31mThe factory needs attention:\033[0m see the ✗ lines above.\n'
fi
exit $failed
