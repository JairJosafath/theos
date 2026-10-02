# Handoff: v4 run of CEL Lens, parallel Crews in progress

## Goal

The user asked (2026-10-02) for the factory to build CEL Lens — CEL language
support for Kubernetes YAML — from a backlog of Gitea issues picked up by Crews,
with reviewers for quality and the right model and context per role; to work
autonomously and improve the factory from what the run finds; never to use Fable.
Mid-run they asked for agents to work in parallel where possible, and for the
"Who's working right now" panel to show the agents.

## Where it stands

Branch `feat/issue-driven-factory`, pushed, no PR (stacked on PR #8). Last commit
`68cd52b` (wip).

- Done and deployed: Crews, Tasks from issues, Agent `harness` and `model`, role
  guides, C4 context and related repos, Gitea reviews, labels and follow-ups,
  waiting on unavailable agents, the five Go and TypeScript templates, the Go
  toolchain, runner and agent images, the UI's Crew views, docs for v4.
- The run: both Crews **paused** at 12:10 UTC. Merged: cel-language-server #1–#3,
  vscode-extension #1–#3. Still running when paused (they finish on their own):
  `cel-language-server-4` (implementing) and `vscode-extension-5` (reviewing).
- In progress, committed as wip in `68cd52b`, **not deployed**: `Crew.spec.parallel`
  with `status.activeTasks`; Crew Tasks skip turn-taking; an approved PR that no
  longer merges goes back as `ResolveConflicts` in the same round; c4agent merges
  `main` at the start of each implement round and names conflicts.

## Live thread

- Envtest spec "sends an approved change that conflicts with main back to its
  implementer, in the same round" (internal/controller/crew_controller_test.go)
  passes alone (`-ginkgo.focus Crew`) but times out in the full suite at the
  wait for the second `orders-api-1-r1-review-a1` sandbox, even with 30 s. Not
  load: it is order-dependent. Suspects: reused sandbox and pod names across the
  conflict round (the stale-pod guard compares creation timestamps in seconds),
  or another spec's fake Git state (`gitFake` is shared; PR numbers per repo).
  Print the Task's status at failure (a `Should(Succeed(), func() string {...})`
  description) and compare `status.sandbox`, `phase` and `checks`.
- Quality so far is good: the Opus reviewer caught a benchmark padded with 900
  comment lines (0.35 ms measured, 1.4 ms real) and reproduced a double-client
  race in the extension. Record both in docs/results-v4.md.
- A Go fuzz test used 11 cores in one sandbox: sandboxes have no CPU limit.

## Next steps

1. Fix the envtest spec above; then `make check`.
2. UI: read `status.activeTasks` (ui/src/k8s/types.ts, model/factory.ts,
   demo/celLensFixture.ts), and make the Story view's "Who's working right now"
   (ui/src/components/Panels.tsx, `SessionsPanel`) list active Tasks with their
   Agent, model, issue and phase, next to Sessions. `make ui-test ui`.
3. Sample: CPU limits on the Agents (implementer `cpu: "4"`, reviewer `"2"`),
   `--max-agent-sandboxes=4` in config/manager/manager.yaml, `parallel: 2` on both
   Crews in config/samples/cel-lens/crews/crews.yaml.
4. `make deploy`; when no Task runs, `make agent-image-go` (6 GB load; see the
   factory-ops skill); `kubectl apply -k config/samples/cel-lens` and `.../crews`.
5. Resume the Crews and watch the first conflict get resolved; save evidence.
6. Fill "The CEL Lens run" in docs/results-v4.md; then a PR, asking the user first.

## State outside git

Kind cluster `c4factory` running (Gitea, operator with 2 agent slots, runner-go,
UI). Secret `cel-lens/claude-credentials` copied from toolbox. A Gitea
port-forward may still listen on :3917. Scratch spike code lives in the session
scratchpad only; nothing needed from it.

## References

- Spec: docs/superpowers/specs/2026-10-02-issue-driven-factory-design.md
- Plan: docs/plans/issue-driven-factory.md; results: docs/results-v4.md
- Backlog: config/samples/cel-lens/backlog/; evidence: docs/screenshots/v4-issue-loop.txt

## For the next session

Skills: `factory-ops` (watching and changing the factory), `handoff` (when the
context grows again). Docs: docs/dev/testenv.md, docs/architecture.md ("Flow of
one Task", "Flow of one Crew"). Use subagents for the UI change.
