#!/usr/bin/env bash
# Tests for hack/go-test.sh, with a fake go: the controller suite runs as
# C4_TEST_PROCS go tests with C4_TEST_SHARD=i/n beside one go test of the
# rest, 1 runs it as before, short memory means 1, a failing part fails the
# run, and the parts' profiles and slowest specs are merged (#1283).
# Run: hack/go-test_test.sh
HERE=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
unset C4_TEST_PROCS C4_TEST_MIN_MEM_GIB C4_SLOWEST_SPECS

CTRL=github.com/JairJosafath/c4factory/internal/controller
# The fake go logs "SHARD args" per call; with a profile it writes one block
# per package, with C4_SLOWEST_SPECS a part's report; FAIL_SHARD fails one.
cat >"$T/go" <<'EOF'
#!/usr/bin/env bash
shift # test
echo "${C4_TEST_SHARD:--} $*" >>"$GO_LOG"
cover=
args=()
while [ $# -gt 0 ]; do
  if [ "$1" = -coverprofile ]; then cover=$2; shift; else args+=("$1"); fi
  shift
done
if [ -n "$cover" ]; then
  { echo "mode: set"; for a in "${args[@]}"; do echo "$a/x.go:1.1,2.2 1 1"; done; } >"$cover"
fi
if [ -n "$C4_TEST_SHARD" ] && [ -n "${C4_SLOWEST_SPECS:-}" ]; then
  i=${C4_TEST_SHARD%/*}
  printf 'Controller Suite: %d specs in %ds; the 2 slowest:\n%6.1fs  spec %d-a  (a.go:1)\n%6.1fs  spec %d-b  (b.go:2)\n' \
    $((10 * i)) $((100 + i)) $((20 * i)) "$i" $((i)) "$i" >"$C4_SLOWEST_SPECS.$i"
fi
echo "ok fake ${C4_TEST_SHARD:-}"
[ "${FAIL_SHARD:-}" != "${C4_TEST_SHARD:--}" ]
EOF
chmod +x "$T/go"
printf 'MemAvailable:   %d kB\n' $((8 * 1048576)) >"$T/meminfo"
export GO=$T/go GO_LOG=$T/log RELAY_MEMINFO=$T/meminfo

run() { : >"$GO_LOG"; "$HERE/go-test.sh" "$@" >"$T/out" 2>&1; echo $?; }
calls() { LC_ALL=C sort "$GO_LOG" | paste -sd'|'; }

check "default: 2 parts and the rest" "$(run -timeout 20m -- a/b $CTRL c/d)" 0
check "default: the calls" "$(calls)" "- -timeout 20m a/b c/d|1/2 -timeout 20m $CTRL|2/2 -timeout 20m $CTRL"
check "default: says so" "$(grep -c 'runs in 2 processes' "$T/out")" 1
check "default: prints each part" "$(grep -c '^ok fake' "$T/out")" 3

check "C4_TEST_PROCS=4" "$(C4_TEST_PROCS=4 run -- $CTRL)" 0
check "C4_TEST_PROCS=4: four parts, no rest" "$(calls)" "1/4 $CTRL|2/4 $CTRL|3/4 $CTRL|4/4 $CTRL"

check "C4_TEST_PROCS=1" "$(C4_TEST_PROCS=1 run -- a/b $CTRL)" 0
check "C4_TEST_PROCS=1: one go test, as before" "$(calls)" "- a/b $CTRL"

check "no controller suite" "$(run -- a/b)" 0
check "no controller suite: one go test" "$(calls)" "- a/b"

printf 'MemAvailable:   %d kB\n' $((3 * 1048576)) >"$T/meminfo"
check "short memory" "$(run -- $CTRL)" 0
check "short memory: one process" "$(calls)" "- $CTRL"
check "short memory: says why" "$(grep -c '3 GiB available' "$T/out")" 1
check "short memory, C4_TEST_PROCS set: it wins" "$(C4_TEST_PROCS=2 run -- $CTRL)" 0
check "short memory, C4_TEST_PROCS set: two parts" "$(calls)" "1/2 $CTRL|2/2 $CTRL"
printf 'MemAvailable:   %d kB\n' $((8 * 1048576)) >"$T/meminfo"

check "a failing part fails the run" "$(FAIL_SHARD=2/2 run -- a/b $CTRL)" 1
check "a failing part: the others still ran" "$(calls)" "- a/b|1/2 $CTRL|2/2 $CTRL"
check "a failing rest fails the run" "$(FAIL_SHARD=- run -- a/b $CTRL)" 1
check "bad C4_TEST_PROCS" "$(C4_TEST_PROCS=x run -- $CTRL)" 2

check "coverage" "$(run -coverprofile "$T/cover.out" -- a/b $CTRL)" 0
check "coverage: one mode line, every block" "$(LC_ALL=C sort "$T/cover.out" | paste -sd'|')" \
  "a/b/x.go:1.1,2.2 1 1|$CTRL/x.go:1.1,2.2 1 1|$CTRL/x.go:1.1,2.2 1 1|mode: set"

check "slowest specs" "$(C4_SLOWEST_SPECS=$T/slow C4_TEST_PROCS=3 run -- $CTRL)" 0
check "slowest specs: merged" "$(cat "$T/slow")" "Controller Suite: 60 specs in 3 processes, the longest 103s; the 10 slowest:
  60.0s  spec 3-a  (a.go:1)
  40.0s  spec 2-a  (a.go:1)
  20.0s  spec 1-a  (a.go:1)
   3.0s  spec 3-b  (b.go:2)
   2.0s  spec 2-b  (b.go:2)
   1.0s  spec 1-b  (b.go:2)"
check "slowest specs: the parts removed" "$(compgen -G "$T/slow.*" | wc -l)" 0

exit $fail
