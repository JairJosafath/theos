# The Go and Node toolchain: Go, golangci-lint, Node with npm and uv, for
# Containers written in Go, TypeScript or Python, and the kubebuilder operator
# tools (kubebuilder, controller-gen, kustomize, setup-envtest and the envtest
# binaries in /usr/local/kubebuilder/bin, where envtest looks when
# KUBEBUILDER_ASSETS is unset). CI (runner label `go`), agents and
# devcontainers all use it, so an agent checks its work with exactly the tools
# CI uses. uv installs the Python a project's `.python-version` pins on first
# use.
#
# UV_IMAGE is the one hack/toolchain-base/Dockerfile pins;
# hack/toolchain-uv_test.sh (make check) fails when they differ.
ARG UV_IMAGE=ghcr.io/astral-sh/uv:0.12.21@sha256:a7aed3216253ee804de3e2d8afa5073baa1a177335345d43845cd4165e43b711
FROM ${UV_IMAGE} AS uv

# The operator tools, at the versions this repository is built with (its
# PROJECT, Makefile and go.mod); hack/toolchain-go/operator-tools_test.sh
# (make check) fails when they differ. Built in their own stage, so their
# module and build caches stay out of the image.
FROM golang:1.26.10-bookworm@sha256:5de7c99eb78ca5e515a4c340235d0955ec5964a5071c492c15503d7c267da473 AS operator-tools

ARG KUBEBUILDER_VERSION=4.16.0
ARG CONTROLLER_TOOLS_VERSION=0.22.0
ARG KUSTOMIZE_VERSION=5.8.1
ARG SETUP_ENVTEST_VERSION=0.25.0
ARG ENVTEST_K8S_VERSION=1.37.0

ENV GOBIN=/out/bin
# Every download retries (#1494): curl with --retry, written to a file so a
# retried transfer isn't piped to tar twice; go install and setup-envtest under
# retry (5 tries, 10 to 40 seconds apart).
RUN retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
 && mkdir -p /out/bin /out/kubebuilder/bin \
 && curl -fsSL --retry 5 --retry-all-errors -o /out/bin/kubebuilder \
      "https://github.com/kubernetes-sigs/kubebuilder/releases/download/v${KUBEBUILDER_VERSION}/kubebuilder_linux_amd64" \
 && chmod 0755 /out/bin/kubebuilder \
 && curl -fsSL --retry 5 --retry-all-errors -o /tmp/kustomize.tar.gz \
      "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv${KUSTOMIZE_VERSION}/kustomize_v${KUSTOMIZE_VERSION}_linux_amd64.tar.gz" \
 && tar -xzf /tmp/kustomize.tar.gz -C /out/bin kustomize && rm /tmp/kustomize.tar.gz \
 && retry go install "sigs.k8s.io/controller-tools/cmd/controller-gen@v${CONTROLLER_TOOLS_VERSION}" \
 && retry go install "sigs.k8s.io/controller-runtime/tools/setup-envtest@v${SETUP_ENVTEST_VERSION}" \
 && retry /out/bin/setup-envtest use "${ENVTEST_K8S_VERSION}" --bin-dir /tmp/envtest \
 && cp -a "$(/out/bin/setup-envtest use -i "${ENVTEST_K8S_VERSION}" --bin-dir /tmp/envtest -p path)/." /out/kubebuilder/bin/ \
 && chmod -R a+rX,u+w /out \
 && /out/bin/kubebuilder version && /out/bin/controller-gen --version \
 && /out/bin/kustomize version && ls /out/kubebuilder/bin

FROM golang:1.26.10-bookworm@sha256:5de7c99eb78ca5e515a4c340235d0955ec5964a5071c492c15503d7c267da473

ARG NODE_VERSION=24.21.0
ARG GOLANGCI_LINT_VERSION=2.14.0

RUN apt-get -o Acquire::Retries=5 update \
 && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
      bash ca-certificates curl git jq make unzip xz-utils zip \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL --retry 5 --retry-all-errors -o /tmp/node.tar.xz \
      "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
 && tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 --exclude='*.md' --exclude=LICENSE \
 && rm /tmp/node.tar.xz \
 && node --version && npm --version \
 && npm config set fund false --location=global \
 && npm config set update-notifier false --location=global

RUN curl -fsSL --retry 5 --retry-all-errors -o /tmp/golangci-lint.tar.gz \
      "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64.tar.gz" \
 && tar -xzf /tmp/golangci-lint.tar.gz -C /usr/local/bin --strip-components=1 "golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64/golangci-lint" \
 && rm /tmp/golangci-lint.tar.gz \
 && golangci-lint version

COPY --from=uv /uv /uvx /usr/local/bin/
RUN uv --version

COPY --from=operator-tools /out/bin/ /usr/local/bin/
COPY --from=operator-tools /out/kubebuilder/ /usr/local/kubebuilder/
RUN kubebuilder version && controller-gen --version && kustomize version \
 && ls /usr/local/kubebuilder/bin

ENV GOTOOLCHAIN=local \
    GOFLAGS=-modcacherw \
    GOCACHE=/root/.cache/go-build \
    GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint

# Warm the module and build caches with the Kubernetes libraries Go projects
# commonly build on. Cold, they take about 7 minutes and 3 GB to compile (half of it for
# -race); CI and agents only compile their own packages on top. Pods don't
# write here: c4-go-cache copies it into the node's caches once.
# go get and go list -deps fetch the modules under retry (#1494), so the
# builds after them need no network.
COPY warm.go.txt /tmp/warm/main.go
RUN retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
 && cd /tmp/warm && go mod init warm >/dev/null 2>&1 \
 && retry go get k8s.io/client-go@v0.37.0 sigs.k8s.io/controller-runtime@v0.25.0 \
 && retry go list -deps ./... >/dev/null \
 && go build ./... && go build -race ./... && go vet ./... \
 && cd / && rm -rf /tmp/warm \
 && chmod -R a+rX /go/pkg/mod /root/.cache

# Readies the build caches the Kind node keeps between pods, seeded from the
# warm one above: agent sandboxes run it in an init container, the go runner on
# start and hourly (docs/architecture.md, "Go build caches").
COPY --chmod=0755 go-cache.sh /usr/local/bin/c4-go-cache
