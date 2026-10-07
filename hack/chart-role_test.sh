#!/usr/bin/env bash
# Tests for hack/chart-role.sh, the check that the chart's manager role
# matches config/rbac/role.yaml, against copies in a temporary directory.
# Run: hack/chart-role_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
ROOT=$(dirname "$HACK")
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
fresh() { cp "$ROOT/config/rbac/role.yaml" "$T/role.yaml"; cp "$ROOT/dist/chart/templates/rbac/manager-role.yaml" "$T/chart.yaml"; }
run() { "$HACK/chart-role.sh" "$T/role.yaml" "$T/chart.yaml" >"$T/out" 2>&1; }

fresh; run; check "the committed chart matches" $? 0

fresh; sed -i '0,/^  - people$/s//  - persons/' "$T/role.yaml"
run; check "a resource renamed by a marker, chart not regenerated: fails" $? 1
grep -q '^-  - persons' "$T/out"; check "the diff names the resource" $? 0
grep -q 'run make chart' "$T/out"; check "says how to fix it" $? 0

fresh; printf 'kind: ClusterRole\n' >"$T/chart.yaml"
run; check "a chart role without rules: fails" $? 1

exit $fail
