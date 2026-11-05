#!/usr/bin/env bash
# make validate-yaml: checks the factory's YAML offline with kubectl-validate,
# against the built-in schemas of the envtest Kubernetes version and our CRDs,
# create-time CEL rules included, and that each file in test/invalid is
# rejected with the message it expects. Each CEL rule's failing case is an
# envtest spec too. The Makefile passes the tools and folders in the environment; OUT
# already holds the chart renders of make helm-lint.
set -euo pipefail

: "${KUBECTL_VALIDATE:?}" "${KUSTOMIZE:?}" "${K8S_OPENAPI:?}" "${KUBECTL_VALIDATE_K8S_VERSION:?}" "${OUT:?}"

# The kustomize overlays that build offline. config/agent-sandbox,
# config/argocd and config/metrics-server fetch remote URLs, so they are left
# out.
OVERLAYS=(default gitea ui chat)
echo "Skipped, they fetch remote URLs: config/agent-sandbox, config/argocd, config/metrics-server"

mkdir -p "$OUT"
rm -f "$OUT"/kustomize-*.yaml
for o in "${OVERLAYS[@]}"; do
	"$KUSTOMIZE" build "config/$o" >"$OUT/kustomize-$o.yaml"
done
# The webshop with orders-api also published on GitHub (#1603).
"$KUSTOMIZE" build config/samples/webshop-github >"$OUT/kustomize-webshop-github.yaml"
# The webshop with orders-api also published on GitLab (#1804).
"$KUSTOMIZE" build config/samples/webshop-gitlab >"$OUT/kustomize-webshop-gitlab.yaml"
# ProRivals, the esports community on the chart's catalog (docs/plans/prorivals.md).
"$KUSTOMIZE" build config/samples/prorivals >"$OUT/kustomize-prorivals.yaml"

# The webshop sample's orders-api repository, as the operator scaffolds it.
rm -rf "$OUT/orders-api"
go run ./hack/render -out "$OUT/orders-api" -type service -container orders-api -display-name "Orders API" \
	Framework=python-fastapi Standard=python-standard Compute=small Deployment=k8s-manifests Connector=postgres >/dev/null
"$KUSTOMIZE" build "$OUT/orders-api/deploy/k8s" >"$OUT/orders-api.yaml"
# postgres runs its database next to the app, both reading the password from
# Secret orders-api-postgres (#1471); the app's OIDC issuer and audience come
# from ConfigMap orders-api-auth (#1540).
kinds=$(sed -n 's/^kind: //p' "$OUT/orders-api.yaml" | sort | tr '\n' ' ')
if [ "$kinds" != "ConfigMap ConfigMap Deployment Service Service StatefulSet " ]; then
	echo "orders-api's kustomize build has kinds ${kinds:-none}, want two ConfigMaps, a Deployment, two Services and a StatefulSet" >&2
	exit 1
fi
refs=$(grep -A1 'secretKeyRef:' "$OUT/orders-api.yaml" | grep -c 'key: password' || true)
if [ "$refs" != 2 ]; then
	echo "orders-api's kustomize build has $refs secretKeyRefs to key password, want 2 (POSTGRES_PASSWORD and PGPASSWORD)" >&2
	exit 1
fi
# A react-vite web app on the same Compute and Deployment, served by nginx.
rm -rf "$OUT/storefront"
go run ./hack/render -out "$OUT/storefront" -type static-site -container storefront -display-name "Storefront" \
	Framework=react-vite Standard=ts-standard Compute=small Deployment=k8s-manifests >/dev/null
"$KUSTOMIZE" build "$OUT/storefront/deploy/k8s" >"$OUT/storefront.yaml"
# The webshop's payments-worker, a python-cli worker: a Deployment and no
# Service (#1468).
rm -rf "$OUT/payments-worker"
go run ./hack/render -out "$OUT/payments-worker" -type worker -container payments-worker -display-name "Payments worker" \
	Framework=python-cli Standard=python-standard Compute=small Deployment=k8s-manifests >/dev/null
"$KUSTOMIZE" build "$OUT/payments-worker/deploy/k8s" >"$OUT/payments-worker.yaml"
kinds=$(sed -n 's/^kind: //p' "$OUT/payments-worker.yaml" | sort | tr '\n' ' ')
if [ "$kinds" != "Deployment " ]; then
	echo "payments-worker's kustomize build has kinds ${kinds:-none}, want only a Deployment" >&2
	exit 1
