# A Brief's issues keep the plan's order

GitHub issue #428, reported by gray-lenovo on v0.5.0. Agent: otter-428.
Related, but separate: #430 (the CLI can't fetch the engine's Go module) is
about getting the dependency at all; this plan is about the order issues run in.

## Goal

When a planner's issue needs another issue's work, in the same app or another
app of the Brief, the factory starts it only once that work is merged. In
gray-lenovo's `cel-lens`, the CLI's first issue waits for the core's checker
issue to close instead of starting at once, failing the review three times and
going to `status/needs-human`.

Done when a round whose plan orders its issues files them with Gitea
dependencies between them, across repositories, before any of them is ready;
the Crews already wait for open dependencies (design.md, "A Crew per Container
picks up the work"), so nothing else has to change.

## Decisions, and why

1. **The planner says the order; the operator doesn't guess it.** An issue
   file's front matter takes `after:`, the round numbers (`nn`) of the issues
   whose work it needs merged first, from any Container of the round:

   ```markdown plan/issues/03-cli.md
   ---
   title: Check a file from the command line
   labels: [kind/feature]
   after: [2]
   ---
   ```

   A Relationship from the CLI to the core is too coarse to order by: it says
   the CLI uses the core, not which of the CLI's issues need which of the
   core's, and ordering every CLI issue after every core issue would serialize
   the apps for no reason. The planner already knows the order (gray-lenovo's
   plan wrote it in prose); `after:` makes it something the operator can keep.
2. **Only issues of the same round.** `after:` names issues by their `nn`, so
   the check is local to the plan and needs no Git calls. An issue that needs
   work filed in an earlier round is rare (each round's issues build on what
   is merged) and is left out; it can come later with a reference form such as
   `core#4` if a person asks for it.
3. **The plan is checked like the rest of it.** `after:` names issues of the
   plan, never the issue itself, at most ten (a round files at most ten
   issues), and the order has no cycle; otherwise the plan is refused with the
   file and the reason, and the planner's next round gets it. A design idea
   (`status/design`) is neither ordered nor waited on: it goes to a designer,
   not a Crew's Task, and isn't closed by a merge.
4. **Gitea dependencies carry the order.** The Crews already read an issue's
   open Gitea dependencies, across repositories too, and start it only when
   none is left (`OpenBlockers`). So the operator only has to make them:
   `AddIssueDependency` takes the blocker's repository, and the dependent
   issue depends on the blocker issue wherever it is. A blocker closes when its
   pull request merges, so "merged" is what the dependent waits for. A blocker
   handed back to a person stays open, and its dependents keep waiting; the
   Crew's `Progressing` message already counts them ("1 waits on open
   dependencies").
5. **Ready only once ordered.** A Crew may pick a `status/ready` issue up the
   moment it exists, before its dependency is added. So the operator files an
   issue that has `after:` only once its blockers are filed, without
   `status/ready`, adds its dependencies, and then labels it ready. A restart
   between those steps finds the issue by its marker and finishes them: adding
   a dependency is safe to repeat. Blockers are filed first, since Containers
   become Ready at different times; a dependent whose blocker's Container
   isn't Ready yet waits with it, and the Brief stays `Applying` with that
   Container as the reason, as it does now.
6. **The implementer is told.** The dependent's body gets a line naming each
   blocker, `Builds on owner/name#N (title).`, above the Brief's marker, so
   the agent knows where the work it builds on lives. The operator knows the
   numbers at filing time, since blockers are filed first.
7. **Off until the planner is told.** The planner guide documents `after:` in
   the last step, so the parser and the filing land first and do nothing until
   a planner writes it.

## Steps

Each step is a GitHub issue, `status/proposed`, one pull request each.

1. **Plan issues carry `after:`** (#440, `internal/model`). `PlanIssue.After`,
   parsed from the front matter; `CheckPlan` refuses an `after:` naming no
   issue of the plan, the issue itself, a design idea, more than ten, or a
   cycle. Done when table tests show each refusal and an ordered plan passing,
   and `model.FilingOrder` (or the like) returns the issues blockers-first.
2. **Dependencies across repositories** (#441, `internal/gitprovider`).
   `AddIssueDependency(ctx, repo, number, blockerRepo, blocker)`; existing
   callers (the Standard change in `task_standard.go`) pass their own
   repository, and the controllers' fake Git takes the blocker's repository too.
   Done when the unit tests and `make test-gitea` makes an
   issue of one repository depend on one of another, `OpenBlockers` reports it
   as `owner/name#N`, and repeating the call adds nothing.
3. **The round files issues in order** (#442, `internal/controller`, the
   planner guide; depends on #440 and #441). Blockers first; an issue with `after:` filed without
   `status/ready`, its dependencies added, its body naming its blockers, then
   labelled ready; restart-safe by the marker. `internal/model/planner.md`
   documents `after:` with the CLI-on-the-engine example. Done when envtest
   applies a two-Container round whose CLI issue is after the core's, with the
   core's Container Ready later than the CLI's, and shows the CLI issue filed
   only after the core's, depending on it, labelled ready last; and a restart
   between the steps finishes them.

## What only Kind would show

That a Crew of the CLI's Container leaves the issue alone until the core's
pull request merges, on a real Gitea with real Crews. The Crew's waiting is
already covered by envtest against faked blockers; the cross-repository
dependency itself by `make test-gitea` in step 2.
