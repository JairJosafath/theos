# The minimal toolchain: Node, uv and nothing else the factory itself needs, for
# the e2e hello-world Container and Python projects. Node is there for `npm
# install` of Claude Code in hack/agent/Dockerfile and for JavaScript actions on
# a host-mode runner; bash, curl, git, make and TLS are what the runner
# entrypoint, the runner and agent Dockerfiles, the stand-in agent and its
# `make check` use. uv checks, builds and releases a Python project, with the
# Python its `.python-version` pins, which uv downloads on first use.
#
# UV_IMAGE is pinned here; hack/toolchain-go/Dockerfile pins the same one,
# and hack/toolchain-uv_test.sh (make check) fails when they differ.
ARG UV_IMAGE=ghcr.io/astral-sh/uv:0.12.21@sha256:a7aed3216253ee804de3e2d8afa5073baa1a177335345d43845cd4165e43b711
FROM ${UV_IMAGE} AS uv

FROM node:26.13.0-bookworm-slim@sha256:add5c17c14752c54d2f3d519ec46a3815684d41c33f198bcb09fb998f7dcbf68

RUN apt-get -o Acquire::Retries=5 update \
 && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
      bash ca-certificates curl git make \
 && rm -rf /var/lib/apt/lists/*

COPY --from=uv /uv /uvx /usr/local/bin/
RUN uv --version