fi
# A python-cli job: a Job and nothing else, annotated for Argo CD to recreate
# it at each release, with small's patch on it (#1587).
rm -rf "$OUT/nightly-sync"
go run ./hack/render -out "$OUT/nightly-sync" -type job -container nightly-sync -display-name "Nightly sync" \
	Framework=python-cli Standard=python-standard Compute=small Deployment=k8s-manifests >/dev/null
"$KUSTOMIZE" build "$OUT/nightly-sync/deploy/k8s" >"$OUT/nightly-sync.yaml"
kinds=$(sed -n 's/^kind: //p' "$OUT/nightly-sync.yaml" | sort | tr '\n' ' ')
if [ "$kinds" != "Job " ]; then
	echo "nightly-sync's kustomize build has kinds ${kinds:-none}, want only a Job" >&2
	exit 1
fi
for want in 'cpu: 50m' 'argocd.argoproj.io/sync-options: Force=true,Replace=true' 'restartPolicy: Never'; do
	if ! grep -qF -- "$want" "$OUT/nightly-sync.yaml"; then
		echo "nightly-sync's kustomize build lacks '$want': small's patch didn't find the Job, or job.yaml changed" >&2
		exit 1
	fi
done
# A kubebuilder operator with gitea-release: the Framework's config/default,
# with small's patch on the manager.
rm -rf "$OUT/backup-operator"
go run ./hack/render -out "$OUT/backup-operator" -type operator -container backup-operator -display-name "Backup Operator" \
	Framework=go-kubebuilder Standard=go-operator-standard Compute=small Deployment=gitea-release >/dev/null
"$KUSTOMIZE" build "$OUT/backup-operator/config/default" >"$OUT/backup-operator.yaml"

# The chart's GitProviders, catalog and Agents are the objects in ConfigMap
# theos-catalog, which the seed applies (hack/seed/catalog.sh): validated
# below as chart-*-catalog.yaml. With Gitea, the chart renders no
# theos.factory.io object of its own and none of them is a Helm hook, so an
# upgrade changes them in place (docs/plans/done/upgrade-in-place.md).
# catalog_objects FILE [CONFIGMAP] prints the objects in ConfigMap CONFIGMAP
# (default theos-catalog) of the render FILE.
catalog_objects() {
	awk -v cm_name="${2:-theos-catalog}" '/^---/ { cm = 0; data = 0 } $0 == "  name: " cm_name { cm = 1 }
		data { sub(/^    /, ""); print } cm && /^  objects\.yaml: \|$/ { data = 1 }' "$1"
}
for f in chart-gitea chart-gitea-bare; do
	catalog_objects "$OUT/$f.yaml" >"$OUT/$f-catalog.yaml"
	if ! grep -q '^kind: GitProvider$' "$OUT/$f-catalog.yaml"; then
		echo "The chart ($f.yaml) renders no GitProvider into ConfigMap theos-catalog" >&2
		exit 1
	fi
	outside=$(grep -A1 '^apiVersion: theos.factory.io/' "$OUT/$f.yaml" | grep '^kind:' || true)
	if [ -n "$outside" ]; then
		echo "The chart ($f.yaml) renders theos.factory.io objects outside ConfigMap theos-catalog; the seed applies them:" >&2
		sort <<<"$outside" | uniq -c >&2
		exit 1
	fi
done
if grep -q 'helm\.sh/hook' "$OUT/chart-gitea-catalog.yaml"; then
	echo "An object in ConfigMap theos-catalog is a Helm hook" >&2
	exit 1
fi
echo "The chart's GitProviders, catalog and Agents are in ConfigMap theos-catalog, none a hook"
# Without Gitea, the chart's gitProviders are the objects in ConfigMap
# theos-gitproviders, which Job theos-gitproviders applies with the
# same catalog.sh: validated below as chart-gitproviders.yaml, none a hook.
catalog_objects "$OUT/chart.yaml" theos-gitproviders >"$OUT/chart-gitproviders.yaml"
if ! grep -q '^kind: GitProvider$' "$OUT/chart-gitproviders.yaml"; then
	echo "The chart (chart.yaml) renders no GitProvider into ConfigMap theos-gitproviders" >&2
	exit 1
