# A Crew says which changes wait for a person

GitHub issue #62, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-10 in a session the owner ran.

## Goal

A team turns the factory on for a repository that matters without handing it
everything: the Crew merges what the team is sure of, and a change to
`migrations/`, one over 400 lines or one labelled `risk/high` waits for a
person's approval. Warp's factory asks the same up front ("where humans enter
the loop"), and its advice is ours: start with low-risk work and widen.

## Where we stand (main at 39fe7fa)

- A Crew merges a pull request once CI, the reviewer and, when named, the
  verifier pass. A person decides at a spec (`specApproval: Person`) and at a
  deploy, and nowhere between.
- The wait exists, for specs: a Spec Task whose reviewer approved waits in
  phase `AwaitingApproval` with a `NeedsYou` condition, and
  `awaitingApprovalStep` (`internal/model/task.go`) merges it on a person's
  approval of the reviewed commit, from the Git host or from chat, sends it
  back on their request for changes, and checks it again when its branch
  moves. `task_spec.go` reads those facts.
- The Task reads its Crew live for `specApproval`, the cost cap and the pause
  (`crewFacts`).

## Decisions, and why

1. **`Crew.spec.approval` is an ordered list of rules,** each with what it
   matches and what it asks:

   ```yaml
   approval:
     - paths: ["migrations/", "internal/auth/"]
       require: Person
     - labels: ["risk/high"]
       require: Person
     - changedLinesOver: 400
       require: Person
     - labels: ["kind/docs"]
       require: None
   ```

   `require` is `Person` (a person approves the pull request), `Reviewer`
   (the reviewer's approval merges, as without rules) or `None` (CI alone:
   no reviewer and no verifier runs).
2. **Each changed file takes the first rule that matches it, and the change
   needs the strictest of what its files need** (`Person` over `Reviewer`
   over `None`). A rule's `labels` and `changedLinesOver` say which changes
   it is for at all: the issue has one of the labels, the pull request adds
   and deletes more lines than the number. A rule that is for the change
   matches a file under one of its `paths`, or every file when it has none.
   A file no rule matches needs `Reviewer`. The issue proposed "the first
   rule that matches the pull request decides"; that lets a change to
   `docs/` and `internal/auth/` through on a `docs/` rule that asks `None`,
   and nobody writing that rule means it. Per file, a rule can only loosen
   the files it names.
3. **Paths are a Standard's `protectedPaths` plus `*`:** a file, a folder
   ending in `/` for everything under it, or a pattern in which `*` stands
   for any run of characters, slashes included (`*.md`, `docs/*`). One
   syntax for paths in the factory, and the two matchers are in the model
   already.
4. **The rules are read from the Crew when they are needed, not copied onto
   the Task,** as `specApproval` is: tightening a rule holds the Tasks under
   way too, and loosening one lets a waiting Task go. The answer is kept in
   `Task.status.approval` with the commit it is of and a key of the rules
   it came from, and worked out again when either changes.
5. **The rules are for a Crew's Implement Tasks.** A spec has its own review
   point; a Standard change's gated merge needs a review run that still
   lasts, so it can't wait for a person and is never unreviewed; a
   Benchmark's Task never merges.
6. **A person is who a spec's approval takes:** anyone but the operator's
   user and the agents' bots, by an approving review of the reviewed commit
   on the Git host, or the Task's `theos.factory.io/approve-spec`
   annotation naming that commit (the annotation keeps its name; it is the
   approval from chat of a waiting pull request, a spec's or a change's). A
   request for changes starts the next round with their words. Closing the
   pull request ends the Task as it does today.
7. **The wait comes last:** after the reviewer and, with one, the verifier.
   A person reads a change the factory already vouches for, with both
   verdicts on the pull request. The Task waits in `AwaitingApproval` with
   `NeedsYou`, and takes no place from `parallel`. When main moved
   meanwhile, the approved change takes it in and merges on green checks
   without a second approval, as a verified change does.
8. **A pull request with more files than the Git host lists is never
   `None`:** it needs `Person` when any rule asks for one, else `Reviewer`.

## Steps

| Step | Done when |
| --- | --- |
| 1. The rules, the wait and the merge (this plan's pull request) | `Crew.spec.approval` with its CEL; `model.ApprovalFor` table-tested; envtest: a change under a `Person` path waits and merges on a person's approval, not an agent's; one that needs `None` merges with no reviewer run; no rules is today's behaviour; the Crew's page shows the rules and *Needs you* the waiting change; the guide's "Start small" |
| 2. The rest (#92) | a thread in the Chat's `reviews` room with `approve` and `changes:`; the reviewer raises with a `Risk: high` verdict line (the runner, an owner's path); the triager's guide to `risk/*` labels; merged Tasks split by who approved in *How well it works* and in `theos_tasks_merged` |

## What is done of step 2

- Who approved, in the numbers (2026-11-10): `theos_tasks_merged` has an
  `approved` label, `person`, `reviewer` or `none`, read from
  `status.approval` and `status.spec`; the dashboard has a panel for it,
  [docs/monitoring.md](../monitoring.md) the query, and the UI's *How well
  it works* a column, "A person approved".
- The chat thread (2026-11-10): a waiting change is a thread in the Chat's
  `reviews` room (`ChatEventChangeWaiting`), opened when the change waits
  and followed until its Task ends. `approve` and `changes: <text>` there
  write the annotations a spec's thread writes, as the person. `reviews`
  also has the deploys' threads, whose `approve` names a version: a bare
  `approve` is parsed as a change's, and the thread's item decides, so in a
  deploy's thread it answers with what a deploy's approval takes.
- The triager's guide to risk (2026-11-10, in a session the owner ran, as
  the prompt is the runner's): one label, `risk/high`, among the factory's
  own (`model.RiskLabel`). The triager's prompt says when a change is
  high-risk and its decision carries `"risk": "high"`, whatever it decides;
  the operator labels an issue that stays open, says so in the triage's
  comment and keeps it in `status.triage.risk`. Decided here: one label and
  not a scale, since a rule either names it or doesn't; the label is never
  taken off by a later triage, as a person may have set it; and it decides
  nothing by itself. The stand-in triager says it for "stand-in: risk
  high".
- The reviewer raises, never lowers (2026-11-10, in the same session; the
  verdict and the prompt are the runner's): a reviewer that approves says
  `"risk": "high"` and a `riskReason`, the run's result carries the reason,
  and `model.RaisedByReviewer` turns a need of `Reviewer` into `Person` for
  it. The operator records that as the review ends
  (`status.approval.raisedByReviewer`, with the reviewer's words as the
  reason), so it holds through a verification, and the wait, the chat
  thread, the merge note and the `approved` label follow from the need as
  for a rule. Decided here: the raise is of one head, so the next round's
  reviewer says it again or doesn't; other rules that still leave the head
  to the reviewer keep it; a rule that now asks for no review, or a Crew
  whose rules are gone, drops it, since the Crew's word comes first; only
  an approval raises, a request for changes being its own answer; and the
  verifier raises nothing. The stand-in reviewer says it for "stand-in:
  risk high", the words that have the stand-in triager label the issue.
- Nothing of #92 is open.

## Done when

Step 1's checks pass in `make check`, and #92, step 2, is closed.
