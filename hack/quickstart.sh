#!/usr/bin/env bash
# One command from a machine with Docker to a factory that builds something,
# with no model and no token (#53): a Kind cluster of its own, Agent Sandbox,
# the published chart with Gitea and its runners, and a Brief the stand-in
# Agent plans, implements and reviews. It follows docs/getting-started.md,
# sections 1 and "Without a token", step for step, after one question the
# guide doesn't ask: can the release's chart be pulled at all? A release
# that isn't published, or packages that are private, end it there.
#
#   hack/quickstart.sh            bring the factory up, and say where to look
#   hack/quickstart.sh --status   what the factory is doing
#   hack/quickstart.sh --down     remove the cluster and the port-forward
#   hack/quickstart.sh --dry-run  print the commands instead of running them
#
# It touches nothing of yours: the cluster's kubeconfig is its own file
# (THEOS_HOME/kubeconfig, never ~/.kube/config), and kind, kubectl and helm
# are used from PATH when they are there, else downloaded once into
# THEOS_HOME/bin at the versions below and checked against their sha256.
# Runs on Linux, macOS and WSL with bash 3.2 or later.
#
#   THEOS_VERSION       the release to install (default: the one this script shipped with)
#   THEOS_HOME          where its files go (default ~/.theos)
#   THEOS_CLUSTER_NAME  the Kind cluster's name (default theos-quickstart)
#   THEOS_PROJECT       the project's namespace (default webshop)
#   THEOS_UI_PORT       the UI's port on localhost (default 8080)
#   THEOS_REGISTRY      where the chart and images are (default ghcr.io/jairjosafath/theos)
#   GHCR_USER, GHCR_TOKEN  a GitHub user and a token with read:packages, only
#                       while the chart and the images are private: they log
#                       helm in and become the pull secret, as the guide says
set -euo pipefail

# The release this script installs. hack/release.sh bumps it with the chart.
VERSION=${THEOS_VERSION:-0.32.0}
THEOS_HOME=${THEOS_HOME:-$HOME/.theos}
CLUSTER=${THEOS_CLUSTER_NAME:-theos-quickstart}
PROJECT=${THEOS_PROJECT:-webshop}
UI_PORT=${THEOS_UI_PORT:-8080}
REGISTRY=${THEOS_REGISTRY:-ghcr.io/jairjosafath/theos}
KUBECONFIG_FILE=$THEOS_HOME/kubeconfig
BIN=$THEOS_HOME/bin
PID_FILE=$THEOS_HOME/ui-forward.pid

# The tools it downloads when PATH has none, and their sha256 per platform.
KIND_VERSION=v0.30.0
KUBECTL_VERSION=v1.34.1
HELM_VERSION=v4.3.0
AGENT_SANDBOX_VERSION=v1.0.4
sha256_of() {
  case $1 in
    kind-linux-amd64) echo 517ab7fc89ddeed5fa65abf71530d90648d9638ef0c4cde22c2c11f8097b8889 ;;
    kind-linux-arm64) echo 7ea2de9d2d190022ed4a8a4e3ac0636c8a455e460b9a13ccf19f15d07f4f00eb ;;
    kind-darwin-amd64) echo 4f0b6e3b88bdc66d922c08469f05ef507d4903dd236e6319199bb9c868eed274 ;;
    kind-darwin-arm64) echo ceaf40df1d1551c481fb50e3deb5c3deecad5fd599df5469626b70ddf52a1518 ;;
    kubectl-linux-amd64) echo 7721f265e18709862655affba5343e85e1980639395d5754473dafaadcaa69e3 ;;
    kubectl-linux-arm64) echo 420e6110e3ba7ee5a3927b5af868d18df17aae36b720529ffa4e9e945aa95450 ;;
    kubectl-darwin-amd64) echo bb211f2b31f2b3bc60562b44cc1e3b712a16a98e9072968ba255beb04cefcfdf ;;
    kubectl-darwin-arm64) echo d80e5fa36f2b14005e5bb35d3a72818acb1aea9a081af05340a000e5fbdb2f76 ;;
    helm-linux-amd64) echo 86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb ;;
    helm-linux-arm64) echo 31c5794dd55c66a51e6b7d2e2ac7a114ae8b1de41ff1d9ba51748ac973b06a08 ;;
    helm-darwin-amd64) echo 347a784877e0e20eac865e8d1c36a80f6bb0861d6f29abd34defb6570ef95d92 ;;
    helm-darwin-arm64) echo d3870437e1e95b67f8edbde964156c84a26503f560821d40c542441658934fba ;;
  esac
}

say() { printf '\n==> %s\n' "$*"; }
die() {
  printf 'quickstart: %s\n' "$*" >&2
  exit 1
}

DRY=
# run CMD...: the command, or its line under --dry-run.
run() {
  if [ -n "$DRY" ]; then
    printf '+ %s\n' "$*"
  else
    "$@"
  fi
}

