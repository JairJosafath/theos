#!/usr/bin/env bash
# Tests for hack/relay-watch.sh: when a round starts the relay, with pick and
# hack/relay.sh faked.
# Run: hack/relay-watch_test.sh
T=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$T"' EXIT
unset C4_CLUSTER
# shellcheck source=hack/relay-watch.sh
. "$(dirname "$0")/relay-watch.sh"
DIR=$T/relay LOG=$T/log WORKERS=2 RETRY=600 IDLE_RUN=1800
mkdir -p "$DIR"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

PICKED= PICK_RC=1 RELAY_RC=0
pick_next() { [ -n "$PICKED" ] && printf '%s\n' "$PICKED"; return "$PICK_RC"; }
run_relay() { echo started >>"$T/starts"; echo "worker 1 started"; return "$RELAY_RC"; }
starts() { cat "$T/starts" 2>/dev/null | wc -l | tr -d ' '; }
logged() { grep -c "$1" "$LOG" 2>/dev/null || true; }

# Nothing ready, on a device without the cluster: wait, and say so once.
C4_CLUSTER=none
round; round
check "idle: no relay" "$(starts)" 0
check "idle: logged once" "$(logged 'nothing ready; waiting')" 1

# Nothing ready, on the device with the cluster: start the relay for the
# evaluator, then not again until IDLE_RUN has passed.
C4_CLUSTER=
round
check "cluster idle: starts the relay for the evaluator" "$(starts)" 1
round
check "cluster idle: not again right away" "$(starts)" 1
IDLE_T=$(($(date +%s) - IDLE_RUN))
round
check "cluster idle: again after IDLE_RUN" "$(starts)" 2
C4_CLUSTER=none

# An issue is ready: start the relay, and log the issue and its output.
PICKED=$'12\tFix the guides\thttps://github.com/x/y/issues/12\tready' PICK_RC=0
round
check "ready: starts the relay" "$(starts)" 3
check "ready: logs the issue" "$(logged 'ready: 12 | Fix the guides')" 1
check "ready: logs the relay's output" "$(logged 'relay: worker 1 started')" "$(starts)"

# Still unclaimed right after: wait for RETRY, saying so once.
round; round
check "unclaimed: no second start" "$(starts)" 3
check "unclaimed: logged once" "$(logged '#12 is still unclaimed')" 1
LAST_T=$(($(date +%s) - RETRY))
round
check "unclaimed: starts again after RETRY" "$(starts)" 4

# Another issue: start straight away.
PICKED=$'13\tAnother\thttps://github.com/x/y/issues/13\tready'
round
check "another issue: starts the relay" "$(starts)" 5

# Every worker runs: don't even ask GitHub.
PICKED=$'14\tA third\thttps://github.com/x/y/issues/14\tready'
flock -o "$DIR/lock-1" sleep 30 &
flock -o "$DIR/lock-2" sleep 30 &
until ! flock -n "$DIR/lock-1" true && ! flock -n "$DIR/lock-2" true; do sleep 0.1; done
round
check "busy: no relay" "$(starts)" 5
kill %1 %2; wait 2>/dev/null

# A free worker again: issue 14 starts it.
round
check "free again: starts the relay" "$(starts)" 6

# hack/relay.sh fails: log it.
PICKED=$'15\tFourth\thttps://github.com/x/y/issues/15\tready' RELAY_RC=1
round
check "relay fails: logged" "$(logged 'hack/relay.sh failed (exit 1)')" 1
RELAY_RC=0

# GitHub can't be reached: no relay, logged once.
PICKED='github: no token' PICK_RC=2
round; round
check "pick fails: no relay" "$(starts)" 7
check "pick fails: logged once" "$(logged 'could not ask GitHub for the next issue (exit 2): github: no token')" 1

# Arguments.
bash "$(dirname "$0")/relay-watch.sh" --workers 0 2>/dev/null
check "--workers 0 refused" $? 64
bash "$(dirname "$0")/relay-watch.sh" --bogus 2>/dev/null
check "unknown flag refused" $? 64

exit $fail
