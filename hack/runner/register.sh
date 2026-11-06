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
# RUNNER_CONFIG and RUNNER_FILE are for the tests.
set -euo pipefail
config=${RUNNER_CONFIG:-/etc/gitea-runner/config.yaml}
file=${RUNNER_FILE:-/data/.runner}
: "${GITEA_INSTANCE:?set GITEA_INSTANCE}" "${RUNNER_TOKEN:?set RUNNER_TOKEN}"
rm -f "$file"
ephemeral=()
[[ -n ${RUNNER_EPHEMERAL:-} ]] && ephemeral=(--ephemeral)
exec gitea-runner register --no-interactive --config "$config" \
  --instance "$GITEA_INSTANCE" --token "$RUNNER_TOKEN" \
  --name "${RUNNER_NAME:-$(hostname)}" --labels "${RUNNER_LABELS:-base:host}" "${ephemeral[@]}"
