#!/usr/bin/env bash
# Captures what each milestone looks like in the Kind cluster as screenshots in
# docs/screenshots/: terminal output rendered by ui/scripts/term-shot.mjs, and web
# pages (Gitea, the UI) taken by ui/scripts/screenshot.mjs.
#
# Needs the test environment (make cluster gitea seed deploy sample) and the UI
# (make ui). Run it from the repository root: hack/evidence.sh
# It takes about five minutes, two of them waiting for a Session to turn Stale.
set -euo pipefail

# Default: the pool cluster this checkout leased (hack/lib/github.sh cluster name), else c4factory.
CLUSTER="${KIND_CLUSTER:-$("$(dirname "${BASH_SOURCE[0]}")/lib/github.sh" cluster name 2>/dev/null || echo c4factory)}"
OUT="${OUT:-docs/screenshots}"
WORK="$(mktemp -d)"
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$WORK"' EXIT
mkdir -p "$OUT"

# Every kubectl call goes to the Kind cluster; captures show it without the flag.
kubectl() { command kubectl --context "kind-$CLUSTER" "$@"; }

# p prints a prompt with a command, as a terminal would show it.
p() { printf '\033[1;32m$\033[0m %s\n' "$*"; }

# run shows a command at a prompt, then runs it; a failing command is evidence too.
run() {
    p "$*"
    "$@" 2>&1 || true
}

# shot <name> <title> <function>: renders what the function prints as a terminal window.
shot() {
    # A subshell, so a capture's cd or failed command can't affect the rest.
    (
        set +e
        "$3"
    ) >"$WORK/$1.txt" 2>&1
    node ui/scripts/term-shot.mjs "$WORK/$1.txt" "$OUT/$1.png" --title "$2" --no-wrap --scale 2 >/dev/null
    echo "Saved $OUT/$1.png"
}

# web <name> <url> [screenshot.mjs options]: takes a web page.
web() {
    local name="$1"
    shift
    node ui/scripts/screenshot.mjs "$@" "$OUT/$name.png" >/dev/null
    echo "Saved $OUT/$name.png"
}

conditions='{range .status.conditions[*]}{.type}={.status}  {.reason}: {.message}{"\n"}{end}'

# --- Milestone 1: test environment ------------------------------------------

m1_pods() { run kubectl get pods -A; }

m1_tags() {
    p 'curl http://localhost:3000/api/v1/repos/templates/<template>/tags'
    for r in python-fastapi small k8s-manifests postgres; do
        printf '%-16s %s\n' "$r" "$(curl -s "http://localhost:3300/api/v1/repos/templates/$r/tags" | jq -r '[.[].name] | join(", ")')"
    done
    echo
    run kubectl -n webshop get secret gitea-credentials
}

# --- Milestone 2: story model ------------------------------------------------

m2_story() { run kubectl get c4 -n webshop; }

m2_ownership() {
    p "kubectl -n webshop get <kind>/<name> -o jsonpath='{.metadata.ownerReferences[0]}'"
    for r in person/customer softwaresystem/webshop-core container/orders-api datastore/shop-db \
        relationship/orders-api-to-shop-db relationship/customer-uses-webshop-core; do
        kubectl -n webshop get "$r" -o jsonpath='{.kind}/{.metadata.name}{"  <- owned by  "}{.metadata.ownerReferences[0].kind}/{.metadata.ownerReferences[0].name}{"\n"}'
    done
}

m2_policy() {
    kubectl create namespace evidence-policy --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    p 'kubectl apply -f -   # a Project called webshop in namespace evidence-policy'
    printf 'apiVersion: c4.factory.io/v1beta1\nkind: Project\nmetadata: {name: webshop, namespace: evidence-policy}\n' |
        kubectl apply -f - 2>&1 || true
    kubectl delete namespace evidence-policy --wait=false >/dev/null
}

m2_cascade() {
    run kubectl -n evidence-cascade get softwaresystems,containers,datastores,relationships
    echo
    run kubectl -n evidence-cascade delete softwaresystem webshop-core
    sleep 5
    echo
    run kubectl -n evidence-cascade get softwaresystems,containers,datastores,relationships
}

