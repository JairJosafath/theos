#!/usr/bin/env bash
# Tests for hack/heavy-slot.sh, the device-wide slots heavy jobs queue for, and
# the Makefile's use of them, with the slots in a temporary directory.
# Run: hack/heavy-slot_test.sh
T=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
SLOT=$HACK/heavy-slot.sh
export HEAVY_SLOTS_DIR=$T/slots HEAVY_JOBS=2 HEAVY_POLL=0.2
unset C4_HEAVY_SLOT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
# job: start a stand-in heavy job; its PID in $job.
job() { sleep 60 & job=$!; }
# until_ok CMD...: retry CMD for up to 5 seconds.
until_ok() { local i; for i in $(seq 50); do "$@" && return 0; sleep 0.1; done; return 1; }
free() { "$SLOT" status | head -1; }

check "status: all free" "$(free)" "heavy-slot: 2 of 2 slots free"
"$SLOT" status >/dev/null; check "status: exit 0 with a free slot" $? 0

# Two jobs run; a third waits and names who holds the slots.
job; a=$job
job; b=$job
job; c=$job
"$SLOT" acquire "$a" "make check" 2>/dev/null; check "first job: gets a slot" $? 0
"$SLOT" acquire "$b" "make lint" 2>/dev/null; check "second job: gets a slot" $? 0
check "status: none free" "$(free)" "heavy-slot: 0 of 2 slots free"
"$SLOT" status >/dev/null; check "status: exit 1 when all are taken" $? 1
"$SLOT" status | grep -q "pid $a in .*\`make check\`"; check "status: names the holder" $? 0
"$SLOT" acquire "$a" "make check" 2>/dev/null; check "same job again: does not wait on itself" $? 0
"$SLOT" acquire "$c" "make test" 2>"$T/wait" &
waiter=$!
sleep 1
kill -0 "$waiter" 2>/dev/null; check "third job: waits" $? 0
grep -q "all 2 heavy-job slots on this device are taken" "$T/wait"; check "third job: says it waits" $? 0
grep -q "not a hang" "$T/wait"; check "third job: says waiting is not a hang" $? 0
grep -q "pid $a .*make check" "$T/wait" && grep -q "pid $b .*make lint" "$T/wait"
check "third job: names who holds the slots" $? 0
check "third job: says it once while the holders stay" "$(grep -c "slots on this device are taken" "$T/wait")" 1

# A job that ends frees its slot, and the one waiting takes it.
kill "$a"; wait "$a" 2>/dev/null
until_ok eval '! kill -0 $waiter 2>/dev/null'; check "ended job: the waiting job starts" $? 0
wait "$waiter"; check "waiting job: acquire succeeds" $? 0
grep -q "got slot .*\`make test\` starts" "$T/wait"; check "waiting job: says it starts" $? 0
"$SLOT" status | grep -q "pid $c "; check "waiting job: holds a slot" $? 0

# A killed job frees its slot.
kill -9 "$b"; wait "$b" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 1 of 2 slots free" ]'; check "killed job: frees its slot" $? 0
kill -9 "$c"; wait "$c" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 2 of 2 slots free" ]'; check "all ended: every slot free" $? 0

# A waiter whose own job goes away stops waiting.
job; a=$job; job; b=$job; job; c=$job
"$SLOT" acquire "$a" x 2>/dev/null; "$SLOT" acquire "$b" y 2>/dev/null
"$SLOT" acquire "$c" z 2>/dev/null &
waiter=$!
kill "$c"
until_ok eval '! kill -0 $waiter 2>/dev/null'; check "waiter's job gone: stops waiting" $? 0
wait "$waiter"; check "waiter's job gone: exit 1" $? 1

# HEAVY_JOBS=0 turns the queue off.
job; c=$job
HEAVY_JOBS=0 "$SLOT" acquire "$c" z 2>/dev/null; check "HEAVY_JOBS=0: no wait" $? 0

# The Makefile queues its heavy goals for a slot, not its light ones.
cd "$HACK/.."
timeout 3 make -n lint >/dev/null 2>"$T/make"; check "make lint, slots taken: waits" $? 124
grep -q '`make lint` waits for one' "$T/make"; check "make lint: says it waits" $? 0
timeout 3 make -n manifests generate check >/dev/null 2>&1; check "make manifests generate check: waits" $? 124
timeout 10 make -n manifests generate >/dev/null 2>&1; check "make manifests generate: no wait" $? 0
C4_HEAVY_SLOT=1 timeout 10 make -n lint >/dev/null 2>&1; check "sub-make of a job with a slot: no wait" $? 0
kill "$a"; wait "$a" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 1 of 2 slots free" ]'
timeout 10 make -n lint >/dev/null 2>&1; check "make lint, a slot free: runs" $? 0

exit $fail
