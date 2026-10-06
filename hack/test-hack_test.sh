#!/usr/bin/env bash
# Tests for hack/test-hack.sh, the runner of the shell tests in hack/, against
# stand-in tests in a temporary directory, and for make check running it.
# Run: hack/test-hack_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
RUN="$HACK/test-hack.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# A passing test at the top, one in a subdirectory, and a file that isn't a test.
mkdir -p "$T/ok/lib"
printf 'echo fine\n' >"$T/ok/a_test.sh"
printf 'echo fine\n' >"$T/ok/lib/b_test.sh"
printf 'exit 1\n' >"$T/ok/helper.sh"
out=$("$RUN" "$T/ok"); check "all pass: exit 0" $? 0
check "all pass: one line per test" "$(grep -c '^ok   ' <<<"$out")" 2
check "all pass: finds a test in a subdirectory" "$(grep -c 'lib/b_test.sh' <<<"$out")" 1
check "all pass: says so" "$(tail -n 1 <<<"$out")" "test-hack: all 2 shell tests passed"

# One failing test fails the run and shows the end of its output.
cp -r "$T/ok" "$T/bad"
printf 'echo "FAIL something broke"\nexit 3\n' >"$T/bad/c_test.sh"
out=$("$RUN" "$T/bad"); check "one fails: exit 1" $? 1
check "one fails: names it with its exit code" "$(grep -c "^FAIL $T/bad/c_test.sh (exit 3, " <<<"$out")" 1
check "one fails: shows its output" "$(grep -c '^    FAIL something broke$' <<<"$out")" 1
check "one fails: counts it" "$(tail -n 1 <<<"$out")" "test-hack: 1 of 3 shell tests failed"

# The tests run at once, not one after another.
mkdir -p "$T/slow"
for n in 1 2 3; do printf 'sleep 2\n' >"$T/slow/s${n}_test.sh"; done
start=$SECONDS
"$RUN" "$T/slow" >/dev/null
check "three 2s tests: under 5s" "$(( SECONDS - start < 5 ))" 1

# A test sees no make variables, even when the runner is a recipe of make check.
mkdir -p "$T/envfail"
printf '[ -z "${MAKEFLAGS-}${MAKELEVEL-}${C4_HEAVY_SLOT-}" ]\n' >"$T/envfail/e_test.sh"
MAKEFLAGS=k MAKELEVEL=1 C4_HEAVY_SLOT=1 "$RUN" "$T/envfail" >/dev/null
check "under make: the test sees none of make's variables" $? 0

# No tests at all is an error, not a pass.
mkdir -p "$T/empty"
"$RUN" "$T/empty" 2>/dev/null; check "no tests: exit 1" $? 1

# make check runs the shell tests (C4_HEAVY_SLOT=1: no slot for a dry run).
cd "$HACK/.." || exit 1
C4_HEAVY_SLOT=1 timeout 60 make -n check 2>/dev/null | grep -qx 'hack/test-hack.sh'
check "make check: runs hack/test-hack.sh" $? 0

exit $fail
