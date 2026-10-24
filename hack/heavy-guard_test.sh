#!/usr/bin/env bash
# Tests for hack/heavy-guard.sh, the PreToolUse hook that warns before a heavy
# job when memory headroom is low, with a faked /proc/meminfo, ps and docker,
# and denies a cluster command in a headless session, or while another
# checkout holds the cluster lease.
# Run: hack/heavy-guard_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HOOK="$(cd "$(dirname "$0")" && pwd)/heavy-guard.sh"
export RELAY_MEMINFO=$T/meminfo RELAY_MIN_MEM_GIB=4 CALLED=$T/called C4_CLUSTER_LEASE=$T/lease \
  HEAVY_SLOTS_DIR=$T/slots HEAVY_JOBS=2
unset C4_HEADLESS KIND_CLUSTER IS_SANDBOX # an agent sandbox sets the last; the hook does nothing there (#1894)
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
meminfo() { printf 'MemTotal:       15400000 kB\nMemAvailable:   %s kB\n' $(($1 * 1048576)) >"$RELAY_MEMINFO"; }
# hook CMD [DESCRIPTION]: run the hook on a Bash tool call, with PATH=$HOOK_PATH
# when set; stdout in $T/out, stderr in $T/err.
hook() {
  jq -n --arg c "$1" --arg d "${2:-}" '{hook_event_name: "PreToolUse", tool_name: "Bash", tool_input: {command: $c, description: $d}}' >"$T/in"
  PATH=${HOOK_PATH:-$PATH} "$BASH" "$HOOK" <"$T/in" >"$T/out" 2>"$T/err"
}
decision() { jq -r '.hookSpecificOutput.permissionDecision // "none"' "$T/out" 2>/dev/null; }

# $T/spy holds commands that only note they ran; $T/bin fakes ps and docker.
mkdir -p "$T/bin" "$T/spy"
for c in jq awk ps docker cat; do printf '#!/bin/sh\necho %s >>"$CALLED"\nexit 1\n' $c >"$T/spy/$c"; done
cat >"$T/bin/ps" <<'EOF'
#!/bin/sh
echo ps >>"$CALLED"
printf '%s\n' ' 1200000 /usr/local/bin/claude -p' ' 1100000 claude' ' 900000 claude --resume 1' ' 300000 bash' \
  ' 2500000 /go/bin/golangci-lint run' ' 400000 /vscode/node --max-old-space-size=4096' ' 350000 tmux new -s x' \
  '  1000 sleep 30'
EOF
cat >"$T/bin/docker" <<'EOF'
#!/bin/sh
echo docker >>"$CALLED"
[ "$1 $2" = "stats --no-stream" ] && [ "$5" = c4factory-control-plane ] && echo '4.4GiB / 14.7GiB'
EOF
chmod +x "$T"/spy/* "$T"/bin/*
PATH=$T/bin:$PATH

# Light commands cost one bash: no subprocess, no file read, no output.
meminfo 1
HOOK_PATH=$T/spy hook "git status"
check "light: allowed" $? 0
check "light: silent" "$(cat "$T/out" "$T/err")" ""
check "light: runs nothing" "$(cat "$T/called" 2>/dev/null)" ""
HOOK_PATH=$T/spy hook "make check"
check "heavy: the spy sees it" "$(head -1 "$T/called" 2>/dev/null)" jq
rm -f "$T/called"
HOOK_PATH=$T/spy hook "make manifests generate"
check "make, light targets: silent" "$(cat "$T/out" "$T/err")" ""
check "make, light targets: runs nothing" "$(cat "$T/called" 2>/dev/null)" ""
hook "git log --oneline" "before make check"
check "heavy words only in the description: silent" "$(cat "$T/out")" ""
hook 'grep -n "make test" docs/dev/testenv.md'
check "heavy words quoted: silent" "$(cat "$T/out")" ""
hook $'make generate\ngo test ./...'
check "make and a heavy word on different lines: silent" "$(cat "$T/out")" ""

# Heavy jobs ask when headroom is low.
for c in "make check" "make -j4 test" "cd hack && make lint" "make lint-fix" "make docker-build IMG=x:dev" \
  "make deploy" "make test-e2e" "make manifests generate check" "devcontainer up --workspace-folder ." \
  "docker build -t x ." "docker buildx build ." "docker compose build" "docker image build ." \
  "(make test; echo done)" "make check>log" "/usr/bin/make check" \
  $'git pull\nmake check' $'make check\ngit push' $'make\tcheck' $'make -j4 \\\n  check'; do
  hook "$c"
  check "low, ${c//$'\n'/\\n}: asks" "$(decision)" ask
done
# Every goal in the Makefile's HEAVY_GOALS is a heavy job to the hook, so the
# two lists cannot drift: a goal added to the Makefile fails here until the
# hook matches it (#1324).
goals=$(sed -n 's/^HEAVY_GOALS[[:space:]]*=[[:space:]]*//p' "$(dirname "$HOOK")/../Makefile")
check "HEAVY_GOALS: read from the Makefile" "$([ -n "$goals" ] && echo yes)" yes
for g in $goals; do
  hook "make $g"
  check "low, HEAVY_GOALS make $g: asks" "$(decision)" ask
  hook "make $g IMG=x:dev"
  check "low, HEAVY_GOALS make $g with an argument: asks" "$(decision)" ask