# --- Milestone 3: catalog ----------------------------------------------------

m3_choices() {
    run kubectl -n evidence-catalog get container storefront
    echo
    p "kubectl -n evidence-catalog get container storefront -o jsonpath='<conditions>'"
    kubectl -n evidence-catalog get container storefront -o jsonpath="$conditions"
}

# --- Milestone 4: scaffolding ------------------------------------------------

m4_container() {
    run kubectl -n webshop get containers -o wide
    echo
    p "kubectl -n webshop get container orders-api -o jsonpath='<conditions, scaffoldedFrom, appliedRelationships>'"
    kubectl -n webshop get container orders-api -o jsonpath="$conditions"
    kubectl -n webshop get container orders-api \
        -o jsonpath='scaffoldedFrom: {.status.scaffoldedFrom}{"\n"}appliedRelationships: {.status.appliedRelationships}{"\n"}'
}

m4_works() {
    cd "$WORK/orders-api"
    run git log --format='%h %an <%ae>  %s'
    echo
    run uv sync -q
    run uv run ruff check
    p uv run pytest -q
    uv run pytest -q 2>&1 | tail -1
    echo
    p 'kubectl kustomize deploy/k8s | grep ^kind:'
    command kubectl kustomize deploy/k8s | grep -E '^kind:'
    echo
    cd .devcontainer
    run docker compose -f compose.yaml -f compose.postgres.yaml config --services
}

# --- Milestone 5: sessions ---------------------------------------------------

m5_active() {
    run kubectl -n webshop get sessions
    echo
    run kubectl -n webshop get containers,softwaresystems,projects
}

m5_stale() {
    p "kubectl -n webshop exec session-orders-api -c client -- pkill -KILL c4session"
    kubectl -n webshop exec session-orders-api -c client -- pkill -KILL c4session
    echo "Killed the client at $(date -u +%T); watching the Session:"
    for _ in $(seq 1 40); do
        phase=$(kubectl -n webshop get sessions -o jsonpath='{.items[0].status.phase}')
        echo "  $(date -u +%T)  $phase"
        [ "$phase" = Stale ] && break
        sleep 10
    done
    echo
    run kubectl -n webshop get sessions,containers
}

# --- Milestone 6: UI ---------------------------------------------------------

m6_readonly() {
    local api=http://localhost:8080/k8s/apis/c4.factory.io/v1beta1/namespaces/webshop/containers/orders-api
    p 'curl -X <method> http://localhost:8080/k8s/apis/c4.factory.io/.../containers/orders-api'
    for m in GET POST PUT PATCH DELETE; do
        printf '  %-7s -> %s\n' "$m" "$(curl -s -o /dev/null -w '%{http_code}' -X "$m" "${api/8080/8088}")"
    done
    p 'curl http://localhost:8080/k8s/api/v1/namespaces/webshop/secrets/gitea-credentials'
    printf '  %-7s -> %s\n' GET \
        "$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8088/k8s/api/v1/namespaces/webshop/secrets/gitea-credentials)"
    echo
    p 'kubectl auth can-i --list --as=system:serviceaccount:c4-factory-ui:c4-factory-ui'
    kubectl auth can-i --list --as=system:serviceaccount:c4-factory-ui:c4-factory-ui | grep -E 'Resources|c4.factory.io'
}

# --- Run ---------------------------------------------------------------------

echo "==> Milestone 1: test environment"
kubectl -n gitea port-forward svc/gitea 3300:3000 >/dev/null 2>&1 &
sleep 3
shot m1-pods "One Kind cluster runs the whole loop" m1_pods
web m1-gitea-templates http://localhost:3300/templates --width 1280 --height 800 --scale 2
shot m1-template-tags "The four templates, tagged, and the credentials" m1_tags

echo "==> Milestone 2: story model"
shot m2-story "The webshop story" m2_story
shot m2-ownership "Every child names its parent; the operator sets the owner" m2_ownership
shot m2-policy "A Project must be named after its namespace" m2_policy
# A copy of the story in its own namespace, so deleting webshop-core there shows
# the cascade without touching the sample.
kubectl create namespace evidence-cascade --dry-run=client -o yaml | kubectl apply -f - >/dev/null
printf 'apiVersion: c4.factory.io/v1beta1\nkind: Project\nmetadata: {name: evidence-cascade}\n' |
    kubectl -n evidence-cascade apply -f - >/dev/null
