# A self-hosted GitHub Actions runner for this repository's workflows, run on a
# device's Docker by hack/gh-runner/gh-runner.sh (make gh-runner). Not the
# factory's Gitea runner, which is hack/runner.
#
# The official runner image, plus what the workflows take for granted on
# ubuntu-latest and don't install themselves. The runner updates itself when
# GitHub asks for a newer version; bump RUNNER_VERSION to
# `gh api repos/actions/runner/releases/latest --jq .tag_name` (without the v)
# now and then, so a recreated container doesn't start too far behind.
ARG RUNNER_VERSION=2.337.0
FROM ghcr.io/actions/actions-runner:${RUNNER_VERSION}

USER root
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# git, curl, jq, unzip, python3, sudo and the docker CLI with buildx come with
# the base image.
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential rsync wget xz-utils zip zstd \
 && rm -rf /var/lib/apt/lists/*

# gh, yq (the Changed action), kubectl, kind and helm, pinned and checked.
ARG GH_VERSION=2.102.0
ARG YQ_VERSION=v4.54.1
ARG KUBECTL_VERSION=v1.37.1
ARG KIND_VERSION=v0.33.0
ARG HELM_VERSION=v4.3.0
RUN arch=$(dpkg --print-architecture) && cd /tmp \
 && gh=gh_${GH_VERSION}_linux_${arch}.tar.gz \
 && curl -fsSLO "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${gh}" \
 && curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_checksums.txt" | grep " ${gh}\$" | sha256sum -c \
 && tar -xzf "${gh}" --strip-components=2 -C /usr/local/bin "gh_${GH_VERSION}_linux_${arch}/bin/gh" \
 && curl -fsSLO "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${arch}" \
 && curl -fsSL "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/checksums-bsd" | grep "^SHA256 (yq_linux_${arch})" | sha256sum -c \
 && install "yq_linux_${arch}" /usr/local/bin/yq \
 && curl -fsSLO "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
 && echo "$(curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl.sha256")  kubectl" | sha256sum -c \
 && install kubectl /usr/local/bin/kubectl \
 && curl -fsSLO "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-${arch}" \
 && curl -fsSL "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-${arch}.sha256sum" | sha256sum -c \
 && install "kind-linux-${arch}" /usr/local/bin/kind \
 && helm=helm-${HELM_VERSION}-linux-${arch}.tar.gz \
 && curl -fsSLO "https://get.helm.sh/${helm}" \
 && curl -fsSL "https://get.helm.sh/${helm}.sha256sum" | sha256sum -c \
 && tar -xzf "${helm}" --strip-components=1 -C /usr/local/bin "linux-${arch}/helm" \
 && rm -rf /tmp/* \
 && gh --version && yq --version && kubectl version --client && kind version && helm version --short

# The runner user takes the device user's uid and gid (gh-runner.sh passes
# them), so the work directory, a bind mount from the host, stays the device
# user's. The base image's ubuntu user holds uid 1000; it goes.
ARG RUNNER_UID=1001
ARG RUNNER_GID=1001
RUN if [ "$(id -u runner):$(id -g runner)" != "${RUNNER_UID}:${RUNNER_GID}" ]; then \
      if getent passwd ubuntu >/dev/null; then userdel --remove ubuntu 2>/dev/null || userdel ubuntu; fi \
      && groupmod --non-unique --gid "${RUNNER_GID}" runner \
      && usermod --non-unique --uid "${RUNNER_UID}" --gid "${RUNNER_GID}" runner \
      && chown -R runner:runner /home/runner; \
    fi \
 && mkdir -p /runner-state /opt/hostedtoolcache /home/runner/.cache \
 && chown runner:runner /runner-state /opt/hostedtoolcache /home/runner/.cache

# Volumes keep these across jobs and containers: the registration in
# /runner-state, the tool cache setup-go and setup-node fill, and the Go, npm
# and golangci-lint caches under ~/.cache.
ENV RUNNER_TOOL_CACHE=/opt/hostedtoolcache \
    AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache \
    GOMODCACHE=/home/runner/.cache/go-mod \
    npm_config_cache=/home/runner/.cache/npm

COPY entrypoint.sh /usr/local/bin/gh-runner-entrypoint
USER runner
WORKDIR /home/runner
ENTRYPOINT ["/usr/local/bin/gh-runner-entrypoint"]
CMD ["run"]
