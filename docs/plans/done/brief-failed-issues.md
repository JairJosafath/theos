# A Brief shows its failed issues and tries them again

GitHub issue #429, reported by gray-lenovo on v0.5.0. Agent: otter-429.
Related: #428 ([brief-issue-order.md](brief-issue-order.md)) orders a round's
issues so fewer fail for want of another app's work; this plan is about what
happens once one has failed anyway.

## Goal

When an issue of a Brief's app is handed back to a person
(`status/needs-human`), the Brief and its card say so, which issue and why, in
one sentence, and a follow-up prompt such as "try the CLI issue again now that
the engine's checker is merged" sends it back to work. Nobody relabels anything
in Gitea.

Done when, in envtest, a Brief whose Container has a failed Task lists the
issue with the Task's reason, a prompt round's plan retries it (with a
clarification) and closes another, and the Crew starts the retried issue's next
Task; and the card shows the failed issues and says to answer them in a prompt.

## Decisions, and why

1. **What a failed issue is.** An open issue labelled `status/needs-human` in
   the repository of a Container the Brief made (label `c4.factory.io/brief`).
   That label is the one way the factory hands an issue back: a failed Task
   (`handBack`) and a Crew refusing an issue (`NotReady`) both set it. No new
   marker, so the issues v0.5.0 already handed back show up after an upgrade.
   Other Containers' issues belong to someone else, as with suggestions.
