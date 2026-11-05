# Plan: ProRivals, the owner's two stalled apps as one theos project

Asked by the owner in a session: take the two projects that stalled,
[eprosportsv2](https://github.com/jairjosafath/eprosportsv2) and
[rival-me-2](https://github.com/jairjosafath/rival-me-2), blend them into one
idea, and build it with theos.

## Goal

The factory can build the blended app: a model a person applies, a Brief the
planner Agent turns into Crews and issues, and a Domain that tells those
Agents what the subject is about, so the third try is not the first two
again.

Done when `make check` is green with:

- `templates/esports-domain`, catalog entry `esports`, passing
  `TestDomainTemplatesHaveTheShape` and rendered in a `templateCombos` row;
- the chart installing it (`catalog.entries`, matched against
  `templates/versions.txt` by `make validate-yaml`);
- `config/samples/prorivals`: the namespace, the Project, the story and the
  Brief, each valid against the CRDs, and the overlay building with
  kustomize in `make validate-yaml`.

## Where they stand

- **eprosportsv2** (React, Create React App, Amplify GraphQL, 7 commits): a
  schema of 24 `@model` types (League, Club, Player, Tournament, Role, three
  kinds of membership, three kinds of join request, Post, Tag, five kinds of
  leaderboard and record, Stat, Reward, Transfer) under a public global auth
  rule marked "FOR TESTING ONLY", and pages for a user's and a club's
  dashboard. No rule says who may do what, nothing produces a leaderboard
  record, and there are no tests.
- **rival-me-2** (React, Vite, Amplify Gen 2, 1 commit): Amplify's Todo
  starter, unchanged. The name is the idea: challenge a rival.
- Neither has issues. Both stalled on the same things: a large data model
  before one loop worked end to end, no authorization, no tests, and
  everything bound to one vendor's backend.

## Decisions

1. **One idea, ProRivals.** An esports community where a player or a club
   challenges a rival to a match, both report the result, and confirmed
   results move ratings and leaderboards. Clubs keep rosters, sign and
   release players, and join leagues and tournaments; leagues post news.
   eprosports gives the structure (leagues, clubs, players, tournaments,
   leaderboards, transfers, requests, posts); rival-me gives the loop that
   makes people come back (the challenge). The challenge loop comes first:
   leagues and tournaments are built from challenges, never beside them.
2. **The model in theos's own catalog, nothing from Amplify.** Both stalled
   on Amplify's backend; the chart's in-cluster catalog builds the same app
   with tests from the first commit, and nothing leaves the cluster unless
   the owner names a DeployTarget. The Containers:
   - `arena-web`, `static-site`, `react-vite` with `ts-standard`: the app in
     the browser (both earlier tries were React);
   - `rivals-api`, `service`, `python-fastapi` with `python-standard`:
     players, clubs, memberships, challenges, matches, results, disputes,
     leagues, tournaments, transfers and posts, the one place their rules
     are enforced;
   - `ladder-worker`, `worker`, `python-cli` with `python-standard`: turns
     confirmed results into ratings and leaderboards, confirms a result its
     opponent leaves unanswered, and expires stale challenges. What the API
     owns it changes through the API, never in the API's tables, so the
     rules stay in one place (as the webshop's payments-worker goes through
     orders-api);
   - `rivals-db`, DataStore on `postgres`: a schema for the API and one for
     the ladder, which only the worker writes and the API reads.
   All on `small` and `k8s-manifests`. One API, not a service per entity:
   the earlier schema's 24 types are one consistency boundary (a transfer
   changes a roster, a membership and a leaderboard together), and splitting
   them would hand the Crews cross-service work before the first loop runs.
3. **Sign-in is an external OIDC provider.** `python-fastapi` verifies
   tokens from `OIDC_ISSUER`; the story names `identity-provider` as an
   External system and the factory builds nothing there. Which provider is
   the user's setting, not the story's.
4. **An `esports` Domain.** The mistakes eprosports was heading for are
   domain mistakes, not stack ones: anyone may write a result, a
   leaderboard stored instead of computed, a transfer that leaves two clubs
   holding one player. A Domain is the place theos gives agents that
   knowledge (docs/plans/domains.md). Topics: players, clubs and rosters;
   challenges and matches; results and disputes; ratings and leaderboards;
   leagues and tournaments; community and safety. Each with *Practices*,
   *Common mistakes*, *Controversies* (the choice and why) and *Sources*, no
   stack. The choices it makes: Glicko-2 ratings computed from confirmed
   results, two-sided result reports with a timed auto-confirm, a dispute a
   moderator decides, transfers as one atomic change, and a minimum age the
   project sets.
5. **The sample relies on the chart.** Like `first-app` and the Briefs,
   `config/samples/prorivals` names the catalog the chart installs (with
   `gitea.projects` including `prorivals`) and carries no `factory.yaml` of
   its own. The Brief is applied on its own, after the model: it needs the
   Agents.
6. **No DeployTarget in the sample.** Each Container's `k8s-manifests`
   repository holds its manifests; where the app runs is the owner's
   choice, a DeployTarget they add, as the webshop's
   `deploytarget-argocd.yaml` shows.

## Steps

1. `templates/esports-domain` with its `.theos/README.md`; `versions.txt`;
   the chart's `catalog.entries`; the `python worker on kubernetes` row of
   `templateCombos`; `docs/templates.md`.
2. `config/samples/prorivals`: `namespace.yaml`, `project.yaml`,
   `story.yaml`, `brief.yaml`, `kustomization.yaml`; the kustomize build in
   `hack/validate-yaml.sh`.
3. `make check`; the output in `docs/screenshots/prorivals-check.txt`.

What follows is the factory's, not this plan's: once the owner applies the
model and the Brief to a cluster, the planner files the first issues and the
Crews build them.
