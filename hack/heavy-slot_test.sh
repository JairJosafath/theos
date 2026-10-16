#!/usr/bin/env bash
# Tests for hack/heavy-slot.sh, the device-wide slots heavy jobs queue for, and
# the Makefile's use of them, with the slots in a temporary directory.
# Run: hack/heavy-slot_test.sh
T=$(mktemp -d)
# Only this shell cleans up (#544): a job killed just after it is forked, before
# it resets the traps it inherits, runs this trap too, and would remove $T.
trap 'if [ "$BASHPID" = $$ ]; then kill $(jobs -p) 2>/dev/null; rm -rf "$T"; fi' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
SLOT=$HACK/heavy-slot.sh
export HEAVY_SLOTS_DIR=$T/slots HEAVY_JOBS=2 HEAVY_POLL=0.2
unset C4_HEAVY_SLOT C4_HEAVY_FIRST
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
# job: start a stand-in heavy job; its PID in $job.
job() { sleep 60 & job=$!; }
# until_ok CMD...: retry CMD for up to 5 seconds (UNTIL, in tenths).
until_ok() { local i; for i in $(seq "${UNTIL:-50}"); do "$@" && return 0; sleep 0.1; done; return 1; }
free() { "$SLOT" status | head -1; }

check "status: all free" "$(free)" "heavy-slot: 2 of 2 slots free, 0 waiting"
"$SLOT" status >/dev/null; check "status: exit 0 with a free slot" $? 0

# Two jobs run; a third waits and names who holds the slots.
job; a=$job
job; b=$job
job; c=$job
"$SLOT" acquire "$a" "make check" 2>/dev/null; check "first job: gets a slot" $? 0
"$SLOT" acquire "$b" "make lint" 2>/dev/null; check "second job: gets a slot" $? 0
check "status: none free" "$(free)" "heavy-slot: 0 of 2 slots free, 0 waiting"
"$SLOT" status >/dev/null; check "status: exit 1 when all are taken" $? 1
"$SLOT" status | grep -q "pid $a in .*\`make check\`"; check "status: names the holder" $? 0
"$SLOT" acquire "$a" "make check" 2>/dev/null; check "same job again: does not wait on itself" $? 0
"$SLOT" acquire "$c" "make test" 2>"$T/wait" &
waiter=$!
until_ok grep -q "all 2 heavy-job slots on this device are taken" "$T/wait"; check "third job: says it waits" $? 0
kill -0 "$waiter" 2>/dev/null; check "third job: waits" $? 0
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
grep -q "unbound variable" "$T/wait"; check "waiting job: exits without an unbound variable" $? 1

# A killed job frees its slot.
kill -9 "$b"; wait "$b" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 1 of 2 slots free, 0 waiting" ]'; check "killed job: frees its slot" $? 0
kill -9 "$c"; wait "$c" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 2 of 2 slots free, 0 waiting" ]'; check "all ended: every slot free" $? 0

# A waiter whose own job goes away stops waiting.
job; a=$job; job; b=$job; job; c=$job
"$SLOT" acquire "$a" x 2>/dev/null; "$SLOT" acquire "$b" y 2>/dev/null
"$SLOT" acquire "$c" z 2>"$T/gone" &
waiter=$!
kill "$c"
until_ok eval '! kill -0 $waiter 2>/dev/null'; check "waiter's job gone: stops waiting" $? 0
wait "$waiter"; check "waiter's job gone: exit 1" $? 1
check "waiter's job gone: removes its ticket" "$(ls "$HEAVY_SLOTS_DIR" | grep -c "^wait-")" 0
grep -q "unbound variable" "$T/gone"; check "waiter's job gone: exits without an unbound variable" $? 1

kill "$a" "$b"; wait "$a" "$b" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 2 of 2 slots free, 0 waiting" ]'

