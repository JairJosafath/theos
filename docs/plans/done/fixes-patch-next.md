# Plan: the fixes a round takes go into the patch release right after the release in progress

GitHub issue #874 (reported by gray-lenovo). Agent: otter-874.

## Goal

A fix a Brief's round takes ships in the next patch release after the release
in progress, not behind the feature releases already planned. That covers a
fix the planner files and a reviewer's bug or test suggestion the round takes.
Nobody makes a patch milestone or moves an issue into it by hand any more.

Done when the two steps below have landed: model table tests show where the
next fix goes in each case (no open release, a release in progress with
planned ones after it, a patch already open after it, one already being
prepared, a closed one with that title) and which issues and taken
suggestions count as fixes; envtests show a mixed round filing its fixes in
the patch slot and the rest in the next minor release, a round of only fixes
joining an open patch release, a taken fix suggestion landing in the patch
slot and a restart making no second milestone. `make check` passes on each.

## What happens today

`docs/design.md` (*A Brief's apps are released when the plan says so*) and
[brief-patch-releases.md](brief-patch-releases.md) (P1 to P4): a round
makes one release milestone per released Container. It is the next patch
version after the newest version milestone only when all of the round's work
for the Container is `kind/bug` and the round takes no suggestion; otherwise
it is the next minor version. Either way it is due after every open release.
So a fix lands behind every planned release, and a round that takes a
reviewer's test fix gets a minor release (#874's rounds 64, 73 and 74).

The maintainer decided on #874: fixes go into a patch release due right after
the release in progress. A taken bug or test suggestion counts as a fix. In a
mixed round, the fixes go to that patch and the features to the next minor,
as today. When such a patch release is already open, the fixes join it.

## Decisions, and why

