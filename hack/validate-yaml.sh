#!/usr/bin/env bash
# make validate-yaml: checks the factory's YAML offline with kubectl-validate,
# against the built-in schemas of the envtest Kubernetes version and our CRDs,
# create-time CEL rules included, and that each file in test/invalid is
# rejected with the message it expects. Each CEL rule's failing case is an
# envtest spec too. The Makefile passes the tools and folders in the environment; OUT
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
# A react-vite web app on the same Compute and Deployment, served by nginx.
rm -rf "$OUT/storefront"
go run ./hack/render -out "$OUT/storefront" -type static-site -container storefront -display-name "Storefront" \
	Framework=react-vite Standard=ts-standard Compute=small Deployment=k8s-manifests >/dev/null
"$KUSTOMIZE" build "$OUT/storefront/deploy/k8s" >"$OUT/storefront.yaml"

# The chart's GitProviders, catalog and Agents are the objects in ConfigMap
# c4factory-catalog, which the seed applies (hack/seed/catalog.sh): validated
# below as chart-*-catalog.yaml. With Gitea, the chart renders no
# c4.factory.io object of its own and none of them is a Helm hook, so an
# upgrade changes them in place (docs/plans/upgrade-in-place.md).
# catalog_objects FILE [CONFIGMAP] prints the objects in ConfigMap CONFIGMAP
# (default c4factory-catalog) of the render FILE.
catalog_objects() {
	awk -v cm_name="${2:-c4factory-catalog}" '/^---/ { cm = 0; data = 0 } $0 == "  name: " cm_name { cm = 1 }
		data { sub(/^    /, ""); print } cm && /^  objects\.yaml: \|$/ { data = 1 }' "$1"
}
for f in chart-gitea chart-gitea-bare; do
	catalog_objects "$OUT/$f.yaml" >"$OUT/$f-catalog.yaml"
	if ! grep -q '^kind: GitProvider$' "$OUT/$f-catalog.yaml"; then
		echo "The chart ($f.yaml) renders no GitProvider into ConfigMap c4factory-catalog" >&2
		exit 1
	fi
	outside=$(grep -A1 '^apiVersion: c4.factory.io/' "$OUT/$f.yaml" | grep '^kind:' || true)
	if [ -n "$outside" ]; then
		echo "The chart ($f.yaml) renders c4.factory.io objects outside ConfigMap c4factory-catalog; the seed applies them:" >&2
		sort <<<"$outside" | uniq -c >&2
		exit 1
	fi
done
if grep -q 'helm\.sh/hook' "$OUT/chart-gitea-catalog.yaml"; then
	echo "An object in ConfigMap c4factory-catalog is a Helm hook" >&2
	exit 1
fi
echo "The chart's GitProviders, catalog and Agents are in ConfigMap c4factory-catalog, none a hook"
# Without Gitea, the chart's gitProviders are the objects in ConfigMap
# c4factory-gitproviders, which Job c4-factory-gitproviders applies with the
# same catalog.sh: validated below as chart-gitproviders.yaml, none a hook.
catalog_objects "$OUT/chart.yaml" c4factory-gitproviders >"$OUT/chart-gitproviders.yaml"
if ! grep -q '^kind: GitProvider$' "$OUT/chart-gitproviders.yaml"; then
	echo "The chart (chart.yaml) renders no GitProvider into ConfigMap c4factory-gitproviders" >&2
	exit 1
fi
outside=$(grep -A1 '^apiVersion: c4.factory.io/' "$OUT/chart.yaml" | grep '^kind:' || true)
if [ -n "$outside" ]; then
	echo "The chart (chart.yaml) renders c4.factory.io objects outside ConfigMap c4factory-gitproviders:" >&2
	sort <<<"$outside" | uniq -c >&2
	exit 1
fi
if grep -q 'helm\.sh/hook' "$OUT/chart-gitproviders.yaml"; then
	echo "A GitProvider in ConfigMap c4factory-gitproviders is a Helm hook" >&2
	exit 1
fi
echo "The chart's gitProviders are in ConfigMap c4factory-gitproviders, none a hook"

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

