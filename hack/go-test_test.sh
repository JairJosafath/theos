#!/usr/bin/env bash
# Tests for hack/go-test.sh, with a fake go: the controller suite runs as
# THEOS_TEST_PROCS go tests with THEOS_TEST_SHARD=i/n beside one go test of the
# rest, 1 runs it as before, short memory means 1, a failing part fails the
# run, and the parts' profiles and slowest specs are merged (#1283).
# Run: hack/go-test_test.sh
HERE=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
unset THEOS_TEST_PROCS THEOS_TEST_MIN_MEM_GIB THEOS_SLOWEST_SPECS

CTRL=github.com/JairJosafath/theos/internal/controller
# The parts after the first run the suite alone, not the package's plain tests.
R="-run ^TestControllers\$ $CTRL"
# The fake go logs "SHARD args" per call; with a profile it writes one block
# per package, with THEOS_SLOWEST_SPECS a part's report; FAIL_SHARD fails one,
# LONG_MODE pads one part's mode line past what a pipe holds (#1372).
cat >"$T/go" <<'EOF'
#!/usr/bin/env bash
shift # test
echo "${THEOS_TEST_SHARD:--} $*" >>"$GO_LOG"
cover=
args=()
while [ $# -gt 0 ]; do
  case $1 in
    -coverprofile) cover=$2; shift ;;
    -run) shift ;;
    *) args+=("$1") ;;
  esac
  shift
done
if [ -n "$cover" ]; then
  pad=0
  [ "${LONG_MODE:-}" != "${THEOS_TEST_SHARD:--}" ] || pad=1048576
  { printf 'mode: set%*s\n' "$pad" ''; for a in "${args[@]}"; do echo "$a/x.go:1.1,2.2 1 1"; done; } >"$cover"
