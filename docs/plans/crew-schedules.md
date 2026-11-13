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
| 3. Run one now (done) | #72 | an annotation on the Crew, and a button in the UI, file a schedule's issue at once |
| 4. The UI shows schedules (done) | #72 | the Crew's page lists each schedule, its next time and its last issue |
| 5. Standards propose schedules: the field (done) | #72 | `Standard.spec.schedules`, each in the shape of a Crew's schedule, held to the same patterns; nothing copies one into a Crew |
| 6. The UI offers a Standard's proposals (done) | #72 | a Crew's page shows the proposals of its Container's Standard whose name the Crew doesn't use, and a person takes one, with the time zone asked for; a proposal whose name is in use shows where the Crew's schedule differs from it |

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

## What step 3 found

Built 2026-11-10.

- The annotation's value is the schedule's name, and the request stands
  until it is answered: `theos.factory.io/run-schedule` stays on the Crew
  while the schedule's last issue is open or the Crew is paused, and the
  schedule runs when that ends. A request that vanished when it couldn't
  run at once would have to be made again by a person who can't see why.
- The run is the schedule's own filing with "due" set to now: the same
  issue, the same marker, the same hold by an open issue. Its next time
  stays as it was; a run asked for on Saturday doesn't move Monday's, which
  is passed over if Saturday's issue is still open then.
- The reason is in the `Scheduled` condition's message while the request
  waits, with the condition still True: the schedules can be used. A name
  the Crew doesn't have makes it False, reason `UnknownSchedule`, since
  what was asked can't be done; the annotation stays for the person to fix.
- The operator takes the annotation off right after it filed. If that
  write fails, the request files nothing while the issue is open and is
  taken off at a later poll; it would run once more after the issue closed
  if every removal until then failed.
- The UI's server allows one more write, the annotation alone on a Crew,
  as it allows the re-run of a release. The button shows only where the
  page can write, and reads "Asked to run now" while the request stands.

## What step 5 found

Built 2026-11-10. The field alone; the UI that offers the proposals is
step 6.

- **The proposal is a Crew's schedule, the same Go type** (`CrewSchedule`:
  name, cron, time zone, goal), so the two CRDs can't drift: a pattern
  changed for one is changed for the other, and a Crew takes a proposal by
  copying it as it is written. `listType=map` on `name` and at most eight,
  as on a Crew.
- **Taken is "the Crew has a schedule of that name".** Nothing else links
  the two: no reference from the Crew to the Standard, no status. A Crew
  that took a proposal owns its copy and may change its time and words; a
  Standard that later changes or drops the proposal changes no Crew, as a
  Relationship added after the scaffold changes no repository. The name is
  the proposal's identity: renaming one is dropping it and proposing
  another, and a Crew that took it under the old name keeps its schedule.
- **Step 6 can say "name in use", not where a schedule came from.** A
  schedule of a proposal's name may be the proposal taken, one a person
  wrote under that name, or one taken from another Standard the Container
  was on; the Crew's spec doesn't say which, and step 6 doesn't claim it.
  It offers the proposals whose name is none of
  `Crew.spec.schedules[].name`, and for one whose name is in use it shows
  the Crew's schedule beside the proposal, with where the cron, the time
  zone or the goal differ.
- **Step 6 is a Crew's page, where #72 says "when a Crew is made".** The UI
  makes no Crew: a Brief's Crews are made by the operator, the others by
  whoever applies them, and the UI's writes to a Crew are patches of one
  that exists (docs/design.md, "UI actions"). The Crew's page is the first
  place a person meets the Crew in the UI, so the offer is made there, and
  stays there for a proposal the Standard adds later.
- **Nothing is on by default, and the operator copies none unasked.** No
  reconciler reads `Standard.spec.schedules` today; a Standard stays a
  catalog entry with no status. The envtest specs hold it: a Crew whose
  Container's Standard has a proposal files nothing past the proposal's time
  and gets no schedule, and files once it has the schedule itself; a
  proposal added, changed and removed opens no pull request on the
  Container and changes neither its `status.standard` nor the schedule of a
  Crew that took it. How step 6 writes the taken schedule (the UI's own
  patch, or the operator asked by an annotation) is step 6's to decide; the
  rule is that a person asks.
- **The time zone defaults to UTC in the proposal too,** since the type is
  shared: a Standard can't know where its Crews are. The API server writes
  the default into the stored proposal, so a proposal read back never says
  "no zone given", and a copy as it is written runs at UTC. Step 6 therefore
  asks for the zone when a person takes one, and doesn't pass UTC on as the
  Standard's choice.
