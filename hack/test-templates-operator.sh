#!/usr/bin/env bash
# make test-templates-operator: renders the operator combo (Framework
# go-kubebuilder, Standard go-operator-standard, Compute small, Deployment
# gitea-release) the way the
# operator scaffolds it, commits it in a new git repository and runs its
# `make check`; then does what an agent does first, `kubebuilder create api`
# and `make manifests generate`, fails if that changed a protected path of
# go-operator-standard, commits, and runs `make check` again; last, what
# gitea-release runs for a tag, `make dist VERSION=v0.1.0`, and fails unless
# dist/ holds the binaries, install.yaml and SHA256SUMS. No cluster, no
# Docker. make check doesn't run it; a change to either operator template does
# (docs/plans/done/kubebuilder-operators.md, decision 6).
#
# The Makefile passes this repository's pinned tools, which the rendered repo
# finds on PATH at the versions it pins, and the envtest binaries, so nothing
# is downloaded twice:
#
#   KUBEBUILDER CONTROLLER_GEN KUSTOMIZE ENVTEST GOLANGCI_LINT  tool binaries
#   ENVTEST_ASSETS  the directory of kube-apiserver and etcd
#   KEEP            non-empty: keep the rendered repository and print where
set -euo pipefail

: "${KUBEBUILDER:?}" "${CONTROLLER_GEN:?}" "${KUSTOMIZE:?}" "${ENVTEST:?}" "${GOLANGCI_LINT:?}" "${ENVTEST_ASSETS:?}"

ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d)
if [ -n "${KEEP:-}" ]; then
	trap 'echo "Kept $T"' EXIT
else
	trap 'rm -rf "$T"' EXIT
fi

# go-operator-standard's protectedPaths, from its entry in the chart's catalog.
mapfile -t protected < <(awk '/name: go-operator-standard$/ { s = 1 }
	s && /protectedPaths:/ { sub(/.*\[/, ""); sub(/\].*/, ""); gsub(/, */, "\n"); print; exit }' \
	"$ROOT/dist/chart/values.yaml")
if [ ${#protected[@]} -eq 0 ]; then
	echo "No protectedPaths for go-operator-standard in dist/chart/values.yaml" >&2
	exit 1
fi

# The tools under the names the rendered Makefile looks for on PATH.
mkdir -p "$T/bin"
ln -s "$KUBEBUILDER" "$T/bin/kubebuilder"
ln -s "$CONTROLLER_GEN" "$T/bin/controller-gen"
ln -s "$KUSTOMIZE" "$T/bin/kustomize"
ln -s "$ENVTEST" "$T/bin/setup-envtest"
ln -s "$GOLANGCI_LINT" "$T/bin/golangci-lint"
# Unset, so the rendered Makefile finds them on PATH as in the toolchain image.
unset KUBEBUILDER CONTROLLER_GEN KUSTOMIZE ENVTEST GOLANGCI_LINT
export PATH="$T/bin:$PATH" ENVTEST_SYSTEM_DIR="$ENVTEST_ASSETS"
# The rendered repo's make is its own, not a sub-make of this repository's.
unset MAKEFLAGS MFLAGS MAKELEVEL MAKEOVERRIDES C4_HEAVY_SLOT

repo="$T/backup-operator"
(cd "$ROOT" && go run ./hack/render -out "$repo" -type operator -container backup-operator \
	-display-name "Backup Operator" Framework=go-kubebuilder Standard=go-operator-standard Compute=small Deployment=gitea-release)
cd "$repo"

git() { command git -c user.name="test-templates-operator" -c user.email="test@c4factory.local" \
	-c core.hooksPath=/dev/null -c commit.gpgsign=false "$@"; }
git init -q -b main
git add -A
git commit -q -m "Scaffold backup-operator"

step() { echo; echo "\$ $*"; "$@"; }

step make check
step kubebuilder create api --group demo --version v1 --kind Widget --resource --controller
step make manifests generate

changed=$(git status --porcelain -- "${protected[@]}")
if [ -n "$changed" ]; then
	echo "kubebuilder create api or make manifests generate changed protected paths of go-operator-standard:" >&2
	echo "$changed" >&2
	exit 1
fi
echo
echo "No protected path changed (${protected[*]})"

git add -A
git commit -q -m "Add the demo/v1 Widget API"
step make check

step make dist VERSION=v0.1.0
step ls dist
want=(install.yaml SHA256SUMS)
for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64.exe windows_arm64.exe; do
	want+=("backup-operator_$platform")
done
for f in "${want[@]}"; do
	[ -s "dist/$f" ] || { echo "make dist didn't write dist/$f" >&2; exit 1; }
done
grep -q '^        image: controller:v0.1.0$' dist/install.yaml ||
	{ echo "dist/install.yaml doesn't name the image controller:v0.1.0" >&2; exit 1; }
(cd dist && sha256sum -c --quiet SHA256SUMS)
if [ -n "$(git status --porcelain)" ]; then
	echo "make dist changed the working tree:" >&2
	git status --porcelain >&2
	exit 1
fi
echo
echo "test-templates-operator: the operator combo passes make check before and after kubebuilder create api, and make dist writes the release files"
