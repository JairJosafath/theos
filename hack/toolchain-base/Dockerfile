# The minimal toolchain: Node and nothing else the factory itself needs, for the
# e2e hello-world Container. Node is there for `npm install` of Claude Code in
# hack/agent/Dockerfile and for JavaScript actions on a host-mode runner;
# bash, curl, git, make and TLS are what the runner entrypoint, the runner and
# agent Dockerfiles, the stand-in agent and its `make check` use.
FROM node:24-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git make \
 && rm -rf /var/lib/apt/lists/*
