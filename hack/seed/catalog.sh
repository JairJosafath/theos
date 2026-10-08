#!/usr/bin/env bash
# Applies FILE, the chart's GitProviders, catalog and Agents (ConfigMap
# c4factory-catalog), with server-side apply as field manager c4factory-chart:
# a new object is created, one already there changes in place and keeps its UID
# and every field the chart doesn't set (an Agent's env a person added). The
# first apply takes over the objects an older chart made as Helm hooks. The
# seed runs it last; test/chart runs it against envtest.
#
#   catalog.sh FILE
set -euo pipefail

file=${1:?usage: catalog.sh FILE}
if ! grep -q '^kind:' "$file"; then
  echo "$(date -u +%H:%M:%S) seed: $file holds no objects; nothing to apply"
  exit 0
fi
kubectl apply --server-side --force-conflicts --field-manager=c4factory-chart -f "$file"
