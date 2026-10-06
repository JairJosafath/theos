# Roadmap

This file holds how C4 Factory releases, the outcomes it works towards, and what
1.0 means. The maintainer decided it on 2026-10-06 (#305). The live state of
each outcome, with its issues, is in the
[milestones](https://github.com/JairJosafath/c4factory/milestones); this file
doesn't copy their issue lists.

## Releases are a train

- The maintainer tags `vX.Y.Z` on origin/main when `feat` or `fix` changes have
  landed since the last tag and main is green. That happens at most once a
  day; a fix that a reporter is waiting for can go out the same day.
- Before 1.0, a `feat` bumps the minor version and a `fix` bumps the patch.
- A release never ships a half-built feature that users can see. If main
  already shows a feature (a UI box, a chart value) and the step that turns it
  on is still in progress, the release waits for that step. The only exception
  is a reporter waiting on the release: then it ships anyway, and its notes say
  what is a preview.
- Release notes use the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
  categories and name each milestone the release completes.

## Milestones are outcomes, not versions

Each milestone is a GitHub milestone with a *Done when*. Versions follow from
what lands, so no milestone is tied to a version number. Only work a user of
the release would notice gets a milestone; work on the relay, `hack/`, the
skills and the governance files doesn't.

| Milestone | Outcome | Target |
| --- | --- | --- |
| [Briefs: an idea in plain words](https://github.com/JairJosafath/c4factory/milestone/1) | the *Done when* of [docs/plans/done/brief.md](docs/plans/done/brief.md), with the published chart | 2026-10-09 |
| [Upgrades and release hygiene](https://github.com/JairJosafath/c4factory/milestone/2) | a device upgrades without losing work, and every release says what changed | 2026-10-14 |
| [1.0: a stable API](https://github.com/JairJosafath/c4factory/milestone/3) | the criteria below hold | when they hold |

## What 1.0 means

1.0 is a promise: what a device builds keeps working across upgrades. It needs
all of these:

- a written compatibility promise for `c4.factory.io`, and a served version
  past `v1alpha1` that existing objects convert to;
- every release checks the upgrade from the previous one;
- the agent fence closes its known gap: today the Claude token is readable
  inside the sandbox ([docs/design.md](docs/design.md), v2);
- the [OpenSSF Best Practices](https://www.bestpractices.dev/) passing
  criteria;
- a device builds and releases an app from a Brief with only the published
  release and [docs/getting-started.md](docs/getting-started.md).

Whether 1.0 is public (licence, repository visibility, public packages) is the
owner's decision, and it isn't on the roadmap until they make it.

## Later, not scheduled

All four come from [docs/design.md](docs/design.md):

- runtime health of the apps;
- git providers other than Gitea;
- short-lived Session tokens;
- a webhook that refuses an incompatible Container up front.

## No project board

There is no GitHub Project board. The relay keeps each issue's state in its
labels, so a board would be a second copy that drifts. The milestones and the
pinned *Maintainer report* issue already give the overview. A board would also
need the `project` scope on the token, and access is reserved to the owner. If
the owner wants a board to look at, the maintainer revisits this.