2. **Where the Brief reads them, and the reason.** The workspace already lists
   every Container's open issues, with labels and body, each reconcile; failed
   issues come from the same listing, so no new Git calls. The reason comes
   from the cluster, not from Gitea comments: the CRDs are the truth, and
   parsing comments would break on any wording change. `model.FailedReason`
   decides it from the issue and the Container's Tasks, in this order:
   - no acceptance criteria: the Crew's reason (`MissingAcceptanceCriteria`),
     kind `unclear`;
   - a Task for the issue merged: kind `merged` ("already merged; close it or
     file the rest as a new issue");
   - `MaxIssueAttempts` Tasks failed: kind `exhausted`, naming them;
   - otherwise the newest failed Task's message, kind `failed`, as
     "Task `cel-lens-cli-1` failed: Failed after 3 rounds: the agent made no
     changes"; kind `blocked` instead when that Task failed with reason
     `Blocked`, its agent having said it can't start the issue yet (#492), so
     the planner retries it once its blocker lands;
   - none of these (a person labelled it): kind `person`, "handed back by a
     person; see the issue".
   Each entry also carries `tries`, the Tasks the issue has had.
3. **Status.** `status.failed` (container, number, title, url, kind, reason,
   tries; at most 50, by Container and number) and `status.failedWaiting`, a
   printer column `Failed` on `kubectl get brief`. A condition `IssuesFailed`,
   True while any is waiting, whose message is the one sentence the reporter
   asked for: the first failed issue and its reason, then "and N more", then
   "say in a prompt whether to try again". The phase stays `Planned`: the
   Brief's own work is done, and a new phase would be a new state machine for
   a list (the same call as suggestions). A Warning Event `IssueFailed` per
   issue when it first appears, so `kubectl get events` shows it too. A
   repository that can't be read keeps the failed issues last seen.
4. **When the Brief notices.** A Task phase change on one of its Containers
   already wakes the Brief (suggestions, `SuggestionsRecheck`), and a Task
   failing is a phase change; the ten-minute recheck catches a Crew's refusal.
5. **How the person answers: in a prompt, like suggestions.** A prompt is the
   one thing the UI writes and already starts a round. The workspace gets
   `model/failed.yaml` (each failed issue with its kind, reason, tries and
   body); the planner answers only the ones a new prompt speaks to, in
   `plan/failed.yaml`:

   ```yaml
   - container: cel-lens-cli
     issue: 1
     answer: retry          # or close
     why: The prompt says the engine's checker is merged now.
     clarify: |             # retry only, optional
       The engine's checker is merged (cel-lens-core#2); import it from ...
   ```

   No retry button or field on the Brief: a second writer of the spec would be
   a second thing to keep, and "try it again now that X is merged" carries what
   the retry needs to know, which a button can't.
6. **Retry and close, and nothing else.** `retry` sends the same issue back,
   with an optional clarification appended to its body; `close` closes it with
   the why. Splitting is `close` plus the round's new issues, which the plan
   already files (with `after:` if they wait on something, #428); the close
   comment names them. Editing the issue in place keeps its history and its
   Task names (`cel-lens-cli-1-2`), so the person can follow it.
7. **Checks** (`model.CheckFailedAnswers`, with the plan, at acceptance): each
   answer names a waiting failed issue once, says retry or close and why. A
   retry is refused when the Crew would refuse the issue again: kind `merged`;
   kind `exhausted` (the Crew's three tries are the guard against an issue
   that keeps failing, and it stays; the planner closes it and files it again,
   reworded or split, which is the "clarify or split" the guard asks for);
   kind `unclear` unless `clarify` adds an "Acceptance criteria" checklist.
   Refused answers go back to the planner like any refusal.
8. **Applying.** After the round's issues are filed (so the close comment can
   link the new ones) and before merging the plan, as suggestions do, each
   answer is done against the Git host as it is then: an issue no longer
   labelled `status/needs-human` is left alone, so a restart is safe. Retry:
   append the clarification under "## Clarification (Brief round N)" (new
   `Provider.EditIssueBody`; the agent's goal is the title and body,
   `IssueGoal`, so a comment wouldn't reach it), comment quoting the why and
   the round's prompts, label it `status/ready`, then remove
   `status/needs-human`, in that order, so a failure between the two never
   leaves the issue with neither label. Close: comment, then close. The Crew starts the next Task on
   its next poll; the attempt count is the Crew's, unchanged. The `Planned`
   Event counts what was retried and closed.
9. **The card.** The Brief's card lists the failed issues above the
   suggestions, each with its link and reason, and says "Say in a prompt
   whether to try again"; an `exhausted` one says it will be filed again.
   The issue list marks a failed issue, so it no longer looks the same as
   before.
10. **Stand-in planner.** It answers nothing, as with suggestions: the loop is
    tested in envtest with the plan written by the test, where the operator's
    part lives.

## Steps

Each lands on its own and leaves main working.

1. **Model** (#453) (`internal/model`): `FailedIssue`, `FailedReason`, the
   `model/failed.yaml` workspace file, `plan/failed.yaml` parsing and
   `CheckFailedAnswers`, with table tests; nothing calls it yet.
2. **The Brief lists its failed issues** (#454): API fields `status.failed`,
   `status.failedWaiting`, the `Failed` column, the `IssuesFailed` condition,
   the `IssueFailed` Event and the workspace file; the hand-back comment on
   a Brief's Container adds "or say in a prompt on Brief `<name>`"; envtest
   with a failed Task.
3. **A prompt retries or closes them** (#455): `Provider.EditIssueBody` (Gitea and
   the fake, `make test-gitea`), checking the answers with the plan, applying
   them, and the planner guide (`internal/model/planner.md`); envtest takes a
   failed issue back to ready with its clarification and closes another, and
   the Crew starts `<container>-<n>-2`.
4. **The card** (#456) (`ui/`): failed issues with their reasons, and the issue
   list marking them; `ui/src/model/brief.test.ts`.

## Evidence

Test output in
[../../screenshots/brief-failed-issues.txt](../../screenshots/brief-failed-issues.txt):

- `internal/model/failed_test.go` (#453, #455): what a failed issue is, its
  reason, the workspace file, parsing and checking the plan's answers, the
  clarified body and the comments.
- `internal/gitprovider/gitea_issues_test.go`: `EditIssueBody` (#455).
- `internal/controller/brief_failed_test.go` (envtest, #454, #455): a Brief
  lists an issue whose Task failed with the Task's message and not a person's
  in another app; a prompt retries one with a clarification and closes
  another.
- `ui/src/model/brief.test.ts` (#456): the card's failed issues, their
  reasons and the issue list marking them.

What only a human run on Kind would show: a real planner reading a prompt as
a retry, and a real agent doing better with the clarification.