fi
outside=$(grep -A1 '^apiVersion: theos.factory.io/' "$OUT/chart.yaml" | grep '^kind:' || true)
if [ -n "$outside" ]; then
	echo "The chart (chart.yaml) renders theos.factory.io objects outside ConfigMap theos-gitproviders:" >&2
	sort <<<"$outside" | uniq -c >&2
	exit 1
fi
if grep -q 'helm\.sh/hook' "$OUT/chart-gitproviders.yaml"; then
	echo "A GitProvider in ConfigMap theos-gitproviders is a Helm hook" >&2
	exit 1
fi
echo "The chart's gitProviders are in ConfigMap theos-gitproviders, none a hook"
# Every GitProvider the chart renders carries a base and a go runner pool
# (hack/chart-pools.sh, #1698).
hack/chart-pools.sh "$OUT/chart-gitea-catalog.yaml" "$OUT/chart-gitea-bare-catalog.yaml" "$OUT/chart-gitproviders.yaml"
# Every Bundle the chart renders (catalog.bundles) names entries it installs
# that fit together (hack/chart-bundles.sh, #1623), and the default values
# render some.
hack/chart-bundles.sh "$OUT/chart-gitea-catalog.yaml"
if ! grep -q '^kind: Bundle$' "$OUT/chart-gitea-catalog.yaml"; then
	echo "The chart renders no Bundle into ConfigMap theos-catalog; values.yaml's catalog.bundles has python-service and go-cli" >&2
	exit 1
fi

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
if grep -A1 '^apiVersion: theos.factory.io/' "$OUT/chart-gitea-bare-catalog.yaml" |
	grep -qE '^kind: (Agent|Framework|Standard|Compute|Deployment|Connector|Domain|Bundle)$'; then
	echo "The chart renders catalog entries, bundles or Agents with catalog.enabled=false and agents.enabled=false" >&2
	exit 1
fi
echo "The chart's catalog matches templates/versions.txt; off, it renders no catalog, bundles or Agents"

# Shared credentials (docs/plans/done/shared-project-credentials.md): the chart makes
# each project's Namespace, labeled to receive the shared Secrets and kept on
# uninstall; the seed may label only the project namespaces; the operator reads
# the shared Secrets from its own namespace. chart-gitea.yaml renders project
# apps with pull Secret ghcr, chart-gitea-bare.yaml with agents.enabled=false
# and projectNamespaces.create=false (and ui.enabled=false, for the UI check
# below). That neither renders a Secret is helm-lint's check.
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
want "Namespace apps lacks the shared-secrets label" "$ns" '^    theos\.factory\.io/shared-secrets: "true"$'
want "Namespace apps is not kept on uninstall" "$ns" '^    helm\.sh/resource-policy: keep$'
if [ -n "$(doc "$OUT/chart-gitea-bare.yaml" Namespace apps)" ]; then
	echo "The chart renders Namespace apps with projectNamespaces.create=false" >&2
	exit 1
fi
for f in chart-gitea chart-gitea-bare; do
	role=$(doc "$OUT/$f.yaml" ClusterRole theos-seed-namespaces)
	want "$f: the seed's ClusterRole doesn't grant get and patch on namespaces apps only" "$role" \
		'^    resourceNames: \[apps\]$'
	want "$f: the seed's ClusterRole doesn't grant get and patch" "$role" '^    verbs: \[get, patch\]$'
	want "$f: the seed's ClusterRole isn't bound" "$(doc "$OUT/$f.yaml" ClusterRoleBinding theos-seed-namespaces)" \
		'^    name: seed$'
	want "$f: the seed doesn't label the project namespaces" "$(doc "$OUT/$f.yaml" Job seed)" '^            - name: SHARE_SECRETS$'
	want "$f: the operator lacks POD_NAMESPACE" "$(doc "$OUT/$f.yaml" Deployment theos-controller-manager)" \
		'^        - name: POD_NAMESPACE$'
done
manager=$(doc "$OUT/chart-gitea.yaml" Deployment theos-controller-manager)
want "the operator isn't told the Agents' token" "$manager" '^        - --shared-secret=claude-credentials$'
want "the operator doesn't share pull Secret ghcr" "$manager" '^        - --shared-pull-secret=ghcr$'
if grep -qE -e '--shared-(pull-)?secret' <<<"$(doc "$OUT/chart-gitea-bare.yaml" Deployment theos-controller-manager)"; then
	echo "The chart: the operator shares a Secret with agents.enabled=false and no pull Secrets" >&2
	exit 1
