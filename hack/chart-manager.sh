#!/usr/bin/env bash
# make validate-yaml: fails when the manager Deployment the Helm chart renders
# lacks what dist/chart/templates/manager/manager.yaml adds by hand: the /tmp
# emptyDir a Go module is repacked in on the read-only root filesystem
# (internal/gitprovider), the POD_NAMESPACE the shared Secrets are read from,
# the --shared-secret, --shared-pull-secret and --model-proxy-url flags, the --ui-service-account
# the UI reads project namespaces' pods as (#1080), the webhook's flags and
# port (#1614), the Git events receiver's flags and port (#1223), and the THEOS_CHART_VERSION
# the Project status reports, the chart's version. kubebuilder's helm
# plugin regenerates that template without them; make chart keeps the file,
# and this check catches a hand edit that loses them (#543).
# Run: hack/chart-manager.sh RENDERED, a helm template of the chart with
# agents on and global.imagePullSecrets[0].name=ghcr (make helm-lint's
# chart-gitea.yaml).
set -uo pipefail

RENDERED=${1:?usage: hack/chart-manager.sh RENDERED}
SOURCE=templates/manager/manager.yaml
CHART=${CHART:-$(cd "$(dirname "$0")/.." && pwd)/dist/chart}
version=$(sed -n 's/^version: *//p' "$CHART/Chart.yaml")

doc=$(awk -v s="$SOURCE" '/^# Source: /{ p = (substr($0, length($0) - length(s) + 1) == s) } p' "$RENDERED")
if [ -z "$doc" ]; then
	echo "$RENDERED renders no $SOURCE" >&2
	exit 1
fi

fail=0
need() { # need WHAT GREP_ARGS...: the manager Deployment matches, or says what it lacks.
	local what=$1
	shift
	if ! grep -q "$@" <<<"$doc"; then
		echo "The chart's manager Deployment lacks $what" >&2
		fail=1
	fi
}
need "the --shared-secret flag" -E '^ *- --shared-secret=[^ ]+$'
need "the --shared-pull-secret flag" -E '^ *- --shared-pull-secret=ghcr$'
need "the --model-proxy-url flag" -E '^ *- --model-proxy-url=http://[^ ]+\.svc:8080$'
need "the --ui-service-account flag" -E '^ *- --ui-service-account=[^/ ]+/[^/ ]+$'
need "the --webhook-service flag" -E '^ *- --webhook-service=[^ ]+-webhook-service$'
need "the --webhook-cert-secret flag" -E '^ *- --webhook-cert-secret=[^ ]+-webhook-cert$'
need "the webhook-server container port" -Pzo '\n *- containerPort: [0-9]+\n *name: webhook-server\n'
need "the --git-events-bind-address flag" -E '^ *- --git-events-bind-address=:[0-9]+$'
need "the --git-events-url flag" -E '^ *- --git-events-url=http://[^ ]+-git-events\.[^ ]+\.svc:[0-9]+$'
need "the git-events container port" -Pzo '\n *- containerPort: [0-9]+\n *name: git-events\n'
need "the POD_NAMESPACE env from metadata.namespace" -Pzo \
	'\n *- name: POD_NAMESPACE\n *valueFrom:\n *fieldRef:\n *fieldPath: metadata\.namespace\n'
need "THEOS_CHART_VERSION set to the chart's version, $version" -Pzo \
	"\n *- name: THEOS_CHART_VERSION\n *value: \"${version//./\\.}\"\n"
need "the /tmp volume mount" -Pzo '\n *- name: tmp\n *mountPath: /tmp\n'
need "the tmp emptyDir volume" -Pzo '\n *- name: tmp\n *emptyDir:'
if [ "$fail" -ne 0 ]; then
	echo "kubebuilder's helm plugin drops these; restore them in dist/chart/$SOURCE." >&2
	exit 1
fi
echo "The chart's manager Deployment keeps /tmp, POD_NAMESPACE, THEOS_CHART_VERSION, the shared Secrets, the model proxy URL, the UI's ServiceAccount, the webhook and the Git events receiver"
