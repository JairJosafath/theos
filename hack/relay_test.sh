#!/usr/bin/env bash
# Tests for the relay's memory guard in hack/relay.sh: the worker cap on the
# device with the Kind cluster, and the wait for headroom before a pick, with a
# faked /proc/meminfo.
# Run: hack/relay_test.sh
T=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$T"' EXIT
export RELAY_MEMINFO=$T/meminfo RELAY_MIN_MEM_GIB=4 RELAY_ADMIT_GAP=300 RELAY_MEM_POLL=1 RELAY_MEM_WAIT=30
unset C4_CLUSTER RELAY_MAX_WORKERS RELAY_DRY_RUN
# shellcheck source=hack/relay.sh
. "$(dirname "$0")/relay.sh"
DIR=$T/relay STOP=$DIR/stop LOG=$T/log
mkdir -p "$DIR"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
meminfo() { printf 'MemTotal:       15400000 kB\nMemAvailable:   %s kB\n' $(($1 * 1048576)) >"$RELAY_MEMINFO"; }

# The cap, on the device with the cluster.
cap 1 2>/dev/null; check "cap: one worker" $? 0
cap 2 2>"$T/err"; check "cap: two workers" $? 1
grep -q RELAY_MAX_WORKERS "$T/err"; check "cap: names RELAY_MAX_WORKERS" $? 0
MAX_WORKERS=2 cap 2 2>/dev/null; check "cap: raised on purpose" $? 0
C4_CLUSTER=none cap 9 2>/dev/null; check "cap: no cluster" $? 0
flock "$DIR/lock-3" sleep 30 &
until ! flock -n "$DIR/lock-3" true; do sleep 0.1; done
check "running workers" "$(running_workers)" 1
cap 1 2>/dev/null; check "cap: worker 3 already runs" $? 1
kill %1; wait 2>/dev/null

# The wait for headroom.
meminfo 8
admit; check "admit: headroom" $? 0
check "admit: marks the start" "$(test -e "$DIR/admitted" && echo yes)" yes
rm -f "$DIR/admitted"
meminfo 2
(sleep 2; meminfo 6) &
t0=$(date +%s)
admit; check "admit: waits until memory frees" $? 0
check "admit: waited" "$(($(date +%s) - t0 >= 2))" 1
grep -q "waiting for headroom: 2 GiB available" "$LOG"; check "admit: logs the wait" $? 0
grep -q "headroom: 6 GiB available" "$LOG"; check "admit: logs the end" $? 0
MEM_WAIT=1 admit; check "admit: sessions start apart" $? 1
grep -q "sessions start at least 300s apart" "$LOG"; check "admit: logs the gap" $? 0
rm -f "$DIR/admitted"
meminfo 1
MEM_WAIT=1 admit; check "admit: gives up" $? 1
touch "$STOP"
admit; check "admit: stop file" $? 1

exit $fail
