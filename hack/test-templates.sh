#!/usr/bin/env bash
# make test-templates: renders template combinations (rows of templateCombos
# in internal/scaffold/templates_test.go: the webshop's, the go cli, the
# vscode extension and every AWS one) the way the operator
# scaffolds them, commits each in a new git repository and runs its own
# `make check`; then what its Deployment does: `kustomize build deploy/k8s`
# for k8s-manifests, and for gitea-release `make dist` at the scaffold's
# version, failing unless dist/ holds the files the release workflow attaches.
# The AWS combos' make check synthesizes with CDK Nag and runs cfn-lint;
# nothing calls AWS. No cluster, no Docker; uv, npm and go fetch what the
# renders' lock files pin. make check doesn't run it; a change to one of
# these templates does (#1469, #1476).
# hack/test-templates-operator.sh does the same for the operator combo.
#
#   KUSTOMIZE      the kustomize binary of this repository (the Makefile passes it)
#   GOLANGCI_LINT  this repository's golangci-lint, which the Go renders' make
#                  check runs as golangci-lint on PATH (the Makefile passes it)
#   COMBO          run only the combo of this name (one of the names below)
#   KEEP           non-empty: keep the rendered repositories and print where
#
# It runs every combo and ends naming each combo and target that failed.
set -euo pipefail

: "${KUSTOMIZE:?}" "${GOLANGCI_LINT:?}"

ROOT=$(cd "$(dirname "$0")/.." && pwd)

# name|type|container|display name|layers, the layers as hack/render takes them.
combos=(
	"fastapi service with postgres|service|orders|Orders|Framework=python-fastapi Standard=python-standard Compute=small Deployment=k8s-manifests Connector=postgres"
	"python cli|cli|shop-admin|Shop Admin|Framework=python-cli Standard=python-standard Deployment=gitea-release"
	"python job on kubernetes|job|nightly-report|Nightly Report|Framework=python-cli Standard=python-standard Compute=small Deployment=k8s-manifests"
	"react web app on kubernetes|static-site|storefront|Storefront|Framework=react-vite Standard=ts-standard Compute=small Deployment=k8s-manifests"
	"node cli|cli|shop-tool|Shop Tool|Framework=node-cli Standard=ts-standard Deployment=gitea-release"
	"go cli|cli|shop-ctl|Shop Ctl|Framework=go-cli Standard=go-standard Deployment=gitea-release"
	"vscode extension|extension|shop-helper|Shop Helper|Framework=vscode-extension Standard=ts-standard Deployment=gitea-release"
	"aws lambda function|function|price-fn|Price Fn|Framework=aws-lambda-ts Standard=aws-standard Deployment=gitea-release"
	"aws lambda function with dynamodb|function|cart-fn|Cart Fn|Framework=aws-lambda-ts Standard=aws-standard Deployment=gitea-release Connector=dynamodb"
	"aws lambda function in go|function|stock-fn|Stock Fn|Framework=aws-lambda-go Standard=aws-standard Deployment=gitea-release"
	"aws lambda function in go with dynamodb|function|orders-fn|Orders Fn|Framework=aws-lambda-go Standard=aws-standard Deployment=gitea-release Connector=dynamodb"
	"aws python lambda function|function|tax-fn|Tax Fn|Framework=aws-lambda-py Standard=aws-standard Deployment=gitea-release"
	"aws python lambda function with dynamodb|function|invoice-fn|Invoice Fn|Framework=aws-lambda-py Standard=aws-standard Deployment=gitea-release Connector=dynamodb"
	"aws ec2 service|service|search|Search|Framework=aws-ec2-ts Standard=aws-standard Deployment=gitea-release"
	"aws fargate service|service|checkout|Checkout|Framework=aws-fargate-ts Standard=aws-standard Deployment=gitea-release"
	"aws queue consumer|worker|mailer|Mailer|Framework=aws-queue-ts Standard=aws-standard"
	"aws scheduled job|job|nightly-report|Nightly Report|Framework=aws-schedule-ts Standard=aws-standard"
	"aws event consumer|worker|audit|Audit|Framework=aws-event-ts Standard=aws-standard"
	"aws static site|static-site|landing|Landing|Framework=aws-static-site-ts Standard=aws-standard Deployment=gitea-release"
)

if [ -n "${COMBO:-}" ]; then
	found=
	for c in "${combos[@]}"; do
		[ "${c%%|*}" = "$COMBO" ] && found=$c
	done
	if [ -z "$found" ]; then
		echo "COMBO=$COMBO: no such combo; one of:" >&2
		for c in "${combos[@]}"; do echo "  ${c%%|*}" >&2; done
		exit 2
	fi
	combos=("$found")
fi

