# Plan: a check that fails on main in the workspace doesn't fail the round

GitHub issue #869 (reported by gray-lenovo). Agent: otter-869.

## Goal

When the agent's workspace can't pass the check for a reason the change didn't
cause, the work goes on to CI and review instead of failing the round or
blocking the issue. CI, which runs the same check on the same commit, decides.
The user sees that the workspace and CI disagree, so they can fix the
difference, but no issue spends a try or holds a release on it.

Done when the two steps below have landed: `internal/agentrunner` tests show a
round whose check fails on its branch and on the base too ending with a commit,
no error and the base's failure reported, a round whose check passes on the
base still failing as today, and an agent whose blocked note comes with a
check that fails on the base sent back to do the issue; model table tests and
an envtest show such a round going on to the pull request and CI, with the
workspace's failure on the Task and the pull request. `make check` passes on
each.

## What happened

`cel-lens-vscode` downloads its engine's release in `make check`, from a
GitHub repository that is still private. Its CI workflow points the download
at the factory's Gitea; the agent's sandbox runs plain `make check` and gets a
404, on `main` too. On 2026-10-10 five issues stopped on it: #318 failed after
three rounds (round 1 "still fails after 2 attempts to fix it", rounds 2 and 3
changed nothing), and #266, #277, #353 and #299 ended `Blocked` or `Failed`,
their agents writing that the release needs publishing. Each needed a person's
retry prompt. Telling the Brief once didn't carry to later issues.

Today `implement` (`internal/agentrunner/run.go`) runs the check, sends the
agent back `checkFixAttempts` times, and sets `Result.Error` when it still
fails; `implementingStep` (`internal/model/task.go`) turns that into a failed
round without asking CI. An agent that sees the check fail before it changes
anything writes a `Blocked:` line, and the Task fails at once.

## Decisions, and why

1. **The base decides whether the workspace's check counts.** When the check
   fails on the branch, the runner runs it once on the base commit
   (`job.Base`, in a separate worktree of the same clone). If the base passes,
   the change broke the check: the round goes on as today. If the base fails
   too, the workspace can't judge the change, so the check's failure is not
   the round's error: the runner pushes and reports, and the operator opens or
   updates the pull request and waits for CI as on a passing check. This was
   the triage's suggestion, and it needs no configuration and no guess about
   why the check fails. A base check that can't run at all (the worktree
   fails) counts as passing: the round fails as today, never more leniently
   than before.
2. **Lazily, not on every round.** The base check runs only after the
   branch's check fails, so a healthy workspace pays nothing. It runs before
   the fix attempts, so they can be told about it: "the check fails on the
   base in this workspace too (output); fix the failures your change causes,
   and leave the rest to CI". It runs at most once per run.
3. **CI stays the judge.** Nothing else loosens: CI runs the full check on the
   head, a red CI is a failed round with its log as feedback, and the reviewer
   still reviews. A change that breaks something the workspace's failure hid
   is caught by CI, which is what the user asked for ("the same result as CI").
   When CI fails on `main` too, nothing here helps, and nothing should.
4. **A blocked note is checked against the base.** The agent may stop before
   the runner ever runs the check, as #353 and #299 did. When it writes a
   blocked note, the runner runs the check on the base; if that fails, it
   sends the agent back once: the check fails on the base in this workspace,
   which is not a reason to block; CI decides it; do the issue, and end with
   a `Blocked:` line again only if something else blocks it. A blocked note
   with a base that passes, or the second one, stands as today. Matching the
   note's words against the check would be guessing; a base check is a fact.
5. **The agents are told, too.** The implement prompt says that a check
   failing on the base for a reason outside the change (a download, a service
   the workspace can't reach) is the workspace's, not a blocker, and to say so
   in the summary. The review prompt says CI passed on this head, so a check
   that fails here and on the base too is not the change's fault. The prompt
   is the cheap half; the runner's base check is what holds when an agent
   ignores it.
6. **A Standard change is filed when the base fails too.** Today a proposal is
   kept only when the check passes with it. When the base fails the check as
   well, the proposal is kept: its own gated Task runs CI before the operator
   merges it.
7. **The difference is shown, not hidden.** The result carries the base's
   failure (`Result.CheckFailsOnBase`, its last lines, within the
   termination message's budget). The operator writes it on the Task's round
   (status message and a Normal Event `CheckFailsOnBase`) and as a comment on
   the pull request, naming the check command and that CI decides. It costs
   no round and no try, and is no condition of its own: the Task's outcome is
   CI's and the review's.
8. **No per-repository check environment.** A setting that copies the CI
   workflow's variables into the sandbox would fix this one repository and
   need the user to keep two places in step; the base check covers every
   cause (an unreachable host, a private download, a missing secret) without
   one.

## Steps

1. **The runner checks the base before failing a round** (#875, `internal/agentrunner`,
   `docs/design.md`). Decisions 1 to 6: the base check in a worktree, run once
   after a failing branch check or a blocked note; the fix-attempt and
   send-back prompts; no `Result.Error` when the base fails too, with
   `Result.CheckFailsOnBase` set; the proposal kept; the implement and review
   prompt lines; the design's Task paragraph. The operator already proceeds on
   a result with a commit and no error, so this step alone stops the lost
   tries. Done when agentrunner tests, with a check script that fails on a
   marker file, show: branch and base both failing gives a commit, no error,
   `CheckFailsOnBase` set and the fix prompts naming the base's failure;
   branch failing and base passing gives today's error; a blocked note with a
   failing base sends the agent back once and the round then goes on; a
   blocked note with a passing base stands; a proposal with both failing is
   kept; and `make check` passes.
2. **The operator shows a workspace that disagrees with CI** (#876,
   `internal/model`, `internal/controller`, `docs/design.md`). Decision 7:
   `RunResult.CheckFailsOnBase` decoded from the result, the round's status
   message, the Normal Event `CheckFailsOnBase`, and a comment on the pull
   request. Depends on step 1. Done when a model table test shows a round
   with `CheckFailsOnBase` and no error going on to the pull request with the
   message, an envtest shows the Event and the pull request comment on a fake
   Git host, and `make check` passes.

## What a human run of Kind would check

A Container whose check downloads from a host the sandbox can't reach while
its CI workflow reaches it: an issue's Task goes to CI and review in its first
round, the pull request carries the comment, and no try is spent.
