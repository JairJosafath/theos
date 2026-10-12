#!/usr/bin/env bash
# Tests for hack/check-pinned-images.sh: the UI's Dockerfile is pinned, and its
# npm ci step checks the native bindings in the same layer (#912); a base image
# by tag alone, an ARG default by tag alone or an ARG without a default fails,
# and a FROM naming an earlier stage passes.
# Run: hack/check-pinned-images_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(cd "$(dirname "$0")/.." && pwd)
CHECK=$ROOT/hack/check-pinned-images.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The Dockerfiles whose base images are pinned; add one when its images are.
"$CHECK" "$ROOT/ui/Dockerfile"
check "ui/Dockerfile's base images are pinned" "$?" 0
check "ui/Dockerfile checks the native bindings where it runs npm ci" \
  "$(grep -c 'npm ci .*&& node scripts/check-native.mjs' "$ROOT/ui/Dockerfile")" 2

digest=sha256:$(printf '0%.0s' {1..64})

# df BODY: a Dockerfile with BODY.
df() { printf '%s\n' "$1" >"$T/Dockerfile"; }

df "ARG IMG=node:24.21.0-alpine3.24@$digest
FROM \${IMG} AS build
RUN true
FROM --platform=linux/amd64 alpine:3.24.2@$digest
COPY --from=build /x /x
FROM build"
"$CHECK" "$T/Dockerfile" 2>/dev/null
check "pinned images, a pinned ARG and a stage pass" "$?" 0

df "FROM node:24-alpine"
out=$("$CHECK" "$T/Dockerfile" 2>&1)
check "a tag alone fails" "$?" 1
check "a tag alone names the image" "$(grep -c 'FROM node:24-alpine is not pinned' <<<"$out")" 1

df "FROM node@$digest"
"$CHECK" "$T/Dockerfile" 2>/dev/null
check "a digest without a tag fails" "$?" 1

df "ARG IMG=node:24-alpine
FROM \$IMG"
out=$("$CHECK" "$T/Dockerfile" 2>&1)
check "an ARG default by tag fails" "$?" 1
check "an ARG default names its value" "$(grep -c 'FROM \$IMG (node:24-alpine) is not pinned' <<<"$out")" 1

df "ARG IMG
FROM \${IMG}"
"$CHECK" "$T/Dockerfile" 2>/dev/null
check "an ARG without a default fails" "$?" 1

df "ARG IMG=node:24-alpine
FROM alpine:3.24.2@$digest
ARG IMG=node:24.21.0-alpine3.24@$digest
FROM \${IMG}"
"$CHECK" "$T/Dockerfile" 2>/dev/null
check "an ARG after the first FROM doesn't resolve a FROM" "$?" 1

df "FROM build AS build"
"$CHECK" "$T/Dockerfile" 2>/dev/null
check "a FROM naming its own stage fails" "$?" 1

exit "$fail"
