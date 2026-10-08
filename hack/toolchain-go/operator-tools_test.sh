#!/usr/bin/env bash
# Checks that the go toolchain image pins the operator tools at the versions
# this repository is built with: kubebuilder from PROJECT, controller-gen and
# kustomize from the Makefile, setup-envtest and the envtest Kubernetes release
# from go.mod, and the warm build cache's client-go and controller-runtime the
# same as go.mod's.
# Run: hack/toolchain-go/operator-tools_test.sh
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
DF="$HERE/Dockerfile"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
arg() { sed -n "s/^ARG $1=//p" "$DF"; }
mk() { sed -n "s/^$1 ?= v//p" "$ROOT/Makefile"; }
mod() { awk -v m="$1" '$1 == m { sub(/^v/, "", $2); print $2 }' "$ROOT/go.mod"; }

check "kubebuilder is PROJECT's cliVersion" "$(arg KUBEBUILDER_VERSION)" "$(sed -n 's/^cliVersion: //p' "$ROOT/PROJECT")"
check "controller-gen is the Makefile's" "$(arg CONTROLLER_TOOLS_VERSION)" "$(mk CONTROLLER_TOOLS_VERSION)"
check "kustomize is the Makefile's" "$(arg KUSTOMIZE_VERSION)" "$(mk KUSTOMIZE_VERSION)"
check "setup-envtest is go.mod's controller-runtime" "$(arg SETUP_ENVTEST_VERSION)" "$(mod sigs.k8s.io/controller-runtime)"
check "envtest runs go.mod's Kubernetes" "$(arg ENVTEST_K8S_VERSION)" "$(mod k8s.io/api | sed -E 's/^0\./1./')"
warm=$(grep -o 'k8s.io/client-go@v[^ ]*' "$DF" | sed 's/.*@v//')
check "the warm cache's client-go is go.mod's" "$warm" "$(mod k8s.io/client-go)"
warm=$(grep -o 'sigs.k8s.io/controller-runtime@v[^ ]*' "$DF" | sed 's/.*@v//')
check "the warm cache's controller-runtime is go.mod's" "$warm" "$(mod sigs.k8s.io/controller-runtime)"

grep -q '^COPY --from=operator-tools /out/kubebuilder/ /usr/local/kubebuilder/$' "$DF"
check "the envtest binaries land in /usr/local/kubebuilder/bin" $? 0

exit $fail
