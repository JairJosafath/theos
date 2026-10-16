#!/usr/bin/env bash
# Tests for hack/runner/register.sh and the runner's entrypoint.sh: only the
# init container registers, with the token, and the runner container refuses
# the token (#1138). A fake gitea-runner records how it was called.
# Run: hack/runner/register_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

mkdir -p "$T/bin"
cat >"$T/bin/gitea-runner" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"$CALLS"
[ "$1" = register ] && echo '{"token":"runner-own"}' >"$RUNNER_FILE"
exit 0
EOF
chmod +x "$T/bin/gitea-runner"
export PATH="$T/bin:$PATH" CALLS="$T/calls" RUNNER_FILE="$T/data/.runner" RUNNER_CONFIG="$T/config.yaml"
mkdir -p "$T/data"
calls() { cat "$T/calls" 2>/dev/null; }

register() { env -u RUNNER_TOKEN GITEA_INSTANCE=http://gitea:3000 RUNNER_LABELS=go:host RUNNER_NAME=runner-1 "$@" bash "$DIR/register.sh" >"$T/out" 2>&1; }

register; check "register without RUNNER_TOKEN fails" $? 1
check "  and calls nothing" "$(calls)" ""

register RUNNER_TOKEN=reg-token; check "register with the token" $? 0
check "  registers once, with the token, name and labels" "$(calls)" \
	"register --no-interactive --config $T/config.yaml --instance http://gitea:3000 --token reg-token --name runner-1 --labels go:host"
check "  writes the runner file" "$(cat "$T/data/.runner")" '{"token":"runner-own"}'

rm "$T/calls"
echo '{"token":"before-restart"}' >"$T/data/.runner"
register RUNNER_TOKEN=reg-token; check "register again, the pod restarted" $? 0
check "  registers a new runner, not the one that stopped (#1143)" "$(calls)" \
	"register --no-interactive --config $T/config.yaml --instance http://gitea:3000 --token reg-token --name runner-1 --labels go:host"
check "  replaces the runner file" "$(cat "$T/data/.runner")" '{"token":"runner-own"}'
rm "$T/calls"

# The runner writes the file its config names; both scripts look for it there.
file=$(sed -n 's/^  file: //p' "$DIR/config.yaml")
for s in register.sh entrypoint.sh; do
	check "$s looks for config.yaml's runner.file" "$(sed -n 's/^file=\${RUNNER_FILE:-\(.*\)}$/\1/p' "$DIR/$s")" "$file"
done

run() { env -u RUNNER_TOKEN "$@" bash "$DIR/entrypoint.sh" >"$T/out" 2>&1; }
run RUNNER_TOKEN=reg-token; check "the runner container with RUNNER_TOKEN fails" $? 1
grep -q 'RUNNER_TOKEN is set in the runner container' "$T/out"; check "  and says why" $? 0
check "  and runs nothing" "$(calls)" ""

rm "$T/data/.runner"
run; check "the runner container unregistered fails" $? 1
grep -q 'register in an init container first' "$T/out"; check "  and says how to fix it" $? 0
check "  and runs nothing" "$(calls)" ""
exit $fail
