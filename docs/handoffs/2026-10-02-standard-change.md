# Handoff: Standard changes, then the extension's release

## Goal

The user wants the factory to run without them: an issue that needs a protected
file (Makefile, CI) gets it through a gated Task the factory files, reviews and
merges itself; then CEL Lens's extension v0.1.0 ships. Work autonomously, don't
check in unless the user shows concern (memory `work-autonomously`).

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9, pushed to `a73381f` (a subagent
pushed although told not to; harmless).

- Plan docs/plans/standard-change.md, 18 tasks, run as subagent-driven development.
  - Done and reviewed: Task 1 `91c7bd2` (spike: outcome A, force_merge), Task 2
    `05a722b`, Task 3 `fc4df4e`, Task 4 `ea757da`, Task 5 `6753696`+`8ef1a03`
    (one fix round), Task 6 `a73381f`.
  - **In flight: Task 7** (API types, CEL, CRD), implementer dispatched at base
    `a73381f`. The tree had uncommitted `api/`, `config/crd/` and
    `internal/controller/task_standard_test.go` changes when this was written.
  - Not started: Tasks 8–18.
- `b3d9c32` ci: the e2e workflow runs only on demand (user: "e2e tests are overkill,
  and keep failing in gh runners, skip those for now"). Memory `skip-e2e`.
- CEL Lens release plan (user approved, applied in Gitea): milestones server v0.1.1,
  v0.2.0, v0.3.0 and extension v0.2.0; 28 issues got "Acceptance criteria";
  server #55 closed. Merged since: extension #16 #18 #19 #24 #25 #26 (its whole
  v0.1.0 batch except #4), server #17. Server #16 is in round 3.

## Live thread

- Ledger: `.superpowers/sdd/standard-change/progress.md` (git-ignored, local). It
  holds the pre-flight scan, rulings F1–F10 and every task's lines. If it is lost,
  rulings beyond F1–F10 (see the old list in git history of this file):
  - Task 5: the Task branch keeps the protected edit in an intermediate commit;
    fine because the operator squash-merges (gitea_agents.go:312).
  - Task 5: `changedPaths` now uses `--no-renames` (renaming a protected file used
    to hide it from the check; predates this plan).
  - e2e: the plan's "make e2e before #9 leaves draft" step is dropped.
- Deferred findings to carry into later dispatches (the rest are in the ledger for
  the final review):
  - Task 9: neutralise `<!-- c4factory:standard-change … -->` in the agent's Reason
    before the operator relies on the marker to find its issue (standard.go:176).
  - Task 12: fill `TaskFacts.Change` on the resumed implement run too, not only
    while Blocked (task.go:119 comment is misleading); `ResumeFeedback` appends
    `f.Feedback`, so a second change nests the first resume text if stored in
    `status.feedback`; its "do not edit those files again" discourages a
    legitimate second change on the same path.
  - Task 11: what to do when CI is `pending` in `GatedMergeRefusal` (wait, not fail).
  - `nextTaskStep` is at gocyclo 30 exactly: any new branch goes in a helper.
  - The operator doesn't copy `Proposal`/`Empty` until Task 9: never deploy before.
- **Disk stalls (found this session).** Gitea and the operator restarted
  (18:41 leader election lost, 18:54 Gitea liveness) because Docker Desktop's
  data disk `/dev/sde` answered ~140 reads/s at ~260 ms each (109% busy at
  2.5 MB/s), I/O pressure 60–75% "full". The reads were page-cache refaults from
  Go agents building the server (k8s deps) plus `make check` in the devcontainer.
  Likely the Windows host disk (Defender scanning the vhdx, or host drive full);
  told the user what to check. Mitigation: server Crew `parallel: 1`. Watch
  `cat /proc/pressure/io` (avg300 was 20% at hand-off).
- Reviewers' follow-ups land as `status/proposed` in `Later` (first live check of
  `61c8cb2`: 3 server, 10 extension today).

## Decisions

- Made by the user: gated Task filed as a Gitea issue that blocks the original;
  eligible paths = protected minus `AGENTS.md`, `docs/agents/`, `.c4factory.yaml`;
  full autonomy; release plan approved ("go! release plan is good"); skip e2e.
- Made by me: see ledger rulings; server Crew at 1 until the disk recovers.
- Open, mine: tag extension v0.1.0 (as c4admin) when #4 merges. The auto-mode
  safety check refused Gitea writes until the user approved the release plan;
  Task 18 (close PR #30 unmerged, ready #4) and the tag may be refused again —
  if so, ask the user once, citing their approval.

## Next steps

1. Load `superpowers:subagent-driven-development`; run its `scripts/sdd-workspace
   docs/plans/standard-change.md`; read the ledger tail.
2. Task 7: if `git log a73381f..` shows `feat(api): Task phase Blocked…` and
   `task-7-report.md` exists, build `scripts/review-package … a73381f HEAD` and
   dispatch the reviewer (sonnet). If the tree is dirty with no commit, the
   implementer died: `git stash` its work, re-dispatch Task 7 fresh.
3. Tasks 8–18, one implementer then one reviewer each; sonnet for mechanical work,
   opus for controller integration (Tasks 9–12) and their reviews; never Fable.
   Hand over paths, ask for replies under 15 lines.
4. Final whole-branch review (opus) with the ledger's deferred minors; no e2e.
5. Factory: when server #16 is done, ready server v0.2.0 (#27 #29 #31 #32 #44 #45
   #49 #50: swap `status/proposed` → `status/ready`; they already have criteria).
   Set the server Crew back to `parallel: 2` once I/O pressure stays low. Server
   v0.3.0 (#46 #53 #57) after Standard changes deploy (Task 17); extension v0.2.0
   (#9 #11 #13) after extension v0.1.0 ships.
6. Extension: close PR #29 if open, tag v0.1.0, check the release, install the
   `.vsix` here (`code --install-extension`) and see diagnostics.

## State outside git

- Kind cluster `c4factory`: Gitea, operator, runner-go, UI. Crews unpaused:
  cel-language-server `parallel: 1`, vscode-extension `parallel: 2`. Etcd no-fsync
  edits still in the node (originals in `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin; password
  contains `/`, use `curl -u`), `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`.
- No port-forwards of mine. The user runs `make ui-forward` (8080) and
  `make gitea-forward` (3000).

## References

- Spec docs/superpowers/specs/2026-10-02-standard-change-design.md; plan
  docs/plans/standard-change.md; results docs/results-v4.md
- PR https://github.com/JairJosafath/c4factory/pull/9; Gitea cel-lens/vscode-extension
  #4, PRs #29, #30; milestones in both cel-lens repos

## For the next session

Skills: `superpowers:subagent-driven-development`, `factory-ops`, `handoff`.
Docs on demand: docs/architecture.md ("Flow of one Task"), docs/dev/api-and-controllers.md.
Stay the controller only; hand off again near 140k tokens.