# The chart's catalog (values.yaml, catalog.entries) pins each template the seed
# pushes to its tag in templates/versions.txt; hello, hello-standard, hello-html
# and hello-html-standard are the end-to-end tests' own. Rendered without the
# catalog and the Agents, it has neither.
pinned=$(awk '/url: .*\/templates\// { name = $2; gsub(/"/, "", name); sub(/.*\/templates\//, "", name) }
	/^ *ref:/ && name != "" { ref = $2; gsub(/"/, "", ref); print name, ref; name = "" }' "$OUT/chart-gitea-catalog.yaml" | sort -u)
want=$(grep -v -e '^hello ' -e '^hello-standard ' -e '^hello-html ' -e '^hello-html-standard ' templates/versions.txt | sort)
if [ "$pinned" != "$want" ]; then
	echo "The chart's catalog.entries don't match templates/versions.txt (< chart, > versions.txt):" >&2
	diff <(echo "$pinned") <(echo "$want") >&2 || true
	exit 1
fi
if grep -A1 '^apiVersion: c4.factory.io/' "$OUT/chart-gitea-bare-catalog.yaml" |
	grep -qE '^kind: (Agent|Framework|Standard|Compute|Deployment|Connector)$'; then
	echo "The chart renders catalog entries or Agents with catalog.enabled=false and agents.enabled=false" >&2
	exit 1
fi
echo "The chart's catalog matches templates/versions.txt; off, it renders no catalog or Agents"

# Shared credentials (docs/plans/done/shared-project-credentials.md): the chart makes
# each project's Namespace, labeled to receive the shared Secrets and kept on
# uninstall; the seed may label only the project namespaces; the operator reads
# the shared Secrets from its own namespace. chart-gitea.yaml renders project
# apps with pull Secret ghcr, chart-gitea-bare.yaml with agents.enabled=false
# and projectNamespaces.create=false. That neither renders a Secret is
# helm-lint's check.
# doc FILE KIND NAME prints the rendered object KIND/NAME, or nothing.
doc() {
	awk -v kind="$2" -v name="$3" '/^---/ { if (k && n) printf "%s", d; d = ""; k = 0; n = 0; next }
		{ d = d $0 "\n" } $0 == "kind: " kind { k = 1 } $0 == "  name: " name { n = 1 }
		END { if (k && n) printf "%s", d }' "$1"
}
# want WHAT TEXT PATTERN fails unless TEXT holds a line matching PATTERN.
want() {
	grep -qE -- "$3" <<<"$2" || { echo "The chart: $1" >&2; exit 1; }
}
ns=$(doc "$OUT/chart-gitea.yaml" Namespace apps)
want "no Namespace apps for gitea.projects={apps}" "$ns" '^kind: Namespace$'
want "Namespace apps lacks the shared-secrets label" "$ns" '^    c4\.factory\.io/shared-secrets: "true"$'
want "Namespace apps is not kept on uninstall" "$ns" '^    helm\.sh/resource-policy: keep$'
if [ -n "$(doc "$OUT/chart-gitea-bare.yaml" Namespace apps)" ]; then
	echo "The chart renders Namespace apps with projectNamespaces.create=false" >&2
	exit 1
fi
for f in chart-gitea chart-gitea-bare; do
	role=$(doc "$OUT/$f.yaml" ClusterRole c4-factory-seed-namespaces)
	want "$f: the seed's ClusterRole doesn't grant get and patch on namespaces apps only" "$role" \
		'^    resourceNames: \[apps\]$'
	want "$f: the seed's ClusterRole doesn't grant get and patch" "$role" '^    verbs: \[get, patch\]$'
	want "$f: the seed's ClusterRole isn't bound" "$(doc "$OUT/$f.yaml" ClusterRoleBinding c4-factory-seed-namespaces)" \
		'^    name: seed$'
	want "$f: the seed doesn't label the project namespaces" "$(doc "$OUT/$f.yaml" Job seed)" '^            - name: SHARE_SECRETS$'
	want "$f: the operator lacks POD_NAMESPACE" "$(doc "$OUT/$f.yaml" Deployment c4-factory-controller-manager)" \
		'^        - name: POD_NAMESPACE$'
done
manager=$(doc "$OUT/chart-gitea.yaml" Deployment c4-factory-controller-manager)
want "the operator doesn't share the Agents' token" "$manager" '^        - --shared-secret=claude-credentials$'
want "the operator doesn't share pull Secret ghcr" "$manager" '^        - --shared-pull-secret=ghcr$'
if grep -qE -e '--shared-(pull-)?secret' <<<"$(doc "$OUT/chart-gitea-bare.yaml" Deployment c4-factory-controller-manager)"; then
	echo "The chart: the operator shares a Secret with agents.enabled=false and no pull Secrets" >&2
	exit 1
fi
echo "The chart makes the project Namespaces, lets the seed label them and turns sharing on"

# The test environment shares the Claude token (make claude-secret): the
# manager names it and its own namespace, and the namespaces make seed and the
# samples make opt in.
for want in '--shared-secret=claude-credentials' 'name: POD_NAMESPACE' 'fieldPath: metadata.namespace'; do
	if ! grep -qF -- "$want" "$OUT/kustomize-default.yaml"; then
		echo "config/default's manager lacks $want: the test environment wouldn't share claude-credentials" >&2
		exit 1
	fi
done
for f in config/samples/webshop/namespace.yaml test/e2e/testdata/apps/namespace.yaml; do
	if ! grep -qF 'c4.factory.io/shared-secrets: "true"' "$f"; then
		echo "$f lacks label c4.factory.io/shared-secrets: \"true\": the operator wouldn't share claude-credentials into it" >&2
		exit 1
	fi
done
echo "The manager shares claude-credentials into the labeled sample namespaces"

# test/invalid holds YAML each create-time CEL rule must reject: each file's
# first line says "# expect: <message>", which the rejection must contain.
checked=0
for f in test/invalid/*.yaml; do
	want=$(sed -n '1s/^# expect: //p' "$f")
	if [ -z "$want" ]; then
		echo "$f: its first line must be '# expect: <message>'" >&2
		exit 1
	fi
	if out=$(env -u KUBERNETES_SERVICE_HOST -u KUBERNETES_SERVICE_PORT KUBECONFIG=/dev/null \
		"$KUBECTL_VALIDATE" --version "$KUBECTL_VALIDATE_K8S_VERSION" --local-schemas "$K8S_OPENAPI" \
		--local-crds config/crd/bases "$f" 2>&1); then
		echo "$f: valid, but should be rejected with: $want" >&2
		exit 1
	fi
	if ! grep -qF -- "$want" <<<"$out"; then
		echo "$f: rejected, but not with: $want" >&2
		echo "$out" >&2
		exit 1
	fi
	checked=$((checked + 1))
done
echo "All $checked invalid files rejected"
