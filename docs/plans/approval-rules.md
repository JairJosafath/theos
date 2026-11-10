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

## Done when

Step 1's checks pass in `make check`, and #92, step 2, is closed.
