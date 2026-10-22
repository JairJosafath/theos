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

# make check-generated, run from the real Makefile in a scratch repository
# (its includes resolve through -I): a chart CRD changed and not committed,
# and one untracked, which git diff never saw (#1705).
R=$T/repo
git init -q -b main "$R"
mkdir -p "$R/config/crd/bases" "$R/config/rbac" "$R/api" "$R/cmd" "$R/internal" "$R/dist/chart/templates/crd" "$R/dist/chart/templates/rbac"
cp "$ROOT/config/crd/bases/c4.factory.io_people.yaml" "$R/config/crd/bases/"
cp "$ROOT/dist/chart/templates/crd/people.c4.factory.io.yaml" "$R/dist/chart/templates/crd/"
echo readme >"$R/README.md"
git -C "$R" add -A && git -C "$R" -c user.name=t -c user.email=t@t commit -qm init
gen() { make -I "$ROOT" -f "$ROOT/Makefile" -C "$R" check-generated >"$T/gen" 2>/dev/null; }
gen; check "make check-generated on a clean tree: passes" $? 0
echo changed >>"$R/README.md"
gen; check "make check-generated with a change outside the generated paths: passes" $? 0
echo '# edited' >>"$R/dist/chart/templates/crd/people.c4.factory.io.yaml"
cp "$R/config/crd/bases/c4.factory.io_people.yaml" "$R/dist/chart/templates/crd/new.c4.factory.io.yaml"
gen; check "make check-generated on a chart CRD left uncommitted and one untracked: fails" $? 2
grep -q '^ M dist/chart/templates/crd/people.c4.factory.io.yaml$' "$T/gen"; check "names the uncommitted chart CRD" $? 0
grep -q '^?? dist/chart/templates/crd/new.c4.factory.io.yaml$' "$T/gen"; check "names the untracked chart CRD (#1705)" $? 0
grep -q 'run make manifests generate and commit them' "$T/gen"; check "says how to fix it" $? 0
git -C "$R" add -A && git -C "$R" -c user.name=t -c user.email=t@t commit -qm crds
gen; check "make check-generated once they are committed: passes" $? 0
cp "$R/config/crd/bases/c4.factory.io_people.yaml" "$R/dist/chart/templates/crd/other.c4.factory.io.yaml"
gen; check "make check-generated on an untracked chart CRD alone: fails (#1705)" $? 2

exit $fail