fi
echo "The chart makes the project Namespaces, lets the seed label them and turns sharing on"

# The UI reads pods only where the factory runs (#1080): no ClusterRole bound to
# its ServiceAccount grants pods; ClusterRole theos-ui-pods is bound by a
# RoleBinding in each namespace of its components (ui.components and Gitea's),
# and the operator binds the same rules in every project namespace
# (--ui-service-account), never without the UI. Nodes and their metrics stay
# cluster-wide.
# The ClusterRoles bound to the UI's ServiceAccount: each ClusterRoleBinding's
# roleRef name when one of its subjects is theos-ui.
bound_to_ui=$(awk 'function flush() { if (b && ui && r != "") print r; b = 0; ui = 0; r = ""; sec = "" }
	/^---/ { flush(); next } /^kind: ClusterRoleBinding$/ { b = 1 } /^[a-z]/ { sec = $1 }
	sec == "roleRef:" && /^  name: / { r = $2 } sec == "subjects:" && $0 == "  name: theos-ui" { ui = 1 }
	END { flush() }' "$OUT/chart-gitea.yaml")
want "can't find the ClusterRoles bound to the UI's ServiceAccount" "$bound_to_ui" '^theos-ui-health$'
for role in $bound_to_ui; do
	# pods in a flow list or a block list of resources
	if awk '/^  resources:/ { r = 1 } /^  [a-z]/ && !/^  resources:/ { r = 0 } /^- / { r = 0 }
		r && /(\[|, |^  - )pods(\]|,|$)/ { found = 1 } END { exit !found }' \
		<<<"$(doc "$OUT/chart-gitea.yaml" ClusterRole "$role")"; then
		echo "The chart: ClusterRole $role grants the UI pods cluster-wide; bind theos-ui-pods per namespace" >&2
		exit 1
	fi
done
want "the UI can't read nodes cluster-wide" "$(doc "$OUT/chart-gitea.yaml" ClusterRole theos-ui-health)" \
	'^  resources: \[nodes\]$'
want "ClusterRole theos-ui-pods doesn't grant pods" "$(doc "$OUT/chart-gitea.yaml" ClusterRole theos-ui-pods)" \
	'^  resources: \[pods\]$'
bound=$(awk '/^---/ { k = 0; n = 0 } /^kind: RoleBinding$/ { k = 1 } k && $0 == "  name: theos-ui-pods" { n = 1 }
	k && n && /^  namespace: / { print $2; k = 0 }' "$OUT/chart-gitea.yaml" | sort | tr '\n' ' ')
if [ "$bound" != "agent-sandbox-system gitea kube-system theos-system " ]; then
	echo "The chart binds theos-ui-pods in [$bound], not in its components' namespaces" >&2
	exit 1
fi
want "the operator doesn't let the UI read project namespaces' pods" "$manager" \
	'^        - --ui-service-account=theos-system/theos-ui$'
if grep -qF -- '--ui-service-account' <<<"$(doc "$OUT/chart-gitea-bare.yaml" Deployment theos-controller-manager)"; then
	echo "The chart: the operator names a UI ServiceAccount with ui.enabled=false" >&2
	exit 1
fi
echo "The chart lets the UI read pods only in its components' namespaces and, through the operator, the projects'"

# Chat (docs/plans/done/chat-matrix.md): off by default, chart-gitea.yaml has
# no theos-chat, homeserver or --chat-homeserver; chart-chat.yaml, with
# chat.enabled and chat.homeserver.enabled, has theos-chat, the homeserver on
# an image pinned by digest with federation and registration off, its
# accounts Job, and the operator naming it (a flag make chart drops from the
# manager template). test/chart/chat_test.go checks the rest.
for object in Deployment/theos-chat StatefulSet/theos-matrix Job/theos-matrix-accounts; do
	if [ -n "$(doc "$OUT/chart-gitea.yaml" "${object%/*}" "${object#*/}")" ]; then
		echo "The chart renders $object with chat off (the default)" >&2
		exit 1
	fi
	want "chat on renders no $object" "$(doc "$OUT/chart-chat.yaml" "${object%/*}" "${object#*/}")" '^kind: '
done
if grep -qF -- '--chat-homeserver' <<<"$(doc "$OUT/chart-gitea.yaml" Deployment theos-controller-manager)"; then
	echo "The chart: the operator names a chat homeserver with chat off" >&2
	exit 1
fi
want "the operator isn't told the in-cluster homeserver" "$(doc "$OUT/chart-chat.yaml" Deployment theos-controller-manager)" \
	'^        - --chat-homeserver=http://theos-matrix\.theos-system\.svc:8008$'
homeserver=$(doc "$OUT/chart-chat.yaml" StatefulSet theos-matrix)
want "the homeserver's image isn't pinned by digest" "$homeserver" '^ *image: "[^"@]+:[^"@]+@sha256:[0-9a-f]{64}"$'
off=$(grep -A1 -e '- name: TUWUNEL_ALLOW_FEDERATION$' -e '- name: TUWUNEL_ALLOW_REGISTRATION$' <<<"$homeserver" |
	grep -c 'value: "false"' || true)
if [ "$off" != 2 ]; then
	echo "The chart: the homeserver's federation or registration isn't off" >&2
	exit 1
fi
echo "The chart installs Chat only with chat.enabled: theos-chat, the pinned homeserver and the operator's --chat-homeserver"

# The test environment shares the Claude token (make claude-secret): the
# manager names it and its own namespace, and the namespaces make seed and the
# samples make opt in.
for want in '--shared-secret=claude-credentials' 'name: POD_NAMESPACE' 'fieldPath: metadata.namespace'; do
	if ! grep -qF -- "$want" "$OUT/kustomize-default.yaml"; then
		echo "config/default's manager lacks $want: the test environment wouldn't share claude-credentials" >&2
		exit 1
	fi
done
for f in config/samples/webshop/namespace.yaml config/samples/prorivals/namespace.yaml test/e2e/testdata/apps/namespace.yaml; do
	if ! grep -qF 'theos.factory.io/shared-secrets: "true"' "$f"; then
		echo "$f lacks label theos.factory.io/shared-secrets: \"true\": the operator wouldn't share the pull secret into it, and its agents wouldn't reach the model proxy" >&2
		exit 1
	fi
done
echo "The manager shares claude-credentials into the labeled sample namespaces"

# The CI fence (docs/design.md): the chart's runners and config/runner's take
# no traffic in, reach only DNS, Gitea and public HTTPS, and only the init
# container that registers gets the registration token (#1138).
for f in chart-gitea chart-gitea-bare; do
	go run ./hack/runner-fence "$OUT/$f.yaml"
done
go run ./hack/runner-fence config/runner/*.yaml "$OUT/kustomize-gitea.yaml"

# clusterDNS says where cluster DNS runs (#1257): chart-gitea-dns.yaml sets
# NodeLocal DNSCache's address and OpenShift's DNS pods, which the CI fence's
# DNS rule and the manager's --cluster-dns both carry; the default is kube-dns.
go run ./hack/runner-fence "$OUT/chart-gitea-dns.yaml"
fence=$(doc "$OUT/chart-gitea-dns.yaml" NetworkPolicy ci-runner)
want "the CI fence doesn't let DNS out to NodeLocal DNSCache's address" "$fence" '^ *cidr: 169\.254\.20\.10/32$'
want "the CI fence doesn't let DNS out to the openshift-dns namespace" "$fence" '^ *matchLabels: \{kubernetes\.io/metadata\.name: "openshift-dns"\}$'
want "the CI fence doesn't select OpenShift's DNS pods" "$fence" '^ *"dns\.operator\.openshift\.io/daemonset-dns": "default"$'
manager=$(doc "$OUT/chart-gitea-dns.yaml" Deployment theos-controller-manager)
want "the manager lacks --cluster-dns for NodeLocal DNSCache" "$manager" '^ *- --cluster-dns=169\.254\.20\.10/32$'
want "the manager lacks --cluster-dns for openshift-dns" "$manager" '^ *- --cluster-dns=openshift-dns/dns\.operator\.openshift\.io/daemonset-dns=default$'
want "the manager's default --cluster-dns isn't kube-dns in kube-system" \
	"$(doc "$OUT/chart-gitea.yaml" Deployment theos-controller-manager)" '^ *- --cluster-dns=kube-system/k8s-app=kube-dns$'
echo "The CI fence and the manager take clusterDNS"

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