T=$(mktemp -d)
if [ -n "${KEEP:-}" ]; then
	trap 'echo "Kept $T"' EXIT
else
	trap 'rm -rf "$T"' EXIT
fi
# golangci-lint under the name the rendered Makefile looks for on PATH, as in
# the toolchain image.
mkdir -p "$T/bin"
ln -s "$GOLANGCI_LINT" "$T/bin/golangci-lint"
unset GOLANGCI_LINT
export PATH="$T/bin:$PATH"
# The rendered repos' make is their own, not a sub-make of this repository's.
unset MAKEFLAGS MFLAGS MAKELEVEL MAKEOVERRIDES C4_HEAVY_SLOT

git() { command git -c user.name="test-templates" -c user.email="test@c4factory.local" \
	-c core.hooksPath=/dev/null -c commit.gpgsign=false "$@"; }
step() { echo; echo "\$ $*"; "$@"; }

# dist_files checks what gitea-release attaches: every file in dist/, here
# the ones the combo's Framework packs for version $1.
dist_files() {
	local version=${1#v} want=()
	case $framework in
	python-cli) want=("dist/${container//-/_}-$version-py3-none-any.whl" "dist/${container//-/_}-$version.tar.gz") ;;
	# npm packs the scoped package @c4factory/<container> (hack/render's default
	# scope) as c4factory-<container>-<version>.tgz (#1695).
	node-cli) want=("dist/c4factory-$container-$version.tgz") ;;
	vscode-extension) want=("dist/$container-$version.vsix") ;;
	# aws-standard's flat cloud assembly, both tiers (#1607).
	aws-*) want=(dist/production.manifest.json dist/development.manifest.json) ;;
	go-cli)
		want=(dist/SHA256SUMS)
		for p in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64.exe windows_arm64.exe; do
			want+=("dist/${container}_$p")
		done
		;;
	*) echo "no release files known for $framework" >&2; return 1 ;;
	esac
	for f in "${want[@]}"; do
		[ -s "$f" ] || { echo "make dist didn't write $f" >&2; return 1; }
	done
	if [ -n "$(git status --porcelain)" ]; then
		echo "make dist changed the working tree:" >&2
		git status --porcelain >&2
		return 1
	fi
}

# run_combo renders one combo and runs its targets, leaving in $failed the
# target it was on when it failed.
run_combo() {
	local type=$1 layers=$2 repo=$T/$container
	failed="render"
	# shellcheck disable=SC2086 # the layers are words
	(cd "$ROOT" && go run ./hack/render -out "$repo" -type "$type" -container "$container" -display-name "$display" $layers) || return 1
	cd "$repo"
	git init -q -b main && git add -A && git commit -q -m "Scaffold $container" || return 1

	failed="make check"
	step make check || return 1
	case " $layers " in
	*" Deployment=k8s-manifests "*)
		failed="kustomize build deploy/k8s"
		echo
		echo "\$ kustomize build deploy/k8s"
		"$KUSTOMIZE" build deploy/k8s > "$T/$container.yaml" || return 1
		echo "deploy/k8s builds $(grep -c '^kind:' "$T/$container.yaml") objects"
		;;
	*" Deployment=gitea-release "*)
		# The scaffold's own version, which make dist requires the tag to match.
		local version
		case $framework in
		python-cli) version=v$(uv version --short) ;;
		# A Go module has no version of its own; the tag is the version.
		go-cli) version=v0.1.0 ;;
		# Nor does an AWS app's assembly; make dist accepts any (#1607).
		aws-*) version=v0.1.0 ;;
		*) version=v$(node -p "require('./package.json').version") ;;
		esac
		failed="make dist VERSION=$version"
		step make dist VERSION="$version" || return 1
		step ls dist
		dist_files "$version" || return 1
		;;
	esac
	failed=
}

failures=()
for c in "${combos[@]}"; do
	IFS='|' read -r name type container display layers <<<"$c"
	framework=${layers#Framework=}
	framework=${framework%% *}
	echo
	echo "=== $name ($layers)"
	# A subshell, so one combo's cd leaves the next alone; it leaves the
	# target that failed in $T/failed.
	if ! (run_combo "$type" "$layers" || { echo "$failed" > "$T/failed"; exit 1; }); then
		failures+=("$name: $(cat "$T/failed")")
		echo >&2
		echo "test-templates: combo \"$name\" failed: $(cat "$T/failed")" >&2
	fi
done

echo
if [ ${#failures[@]} -gt 0 ]; then
	echo "test-templates: ${#failures[@]} of ${#combos[@]} combos failed:" >&2
	for f in "${failures[@]}"; do echo "  $f" >&2; done
	exit 1
fi
echo "test-templates: ${#combos[@]} combos pass their make check and their Deployment's build"
