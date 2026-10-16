#!/usr/bin/env bash
# Tests for hack/gh-runner/entrypoint.sh, the runner container's entrypoint:
# run, on a home with a registration and stand-ins for env.sh, run.sh and the
# job-started hook, restores the registration, runs the hook before run.sh
# (#1351: the runner makes _temp in the work directory before any hook, so a
# work directory Docker made root's failed every job), and exits with run.sh's
# code.
# Run: hack/gh-runner/entrypoint_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ENTRY=$(cd "$(dirname "$0")" && pwd)/entrypoint.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

export GH_RUNNER_HOME=$T/home GH_RUNNER_STATE=$T/state LOG=$T/log
mkdir -p "$GH_RUNNER_HOME" "$GH_RUNNER_STATE"
echo '{"workFolder": "/w"}' >"$GH_RUNNER_STATE/.runner"
printf '#!/usr/bin/env bash\necho env >>"$LOG"\n' >"$GH_RUNNER_HOME/env.sh"
printf '#!/usr/bin/env bash\necho run >>"$LOG"\nexit 3\n' >"$GH_RUNNER_HOME/run.sh"
chmod +x "$GH_RUNNER_HOME/env.sh" "$GH_RUNNER_HOME/run.sh"
printf 'echo hook >>"$LOG"\n' >"$T/hook.sh"

ACTIONS_RUNNER_HOOK_JOB_STARTED=$T/hook.sh bash "$ENTRY" run
check "run: exits with run.sh's code" "$?" 3
check "run: restores the registration" "$(cat "$GH_RUNNER_HOME/.runner" 2>/dev/null)" '{"workFolder": "/w"}'
check "run: the hook before run.sh (#1351)" "$(tr '\n' ' ' <"$LOG")" "hook env run "

# A failing hook doesn't keep the runner from taking jobs.
: >"$LOG"
printf 'echo hook >>"$LOG"\nexit 1\n' >"$T/hook.sh"
out=$(ACTIONS_RUNNER_HOOK_JOB_STARTED=$T/hook.sh bash "$ENTRY" run 2>&1)
check "failing hook: exits with run.sh's code" "$?" 3
check "failing hook: run.sh still runs" "$(tr '\n' ' ' <"$LOG")" "hook env run "
check "failing hook: says so" "$out" "gh-runner: the job-started hook failed at start"

# Without a hook, run.sh runs as before.
: >"$LOG"
ACTIONS_RUNNER_HOOK_JOB_STARTED='' bash "$ENTRY" run
check "no hook: env.sh and run.sh" "$(tr '\n' ' ' <"$LOG")" "env run "

exit $fail
