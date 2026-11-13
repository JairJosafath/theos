# syntax=docker/dockerfile:1
# The Session client image: theos-session plus git, for the test Session pod
# (hack/session/pod.yaml). Build it from the repository root:
#   docker build -f hack/session/Dockerfile -t theos/theos-session:dev .
ARG BASE_IMAGE=golang:1.26.11@sha256:957952ae74dc010608717144399a59a2c6e82ea03d63be1c7ff6577c5a135f93
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

FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
 && retry apk add --no-cache git
COPY --from=builder /workspace/theos-session /usr/local/bin/theos-session
ENV HOME=/tmp
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/theos-session"]
