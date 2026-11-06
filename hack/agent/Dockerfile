# syntax=docker/dockerfile:1
# The agent image: theos-agent and Claude Code on a toolchain image (BASE: the minimal
# one in hack/toolchain-base by default, or hack/toolchain-go), so the agent
# checks its work with exactly the tools CI uses. Tasks run it in an Agent Sandbox.
# It holds theos-env too: the agent runs commands in a leased test environment
# with it, and a local environment's lease pod copies it in from this image
# (docs/plans/done/test-environments.md, T4).
# Build from the repository root: docker build -f hack/agent/Dockerfile .
ARG BASE=theos/base:dev
FROM golang:1.26.10@sha256:3745f1aa11822297b79113f5e84ce2bffeb039734571c39855809cfe9e5d7be6 AS build
WORKDIR /src
COPY go.mod go.sum ./
# go mod download retries (retry: 5 tries, 10 to 40 seconds apart), so one
# slow handshake with the module proxy doesn't fail a release (#1494).
RUN --mount=type=cache,target=/go/pkg/mod \
    retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
    && retry go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /theos-agent ./cmd/theos-agent \
    && CGO_ENABLED=0 go build -o /theos-env ./cmd/theos-env

FROM ${BASE}
ARG CLAUDE_CODE_VERSION=2.1.286
RUN npm install -g --fetch-retries=5 "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" && npm cache clean --force
COPY --from=build /theos-agent /usr/local/bin/theos-agent
COPY --from=build /theos-env /usr/local/bin/theos-env
# IS_SANDBOX: the pod is the sandbox, so Claude Code may skip permission prompts
# as root. The rest keeps the CLI from updating itself or phoning home for
# anything but the model.
ENV IS_SANDBOX=1 \
    DISABLE_AUTOUPDATER=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    DISABLE_TELEMETRY=1
ENTRYPOINT ["/usr/local/bin/theos-agent"]
