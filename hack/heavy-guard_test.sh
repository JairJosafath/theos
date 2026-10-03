#!/usr/bin/env bash
# Tests for hack/heavy-guard.sh, the PreToolUse hook that warns before a heavy
# job when memory headroom is low, with a faked /proc/meminfo, ps and docker.
# Run: hack/heavy-guard_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HOOK="$(cd "$(dirname "$0")" && pwd)/heavy-guard.sh"
export RELAY_MEMINFO=$T/meminfo RELAY_MIN_MEM_GIB=4
unset C4_HEADLESS KIND_CLUSTER
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

mkdir -p "$T/bin" "$T/spy"
for c in jq awk ps docker cat; do printf '#!/bin/sh\necho %s >>"%s"\nexit 1\n' $c "$T/called" >"$T/spy/$c"; chmod +x "$T/spy/$c"; done
cat >"$T/bin/ps" <<'EOF'
#!/bin/sh
printf '%s\n' ' 1200000 claude' ' 1100000 claude' ' 900000 claude' ' 300000 bash' ' 2500000 golangci-lint' ' 400000 node' \
  ' 350000 tmux: server' '  1000 sleep'
EOF
cat >"$T/bin/docker" <<'EOF'
#!/bin/sh
[ "$1 $2" = "stats --no-stream" ] && [ "$5" = c4factory-control-plane ] && echo '4.4GiB / 14.7GiB'
EOF
chmod +x "$T/bin/ps" "$T/bin/docker"
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

# Heavy jobs ask when headroom is low.
for c in "make check" "make -j4 test" "cd hack && make lint" "make lint-fix" "make docker-build IMG=x:dev" \
  "make deploy" "make test-e2e" "make manifests generate check" "devcontainer up --workspace-folder ." \
  "docker build -t x ." "docker buildx build ." "(make test; echo done)" $'git pull\nmake check'; do
  hook "$c"
  check "low, $c: asks" "$(decision)" ask
done
hook "make check"
reason=$(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out")
case $reason in *"1.0 GiB available"*) r=yes ;; *) r=no ;; esac
check "reason: names the headroom" $r yes
case $reason in *RELAY_MIN_MEM_GIB*) r=yes ;; *) r=no ;; esac
check "reason: names the threshold" $r yes
case $reason in *"make check"*) r=yes ;; *) r=no ;; esac
check "reason: names the job" $r yes
case $reason in *"claude (3) 3.1 GiB, golangci-lint 2.4 GiB, node 0.4 GiB, tmux: server 0.3 GiB, bash 0.3 GiB"*) r=yes ;; *) r=no ;; esac
check "reason: names the top processes" $r yes
case $reason in *sleep*) r=yes ;; *) r=no ;; esac
check "reason: only the top five" $r no
case $reason in *"Kind node c4factory-control-plane uses 4.4GiB"*) r=yes ;; *) r=no ;; esac
check "reason: names the Kind node" $r yes
KIND_CLUSTER=other hook "make check"
case $(jq -r .hookSpecificOutput.permissionDecisionReason "$T/out") in *"Kind node"*) r=yes ;; *) r=no ;; esac
check "reason: no Kind node running" $r no
rm "$T/bin/docker"
hook "make check"
check "no docker: still asks" "$(decision)" ask

# A headless session gets the warning in its context; nobody could answer a question.
C4_HEADLESS=1 hook "make check"
check "headless: does not ask" "$(decision)" none
case $(jq -r .hookSpecificOutput.additionalContext "$T/out") in *"1.0 GiB available"*) r=yes ;; *) r=no ;; esac
check "headless: warns" $r yes

# Enough headroom, or no way to read it: silent.
meminfo 8
hook "make check"
check "headroom: silent" "$(cat "$T/out")" ""
meminfo 3
RELAY_MIN_MEM_GIB=2 hook "make check"
check "headroom under a lower threshold: silent" "$(cat "$T/out")" ""
RELAY_MEMINFO=$T/missing hook "make check"
check "no meminfo: allowed" $? 0
check "no meminfo: silent" "$(cat "$T/out")" ""

exit $fail
