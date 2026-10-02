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

# Warm the module cache with the Kubernetes libraries CEL Lens builds on: they are
# large, and every CI job and agent run would download them again.
RUN mkdir /tmp/warm && cd /tmp/warm && go mod init warm >/dev/null 2>&1 \
 && go get k8s.io/apiserver@v0.37.0 k8s.io/apiextensions-apiserver@v0.37.0 k8s.io/client-go@v0.37.0 \
 && cd / && rm -rf /tmp/warm /root/.cache/go-build \
 && chmod -R a+rX /go/pkg/mod

ENV GOTOOLCHAIN=local \
    GOFLAGS=-modcacherw
