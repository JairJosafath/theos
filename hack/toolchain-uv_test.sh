#!/usr/bin/env bash
# Checks that both toolchain images pin the same uv: the version lives in
# hack/toolchain-base/Dockerfile, and hack/toolchain-go/Dockerfile repeats it,
# since each image builds from its own directory.
# Run: hack/toolchain-uv_test.sh
HACK="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
pin() { sed -n 's/^ARG UV_IMAGE=//p' "$HACK/$1/Dockerfile"; }

base=$(pin toolchain-base)
go=$(pin toolchain-go)
[[ $base =~ ^ghcr\.io/astral-sh/uv:[0-9]+\.[0-9]+\.[0-9]+@sha256:[0-9a-f]{64}$ ]]; check "toolchain-base pins uv to a release by digest ($base)" $? 0
check "toolchain-go pins the same uv as toolchain-base" "$go" "$base"
for d in toolchain-base toolchain-go; do
  grep -q '^COPY --from=uv /uv /uvx /usr/local/bin/$' "$HACK/$d/Dockerfile"
  check "$d installs uv" $? 0
done

exit $fail
