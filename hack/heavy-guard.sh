#!/usr/bin/env bash
# PreToolUse hook on Bash (.claude/settings.json): before a heavy job (make
# check, test, lint, lint-fix, docker-build, deploy, e2e, test-e2e;
# devcontainer up; docker build) it reads MemAvailable. Below
# RELAY_MIN_MEM_GIB (default 4, the relay's own threshold) it names what uses
# the memory: the processes with the most, and the Kind node. An interactive
# session then asks the user before running the job; a headless one
# (C4_HEADLESS=1, set by hack/relay.sh) gets the warning
# in its context instead and the job runs: in `claude -p` a question nobody
# can answer blocks the command. Silent otherwise.
# Before a cluster command (make deploy, sample, e2e, test-e2e, up, down) it
# reads the cluster lease (hack/lib/github.sh cluster): while another checkout
# holds a live one it denies the command and tells the session to run
# `hack/lib/github.sh cluster acquire`; without a holder the command goes on
# to the memory check, as before.
# Other commands cost one bash, with no subprocess and no file read.
#   RELAY_MEMINFO   file to read instead of /proc/meminfo (hack/heavy-guard_test.sh fakes it)
#   KIND_CLUSTER    the Kind cluster whose node it names (default c4factory)
set -uo pipefail

IFS= read -r -d '' input
# First a loose match on the raw JSON, with builtins only: it lets through
# every command the exact match below finds, and the rest go without a
# subprocess.
loose='make.*(check|test|e2e|lint|build|deploy|sample|up|down)|devcontainer.*up|docker.*build'
[[ $input =~ $loose ]] || exit 0

# A heavy job: one command, its words on one line, continued lines joined.
nl=$'\n'
jobs="(make([[:blank:]][^;&|$nl]*)?[[:blank:]](check|test|test-e2e|e2e|lint|lint-fix|docker-build|deploy)|devcontainer[[:blank:]]+up|docker[[:blank:]]+((buildx|compose|image)[[:blank:]]+)?build)"
sep='(^|[;&|(/]|[[:space:]])'
end='([;&|)<>]|[[:space:]]|$)'
cmd=$(jq -r '.tool_input.command // empty' <<<"$input" 2>/dev/null) || exit 0
cmd=${cmd//$'\\\n'/ }

# A cluster command while another checkout holds the cluster lease: deny it.
cluster="make([[:blank:]][^;&|$nl]*)?[[:blank:]](deploy|sample|e2e|test-e2e|up|down)"
if [[ $cmd =~ $sep$cluster$end ]]; then
  job=${BASH_REMATCH[2]}
  # shellcheck source=hack/lib/github.sh
  . "$(dirname "${BASH_SOURCE[0]}")/lib/github.sh"
  if holder=$(gh_lease_holder 2>/dev/null); then
    msg="The Kind cluster is in use: $holder holds the cluster lease, so \`make $job\` would change or stop the cluster under that session. Run \`hack/lib/github.sh cluster acquire\` first (it waits up to 30m); on exit 4, land what needs no cluster and hand off with only the cluster steps left."
    jq -n --arg m "$msg" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $m}}'
    exit 0
  fi
fi

[[ $cmd =~ $sep$jobs$end ]] || exit 0
job=${BASH_REMATCH[2]}

min=${RELAY_MIN_MEM_GIB:-4}
[[ $min =~ ^[0-9]+$ ]] || min=4
avail=$(awk '/^MemAvailable:/ { print $2; found = 1 } END { exit !found }' "${RELAY_MEMINFO:-/proc/meminfo}" 2>/dev/null) || exit 0
[ "$avail" -ge $((min * 1048576)) ] && exit 0

# The five programs with the most resident memory, summed over their
# processes; named by the command line, since node renames its main thread.
top=$(ps -eo rss=,args= 2>/dev/null | awk '
  { c = $2; sub(".*/", "", c); sum[c] += $1; n[c]++ }
  END { for (c in sum) printf "%d\t%s\t%d\n", sum[c], c, n[c] }' |
  sort -rn | head -5 | awk -F'\t' '
  { printf "%s%s%s %.1f GiB", (NR > 1 ? ", " : ""), $2, ($3 > 1 ? " (" $3 ")" : ""), $1 / 1048576 }')
node="${KIND_CLUSTER:-c4factory}-control-plane"
kind=
if command -v docker >/dev/null; then
  use=$(timeout 5 docker stats --no-stream --format '{{.MemUsage}}' "$node" 2>/dev/null) &&
    [ -n "$use" ] && kind="; the Kind node $node uses ${use%% /*}"
fi

msg="Memory is low: $(awk -v kb="$avail" 'BEGIN { printf "%.1f", kb / 1048576 }') GiB available, under the $min GiB a heavy job like \`$job\` needs (RELAY_MIN_MEM_GIB). Using the most: ${top:-unknown}$kind. Running it now can stall every session on this device; close a session or wait for other builds to finish first (docs/dev/testenv.md, \"Memory on the cluster device\")."
if [ "${C4_HEADLESS:-}" = 1 ]; then
  jq -n --arg m "$msg" '{hookSpecificOutput: {hookEventName: "PreToolUse", additionalContext: $m}}'
else
  jq -n --arg m "$msg" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "ask", permissionDecisionReason: $m}}'
fi
