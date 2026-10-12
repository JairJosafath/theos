# Plan: checks the machine killed, or a commit pushed again, run again

GitHub issue #870 (reported by gray-lenovo). Agent: otter-870. Done: steps 1 (#877) and 2 (#878) landed; evidence in [checks-run-again.txt](../../screenshots/checks-run-again.txt).

## Goal

A CI job the machine killed for lack of memory is the infrastructure's failure,
not the change's: its failed jobs run again, the Task spends no round and its
issue no try, and the agent is never told its test is flaky. And when an
implementer pushes the same commit whose checks already failed (it couldn't
reproduce the failure and rightly changed nothing), the checks run again
instead of the old failure being read a second time.

Done when both steps below have landed: `internal/model` table tests show a
failed log with `signal: killed` leading to a rerun of the checks in the same
round, the third such failure in a round failing the Task with reason
`RunnerOutOfMemory` (no try spent), and a round that pushes the commit whose
checks failed leading to a rerun before the checks are read; envtests show
the Task calling `RerunFailedJobs` on the failed run at its commit, staying in
`Checking` in the same round, and not reading the failure from before the
rerun. `make check` passes on each.

## What happened

On `cel-lens-core#292` the agent pushed 296c7ab at 14:23Z. Its CI run 962
started while the machine was overloaded; at 14:39Z `go build: signal:
killed`, and the root package showed `FAIL` after 613 s, no test named. The
Task sent the agent back with "A test that passes for you but fails in CI is
flaky: make it deterministic." Rounds 2 and 3 found nothing to fix and pushed
296c7ab again; each time, two seconds later, the Task read run 962's failure
at that commit once more (`CombinedStatus` of `status.commit`,
`internal/controller/task_controller.go`) and spent the round. The Task failed
after 3 rounds; a retry by prompt changed nothing, the checks ran again, passed
and the change merged.

## Decisions

- **What counts as killed.** The failed log, read the way `RunnerOutOfSpace`
  reads it (the change's own test output, findings and captured output passed
  over), shows the runner or toolchain killed by a signal for memory: Go's
  `signal: killed`, the shell's `Killed` report of a job
  (`/bin/sh: line 1: 1234 Killed ...`), make's `Error 137`, `exit code 137`
  and Node's `JavaScript heap out of memory`. A new
  `RunnerKilled(log)` in `internal/model/task.go` shares the section skipping
  with `RunnerOutOfSpace` (one scanner, two phrases lists), so a test that
  prints `signal: killed` in its own message doesn't count.
- **A killed job runs again; out of space still fails.** Running out of room
  stays as the design says (`RunnerOutOfSpace` fails the Task: the disk stays
  full until someone frees it). Memory is different: the reporter's machine
  was overloaded for a while, and the same commit passed later. So a killed
  job is rerun, not failed: new action `ActionRerunChecks`, the Task stays in
  `Checking` in the same round, and the agent sees nothing.
- **Bounded.** At most `MaxChecksReruns = 2` reruns for a killed job per
  round, counted in a new `status.checksReruns` (reset with the round, like
  `reruns`). The third killed failure in a round fails the Task with reason
  `RunnerOutOfMemory`, which `RunnerFault` counts as the runner's (no try),
  and the hand-back says to free memory on the runner or run fewer jobs at
  once. A runner that is always short of memory then doesn't loop forever.
- **How it reruns.** The operator lists the runs at the commit
  (`CommitRuns`, newest first), takes the failed ones and calls
  `RerunFailedJobs` on each, as `crew_release.go` already does for releases.
  No new provider call. A rerun that the host refuses fails the Task with
  reason `NoCIResult` and the host's message (the infrastructure's, no try).
- **Not reading the old failure.** Right after a rerun the commit's combined
  status may still say `failure` until the runner picks the job up. The Task
  records when it reran (`status.checksRerunAt`) and treats a failed status
  as pending until every run at the commit has ended and one started after
  that time (`WorkflowRun.Started`): a run of another workflow that starts
  meanwhile doesn't say the rerun did. That wait has the bounds of any
  pending checks, counted from the rerun: a queued or running rerun the
  queued and running limits, and no run started the existing
  `ChecksStartLimit`, failing with `NoCIResult` past them, as today.
- **A kill read while other runs still run.** The Git host runs a run's
  jobs again only once it ended, so a killed failure whose commit still has
  a run queued or running waits for it, with the same bounds, before its
  jobs run again.
- **The same commit pushed again.** When an implementer round ends with a
  commit equal to the one whose checks failed in the round before
  (`status.checks.commit` with a failed state), the Task enters `Checking`
  and reruns the failed jobs (`ActionRerunChecks`) before reading the
  checks, then waits as above. The round is still spent: the agent ran and
  found nothing, and a failure after the rerun is a real result. This rerun
  doesn't count towards `MaxChecksReruns`; each round pushes once, so it
  happens at most once per round.
- **The flaky line stays for real failures.** A failure that is neither
  killed nor out of space keeps today's feedback, flaky hint included.

## Steps

1. A CI job killed for lack of memory runs its checks again, at no round
   or try (#877). Model: `RunnerKilled`, `ActionRerunChecks`,
   `MaxChecksReruns`, `ReasonRunnerOutOfMemory` in `RunnerFault`; API:
   `status.checksReruns` and `status.checksRerunAt`; controller: the rerun
   through `CommitRuns` and `RerunFailedJobs`, the old-failure guard, the
   hand-back message. Table tests and envtests as in "Done when".
2. A commit pushed again after its checks failed gets a fresh run (#878),
   on step 1's action and guard. Model table test and envtest.

## Design

`docs/design.md` says this under "A Task waits for checks a runner will run"
and in the Crew's list of failures that cost no try.
