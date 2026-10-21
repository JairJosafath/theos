#!/usr/bin/env bash
# Runs every shell test under hack/ (hack/**/*_test.sh) at once, each with its
# output in a log, and prints one line per test; a failing test's line is
# followed by every "FAIL " line of its log above its last 30 lines, then
# those 30 lines, indented. Exit 1 when any test fails. make test-hack runs it, and
# make check runs that. Every test fakes what it calls (docker, kind, kubectl,
# gh, claude, curl), so none needs a cluster or the network.
#
#   hack/test-hack.sh [DIR]   the tests under DIR (default this hack/)
#
# The tests run with make's own variables cleared, so a test that runs make
# sees a make started from a shell, not a sub-make of make check; and with the
# relay's variables cleared, so a test run from a relay session (hack/relay.sh
# exports C4_HEADLESS, C4_RELAY_WORKER and, to its children, C4_RELAY_CHILD,
# C4_RELAY_TRIAGE and C4_RELAY_MAINTAIN) sees the same environment as one run
# from a shell: under the maintainer session, relay_test.sh's --triage dry run
# took the maintainer's lock (#1564).
set -uo pipefail

DIR=${1:-$(dirname "$0")}
LOGS=$(mktemp -d)
trap 'rm -rf "$LOGS"' EXIT

mapfile -t tests < <(find "$DIR" -name '*_test.sh' -type f | sort)
if [ ${#tests[@]} -eq 0 ]; then
  echo "test-hack: no *_test.sh under $DIR" >&2
  exit 1
fi

# run TEST N: the test with its output in $LOGS/N.log, its exit code and
# seconds in $LOGS/N.rc.
run() {
  local start=$SECONDS rc
  env -u MAKEFLAGS -u MFLAGS -u MAKELEVEL -u MAKEOVERRIDES -u C4_HEAVY_SLOT \
    -u C4_HEADLESS -u C4_RELAY_WORKER -u C4_RELAY_CHILD -u C4_RELAY_TRIAGE -u C4_RELAY_MAINTAIN \
    bash "$1" >"$LOGS/$2.log" 2>&1 </dev/null
  rc=$?
  echo "$rc $((SECONDS - start))" >"$LOGS/$2.rc"
}

for i in "${!tests[@]}"; do run "${tests[$i]}" "$i" & done
wait

failed=0
for i in "${!tests[@]}"; do
  read -r rc secs <"$LOGS/$i.rc"
  if [ "$rc" = 0 ]; then
    echo "ok   ${tests[$i]} (${secs}s)"
  else
    failed=$((failed + 1))
    echo "FAIL ${tests[$i]} (exit $rc, ${secs}s):"
    { head -n -30 "$LOGS/$i.log" | grep '^FAIL '; tail -n 30 "$LOGS/$i.log"; } | sed 's/^/    /'
  fi
done
if [ "$failed" -gt 0 ]; then
  echo "test-hack: $failed of ${#tests[@]} shell tests failed"
  exit 1
fi
echo "test-hack: all ${#tests[@]} shell tests passed"
