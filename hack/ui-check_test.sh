#!/usr/bin/env bash
# Tests that make check runs the UI's check (#1006): its steps (hack/check.sh,
# #127) hold ui-test, run through the tier cache (#1285) when ui/ changed,
# and ui-test runs npm run check in ui/ and fails when it fails, a Prettier
# violation among them. npm is a fake that records its calls, so the test needs
# neither Node nor the network.
# Run: hack/ui-check_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1
unset FORCE THEOS_HEAVY_SLOT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The recipe make check has, from make's database; help is only asked about
# (-q), so nothing runs. hack/check_test.sh shows hack/check.sh running make
# ui-test for a change under ui/, and nothing else.
make -pq help 2>/dev/null | grep -A8 '^check:' | grep -q 'hack/check.sh$'; check "make check runs hack/check.sh" $? 0
grep -q '|| \[ ! -f ui/package.json \] || tier ui-test$' hack/check.sh; check "hack/check.sh holds ui-test, through the tier cache" $? 0

# A fake npm: logs where it ran and with what, makes node_modules on npm ci,
# and fails as npm run check does on a file Prettier would rewrite.
cat >"$T/npm" <<EOF
#!/usr/bin/env bash
echo "\$(basename "\$PWD") \$*" >>"$T/calls"
# npm ci makes node_modules, unless the test wants one that makes nothing.
if [ "\$1" = ci ] && [ -z "\${FAKE_CI_MAKES_NOTHING:-}" ]; then mkdir -p node_modules; fi
if [ "\$*" = "run check" ] && [ -n "\${FAKE_PRETTIER_FAILS:-}" ]; then
  echo "[warn] src/bad.ts" >&2
  echo "[warn] Code style issues found in the above file. Run Prettier with --write to fix." >&2
  exit 1
fi
EOF
chmod +x "$T/npm"

# ui/node_modules is up to date in a checkout that ran npm ci; -o keeps make
# from running npm ci where it isn't, so only the check is under test.
make -s -o ui/node_modules ui-test NPM="$T/npm" >/dev/null 2>&1
check "ui-test passes when npm run check passes" $? 0
check "ui-test runs npm run check in ui/" "$(cat "$T/calls")" "ui run check"

FAKE_PRETTIER_FAILS=1 make -s -o ui/node_modules ui-test NPM="$T/npm" >"$T/out" 2>&1
check "ui-test fails on a Prettier violation" $? 2
grep -q "Code style issues found" "$T/out"; check "ui-test prints Prettier's complaint" $? 0

# The install cases run hack/ui.mk alone in a copy of ui/'s rule inputs, so
# this checkout's ui/node_modules stays as it was. Run here, the rule once left
# an empty ui/node_modules file in a checkout that had none, and every later
# ui-test skipped the install (#1133).
before=$(stat -c '%F %y' ui/node_modules 2>&1)
mkdir -p "$T/tree/ui"
cp ui/package.json ui/package-lock.json "$T/tree/ui/"
nm="$T/tree/ui/node_modules"
uitest() { rm -f "$T/calls"; make -s -C "$T/tree" -f "$ROOT/hack/ui.mk" ui-test NPM="$T/npm" "$@" >"$T/out" 2>&1; }
calls() { tr '\n' ';' <"$T/calls"; }

uitest
check "no node_modules: ui-test passes" $? 0
check "no node_modules: npm ci, then the check" "$(calls)" "ui ci --no-audit --no-fund;ui run check;"
[ -d "$nm" ]; check "no node_modules: npm ci made the directory" $? 0

uitest
check "installed node_modules: only the check" "$(calls)" "ui run check;"

uitest -W ui/package-lock.json
check "stale node_modules: npm ci, then the check" "$(calls)" "ui ci --no-audit --no-fund;ui run check;"

# An empty ui/node_modules file newer than ui/'s inputs, as #1133 left it.
rm -rf "$nm" && : >"$nm"
uitest
check "node_modules a file: ui-test passes" $? 0
check "node_modules a file: npm ci, then the check" "$(calls)" "ui ci --no-audit --no-fund;ui run check;"
[ -d "$nm" ]; check "node_modules a file: a directory after" $? 0

rm -rf "$nm"
FAKE_CI_MAKES_NOTHING=1 uitest
check "npm ci makes no directory: ui-test fails" $? 2
check "npm ci makes no directory: no check" "$(calls)" "ui ci --no-audit --no-fund;"
grep -q "ui/node_modules is not a directory after npm ci" "$T/out"; check "npm ci makes no directory: says so" $? 0
[ ! -e "$nm" ]; check "npm ci makes no directory: nothing touched" $? 0

check "this checkout's ui/node_modules is as it was" "$(stat -c '%F %y' ui/node_modules 2>&1)" "$before"

exit $fail
