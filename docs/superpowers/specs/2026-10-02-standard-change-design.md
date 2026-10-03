# Standard changes: a protected file without a person

## Goal

An issue sometimes needs a file the Standard or the platform protects. Two
CEL Lens issues did: cel-language-server #24 (a `make bench` target) and
vscode-extension #4 (`make dist` builds one `.vsix` per platform). Today the
implementer may not touch the file, says it is blocked, and the Task fails when
its last round makes no changes. A person then has to edit the file, and Gitea
refuses to merge any pull request that touches it, whoever asks.

The factory should carry such a change on its own: no person acts, and
protection still means that an agent cannot weaken a check to pass its own
work.

## Decisions

Made by the user (2026-10-02):

- The change runs as its own gated Task, not inside the Task that needs it.
- The operator files it as a Gitea issue, so it shows in the backlog, the
  milestone and the UI.
- Eligible: every protected path except the agents' rules (`AGENTS.md`,
  `docs/agents/`) and the factory's contract (`.c4factory.yaml`). The Makefile,
  CI and release workflows (`.gitea/`), lint config, `.editorconfig`,
  `docs/style/` and `.devcontainer/` are eligible.

Left to me by the user, and taken here:

- A waiting Task gets its own phase, `Blocked`, which spends no round and does
  not count against the Crew's `parallel`.
- What a gated Task may change is recorded in Kubernetes, which agents cannot
  write, not in the issue, which they can comment on.
- The operator merges a gated pull request with Gitea's admin force-merge, after
  checking CI, the approval and the changed paths itself.

## Flow

Vscode-extension #4 as the example.

1. **Implementer.** It needs `make dist` to run its new packaging script, so it
   edits the Makefile as part of its work. Its guide says how: the protected
   change must work on `main` alone, so it is a hook
   (`npm run dist --if-present`), not a call to a file only its branch has.
2. **Runner.** After the agent commits, it splits the change since the base:
   - edits to eligible protected paths go to a proposal branch,
     `standard/<task>`, cut from the base, and are reverted on the Task branch;
   - the rest is pushed to the Task branch as today;
   - edits to an ineligible path fail the round as today.

   The result carries the proposal: its branch, its paths and the agent's
   reason. The patch itself stays in Git, because a result is at most 4000
   bytes.
3. **Operator, on the Task.** It files the issue "Standard change for #4:
   Makefile", authored by itself, labelled `kind/standard-change` (created if
   missing) and `status/ready`, in #4's milestone, with the reason, the paths
   and the proposal branch. In Gitea the new issue blocks #4. The Task records
   `status.standardChange` (issue number, paths) and moves to `Blocked`.
4. **Crew.** It runs the new issue like any ready issue, within `parallel`.
   `Blocked` Tasks do not count, so the Crew cannot fill its slots with Tasks
   that wait on issues it has no slot for. For an issue labelled
   `kind/standard-change`, the Crew sets the new Task's
   `spec.allowedProtectedPaths` from the `status.standardChange` of the Task
   that filed it, and leaves out any ineligible path. A label alone grants
   nothing.
5. **Gated Task.** The implementer starts by merging the proposal branch and
   answers review like any implementer. Its runner allows exactly the allowed
   paths and nothing else: a change to any other path fails the round.
6. **Reviewer.** Its prompt now lists the protected paths for every Task. For a
   gated Task it also checks: the change weakens no check (no target, test,
   lint rule or CI job removed or skipped), `main` works with this change alone,
   and the change does no more than its issue asks.
7. **Merge.** Gitea's protected-file rule blocks the normal merge. The operator
   checks that CI passed, the reviewer approved, and every changed path is
   allowed, and then merges with `force_merge` as Gitea admin. The merge closes
   the gated issue.
8. **Resume.** The `Blocked` Task sees its standard-change issue closed by a
   merge and goes back to `Implementing` in the same round. The runner's usual
   start merges `main` into the branch, and the agent's feedback says the
   change has merged and what is left. On a resume, making no changes is not a
   failure: the Task goes on to `Checking`, or ends as the table below says
   when its branch equals `main`.

## Limits and failures

| Case | Outcome |
| --- | --- |
| The gated Task fails, or its issue is closed unmerged | The waiting Task fails with that reason and is handed back (`status/needs-human`), as today |
| A Task proposes a second change while it waits | Not possible: a `Blocked` Task runs no agent |
| A Task asks for a third Standard change | The Task fails: "more than two Standard changes"; a person looks at the issue |
| The proposal touches only ineligible paths | The round fails as today, and the error names the paths |
| The Task has no changes besides the proposal | It still waits. If after the resume its branch equals `main`, the Standard change did the whole issue: the Task ends `Merged` with reason `DoneByStandardChange`, and the operator closes the issue with a comment naming the change |
| Gitea refuses `force_merge` (an older version, or `block_admin_merge_override`) | Plan step 1 finds out. The fallback is removing the allowed paths from the branch rule's protected file patterns for that one merge and restoring them afterwards, the operator being the only merger |

## Changes, by package

- `api/v1alpha1`: Task phase `Blocked`; `spec.allowedProtectedPaths`;
  `status.standardChange` (issue, paths, count). Made with markers and `make
  manifests generate`.
- `internal/model`: the split of a change into proposal, Task change and
  refused paths; eligibility (`AgentNeverPaths`); the Task step for a proposal,
  `Blocked`, the resume and the failures; `Blocked` left out of the Crew's
  count.
- `internal/agentrunner`: the split, the proposal branch, the result's
  proposal, the allowance check, "no changes" on a resume, and both prompts.
- `internal/controller`: filing the issue and its dependency, `Blocked` and
  the resume in the Task controller, the allowance in the Crew controller, and
  the gated merge.
- `internal/gitprovider`: create the label, add an issue dependency, and merge
  with `force_merge`.
- Templates: each Standard's implementer and reviewer guides describe Standard
  changes. As templates are pinned, this takes a new version of each, and only
  new Containers get it. The prompts carry the rules for existing ones.
- Docs: `docs/design.md`, `docs/architecture.md` ("Flow of one Task"), and the
  spec of the issue-driven factory (issue states).
- UI: shows `Blocked` like any other phase, with the issue it waits on.

## Testing

- Table tests in `internal/model`: the split (eligible, ineligible, allowed,
  mixed), the Task steps (proposal to `Blocked`, merged to `Implementing` in
  the same round, failed to `Failed`, third change), the Crew count without
  `Blocked`, and the allowance taken only from a Task's status.
- Runner tests on a real Git repository: the proposal branch holds the
  protected edits alone, and the Task branch holds the rest.
- Envtest: a Task blocks, its standard-change Task is created with the
  allowance, and the Task resumes when the fake provider reports the merge.
- Live in Kind: a `force_merge` of a pull request that touches the Makefile
  (plan step 1).

## Done when

PR #30 in vscode-extension is closed unmerged and #4 is readied again. With no
person acting, the factory files a Standard change for the Makefile, merges it,
resumes #4, and merges #4. Its packages appear in `dist/` from `make dist`.
Evidence goes into `docs/results-v4.md` and `docs/screenshots/`.

## Out of scope

- Blocks of any other kind (a missing secret, an unclear issue): they still
  fail the Task and hand the issue back.
- Updating existing Containers' guides: they keep their pinned template. (Since
  #65 a bump of the Standard's `ref` updates them; see `docs/design.md`.)
