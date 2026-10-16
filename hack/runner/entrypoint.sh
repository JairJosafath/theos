#!/usr/bin/env bash
# Runs jobs as the runner registered in /data/.runner, which the pod's init
# container wrote (c4-runner-register, register.sh). The jobs run in this
# container, so it refuses to start holding the registration token, with
# which a job could register a runner of its own.
# RUNNER_CONFIG and RUNNER_FILE are for the tests.
set -euo pipefail
config=${RUNNER_CONFIG:-/etc/gitea-runner/config.yaml}
file=${RUNNER_FILE:-/data/.runner}
if [[ -n ${RUNNER_TOKEN:-} ]]; then
  echo "RUNNER_TOKEN is set in the runner container, where jobs could read it; give it to the init container that runs c4-runner-register only" >&2
  exit 1
fi
if [[ ! -s $file ]]; then
  echo "No $file: register in an init container first (c4-runner-register)" >&2
  exit 1
fi
mkdir -p /data/work
# /tmp outlives each job; clear what jobs left there long ago, past the 1h job
# timeout, so it can't fill up and fail a later job (#814).
(while sleep 600; do c4-tmp-sweep /tmp 120; done) &
exec gitea-runner daemon --config "$config"
