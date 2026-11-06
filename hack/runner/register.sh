#!/usr/bin/env bash
# Registers the runner with Gitea, which writes /data/.runner (runner.file in
# hack/runner/config.yaml), as a new runner each time the pod starts, a
# restart on its node too: a runner told to stop cancels its job, and the
# operator reads a cancelled job whose runner never came back online as the
# runner's, not a person's (model.Canceller, #1143). The pod's init
# container runs it, and only that container gets RUNNER_TOKEN, Gitea's
# instance-wide registration token: the runner container, where the jobs run,
# never has it (entrypoint.sh; docs/design.md, "The CI fence").
# RUNNER_EPHEMERAL set registers a single-job runner (--ephemeral): it takes one
# job and Gitea removes it when that job ends, as the operator's per-project
# pool pods are (docs/plans/done/ci-runner-isolation.md, step 3).
# RUNNER_COPY_TO set copies the runner, its config, the entrypoint and the
# /tmp sweep to that folder first: a test environment's pool pod runs its
# jobs on the environment's image, from there (docs/plans/test-environments.md,
# T8; entrypoint.sh's RUNNER_BIN_DIR).
# RUNNER_CONFIG and RUNNER_FILE are for the tests.
set -euo pipefail
config=${RUNNER_CONFIG:-/etc/gitea-runner/config.yaml}
file=${RUNNER_FILE:-/data/.runner}
: "${GITEA_INSTANCE:?set GITEA_INSTANCE}" "${RUNNER_TOKEN:?set RUNNER_TOKEN}"
if [[ -n ${RUNNER_COPY_TO:-} ]]; then
  mkdir -p "$RUNNER_COPY_TO"
  cp "$(command -v gitea-runner)" "$(command -v entrypoint)" "$(command -v c4-tmp-sweep)" "$RUNNER_COPY_TO/"
  cp "$config" "$RUNNER_COPY_TO/config.yaml"
fi
rm -f "$file"
ephemeral=()
[[ -n ${RUNNER_EPHEMERAL:-} ]] && ephemeral=(--ephemeral)
exec gitea-runner register --no-interactive --config "$config" \
  --instance "$GITEA_INSTANCE" --token "$RUNNER_TOKEN" \
  --name "${RUNNER_NAME:-$(hostname)}" --labels "${RUNNER_LABELS:-base:host}" "${ephemeral[@]}"
