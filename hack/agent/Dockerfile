# syntax=docker/dockerfile:1
# The agent image: c4agent and Claude Code on the Flutter toolchain, so the agent
# checks its work with exactly the tools CI uses. Tasks run it in an Agent Sandbox.
# Build from the repository root: docker build -f hack/agent/Dockerfile .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /c4agent ./cmd/c4agent

FROM c4factory/flutter:3.47.5
ARG CLAUDE_CODE_VERSION=2.1.286
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" && npm cache clean --force
COPY --from=build /c4agent /usr/local/bin/c4agent
# IS_SANDBOX: the pod is the sandbox, so Claude Code may skip permission prompts
# as root. The rest keeps the CLI from updating itself or phoning home for
# anything but the model.
ENV IS_SANDBOX=1 \
    DISABLE_AUTOUPDATER=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    DISABLE_TELEMETRY=1
ENTRYPOINT ["/usr/local/bin/c4agent"]
