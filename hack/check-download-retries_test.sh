#!/usr/bin/env bash
# Tests for hack/check-download-retries.sh: every network fetch in the
# Dockerfiles Release builds retries (#1494: v0.28.1's release failed on one
# TLS handshake timeout in go install). curl without --retry or piped to tar,
# go install, go get, go mod download, setup-envtest use and apk add outside
# retry, apt-get without Acquire::Retries and npm without --fetch-retries fail.
# Run: hack/check-download-retries_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(cd "$(dirname "$0")/.." && pwd)
CHECK=$ROOT/hack/check-download-retries.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# Every Dockerfile .github/workflows/release.yml builds (`-f FILE`, or the
# context's own Dockerfile), and the Session client's.
built=$(grep -E '^ +docker build ' "$ROOT/.github/workflows/release.yml" |
  sed -E 's/.* -f ([^ ]+) .*/\1/; t; s/.* ([^ ]+)$/\1\/Dockerfile/; s/^\.\///' | sort -u)
check "#1494 release.yml builds images" "$([ "$(wc -l <<<"$built")" -ge 7 ] && echo yes)" yes
for f in $built hack/session/Dockerfile; do
  out=$("$CHECK" "$ROOT/$f" 2>&1)
  check "#1494 $f's downloads retry" "$?" 0
  [ -n "$out" ] && echo "$out"
done

R='retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; }'

# df BODY: a Dockerfile with BODY. run: the check on it, its exit code then
# its output.
df() { printf '%s\n' "$1" >"$T/Dockerfile"; }
run() {
  out=$("$CHECK" "$T/Dockerfile" 2>&1)
  rc=$?
}

df "FROM x
# A comment: curl https://example.com | tar -x
RUN curl -fsSL --retry 5 --retry-all-errors -o /tmp/a.tgz \"https://example.com/\${V}/a.tgz\" \\
 && tar -xzf /tmp/a.tgz
RUN --mount=type=cache,target=/go/pkg/mod \\
    $R \\
    && retry go mod download
RUN $R \\
 && mkdir -p /out \\
 # a comment line inside a continued RUN
 && retry go install \"example.com/tool@v\${V}\" \\
 && retry /out/bin/setup-envtest use 1.37.0 --bin-dir /tmp/e \\
 && cp -a \"\$(/out/bin/setup-envtest use -i 1.37.0 --bin-dir /tmp/e -p path)/.\" /out/
RUN $R && retry apk add --no-cache curl git
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y curl wget
RUN npm install -g --fetch-retries=5 x && npm config set fund false
RUN { npm ci --fetch-retries=5 && node check.mjs; } || { npm ci --fetch-retries=5 && node check.mjs; }
RUN go build ./... && go vet ./..."
run
check "#1494 retried fetches, packages named curl and offline commands pass" "$rc" 0
[ -n "$out" ] && echo "$out"

# bad NAME BODY WANT: a Dockerfile with BODY fails, with WANT in the output.
bad() {
  df "FROM x
$2"
  run
  check "#1494 $1 fails" "$rc" 1
  check "#1494 $1 says why" "$(grep -c -- "$3" <<<"$out")" 1
}

bad "curl without --retry" 'RUN curl -fsSL --retry-all-errors -o a https://example.com/a' "curl without --retry N"
bad "curl without --retry-all-errors" 'RUN curl -fsSL --retry 5 -o a https://example.com/a' "curl without --retry-all-errors"
bad "curl piped to tar" 'RUN curl -fsSL --retry 5 --retry-all-errors https://example.com/a.tgz | tar -xz' "curl piped"
bad "curl after a continued line" 'RUN mkdir -p /a \
 && curl -fsSL -o /a/b https://example.com/b --retry-all-errors' "curl without --retry N"
bad "go mod download alone" 'RUN --mount=type=cache,target=/go/pkg/mod go mod download' "not run under retry: go mod download"
bad "go install alone" "RUN $R && go install example.com/tool@v1" "not run under retry"
bad "go get alone" "RUN $R && cd /w && go get example.com/m@v1" "not run under retry"
bad "setup-envtest use alone" 'RUN cp -a "$(setup-envtest use 1.37.0 -p path)/." /out' "not run under retry"
bad "apk add alone" 'RUN apk add --no-cache git' "not run under retry"
bad "retry no RUN defines" 'RUN retry go mod download' "retry used but not defined"
bad "apt-get update without retries" 'RUN apt-get update && apt-get -o Acquire::Retries=5 install -y git' "apt-get without"
bad "apt-get install without retries" 'RUN apt-get -o Acquire::Retries=5 update && apt-get install -y git' "apt-get without"
bad "npm ci without retries" 'RUN npm ci --no-audit' "npm without --fetch-retries"
bad "wget without tries" 'RUN wget -q https://example.com/a' "wget without --tries"

exit "$fail"
