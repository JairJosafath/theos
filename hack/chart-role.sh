#!/usr/bin/env bash
# make validate-yaml: fails when the rules of the Helm chart's manager role
# differ from config/rbac/role.yaml, which make manifests generates from the
# controllers' RBAC markers. make manifests regenerates the chart's copy too
# (--write), as make chart does: a marker fixed only in config/ left the chart
# without it (#371, #1689).
# Run: hack/chart-role.sh [--write] [ROLE CHART_ROLE]
#   --write  give the chart's role the rules of ROLE instead of checking,
#            rewriting it only when they differ; says when it did.
set -uo pipefail

write=
if [ "${1:-}" = --write ]; then write=1; shift; fi
ROLE=${1:-config/rbac/role.yaml}
CHART=${2:-dist/chart/templates/rbac/manager-role.yaml}

rules() { sed -n '/^rules:$/,$p' "$1"; }

if [ -z "$(rules "$ROLE")" ] || [ -z "$(rules "$CHART")" ]; then
	echo "$ROLE or $CHART has no rules" >&2
	exit 1
fi
if [ -n "$write" ]; then
	if ! rules "$ROLE" | cmp -s - <(rules "$CHART"); then
		{ sed '/^rules:$/,$d' "$CHART"; rules "$ROLE"; } >"$CHART.tmp" && mv "$CHART.tmp" "$CHART" &&
			echo "$CHART: rules written from $ROLE"
	fi
	exit 0
fi
if ! diff -u --label "$ROLE" --label "$CHART" <(rules "$ROLE") <(rules "$CHART") >&2; then
	echo "The chart's manager role differs from $ROLE; run make manifests and commit dist/chart." >&2
	exit 1
fi
echo "The chart's manager role matches $ROLE"
