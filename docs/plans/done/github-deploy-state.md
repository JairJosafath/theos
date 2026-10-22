# What runs at a GitHub target, and whose tags are whose

Parent issue: #1445, found while building #1162. Agent: otter-1445. It
settles two rules [deploy.md](deploy.md) left as known
limits of the DeployTarget's `github` member; #1070 builds the rest of the
member (source, history, GHCR, a GitHub App) on them.

## What was found

- **A pin to an older published release stays `Deploying`.** The member's
  `State` (`internal/adapters/github/deployer.go`) reports the app's marked
  release with the highest id. An entry pinned to `v1.0.0` after `v1.1.0`
  shipped (a rollback) reads `v1.1.0` back, so the reconciler ships `v1.0.0`
  again each poll; `Ship` does nothing, since that release is out, and the
  entry never reaches `Deployed`. A target that never takes Latest
  (`latest: false`, a library on a repository it shares) has the same bug.
- **Two Containers on one `github` target take the same tags.** The member
  has no tag prefix, so both ship `v0.1.0`; the second finds the first's
  release, not its marker, and fails `TagTaken` on that and every later
  version.

## Decisions

1. **`State` is told the version the entry wants.** GitHub releases stand
   side by side: every published one is out, and none "runs". So the port's
   `State(ctx, target, app)` becomes `State(ctx, target, app, version)`,
   `version` being the entry's wanted version (its pin, else the newest
   release), as the reconciler already knows it before it reads the target.
   An adapter whose target runs one version at a time (CloudFormation,
   Argo CD) ignores it and reports what runs, as now. Why not keep the port
   and read GitHub's Latest only: a `latest: false` target never has one,
   so a rollback there would stay `Deploying` still.
2. **What the `github` member reports**, in `internal/model` as one pure
   rule (`GitHubDeployState`), table-tested:
   - the app's marked release of `version` is a draft: `Deploying` at
     `version`;
   - it is published: `Deployed` and `Healthy` at `version`, with its page
     as the URL, except on a target that takes Latest (`latest` not
     `false`) when GitHub's Latest is another of **this app's** marked
     releases: then `Deploying`, and `Ship` makes the wanted release Latest
     (`PublishRelease(id, true)`, GitHub's `make_latest`, one write). A
     rollback on a target people install from moves the Latest badge back
     with it.
   - Latest held by a release of another app, or by one the factory didn't
     make, is left alone and doesn't keep the entry `Deploying`: two apps
     taking Latest on one repository would otherwise take it from each
     other each poll.
   - no marked release of `version`: the app's newest marked release by id,
     as today (`Deploying` for a draft, `Deployed` for a published one), so
     the status shows what is out while the reconciler ships the new one;
     none at all is the zero state.
   The release client gains `LatestRelease` (GitHub's
   `GET /repos/{owner}/{repo}/releases/latest`, nil on 404), and the fake
   GitHub serves it, keeping which release is Latest as `make_latest` sets
   it.
3. **Each Container's tags carry its name by default.** The `github`
   member gains the two fields the Crew's copy already has:
   - `tagPrefix` (`*string`, the Crew's pattern and length): unset, it is
     the Container's name and `-` (`orders-cli-v0.1.0`), so any number of
     Containers share a target with no setting; set, it is used as it is
     for every Container shipping there, `""` giving bare tags for the
     repository of one app. The reconciler fills the setting per Container,
     since the default needs the Container's name. Why a default that
     prefixes even a lone Container's tags: one that depends on how many
     Containers share the target would rename a Container's tags the day a
     second one arrives, and a shared prefix set by hand still fails
     `TagTaken` on the second Container, with the tag in the message.
   - `latest` (`*bool`, default true), as the Crew's `latest`.
   #1070 takes both as decided; the Crew's `publishReleases` keeps its own
   fields until #1070 moves it onto a DeployTarget.

## Steps

Each step is a GitHub issue naming #1445, landing on its own with
`make check`.

| Step | Issue | Depends on | Done when |
| --- | --- | --- | --- |
| 1. `State` takes the wanted version; the `github` member reports it and moves Latest back on a rollback | #1485 | | The port, its fake and contract suite take `version`; `model.GitHubDeployState` is table-tested for each case above; `LatestRelease` on the client and the fake GitHub; an envtest in `internal/controller/deployer_contract_test.go` against the fake GitHub where a pin to an older published release reports `Deployed` at that version, with Latest moved back, and on a `latest: false` target without a Latest write; `docs/plans/done/deploy.md`'s known limit points here |
| 2. The `github` member's `tagPrefix` and `latest` | #1486 | #1485 | The fields on `DeployTargetGitHub` with their CEL and defaults (`make manifests`), the reconciler filling the settings per Container; envtest: two Containers ship to one target and both reach `Deployed`, tagged `<name>-v…`; `tagPrefix: ""` gives bare tags; `docs/design.md`'s DeployTarget line names the fields; a comment on #1070 says they are decided |

## Done when

Both steps are closed: a rollback by pin reaches `Deployed` on a `github`
target with and without Latest, and two Containers share one.
