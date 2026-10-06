#!/usr/bin/env bash
# PreToolUse hook on Bash (.claude/settings.json): before a heavy job (make
# check, test, lint, lint-fix, docker-build, deploy, e2e, test-e2e;
# devcontainer up; docker build) it reads MemAvailable. Below
# RELAY_MIN_MEM_GIB (default 2, the relay's own threshold) it names what uses
# the memory: the processes with the most, and the Kind node. An interactive
# session then asks the user before running the job; a headless one
# (C4_HEADLESS=1, set by hack/relay.sh) gets the warning
# in its context instead and the job runs: in `claude -p` a question nobody
# can answer blocks the command. When every heavy-job slot is taken
# (hack/heavy-slot.sh), a make job will wait for one: the session is told so,
# and that the wait is not a hang, with who holds the slots. Silent otherwise.
# A cluster command (make deploy, sample, e2e, test-e2e, up, down) in a
# headless session is denied: agents never deploy to or run a Kind cluster,
# make check is their gate, and Kind is a tier only a human runs. For a human
# it reads the lease of the Kind cluster the command targets
# (hack/lib/github.sh cluster): KIND_CLUSTER= in the command, else
# KIND_CLUSTER, else the one this checkout leased, else c4factory. While
# another checkout holds a live lease on it, it denies the command and tells
# the human to lease one (C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster
# acquire); without a holder the command goes on to the memory check, as before.
# Other commands cost one bash, with no subprocess and no file read.
#   RELAY_MEMINFO   file to read instead of /proc/meminfo (hack/heavy-guard_test.sh fakes it)
#   KIND_CLUSTER    the Kind cluster a cluster command targets and whose node it names
#                   (default: this checkout's leased cluster, else c4factory)
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
# Quoted text (a commit message, an echo) is not a command.
unquoted=$(sed -E "s/'[^']*'//g; s/\"[^\"]*\"//g" <<<"$cmd")
if [[ $unquoted =~ $sep$cluster$end ]]; then
  job=${BASH_REMATCH[2]}
  if [ "${C4_HEADLESS:-}" = 1 ]; then
    msg="Agents never deploy to or run a Kind cluster, so \`make $job\` is not for this session: make check (Go tests, validate-yaml, envtest) is the gate, and Kind is a tier only a human runs. Land the code with the tests the lower tiers can give, and say in the pull request what a human run of Kind would check."
    jq -n --arg m "$msg" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $m}}'
    exit 0
  fi
  # shellcheck source=hack/lib/github.sh
  . "$(dirname "${BASH_SOURCE[0]}")/lib/github.sh"
  kc=${KIND_CLUSTER:-}
  # On $cmd, not $unquoted: the quotes of KIND_CLUSTER="c4factory-2" are taken off there with the name.
  [[ $cmd =~ (^|[[:space:]])KIND_CLUSTER=[\"\']?([^\"\'[:space:]\;\&\|]+) ]] && kc=${BASH_REMATCH[2]}
  [ -n "$kc" ] || kc=$(gh_cluster name 2>/dev/null)
  kc=${kc:-c4factory}
  if holder=$(gh_lease_holder "$kc" 2>/dev/null); then
    msg="The Kind cluster $kc is in use: $holder holds its lease, so \`make $job\` would change or stop it under that session. Lease one first, \`C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire\`: it leases a free cluster of the pool to this checkout (or waits up to 30m), and make here then uses that one; exit 4 means every cluster is held."
    jq -n --arg m "$msg" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $m}}'
    exit 0
  fi
fi

[[ $cmd =~ $sep$jobs$end ]] || exit 0
job=${BASH_REMATCH[2]} target=${BASH_REMATCH[4]}

# A make job takes a heavy-job slot first; when none is free it waits.
queue=
if [ -n "$target" ] && ! slots=$("${0%/*}/heavy-slot.sh" status 2>/dev/null); then
  queue="Every heavy-job slot on this device is taken (HEAVY_JOBS), so \`$job\` will first wait for one, printing who holds them; that wait is not a hang. Give the command a timeout long enough for the jobs ahead of it, or run it in the background, and don't kill it for being quiet. ${slots//$'\n'/ }"
fi
say() {
  jq -n --arg m "$1" '{hookSpecificOutput: {hookEventName: "PreToolUse", additionalContext: $m}}'
}

min=${RELAY_MIN_MEM_GIB:-2}
[[ $min =~ ^[0-9]+$ ]] || min=2
avail=$(awk '/^MemAvailable:/ { print $2; found = 1 } END { exit !found }' "${RELAY_MEMINFO:-/proc/meminfo}" 2>/dev/null) ||
  { [ -n "$queue" ] && say "$queue"; exit 0; }
if [ "$avail" -ge $((min * 1048576)) ]; then
  [ -n "$queue" ] && say "$queue"
  exit 0
fi

# The five programs with the most resident memory, summed over their
# processes; named by the command line, since node renames its main thread.
top=$(ps -eo rss=,args= 2>/dev/null | awk '
  { c = $2; sub(".*/", "", c); sum[c] += $1; n[c]++ }
  END { for (c in sum) printf "%d\t%s\t%d\n", sum[c], c, n[c] }' |
  sort -rn | head -5 | awk -F'\t' '
  { printf "%s%s%s %.1f GiB", (NR > 1 ? ", " : ""), $2, ($3 > 1 ? " (" $3 ")" : ""), $1 / 1048576 }')
node="${kc:-${KIND_CLUSTER:-c4factory}}-control-plane"
kind=
if command -v docker >/dev/null; then
  use=$(timeout 5 docker stats --no-stream --format '{{.MemUsage}}' "$node" 2>/dev/null) &&
    [ -n "$use" ] && kind="; the Kind node $node uses ${use%% /*}"
fi

msg="Memory is low: $(awk -v kb="$avail" 'BEGIN { printf "%.1f", kb / 1048576 }') GiB available, under the $min GiB a heavy job like \`$job\` needs (RELAY_MIN_MEM_GIB). Using the most: ${top:-unknown}$kind. Running it now can stall every session on this device; close a session or wait for other builds to finish first (docs/dev/testenv.md, \"Memory on the cluster device\").${queue:+ $queue}"
if [ "${C4_HEADLESS:-}" = 1 ]; then
  say "$msg"
else
  jq -n --arg m "$msg" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "ask", permissionDecisionReason: $m}}'
fi