- **A proposal is checked as far as the CRD can** (the cron's shape, the
  name, a goal, eight at most, no name twice). The 30th of February and a
  time zone that can't be loaded are found by the Crew that takes it, in
  `Scheduled`, as for a schedule a person wrote. A Standard has no status to
  say it in, and a catalog kind with a reconciler for this alone is not
  worth it.
- **The sample is the webshop's `python-standard`**
  (`config/samples/webshop/factory.yaml`). The chart's catalog passes an
  entry's `spec` through whole, so `go-standard` can carry a proposal in
  `dist/chart/values.yaml` with no template change; that file is a protected
  path and was left for the owner's session.
- **Tested:** the CRD's refusals in envtest
  (`internal/controller/standard_schedules_test.go`) and as files in
  `test/invalid/standard-schedule-*.yaml`; the sample in `make validate-yaml`.

## What step 6 found

Built 2026-11-13 in a session the owner ran. The UI alone: no Go, no CRD and
no chart change. It closes #72.

- **The taken schedule is the UI's own write, not the operator's.** The page
  appends the schedule to the Crew's `spec.schedules`; no annotation asks
  the operator to copy it. Why:
  - A goal is words an agent will act on, and decision 7 has them written by
    whoever may edit the Crew. With the patch the person sends the cron and
    the goal they read on the page, as themselves when signed in with OIDC,
    and the API server's audit log names them. An operator that copied on
    request would copy what the Standard says when it gets round to it,
    which may no longer be what the person read.
  - Step 5 left the operator reading no proposal, and a Standard a catalog
    entry with no reconciler. That stays true. An annotation would have
    needed the Crew's reconciler to read Standards, write a Crew's spec, and
    say in a condition why a request failed: a second way to do what
    `kubectl edit` and the patch already do.
  - Nothing is lost by it: the same RBAC decides either way, since both are
    patches of the Crew.
- **The write is an append, and the UI server lets through nothing else.**
  The browser sends one JSON patch operation, `add` at `/spec/schedules/-`,
  with an object of `name`, `cron`, `timeZone` and `goal`. `ui/server.mjs`
  checks that shape (strings, no other field) and, as for a Brief's first
  prompt, makes the list for a Crew that has none, guarded by the
  `resourceVersion` it just read. So the UI can add a schedule and can't
  set, change or remove one: a stale page loses nobody's schedule. The
  values are the CRD's to judge: a name already in use (`listType=map`), a
  ninth schedule and a cron of the wrong shape are refused by the API server
  and the form shows its message.
- **The zone is asked for with an empty field.** The form's one input is the
  time zone; *Take* is off until something is typed. It is not filled in
  with the proposal's zone (UTC as stored, the Standard's choice or not) and
  not with the browser's either: a filled field is taken without being
  read. The hint names the browser's zone and the field suggests the IANA
  names the browser knows. What is typed goes as typed, trimmed: a zone the
  operator can't load is said by the Crew's `Scheduled` condition, on the
  same page.
- **Only the zone is asked.** The cron and the goal are copied as written;
  a person who wants other words takes it and edits the Crew. A form that
  edits a whole schedule would be the UI editing the model, which it
  doesn't (docs/design.md, "UI actions").
- **The rule is `scheduleProposals` in `ui/src/model/schedules.ts`.** The
  Standard is the one the Container chose (`choicesOf`: its `standardRef`,
  or what its Bundle expanded to). A proposal is offered when none of the
  Crew's schedules has its name, else in use with its differences: the cron
  as written, the zone with UTC for none, the goal without the white space
  at its ends (YAML's block styles end a text differently, and that is no
  change of words). Both lists are by name in code-point order
  (`compareText`), the same for every reader.
- **A taken proposal says its zone differs,** when the zone given isn't UTC:
  the page shows the Crew's zone beside the proposal's stored `UTC`. That is
  what the two objects say, and it tells the reader the two run at different
  hours. The page words it as a difference, not as a change someone made.
- **Without a writer the page still shows both lists** (actions off, the
  demo, nobody signed in), with the `kubectl edit` line in place of the
  form, as the header does for a step the UI can't take.
- **Not done:** the Standard's own page doesn't list its proposals, and the
  demo's data has none, so the demo shows no proposals panel.
- **Tested:** the rule's table (`ui/src/model/schedules.test.ts`); the
  page's panel, rendered (`CrewPage.test.ts`, "the Crew page's Standard
  proposals"); the form in a DOM, typing the zone and reading the patch it
  sends (`TakeProposal.test.ts`); the writer (`crews.test.ts`); the server's
  entry, what it sends upstream and what it refuses (`ui/test/server.test.mjs`).
  The UI's browser tests don't run in an agent's container; CI runs them.
  Evidence: [72-ui-proposals.txt](../screenshots/72-ui-proposals.txt).
