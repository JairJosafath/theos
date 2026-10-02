# Handoff: Standard changes, then the extension's release

## Goal

The user wants the factory to run without them: an issue that needs a protected
file (Makefile, CI) gets it through a gated Task the factory files, reviews and
merges itself; then CEL Lens's extension v0.1.0 ships. Work autonomously, don't
check in unless the user shows concern (memory `work-autonomously`). Never use
the Fable model, for anything (the user's one hard rule).

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9, pushed through `8c104be`.

- Plan docs/plans/standard-change.md, 18 tasks, run as subagent-driven development.
  - Done and reviewed: Tasks 1–10 (see the ledger), and **Task 11** (relay
    session 2): `6446a5c` (the interrupted WIP), `444e6bc` (gated merge:
    `gatedMerge`, Branch A `ForceMergePullRequest`, CI pending waits via
    `errChecksPending` → reason `WaitingForChecks`), `8c104be` (fix round 1: the
    wait ends when the review run ends — model `ActionRefuseMerge` from
    `noVerdictStep`, `status.checks` holds the pending state — and
    `gitprovider.ErrTooManyFiles` is refused at once). Review clean after 1 round.
  - `wip/standard-change-task-11` on GitHub deleted (Task 11 landed).
  - Not started: Tasks 12–18. Next is Task 12.
- CEL Lens: server v0.2.0 (#27 #29 #31 #32 #44 #45 #49 #50) `status/ready`.
  Extension: its whole v0.1.0 batch merged except #4 (needs the Makefile; waits
  for Standard changes, Task 17/18).

## Live thread

- Ledger: `.superpowers/sdd/standard-change/progress.md` (git-ignored, local):
  pre-flight scan, rulings F1–F10 and every task's lines, deferred minors. If
  lost, the rulings beyond F1–F10 that still matter:
  - Task 5: the Task branch keeps the protected edit in an intermediate commit;
    fine because the operator squash-merges (gitea_agents.go:312).
  - Task 5: `changedPaths` uses `--no-renames`.
  - Task 11: Branch A only; CI pending waits until the review run ends, then
    refuses `GatedMergeRefused`; no reviewer run is spent on a CI wait.
  - e2e: dropped (memory `skip-e2e`); `make check` + the live Kind check remain.
- Carry into dispatches (deferred findings):
  - Task 9: neutralise `<!-- c4factory:standard-change … -->` in the agent's
    Reason before the operator relies on the marker to find its issue
    (standard.go:176). F5: the reuse search skips `status.standardChange.issue`.
    F8: convert `StandardChangeLabel()` to `gitprovider.Label` in task_standard.go.
  - Task 12: fill `TaskFacts.Change` on the resumed implement run too, not only
    while Blocked (task.go:119 comment is misleading); `ResumeFeedback` appends
    `f.Feedback`, so a second change nests the first resume text if stored in
    `status.feedback`; its "do not edit those files again" discourages a
    legitimate second change on the same path. Also from Task 9: propose must
    fail the Task (IssueClosed / no issue) when a person closed the waiting
    issue during the round, and a goal Task reaching Propose must not
    `GetIssue(0)` and requeue forever.
  - Task 16 docs: CI pending at a gated merge waits until the review run ends,
    then refuses; a PR too large to list (1000+ files) is refused.
  - `nextTaskStep` is at gocyclo 30 exactly: any new branch goes in a helper.
- Model choice: opus for the controller integration (Task 12) and its review;
  sonnet for mechanical tasks and small reviews; never Fable. Hand subagents
  paths, ask for replies under 15 lines, reports in the workspace.
- **Headless fix rounds:** SendMessage to a finished implementer resumes it in
  the background, which a headless session can't wait on. Dispatch a fresh
  implementer in the foreground with brief + report file + findings instead
  (worked for Task 11).
- Review packages: `scripts/review-package` has no path filter; when a task's
  range holds controller docs commits, build the package by hand limited to
  `internal/` (as `review-01f8f8a..444e6bc-internal.diff`).
- **Disk stalls.** Docker Desktop's data disk stalls under Go builds plus
  `make check` (I/O pressure avg300 ~47% right after the node restart). Server
  Crew stays at `parallel: 1`; watch `cat /proc/pressure/io`.

## Decisions

- Made by the user: gated Task filed as a Gitea issue that blocks the original;
  eligible paths = protected minus `AGENTS.md`, `docs/agents/`, `.c4factory.yaml`;
  full autonomy; release plan approved ("go! release plan is good"); skip e2e;
  never Fable; hand off between tasks; keep a log of session management (the
  relay's `.relay/relay.log`, and the ledger).
- **The user unpaused both Crews at 21:53 UTC** (told to the interactive
  session `c4factory-a7`: "allow the factory to work again … go ahead with the
  work to improve the factory"). Don't re-pause; don't change `parallel`
  (server 1, extension 2) unless I/O pressure stays low.
- **Peer session `c4factory-a7`** watches the running factory (Crews, Tasks,
  Gitea, I/O) and writes `Factory (a7): …` ledger lines; it doesn't touch the
  working tree or the task lines. Tell it (SendMessage) before deploying the
  operator (Task 17) so it doesn't misread the restart.
- Open, mine: tag extension v0.1.0 (as c4admin) when #4 merges. Task 18 (close
  PR #30 unmerged, ready #4) and the tag may be refused by auto mode — if so,
  leave it open in the handoff for the user, citing their approval.

## Next steps

1. Load `superpowers:subagent-driven-development`; run its `scripts/sdd-workspace
   docs/plans/standard-change.md`; read the ledger tail.
2. Task 12: `scripts/task-brief docs/plans/standard-change.md 12`, BASE = HEAD,
   implementer (opus) with the Task 12 notes above, then a reviewer (opus).
3. Tasks 13–18, one implementer then one reviewer each, finishing each task
   (fix rounds included) before handing off.
4. Final whole-branch review (opus) with the ledger's deferred minors; no e2e.
5. Factory: server v0.3.0 (#46 #53 #57) after Standard changes deploy (Task 17);
   extension v0.2.0 (#9 #11 #13) after extension v0.1.0 ships.
6. Extension: close PR #29 if open, tag v0.1.0, check the release, install the
   `.vsix` here (`code --install-extension`) and see diagnostics.
7. File the factory gap as a GitHub issue (if a7 hasn't): an agent killed by
   the infrastructure (restart, eviction) should rerun the same round, not
   spend one (server #27 lost round 1 to "claude failed: signal: killed").

## State outside git

- Kind cluster `c4factory`: Gitea, operator, runner-go, UI. Crews **running**
  (user's call, see Decisions). Etcd no-fsync edits still in the node
  (originals in `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin; password
  contains `/`, use `curl -u`), `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`.
- No port-forwards of mine. The user runs `make ui-forward` (8080) and
  `make gitea-forward` (3000).
- Relay: restarted 21:50 (`.relay/relay.log`); this handoff ends its session 1.
- A leftover shell from the Task 8 implementer may still loop `pgrep -f "make
  check"` (pid 322844 before the restart); harmless.
- After a PC restart the devcontainer's dockerd can lose a race with containerd
  ("failed to get containerd plugins"); re-run `/usr/local/share/docker-init.sh`.

## References

- Spec docs/superpowers/specs/2026-10-02-standard-change-design.md; plan
  docs/plans/standard-change.md; results docs/results-v4.md
- PR https://github.com/JairJosafath/c4factory/pull/9; Gitea cel-lens/vscode-extension
  #4, PRs #29, #30; milestones in both cel-lens repos

## For the next session

Skills: `superpowers:subagent-driven-development`, `factory-ops`, `handoff`.
Docs on demand: docs/architecture.md ("Flow of one Task"), docs/dev/api-and-controllers.md.
Stay the controller only; hand off between tasks once past ~100k tokens.
