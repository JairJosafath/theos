#!/usr/bin/env bash
# make validate-yaml: fails when the rules of the Helm chart's manager role
# differ from config/rbac/role.yaml, which make manifests generates from the
# controllers' RBAC markers. make chart regenerates the chart's copy; make
# manifests alone does not, and a marker fixed only in config/ left the chart
# without it (#371).
# Run: hack/chart-role.sh [ROLE CHART_ROLE]
set -uo pipefail

ROLE=${1:-config/rbac/role.yaml}
CHART=${2:-dist/chart/templates/rbac/manager-role.yaml}

rules() { sed -n '/^rules:$/,$p' "$1"; }

if [ -z "$(rules "$ROLE")" ] || [ -z "$(rules "$CHART")" ]; then
	echo "$ROLE or $CHART has no rules" >&2
	exit 1
fi
if ! diff -u --label "$ROLE" --label "$CHART" <(rules "$ROLE") <(rules "$CHART") >&2; then
	echo "The chart's manager role differs from $ROLE; run make chart and commit dist/chart." >&2
	exit 1
fi
echo "The chart's manager role matches $ROLE"
