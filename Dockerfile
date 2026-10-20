# syntax=docker/dockerfile:1
# Build the manager and model-proxy binaries
# Override BASE_IMAGE to build from another registry, e.g. docker.io/library/golang:1.26.9@sha256:...
ARG BASE_IMAGE=golang:1.26.9@sha256:d7722066f0b60ceccb6c0643cbed1f5f9e15506ac237146c95333504d7805d89
FROM ${BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH
# The release this image is, stamped into the binary (internal/buildinfo);
# release.yml passes it, a local build stays dev.
ARG VERSION=dev

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer.
# go mod download retries (retry: 5 tries, 10 to 40 seconds apart), so one
# slow handshake with the module proxy doesn't fail a release (#1494).
RUN --mount=type=cache,target=/go/pkg/mod \
    retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
    && retry go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
# The cache mounts keep Go's module and build caches between builds, so a rebuild
# after a small change takes seconds.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags=-X=github.com/JairJosafath/c4factory/internal/buildinfo.Version=${VERSION} \
    -o manager cmd/main.go \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags=-X=github.com/JairJosafath/c4factory/internal/buildinfo.Version=${VERSION} \
    -o model-proxy ./cmd/model-proxy

# Use distroless as minimal base image to package the manager binary and the
# model proxy's (cmd/model-proxy), so a release builds no image of its own for it
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/model-proxy .
USER 65532:65532

ENTRYPOINT ["/manager"]