platform() {
  local os arch
  case $(uname -s) in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "runs on Linux, macOS and WSL, not on $(uname -s)" ;;
  esac
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "runs on amd64 and arm64, not on $(uname -m)" ;;
  esac
  echo "$os-$arch"
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# fetch NAME URL: downloads URL to BIN/NAME.part and checks it against NAME's
# pinned sha256 for this platform; a file that differs is removed.
fetch() {
  local name=$1 url=$2 p want got
  p=$(platform) || exit 1
  want=$(sha256_of "$name-$p")
  [ -n "$want" ] || die "no pinned sha256 for $name on $p"
  mkdir -p "$BIN"
  curl -fsSL --retry 3 -o "$BIN/$name.part" "$url" || die "could not download $url"
  got=$(sha256_file "$BIN/$name.part")
  if [ "$got" != "$want" ]; then
    rm -f "$BIN/$name.part"
    die "$url has sha256 $got, not the pinned $want; nothing was installed"
  fi
}

# tool NAME: NAME from PATH, else from BIN, downloading it there first.
tool() {
  local name=$1 p os arch
  if command -v "$name" >/dev/null 2>&1; then
    command -v "$name"
    return
  fi
  if [ ! -x "$BIN/$name" ]; then
    p=$(platform) || exit 1
    os=${p%-*} arch=${p#*-}
    printf 'quickstart: no %s on PATH; downloading it to %s\n' "$name" "$BIN" >&2
    case $name in
      kind)
        fetch kind "https://github.com/kubernetes-sigs/kind/releases/download/$KIND_VERSION/kind-$p"
        mv "$BIN/kind.part" "$BIN/kind"
        ;;
      kubectl)
        fetch kubectl "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/$os/$arch/kubectl"
        mv "$BIN/kubectl.part" "$BIN/kubectl"
        ;;
      helm)
        fetch helm "https://get.helm.sh/helm-$HELM_VERSION-$p.tar.gz"
        tar -xzOf "$BIN/helm.part" "$p/helm" >"$BIN/helm"
        rm -f "$BIN/helm.part"
        ;;
    esac
    chmod +x "$BIN/$name"
  fi
  echo "$BIN/$name"
}

# The Brief the stand-in Agent works: config/samples/brief/stand-in.yaml, in
# the project's namespace. hack/quickstart_test.sh keeps the two in step.
brief() {
  cat <<EOF
apiVersion: theos.factory.io/v1beta1
kind: Brief
metadata:
  name: stand-in-demo
  namespace: $PROJECT
spec:
  idea: >-
    A small command-line tool, planned by the stand-in Agent to show the loop
    from an idea to a Crew working an issue.
  agents:
    planner: {name: stand-in}
    implementer: {name: stand-in}
    reviewer: {name: stand-in}
EOF
}

stop_forward() {
  if [ -f "$PID_FILE" ]; then
    kill "$(cat "$PID_FILE")" 2>/dev/null || true
    rm -f "$PID_FILE"
  fi
}

# published HELM: the chart of this release can be pulled, or the script ends
# here saying the two reasons it can't, with helm's own words.
published() {
  local helm=$1 why
  if [ -n "$DRY" ]; then
    printf '+ %s show chart oci://%s/charts/theos --version %s\n' "$helm" "$REGISTRY" "$VERSION"
    return 0
  fi
  why=$($helm show chart "oci://$REGISTRY/charts/theos" --version "$VERSION" 2>&1 >/dev/null) && return 0
  printf '%s\n' "$why" | tail -n 3 >&2
  die "can't pull the chart of theos $VERSION from $REGISTRY, and nothing was made. Either that release isn't published there (THEOS_VERSION picks another), or its packages are private: then GHCR_USER and GHCR_TOKEN, a GitHub user and a token that may read them, log this in"
}

