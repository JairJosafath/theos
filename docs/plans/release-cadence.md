# Plan: a released app's releases go out on a cadence, daily by default

Issue #2038, [theos#11](https://github.com/JairJosafath/theos/issues/11)
(reported on 0.30.0). Builds on
[brief-releases.md](done/brief-releases.md),
[brief-patch-releases.md](done/brief-patch-releases.md) and
[fixes-patch-next.md](done/fixes-patch-next.md), and needs #2025
([theos#9](https://github.com/JairJosafath/theos/issues/9)) first: an
empty open release must not hold the releases after it.

## Goal

A project chooses how often a released app's releases go out. A Brief's
apps release once a day by default. A day's release holds all the work
merged that day, from every round and fixes included, and its notes cover
the whole day. A change marked as bigger (a new feature someone wants out
now, or a fix for something broken in the released version) goes out as
soon as it merges, without waiting for the day's cut. The Crew keeps
starting and merging Tasks while the day's release waits. Nobody moves
issues between milestones by hand, and no empty milestone is left behind.

## What happens today

- Each Brief round that files work for a released Container makes a
  release milestone of its own (R6): the next minor for its features and,
  when it has fixes, a patch slot right after the release in progress
  (F1 to F6). So every round adds one or two releases.
- The Crew tags a release as soon as it is done: no open issue, no Task at
  work on one (`model.NextRelease`). With `prepareReleases` the prepare
  issue waits until no other Task works (`PrepareWaitsForTasks`). On a
  busy Crew that is rare, so it holds the release, and a release that
  waits for an idle Crew does not fit "agents keep working".
- The reporter's apps got 12, 6, 7 and 13 releases on four days, most with
  two to five issues, some just a docs line. Their only levers were sending
  fewer prompts, which leaves agents idle, or moving issues by hand, which
  leaves empty milestones that hold every later release (#2025).

## Decisions

| # | Topic | Decision | Why |
| --- | --- | --- | --- |
| C1 | The setting | `releaseCadence`, one shared type (`api/v1beta1/shared_types.go`): `every` (`Release`, `Day` or `Week`), `at` (`HH:MM`, 24-hour, default `18:00`), `timeZone` (an IANA name, default `UTC`) and `weekday` (only with `Week`, default `Friday`). CEL checks `at`'s pattern and that `weekday` comes only with `Week`. The operator checks the time zone, since CEL can't load one: a name it can't load makes the Crew `Ready=False`, reason `InvalidTimeZone`, and the Crew tags nothing until the name is fixed. | How often to release depends on the user and their users. It is a setting, never a fixed pick (AGENTS.md, *What can be configured is configured*). Cut time and time zone are part of that choice, since "daily" means a different hour for everyone. `Week` costs one field more and covers the next request we would get. |
| C2 | Where it lives | On the Crew (`Crew.spec.releaseCadence`), which tags, and on the Brief (`Brief.spec.releaseCadence`), which writes its value onto the Crews it made (label `theos.factory.io/brief`), as R5 does with `publishReleases`, and uses it to choose milestones. A Crew the Brief didn't make keeps its own value. | Tagging belongs to the Crew, so a Crew made by hand can use a cadence too. Milestones a round makes belong to the Brief. A person sets the cadence once on the Brief, the place they steer from, and does not edit Crews they never made. |
| C3 | Defaults | A Crew with no `releaseCadence` means `Release`: tag each release as soon as it is done, as today. A Brief's defaults to `every: Day`, `at: 18:00`, `timeZone: UTC`. An existing Brief reads the default on upgrade, so its apps release daily from then on, and the release notes say so under *Changed*, with `every: Release` as the way to keep today's behaviour. | The owner asked for a daily release as the default, and Briefs are where the stream of tiny releases comes from: every round makes milestones. A hand-made Crew releases only the milestones a person makes, already at that person's pace, so changing it under them would surprise them and fix nothing. Under *Compatibility* adding an optional field with a default is allowed, and what the operator does with an object may change as the notes say. |
| C4 | The cut | With `every: Day` or `Week`, the Crew holds a done release until its cut time, reason `WaitingForCut`, with the message "v0.15.0 holds 4 merged issues and goes out at 18:00 UTC". At the cut time (the first `at` in `timeZone` on or after the last cut, on `weekday` for `Week`, DST handled by the time zone) the Crew cuts the first open release, the one `NextRelease` would tag. Its open issues move to the next open release that can still take work (no prepare issue), and the Crew makes that release if there is none (C6). Then the cut release is done and goes on as today: prepare, full check, tag, publish. A cut that finds no merged work in the release moves its issues and closes it untagged, as #2025 closes an empty release. | A release that waits for all its issues to close never ships while rounds keep adding work. Cutting at a fixed time ships what is merged and carries the rest, so agents never wait for a release and a release never waits for agents. One cut per period gives one release a day. |
| C5 | Rounds under a cadence | A Brief round, a tending round included, files every issue and every taken suggestion of a released Container in the release in progress: the first open, dated version milestone that can still take work (F1's order, no prepare issue). It makes one, with C6's title and due date, only when none is open. The patch slot (F2) is not used. Fixes, features and suggestions all go to the same release. With `every: Release` nothing changes: F1 to F8 stay. | The day's release is the one that collects the day's work. Using the release in progress also absorbs milestones earlier rounds planned: once a cut carries work into the next planned milestone, that one collects new work until its own cut, and empty planned ones are closed (#2025). No migration step is needed. |
| C6 | New release milestones | A milestone the Brief (C5) or the Crew (C4) makes under a cadence is titled the next minor after the newest version milestone, open or closed (R6), and is due at the next cut time, or an hour after the latest open release, whichever is later (P4). The Crew's milestone description ends `<!-- theos-crew: <crew>/carry <from-title> -->`, so a restart finds it again. The Brief's keeps its round marker (R6, F7). | The same title rule as today, so versions keep their order. The due date only orders releases. The marker makes every cut step idempotent: a restart finds the milestone it made instead of making a second one. |
| C7 | The version at the cut | At the cut, a release whose merged work is all fixes (`model.IsFix`, kind/bug or kind/test, and taken suggestions marked `fix: true`) is retitled to the next patch after the last release tag (`v0.14.1` after `v0.14.0`), skipping a title that exists, before it is prepared. Any other release keeps its minor title. Retitling needs a new call on the Issues port (step 3). | Under a cadence a release's contents are known only at the cut, so its version is decided then. A day of fixes is a patch, as semantic versioning and today's F-rules expect. A title that was never tagged is free to change, and the prepare issue, tag and notes come after the cut, so they all see the final title. |
| C8 | What is bigger | A merged issue labelled `release/now` (a new label, the only one in its scope, which the Crew adds to the repository's labels) cuts its release at once, without waiting for the cut time, and the releases before it in order too. It is meant for a fix of something broken in the latest release that users hit (a regression, a crash, wrong output, a security hole), or for a feature the person asks to ship now. A fix for work not yet released, a test, docs or a refactor is never bigger. The early cut is the same as C4's, so the release also carries the day's work merged so far, and its notes say so. With `every: Release` the label changes nothing. | A tag carries everything merged before its commit, so a sooner release can never leave out the day's earlier merges. Cutting early is the honest form of "a release of its own". The label is visible on the issue and in the notes, and a person can add or remove it on the Git host, where they already triage. |
| C9 | Who marks it | The planner, by `release: now` in a plan issue's front matter or `now: true` on a taken suggestion's answer in `plan/suggestions.yaml`, which the operator turns into the label. Plan validation refuses `now` on a skip, as it refuses `fix` (F5). A person, by adding the label to an issue on the Git host, or by a prompt ("ship the login fix today") that the planner answers with C9's fields. The planner's guide (`internal/model/planner.md`) says what counts as bigger (C8), and that a tending round never marks anything. Reviewers and the triager don't mark. | The planner already reads the person's words and every suggestion it answers, as it does for `fix: true`. A person can always override on the Git host. A tending round runs without a person, so it has no one's word that something is urgent. |
| C10 | Agents keep working | Under a cadence the prepare issue waits only for Tasks on the cut release's issues, and after a cut those are none. While a cut release's prepare Task runs, the Crew keeps starting Tasks and running their rounds, and each one that is ready to merge waits at its merge, reason `WaitingForRelease`, costing no round, until the tag is made. Then the merges go on in their usual order. With `every: Release`, `PrepareWaitsForTasks` stays as it is. | A prepare that waits for an idle Crew would hold the day's release indefinitely. Holding only merges, and only for the length of one prepare Task (minutes), keeps the changelog equal to what the tag carries (no `MergedAfterPrepare`, no carried work the notes miss) while agents keep working. |
| C11 | Empty milestones | Needs #2025's rule: an open release with no issue doesn't hold later ones, and the Crew closes it. A cut that carries every issue out of a release (C4) leaves it empty, so it is closed the same way. | Without it, the first cut with no merged work would stop the app's releases, the bug #2025 reports. |
| C12 | What the person sees | The Crew's `status.nextCut` (time and release title) and `Released=False, WaitingForCut`. The Brief's `status.apps[].nextRelease` copies both, and the Brief card shows "next release v0.15.0 at 18:00 UTC, 4 merged". The notes (the prepare issue's list, then the changelog) list every issue the release carries, as today. | The person needs to see why nothing has been tagged yet and when it will be. The Crew already knows every fact, so the Brief copies them (R8). |

Not doing: a command to cut now from chat or an annotation. A person who
wants that labels a merged issue `release/now`. A per-Container cadence on a
Brief: the Brief's one value goes to all its Crews, and a Crew made by hand
sets its own.

## Steps

Each step is one pull request and leaves main working. Nothing reads
`releaseCadence` before step 4. Step 2 needs #2025's model change merged.

1. **The design.** This plan, and `docs/design.md`: under *Crews and
   releases*, a **Cadence** bullet after *Tagging* (C1, C3, C4, C7, C8,
   C10); under *Briefs*, the *Releases when the plan says so* bullet gets
   C2, C5 and C9. `fixes-patch-next.md` gets a line saying F2 applies only
   with `every: Release`.
   *Done when:* merged; the design's two bullets say what this table says.
2. **The model** (`internal/model/release_cadence.go`, `brief_release.go`,
   `release.go`). `ReleaseCadence` and `NextCut(cadence, lastCut, now)`
   (C1, C4); `ReleaseFacts` gets `Cadence`, `Now`, `LastCut` and the
   `release/now` issues, and `NextRelease` returns a hold
   (`WaitingForCut`) or a cut step: the issues to carry, the release to
   carry them into or the title and due date of the one to make (C6), and
   the new title (C7). `CadenceSlot(milestones, cadence, now)` for C5;
   `ReleaseNowLabel()` and `IsReleaseNow(labels)` (C8). Plan parsing of
   `release: now` and `now: true`, and the refusal on a skip (C9).
   `prepareStep` waits only on the cut release's Tasks under a cadence
   (C10). Table tests:
   - `NextCut`: daily before and after `at`, across a DST change in
     `Europe/Amsterdam`, weekly on `Friday` from a Saturday, and an
     unloadable time zone.
   - `NextRelease` under `Day`: a done release before its cut time is held
     with the message; at the cut, open issues are carried to the next
     open release, or a new one is made when none takes work; a release
     with no merged work is carried and closed; a merged `release/now`
     issue cuts at once, and cuts a release before it too; all-fixes
     work is retitled to the next patch and mixed work keeps its minor;
     a patch title that exists is skipped.
   - `every: Release` gives today's steps for each existing case (the
     existing table, unchanged).
   - `CadenceSlot` joins the release in progress, skips a prepared one,
     and makes `v0.1.0` when there is none.
   - Plan parsing: `release: now` on an issue and `now: true` on a take
     are accepted, and on a skip refused.
   *Done when:* those tests and `make check` pass.
3. **Retitling a milestone** (`internal/ports/issues.go`, the fake,
   `internal/adapters/gitea`). `EditMilestoneTitle(repo, id, title)`. Gitea
   clears the due date on an edit that leaves it out (step 3 of
   brief-releases.md), so the adapter sends the due date back.
   *Done when:* a `make test-gitea` test retitles a dated milestone and
   reads back the new title with the same due date, the fake's test does
   the same, and `make check` passes.
4. **The Crew cuts** (`api/v1beta1`, `internal/controller/crew_release.go`,
   the Task's merge). `Crew.spec.releaseCadence` with its CEL rules, then
   `make manifests generate`; the Crew runs C4, C6 to C8 and C10, requeues
   at the next cut, and writes `status.nextCut` (C12); the `release/now`
   label goes into each repository's labels; a Task ready to merge while
   a cut release is being prepared waits (`WaitingForRelease`).
   Envtests on the fake Git host:
   - a `Day` Crew holds a done release until a faked clock passes 18:00,
     then carries its open issue into a new `v0.16.0` with the carry
     marker and tags `v0.15.0`;
   - a restart in the middle of a cut makes no second milestone;
   - a merged `release/now` issue tags at once;
   - an all-fixes release is tagged `v0.14.1`;
   - a Task ready to merge waits while the prepare Task runs, then
     merges;
   - an invalid time zone gives `InvalidTimeZone` and no tag;
   - a Crew without the field tags as before.
   The CEL failing cases (bad `at`, `weekday` without `Week`) go in
   `test/invalid` for `make validate-yaml`.
   *Done when:* those specs and `make check` pass.
5. **The Brief files under a cadence** (`api/v1beta1/brief_types.go`,
   `internal/controller/brief_release.go`, `brief_apply.go`,
   `brief_tend.go`, `internal/model/planner.md`).
   `Brief.spec.releaseCadence` with its default, then `make manifests
   generate`; the Brief writes it onto its Crews (C2); rounds and tending
   rounds file into `CadenceSlot` (C5); `release: now` and `now: true`
   become the label (C9); `status.apps[].nextRelease` (C12); the planner's
   guide on what counts as bigger. `docs/getting-started.md`: *How often a
   Brief's apps release*.
   Envtests:
   - two rounds on one day file into the same release, and a fix and a
     feature land in the same milestone;
   - a round after the cut files into the next release;
   - a planned `release: now` issue is labelled;
   - a Crew the Brief made gets the Brief's cadence, and a Crew it didn't
     make keeps its own;
   - `every: Release` on the Brief keeps F2's patch slot (the existing
     specs, unchanged);
   - a Brief stored without the field reads `Day`.
   *Done when:* those specs and `make check` pass.
6. **The card** (`ui/`). The Brief card's app line shows the next release,
   its time and how many issues it holds, and the demo fixture has one
   app waiting for its cut.
   *Done when:* the UI tests pass and a screenshot of the demo card is in
   `docs/screenshots/`.

## Done when

The six steps are merged. A Brief left at its default releases each
released app at most once a day (plus any `release/now` cut), each
release's notes list everything it carries, agents start and merge Tasks
throughout the day, and no milestone is left empty and open. The proof is
the envtests of steps 4 and 5 and `make check`. Whether a real day of
rounds comes out as one release is for #2038's reporter to see on their
device once step 5 ships. No agent runs Kind for it.
