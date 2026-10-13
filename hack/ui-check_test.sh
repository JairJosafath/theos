#!/usr/bin/env bash
# Tests that make check runs the UI's check (#1006): check depends on ui-test,
# and ui-test runs npm run check in ui/ and fails when it fails, a Prettier
# violation among them. npm is a fake that records its calls, so the test needs
# neither Node nor the network.
# Run: hack/ui-check_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1
unset FORCE C4_HEAVY_SLOT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The rule make check has, from make's database; help is only asked about
# (-q), so nothing runs.
rule=$(make -pq help 2>/dev/null | grep -m1 '^check:')
[[ " $rule " == *" ui-test "* ]]; check "check depends on ui-test" $? 0

# A fake npm: logs where it ran and with what, and fails as npm run check does
# on a file Prettier would rewrite.
cat >"$T/npm" <<EOF
#!/usr/bin/env bash
echo "\$(basename "\$PWD") \$*" >>"$T/calls"
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

# Without node_modules, ui-test installs them first with npm ci.
rm -f "$T/calls"
make -s -W ui/package-lock.json ui-test NPM="$T/npm" >/dev/null 2>&1
check "stale node_modules: npm ci, then the check" "$(tr '\n' ';' <"$T/calls")" "ui ci --no-audit --no-fund;ui run check;"

exit $fail