up() {
  local kind kubectl helm started=$SECONDS sets clusters
  command -v docker >/dev/null 2>&1 || die "needs Docker: install Docker Desktop or Docker Engine, start it, and run this again"
  [ -n "$DRY" ] || docker info >/dev/null 2>&1 || die "Docker is installed but not running, or this user may not use it: start it and run this again"
  # One by one: a failed download ends the script here, which a failure
  # inside a line of several assignments would not.
  kind=$(tool kind) || exit 1
  kubectl=$(tool kubectl) || exit 1
  helm=$(tool helm) || exit 1
  mkdir -p "$THEOS_HOME"
  kubectl="$kubectl --kubeconfig $KUBECONFIG_FILE"
  helm="$helm --kubeconfig $KUBECONFIG_FILE"

  if [ -n "${GHCR_USER:-}" ] && [ -n "${GHCR_TOKEN:-}" ]; then
    say "A login for the private packages, as $GHCR_USER"
    if [ -n "$DRY" ]; then
      printf '+ %s registry login ghcr.io -u %s --password-stdin\n' "$helm" "$GHCR_USER"
    else
      printf '%s' "$GHCR_TOKEN" | $helm registry login ghcr.io -u "$GHCR_USER" --password-stdin
    fi
  fi
  # Before anything is made: a release that isn't published, or packages
  # that are private, would otherwise fail fifteen minutes in, on helm's
  # words alone, with a cluster left behind.
  published "$helm"

  say "A Kind cluster, $CLUSTER, with a kubeconfig of its own ($KUBECONFIG_FILE)"
  # Read first, then match: grep -q in a pipe under pipefail turns a match
  # into a miss when kind is still writing (hack/grep-q-pipe_test.sh).
  clusters=
  [ -n "$DRY" ] || clusters=$("$kind" get clusters 2>/dev/null || true)
  if grep -qx "$CLUSTER" <<<"$clusters"; then
    echo "It exists; using it."
    "$kind" export kubeconfig --name "$CLUSTER" --kubeconfig "$KUBECONFIG_FILE" >/dev/null
  else
    run "$kind" create cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG_FILE" --wait 120s
  fi

  say "Agent Sandbox $AGENT_SANDBOX_VERSION, where the agents run"
  run $kubectl apply -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/$AGENT_SANDBOX_VERSION/sandbox.yaml"
  run $kubectl -n agent-sandbox-system patch deployment agent-sandbox-controller --type=json \
    -p '[{"op":"replace","path":"/spec/template/spec/containers/0/args/0","value":"--leader-elect=false"}]'

  say "The namespaces of the release and of Gitea"
  for ns in theos-system gitea; do
    if [ -n "$DRY" ]; then
      printf '+ %s create namespace %s\n' "$kubectl" "$ns"
    else
      $kubectl create namespace "$ns" --dry-run=client -o yaml | $kubectl apply -f -
    fi
  done

  sets="gitea.createNamespace=false,gitea.projects={$PROJECT}"
  if [ -n "${GHCR_USER:-}" ] && [ -n "${GHCR_TOKEN:-}" ]; then
    say "The pull secret for the private packages, as $GHCR_USER"
    for ns in theos-system gitea; do
      if [ -n "$DRY" ]; then
        printf '+ %s -n %s create secret docker-registry ghcr\n' "$kubectl" "$ns"
      else
        $kubectl -n "$ns" create secret docker-registry ghcr --docker-server=ghcr.io \
          --docker-username="$GHCR_USER" --docker-password="$GHCR_TOKEN" --dry-run=client -o yaml | $kubectl apply -f -
      fi
    done
    sets="$sets,global.imagePullSecrets[0].name=ghcr,manager.imagePullSecrets[0].name=ghcr"
  fi

  say "theos $VERSION: the operator, the UI, Gitea and its runners (a few minutes on a first run)"
  run $helm upgrade --install theos "oci://$REGISTRY/charts/theos" --version "$VERSION" \
    -n theos-system --wait --timeout 15m --set "$sets"

  say "A Brief for the stand-in Agent, which needs no model and no token"
  if [ -n "$DRY" ]; then
    printf '+ %s apply -f - <<the Brief stand-in-demo in %s\n' "$kubectl" "$PROJECT"
  else
    brief | $kubectl apply -f -
  fi
  run $kubectl -n "$PROJECT" wait brief/stand-in-demo "--for=jsonpath={.status.phase}=Planned" --timeout=15m

  say "The UI on http://localhost:$UI_PORT"
  if [ -z "$DRY" ]; then
    stop_forward
    nohup $kubectl -n theos-system port-forward svc/theos-ui "$UI_PORT:8080" >"$THEOS_HOME/ui-forward.log" 2>&1 &
    echo $! >"$PID_FILE"
  fi

  cat <<EOF

The factory is up, in $((SECONDS - started)) seconds.

  Open        http://localhost:$UI_PORT/?area=work
  You see     the Brief "stand-in-demo": its plan, the Container the factory
              scaffolded for it, and a Crew working that Container's first
              issue. The stand-in implements, CI checks, the stand-in reviews,
              and the change merges; a release follows in about half an hour.
  Watch       $0 --status
  kubectl     export KUBECONFIG=$KUBECONFIG_FILE
  Real agents docs/getting-started.md, "2. The agents' token": a Claude token
              in a Secret, then name the Agents implementer and reviewer.
  Remove it   $0 --down
EOF
}

status() {
  local kubectl
  [ -f "$KUBECONFIG_FILE" ] || die "no factory here: run $0 first"
  kubectl=$(tool kubectl) || exit 1
  kubectl="$kubectl --kubeconfig $KUBECONFIG_FILE"
  $kubectl -n "$PROJECT" get briefs,containers,crews,tasks
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "The UI is on http://localhost:$UI_PORT"
  else
    echo "The UI's port-forward is not running; $0 starts it again."
  fi
}

down() {
  local kind
  kind=$(tool kind) || exit 1
  stop_forward
  say "Removing the Kind cluster $CLUSTER"
  run "$kind" delete cluster --name "$CLUSTER"
  [ -n "$DRY" ] || rm -f "$KUBECONFIG_FILE" "$THEOS_HOME/ui-forward.log"
  echo "Gone. The downloaded tools stay in $BIN; remove $THEOS_HOME to drop them too."
}

action=up
for arg in "$@"; do
  case $arg in
    --down) action=down ;;
    --status) action=status ;;
    --dry-run) DRY=1 ;;
    -h | --help)
      sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'
      exit 0
      ;;
    *) die "unknown argument $arg; see $0 --help" ;;
  esac
done
$action
