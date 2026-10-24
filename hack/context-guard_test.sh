#!/usr/bin/env bash
# Tests for hack/context-guard.sh, the PostToolUse hook that tells a session
# to hand off when its context grows (#1514), against fake transcripts.
# Run: hack/context-guard_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HOOK="$(cd "$(dirname "$0")" && pwd)/context-guard.sh"
export C4_CONTEXT_STATE_DIR=$T/state
unset C4_CONTEXT_SOFT C4_CONTEXT_HARD IS_SANDBOX # an agent sandbox sets the last; the hook does nothing there (#1894)
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# transcript FILE INPUT CACHE_READ CACHE_CREATION: a transcript whose newest
# assistant message has that usage, after an older, bigger one and followed
# by a tool result.
transcript() {
  {
    echo '{"type":"user","message":{"role":"user","content":"hi"}}'
    echo '{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":1,"cache_read_input_tokens":500000,"cache_creation_input_tokens":0}}}'
    printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":%s,"cache_creation_input_tokens":%s,"cache_read_input_tokens":%s,"cache_creation":{"ephemeral_5m_input_tokens":99999},"output_tokens":7}}}\n' "$2" "$4" "$3"
    echo '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"\"input_tokens\":999999"}]}}'
  } >"$1"
}
run() { printf '{"session_id":"%s","transcript_path":"%s","hook_event_name":"PostToolUse"}' "$1" "$2" | "$HOOK"; }
has() { grep -q "$2" <<<"$1" && echo yes || echo no; }

transcript "$T/small.jsonl" 3 50000 1000
out=$(run s1 "$T/small.jsonl"); rc=$?
check "below soft: silent" "$out" ""
check "below soft: exit 0" "$rc" 0

transcript "$T/soft.jsonl" 3 79000 1000
out=$(run s1 "$T/soft.jsonl")
check "at soft (80003): tells it to hand off" "$(has "$out" 'past the soft limit of 80k')" yes
check "at soft: valid hook JSON" "$(jq -r .hookSpecificOutput.hookEventName <<<"$out" 2>/dev/null)" PostToolUse
check "at soft: says 80k" "$(has "$out" 'context is 80k tokens')" yes
out=$(run s1 "$T/soft.jsonl")
check "at soft: once per session" "$out" ""
out=$(run s2 "$T/soft.jsonl")
check "at soft: again in another session" "$(has "$out" 'soft limit')" yes

transcript "$T/hard.jsonl" 10 119000 990
for i in 1 2; do
  out=$(run s1 "$T/hard.jsonl"); rc=$?
  check "at hard, call $i: stop now" "$(has "$out" 'Stop new work now')" yes
  check "at hard, call $i: exit 0, never blocks" "$rc" 0
done

out=$(C4_CONTEXT_SOFT=1000 C4_CONTEXT_HARD=2000 run s3 "$T/small.jsonl")
check "C4_CONTEXT_HARD overrides" "$(has "$out" 'hard limit of 2k')" yes

out=$(run s4 "$T/missing.jsonl"); rc=$?
check "missing transcript: silent" "$out" ""
check "missing transcript: exit 0" "$rc" 0
out=$(echo 'not json' | "$HOOK"); rc=$?
check "bad input: exit 0" "$rc" 0

# In an agent sandbox (IS_SANDBOX=1) the hook exits 0 at once, before it reads
# its input (#1894): its stdin is a pipe nobody writes to, so a read would
# hang until timeout kills it (exit 124); and a context past the hard limit,
# which nags above, gets nothing.
mkfifo "$T/quiet"; exec 3<>"$T/quiet"
out=$(IS_SANDBOX=1 timeout 10 "$HOOK" <"$T/quiet" 2>&1); rc=$?
check "sandbox: exit 0 without reading its input" "$rc" 0
check "sandbox: silent" "$out" ""
exec 3>&-
transcript "$T/sandbox-hard.jsonl" 3 130000 1000
out=$(IS_SANDBOX=1 run s9 "$T/sandbox-hard.jsonl" 2>&1); rc=$?
check "sandbox, past the hard limit: exit 0" "$rc" 0
check "sandbox, past the hard limit: silent" "$out" ""

exit $fail