# Waiters get slots first come, first served, and a dead waiter loses its turn.
job; a=$job; job; b=$job; job; c=$job; job; d=$job; job; e=$job
"$SLOT" acquire "$a" x 2>/dev/null; "$SLOT" acquire "$b" y 2>/dev/null
"$SLOT" acquire "$c" older 2>"$T/older" &
older=$!
until_ok grep -q "waits for one" "$T/older"
"$SLOT" acquire "$d" dead 2>/dev/null &
dead=$!
until_ok eval '"$SLOT" status | grep -q "^heavy-slot: 0 of 2 slots free, 2 waiting"'
"$SLOT" acquire "$e" newer 2>"$T/newer" &
newer=$!
until_ok grep -q "waits for one" "$T/newer"
grep -q "with 0 job(s) waiting ahead" "$T/older"; check "older waiter: none ahead" $? 0
grep -q "with 2 job(s) waiting ahead" "$T/newer"; check "newer waiter: says two wait ahead" $? 0
"$SLOT" status | grep -q "waiting 1: pid $c .*\`older\`"; check "status: names the waiters in order" $? 0
"$SLOT" status | grep -q "waiting 3: pid $e .*\`newer\`"; check "status: the newest waiter last" $? 0
kill "$a"; wait "$a" 2>/dev/null
until_ok eval '! kill -0 $older 2>/dev/null'; check "freed slot: the oldest waiter takes it" $? 0
until_ok grep -q "with 1 job(s) waiting ahead" "$T/newer"; check "newer waiter: says one waits ahead now" $? 0
kill -0 "$newer" 2>/dev/null; check "freed slot: the newer waiter keeps waiting" $? 0
kill -9 "$dead"; wait "$dead" 2>/dev/null
kill "$b"; wait "$b" 2>/dev/null
until_ok eval '! kill -0 $newer 2>/dev/null'; check "dead waiter: skipped, the newer one takes the slot" $? 0
"$SLOT" status | grep -q "pid $e "; check "newer waiter: holds a slot" $? 0
ls "$HEAVY_SLOTS_DIR" | grep -c "^wait-" >"$T/tickets"; check "no tickets left" "$(cat "$T/tickets")" 0
kill "$c" "$d" "$e"; wait "$c" "$d" "$e" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 2 of 2 slots free, 0 waiting" ]'

# A --first waiter (land's check under the merge turn, #1290) takes the next
# slot that frees ahead of older waiters, never a held one; the others keep
# their order behind it.
job; a=$job; job; b=$job; job; c=$job; job; d=$job; job; f=$job
"$SLOT" acquire "$a" x 2>/dev/null; "$SLOT" acquire "$b" y 2>/dev/null
"$SLOT" acquire "$c" older 2>"$T/older" &
older=$!
until_ok grep -q "waits for one" "$T/older"
"$SLOT" acquire "$d" newer 2>"$T/newer" &
newer=$!
until_ok grep -q "waits for one" "$T/newer"
"$SLOT" acquire --first "$f" "make check" 2>"$T/first" &
first=$!
until_ok grep -q "waits for one" "$T/first"
grep -q "\`make check\` waits for one, with 0 job(s) waiting ahead of it; it goes first, ahead of 2 other waiting job(s)" "$T/first"
check "--first, slots taken: says it goes first, ahead of the older waiters" $? 0
until_ok grep -q "with 1 job(s) waiting ahead" "$T/older"; check "older waiter: says the --first one waits ahead of it" $? 0
"$SLOT" status | grep -q "waiting 1: pid $f .*\`make check\`, since [0-9:]*, goes first$"; check "status: the --first waiter first, marked" $? 0
"$SLOT" status | grep -q "waiting 2: pid $c .*\`older\`"; check "status: the older waiters after it" $? 0
"$SLOT" status | grep -q "waiting 3: pid $d .*\`newer\`"; check "status: the newer waiter last" $? 0
sleep 1
kill -0 "$first" 2>/dev/null; check "--first: never takes a held slot" $? 0
kill "$a"; wait "$a" 2>/dev/null
until_ok eval '! kill -0 $first 2>/dev/null'; check "--first: takes the next free slot ahead of older waiters" $? 0
wait "$first"; check "--first: acquire succeeds" $? 0
kill -0 "$older" 2>/dev/null && kill -0 "$newer" 2>/dev/null; check "--first: the older waiters keep waiting" $? 0
"$SLOT" status | grep -q "slot [12]: pid $f .*\`make check\`, since [0-9:]*, went first$"; check "status: shows the job that went first" $? 0
kill "$b"; wait "$b" 2>/dev/null
until_ok eval '! kill -0 $older 2>/dev/null'; check "after --first: the oldest ordinary waiter takes the next slot" $? 0
kill -0 "$newer" 2>/dev/null; check "after --first: the newer ordinary waiter keeps waiting" $? 0
kill "$f"; wait "$f" 2>/dev/null
until_ok eval '! kill -0 $newer 2>/dev/null'; check "after --first: the newer ordinary waiter takes the slot after" $? 0
ls "$HEAVY_SLOTS_DIR" | grep -c "^first-\|^wait-" >"$T/tickets"; check "--first: no tickets left" "$(cat "$T/tickets")" 0
kill "$c" "$d"; wait "$c" "$d" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 2 of 2 slots free, 0 waiting" ]'

