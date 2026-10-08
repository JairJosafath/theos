# Plan: Tasks say what tokens their agents used, and a long session hands off

GitHub issue #631, reported by gray-lenovo on v0.8.0. Agent: otter-631.

## Goal

Each Task and each Brief round shows the tokens its agent runs used (input,
output, cache read, cache write) next to their cost, and how large the
largest session got. An agent session that grows past its Agent's limit
hands its work to a fresh session with a short note, and the fresh one
carries on; the Task counts those handoffs and says when each happened. With
both, someone can see whether long sessions go with Tasks that need more
review rounds or fail.

Done when the three steps below are merged.

## What happened

Since #377 and #384 a Task says what its runs cost (`status.costMilliUSD`)
and a Brief round says what its Plan Task cost. A dollar figure mixes cheap
cache reads with output, so it says nothing about how full a session got,
and the size of one session is what hurts an agent's work: our own sessions
get sloppier past about 130k tokens, so we keep them under that and hand the
rest to a fresh session. In the factory nothing measures a session, and a
session runs until its agent ends it. `cel-lens-core-129` cost $6.12 over 3
rounds and `cel-lens-core-139` $2.29 over 3 rounds and failed; nobody can
tell whether those were a few long sessions or many short ones.

How a run works today: a sandbox runs one job (implement, review or plan);
c4agent calls the agent once, and on an implement whose check fails up to
`checkFixAttempts` (2) more times, each a fresh `claude -p --output-format
json` with the prompt on stdin (`internal/agentrunner/agent.go`). Each call
is one Claude Code session. c4agent reads `total_cost_usd` from the JSON,
`costMeter` adds up the calls, and the run's `Result` reaches the operator in
the pod's termination message, where `endRun` adds it to the Task.

## Decisions, and why

1. **Claude Code streams, and c4agent reads every turn.** The default command
   becomes `claude -p --output-format stream-json --verbose`. Its last line,
   `type: result`, has what the JSON output has today (`result`, `is_error`,
   `subtype`, `total_cost_usd`, `session_id`) plus `usage` and `modelUsage`;
   every `type: assistant` line has the `usage` of the model call behind it.
   Only the stream shows each turn, and a session's size is a per-turn fact:
   the single JSON object only has sums. A custom command that prints one
   JSON object, or plain text, still works as now; its sizes are unknown.
2. **What is counted.** A call's tokens are the result line's, summed over
   `modelUsage` (it covers subagents and the small model, like
   `total_cost_usd`), falling back to `usage` when `modelUsage` is missing:
   `inputTokens`, `outputTokens`, `cacheReadTokens` (cache reads) and
   `cacheWriteTokens` (cache creation). A run adds up its calls, a Task its
   runs, a Brief round its Plan Task's and a Brief its rounds, like the cost.
   One call that reports no tokens leaves its run's tokens out, and the run
   counts in `runsWithoutTokens`, as `runsWithoutCost` does for cost.
3. **A session's size is the prompt of its biggest turn.** For each
   assistant line, `input_tokens + cache_read_input_tokens +
   cache_creation_input_tokens` is how much context the model was given on
   that turn: everything the session held so far. The session's size is the
   largest of these; a subagent (its lines carry `parent_tool_use_id`) is a
   session of its own, measured the same way. `largestSessionTokens` is the
   largest over the run's sessions, the Task's runs, and a Brief round's (or a
   Brief's rounds'), so one number answers "did any session get long". Output
   tokens are left out: they are in the next turn's prompt anyway.
4. **The fields: one `TokenUsage` type in `status.tokens`.** `TaskStatus`,
   `BriefRound` and `BriefStatus` gain `tokens` with `input`, `output`,
   `cacheRead`, `cacheWrite`, `largestSession` (int64), `runsWithout` and
   `sessionHandoffs` (int32), all omitted when zero. One nested type keeps
   the four kinds of token together and out of the cost fields, which stay as
   they are; the design's cost bullet names both. The run's `Result` carries
   the same as `tokens` (omitted when unreported), well inside
   `MaxResultBytes`.
