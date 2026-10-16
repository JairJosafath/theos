#!/usr/bin/env bash
# Registers the runner with Gitea, then runs jobs.
set -euo pipefail
config=/etc/gitea-runner/config.yaml
: "${GITEA_INSTANCE:?set GITEA_INSTANCE}" "${RUNNER_TOKEN:?set RUNNER_TOKEN}"
mkdir -p /data/work
# A new runner at every start, even a restart in the same pod: a runner told to
# stop cancels its job, and the operator reads a cancelled job whose runner
# never came back online as the runner's, not a person's (model.Canceller).
rm -f /data/.runner
gitea-runner register --no-interactive --config "$config" \
  --instance "$GITEA_INSTANCE" --token "$RUNNER_TOKEN" \
  --name "${RUNNER_NAME:-$(hostname)}" --labels "${RUNNER_LABELS:-base:host}"
# /tmp outlives each job; clear what jobs left there long ago, past the 1h job
# timeout, so it can't fill up and fail a later job (#814).
(while sleep 600; do c4-tmp-sweep /tmp 120; done) &
exec gitea-runner daemon --config "$config"
