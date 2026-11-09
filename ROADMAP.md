# Roadmap

This file holds how theos releases, the outcomes it works towards, and what
1.0 means. The relay's maintainer decided it on 2026-10-06 (#305); the relay
and that role were retired on 2026-10-28, when the factory began to develop
itself ([docs/plans/self-hosting.md](docs/plans/self-hosting.md)), so where
this file says "the relay" or "the maintainer" below, read the factory's own
Crew and the owner's sessions. The live state of
each outcome, with its issues, is in the
[milestones](https://github.com/JairJosafath/theos/milestones); this file
doesn't copy their issue lists.

## What the project is for

The owner's goal, stated on 2026-10-19: a software factory that uses rich
templates to create high-quality software and full applications. The relay's
picks follow it: work that changes what the templates build, or what a Brief
can build, comes before work on the factory's own machinery. The webshop is
the reference application and defines done
([docs/plans/fresh-start.md](docs/plans/fresh-start.md), #1426), so its path
comes first. The `priority/N` labels say it, 1 picked first:

- `priority/1`: something broken now (a red main, a dead relay, a failed
  release check).
- `priority/2`: the webshop's path, the issues in the milestone *Reference
  app: the webshop ships*, and a `kind/bug` whose fix is under `templates/`.
- `priority/3`: what makes development itself faster (the relay, `land`,
  `make check`, flakes, the scripts).
- `priority/4`: the rest of the goal's work: the templates (Frameworks,
  Standards, Connectors, Deployments, Compute) and their features, the apps
  they produce and their quality, packages, the Brief's planner, and deploys
  of the apps (the AWS and GitHub targets too).
- No label (counts as 5): everything else.
- `priority/8` or lower: a refactor of the factory's own components that
  changes nothing a user of the templates would notice, and, until the
  reference-app milestone closes, a new relay, landing or CI feature.

## Releases are a train

- Releases are small and frequent, so users move faster (the owner, #261).
  The maintainer tags `vX.Y.Z` on origin/main on any sweep where a `feat` or
  `fix` has landed since the last tag, main is green, and nothing half-built
  would ship. There is no daily cap, and it doesn't batch changes into a
  bigger release.
- Before 1.0, a `feat` bumps the minor version and a `fix` bumps the patch: a
  release with only `fix` changes since the last tag is a patch release.
- A `fix` doesn't wait for a milestone: it ships as a patch release on the
  next sweep after it lands.
- Each milestone is small, one plan's outcome, and normally completes in one
  minor release. 1.0 is reached through these steps, not as one big release.
- A release never ships a half-built feature that users can see. If main
  already shows a feature (a UI box, a chart value) and the step that turns it
  on is still in progress, the release waits for that step. Two exceptions
  ship anyway, and their notes say what is a preview: a reporter waiting on
  the release, and a security fix. A security fix is a change that fixes a
  reported vulnerability or one a scanner (govulncheck, Trivy, CodeQL) finds
  reachable; a dependency bump with no finding doesn't count.
- Release notes use the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
  categories and name each milestone the release completes.

## Milestones are outcomes, not versions

Each milestone is a GitHub milestone with a *Done when*. Versions follow from
what lands, so no milestone is tied to a version number. Only work a user of
the release would notice gets a milestone; work on the relay, `hack/`, the
skills and the governance files doesn't.
A plan gets its own milestone when its outcome is one a release's user would
notice; a plan whose steps are mostly relay, `hack/` or docs work has none, and
its user-visible steps join an existing milestone or none.

| Milestone | Outcome | State |
| --- | --- | --- |
| Briefs: an idea in plain words | a Brief becomes a merged plan and working Crews with the stand-in planner and no Claude token, on the published chart: the *Done when* of [docs/plans/done/brief.md](docs/plans/done/brief.md) | reached 2026-10-06, released in v0.5.0 |
| Upgrades and release hygiene | a device upgrades without losing work, and every release says what changed | reached 2026-10-06, released in v0.5.0 |
| Shared credentials | a second project gets the factory's Claude token and pull secret without anyone handing them over again (#427), on the published chart; tested by `make check` | reached 2026-10-07, released in v0.7.0 |
| App modules | one app's Go module is published to Gitea and fetched by another app's Crew and CI, and plans order the work with `uses:` (#430); tested by `make check` and `make test-gitea` | reached 2026-10-07, released in v0.7.0 |
| Briefs build in order | a Brief files its issues in the plan's order with Gitea dependencies, so no issue starts before what it builds on exists (#428); tested by `make check` and `make test-gitea` | reached 2026-10-07, released in v0.7.0 |
| Catalog: more kinds of app | the catalog offers react-vite, node-cli, python-standard, python-cli and a static-site deployment, each scaffolding a repo whose own checks pass (#399); tested by `make check` | reached 2026-10-07, released in v0.7.0 |
| Brief autopilot | a Brief tends its own failed issues within its heal window, asks by default, and shows what needs a person on its card (#486, #503-#506); tested by `make check` | reached 2026-10-07, released in v0.8.0 |
| Honest agents | agents never pass off invented evidence, open criteria become by-hand issues, and an agent that can't start says why (#467, #472-#475, #492); tested by `make check` | reached 2026-10-07, released in v0.7.0 |
| UI: one story, and what needs you | every level the UI shows, from a Brief's idea to Crews' Tasks and releases, tells its lifecycle in one order, and one *Needs you* list covers every item the CRDs flag for a person (#705): the *Done when* of [docs/plans/done/ui-lifecycle.md](docs/plans/done/ui-lifecycle.md); tested by `make check` | reached 2026-10-09, released in v0.18.0 |
| UI: act as the person signed in | a person steers the factory from the UI, as the ServiceAccount over port-forward and as themselves with OIDC behind an Ingress, and the cluster's RBAC decides (#706): the *Done when* of [docs/plans/done/ui-actions.md](docs/plans/done/ui-actions.md); tested by `make check` | reached 2026-10-09, released as a preview behind `ui.actions.enabled` in v0.16.0 and v0.17.0 |
| Reference app: the webshop ships | the webshop Brief in `config/samples/webshop` builds four Containers, a DataStore and a Kubernetes deploy on a Kind cluster from the published release, every gap the owner found in that build is closed, and a release ships it as the getting-started example: the *Done when* of [docs/plans/fresh-start.md](docs/plans/fresh-start.md) (#1426) | open |
| Open source: the repository goes public | a person who has never seen theos gets from the public README to a running factory and to a merged pull request of their own: the *Done when* of [docs/plans/open-source-launch.md](docs/plans/open-source-launch.md) (#45) | open |
| The open alternative to Warp's factory | [docs/comparison.md](docs/comparison.md) has no "not yet" in theos's column for something Warp ships (#46); the page is rechecked at each minor release | open |
| 1.0: a stable API | the criteria below hold; it holds only those, and the work towards them comes in the milestones above | when they hold |

## What 1.0 means

1.0 is a promise: what a device builds keeps working across upgrades. It needs
all of these:

- a written [compatibility promise](docs/design.md#compatibility) for
  `theos.factory.io`, and a served version past `v1alpha1`: every kind at
  `v1beta1`, `v1alpha1` dropped before the first release (#1963);
- every release checks the upgrade from the previous one;
- the agent fence closes its known gap: the Claude token never enters the
  sandbox ([docs/design.md](docs/design.md#the-fence)); met 2026-10-22, #1137;
- the [OpenSSF Best Practices](https://www.bestpractices.dev/) passing
  criteria ([docs/openssf.md](docs/openssf.md) has the gaps);
- a device builds and releases an app from a Brief with only the published
  release and [docs/getting-started.md](docs/getting-started.md).

The owner decided on 2026-11-09 to move towards open source. The licence,
the repository's visibility and the public packages stay the owner's acts;
[docs/plans/open-source-launch.md](docs/plans/open-source-launch.md) lists
them with the work around them (#45).

## Later, not scheduled

All four come from [docs/design.md](docs/design.md):

- runtime health of the apps beyond what a deploy target's tool reports;
- git providers other than Gitea (GitHub is now planned: #57);
- short-lived Session tokens;
- a webhook that refuses an incompatible Container up front.

## No project board

There is no GitHub Project board. The relay keeps each issue's state in its
labels, so a board would be a second copy that drifts. The milestones and the
pinned *Maintainer report* issue already give the overview. A board would also
need the `project` scope on the token, and access is reserved to the owner. If
the owner wants a board to look at, the maintainer revisits this.
