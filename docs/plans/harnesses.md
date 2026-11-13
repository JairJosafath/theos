# More harnesses and models

GitHub issue #58, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-10 in a session the owner ran.

## Goal

An Agent runs on the harness and the model its user has: Claude Code, the
OpenAI Codex CLI, OpenCode or the Gemini CLI, on the vendor's API or on a
model the user hosts. Whatever the vendor, no model key enters a sandbox, a
run's tokens and cost are recorded, and a Benchmark compares two harnesses
on the same work.

## Where we stand (main at 7be7d958)

- `Agent.spec.harness` is `claude-code` or `stand-in`
  (`api/v1beta1/agent_types.go`). `internal/agentrunner/job.go` refuses any
  other value, and `cmd/theos-agent/main.go` picks the agent.
- The runner already works through an interface, `Agent` (`Run`), with
  `UsageReporter` for cost and tokens (`internal/agentrunner/agent.go`). Two
  places ask whether the agent is Claude Code by its Go type: the session
  log (`checkpoint.go`) and whether a session can be resumed (`pause.go`).
  What says "the service is unavailable" is one pattern of Claude's words.
- Three features are Claude Code's own: the session guard that hands a full
  session to a fresh one (`--settings` hooks, the transcript file), the pause
  at a tool boundary (a hook), and the resume of a saved session
  (`--session-id`, `--resume`, the transcript's place under `~/.claude`).
- The model proxy has one upstream (`--upstream`, the chart's
  `agents.modelProxy.upstream`) and two key files, `CLAUDE_CODE_OAUTH_TOKEN`
  and `ANTHROPIC_API_KEY` (`internal/modelproxy`). A sandbox on the shared
  token gets `ANTHROPIC_BASE_URL` and a placeholder token
  (`internal/controller/task_sandbox.go`).
- Only Claude Code reports dollars. A run without them counts in
  `runsWithoutCost` (`model.RunState.Cost`). There is no price table.
- Every prompt tells the agent to read `AGENTS.md`; nothing relies on a
  harness loading a file by itself. The Standards ship `AGENTS.md` and no
  `CLAUDE.md`.
- The agent image installs Claude Code at a pinned version
  (`hack/agent/Dockerfile`).
- Adding an enum value is a compatible change (`test/compat`).

## Decisions, and why

1. **A harness is a name in a table inside `internal/agentrunner`.** Each
   entry builds the `Agent` for a job and says what its CLI prints when the
   service, not the agent, can't go on. What a harness can do beyond one
   call is an interface its agent has or hasn't: report usage, record its
   sessions, be resumed. The runner asks for the interface, never for the
   type. Claude Code is the first entry, moved, not changed; the stand-in is
   the second. Why a table: the API's enum, the runner's check and the
   picking in `theos-agent` are three lists today, and a fourth harness
   would be three edits to forget one of.
2. **Sessions stay Claude Code's until another harness earns them.** A
   harness without them runs one session to its end: `sessionTokenLimit`
   does nothing for it, a pause stops it between steps and not between tool
   calls, and a run that resumes starts its step again from the checkpoint's
   commit with the handoff note. That is how a custom `THEOS_AGENT_COMMAND`
   runs today. Codex and OpenCode compact their own context; what we lose
   is the pause mid-step, and the docs say so.
3. **The model proxy takes named upstreams, under `/upstreams/<name>/`.**
   The path without that prefix stays what it is, Anthropic, so no install
   changes. An upstream is a URL, how its key is sent (`bearer`, a header's
   name, or none) and the key's file in the Secret the proxy already
   mounts. One Secret, one key per upstream: the mount, the rotation without
   a restart and the "never copied" rule exist once, and the issue's "each
   with its own Secret" bought nothing more. A name that isn't configured
   answers 404 and forwards nothing. The proxy drops every credential
   header the client sent, whichever upstream it is. A prefix of its own,
   not the first path segment: `/v1/...` is Anthropic's, and a typo must
   not send one vendor's request to another with that vendor's key.
4. **An Agent names its upstream only when it isn't its harness's own.**
   `claude-code` uses the path without a prefix, `codex` the upstream
   `openai`, `gemini-cli` `gemini`. `Agent.spec.upstream` names another:
   `local` for a server in the cluster, say. The operator hands the runner
   one harness-neutral URL, `THEOS_MODEL_BASE_URL`, and the harness turns it
   into its CLI's own setting. The operator doesn't know the proxy's
   upstreams; a name the proxy lacks fails the run with the proxy's words.
   Checking it at admission would need the operator to read the proxy's
   settings, which nothing asks for yet.
5. **An Agent with a Secret of its own keeps working as today:** every key
   becomes an environment variable and the CLI talks to its vendor
   directly, over the public HTTPS the fence allows. `CODEX_API_KEY` in such
   a Secret is all a Codex Agent needs. The proxy is for the shared key.
6. **A model the user hosts is an upstream, reached through the proxy.**
   The fence keeps a sandbox off private addresses, and that stays. The
   proxy's own egress is open, so `http://ollama.models.svc:11434/v1` as an
   upstream with no key works where the cluster's policy lets the release
   namespace reach it. Codex speaks OpenAI's Responses API, which Ollama,
   vLLM and LiteLLM serve; OpenCode speaks Chat Completions too.
7. **Cost comes from the harness, else from a price table, else it is
   unknown.** The table is a setting (the chart's `manager.modelPrices`):
   US dollars per million tokens of input, output, cache read and cache
   write, per model name. A run that reported tokens and no dollars, on a
   model with a price, gets a cost worked out from them and says so
   (`costEstimated` on the run). A run on a model with no price stays in
   `runsWithoutCost`. No built-in prices: they change, and a wrong number in
   a release is worse than none. An Agent that names no model has no price.
8. **`AGENTS.md` stays the one guide.** Every prompt names it, Codex and
   OpenCode load it by themselves as well, and no Standard grows a second
   file to keep in step.
9. **One agent image carries every harness's CLI, pinned.** The Agent's
   `harness` picks, not its image, so a Benchmark of two harnesses needs no
   second image. The image grows by each CLI.
10. **Codex first, then OpenCode, then the Gemini CLI.** Codex is what the
    comparison names, and its documentation shows its whole event stream.
    OpenCode brings every OpenAI-compatible server, but its `run --format
    json` events are documented only by third parties, and the Gemini CLI's
    docs name its events without showing one.
11. **A harness built from documentation says so.** This session may not
    install or run the CLIs, so Codex's parser is written against the
    stream its documentation shows, and the harness is *experimental* in
    the guide until a person has recorded a real run:
    `hack/record-harness.sh codex` runs the contract's prompts with the real
    CLI and a key and writes the transcripts the tests read. OpenCode and
    the Gemini CLI wait for such a recording before any parser is written.

## Steps

Each is an issue under #58.

1. **The harness table.** `agentrunner.Harnesses`, `AgentFor(job)`, the
   three capability interfaces, Claude's code in a file of its own, and a
   contract test every harness passes on a stand-in CLI: a change, a
   review's verdict, a `Blocked:` line, a failed call, an unavailable
   service, usage. No behaviour changes.
2. **Named upstreams in the model proxy.** `--named-upstream`, the chart's
   `agents.modelProxy.upstreams`, the kustomize copy. Tests: a second
   upstream's key added and never returned, the client's credential
   headers dropped, an unconfigured name refused, a rotated key read.
3. **The price table.** `model.Prices`, the manager's flag, the chart's
   value, `costEstimated`, and the UI's mark on an estimated cost.
4. **The `codex` harness**, experimental: the enum, the command and the
   parser, `THEOS_MODEL_BASE_URL`, the sandbox's settings for the shared
   key, the CLI in the image, `hack/record-harness.sh`.
5. **`Agent.spec.upstream`**, and the guide's page for a model the user
   hosts.
6. **The `opencode` harness**, after a recorded run.
7. **The `gemini-cli` harness**, after a recorded run.
8. **A Benchmark sample** of `claude-code` and `codex` variants, the
   comparison's two rows, and `docs/design.md`, "Agents and Tasks" and "The
   fence", as each step lands.

## Progress

| Step | State |
| --- | --- |
| 1. The harness table | built: `internal/agentrunner/harness.go`, Claude's code in `claude.go` and the stand-in's in `standin.go`, moved as they were; `TestEveryHarnessMeetsTheContract`, `TestAgentFor`, and `TestTheHarnessEnumIsTheRunnersTable` in `internal/controller` |
| 2. Named upstreams | the proxy's part is built: `modelproxy.ParseUpstream`, `NewWithUpstreams`, `--named-upstream` on `cmd/model-proxy`, tests in `upstreams_test.go`. Open: the chart's `agents.modelProxy.upstreams`, its test in `test/chart`, and "The fence" in the design |
| 3 to 8 | open |

Found on the way:

- The proxy drops four credential headers on every request now, the
  default upstream included: `Authorization`, `X-Api-Key`, `X-Goog-Api-Key`
  and `Api-Key`. It dropped the first two before.
- A request for an upstream's root, `/upstreams/local`, reaches the
  upstream as `/v1/`, with the slash Go's reverse proxy adds.
- This session was refused the installing of the Codex and OpenCode CLIs,
  which is what decision 11 is written from.

## Done when

- `make check` passes after each step, and the design says what main does.
- Every harness in the enum passes the contract test, and the table, the
  enum and the runner's check can't disagree (a test reads all three).
- The proxy's tests show no credential of one upstream sent to another.
- A Task run on Codex records its tokens, and a cost when its model has a
  price.
- The guide says how to run an Agent on Codex and on a hosted model, and
  marks what nobody has run.

## Not tested, and who can

Nothing here runs a real model: no agent has a key, and agents touch no
cluster. Until a person runs `hack/record-harness.sh` and one Task on a
cluster, the `codex` harness is held by its documentation's examples alone.
