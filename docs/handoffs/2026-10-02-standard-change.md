# Handoff: Standard changes, then the extension's release

## Goal

The user wants the factory to run without them: an issue that needs a protected
file (Makefile, CI) gets it through a gated Task the factory files, reviews and
merges itself; then CEL Lens's extension v0.1.0 ships. Work autonomously, don't
check in unless the user shows concern (memory `work-autonomously`). Never use
the Fable model, for anything (the user's one hard rule).

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9. The hand-off commit is pushed.

- Plan docs/plans/standard-change.md, 18 tasks, run as subagent-driven development.
  - Done and reviewed: Tasks 1–7 (see the ledger), Task 8 `85d2b00`
    (gitprovider: `Issue.Milestone`, `AddIssueDependency`, `PullRequestFiles`,
    `ClosePullRequest`, `ForceMergePullRequest`; fakes `setPullFiles`,
    `setIssueMilestone`, `forcedMerges`, stricter fake merge).
  - Not started: Tasks 9–18. Next is Task 9 (controller: propose — file the
    Standard change issue, `Blocked`, `status.standardChange`).
- `fa1899f` hack/relay.sh: chains headless sessions overnight from the newest
  handoff (the user runs it; auto mode refuses me launching it). AGENTS.md now
  says: hand off between tasks, never inside one; past ~100k after a task, hand off.
- CEL Lens: server #16 merged; server v0.2.0 (#27 #29 #31 #32 #44 #45 #49 #50)
  is `status/ready`. Server #36 was implementing round 1 at hand-off.
  Extension: its whole v0.1.0 batch merged except #4 (needs the Makefile; waits
  for Standard changes, Task 17/18).

## Live thread

- Ledger: `.superpowers/sdd/standard-change/progress.md` (git-ignored, local):
  pre-flight scan, rulings F1–F10 and every task's lines. If lost, the rulings
  beyond F1–F10 that still matter:
  - Task 5: the Task branch keeps the protected edit in an intermediate commit;
    fine because the operator squash-merges (gitea_agents.go:312).
  - Task 5: `changedPaths` uses `--no-renames`.
  - e2e: dropped (memory `skip-e2e`); `make check` + the live Kind check remain.
- Carry into dispatches (deferred findings):
  - Task 9: neutralise `<!-- c4factory:standard-change … -->` in the agent's
    Reason before the operator relies on the marker to find its issue
    (standard.go:176). F5: the reuse search skips `status.standardChange.issue`.
    F8: convert `StandardChangeLabel()` to `gitprovider.Label` in task_standard.go.
  - Task 11: the gated merge must fail closed — `PullRequestFiles` stops at 20
    pages (1000 files) silently; make it error when page 20 is still full, or
    refuse at 1000 files. Also: CI `pending` in `GatedMergeRefusal` means wait,
    not fail. F6: Task 11 may edit task_controller.go.
  - Task 12: fill `TaskFacts.Change` on the resumed implement run too, not only
    while Blocked (task.go:119 comment is misleading); `ResumeFeedback` appends
    `f.Feedback`, so a second change nests the first resume text if stored in
    `status.feedback`; its "do not edit those files again" discourages a
    legitimate second change on the same path.
  - `nextTaskStep` is at gocyclo 30 exactly: any new branch goes in a helper.
  - The operator doesn't copy `Proposal`/`Empty` until Task 9: never deploy before.
- Model choice: sonnet for mechanical tasks and small reviews; opus for the
  controller integration (Tasks 9–12) and their reviews; never Fable. Hand
  subagents paths, ask for replies under 15 lines, report files in the workspace.
- Two stale agent worktrees under `.claude/worktrees/` broke `make manifests`
  (controller-gen read their files); removed (their commits were in HEAD,
  branches `worktree-agent-*` kept). Envtest binaries there are read-only:
  `chmod -R u+w` before removing any such tree.
- A leftover shell from the Task 8 implementer loops `pgrep -f "make check"`
  (it matches itself; pid 322844); auto mode refused my kill. Harmless.
- **Disk stalls.** Docker Desktop's data disk stalls under Go builds plus
  `make check` (I/O pressure avg300 ~20% at hand-off). Server Crew stays at
  `parallel: 1`; watch `cat /proc/pressure/io`.

## Decisions

- Made by the user: gated Task filed as a Gitea issue that blocks the original;
  eligible paths = protected minus `AGENTS.md`, `docs/agents/`, `.c4factory.yaml`;
  full autonomy; release plan approved ("go! release plan is good"); skip e2e;
  never Fable; hand off between tasks; keep a log of session management (the
  relay's `.relay/relay.log`, and this ledger).
- The user paused both Crews at ~20:30 to give the PC a rest, "before you go
  full auto mode". Mine: if this session was started by `hack/relay.sh`, the
  user chose full auto — unpause both Crews (server `parallel: 1` while I/O
  avg300 > 15%). In an interactive session, ask before unpausing.
- Open, mine: tag extension v0.1.0 (as c4admin) when #4 merges. Task 18 (close
  PR #30 unmerged, ready #4) and the tag may be refused by auto mode — if so,
  leave it open in the handoff for the user, citing their approval.

## Next steps

1. Load `superpowers:subagent-driven-development`; run its `scripts/sdd-workspace
   docs/plans/standard-change.md`; read the ledger tail.
2. Task 9: `scripts/task-brief docs/plans/standard-change.md 9`, record BASE
   (`git rev-parse HEAD`), dispatch the implementer (opus) with the Task 9 notes
   above, then a reviewer (opus).
3. Tasks 10–18, one implementer then one reviewer each, finishing each task
   (fix rounds included) before handing off.
4. Final whole-branch review (opus) with the ledger's deferred minors; no e2e.
5. Factory: server v0.3.0 (#46 #53 #57) after Standard changes deploy (Task 17);
   extension v0.2.0 (#9 #11 #13) after extension v0.1.0 ships. Set the server
   Crew back to `parallel: 2` once I/O pressure stays low.
6. Extension: close PR #29 if open, tag v0.1.0, check the release, install the
   `.vsix` here (`code --install-extension`) and see diagnostics.

## State outside git

- Kind cluster `c4factory`: Gitea, operator, runner-go, UI. Crews **paused**
  (cel-language-server `parallel: 1`, vscode-extension `parallel: 2`). Etcd
  no-fsync edits still in the node (originals in `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin; password
  contains `/`, use `curl -u`), `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`.
- No port-forwards of mine. The user runs `make ui-forward` (8080) and
  `make gitea-forward` (3000).
- Relay log (if the user started it): `.relay/relay.log`, `.relay/session-*.md`.

## References

- Spec docs/superpowers/specs/2026-10-02-standard-change-design.md; plan
  docs/plans/standard-change.md; results docs/results-v4.md
- PR https://github.com/JairJosafath/c4factory/pull/9; Gitea cel-lens/vscode-extension
  #4, PRs #29, #30; milestones in both cel-lens repos

## For the next session

Skills: `superpowers:subagent-driven-development`, `factory-ops`, `handoff`.
Docs on demand: docs/architecture.md ("Flow of one Task"), docs/dev/api-and-controllers.md.
Stay the controller only; hand off between tasks once past ~100k tokens.