done
for g in vulncheck test-templates-operator; do
  case " $goals " in *" $g "*) r=yes ;; *) r=no ;; esac
  check "HEAVY_GOALS: still lists $g" $r yes
done
hook "make check"
check "ask: exits 0" $? 0
reason=$(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out")
case $reason in *"1.0 GiB available"*) r=yes ;; *) r=no ;; esac
check "reason: names the headroom" $r yes
case $reason in *RELAY_MIN_MEM_GIB*) r=yes ;; *) r=no ;; esac
check "reason: names the threshold" $r yes
case $reason in *"make check"*) r=yes ;; *) r=no ;; esac
check "reason: names the job" $r yes
case $reason in *"claude (3) 3.1 GiB, golangci-lint 2.4 GiB, node 0.4 GiB, tmux 0.3 GiB, bash 0.3 GiB"*) r=yes ;; *) r=no ;; esac
check "reason: names the top processes" $r yes
case $reason in *sleep*) r=yes ;; *) r=no ;; esac
check "reason: only the top five" $r no
case $reason in *"Kind node c4factory-control-plane uses 4.4GiB"*) r=yes ;; *) r=no ;; esac
check "reason: names the Kind node" $r yes
KIND_CLUSTER=other hook "make check"
case $(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out") in *"Kind node"*) r=yes ;; *) r=no ;; esac
check "reason: no Kind node running" $r no
mv "$T/bin/docker" "$T/docker"
hook "make check"
check "no docker: still asks" "$(decision)" ask
mv "$T/docker" "$T/bin/docker"

# A headless session gets the warning in its context; nobody could answer a question.
C4_HEADLESS=1 hook "make check"
check "headless: does not ask" "$(decision)" none
case $(jq -r .hookSpecificOutput.additionalContext "$T/out") in *"1.0 GiB available"*) r=yes ;; *) r=no ;; esac
check "headless: warns" $r yes

# Enough headroom, or no way to read it: silent.
meminfo 8
rm -f "$T/called"
hook "make check"
check "headroom: silent" "$(cat "$T/out")" ""
check "headroom: looks no further" "$(cat "$T/called" 2>/dev/null)" ""
meminfo 3
RELAY_MIN_MEM_GIB=2 hook "make check"
check "headroom under a lower threshold: silent" "$(cat "$T/out")" ""
RELAY_MEMINFO=$T/missing hook "make check"
check "no meminfo: allowed" $? 0
check "no meminfo: silent" "$(cat "$T/out")" ""

# A cluster command while another checkout holds a live cluster lease is
# denied, whatever the memory; without a holder it runs as before.
REPO=$(cd "$(dirname "$HOOK")/.." && pwd)
meminfo 8
mkdir -p "$T/other"
lease() { jq -n --arg wt "$1" --argjson b "$2" '{name:"heron-50",issue:50,worktree:$wt,beat:$b}' >"$C4_CLUSTER_LEASE"; }
lease "$T/other" "$(date +%s)"
for c in "make deploy" "make sample" "make e2e" "make test-e2e" "make up" "make down" "cd x && make -j2 deploy IMG=y"; do
  hook "$c"
  check "lease held, $c: denied" "$(decision)" deny
done
reason=$(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out")
case $reason in *"heron-50 (#50, $T/other)"*) r=yes ;; *) r=no ;; esac
check "deny: names the holder" $r yes
case $reason in *"C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire"*) r=yes ;; *) r=no ;; esac
check "deny: tells the human to acquire" $r yes
C4_HEADLESS=1 hook "make deploy"
check "lease held, headless: denied" "$(decision)" deny
for c in "make check" "make upgrade" "make docker-build" "git log --grep 'make deploy'" \
  "git commit -m 'next: make deploy then verify'" 'echo "then make e2e"'; do
  hook "$c"
  check "lease held, $c: not a cluster command" "$(cat "$T/out")" ""
