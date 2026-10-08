# A Standard change carries the changes its checks need

Issue #578. Decided by the maintainer on 2026-10-07; built by otter-578.

## Goal

cel-lens-core's Standard change #53 renamed what `make dist` builds. A test on
main (`release_test.go`) pinned the old names, so the Makefile change alone
failed CI. The gated Task could change only the Makefile and the release
workflow, so each round that also fixed the test was turned back, and the
issue that needed the change (#51) waited on #53 for ever. The Brief showed
only rounds being spent.

## Decisions

1. **A Standard change may carry the unprotected changes its checks need.** A
   gated Task's scope is its allowed protected paths plus any path no Standard
   or the platform protects. Other protected paths, the agents' rules and
   `.c4factory.yaml` included, stay refused. The extra paths are named: in the
   round's history entry ("besides its Standard change's files it changes
   ..."), and in the pull request's description, for the reviewer. The gated
   merge refuses only protected paths outside the allowance.
2. **The reviewer still guards the checks.** The gated review prompt and the
   Standards' reviewer guides require that every extra file changes only what
   the check command fails on without it, and that no check is weakened. The
   implementer prompt and guides say the same, and add no new tests.
3. **Twice for scope stops the Task.** A round whose change touches paths the
   Task may not change fails with an error that starts `Out of scope: `. When
   the round before it failed the same way (its feedback starts so too), the
   Task fails at once, reason `Blocked`: more rounds won't change it. A Task
   blocked on a Standard change whose Task ended `Blocked` ends `Blocked` too,
   with the change's issue and its message. Both issues are handed back, and
   the Brief lists both as kind `blocked`, with the files in the reason.

   Two in a row, not two in all: a round in between that failed for another
   reason shows the agent moved on. It is detected from the round's feedback,
   which already holds the previous round's error, so no status field is added.

## Steps

One pull request:

- `internal/model`: `SplitChange` lets a gated Task change unprotected paths;
  `StandardChangeExtras`, `ScopeRefusal`, `IsScopeRefusal`; `RunResult.Extra`;
  `GatedMergeRefusal` takes the protected paths; the implementing step stops a
  Task refused twice; `StandardChangeOutcome` says whether the change's Task
  ended blocked, and the blocked step passes it on.
- `internal/agentrunner`: the result carries `extra`; the refusal error comes
  from `model.ScopeRefusal`; the gated implement and review prompts.
- `internal/controller`: the gated merge reads the Container's protected
  paths; the pull request names the extras; a change Task's blocked reason
  reaches the waiting Task.
- `templates/*/docs/agents/`: the implementer and reviewer guides.

## Done when

- `make check` passes, with tests for: the split (model), the refusal and the
  extra in a real runner repository (agentrunner), the twice-for-scope stop and
  the blocked parent (model and envtest), the gated merge of an unprotected
  extra and the refusal of a protected one (envtest), and the pull request body.
- What only a human run of Kind would show: a real agent, given a Standard
  change whose test pins the old output, updating both and the gated merge
  landing them.
