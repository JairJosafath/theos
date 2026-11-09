#!/usr/bin/env bash
# Tests for the factory's dashboard and alerts in the chart (#59): the
# Grafana dashboard is JSON with panels, the alerts are a PrometheusRule, and
# every theos_ series either of them or docs/monitoring.md names is one
# internal/model's
# FactoryMetrics serves (a histogram's _bucket, _sum and _count included), so
# a renamed metric can't leave a panel or an alert empty.
# Run: hack/monitoring_test.sh
ROOT=$(cd "$(dirname "$0")/.." && pwd)
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
DASH=$ROOT/dist/chart/files/theos-dashboard.json
ALERTS=$ROOT/dist/chart/templates/prometheus/factory-rules.yaml
GUIDE=$ROOT/docs/monitoring.md

served=$(sed -n 's/^\tMetric[A-Za-z]* *= "\(theos_[a-z_]*\)"$/\1/p' "$ROOT/internal/model/factory_metrics.go" | sort -u)
check "internal/model serves the factory's series" "$([ "$(wc -l <<<"$served")" -ge 8 ] && echo yes)" yes

check "the dashboard is JSON" "$(jq -e . "$DASH" >/dev/null 2>&1 && echo yes)" yes
check "the dashboard has panels, each with a query" \
  "$(jq '[.panels[] | select((.targets | length) == 0)] | length' "$DASH")" 0
check "the dashboard's panel ids are distinct" "$(jq '[.panels[].id] | length - (unique | length)' "$DASH")" 0
check "the alerts are a PrometheusRule" "$(grep -c '^kind: PrometheusRule$' "$ALERTS")" 1
check "every alert has a summary" \
  "$(grep -c '^        - alert:' "$ALERTS")" "$(grep -c '^            summary:' "$ALERTS")"

for f in "$DASH" "$ALERTS" "$GUIDE"; do
  for name in $(grep -o 'theos_[a-z_]*' "$f" | sort -u); do
    base=${name%_bucket} base=${base%_sum} base=${base%_count}
    check "${f##*/} names $name, which the manager serves" "$(grep -cx -e "$name" -e "$base" <<<"$served" | head -1)" 1
  done
done
for name in $served; do
  check "docs/monitoring.md lists $name" "$(grep -c "^| \`$name\`" "$GUIDE")" 1
done

exit $fail
