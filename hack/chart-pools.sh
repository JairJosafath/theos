#!/usr/bin/env bash
# make validate-yaml: every GitProvider the chart renders carries a runner
# pool with a base and a go runner, the labels the templates' check workflows
# run on, since the shared runners serve the templates organization only and
# a project's checks run on the pool its GitProvider's ci block names (#1698;
# test/chart/gitprovider_pool_test.go checks the images and sizes). A render
# whose runners lack a label fails with the file, the counts and the label;
# grep -c exits 1 on zero matches, so each count reads it with || true, else
# set -e would end the script before the message (#1730).
# Run: hack/chart-pools.sh RENDERED..., each a file of the chart's GitProviders
# (make validate-yaml's chart-gitea-catalog.yaml, chart-gitea-bare-catalog.yaml
# and chart-gitproviders.yaml).
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: hack/chart-pools.sh RENDERED..." >&2; exit 2; }
for f in "$@"; do
	providers=$(grep -c '^kind: GitProvider$' "$f" || true)
	for label in base go; do
		pools=$(grep -c "^ *- label: $label$" "$f" || true)
		if [ "$pools" -ne "$providers" ]; then
			echo "The chart ($(basename "$f")) renders $providers GitProviders but $pools $label runners in spec.ci.giteaActions.runners; a project's checks run on that pool (#1698)" >&2
			exit 1
		fi
	done
done
echo "Every GitProvider the chart renders carries a base and a go runner pool"
