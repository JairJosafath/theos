# The minimal toolchain: Node, uv and nothing else the factory itself needs, for
# the e2e hello-world Container and Python projects. Node is there for `npm
# install` of Claude Code in hack/agent/Dockerfile and for JavaScript actions on
# a host-mode runner; bash, curl, git, make and TLS are what the runner
# entrypoint, the runner and agent Dockerfiles, the stand-in agent and its
# `make check` use. uv checks, builds and releases a Python project, with the
# Python its `.python-version` pins, which uv downloads on first use.
#
# UV_VERSION is pinned here; hack/toolchain-go/Dockerfile pins the same one,
# and hack/toolchain-uv_test.sh (make check) fails when they differ.
ARG UV_VERSION=0.12.21
FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv

FROM node:24-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git make \
 && rm -rf /var/lib/apt/lists/*

COPY --from=uv /uv /uvx /usr/local/bin/
RUN uv --version
