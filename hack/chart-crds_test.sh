#!/usr/bin/env bash
# Tests for hack/chart-crds.sh, the check that the chart's CRDs match
# config/crd/bases, against copies in a temporary directory.
# Run: hack/chart-crds_test.sh
# The --write specs are make manifests' sync of the chart (#1689).
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
grep -q 'run make manifests' "$T/out"; check "says how to fix it" $? 0

fresh; rm "$T/crd/people.c4.factory.io.yaml"
run; check "a CRD missing from the chart: fails" $? 1
grep -q 'people.c4.factory.io.yaml: missing' "$T/out"; check "names the missing file" $? 0

fresh; cp "$T/crd/people.c4.factory.io.yaml" "$T/crd/gone.c4.factory.io.yaml"
run; check "a chart CRD with no base: fails" $? 1
grep -q 'gone.c4.factory.io.yaml: no CRD' "$T/out"; check "names the left-over file" $? 0

# --write, what make manifests runs (#1689): the bases gain a field, the chart
# lacks a CRD and has one no base makes.
fresh
sed -i 's/^            properties:$/            properties:\n              addedField:\n                type: string/' "$T/bases/c4.factory.io_people.yaml"
rm "$T/crd/people.c4.factory.io.yaml"
cp "$T/crd/standards.c4.factory.io.yaml" "$T/crd/gone.c4.factory.io.yaml"
touch -d '2001-01-01' "$T/crd/standards.c4.factory.io.yaml"
"$HACK/chart-crds.sh" --write "$T/bases" "$T/crd" >"$T/wrote" 2>&1; check "--write exits 0" $? 0
run; check "--write makes the chart match the bases" $? 0
grep -q 'addedField' "$T/crd/people.c4.factory.io.yaml"; check "the written CRD has the field" $? 0
[ ! -e "$T/crd/gone.c4.factory.io.yaml" ]; check "--write removes the left-over file" $? 0
[ "$(stat -c %Y "$T/crd/standards.c4.factory.io.yaml")" = "$(date -d 2001-01-01 +%s)" ]; check "--write leaves a matching file untouched" $? 0
check "--write says what it wrote" "$(grep -c 'written from\|removed' "$T/wrote")" 2

make -n -C "$ROOT" manifests 2>/dev/null | grep -q 'chart-crds.sh --write'; check "make manifests runs it" $? 0
make -n -C "$ROOT" check-generated 2>/dev/null | grep -q 'git diff --quiet -- .*dist/chart/templates/crd'; check "make check-generated fails on a chart CRD left uncommitted" $? 0

exit $fail
