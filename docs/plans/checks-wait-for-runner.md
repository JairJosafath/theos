# Plan: a Task waits for checks a runner will run

GitHub issue #756 (reported by gray-lenovo). Agent: otter-756.

## Goal

A Task whose pull request's check is queued behind other jobs on an online
runner, or is running, keeps waiting for it. Only a check that never starts, or
whose runner is gone, or that runs past any sane job length, ends the Task. And
such an end is the infrastructure's fault, not the agent's: it costs the issue
no try, and the next try picks up the green pull request at review instead of
running the implementer again.

Done when the three steps below have landed: `internal/model` table tests show
a Task whose run is queued past 30 minutes on an online runner keeps waiting,
one with no run fails after 30 minutes, one queued with no runner online fails
after 30 minutes; a `make test-gitea` test reads a commit's run state and the
runners from a real Gitea; envtests show a no-CI failure leaving the issue's
tries untouched and a continuing try whose pull request has green checks going
straight to review. `make check` passes on each.

## What happened

On `cel-lens-vscode#144` the agent pushed pull request #162 at 01:12:36Z. The
user's Gitea has one runner, busy with a release build and five other Tasks'
checks. #162's job started at 01:40:53Z and passed at 01:43:09Z; the Task had
failed at 01:42:36Z, "No CI result after 30m0s; check that a runner for the
repository is online". Today a Task in `Checking` fails after a flat
`ChecksTimeout` (30 minutes, `internal/model/task.go`) whatever its run is
doing, because the operator reads only the commit's combined status, which is
`pending` for a job that waits, one that runs and one nobody will ever run.
The failure handed the issue back and spent one of its three tries.

## Decisions, and why

1. **The run's state comes from Gitea Actions, not the commit status.** The
   combined status can't tell "waiting" from "running" (Gitea writes both as
   `pending`; only the description text differs, and that is not an API). The
   operator already lists Actions runs by commit for release tags
   (`TagRuns`); a Task in `Checking` lists the runs at its head commit the
   same way (`GET /repos/{o}/{r}/actions/runs?head_sha=`) and reads each
   run's status: `queued`/`waiting` (queued), `in_progress` (running),
   `completed`. The commit status stays the source of the *result*: success,
   failure and the required contexts are read as today.
2. **"Is a runner online" comes from the runners API.** The operator's token
   is an admin's (it makes the agent bot users), so it reads
   `GET /api/v1/admin/actions/runners`, which lists every runner the instance
   has, global, organisation and repository ones, with their status. A runner
   counts when it is online, idle or busy; one whose labels can't run the
   repository's jobs is not ours to judge (the job's `runs-on` is the
   Standard's), so any online runner counts. A Gitea that doesn't answer
   (older, or the call fails) is *unknown*, and unknown is treated as online:
   a wrong "offline" fails good work, a wrong "online" only waits longer,
   which the ceilings below bound.
3. **The bounds, by state.**
   - **No run at the commit** after 30 minutes in `Checking`: fail, "No CI run
     started for <commit> after 30m0s; check that the repository's workflows
     run on pull requests". A Standard whose workflow doesn't trigger, or a
     Gitea with Actions off, never makes a run; 30 minutes is long enough for
     Gitea to schedule one, as now.
   - **Queued, a runner online:** wait. The queue is moving or will; the
     report's case. Ceiling: 6 hours queued (`QueuedChecksLimit`), since a
     backlog that long is something a person has to see, and the Task should
     not hold a slot and a Crew's turn for ever.
   - **Queued, no runner online** for 30 minutes in `Checking`: fail, "CI run
     <url> is queued and no runner is online; start a runner for the
     repository". A runner that drops while a job waits fails the Task 30
     minutes after the Task entered `Checking`, at the earliest; one that
     comes back in time saves it.
   - **Running:** wait up to 3 hours from the run's start
     (`RunningChecksLimit`), act_runner's default job timeout, plus 15
     minutes for it to report. A runner that dies mid-job leaves the run
     `in_progress` until Gitea's own zombie-task cleanup ends it; this bound
     covers a Gitea that doesn't.
   - `ChecksTimeout` goes; the three limits replace it. While it waits, the
     Task's message says which state it is in ("Waiting for checks: queued
     behind other jobs since 01:13Z", "Waiting for checks: running since
     01:40Z"), and the Brief's line for a Task in the checks stage says the
     bound that applies instead of "fails it after 30 minutes".
4. **These failures are the infrastructure's, so they cost no try.** A Task
   that fails for one of the reasons above fails with reason `NoCIResult`
   (not `Failed`). A Crew counts only other failures towards
   `MaxIssueAttempts`, as a checks timeout already costs no round. The issue
   is still handed back (`status/needs-human`) with the message: someone has
   to start or fix the runner, and handing it straight back to the Crew would
   start try after try against a dead runner. When a person, or a Brief's
   autopilot, readies it again, the Crew starts a new try, which does not
   count either.
5. **A try that inherits green work starts at review.** A new try that
   continues an earlier try's open pull request (`status.continues`, from
   #607) whose head is the commit the earlier try pushed, and whose checks on
   that commit now pass, skips the implementer: it enters `Checking` at that
   commit, sees success, and starts the review, as any Task with passing
   checks does. Checks still pending or failed, or a head someone moved, start
   round 1 as today. The reviewer, not the implementer, is what a finished
   change still needs; this is the "shouldn't need a fresh try from scratch"
   of the report, without reviving a Failed Task (a Failed Task stays
   terminal; its pull request carries the work).

## Steps

Each is its own issue and pull request; main keeps working between them.

1. **#757, Gitea: a commit's CI runs and the online runners** (`internal/gitprovider`).
   `CommitRuns(ctx, repo, commit)` lists the Actions runs at a commit, any
   event, with status, conclusion, start time and URL (the `TagRuns` reader
   generalised); `RunnersOnline(ctx)` returns how many runners are online, or
   unknown. Nothing calls them yet. Done when httptest tests cover parsing,
   paging and the unknown case, and a `make test-gitea` test pushes a commit
   with a workflow to the local Gitea and reads one queued run (the test Gitea
   has no runner) and no online runner.
2. **#758, The Task waits while its run is queued or running** (`internal/model`,
   `internal/controller`, the Brief's checks line). Facts gain the run's
   state, since when, and whether a runner is online; `nextTaskStep`
   applies decision 3, failing with reason `NoCIResult`; the message says the
   state. Depends on #757. Done when model table tests show: queued 45
   minutes with a runner online waits; no run after 30 minutes fails; queued
   with no runner online after 30 minutes fails; running 2 hours waits;
   running past 3h15m fails; queued past 6 hours fails; and an envtest with a
   faked provider shows a Task whose run is queued past 30 minutes still in
   `Checking`.
3. **#759, A no-CI failure costs no try, and a green inherited pull request goes to
   review** (`internal/model` crew and Task, `internal/controller`). Decisions
   4 and 5. Depends on #758. Done when a model test shows an issue with
   three `NoCIResult` failures still offered to the Crew, and an envtest shows
   a try continuing a pull request whose head has passing checks starting the
   reviewer with no implementer run.

What only a real node would show, and a human run of Kind checks: a Task's
check queued behind a busy act_runner keeps the Task in `Checking` until the
job runs, and stopping the runner fails it after 30 minutes with the "no
runner is online" message.