# Both slots taken again, for the Makefile below.
job; a=$job; job; b=$job
"$SLOT" acquire "$a" x 2>/dev/null; "$SLOT" acquire "$b" y 2>/dev/null

# HEAVY_JOBS=0 turns the queue off.
job; c=$job
HEAVY_JOBS=0 "$SLOT" acquire "$c" z 2>/dev/null; check "HEAVY_JOBS=0: no wait" $? 0

# The Makefile queues its heavy goals for a slot, not its light ones. A loaded
# device can take seconds to read the Makefile (its `go env`s), so the tests
# wait for make to say it waits, not for a fixed time; the timeouts only end a
# make that hangs.
cd "$HACK/.."
# make_waits GOALS...: run make -n GOALS until it says it waits for a slot, or
# for up to 60 seconds; its stderr in $T/make. Whether it said so and still waits.
make_waits() {
  local m rc
  timeout 120 make -n "$@" >/dev/null 2>"$T/make" &
  m=$!
  UNTIL=600 until_ok grep -q "\`make $*\` waits for one" "$T/make"
  rc=$?
  [ "$rc" = 0 ] && { kill -0 "$m" 2>/dev/null || rc=1; }
  kill "$m" 2>/dev/null; wait "$m" 2>/dev/null
  return "$rc"
}
make_waits lint; check "make lint, slots taken: says it waits, and waits" $? 0
grep -q "goes first" "$T/make"; check "make lint: does not go first" $? 1
C4_HEAVY_FIRST=1 make_waits lint && grep -q "it goes first" "$T/make"; check "C4_HEAVY_FIRST=1 make lint: waits, going first" $? 0
check "C4_HEAVY_FIRST: the recipes don't inherit it" \
  "$(C4_HEAVY_FIRST=1 C4_HEAVY_SLOT=1 timeout 120 make -s --no-print-directory --eval 'heavy-first: ; @echo "[$$C4_HEAVY_FIRST]"' heavy-first 2>/dev/null)" "[]"
make_waits manifests generate check; check "make manifests generate check: says it waits, and waits" $? 0
timeout 120 make -n manifests generate >/dev/null 2>&1; check "make manifests generate: no wait" $? 0
C4_HEAVY_SLOT=1 timeout 120 make -n lint >/dev/null 2>&1; check "sub-make of a job with a slot: no wait" $? 0
kill "$a"; wait "$a" 2>/dev/null
until_ok eval '[ "$(free)" = "heavy-slot: 1 of 2 slots free, 0 waiting" ]'
timeout 120 make -n lint >/dev/null 2>&1; check "make lint, a slot free: runs" $? 0

exit $fail
