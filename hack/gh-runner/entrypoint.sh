#!/usr/bin/env bash
# The self-hosted GitHub Actions runner's entrypoint (hack/gh-runner/Dockerfile),
# driven by hack/gh-runner/gh-runner.sh. The registration lives on the volume at
# /runner-state, so a restarted or recreated container keeps it without a new
# registration token.
#
#   run          copy the registration in and take jobs (the default)
#   configure    register with RUNNER_TOKEN, RUNNER_URL, RUNNER_NAME, RUNNER_LABELS and RUNNER_WORKDIR
#   remove       deregister with RUNNER_TOKEN, a remove token
#   registered   exit 0 when the volume holds a registration
set -euo pipefail

state=${GH_RUNNER_STATE:-/runner-state}
# What config.sh writes; the runner may write the *_migrated ones later.
files=(.runner .credentials .credentials_rsaparams .runner_migrated .credentials_migrated)
cd "${GH_RUNNER_HOME:-/home/runner}"

# restore: the volume's registration into the runner's directory. The
# container's own copy, if it has one, is at least as new, so it stays.
restore() {
  local f
  for f in "${files[@]}"; do
    if [ ! -e "$f" ] && [ -e "$state/$f" ]; then cp -p "$state/$f" "$f"; fi
  done
}

# save: the runner's directory's registration onto the volume.
save() {
  local f
  for f in "${files[@]}"; do
    if [ -e "$f" ]; then cp -p "$f" "$state/$f"; else rm -f "$state/$f"; fi
  done
}

case "${1:-run}" in
  run)
    restore
    if [ ! -s .runner ]; then
      echo "gh-runner: not registered; run make gh-runner on the device" >&2
      exit 1
    fi
    save
    # The job-started hook once before the first job too: the runner makes
    # _temp in the work directory before any hook runs, and Docker makes a
    # missing bind mount root's, as it does when it restarts the container
    # after the host lost the directory (#1351). The hook makes it the runner's.
    if [ -n "${ACTIONS_RUNNER_HOOK_JOB_STARTED:-}" ]; then
      bash -e "$ACTIONS_RUNNER_HOOK_JOB_STARTED" || echo "gh-runner: the job-started hook failed at start" >&2
    fi
    ./env.sh
    # run.sh in the background, so a docker stop reaches it and the runner
    # shuts down cleanly, and anything it rewrote is saved afterwards. Job
    # control (set -m), or bash starts it with SIGINT ignored, and SIGINT is
    # how run.sh stops the runner.
    set -m
    ./run.sh &
    pid=$!
    trap 'kill -TERM "$pid" 2>/dev/null || true' TERM INT
    code=0
    wait "$pid" || code=$?
    while kill -0 "$pid" 2>/dev/null; do
      code=0
      wait "$pid" || code=$?
    done
    save
    exit "$code"
    ;;
  configure)
    : "${RUNNER_TOKEN:?}" "${RUNNER_URL:?}" "${RUNNER_NAME:?}" "${RUNNER_WORKDIR:?}"
    ./config.sh --unattended --replace --url "$RUNNER_URL" --token "$RUNNER_TOKEN" \
      --name "$RUNNER_NAME" --labels "${RUNNER_LABELS:-theos}" --work "$RUNNER_WORKDIR"
    save
    ;;
  remove)
    : "${RUNNER_TOKEN:?}"
    restore
    ./config.sh remove --token "$RUNNER_TOKEN"
    save
    ;;
  registered)
    [ -s "$state/.runner" ]
    ;;
  *)
    exec "$@"
    ;;
esac
