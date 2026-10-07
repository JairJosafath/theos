# Reviewer suggestions for a Brief's apps

GitHub issue #375, reported by gray-lenovo on v0.5.0; the maintainer decided
the direction on 7 October 2026. Agent: otter-375. One pull request.

## Goal

A Brief user sees the reviewers' suggestions for the Brief's apps where they
steer it (the Brief's status and card) and answers them in plain words in a
follow-up prompt. The factory does the Gitea part. Done when a prompt round
can take or skip suggestions, envtest shows it, and the card lists them.

## Decisions, and why

1. **What a suggestion is.** An open issue labelled `status/proposed` whose
   body ends with the line `model.FollowUpIssue` writes ("Suggested by ...
   while reviewing ... for #N (Task `...`)."). No new marker: the issues
   v0.5.0 already filed match too, so gray-lenovo's six show up after an
   upgrade. Only the repositories of Containers the Brief made (label
   `c4.factory.io/brief`) count: other Containers belong to someone else.
2. **Where the Brief reads them.** The workspace already lists every
   Container's open issues each reconcile; the suggestions come from the same
   listing, so no new Git calls. A Brief that made a Container requeues every
   ten minutes (`SuggestionsRecheck`), and a Task phase change on one of its
   Containers wakes it, since a merge is when a reviewer's follow-ups are
   filed. A repository that can't be read keeps the suggestions last seen.
3. **Status.** `status.suggestions` (container, number, title, url, criteria;
   at most 50) and `status.suggestionsWaiting`, a column of `kubectl get
   brief`. The phase stays `Planned`: nothing is wrong, and a new phase would
   mean a new state machine for what is a list. The card says it instead.
4. **How the person answers.** In a prompt, which already starts a round and
   is the one thing the UI writes. The planner reads `model/suggestions.yaml`
   and writes `plan/suggestions.yaml`, `take` or `skip` with a `why`, only for
   the suggestions a new prompt speaks to; the rest keep waiting. No yes/no
   field on the Brief: a prompt such as "yes to #5, no to the rest" says the
   same, and a second writer of the spec would be a second thing to keep.
5. **Checks.** With the plan, at acceptance (`model.CheckAnswers`): each
   answer names a waiting suggestion once, says take or skip and why, and
   takes only one with acceptance criteria, since a Crew refuses a ready issue
   without them. To take one without, the planner files it again with
   criteria and skips the original saying so. Refused answers go back to the
   planner like any refusal.
6. **Applying.** After the round's issues and before merging the plan, each
   answer is done against the Git host as it is then: a suggestion no longer
   waiting (a person answered it in Gitea, or an earlier try did) is left
   alone, so a restart is safe. Take: out of the Later milestone (new
   `Provider.SetIssueMilestone`; Brief issues have no milestone, and Later says
   "not part of any release"), comment, then `status/ready`. Skip: comment,
   then close. The comment quotes the planner's why and the round's prompts.
   The `Planned` Event counts what was taken and skipped.
7. **Stand-in planner.** It answers nothing: suggestions keep waiting on a
   stand-in Brief. The loop is tested in envtest with the plan written by the
   test, which is where the operator's part lives.

## Evidence

- `internal/model/suggestion_test.go`: detection, parsing, checks, comments
  and the workspace file.
- `internal/controller/brief_suggestions_test.go` (envtest): a Brief lists
  two reviewer suggestions and not a person's proposed issue, refuses a plan
  taking one without criteria, then takes one out of Later as ready and closes
  the other, both commented with the prompt quoted.
- `internal/gitprovider/gitea_issues_test.go`: `SetIssueMilestone`.
- `ui/src/model/brief.test.ts`: the card's view of the suggestions.

What only a human run on Kind would show: a real Gitea clearing the
milestone with `{"milestone": 0}` and a real planner answering a prompt well.
