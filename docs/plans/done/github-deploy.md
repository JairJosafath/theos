# Plan: publish a Container as open source on GitHub

GitHub issue #1070, filed by the owner (`source/dev`, priority/4). Agents:
otter-1070 (the plan), heron-1070 (the design change and the steps). It builds on the deploy model of [deploy.md](deploy.md)
(#1068), the release images of [images.md](../images.md) (#1069) and the two
rules of [github-deploy-state.md](github-deploy-state.md) (#1445: `State`
takes the wanted version; the `github` member's `tagPrefix` and `latest`).
It plans what `docs/plans/done/deploy.md` decision 7 left to #1070: the
source, the checks before publishing, images on GHCR, a GitHub App
credential, and what becomes of `Crew.spec.publishReleases`.

## Goal

"Publish this Container as open source on GitHub" is one DeployTarget with
the `github` member and one `spec.deploy` entry, for every Container type.
Each release ships the same way for all of them: the app's source at the
tag into the repository, a GitHub release of the tag with its notes and
files, and the release's image on GHCR when the Container builds one. The
tree goes out only when it carries a license and a README and holds no
secret the factory can recognise. The credential is a fine-grained token or
a GitHub App. The Crew's `publishReleases` keeps working and becomes one
code path with the member.

Done when every step below is merged: a Container of each type ships to a
`github` target through the deploy loop, verified by Go tests and envtest
against the fake GitHub the publish tests use; no real GitHub is touched.

## Where we stand

- The `github` member ships a release (tag, notes, files) through
  `internal/adapters/github` (`Ship` takes one write per call,
  `model.NextReleaseStep`). A shipment without files is `ArtifactMissing`,
  so today only `cli`, `extension` and `operator` (the `gitea-release`
  Deployment) can ship there.
- The Crew's `publishReleases.github` ships the same release through the
  same adapter, and its `source` commits the app's tree at the tag into a
  folder through GitHub's Git Data API (`crew_publish_source.go`,
  `gitprovider.SourceTarget`), one commit per release on the default
  branch, tagged `<path>/vX.Y.Z`, the factory's own files left out
  (`model.FactoryOwnPaths`, [release-source.md](release-source.md)).
  The Crew still reads the repository, the release and the source itself.
- `internal/oci.Copy` copies an image by digest from one registry to another
  with each side's credential, made for this plan (#1453); `status.image`
  holds the release's digest in the owner's registry (#1455);
  `Shipment.Image` is `<repository>@<digest>`.
- `#1485` and `#1486` (ready) give `State` the wanted version and the member
  `tagPrefix` (default: the Container's name and `-`) and `latest`.
- The credential is a fine-grained token under key `token`; the GitHub
  client takes an API URL (tests point it at `httptest`), so GitHub
  Enterprise needs only a field.

## Decisions

1. **The member ships three things, each by its own rule, for every type.**
   What a `github` target ships of a release, decided in `internal/model`
   (`GitHubShipment`, table-tested over the nine types and the
   Deployments):
   - **the source,** when the member's `source` is set (decision 2);
   - **a GitHub release of the tag,** always: the notes, and the files when
     the release has them (`*.oci.tar` left out, as today);
   - **the image,** when the Container publishes one (`status.image`
     `Published` for the version, `model.BuildsImage`) and the Secret holds
     a `packagesToken` (decision 5).

   A release that has no files is no longer `ArtifactMissing` when the
   source or the image goes out: the release is then the tag and its notes.
   A target that publishes neither for a Container whose release has no
   files fails `ArtifactMissing` with the message "nothing to publish: no
   release files, no source, no image", so a releases-only target stays
   honest. The member requirement table (`model.DeployMember`) keeps needing
   nothing from a Container: every type can be open source.

2. **The source is a snapshot per release, not the history.** The member
   gains `source` (`*DeployTargetGitHubSource`, the Crew's shape):
   - `path` (`*string`, the Crew's pattern and CEL): unset, it is the
     Container's name (`orders-cli/`), so any number of Containers share a
     repository with no setting, as `tagPrefix` defaults; `"."` is the whole
     repository, for the repository of one app. The reconciler fills it per
     Container, since the default needs the Container's name.
   - `tagPrefix` (`*string`): unset, the folder and `/` (`orders-cli/v1.0.0`,
     what `go install` looks for), `""` for `"."`, as the Crew's.
   Two Containers whose folders overlap on one target fail `FolderOverlaps`
   (today's rule, `model.SourceOverlaps`, now across the target's
   Containers), the second in list order, with both names in the message.

   Why a snapshot and not a mirror of history: the Container's history is
   the agents' commits, with Gitea issue numbers (`#12`) that would link to
   the wrong GitHub issues, review rounds, and the factory's own files in
   every commit; a secret committed and removed stays in history, and no
   check can clean a history before it goes. A snapshot is one commit per
   release whose message names the version and the app
   (`model.SourceMessage`), built from the tag's archive, so each check of
   decision 4 sees exactly what goes out. Why not both: nobody asked for
   history yet, and a `history: true` field can come later without changing
   what a snapshot target does. The published repository is for reading
   and `go install`, not for pull requests: the Crew works in Gitea.

   The ship sequence becomes: the draft release, the source commit and the
   draft retargeted at it (so the release tag names the published tree, as
   the Crew does today), the source tag, the files, the image, then publish.
   Each is one write per `Ship` call; repeating finds what is there (a blob,
   tree or ref already made, a file already uploaded, a digest already in
   GHCR) and takes the next step.

3. **One code path: the Crew's copy is a `github` ship.** The Crew
   reconciler builds a `ports.Target` from `publishReleases` (repository,
   `tagPrefix`, `latest`, `source.path`, `source.tagPrefix`, the token
   Secret's data) and ships with the same adapter the Container's deploy
   loop uses; `crew_publish_source.go` moves into the adapter and the model
   (`ReadSource` reads the archive from the Git host port; the writes are
   the adapter's). The Crew's `Published` condition and its reasons stay,
   fed by the ship's outcome; the publish tests pass unchanged, as #1161's
   did. `Crew.spec.publishReleases` stays valid and working (the
   compatibility promise forbids removing it in `v1alpha1`); the design
   calls it deprecated and gives the hand migration: a DeployTarget with the
   same repository and the same Secret (key `token`, unchanged), a
   `spec.deploy` entry on the Container, then the field removed. Both may
   exist at once: each finds the other's release by its marker and leaves
   it alone, so the second is a no-op reporting `Deployed`. `v1beta1`
   keeps the field, deprecated, and the version after it drops it (#1082,
   decision 2), whose conversion keeps a Crew's `publishReleases` readable
   and whose plan says how the objects move. A
   Brief's `releases[]` keeps inlining `CrewPublishReleases` and writing
   `publishReleases` on its Crews; a Brief naming DeployTargets is the
   planner follow-up [deploy.md](deploy.md) decision 2 put after
   #1070 to #1072, once all three members exist.

4. **Checks before the source goes out, each release.** A pure rule in
   `internal/model` (`PublishChecks(files) []PublishProblem`, table-tested)
   over the tree about to be published, after `FactoryOwnPaths` are left
   out, run before the first write of a version:
   - **a license:** `LICENSE`, `LICENSE.md`, `LICENSE.txt` or `COPYING` at
     the published folder's root, non-empty; else `LicenseMissing`;
   - **a README:** `README.md` or `README` at that root, non-empty; else
     `ReadmeMissing`;
   - **no secret the factory can recognise,** `SecretFound`:
     - a private key block (`-----BEGIN … PRIVATE KEY-----`);
     - a token with a known shape, a table of high-precision patterns with
       the issuer's name (GitHub `ghp_`, `gho_`, `ghs_`, `github_pat_`; AWS
       `AKIA`/`ASIA` access key ids; Slack `xox[abprs]-`; Google `AIza`;
       Stripe `sk_live_`; npm `npm_`; PyPI `pypi-`; an Anthropic `sk-ant-`),
       each with its own table case;
     - the literal value of a credential the operator holds for that
       project: the GitProvider's token, the Agents' token Secret, the
       target's own Secret. The operator passes them as values to compare,
       never stores or logs them; the message names the file, the line and
       the rule, never the value.
   A problem fails the entry `Failed` with that reason, held until another
   version is wanted (a newer release or a pin), as a `TagTaken` is: a
   release the agents made is not published by itself with a secret in it.
   The Crew's `Published` gets the same reasons. The message lists every
   problem, files first, so one pass fixes them. A Warning Event per
   version and reason; *Needs you* already lists a `Failed` entry with its
   reason. Release files and images are not scanned: they are what `make
   dist` built from the scanned tree. Why own patterns and not gitleaks as a
   library: a hit stops a release with no person in the loop, so the list
   must be high-precision, short and tested per rule; gitleaks's hundreds of
   rules and its config are a dependency to weigh when a user asks for
   more. A line marked `c4factory:allow-secret` (a test fixture, a sample
   key) is skipped, and the message says so when nothing else is found.

5. **The image goes to GHCR by digest.** When decision 1 says the image
   ships, the adapter copies `Shipment.Image` with `oci.Copy` from the
   owner's registry (read with the GitProvider's token, as the deploy loop
   resolves a pin) to `ghcr.io/<owner>/<repository>/<container name>`,
   lower-cased, tagged with the version; on a target whose `tagPrefix` is
   `""` (the repository of one app) it is `ghcr.io/<owner>/<repository>`.
   The same digest already there is a no-op; another digest under the tag
   fails `TagTaken` naming the image. The write needs its own credential:
   GHCR takes no fine-grained token and no installation token, so the Secret
   holds `packagesToken`, a classic token with `write:packages` (and
   `read:packages`) for the owner, under its own key; without it no image
   is copied and the entry says nothing, since the target chose not to.
   `status.deploys[].image` (new, optional) reports `<repository>@<digest>`
   at the target once copied, and the release notes get a line naming it.
   The copy is a step of the ship sequence before publish, so a published
   release always has its image. The DeployTarget page shows which keys the
   Secret has (`token` or the App's, `packagesToken`), never a value.
   Landed in #1600: `model.GitHubImage` names the image, the adapter's
   `WithImageRegistry` points tests at an in-memory registry standing in
   for GHCR, and `State` resolves the tag to fill the image. Until #1599
   says which types ship files, a release with an image and no files still
   ships; that is the smallest piece of decision 1 this step needs.

6. **A GitHub App instead of a personal token.** The Secret holds either
   `token` or the three keys `appId`, `installationId` and `privateKey` (the
   App's PEM); `SecretIncomplete` names what is missing when it has neither
   set whole, and `token` wins when both are there. The adapter mints an
   installation token (an RS256 JWT of `appId` with the standard library's
   `crypto/rsa`, then `POST /app/installations/{id}/access_tokens`), keeps
   it per target until five minutes before it expires, and uses it as the
   token for every call; a refused mint is `CredentialRefused` with
   GitHub's message. The App needs *Contents: Read and write* on the one
   repository, the same the token asked for, since the source commit needs
   it; what the App gains over a token is a credential that belongs to the
   organisation, expires hourly, and is revoked by uninstalling. The fake
   GitHub serves the two endpoints and checks the JWT's signature against
   the test key. The Crew's `tokenSecretRef` takes the same keys through
   the one code path, so a Brief's Secret may be an App too. GHCR stays
   `packagesToken` (decision 5).

7. **Other hosts: room, not steps.** GitHub Enterprise is a `url` field on
   the `github` member (the client already takes an API URL) with
   `containers.<host>` for the images; GitLab is its own member and adapter
   on the same port, with its own release, source and registry calls. Both
   are filed `status/proposed` when this plan's steps land, not built here.

### How step 2 builds decision 3 (#1598)

Settled by otter-1598 when it took the step. The publish tests' messages
and reasons stay byte for byte, so the rules that word them move, not
change.

- **What the Crew keeps.** `model.NextPublishStep` keeps the Crew's own
  facts: the runs, the Secret, `FolderOverlaps` (across Crews), the Gitea
  release and its files, the repository's identity (`RepositoryRenamed`,
  `RepositoryReplaced`, `private`: a read, not a write) and the notes
  written to Gitea. Where it now says `ReadGitHub`, `ReadGitHubSource`, a
  source write or a release write, it says one action, `PublishShip`. On
  it the Crew calls `State` for the release: `Deployed` is `PublishDone` at
  its URL; else one `Ship`, and a failed one goes back in as
  `facts.Failure`, as today.
- **The ports.** `Shipment.Source` is the release's source
  (`*model.ReleaseSource`, read and hashed by the caller from the Git host
  port's `ReadSource`, `FactoryOwnPaths` left out by `model.NewSource`), and
  `Shipment.ReadSource` streams its files again for the blobs GitHub lacks.
  `DeployState.Source` is the published source (`*model.PublishedSource`),
  set once the source tag is there. `model.GitHubSettingSourcePath` and
  `GitHubSettingSourceTagPrefix` (`source.path`, `source.tagPrefix`; a
  `tagPrefix` key set to `""` is `""`, unset is the default) name the
  folder; no `source.path` publishes no source.
- **The adapter's sequence.** The source goes out first, then the draft on
  its commit, as the Crew does today (decision 2's order, draft first, ships
  the same end state; keeping the Crew's keeps its tests unchanged). Each
  `Ship` reads the release and the folder (`readGitHubSource` moves into the
  adapter), asks the model for the next step and takes one: the source
  commit (its blobs, trees, commit and the branch's fast-forward, one step
  as today: blobs are content-addressed, so a repeat after a failure finds
  or remakes them harmlessly), the source tag, then `NextReleaseStep`.
  `sourceStep` becomes `model.NextSourceStep(model.SourceShip, *GitHubSource)`
  so the adapter and nothing else calls it.
- **Failures keep their words.** A refusal the model words
  (`SourceTagTaken`, `SourceTooLarge`, the source's `EmptyRepository` and
  `NoReleaseFiles`) comes back from `Ship` as a `gitprovider.ReleaseError`
  with that reason and message; `model.PublishFailure` gains `Message`, and
  `failed` reports a failure with one verbatim. `portError` leaves those
  reasons alone, as it does `TagTaken`.
- **Events.** `SourcePublished` and `SourceNotGoInstallable` stay the
  Crew's: after a `Ship` it reads `State`, and the first pass that sees
  `Source` set where `status.lastRelease.publish.source` is not records it
  and raises them; the go install problem is `model.GoInstallProblem` of the
  module the Crew read.
- **Credentials.** The Crew passes its Secret's data as `Target.Secret`;
  `docs/architecture.md`'s two "until" clauses go when the Crew no longer
  builds a `ReleaseTarget` for anything but the repository read. That read
  moves behind the adapter too, as `Check` reporting the repository's
  identity, if it fits in the step; else it stays and the row says so.

## Steps

Each step is a GitHub issue that names #1070, lands on its own with `make
check`, and leaves main working. The `secrets` skill applies to every step
that names a Secret key: tests use generated values, and no fixture holds a
real one.

| Step | Issue | Waits on | How we know |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1070 (`Refs`) | | landed in `docs/plans/`; `docs/design.md`'s Deploys section says what a `github` target ships and marks `publishReleases` deprecated with the migration; `docs/architecture.md`'s Deployer row names step 2 as what ends its two "until" clauses, which stay true until it lands |
| 2. One code path: the Crew's copy ships through the `github` adapter, source included | #1598 | #1485, #1486 | `crew_publish_source.go`'s reads move to the model and the Git host port, its writes into the adapter; the Crew builds a `ports.Target` and calls `Ship` and `State`; the publish and source tests pass unchanged; `Target.Settings` names `source.path` and `source.tagPrefix`; `docs/architecture.md`'s Deployer row drops its two "until" clauses |
| 3. The member's `source`, and what each type ships | #1599 | #1598 | `DeployTargetGitHub.source` with its CEL and defaults (`make manifests`); the reconciler fills `path` per Container; `model.GitHubShipment` table-tested over all nine types, the Deployments and `source` set or not; `FolderOverlaps` across a target's Containers; the ship sequence with the source commit, retarget and tag; envtest: a `service` Container (no release files) with a `source` target reaches `Deployed` with the folder and both tags on the fake GitHub; one without `source` fails `ArtifactMissing` with the message above |
| 4. The checks before publishing | #1602 | #1599 | `model.PublishChecks` table-tested per rule (each pattern, the private key, a known value, the allow mark, the license and README names); the reasons on the entry and on `Published`; the Events; envtest: a tree with a `ghp_` line fails `SecretFound` naming the file and line and nothing is written to the fake GitHub; the next release with it removed ships |
| 5. The image on GHCR | #1600 | #1598, #1455 | `packagesToken`; `oci.Copy` as a ship step against the in-memory registry standing in for GHCR; the image name rule table-tested (`tagPrefix` `""` and not); `status.deploys[].image`; envtest: a `k8s-manifests` Container's published image reaches GHCR by digest and the entry reports it; a same-digest repeat copies nothing; another digest is `TagTaken`; no `packagesToken` copies nothing and ships |
| 6. The GitHub App credential | #1601 | #1598 | the three keys, `SecretIncomplete`'s message, the mint with the standard library, its cache, `CredentialRefused` on a refused mint; the fake GitHub's App endpoints verify the JWT; the contract suite runs the adapter with an App Secret too; the Crew's `tokenSecretRef` takes it; the chart and `docs/` say how to make the App and its Secret |
| 7. Samples, the UI and the migration | #1603 | #1599 to #1602 | the webshop sample has a `github` target with `source` and a Container entry; the DeployTarget page shows `source`, the Secret's keys and the image name; the Container page's deploy shows the image; the Crew page says `publishReleases` is deprecated and links the migration; `make ui-test`; `docs/screenshots/` shows a Container of each artifact kind `Deployed` |

## Out of scope

- A mirror of the Container's history; a per-target exclude list beyond
  `FactoryOwnPaths`.
- GitHub Enterprise and GitLab (decision 7), each its own proposed issue.
- Dropping `publishReleases` (the version after #1082's `v1beta1`) and a Brief naming
  DeployTargets (the planner follow-up of [deploy.md](deploy.md)).
- Scanning release files or images; signing them.
- Images on GHCR from an App's installation token, until GitHub takes one.
