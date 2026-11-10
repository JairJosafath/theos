# A Crew works on a timer

GitHub issue #63, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-09 in a session the owner ran.

## Goal

A Crew starts work from the clock as well as from issues people file: the
upkeep a team least wants to do (dependencies, flaky tests, docs that drift)
runs on a schedule, through the same loop as everything else.

## Decisions, and why

1. **A schedule files an issue; it starts no Task itself.** Issues are the
   work (docs/design.md, "Agents and Tasks"): the Git host shows what was
   asked and what came of it, the Crew's order, routes, budget, tries and
   review apply unchanged, and a person can edit or close a scheduled issue
   like any other. A Task without an issue would be a second way in.
2. **On the Crew, not a kind of its own.** A schedule is for one Container
   and is worked by its Crew; a `Schedule` kind would need a reference to
   both and a reconciler for eight lines of spec. At most eight per Crew.
3. **Cron, five fields, read by our own parser** in `internal/model`: the
   business rules import no library for I/O or Kubernetes, and a cron reader
   is 80 lines with a table test. Numbers, ranges, steps and lists; no names
   and no macros, so the CRD's pattern can check the shape. The operator
   checks the rest (the 30th of February, the time zone) and says so in a
   `Scheduled` condition, as `InvalidTimeZone` does for a cadence, but
   without stopping the Crew: a bad schedule shouldn't hold its issues.
4. **Due at the first time after `since`,** where `since` is when the
   schedule last filed, last passed a time over, or was first seen. One
   stored time gives all three rules: a new schedule waits for its next
   time, an outage files one issue and not one per missed time, and filing
   is idempotent.
5. **One open issue per schedule.** A due time is passed over while the
   schedule's last issue is open, so a slow job never piles up. The issue is
   found by a marker line in its body and the label `kind/schedule`, as a
   bump's is, so a status write lost after filing can't file a second.
6. **A paused Crew files nothing,** and passes nothing over: on resume it
   files one issue if a time came due meanwhile.
7. **Triage is skipped.** The issue is filed `status/ready` with acceptance
   criteria the operator writes; the goal is the Crew's own words, written
   by whoever may edit the Crew.

## Steps

| Step | Issue | Done when |
| --- | --- | --- |
| 1. The rule, the field, the Crew files the issue | #63 | `model.NextScheduleStep` and `ParseCron` table-tested; `spec.schedules` with its CEL; envtest: due, held while open, one for many missed, paused, invalid, refused by the CRD; the sample and the guide |
| 2. "Nothing to do" is a result (done) | #72 | an implementer that finds nothing to change ends with `Nothing to do: <why>`; the Task closes the issue as done instead of failing. It touches `internal/agentrunner/`, a protected path |
| 3. Run one now | #72 | an annotation on the Crew, and a button in the UI, file a schedule's issue at once |
| 4. The UI shows schedules | #72 | the Crew's page lists each schedule, its next time and its last issue |
| 5. Standards propose schedules | #72 | a Standard names schedules for its language; a Crew takes or leaves them |

## Done when

Step 1 is merged: a Crew with a schedule files one issue when it comes due
and works it (the envtest specs in `internal/controller/crew_schedule_test.go`),
and the later steps are filed as their own issues.

## What step 2 found

Built 2026-11-10 in a session the owner ran, the result's field and the
prompt being the runner's.

- The line is the agent's to write, `Nothing to do: <why>`, as `Blocked:`
  is, and it counts only with no change made: a change beside the line goes
  on as any change, and a round that says "Nothing to do." with no reason
  still fails, with a hint that names the line.
- The Task ends `Merged`, reason `NothingToDo`, as `DoneByStandardChange`
  ends one whose Standard change did its issue: no new phase, since every
  reader of a Task's end knows `Merged` and `Failed`. What reads `Merged` as
  "a change landed" is told apart: a release would have waited forever for
  the merge commit of a Task that merged nothing (the release's rule for a
  merge not yet read), so such a Task is no merge to a release;
  `theos_tasks_merged` doesn't count it; a Scorer doesn't judge it. The
  UI's numbers already count only Tasks with a pull request.
- Only the build of an issue ends so. The runner says it of no spec, no
  Standard change, no Task with a pull request or resumed after its
  Standard change; the operator turns it back into a failed round for a
  Benchmark's Task and a Task with no issue, which the runner can't tell.
- Nobody reviews the agent's reason: there is no change to review, and a
  review run each quiet week is what the issue was filed against. The
  prompt says so, the comment on the closed issue quotes the reason and says
  how to reopen it, and the stand-in says it for "stand-in: nothing to do".
- The check command still runs before the round ends, as for any round: an
  agent that changed nothing costs one run of it.
