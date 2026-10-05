# The Crew files the prepare-release issue

**Goal:** A release's tag carries its version and changelog without a person
filing the "prepare vX" issue: once a release's work is merged, the Crew files
that issue in the release, its agents bump the version files and write the
changelog, and the release is tagged at that merge. GitHub issue #117, a
follow-up of [release-tags.md](../release-tags.md) (#78).

**Why:** `make release-version` in the release job refuses a tag that differs
from `package.json`, so today a person files and readies "Prepare vX" before the
last merge, or the tag the Crew makes fails its release run.

## The rule

- **Off by default.** `spec.prepareReleases: true` on the Crew turns it on,
  independently of `tagReleases`; without it the Crew behaves as today.
- A **prepare issue** is an issue labelled `kind/release` in the release's
  milestone, whoever filed it. The Crew's is titled `Prepare vX.Y.Z`, labelled
  `kind/release` and `status/ready`, and lists the release's merged issues.
- The Crew files one for the **first open release** (the one the Crew works on
  and tags first) when it is done (no open issue, no active Task on one of its
  issues), has **merged work** (a closed issue other than a prepare issue that a
  Task merged), and has **no prepare issue**, open or closed. Filing it makes the
  release not done; the Crew picks it up like any ready issue of that release,
  and once it merges the release is done again with its prepare issue merged
  last, so the tag goes at that merge.
- **One per release.** A prepare issue closed by hand, merged or not, is the
  person's call: the Crew files no other one, and tags (when `tagReleases` is
  on) as before.
- **Work merged after the preparation.** With `prepareReleases` on, a release
  whose newest merge is not a prepare issue, though one of its prepare issues
  merged, is left to a person (reason `MergedAfterPrepare`): its changelog
  misses that work. A person files another `kind/release` issue in the milestone,
  or tags it by hand.
- **An empty release** (no merged work) gets no prepare issue, so a milestone
  made ahead of its issues is not released on its own.
- **Reporting.** Filing is a Normal Event `PreparingRelease` and the log line
  `Filed release preparation`; a failure is logged, and with `tagReleases` on
  the `Released` condition says the Git host failed.

## The issue

Body, in the Standards' issue form, with an "Acceptance criteria" checklist so
it is ready as filed:

- what it is for: release vX.Y.Z's work is merged, and its tag goes at this
  issue's merge;
- the merged issues, `#N title`, newest last;
- criteria: every file that holds the version says X.Y.Z (`package.json` and
  its lockfile, when there is one), and `make release-version VERSION=vX.Y.Z`
  passes when the Makefile has that target; `CHANGELOG.md` has an X.Y.Z section
  covering the issues above, its "Unreleased" entries moved under it (the file
  is created in Keep a Changelog form when missing); nothing else changes.

## Steps

Each step is its own commit on the issue's branch, which lands as one pull
request that closes #117, as #78 did; filing stays off until step 4 adds the
switch.

1. This plan.
2. `internal/model/release.go`: `ReleaseMilestone` gains its issues' titles and
   which are prepare issues; `ReleaseFacts.Prepare`; `NextRelease` returns
   `File` (the prepare issue to file) before it looks at tags, and holds
   `MergedAfterPrepare`; `PrepareIssue` renders the title and body. Table tests.
3. `internal/gitprovider`: `MilestoneIssues` returns the issues (number, title,
   state, labels) instead of two lists of numbers. Tests with the fake Gitea.
4. API and controllers: `CrewSpec.PrepareReleases`; the Crew reads the release
   facts when either switch is on, ensures the `kind/release` label and files
   the issue, and tags only with `tagReleases`. Envtest. `make manifests
   generate`.
5. `docs/design.md` next to "The Crew tags a done release". `make check`, `make
   deploy`, then a test Container with a release milestone, one small issue and
   its Crew with both switches; evidence in `docs/screenshots/`.

## Done when

- `make check` passes.
- In the Kind cluster, a Crew with both switches files `Prepare vX.Y.Z` after
  the release's issue merges, its Task merges it, and the tag goes at that
  merge, with no person acting; the issue, the Event and the tag are saved in
  `docs/screenshots/`.
- A Crew without the switch behaves as today.
