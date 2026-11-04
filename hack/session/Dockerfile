# syntax=docker/dockerfile:1
# The Session client image: theos-session plus git, for the test Session pod
# (hack/session/pod.yaml). Build it from the repository root:
#   docker build -f hack/session/Dockerfile -t theos/theos-session:dev .
ARG BASE_IMAGE=golang:1.26.10@sha256:3745f1aa11822297b79113f5e84ce2bffeb039734571c39855809cfe9e5d7be6
FROM ${BASE_IMAGE} AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
# go mod download retries (retry: 5 tries, 10 to 40 seconds apart), so one
# slow handshake with the module proxy doesn't fail a release (#1494).
RUN --mount=type=cache,target=/go/pkg/mod \
    retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
    && retry go mod download
# The root .dockerignore lets only Go sources in.
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags=-s -o theos-session ./cmd/theos-session

FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
 && retry apk add --no-cache git
COPY --from=builder /workspace/theos-session /usr/local/bin/theos-session
ENV HOME=/tmp
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/theos-session"]
