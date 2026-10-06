#!/usr/bin/env bash
# make validate-yaml: fails when a CRD of the Helm chart differs from
# config/crd/bases beyond the chart's template guard, or when one is missing or
# left over. make chart regenerates them; make manifests alone does not.
# Run: hack/chart-crds.sh [BASES_DIR CHART_CRD_DIR]
set -uo pipefail

BASES=${1:-config/crd/bases}
CHART=${2:-dist/chart/templates/crd}

# The chart's copy of a base CRD: kubebuilder's helm plugin wraps it in
# crd.enabled and adds the crd.keep annotation.
guarded() {
	awk 'NR == 1 && $0 == "---" { print "{{- if .Values.crd.enabled }}"; next }
		{ print }
		!done && $0 == "  annotations:" {
			print "    {{- if .Values.crd.keep }}"
			print "    \"helm.sh/resource-policy\": keep"
			print "    {{- end }}"
			done = 1
		}
		END { print "{{- end }}" }' "$1"
}

fail=0
want=()
for base in "$BASES"/*.yaml; do
	name=$(basename "$base" .yaml)
	group=${name%%_*}
	chart=$CHART/${name#*_}.$group.yaml
	want+=("$chart")
	if [ ! -f "$chart" ]; then
		echo "$chart: missing, for $base" >&2
		fail=1
	elif ! diff -u --label "$base (guarded)" --label "$chart" <(guarded "$base") "$chart" >&2; then
		fail=1
	fi
done
for chart in "$CHART"/*.yaml; do
	[[ " ${want[*]} " == *" $chart "* ]] && continue
	echo "$chart: no CRD in $BASES makes it" >&2
	fail=1
done
if [ "$fail" -ne 0 ]; then
	echo "The chart's CRDs differ from $BASES; run make chart and commit dist/chart." >&2
	exit 1
fi
echo "The chart's CRDs match $BASES"
