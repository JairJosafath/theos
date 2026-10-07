# The Go and Node toolchain: Go, golangci-lint, Node with npm and uv, for
# Containers written in Go, TypeScript or Python. CI (runner label `go`), agents
# and devcontainers all use it, so an agent checks its work with exactly the
# tools CI uses. uv installs the Python a project's `.python-version` pins on
# first use.
#
# UV_VERSION is the one hack/toolchain-base/Dockerfile pins;
# hack/toolchain-uv_test.sh (make check) fails when they differ.
ARG UV_VERSION=0.12.21
FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv

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

COPY --from=uv /uv /uvx /usr/local/bin/
RUN uv --version

ENV GOTOOLCHAIN=local \
    GOFLAGS=-modcacherw \
    GOCACHE=/root/.cache/go-build \
    GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint

# Warm the module and build caches with the Kubernetes libraries Go projects
# commonly build on. Cold, they take about 7 minutes and 3 GB to compile (half of it for
# -race); CI and agents only compile their own packages on top. Pods don't
# write here: c4-go-cache copies it into the node's caches once.
COPY warm.go.txt /tmp/warm/main.go
RUN cd /tmp/warm && go mod init warm >/dev/null 2>&1 \
 && go get k8s.io/client-go@v0.37.0 sigs.k8s.io/controller-runtime@v0.25.0 \
 && go build ./... && go build -race ./... && go vet ./... \
 && cd / && rm -rf /tmp/warm \
 && chmod -R a+rX /go/pkg/mod /root/.cache

# Readies the build caches the Kind node keeps between pods, seeded from the
# warm one above: agent sandboxes run it in an init container, the go runner on
# start and hourly (docs/architecture.md, "Go build caches").
COPY --chmod=0755 go-cache.sh /usr/local/bin/c4-go-cache
