#!/usr/bin/env bash
# Registers the runner with Gitea, which writes /data/.runner (runner.file in
# hack/runner/config.yaml), unless it is registered already. The pod's init
# container runs it, and only that container gets RUNNER_TOKEN, Gitea's
# instance-wide registration token: the runner container, where the jobs run,
# never has it (entrypoint.sh; docs/design.md, "The CI fence").
# RUNNER_CONFIG and RUNNER_FILE are for the tests.
set -euo pipefail
config=${RUNNER_CONFIG:-/etc/gitea-runner/config.yaml}
file=${RUNNER_FILE:-/data/.runner}
: "${GITEA_INSTANCE:?set GITEA_INSTANCE}" "${RUNNER_TOKEN:?set RUNNER_TOKEN}"
if [[ -s $file ]]; then
  echo "Registered already: $file"
  exit 0
fi
exec gitea-runner register --no-interactive --config "$config" \
  --instance "$GITEA_INSTANCE" --token "$RUNNER_TOKEN" \
  --name "${RUNNER_NAME:-$(hostname)}" --labels "${RUNNER_LABELS:-base:host}"
