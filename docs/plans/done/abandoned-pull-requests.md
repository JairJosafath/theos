# Abandoned pull requests (#617)

**Goal.** A try that ended for good leaves no open pull request behind, so a
repository's open pull requests are the work in flight. Reported by
gray-lenovo: `cel-lens` had 10 open pull requests and 8 were dead.

## Decisions

The maintainer decided the operator closes a try's pull request, with a short
comment naming the Task and why, when no later work will use it, and never
deletes the branch. What "no later work" means, decided here:

1. **Not at the moment the Task fails.** #607 lets a new try at an issue
   continue an earlier try's *open* pull request, and a failed Task hands its
   issue back to a person, who may label it ready again. Closing on failure
   would take that pull request away from the next try. So a failed Task's
   pull request stays open while a later try may come.
2. **Closed once none will** (`model.AbandonedPull`), checked in this order:
   - merged or closed on the Git host: nothing to do, recorded;
   - a newer try at the issue has this pull request (#607): it is that try's
     now; recorded as continued and left open;
   - a newer try at the issue has its own pull request: closed, the comment
     links the newer one;
   - a newer try still runs without a pull request: kept, it may take this
     one up (#607); a closed issue ends that try at its next run, and this
     Task looks again;
   - the Task had no issue: closed at once, no try comes back to a goal;
   - the issue is closed (or gone): closed;
   - the issue's failed tries reached `MaxIssueAttempts`: closed, a Crew
     starts no more;
   - otherwise it stays open and the Task looks again.
3. **Where it runs.** In the Failed Task's own reconcile, every 10 minutes
   (`TaskReconciler.PullRequestRecheck`), because the Git host doesn't call
   back when an issue closes. `status.pullRequestEnded` records how it ended
   and stops the looking. The tries are listed live, not from the cache, so a
   newer try that took the pull request up a moment ago keeps it. Tasks from
   before the upgrade have it empty, so their pull requests are cleaned up
   too while the Task exists.
4. A Git host that can't be reached is logged and tried again at the next
   look; it never changes the Task's phase.

## Steps

One pull request: the rule and its table test in `internal/model`, the status
field, the reconcile step, envtests, this plan and the `docs/design.md` bullet.

## Done when

- `internal/model` table test: every rule above, ordering by creation time and
  name.
- envtests against the fake Git server
  (`internal/controller/task_abandoned_test.go`): a Failed Task's pull request
  stays open while its issue is open and closes, with the comment, once the
  issue is closed, the branch kept; an older try's pull request closes when a
  newer try has its own (as tries before #607 did), linking it; a pull request
  a newer try continues through #607 stays open, its issue closed or not; a
  Task without an issue closes its pull request at once.
- `make check` passes.

A human run of Kind would show the same against Gitea: the close and the
comment go through the provider calls Gitea's tests already cover.
