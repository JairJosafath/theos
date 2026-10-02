# The Go and Node toolchain: Go, golangci-lint and Node with npm, for Containers
# written in Go or TypeScript (CEL Lens: a Go language server and a VS Code
# extension). CI (runner label `go`), agents and devcontainers all use it, so an
# agent checks its work with exactly the tools CI uses.
FROM golang:1.26-bookworm

ARG NODE_VERSION=24.21.0
ARG GOLANGCI_LINT_VERSION=2.14.0

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git jq make unzip xz-utils zip \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
      | tar -xJ -C /usr/local --strip-components=1 --exclude='*.md' --exclude=LICENSE \
 && node --version && npm --version \
 && npm config set fund false --location=global \
 && npm config set update-notifier false --location=global

RUN curl -fsSL "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64.tar.gz" \
      | tar -xz -C /usr/local/bin --strip-components=1 "golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64/golangci-lint" \
 && golangci-lint version

ENV GOTOOLCHAIN=local \
    GOFLAGS=-modcacherw \
    GOCACHE=/root/.cache/go-build \
    GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint

# Warm the module and build caches with the Kubernetes CEL libraries CEL Lens
# builds on. Cold, they take about 7 minutes and 3 GB to compile (half of it for
# -race), and every agent sandbox starts cold; CI and agents only compile their
# own packages on top. The cache layer is read-only to them: new entries go to
# the container's own layer.
COPY warm /tmp/warm/
RUN cd /tmp/warm \
 && go get k8s.io/apiserver@v0.37.0 k8s.io/apiextensions-apiserver@v0.37.0 k8s.io/client-go@v0.37.0 \
 && go build ./... && go build -race ./... && go vet ./... \
 && cd / && rm -rf /tmp/warm \
 && chmod -R a+rX /go/pkg/mod /root/.cache
