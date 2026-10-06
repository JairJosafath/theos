#!/usr/bin/env bash
# Tests for hack/chart-crds.sh, the check that the chart's CRDs match
# config/crd/bases, against copies in a temporary directory.
# Run: hack/chart-crds_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
ROOT=$(dirname "$HACK")
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
fresh() { rm -rf "$T/bases" "$T/crd"; cp -r "$ROOT/config/crd/bases" "$T/bases"; cp -r "$ROOT/dist/chart/templates/crd" "$T/crd"; }
run() { "$HACK/chart-crds.sh" "$T/bases" "$T/crd" >"$T/out" 2>&1; }

fresh; run; check "the committed chart matches" $? 0

fresh
sed -i 's/^            properties:$/            properties:\n              addedField:\n                type: string/' "$T/bases/c4.factory.io_people.yaml"
run; check "a field added to the API, chart not regenerated: fails" $? 1
grep -q '^-.*addedField:' "$T/out"; check "the diff names the field" $? 0
grep -q 'run make chart' "$T/out"; check "says how to fix it" $? 0

fresh; rm "$T/crd/people.c4.factory.io.yaml"
run; check "a CRD missing from the chart: fails" $? 1
grep -q 'people.c4.factory.io.yaml: missing' "$T/out"; check "names the missing file" $? 0

fresh; cp "$T/crd/people.c4.factory.io.yaml" "$T/crd/gone.c4.factory.io.yaml"
run; check "a chart CRD with no base: fails" $? 1
grep -q 'gone.c4.factory.io.yaml: no CRD' "$T/out"; check "names the left-over file" $? 0

exit $fail
