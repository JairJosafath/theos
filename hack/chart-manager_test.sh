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
	"$HELM" template c4-factory "$T/chart" --namespace c4-factory-system \
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

edited '/- name: C4_CHART_VERSION$/,+1d'; run; check "no C4_CHART_VERSION: fails" $? 1
grep -q 'lacks C4_CHART_VERSION' "$T/out"; check "names the chart version env" $? 0

edited 's/value: {{ .Chart.Version | quote }}/value: "0.0.1"/'; run; check "C4_CHART_VERSION not the chart's: fails" $? 1

edited '/--shared-secret=/d'; run; check "no --shared-secret: fails" $? 1
edited '/--shared-pull-secret=/d'; run; check "no --shared-pull-secret: fails" $? 1

printf 'kind: ConfigMap\n' >"$T/rendered.yaml"
run; check "a render without the manager: fails" $? 1

exit $fail
