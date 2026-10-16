#!/usr/bin/env bash
# Tests for hack/keep-mtime.sh, which gives a file a generator rewrote with the
# same bytes its old mtime back, and for the Makefile's use of it and of Go's
# test cache: manifests and generate keep the mtimes of what they regenerate,
# make test passes no -coverprofile unless COVER=1 and -count=1 only for
# FORCE=1, and runs the tests with a cache home outside the module (#1282).
# Run: hack/keep-mtime_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HACK")"
KEEP=$HACK/keep-mtime.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
mtime() { stat -c %Y "$1" 2>/dev/null || stat -f %m "$1"; }

# Four files a day old; the "generator" rewrites same with the same bytes,
# changes changed, removes gone and writes new, which didn't exist.
cd "$T" || exit 1
printf 'a\n' >same && printf 'b\n' >changed && printf 'c\n' >gone && printf 'd\n' >'with space'
touch -d '-1 day' same changed gone 'with space'
old=$(mtime same)
echo "printf 'a\n' >same; printf 'B\n' >changed; rm gone; printf 'n\n' >new; printf 'd\n' >'with space'" >"$T/gen"
"$KEEP" same changed gone new 'with space' -- sh "$T/gen"
check "exit: the command's" $? 0
check "rewritten with the same bytes: old mtime" "$(mtime same)" "$old"
check "a name with a space: old mtime" "$(mtime 'with space')" "$old"
[ "$(mtime changed)" -gt "$old" ]; check "changed: the new mtime" $? 0
check "changed: the new bytes" "$(cat changed)" B
[ -e gone ]; check "removed: stays removed" $? 1
check "new: kept" "$(cat new)" n

# A failing command: its status, and what it rewrote unchanged still restored.
touch -d '-1 day' same && old=$(mtime same)
"$KEEP" same -- sh -c 'printf "a\n" >same; exit 3'
check "failing command: its status" $? 3
check "failing command: old mtime still" "$(mtime same)" "$old"

"$KEEP" same -- true; check "no file given changed: exit 0" $? 0
"$KEEP" same 2>/dev/null; check "no --: usage" $? 64
"$KEEP" same -- 2>/dev/null; check "no command: usage" $? 64

# The Makefile: each controller-gen run goes through keep-mtime.sh with the
# files it regenerates, and make test's go test flags.
cd "$ROOT" || exit 1
gen=$(timeout 120 make -n manifests generate CONTROLLER_GEN=/bin/true 2>/dev/null | grep '/bin/true')
check "manifests and generate: two controller-gen runs" "$(grep -c . <<<"$gen")" 2
check "both through keep-mtime.sh" "$(grep -c '^hack/keep-mtime.sh .* -- "/bin/true" ' <<<"$gen")" 2
grep -q '^hack/keep-mtime.sh .*config/crd/bases/[^ ]*\.yaml .*config/rbac/role\.yaml -- .* crd ' <<<"$gen"
check "manifests: keeps the CRDs and the role" $? 0
grep -q '^hack/keep-mtime.sh .*zz_generated\.deepcopy\.go -- .* object:' <<<"$gen"
check "generate: keeps the DeepCopy code" $? 0

# gotest VAR=VALUE...: the flags make test passes go test after the packages,
# with TEST_TIMEOUT=7m.
gotest() {
  env -u COVER -u FORCE C4_HEAVY_SLOT=1 timeout 120 make -n test ENVTEST_LOCKED=true CONTROLLER_GEN=/bin/true \
    TEST_TIMEOUT=7m "$@" 2>/dev/null | sed -n 's/.* go test .*$pkgs //p' | xargs
}
check "make test: TEST_TIMEOUT, no flag Go won't cache" "$(gotest)" "-timeout 7m"
check "COVER=1: writes cover.out" "$(gotest COVER=1)" "-timeout 7m -coverprofile cover.out"
check "FORCE=1: reruns every package" "$(gotest FORCE=1)" "-timeout 7m -count=1"
line=$(C4_HEAVY_SLOT=1 timeout 120 make -n test ENVTEST_LOCKED=true CONTROLLER_GEN=/bin/true 2>/dev/null | grep ' go test ')
[[ $line == *' go test -trimpath '* ]]; check "GO_TRIM still passed" $? 0
[[ $line == KUBEBUILDER_CONTROLPLANE_START_TIMEOUT=*' ETCD_ELECTION_TIMEOUT='*' ETCD_HEARTBEAT_INTERVAL='*' go test '* ]]
check "ENVTEST_ENV still set" $? 0
# envtest's port files: a cache home outside the module, and Go's own cache kept.
xdg=$(sed -n 's/.* XDG_CACHE_HOME="\([^"]*\)" .*/\1/p' <<<"$line")
[ -n "$xdg" ] && [[ $xdg != "$ROOT"/* ]]; check "tests: XDG_CACHE_HOME outside the module" $? 0
check "tests: GOCACHE kept" "$(sed -n 's/.* GOCACHE="\([^"]*\)" .*/\1/p' <<<"$line")" "$(go env GOCACHE)"

exit $fail
