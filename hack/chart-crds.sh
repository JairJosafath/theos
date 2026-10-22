#!/usr/bin/env bash
# make validate-yaml: fails when a CRD of the Helm chart differs from
# config/crd/bases beyond the chart's template guard, or when one is missing or
# left over. make manifests regenerates them (--write), as make chart does: a
# base regenerated without the chart landed under yolo mode three times in a
# day and went red on main each time (#1689).
# Run: hack/chart-crds.sh [--write] [BASES_DIR CHART_CRD_DIR]
#   --write  make the chart's CRDs match the bases instead of checking: a
#            guarded copy per base, rewritten only when it differs, left-overs
#            removed; says what it wrote.
set -uo pipefail

write=
if [ "${1:-}" = --write ]; then write=1; shift; fi
BASES=${1:-config/crd/bases}
CHART=${2:-dist/chart/templates/crd}

# The chart's copy of a base CRD: kubebuilder's helm plugin wraps it in
# crd.enabled and adds the crd.keep annotation. With webhook.enabled it
# converts by the operator's webhook, which patches its CA in
# (docs/plans/api-versions.md, decision 3); without, by strategy None, which
# serves while the versions' shapes match.
guarded() {
	awk 'NR == 1 && $0 == "---" { print "{{- if .Values.crd.enabled }}"; next }
		{ print }
		!done && $0 == "  annotations:" {
			print "    {{- if .Values.crd.keep }}"
			print "    \"helm.sh/resource-policy\": keep"
			print "    {{- end }}"
			done = 1
		}
		$0 == "spec:" {
			print "  {{- if .Values.webhook.enabled }}"
			print "  conversion:"
			print "    strategy: Webhook"
			print "    webhook:"
			print "      clientConfig:"
			print "        service:"
			print "          name: {{ include \"c4-factory.resourceName\" (dict \"suffix\" \"webhook-service\" \"context\" $) }}"
			print "          namespace: {{ .Release.Namespace }}"
			print "          path: /convert"
			print "      conversionReviewVersions:"
			print "      - v1"
			print "  {{- end }}"
		}
		END { print "{{- end }}" }' "$1"
}

fail=0
want=()
[ -n "$write" ] && mkdir -p "$CHART"
for base in "$BASES"/*.yaml; do
	name=$(basename "$base" .yaml)
	group=${name%%_*}
	chart=$CHART/${name#*_}.$group.yaml
	want+=("$chart")
	if [ -n "$write" ]; then
		if [ ! -f "$chart" ] || ! guarded "$base" | cmp -s - "$chart"; then
			guarded "$base" >"$chart" && echo "$chart: written from $base"
		fi
	elif [ ! -f "$chart" ]; then
		echo "$chart: missing, for $base" >&2
		fail=1
	elif ! diff -u --label "$base (guarded)" --label "$chart" <(guarded "$base") "$chart" >&2; then
		fail=1
	fi
done
for chart in "$CHART"/*.yaml; do
	[[ " ${want[*]} " == *" $chart "* ]] && continue
	if [ -n "$write" ]; then
		rm -f "$chart" && echo "$chart: removed, no CRD in $BASES makes it"
		continue
	fi
	echo "$chart: no CRD in $BASES makes it" >&2
	fail=1
done
[ -n "$write" ] && exit 0
if [ "$fail" -ne 0 ]; then
	echo "The chart's CRDs differ from $BASES; run make manifests and commit dist/chart." >&2
	exit 1
fi
echo "The chart's CRDs match $BASES"
