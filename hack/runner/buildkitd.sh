#!/bin/sh
# Usage: c4-buildkitd [BUILDKITD FLAG]...
# Runs buildkitd for one job of the image runner, with its state under the
# job's RUNNER_TEMP, and removes that state when buildkitd ends.
# buildctl-daemonless.sh starts it under rootlesskit ($BUILDKITD,
# hack/runner/image-job.env) and stops it with SIGTERM once the build is done.
# The removal runs here, inside rootlesskit's user namespace, where it owns
# every file a build made: the runner, outside it, can't remove one a RUN step
# gave another user, so the job's own cleanup would leave layers behind.
# THEOS_BUILDKITD is for the tests.
set -u
root=${RUNNER_TEMP:?RUNNER_TEMP is unset: c4-buildkitd runs in a job}/buildkit
# Inside rootlesskit's namespace every file is ours to change: open a
# directory a build left read-only, which rm couldn't empty.
remove() { chmod -R u+rwX "$1" 2>/dev/null; rm -rf "$1"; }
# A cancelled job's buildkitd is killed with its step, leaving its state
# behind in that job's RUNNER_TEMP (<work>/<job>/tmp). The runner runs one job
# at a time, so another job's state is such a leftover.
for old in "$(dirname "$(dirname "$RUNNER_TEMP")")"/*/tmp/buildkit; do
  if [ -d "$old" ] && [ "$old" != "$root" ]; then remove "$old"; fi
done
pid= stopping=
trap 'stopping=1; [ -z "$pid" ] || kill -TERM "$pid" 2>/dev/null' TERM INT
"${THEOS_BUILDKITD:-buildkitd}" --root "$root" "$@" &
pid=$!
[ -z "$stopping" ] || kill -TERM "$pid" 2>/dev/null
wait "$pid"
status=$?
# A signal ends the wait at once; wait until buildkitd has ended too.
while kill -0 "$pid" 2>/dev/null; do wait "$pid"; done
remove "$root"
# Stopped as asked, it ends cleanly; else as buildkitd did, so a buildkitd
# that couldn't start fails the build with its own status.
[ -n "$stopping" ] && exit 0
exit "$status"
