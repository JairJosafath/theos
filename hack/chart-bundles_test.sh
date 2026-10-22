#!/usr/bin/env bash
# Tests for hack/chart-bundles.sh (#1623): a render whose Bundles name entries
# it installs that fit together passes and says how many; a Bundle naming an
# entry the chart doesn't install, one whose entries share no Container type,
# one whose Standard doesn't support its Framework, or one that gives a
# Framework deploying itself a Compute fails, naming the Bundle and what is
# wrong. Entries live per namespace, as the chart renders them.
# Run: hack/chart-bundles_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK=$(cd "$(dirname "$0")" && pwd)
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# entry FILE NS KIND NAME TYPES [FRAMEWORKS] [self]: appends a catalog entry
# with supportedTypes TYPES (space-separated), a Standard's supportedFrameworks,
# and deploysItself when the last word is self.
entry() {
	local f=$1 ns=$2 kind=$3 name=$4 types=$5 fws=${6:-} self=${7:-}
	{
		echo "---"
		echo "apiVersion: c4.factory.io/v1alpha1"
		echo "kind: $kind"
		echo "metadata:"
		echo "  name: $name"
		echo "  namespace: $ns"
		echo "spec:"
		[ "$self" = self ] && echo "  deploysItself: true"
		echo "  displayName: $name"
		[ -n "$fws" ] && { echo "  supportedFrameworks:"; for w in $fws; do echo "  - $w"; done; }
		echo "  supportedTypes:"
		for t in $types; do echo "  - $t"; done
		echo "  template:"
		echo "    url: http://gitea/templates/$name"
		echo "    ref: v1.0.0"
	} >>"$f"
}
# bundle FILE NS NAME SIDE=ENTRY...: appends a Bundle naming the given sides.
bundle() {
	local f=$1 ns=$2 name=$3
	shift 3
	{
		echo "---"
		echo "apiVersion: c4.factory.io/v1alpha1"
		echo "kind: Bundle"
		echo "metadata:"
		echo "  name: $name"
		echo "  namespace: $ns"
		echo "spec:"
		echo "  description: picked together"
		for s in "$@"; do
			echo "  ${s%%=*}Ref:"
			echo "    name: ${s#*=}"
		done
	} >>"$f"
}
catalog() {
	local f=$1 ns=$2
	entry "$f" "$ns" Framework python-fastapi service
	entry "$f" "$ns" Framework go-cli cli
	entry "$f" "$ns" Framework aws-lambda-ts function "" self
	entry "$f" "$ns" Standard python-standard "service cli worker" "python-fastapi python-cli"
	entry "$f" "$ns" Standard go-standard cli go-cli
	entry "$f" "$ns" Standard aws-standard "function service" aws-lambda-ts
	entry "$f" "$ns" Compute small "service worker job static-site"
	entry "$f" "$ns" Deployment k8s-manifests "service worker job static-site"
	entry "$f" "$ns" Deployment gitea-release "cli extension function service"
}
run() { "$HACK/chart-bundles.sh" "$@" >"$T/out" 2>&1; }

: >"$T/good.yaml"
for ns in apps shop; do
	catalog "$T/good.yaml" "$ns"
	bundle "$T/good.yaml" "$ns" python-service framework=python-fastapi standard=python-standard compute=small deployment=k8s-manifests
	bundle "$T/good.yaml" "$ns" go-cli framework=go-cli standard=go-standard deployment=gitea-release
	bundle "$T/good.yaml" "$ns" aws-fn framework=aws-lambda-ts standard=aws-standard
done
run "$T/good.yaml"
check "bundles naming installed entries that fit pass" $? 0
check "a pass counts them" "$(grep -c '^6 Bundles in good.yaml name entries the chart installs' "$T/out")" 1

: >"$T/missing.yaml"
catalog "$T/missing.yaml" apps
bundle "$T/missing.yaml" apps rust-service framework=rust-axum standard=python-standard compute=small deployment=k8s-manifests
bundle "$T/missing.yaml" apps go-cli framework=go-cli standard=go-standard deployment=gitea-release
run "$T/good.yaml" "$T/missing.yaml"
check "a bundle naming an entry the chart doesn't install fails" $? 1
check "it names the file, the bundle and the entry" \
	"$(grep -c '^The chart (missing.yaml): Bundle apps/rust-service names Framework rust-axum, which the chart does not install in apps$' "$T/out")" 1

# The entry exists, in another namespace: each project has its own catalog.
: >"$T/elsewhere.yaml"
catalog "$T/elsewhere.yaml" apps
entry "$T/elsewhere.yaml" shop Framework rust-axum service
bundle "$T/elsewhere.yaml" apps rust-service framework=rust-axum
run "$T/elsewhere.yaml"
check "an entry in another namespace doesn't count" $? 1
check "it says which namespace lacks it" "$(grep -c 'does not install in apps$' "$T/out")" 1

: >"$T/types.yaml"
catalog "$T/types.yaml" apps
bundle "$T/types.yaml" apps go-web framework=go-cli standard=go-standard compute=small deployment=k8s-manifests
run "$T/types.yaml"
check "entries sharing no Container type fail" $? 1
check "it lists each entry's types" \
	"$(grep -c '^The chart (types.yaml): Bundle apps/go-web: its entries share no Container type (go-cli: cli; go-standard: cli; small: service worker job static-site; k8s-manifests: service worker job static-site)$' "$T/out")" 1

: >"$T/standard.yaml"
catalog "$T/standard.yaml" apps
bundle "$T/standard.yaml" apps py-go framework=python-fastapi standard=aws-standard
run "$T/standard.yaml"
check "a Standard that doesn't support the Framework fails" $? 1
check "it names the Standard and the Framework" \
	"$(grep -c '^The chart (standard.yaml): Bundle apps/py-go: Standard aws-standard does not support Framework python-fastapi (supportedFrameworks: aws-lambda-ts)$' "$T/out")" 1

: >"$T/self.yaml"
catalog "$T/self.yaml" apps
bundle "$T/self.yaml" apps aws-fn framework=aws-lambda-ts standard=aws-standard compute=small
run "$T/self.yaml"
check "a Compute on a Framework that deploys itself fails" $? 1
check "it says to remove it" \
	"$(grep -c '^The chart (self.yaml): Bundle apps/aws-fn: Framework aws-lambda-ts deploys itself; remove computeRef and deploymentRef$' "$T/out")" 1

# Every failing Bundle is reported before the exit.
: >"$T/two.yaml"
catalog "$T/two.yaml" apps
bundle "$T/two.yaml" apps a framework=nope
bundle "$T/two.yaml" apps b framework=go-cli compute=small
run "$T/two.yaml"
check "two bad bundles fail" $? 1
check "both are reported" "$(grep -c '^The chart (two.yaml): Bundle apps/' "$T/out")" 2

# A render without Bundles passes with a count of none: the caller asks for some.
: >"$T/none.yaml"
catalog "$T/none.yaml" apps
run "$T/none.yaml"
check "no bundle passes" $? 0
check "it says none" "$(grep -c '^0 Bundles in none.yaml' "$T/out")" 1

run
check "no file is a usage error" $? 2

[ "$fail" = 0 ] && echo "ok   hack/chart-bundles.sh"
exit $fail
