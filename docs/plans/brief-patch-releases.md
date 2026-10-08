# Plan: a Brief's apps get small releases after the first one

## Goal

After a Brief's app ships its first release, the Brief keeps lining up small
releases instead of one big one and a growing Later pile (#601, reported by
`gray-lenovo` for cel-lens). A round of fixes becomes a patch release, a
suggestion a prompt takes lands in the round's release, and no issue the Brief
files or takes for a released Container is left in no milestone.

This extends [done/brief-releases.md](done/brief-releases.md) (R6, R7); the
maintainer's decision on #601 settles the three rules.

## Decisions

| # | Topic | Decision | Why |
| --- | --- | --- | --- |
| P1 | Patch or minor | A round's release milestone for a Container is the next **patch** version after the newest `vMAJOR.MINOR.PATCH` milestone of the repository, open or closed (`v0.1.1` after `v0.1.0`), when the round's work for it is all fixes; otherwise the next minor version, as before. `v0.1.0` stays the first. | Fixes ship as soon as they are done, not with the next feature round, as the factory's own releases do (#515, #524). Bumping the newest version, not the last released one, never reuses a title and keeps versions in due-date order. |
| P2 | What is all fixes | Read from the plan alone: the round files at least one issue for the Container that isn't `kind/by-hand`, every such issue is labelled `kind/bug`, and the plan takes no reviewer's suggestion of it. A taken suggestion counts as new work: reviewers file improvements and ideas, unlabelled. The planner's guide says to label a fix `kind/bug`. | Deciding from the plan needs no extra reads and is the same on every try, so a restart makes the same milestone. The planner already writes labels. |
| P3 | Taken suggestions | A suggestion the round takes in a released Container goes into the round's release milestone (made then if the round files nothing else for it), not into no milestone. In a Container that isn't released it leaves Later for no milestone, as before (B11). | A taken suggestion is the round's work; leaving it in no milestone means no release ever lists it. |
| P4 | Due dates line up | A new release milestone is due seven days after it is made, or one hour after the latest due date among the repository's open version milestones, whichever is later. | Its title is the newest version plus one, so it must also be due last: a milestone a person made by hand with a later date (v0.1.3 in two weeks) would otherwise be released after it. |
| P5 | Tending rounds too | A tending round (autopilot) files a released Container's new issues in its round's release milestone like any round; until now it filed them in none. | The same rule for every issue the Brief files: no release would list them otherwise. |

Issues filed before releases were turned on are #599's.

## Steps

One pull request:

1. `docs/design.md`: the Brief releases rule says patch or minor, taken
   suggestions and due dates; the suggestions rule says where a taken one goes.
2. `internal/model`: `ReleaseMilestoneTitle(titles, fixes)`, `RoundFixes(plan)`
   and `ReleaseMilestoneDueAt(open, now)`, with table tests.
3. `internal/controller`: the round's release milestone takes the round's
   fixes and the due rule; `answerSuggestions` puts a taken suggestion of a
   released Container in it; a tending round knows its released Containers.
   Envtests.
4. `internal/model/planner.md`: label a fix `kind/bug`.

## Done when

- `internal/model` table tests: the next title is `v0.1.1` after `v0.1.0` for an
  all-fixes round and `v0.2.0` otherwise, and never an existing title, open or
  closed; `RoundFixes` per P2; the due date per P4.
- Envtests: a round of fixes for a released Container files them in `v0.1.1`;
  a taken suggestion of a released Container ends up in the round's release
  milestone; no issue the Brief files or takes for a released Container is
  left in no milestone.
- `make check` passes.
