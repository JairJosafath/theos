#!/usr/bin/env bash
# Tests for hack/chart-manager.sh, the check that the chart's manager
# Deployment keeps what make chart's helm plugin drops, against renders of
# the committed chart and edited copies of it in a temporary directory.
# Run: hack/chart-manager_test.sh (needs bin/helm: make install-helm)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
ROOT=$(dirname "$HACK")
HELM=${HELM:-$ROOT/bin/helm}
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
if ! [ -x "$HELM" ]; then
	echo "FAIL no helm at $HELM; run make install-helm"
	exit 1
fi
MANIFEST=templates/manager/manager.yaml
# edited SED: render a copy of the chart whose manager template SED edits.
edited() {
	rm -rf "$T/chart"
	cp -r "$ROOT/dist/chart" "$T/chart"
	sed -i "$1" "$T/chart/$MANIFEST"
	"$HELM" template theos "$T/chart" --namespace theos-system \
		--set 'gitea.projects={apps},global.imagePullSecrets[0].name=ghcr' >"$T/rendered.yaml"
}
run() { "$HACK/chart-manager.sh" "$T/rendered.yaml" >"$T/out" 2>&1; }

edited ''; run; check "the committed chart keeps them" $? 0

edited '/mountPath: \/tmp$/d'; run; check "no /tmp mount: fails" $? 1
grep -q 'lacks the /tmp volume mount' "$T/out"; check "names the mount" $? 0
grep -q 'restore them in dist/chart' "$T/out"; check "says how to fix it" $? 0

edited '/sizeLimit: 1Gi$/d; /emptyDir:$/d'; run; check "no tmp volume: fails" $? 1
grep -q 'lacks the tmp emptyDir volume' "$T/out"; check "names the volume" $? 0

edited '/- name: POD_NAMESPACE$/,/fieldPath: metadata.namespace$/d'; run; check "no POD_NAMESPACE: fails" $? 1
grep -q 'lacks the POD_NAMESPACE env' "$T/out"; check "names the env" $? 0

edited '/- name: THEOS_CHART_VERSION$/,+1d'; run; check "no THEOS_CHART_VERSION: fails" $? 1
grep -q 'lacks THEOS_CHART_VERSION' "$T/out"; check "names the chart version env" $? 0

edited 's/value: {{ .Chart.Version | quote }}/value: "0.0.1"/'; run; check "THEOS_CHART_VERSION not the chart's: fails" $? 1

edited '/--shared-secret=/d'; run; check "no --shared-secret: fails" $? 1
edited '/--shared-pull-secret=/d'; run; check "no --shared-pull-secret: fails" $? 1
edited '/--model-proxy-url=/d'; run; check "no --model-proxy-url: fails" $? 1
edited '/--ui-service-account=/d'; run; check "no --ui-service-account: fails" $? 1
grep -q 'lacks the --ui-service-account flag' "$T/out"; check "names the UI flag" $? 0
edited '/--webhook-service=/d'; run; check "no --webhook-service: fails" $? 1
grep -q 'lacks the --webhook-service flag' "$T/out"; check "names the webhook flag" $? 0
edited '/--webhook-cert-secret=/d'; run; check "no --webhook-cert-secret: fails" $? 1
edited '/name: webhook-server/d'; run; check "no webhook-server port: fails" $? 1
edited '/--git-events-bind-address=/d'; run; check "no --git-events-bind-address: fails" $? 1
grep -q 'lacks the --git-events-bind-address flag' "$T/out"; check "names the receiver's flag" $? 0
edited '/--git-events-url=/d'; run; check "no --git-events-url: fails" $? 1
grep -q 'lacks the --git-events-url flag' "$T/out"; check "names the URL flag" $? 0
edited '/name: git-events$/d'; run; check "no git-events port: fails" $? 1
grep -q 'lacks the git-events container port' "$T/out"; check "names the port" $? 0
edited '/--test-environment-tools-image=/d'; run; check "no --test-environment-tools-image: fails" $? 1
grep -q 'lacks the --test-environment-tools-image flag' "$T/out"; check "names the tools image flag" $? 0
edited '/--test-environment-tools-url=/d'; run; check "no --test-environment-tools-url: fails" $? 1
grep -q 'lacks the --test-environment-tools-url flag' "$T/out"; check "names the tools URL flag" $? 0
edited '/--test-environment-runner-url=/d'; run; check "no --test-environment-runner-url: fails" $? 1
grep -q 'lacks the --test-environment-runner-url flag' "$T/out"; check "names the runner URL flag" $? 0

printf 'kind: ConfigMap\n' >"$T/rendered.yaml"
run; check "a render without the manager: fails" $? 1

exit $fail