fi
if [ -n "$THEOS_TEST_SHARD" ] && [ -n "${THEOS_SLOWEST_SPECS:-}" ] && [ "${NO_SLOW:-}" != "$THEOS_TEST_SHARD" ]; then
  i=${THEOS_TEST_SHARD%/*}
  printf 'Controller Suite: %d specs in %ds; the 2 slowest:\n%6.1fs  spec %d-a  (a.go:1)\n%6.1fs  spec %d-b  (b.go:2)\n' \
    $((10 * i)) $((100 + i)) $((20 * i)) "$i" $((i)) "$i" >"$THEOS_SLOWEST_SPECS.$i"
fi
echo "ok fake ${THEOS_TEST_SHARD:-}"
[ "${FAIL_SHARD:-}" != "${THEOS_TEST_SHARD:--}" ]
EOF
chmod +x "$T/go"
printf 'MemAvailable:   %d kB\n' $((8 * 1048576)) >"$T/meminfo"
export GO=$T/go GO_LOG=$T/log RELAY_MEMINFO=$T/meminfo

run() { : >"$GO_LOG"; "$HERE/go-test.sh" "$@" >"$T/out" 2>&1; echo $?; }
calls() { LC_ALL=C sort "$GO_LOG" | paste -sd'|'; }

check "default: 2 parts and the rest" "$(run -timeout 20m -- a/b $CTRL c/d)" 0
check "default: the calls" "$(calls)" "- -timeout 20m a/b c/d|1/2 -timeout 20m $CTRL|2/2 -timeout 20m -run ^TestControllers\$ $CTRL"
check "default: says so" "$(grep -c 'runs in 2 processes' "$T/out")" 1
check "default: prints each part" "$(grep -c '^ok fake' "$T/out")" 3

check "THEOS_TEST_PROCS=4" "$(THEOS_TEST_PROCS=4 run -- $CTRL)" 0
check "THEOS_TEST_PROCS=4: four parts, no rest" "$(calls)" "1/4 $CTRL|2/4 $R|3/4 $R|4/4 $R"

# The Tests workflow sets it empty on a self-hosted runner, which keeps the
# default.
check "THEOS_TEST_PROCS empty" "$(THEOS_TEST_PROCS= run -- $CTRL)" 0
check "THEOS_TEST_PROCS empty: the default, 2 parts" "$(calls)" "1/2 $CTRL|2/2 $R"

check "THEOS_TEST_PROCS=1" "$(THEOS_TEST_PROCS=1 run -- a/b $CTRL)" 0
check "THEOS_TEST_PROCS=1: one go test, as before" "$(calls)" "- a/b $CTRL"

check "no controller suite" "$(run -- a/b)" 0
check "no controller suite: one go test" "$(calls)" "- a/b"

printf 'MemAvailable:   %d kB\n' $((3 * 1048576)) >"$T/meminfo"
check "short memory" "$(run -- $CTRL)" 0
check "short memory: one process" "$(calls)" "- $CTRL"
check "short memory: says why" "$(grep -c '3 GiB available' "$T/out")" 1
check "short memory, THEOS_TEST_PROCS set: it wins" "$(THEOS_TEST_PROCS=2 run -- $CTRL)" 0
check "short memory, THEOS_TEST_PROCS set: two parts" "$(calls)" "1/2 $CTRL|2/2 $R"
printf 'MemAvailable:   %d kB\n' $((8 * 1048576)) >"$T/meminfo"

check "a failing part fails the run" "$(FAIL_SHARD=2/2 run -- a/b $CTRL)" 1
check "a failing part: the others still ran" "$(calls)" "- a/b|1/2 $CTRL|2/2 $R"
check "a failing rest fails the run" "$(FAIL_SHARD=- run -- a/b $CTRL)" 1
check "bad THEOS_TEST_PROCS" "$(THEOS_TEST_PROCS=x run -- $CTRL)" 2

check "coverage" "$(run -coverprofile "$T/cover.out" -- a/b $CTRL)" 0
check "coverage: the parts' mode line, every block" "$(LC_ALL=C sort "$T/cover.out" | paste -sd'|')" \
  "a/b/x.go:1.1,2.2 1 1|$CTRL/x.go:1.1,2.2 1 1|$CTRL/x.go:1.1,2.2 1 1|mode: set"
# The merge reads the first part's mode line and is done before a later
# part's long one is written: one mode line still, the first part's (#1372).
check "coverage, a later part's long mode line" "$(LONG_MODE=2/2 run -coverprofile "$T/cover.out" -- a/b $CTRL)" 0
check "coverage, a later part's long mode line: one mode line" "$(LC_ALL=C sort "$T/cover.out" | paste -sd'|')" \
  "a/b/x.go:1.1,2.2 1 1|$CTRL/x.go:1.1,2.2 1 1|$CTRL/x.go:1.1,2.2 1 1|mode: set"

# Many parts' profiles: a head -n 1 reading the first lines exits early, and
# under pipefail the writer's SIGPIPE added a second mode line (#1378).
# Three runs, since that race lost only most of the time.
modes=
for _ in 1 2 3; do
  THEOS_TEST_PROCS=300 run -coverprofile "$T/cover.out" -- $CTRL >/dev/null
  modes+="$(grep -c '^mode:' "$T/cover.out") $(head -n 1 "$T/cover.out") $(wc -l <"$T/cover.out");"
done
check "coverage, 300 parts: one mode line, first (#1378)" "$modes" \
  "1 mode: set 301;1 mode: set 301;1 mode: set 301;"

check "slowest specs" "$(THEOS_SLOWEST_SPECS=$T/slow THEOS_TEST_PROCS=3 run -- $CTRL)" 0
check "slowest specs: merged" "$(cat "$T/slow")" "Controller Suite: 60 specs in 3 of 3 processes, the longest 103s; the 10 slowest:
  60.0s  spec 3-a  (a.go:1)
  40.0s  spec 2-a  (a.go:1)
  20.0s  spec 1-a  (a.go:1)
   3.0s  spec 3-b  (b.go:2)
   2.0s  spec 2-b  (b.go:2)
   1.0s  spec 1-b  (b.go:2)"
check "slowest specs: the parts removed" "$(compgen -G "$T/slow.*" | wc -l)" 0
# A part answered from the cache writes nothing; a part a run with more
# processes left is not merged.
echo stale >"$T/slow.3"
check "slowest specs, a cached part" "$(THEOS_SLOWEST_SPECS=$T/slow NO_SLOW=2/2 run -- $CTRL)" 0
check "slowest specs, a cached part: named" "$(head -n 1 "$T/slow")" \
  "Controller Suite: 10 specs in 1 of 2 processes, the longest 101s (parts 2 cached, not here); the 10 slowest:"
check "slowest specs, a cached part: no stale part" "$(grep -c stale "$T/slow"; compgen -G "$T/slow.*" | wc -l)" "0
0"

exit $fail
