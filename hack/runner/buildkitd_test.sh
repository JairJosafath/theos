#!/usr/bin/env bash
# Tests for hack/runner/buildkitd.sh: buildkitd's state goes under the job's
# RUNNER_TEMP and is gone once buildctl-daemonless.sh stops it. A fake
# buildkitd records its flags, writes state and waits for SIGTERM.
# Run: hack/runner/buildkitd_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
SCRIPT="$(cd "$(dirname "$0")" && pwd)/buildkitd.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
has() { [ -e "$1" ] && echo yes || echo no; }

cat >"$T/buildkitd" <<'EOF'
#!/bin/sh
echo "$*" >"$CALLS"
root=$2
mkdir -p "$root/snapshots/1" && echo layer >"$root/snapshots/1/file"
chmod 0500 "$root/snapshots/1"
trap 'echo stopped >>"$CALLS"; exit 0' TERM
touch "$CALLS.ready"
while :; do sleep 0.1; done
EOF
chmod +x "$T/buildkitd"
export C4_BUILDKITD="$T/buildkitd" CALLS="$T/calls"

mkdir -p "$T/job/tmp"
RUNNER_TEMP="$T/job/tmp" sh "$SCRIPT" --oci-worker-no-process-sandbox --addr=unix:///run/buildkitd.sock &
pid=$!
for _ in $(seq 50); do [ -e "$T/calls.ready" ] && break; sleep 0.1; done
check "starts buildkitd with its state under RUNNER_TEMP and the flags it is given" "$(head -1 "$T/calls")" \
  "--root $T/job/tmp/buildkit --oci-worker-no-process-sandbox --addr=unix:///run/buildkitd.sock"
check "  which writes its state there" "$(has "$T/job/tmp/buildkit/snapshots/1/file")" yes
kill -TERM "$pid"
wait "$pid"
check "stopped as buildctl-daemonless.sh stops it, it ends cleanly" $? 0
check "  stops buildkitd" "$(tail -1 "$T/calls")" stopped
check "  and removes the state, a read-only directory too" "$(has "$T/job/tmp/buildkit")" no
check "  keeping the rest of RUNNER_TEMP" "$(has "$T/job/tmp")" yes

env -u RUNNER_TEMP sh "$SCRIPT" >"$T/out" 2>&1
check "outside a job (no RUNNER_TEMP) it fails" $? 2
check "  saying why" "$(grep -c 'RUNNER_TEMP is unset' "$T/out")" 1
exit $fail
