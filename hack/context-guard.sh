#!/usr/bin/env bash
# A PostToolUse hook (.claude/settings.json) that keeps a session's context
# small (#1514; the owner asked for it on 2026-10-21): it reads the size of the
# context from the newest assistant message's usage in the session's
# transcript (input + cache read + cache creation tokens) and
#   - below C4_CONTEXT_SOFT (default 80000) says nothing;
#   - at or over it, once per session, tells the agent to finish its small
#     step, commit, push, hand off (the handoff skill) and end the session;
#   - at or over C4_CONTEXT_HARD (default 120000), after every tool call,
#     tells it to stop new work now and hand off, mid-task.
# It is a nudge: it never blocks a tool call, so git and hack/lib/github.sh
# still run for the handoff. It needs no jq, and reads only the transcript's
# tail. The once-per-session mark lives under C4_CONTEXT_STATE_DIR (default
# $TMPDIR/c4-context-guard).
#
#   hack/context-guard.sh < hook-input.json
set -u

soft=${C4_CONTEXT_SOFT:-80000}
hard=${C4_CONTEXT_HARD:-120000}
# In an agent sandbox (IS_SANDBOX=1, hack/agent/Dockerfile) the hook does
# nothing, before it reads its input: the sandbox has no heavy-job slots, no
# cluster lease, no claim and no main to pull (#1894).
[ "${IS_SANDBOX:-}" = 1 ] && exit 0

input=$(cat)

# field NAME: a string field of the hook's input JSON.
field() { sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$input" | head -n1; }

transcript=$(field transcript_path)
session=$(field session_id)
[ -n "$transcript" ] && [ -r "$transcript" ] || exit 0

# The newest assistant line with a usage, among the last lines.
line=$(tail -n 60 "$transcript" | grep '"type":"assistant"' | grep '"usage":{' | tail -n1)
[ -n "$line" ] || exit 0
usage=${line##*\"usage\":\{}
num() { local n; n=$(grep -o "\"$1\":[0-9]*" <<<"$usage" | head -n1 | cut -d: -f2); echo "${n:-0}"; }
tokens=$(($(num input_tokens) + $(num cache_read_input_tokens) + $(num cache_creation_input_tokens)))

[ "$tokens" -ge "$soft" ] || exit 0

k=$((tokens / 1000))k
if [ "$tokens" -ge "$hard" ]; then
  msg="Context guard: this session's context is $k tokens, over the hard limit of $((hard / 1000))k. Stop new work now. Commit what you have (WIP is fine), push it, write the handoff (the handoff skill: hack/lib/github.sh handoff N FILE) saying exactly where it stands and what comes next, and end your turn. Handing off mid-task is expected; the next session continues from the handoff."
else
  dir=${C4_CONTEXT_STATE_DIR:-${TMPDIR:-/tmp}/c4-context-guard}
  mark=$dir/${session:-unknown}.soft
  [ -e "$mark" ] && exit 0
  mkdir -p "$dir" 2>/dev/null && : >"$mark" 2>/dev/null
  msg="Context guard: this session's context is $k tokens, past the soft limit of $((soft / 1000))k. Finish the current small step (no new task or step), commit, push, write the handoff (the handoff skill: hack/lib/github.sh handoff N FILE) saying exactly where it stands, then end the session. Never continue past $((hard / 1000))k; the next session continues from the handoff."
fi
printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"%s"}}\n' "$msg"
exit 0
