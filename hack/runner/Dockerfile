# The CI runner for Container repositories: the Gitea runner on a toolchain image
# (BASE), running jobs in host mode, so CI checks with exactly the tools the
# agents use. Jobs pick it with `runs-on: <label>`: `make runner-base` builds it
# on the minimal image (label base), `make runner-go` on the Go and Node one (label go).
# Both images have uv, so a Python project checks on either and releases on go.
ARG BASE=theos/base:dev
FROM ${BASE}
ARG RUNNER_VERSION=3.5.0
ARG RUNNER_SHA256=16dc5c89ad5ac5496369642f01ed29c9bb130a2512e16a8cc3b400d9d0bfea73
# The arm64 build's sum, which the image doesn't use: Release attaches both
# builds of this version, checked against these sums, for a remote
# TestEnvironment's machine that runs a CI job (docs/plans/test-environments.md,
# step 8).
ARG RUNNER_SHA256_ARM64=8f48540ac05bf13b6489c0d75f03d78868e556b1dc58415d1c507a264fff2add
RUN curl -fsSL --retry 5 --retry-all-errors -o /usr/local/bin/gitea-runner \
      "https://gitea.com/gitea/runner/releases/download/v${RUNNER_VERSION}/gitea-runner-${RUNNER_VERSION}-linux-amd64" \
 && echo "${RUNNER_SHA256}  /usr/local/bin/gitea-runner" | sha256sum -c - \
 && chmod +x /usr/local/bin/gitea-runner
COPY hack/runner/config.yaml /etc/gitea-runner/config.yaml
# The pod's init container registers (c4-runner-register), the only one given
# the registration token; the entrypoint runs jobs and refuses the token.
COPY --chmod=0755 hack/runner/register.sh /usr/local/bin/c4-runner-register
COPY --chmod=0755 hack/runner/entrypoint.sh /usr/local/bin/entrypoint
COPY --chmod=0755 hack/runner/tmp-sweep.sh /usr/local/bin/c4-tmp-sweep
ENTRYPOINT ["/usr/local/bin/entrypoint"]