done
lease "$REPO" "$(date +%s)"
hook "make deploy"
check "own lease: runs" "$(cat "$T/out")" ""
lease "$T/other" $(($(date +%s) - 3 * 3600))
hook "make deploy"
check "stale lease: runs" "$(cat "$T/out")" ""
# With a pool, the lease that counts is the targeted cluster's: KIND_CLUSTER=
# in the command, else KIND_CLUSTER, else this checkout's leased one.
LEASE2=$T/lease-c4factory-2.lease
lease "$T/other" "$(date +%s)"
hook "make deploy KIND_CLUSTER=c4factory-2"
check "c4factory held, deploy to c4factory-2: runs" "$(cat "$T/out")" ""
KIND_CLUSTER=c4factory-2 hook "make deploy"
check "c4factory held, KIND_CLUSTER=c4factory-2: runs" "$(cat "$T/out")" ""
jq -n --arg wt "$REPO" --argjson b "$(date +%s)" '{cluster:"c4factory-2",name:"otter-9",issue:9,worktree:$wt,beat:$b}' >"$LEASE2"
hook "make deploy"
check "c4factory held, this checkout leased c4factory-2: runs" "$(cat "$T/out")" ""
jq -n --arg wt "$T/other" --argjson b "$(date +%s)" '{cluster:"c4factory-2",name:"heron-51",issue:51,worktree:$wt,beat:$b}' >"$LEASE2"
hook "KIND_CLUSTER=c4factory-2 make deploy"
check "c4factory-2 held elsewhere, deploy to it: denied" "$(decision)" deny
hook 'KIND_CLUSTER="c4factory-2" make deploy'
check "c4factory-2 held elsewhere, quoted name: denied" "$(decision)" deny
hook "make deploy KIND_CLUSTER='c4factory-2'"
check "c4factory-2 held elsewhere, single-quoted name: denied" "$(decision)" deny
case $(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out") in *"Kind cluster c4factory-2 is in use: heron-51"*) r=yes ;; *) r=no ;; esac
check "deny: names the cluster and its holder" $r yes
rm -f "$LEASE2"

rm -f "$C4_CLUSTER_LEASE"
hook "make up"
check "no lease: runs" "$(cat "$T/out")" ""
# A headless session never runs a cluster command, lease or not, and is never
# told to lease one (#353).
for c in "make deploy" "make e2e" "make up"; do
  C4_HEADLESS=1 hook "$c"
  check "headless, no lease, $c: denied" "$(decision)" deny
done
reason=$(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out")
case $reason in *"cluster acquire"*) r=yes ;; *) r=no ;; esac
check "headless deny: never says to acquire" $r no
case $reason in *"make check"*) r=yes ;; *) r=no ;; esac
check "headless deny: names the gate" $r yes
C4_HEADLESS=1 hook "git commit -m 'next: make deploy then verify'"
check "headless, quoted: not a cluster command" "$(cat "$T/out")" ""
meminfo 1
hook "make deploy"
check "no lease, low memory: asks" "$(decision)" ask

# The default threshold is 2 GiB, the relay's own.
unset RELAY_MIN_MEM_GIB
meminfo 2
hook "make check"
check "default threshold, 2 GiB: silent" "$(cat "$T/out")" ""
meminfo 1
hook "make check"
check "default threshold, 1 GiB: asks" "$(decision)" ask
case $(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out") in *"under the 2 GiB"*) r=yes ;; *) r=no ;; esac
check "default threshold: named" $r yes

# Every heavy-job slot taken: a make job will wait, and the session hears that
# the wait is not a hang, with who holds the slots.
meminfo 8
sleep 60 & a=$!
sleep 60 & b=$!
"$(dirname "$HOOK")/heavy-slot.sh" acquire $a "make check" 2>/dev/null
hook "make lint"
check "slots free: silent" "$(cat "$T/out")" ""
"$(dirname "$HOOK")/heavy-slot.sh" acquire $b "make test" 2>/dev/null
hook "make lint"
check "slots taken: allowed" "$(decision)" none
ctx=$(jq -r .hookSpecificOutput.additionalContext "$T/out")
case $ctx in *"not a hang"*) r=yes ;; *) r=no ;; esac
check "slots taken: not a hang" $r yes
case $ctx in *"pid $a "*"make check"*"pid $b "*"make test"*) r=yes ;; *) r=no ;; esac
check "slots taken: names the holders" $r yes
hook "docker build -t x ."
check "slots taken, not a make job: silent" "$(cat "$T/out")" ""
meminfo 1
hook "make lint"
check "slots taken, low memory: asks" "$(decision)" ask
case $(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out") in *"GiB available"*"not a hang"*) r=yes ;; *) r=no ;; esac
check "slots taken, low memory: says both" $r yes
kill $a $b; wait $a $b 2>/dev/null

# In an agent sandbox (IS_SANDBOX=1) the hook exits 0 at once, before it reads
# its input (#1894): its stdin is a pipe nobody writes to, so a read would
# hang until timeout kills it (exit 124); and a heavy job on low memory, which
# warns above, gets nothing.
meminfo 1
mkfifo "$T/quiet"; exec 3<>"$T/quiet"
IS_SANDBOX=1 timeout 10 "$BASH" "$HOOK" <"$T/quiet" >"$T/out" 2>"$T/err"
check "sandbox: exit 0 without reading its input" $? 0
check "sandbox: silent" "$(cat "$T/out" "$T/err")" ""
exec 3>&-
rm -f "$T/called"
IS_SANDBOX=1 HOOK_PATH=$T/spy hook "make check"
check "sandbox, make check on low memory: exit 0" $? 0
check "sandbox, make check on low memory: silent" "$(cat "$T/out" "$T/err")" ""
check "sandbox, make check on low memory: runs nothing" "$(cat "$T/called" 2>/dev/null)" ""

exit $fail
