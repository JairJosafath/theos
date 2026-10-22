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

# The committed tree itself: on a failure its diff goes to the log, since a
# stale chart on main reads as a flake without it (#1799).
fresh; run; st=$?; check "the committed chart matches" $st 0
[ "$st" = 0 ] || sed 's/^/     /' "$T/out"

# A CRD whose kind's versions share a shape converts by the API server,
# strategy None, webhook.enabled or not (#1830). One whose base is marked
# c4.factory.io/conversion: webhook converts by the operator's webhook at
# /convert under webhook.enabled (#1615), and the render fails without it.
# The committed chart follows the committed bases' marks, whichever they are.
mark='^    c4.factory.io/conversion: webhook$'
n=0; for base in "$ROOT"/config/crd/bases/*.yaml; do
	name=$(basename "$base" .yaml)
	want=None; grep -q "$mark" "$base" && want=Webhook
	awk -v want="$want" '$0 == "spec:" { s = 1; next }
		s == 1 { ok = $0 == "  conversion:"; s = 2; next }
		s == 2 { ok = ok && $0 == "    strategy: " want; exit }
		END { exit !ok }' "$ROOT/dist/chart/templates/crd/${name#*_}.${name%%_*}.yaml" || n=$((n + 1))
done
check "every chart CRD converts by the strategy its base's mark says (#1830)" $n 0
check "the chart CRDs naming /convert are the marked bases (#1830)" \
	"$(grep -l 'path: /convert' "$ROOT"/dist/chart/templates/crd/*.yaml | wc -l)" "$(grep -l "$mark" "$ROOT"/config/crd/bases/*.yaml | wc -l)"

HELM=${HELM:-$ROOT/bin/helm}
if [ -x "$HELM" ]; then
	fresh
	sed -i 's|^  annotations:$|  annotations:\n    c4.factory.io/conversion: webhook|' "$T/bases/c4.factory.io_people.yaml"
	"$HACK/chart-crds.sh" --write "$T/bases" "$T/crd" >/dev/null 2>&1
	f=$T/crd/people.c4.factory.io.yaml
	awk '$0 == "spec:" { s = 1; next }
		s == 1 && $0 == "  conversion:" { c = 1 }
		s == 1 && c && $0 == "    strategy: Webhook" { w = 1 }
		s == 1 && c && /path: \/convert$/ { p = 1 }
		s == 1 && $0 == "  group: c4.factory.io" { exit }
		END { exit !(w && p) }' "$f"; check "a kind marked as differing converts at /convert by webhook (#1830)" $? 0
	grep -q 'strategy: None' "$f"; check "and not by strategy None" $? 1
	rm -rf "$T/chart"; cp -r "$ROOT/dist/chart" "$T/chart"; cp "$f" "$T/chart/templates/crd/"
	render() { "$HELM" template c4-factory "$T/chart" --namespace c4-factory-system --set "webhook.enabled=$1" >"$T/rendered.yaml" 2>"$T/render-err"; }
	render true; check "the chart renders it with webhook.enabled" $? 0
	check "the rendered CRD converts at the release's webhook Service" \
		"$(awk '/^  name: people.c4.factory.io$/ { s = 1 } s && /path: \/convert$/ { f = 1 } s && /^---$/ { exit } END { print f + 0 }' "$T/rendered.yaml")" 1
	check "the rest stay at strategy None" "$(grep -c 'strategy: None' "$T/rendered.yaml")" "$(grep -L 'path: /convert' "$T"/crd/*.yaml | wc -l)"
	render false; check "without webhook.enabled the render fails (#1830)" $? 1
	grep -q 'people.c4.factory.io: its versions differ in shape' "$T/render-err"; check "and names the kind" $? 0
else
	echo "FAIL the marked-kind render specs: no helm at $HELM (make install-helm; make test-hack installs it)"; fail=1
fi

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