5. **The UI shows them beside the cost.** The Factory view's Task line and a
   Brief round's history line read `$6.12 · 1.4M tokens, 1.1M of them cache
   reads · largest session 142k`, the largest session marked when it passed
   the Agent's limit; a run without tokens says so, like a run without cost.
6. **The limit is the Agent's `sessionTokenLimit`.** Default 130000, minimum
   20000, maximum 1000000. It sits beside `model` and `maxRounds` because it
   depends on the model and the role: a bigger window may take more, a
   reviewer may need less. The operator passes it to c4agent as
   `C4AGENT_SESSION_TOKEN_LIMIT`.
7. **The session is told to hand off, by a hook.** c4agent writes a Claude
   Code settings file outside the repository (`--settings`, so nothing lands
   in the commit) with a `PreToolUse` hook that runs `c4agent session-guard`.
   The guard reads the hook's input, finds the session's newest turn in its
   transcript (`transcript_path`), and once that turn's size passes the limit
   denies every tool but writing the handoff file, with the reason: "This
   session is past N tokens. Stop here: write to HANDOFF what is done, what
   is left and what you learned that the code doesn't show, then end your
   turn." The agent stops at a tool boundary, so no edit is cut in half. A
   hook rather than c4agent killing the process: the agent writes the note
   itself, which only it can, and stops where it chooses.
8. **The fresh session gets the job and the note.** When a session ended
   with a handoff file, c4agent starts a fresh `claude -p` with the same
   prompt and a section: "A previous session worked on this and handed off.
   Its changes are in the working tree (git status, git diff). Its note:
   ...". The fresh session's answer is the call's answer; every session's
   tokens and cost add up into the call's. This lives inside `ClaudeAgent`,
   so implement, review, plan and check fixes all get it unchanged.
9. **A session that won't stop is stopped.** One past the limit by half
   again (a huge turn, or an agent ignoring the guard) is ended by c4agent,
   which then writes the note itself (the session's last assistant text and
   `git status --short`) and starts the fresh session the same way. After
   `maxSessionHandoffs` (3) handoffs in one call, the guard stops guarding
   and the last session runs to its end: a run never fails for length alone,
   and the sandbox timeout still bounds it.
10. **The Task shows when it happened.** The run's `Result` carries
    `sessionHandoffs` and, per handoff, the session size it happened at.
    `endRun` adds them to `status.tokens.sessionHandoffs` and posts a Normal
    Event `SessionHandedOff` ("round 2's implement run handed off at 134k
    tokens"), so the time is the run's end; the operator learns of a run only
    when it ends. The UI's token line says "handed off 2×".

## Steps

Each step is its own issue and pull request, and leaves main working.

1. **Tokens from the agent's stream to Task and Brief status** (#657,
   decisions 1-4). c4agent switches to stream-json and reads the tokens and
   session sizes; `Result`, `model.RunResult`, `TaskStatus`, `BriefRound`
   and `BriefStatus` carry `tokens`; `docs/design.md` says so. Tier 1: the
   stream parsed from a fixture captured from the agent image's Claude Code
   (a subagent in it, a call without `modelUsage`, a plain-JSON and a
   plain-text command), the run adding up its calls, an unreported call
   leaving the run's tokens out. envtest: a Task adds up its runs' tokens and
   keeps the largest session, counts a run without tokens, and a Brief round
   takes its Plan Task's.
2. **The UI shows the tokens** (#658, decision 5). The Factory view's Task line
   and a Brief's round history; the demo fixture gets token figures. Vitest:
   the line for reported tokens, a run without them, and a largest session
   past the limit. Depends on step 1.
3. **A long session hands off to a fresh one** (#659, decisions 6-10). The
   Agent's `sessionTokenLimit`, `c4agent session-guard`, the fresh session,
   the hard stop, `sessionHandoffs`, the Event and "handed off N×" in the UI.
   Tier 1: the guard on transcripts below and past the limit, allowing only
   the handoff file; a stand-in Claude command that streams growing turns and
   honours (or ignores) the guard, so the call hands off, carries the note
   into the fresh prompt, adds up both sessions and stops after
   `maxSessionHandoffs`; the sandbox spec sets the limit. envtest: the CEL
   bounds of `sessionTokenLimit`, and a run result with handoffs adds to the
   Task and posts the Event. Depends on step 1.

## Not done here

A chart of tokens against rounds and failures: with the numbers on every
Task and Brief round, the reporter can compare them; a Factory-view chart is
its own issue once the numbers have been watched for a while. The stand-in
harness reports no tokens. Whether 130k is the right default for every model
is for the numbers to show; the Agent can change it. A Kind run, which only a
human does, would show a real Claude Code session handing off in a sandbox.