kubectl -n evidence-cascade apply -f config/samples/webshop/story.yaml >/dev/null
sleep 5
shot m2-cascade "Deleting webshop-core deletes what it owns" m2_cascade
kubectl delete namespace evidence-cascade --wait=false >/dev/null

echo "==> Milestone 3: catalog"
kubectl create namespace evidence-catalog --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n evidence-catalog apply -f config/samples/webshop/factory.yaml >/dev/null
kubectl -n evidence-catalog create secret generic gitea-credentials --from-literal=token=unused \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
cat <<'EOF' | kubectl -n evidence-catalog apply -f - >/dev/null
apiVersion: c4.factory.io/v1beta1
kind: Project
metadata: {name: evidence-catalog}
---
apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata: {name: webshop-core}
---
apiVersion: c4.factory.io/v1beta1
kind: Container
metadata: {name: storefront}
spec:
  softwareSystemRef: {name: webshop-core}
  displayName: Storefront
  type: static-site
  gitProviderRef: {name: gitea}
  frameworkRef: {name: python-fastapi}
EOF
kubectl -n evidence-catalog wait container/storefront --for=jsonpath='{.status.phase}'=Failed --timeout=60s >/dev/null
shot m3-choices "A static-site that uses python-fastapi" m3_choices
kubectl delete namespace evidence-catalog --wait=false >/dev/null

echo "==> Milestone 4: scaffolding"
shot m4-container "orders-api is Ready" m4_container
web m4-gitea-repo http://localhost:3300/webshop/orders-api --width 1280 --height 1000 --scale 2
sha=$(curl -s 'http://localhost:3300/api/v1/repos/webshop/orders-api/commits?limit=1' | jq -r '.[0].sha')
web m4-gitea-commit "http://localhost:3300/webshop/orders-api/commit/$sha" --width 1280 --height 800 --scale 2
git clone -q http://localhost:3300/webshop/orders-api.git "$WORK/orders-api"
shot m4-works "The scaffolded repository works" m4_works

echo "==> Milestone 5: sessions"
kubectl -n webshop delete pod session-orders-api --ignore-not-found --wait=true >/dev/null
kubectl -n webshop delete sessions --all >/dev/null
kubectl apply -f hack/session/pod.yaml >/dev/null
kubectl -n webshop wait pod/session-orders-api --for=condition=Ready --timeout=120s >/dev/null
sleep 5
shot m5-active "The test pod's Session, rolled up" m5_active
shot m5-stale "The client is killed; two minutes later the Session is Stale" m5_stale
# Leave an Active Session behind for the UI screenshots.
kubectl -n webshop delete pod session-orders-api --wait=true >/dev/null
kubectl apply -f hack/session/pod.yaml >/dev/null
kubectl -n webshop wait pod/session-orders-api --for=condition=Ready --timeout=120s >/dev/null
sleep 5

echo "==> Milestone 6: UI"
kubectl -n c4-factory-ui port-forward svc/c4-factory-ui 8088:8080 >/dev/null 2>&1 &
sleep 3
ui="http://localhost:8088/?project=webshop"
containers="view=containers&system=webshop-core"
# The watches never go idle, so give the page a moment to list and draw.
web m6-ui-context "$ui" --width 1440 --height 900 --full --delay 2500 --scale 2
web m6-ui-containers "$ui&$containers" --width 1440 --height 900 --full --delay 2500 --scale 2
web m6-ui-containers-dark "$ui&$containers" --width 1440 --height 900 --full --delay 2500 --scale 2 --dark
# Demo mode shows the states the live sample doesn't: Pending, Scaffolding,
# Failed, several Sessions, a Relationship added after scaffolding.
web m6-ui-demo-containers "http://localhost:8088/?demo=1&$containers" --width 1440 --height 900 --full --delay 1500 --scale 2
web m6-ui-phone "$ui" --width 390 --height 844 --full --delay 2500 --scale 2
shot m6-ui-readonly "The UI is read-only" m6_readonly

echo "Done."
