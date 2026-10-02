# Handoff: v4 CEL Lens run, second part with parallel Crews

## Goal

The user asked (2026-10-02) for the factory to build CEL Lens from a backlog of
Gitea issues worked by Crews, with reviewers and the right model per role,
autonomously, improving the factory from what the run finds; never Fable. Now:
watch the Crews work two issues at once, see a conflict resolved, record it,
and open a PR (ask the user first).

## Where it stands

Branch `feat/issue-driven-factory`, pushed, no PR (stacked on PR #8).

- Done this session:
  - `cf1e308` fix(task): `runState` reads only the pod its Sandbox controls
    (`metav1.IsControlledBy`), not by creation time. That was the
    order-dependent envtest spec. Root cause: a conflict round reuses its
    round's sandbox names, and creation times count whole seconds. `make check`
    is green.
  - `61ab562` feat(ui): `status.activeTasks` on Crew cards, and "Who's working
    right now" lists active Crew Tasks (Agent, model, issue, phase).
    `npm run check` is green.
  - `cc77cc6` feat(config): `--max-agent-sandboxes=4`; CPU limits on the sample
    Agents (implementer 4, reviewer 2); `parallel: 2` on both Crews.
  - `fa4dd9c` docs: results-v4 has the first part of the run, the two reviewer
    catches and two new platform findings.
- Deployed: the operator (`make deploy`), the agent image (`make agent-image-go`,
  with the c4agent merge of `main` at each implement round), the UI (`make ui`),
  and the samples applied.
- Running since 12:40 UTC: Crew cel-language-server is Working on
  `cel-language-server-5` (#5, CRD rules) and `cel-language-server-7` (#7, LSP
  diagnostics), both in round 1, Implementing, in parallel. Crew vscode-extension
  is Idle: it has no ready issues.

## Live thread

- Waiting for the first parallel merge. #5 and #7 both touch the server's
  diagnostics, so whichever merges second should conflict and come back
  `ResolvingConflicts`, in the same round. Watch for the reason in its history.
  Then check that c4agent's round merged `main` and named the conflicts, and
  that the review and merge follow.
- When #5 or #7 closes, the Crew should start the next ready issue (#6 and
  #8–#11 wait on dependencies).
- Memory is the tight resource: the node has 12 CPUs and about 15 GiB; the host
  had 10.6 GB free with two implementers running. Four sandboxes at 3 GiB of
  in-memory workspace each could exhaust it. If host available memory drops
  below about 1.5 GB, pause a Crew.

## Decisions

- Made: 4 slots, CPU limits 4/2, Crew `parallel: 2` (the previous handoff's plan).
- Open, the user's to make: vscode-extension's backlog is all `status/proposed`.
  #4 (platform packages) needs server releases first; the rest are reviewer
  follow-ups (#7–#20). I recommend promoting the test-determinism ones (#8, #15)
  and #7 so that Crew works in parallel too. Ask the user before labelling.
- Open: the user asked whether a second PC (Docker, 16 GB) can add agent
  capacity. I answered: Kind is one host, so either k3s across both PCs with a
  registry (the cleaner option) or a second factory pointing at this Gitea. Not
  pursued; they were only curious.

## Next steps

1. Check progress: `kubectl -n cel-lens get crews,tasks`, and `free -m` for memory.
2. When a Task shows `ResolvingConflicts`, save evidence to
   `docs/screenshots/v4-parallel-conflict.txt`: the Task's history (see the
   factory-ops skill), the PR in Gitea and the sandbox log lines about the merge.
3. Take a UI screenshot of the Story view with two active Tasks into
   `docs/screenshots/`.
4. Fill "Second part, two Tasks per Crew" in docs/results-v4.md: throughput,
   conflicts and how they resolved, memory and CPU seen.
5. Ask the user about the vscode-extension backlog (above) and about opening the
   PR; then open it with `gh` (see the c4factory-repo memory for pushing).

## State outside git

- Kind cluster `c4factory`: Gitea, the operator (4 agent slots), runner-go and
  the UI. Both Crews are unpaused.
- Secret `cel-lens/claude-credentials`, copied from toolbox; never print it.
- A Gitea port-forward may still listen on :3917.

## References

- Spec: docs/superpowers/specs/2026-10-02-issue-driven-factory-design.md
- Plan: docs/plans/issue-driven-factory.md; results: docs/results-v4.md
- Backlog: config/samples/cel-lens/backlog/; earlier evidence: docs/screenshots/v4-issue-loop.txt

## For the next session

Skills: `factory-ops` (commands for Crews, Tasks, Gitea and memory) and
`handoff`. Docs: docs/architecture.md ("Flow of one Task", "Flow of one Crew").
