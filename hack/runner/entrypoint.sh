#!/usr/bin/env bash
# Registers the runner with Gitea on first start, then runs jobs.
set -euo pipefail
config=/etc/gitea-runner/config.yaml
: "${GITEA_INSTANCE:?set GITEA_INSTANCE}" "${RUNNER_TOKEN:?set RUNNER_TOKEN}"
mkdir -p /data/work
if [[ ! -s /data/.runner ]]; then
  gitea-runner register --no-interactive --config "$config" \
    --instance "$GITEA_INSTANCE" --token "$RUNNER_TOKEN" \
    --name "${RUNNER_NAME:-$(hostname)}" --labels flutter:host
fi
exec gitea-runner daemon --config "$config"
