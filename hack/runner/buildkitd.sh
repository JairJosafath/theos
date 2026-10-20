#!/bin/sh
# Usage: c4-buildkitd [BUILDKITD FLAG]...
# Runs buildkitd for one job of the image runner, with its state under the
# job's RUNNER_TEMP, and removes that state when buildkitd ends.
# buildctl-daemonless.sh starts it under rootlesskit ($BUILDKITD,
# hack/runner/image-job.env) and stops it with SIGTERM once the build is done.
# The removal runs here, inside rootlesskit's user namespace, where it owns
# every file a build made: the runner, outside it, can't remove one a RUN step
# gave another user, so the job's own cleanup would leave layers behind.
# C4_BUILDKITD is for the tests.
set -u
root=${RUNNER_TEMP:?RUNNER_TEMP is unset: c4-buildkitd runs in a job}/buildkit
"${C4_BUILDKITD:-buildkitd}" --root "$root" "$@" &
pid=$!
trap 'kill -TERM "$pid" 2>/dev/null' TERM INT
wait "$pid"
# A signal ends the wait at once; wait until buildkitd has ended too.
while kill -0 "$pid" 2>/dev/null; do wait "$pid"; done
# Inside rootlesskit's namespace every file is ours to change: open a
# directory a build left read-only, which rm couldn't empty.
chmod -R u+rwX "$root" 2>/dev/null
rm -rf "$root"