| # | Topic | Decision | Why |
| --- | --- | --- | --- |
| F1 | The release in progress | The open, dated version milestone due first (`compareReleases` order: due date, then title), whether or not its prepare issue is filed. When no version milestone is open, it is the newest version milestone, open or closed, as P1 counts today. | A Crew works the release due first (#873 keeps that release first in line), so that is the release the user waits on. A release being prepared still comes out before anything filed now, so the patch goes right after it. |
| F2 | The patch slot | Start from the release in progress and take the next patch title (`v0.13.1` after `v0.13.0`). If an open milestone has that title and can still take work (no prepare issue, as `ReleaseTakingWork` counts it), the fixes join it. If the title exists in any other state (closed, or prepared), start again from it (`v0.13.2`). Otherwise make a milestone with that title. | Joining an open patch is what the maintainer decided, and it stops one round after another making `v0.13.1`, `v0.13.2`, .... Skipping a closed or prepared title never reuses a title and never adds work to a release already being tagged (#834). |
| F3 | No renumbering | Planned milestones keep their titles and due dates. | Patch titles of the in-progress `vX.Y` can't collide with a planned `vX.Y+1.0` or later, so version order and due order still agree. A milestone a person made by hand with the next patch title is joined, with the due date the person gave it: the operator doesn't move dates people set. |
| F4 | A new patch's due date | Halfway between the due date of the milestone it follows (the release in progress, or the last patch skipped in F2) and the due date of the next open version milestone after that one. When nothing is open after it, today's P4 applies (seven days from now, or an hour after the latest open release, whichever is later). | The due date only orders releases (R6). Halfway between them puts the patch right after the release in progress and before the planned ones, and leaves room for another patch after it. |
| F5 | What counts as a fix | A plan issue, not `kind/by-hand`, labelled `kind/bug` or `kind/test`. A taken suggestion whose answer in `plan/suggestions.yaml` says `fix: true`. Plan validation refuses `fix` on an answer that skips a suggestion. The planner's guide says to label test fixes `kind/test` and to mark a taken suggestion `fix: true` when it fixes a bug or adds or fixes a test. | The decision is still made from the plan alone, so a restart decides the same (P2). Reviewers' follow-ups carry only a title and text, and the reviewer's guide lives in pinned templates. The planner already reads each suggestion it answers, so it says which ones are fixes. |
| F6 | Mixed rounds | Each issue goes where it belongs: a fix to the patch slot, any other work to the round's minor milestone, made and dated as today (due after every open release). Taken suggestions go the same way. A milestone is made only when an issue goes into it (R7), so a round of only fixes makes no minor milestone. `RoundFixes` per Container is replaced by a per-issue and per-answer decision. | That is the maintainer's rule. One milestone per round could only be all-patch or all-minor, which is what pushed fixes behind features. |
| F7 | Restarts | A patch milestone a round makes carries its own marker, `<!-- c4-brief: NAME/ROUND fixes -->`, next to today's `<!-- c4-brief: NAME/ROUND -->` for the round's minor milestone, so a restart finds each and makes neither twice. A milestone the round joins gets no marker: the retry works out the same slot again, and the issues already filed are recorded and not filed again (#501). | The Git provider has no call to edit a milestone, and none is needed. If the release in progress is tagged between two tries, the retry puts the remaining fixes into the next patch, which is still right after the release in progress. |
| F8 | Tending rounds | An autopilot tending round places its issues the same way. | The same rule for every issue the Brief files (P5). |

Since [release-cadence.md](../release-cadence.md) (C5), F1 to F8 apply
only to a Brief whose `releaseCadence` is `every: Release`, and F2's patch
slot only there: under a cadence a round's fixes go into the release in
progress with the rest of its work, and the cut decides its version (C7).

#873 lets a Crew's free slots start ready issues of the next releases while
the release in progress waits on its chain, and still tags releases in order.
With F2 the patch is the next release in that order, so its fixes are the
first work those free slots take. The two plans don't touch the same code:
#873 changes `internal/model/crew.go`, this one `brief_release.go`, the
Brief's apply and the planner's guide.

## Steps

1. **The model works out the patch slot and what is a fix** (#883, `internal/model`).
   F1 to F5 and F7 as functions, unused by the controller until step 2:
   `PatchSlot(milestones, now)`, which joins an existing milestone (its ID) or
   gives a new title and due date; `IsFix(labels)` for `kind/bug` and
   `kind/test`; `SuggestionAnswer.Fix` read from `plan/suggestions.yaml` and
   refused on a skip; the patch marker functions next to today's. Done when
   table tests cover: no milestone at all (`v0.1.0` stays the first, as a
   minor); only closed milestones (the next patch after the newest); `v0.13.0`
   open with `v0.14.0` and `v0.15.0` planned (new `v0.13.1` due halfway
   between `v0.13.0` and `v0.14.0`); `v0.13.1` open and taking work (joined);
   `v0.13.1` open with a prepare issue (new `v0.13.2` between `v0.13.1` and
   `v0.14.0`); `v0.13.1` closed (`v0.13.2`); nothing after the release in
   progress (P4's date); `IsFix` on bug, test, other and by-hand issues;
   `fix: true` on a take parsed and on a skip refused; and `make check`
   passes.
2. **Rounds file each fix in the patch slot** (#884, `internal/controller`,
   `internal/model/planner.md`, `docs/design.md`). F5 to F8: the round's apply
   and a tending round place each issue and each taken suggestion of a
   released Container by F6, through `PatchSlot` and the round's minor
   milestone; the planner's guide on `kind/test` and `fix: true`; the design's
   *A Brief's apps are released* paragraph says the new rule, and
   brief-patch-releases.md's P1 and P2 point to this plan. Depends on step 1.
   Done when envtests on a fake Git host show: a mixed round for a Container
   with `v0.13.0` and `v0.14.0` open files its `kind/bug` issue in a new
   `v0.13.1` and its feature in `v0.15.0`; a round of only fixes with `v0.13.1`
   open files into it and makes no milestone; a taken suggestion with
   `fix: true` lands in the patch slot and one without in the minor
   milestone; a tending round does the same; a restart of each round makes
   no second milestone; and `make check` passes.

## What a human run of Kind would check

A Brief whose released Container has a release in progress and a planned one
after it: a prompt that takes a reviewer's test suggestion and a feature gets
the suggestion into the patch right after the release in progress and the
feature into the next minor release, and the Crew tags the patch before the
planned release.
