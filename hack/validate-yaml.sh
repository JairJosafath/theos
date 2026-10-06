#!/usr/bin/env bash
# make validate-yaml: checks the factory's YAML offline with kubectl-validate,
# against the built-in schemas of the envtest Kubernetes version and our CRDs,
# create-time CEL rules included. Each CEL rule's failing case is an envtest
# spec. The Makefile passes the tools and folders in the environment; OUT
# already holds the chart renders of make helm-lint.
set -euo pipefail

: "${KUBECTL_VALIDATE:?}" "${KUSTOMIZE:?}" "${K8S_OPENAPI:?}" "${KUBECTL_VALIDATE_K8S_VERSION:?}" "${OUT:?}"

# The kustomize overlays that build offline. config/agent-sandbox and
# config/metrics-server fetch remote URLs, so they are left out.
OVERLAYS=(default gitea ui)
echo "Skipped, they fetch remote URLs: config/agent-sandbox, config/metrics-server"

mkdir -p "$OUT"
rm -f "$OUT"/kustomize-*.yaml
for o in "${OVERLAYS[@]}"; do
	"$KUSTOMIZE" build "config/$o" >"$OUT/kustomize-$o.yaml"
done

# The webshop sample's orders-api repository, as the operator scaffolds it.
rm -rf "$OUT/orders-api"
go run ./hack/render -out "$OUT/orders-api" -type service -container orders-api -display-name "Orders API" \
	Framework=python-fastapi Compute=small Deployment=k8s-manifests Connector=postgres >/dev/null
"$KUSTOMIZE" build "$OUT/orders-api/deploy/k8s" >"$OUT/orders-api.yaml"

files=()
while IFS= read -r f; do files+=("$f"); done < <(
	find config/samples test/e2e/testdata -name '*.yaml' ! -name kustomization.yaml | sort
)
files+=(config/runner/*.yaml hack/seed/job.yaml hack/session/pod.yaml "$OUT"/*.yaml)

# kubectl-validate embeds built-in schemas only up to an older release, which
# --version names; the envtest release's schemas in K8S_OPENAPI come first.
# No kubeconfig and no in-cluster config, so it never asks a cluster.
echo "Validating ${#files[@]} files"
if ! env -u KUBERNETES_SERVICE_HOST -u KUBERNETES_SERVICE_PORT KUBECONFIG=/dev/null \
	"$KUBECTL_VALIDATE" --version "$KUBECTL_VALIDATE_K8S_VERSION" --local-schemas "$K8S_OPENAPI" \
	--local-crds config/crd/bases --local-crds test/crds/agent-sandbox "${files[@]}" >"$OUT/result.txt" 2>&1; then
	grep -v -e '\.\.\..*OK' -e '^$' "$OUT/result.txt" >&2
	exit 1
fi
echo "All valid"
