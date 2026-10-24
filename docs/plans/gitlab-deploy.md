# Plan: publish a Container on GitLab

GitHub issue #1606 (`source/dev`, priority/4), from decision 7 of
[github-deploy.md](done/github-deploy.md) (#1070), which left room for GitLab
and built none of it. Agent: otter-1606 (the plan, the design change and the
steps).

## Goal

What a `github` target does for a Container, a `gitlab` target does on
gitlab.com or a self-hosted GitLab: each release ships as a GitLab release
of the tag with its notes and files, the app's source goes into a folder as
one checked commit per release, and the release's image is copied to the
project's container registry. One DeployTarget with the `gitlab` member and
one `spec.deploy` entry do it, for every Container type, with the same rules
the `github` member keeps (what ships, the snapshot, the checks before
publishing, the tag prefixes, `FolderOverlaps`, `TagTaken`).

Done when every step below is merged: `contract.Deployer` passes for the
GitLab adapter against a fake GitLab; envtest ships a Container of each
artifact kind (release files, source, image) to a `gitlab` target and it
reaches `Deployed`; `docs/design.md`'s Deploys section names the member
(this plan's pull request). No real GitLab is touched.

## Where we stand

- The `github` member ships through `internal/adapters/github` on
  `ports.Deployer`, one write per `Ship`. The rules it ships by are in
  `internal/model` and most are host-neutral already: `GitHubShipment`
  (what ships, by type), `ShipMarker` and `ReleaseBody` (finding our
  release), `NextSourceStep` with `SourceShip` (the snapshot, its commit and
  its tag), `PublishChecks`, `FactoryOwnPaths`, `SourceOverlaps`,
  `ReleaseTagPrefix`, `ImageRepository` (`GitHubImage` before step 4) and
  `GitHubImageNotes`.
- `NextReleaseStep` is GitHub's: a draft, retargeted, files uploaded to it,
  then published. GitLab has no drafts.
- The GitHub client and its fake are in `internal/gitprovider` and in
  `internal/controller`'s tests (`fakegithub*_test.go`); the adapters'
  contract tests call `contract.Deployer`.
- `internal/oci.Copy` copies an image by digest between registries with
  each side's credential.
- The UI reads members in `ui/src/model/deploys.ts`.

## Decisions

1. **The member's shape mirrors `github`'s, minus `latest`.**
   `DeployTargetSpec.gitlab` (`*DeployTargetGitLab`) has:
   - `url` (optional): a self-hosted GitLab's address,
     `https://host[:port]` with an optional path for an instance under a
     relative URL root (`https://example.com/gitlab`); unset is gitlab.com,
     and gitlab.com itself is refused by CEL, as `github`'s `url` refuses
     github.com. The API is `<url>/api/v4`.
   - `project` (required): the project's full path, `group/project` or
     deeper (`group/sub/project`), at most 255 characters, the pattern a
     GitLab namespace path allows. The API names it URL-encoded.
   - `tagPrefix` (`*string`): `github`'s field, rule and default
     (`model.ReleaseTagPrefix`, the Container's name and `-`).
   - `source`: `github`'s shape. Its Go type is renamed from
     `DeployTargetGitHubSource` to `DeployTargetSource`, shared by both
     members; the schema does not change.

   No `latest`: GitLab has no Latest flag to set; its latest release is the
   newest by date, and the factory leaves it so. A rollback reaches
   `Deployed` on the old release, as a `github` target with `latest: false`
   does. The exactly-one CEL rule on `DeployTargetSpec` gains `gitlab`, and
   `credentialsSecretRef` stays required for it.

2. **One credential: `token`, any GitLab access token.** The Secret holds
   `token`, sent as `PRIVATE-TOKEN`. The design names a project access token
   with the Maintainer role and the scopes `api`, `read_registry` and
   `write_registry` (Maintainer, since the source commit goes to the default
   branch and the tags may be protected). A group access token or a
   personal access token has the same shape and works with no change, so
   "which credential shapes" needs no second key. A deploy token is not
   taken: it can't call the REST API, so it can't make a release or a
   commit. Unlike GHCR, the project's registry takes the same token, so
   there is no `packagesToken`.

   `Check` reads the project (`GET /projects/:id`) and the token's own
   record (`GET /personal_access_tokens/self`, which project and group
   tokens answer too): a 401, a 403 or a 404 is `CredentialRefused` with
   GitLab's message ("can't see the project" for a 404), and a token
   without `api` is `CredentialRefused` naming the scope. A token without
   `write_registry` is ready: the target ships no image (decision 5).

3. **The release is the last write, since GitLab has no drafts.** The ship
   sequence is the source commit and its tag (decision 4), the release's
   files, the image (decision 5), then the release itself, made in one call
   (`POST /projects/:id/releases`: `tag_name`, `ref` the commit, the name of
   `model.ReleaseName`, the description of `model.ReleaseBody` with the
   marker and the image line, and `assets.links` to the files). A release
   that exists is out, so nothing else is written after it. A new pure rule,
   `model.NextGitLabStep`, table-tested, takes what the project holds and
   says the next write, as `NextReleaseStep` does for GitHub; `Ship` takes
   one write per call and repeating it finds what is there.
   - Ours is the release of the tag (`GET /projects/:id/releases/:tag`)
     whose description holds `model.ShipMarker`. A release of the tag
     without it, or the tag at another commit with no release, fails
     `TagTaken`, as on GitHub.
   - The files go to the project's generic package registry, package the
     Container's name, version the release's tag
     (`PUT /projects/:id/packages/generic/<name>/<tag>/<file>`), since a
     GitLab release holds links and no files, and the project upload
     endpoint is capped for attachments. The version is the tag and not the
     bare version so that two Containers of one name (`ns1/web` and
     `ns2/web`), with different tag prefixes, shipping one version to one
     project keep their files apart (#1873): the tag is already the one key
     that tells two apps' releases apart in a project, and `TagTaken` guards
     it; the default prefix is the Container's name and `-`, so prefixing
     the package *name* instead would read `web-web` on every default
     target. GitLab's rule for a version (`lib/gitlab/regex/packages.rb`:
     letters, digits, `.`, `_`, `+` and `-`, no `..`) refuses `/`, which
     `tagPrefix` allows (a tag folder, `cli/`), so the tag is encoded by
     `model.GitLabPackageVersion`: `_` is `__` and any other byte outside
     the rule is `_` and two hex digits (`cli/v1.0.0` is `cli_2fv1.0.0`); a
     tag within the rule, the default `<name>-vX.Y.Z` included, is itself. `*.oci.tar` is left out, as on
     GitHub. A file there with the same name and size is kept; another size
     is deleted and uploaded again. Each link is `link_type: package` with
     `direct_asset_path: /<file>`. The package registry being off fails the
     entry with GitLab's message.
   - What ships, by type, is `github`'s rule (`model.GitHubShipment`),
     renamed to a member-neutral `model.ReleaseShipment` whose image fact is
     "the target takes the image" (GitHub: a `packagesToken`; GitLab:
     decision 5). A release with no files, no source and no image is
     `ArtifactMissing`, with the same message.
   - `State` reports the wanted version's release, else the newest marked
     one, with `model.GitHubDeployState`'s phases (the release made is
     `Deployed`, a source or file step taken without it is `Deploying`) and
     the release's web URL.

4. **The source is `github`'s snapshot, written with the Commits API.**
   Every rule of github-deploy.md decision 2 and decision 4 holds unchanged:
   `model.NextSourceStep` decides, `PublishChecks` runs before the first
   write of a version (the target's own `token` among the values compared),
   `FactoryOwnPaths` are left out, `SourceOverlaps` runs across the target's
   Containers. Only the writes differ: the adapter reads the folder at the
   default branch (`GET /repository/tree?recursive=true`, paged; its `id`
   is the git blob hash the model compares) and writes the commit in one
   call (`POST /repository/commits` with `create`, `update` and `delete`
   actions for the paths that differ, `encoding: base64`), then the source
   tag (`POST /repository/tags` at that commit). A commit GitLab refuses for
   its size fails the entry with GitLab's message; splitting a snapshot
   over several commits is a later issue, for when someone meets it. The
   release's `ref` is the source commit, so the release tag names the
   published tree, as on GitHub.

5. **The image goes to the project's registry, by digest.** The registry
   path is the project's `container_registry_image_prefix` (from
   `GET /projects/:id`; it carries a self-hosted instance's registry host,
   which `url` can't tell), then `/<container name>` lower-cased, or the
   prefix alone when `tagPrefix` is `""`: `model.GitHubImage`'s rule, moved
   to a member-neutral name. The adapter copies `Shipment.Image` there with
   `oci.Copy`, tagged with the version, logged in as the token's user
   (`GET /user`) with the token. The same digest there copies nothing;
   another fails `TagTaken`, naming the image. The copy comes before the
   release, which names the image in its notes, and
   `status.deploys[].image` reports it. When the project's registry is off
   (`container_registry_access_level` `disabled`) or the token lacks
   `write_registry`, no image is copied and the release ships without one,
   as a `github` target without `packagesToken`; the DeployTarget page says
   which.

6. **A fake GitLab, shared by the adapter's and the controllers' tests.**
   The fake is an `httptest` server in `internal/adapters/gitlab/gitlabtest`
   (an importable package, so the controller envtest uses the same one),
   serving the calls above and refusing a wrong token. The image copy is
   tested against the in-memory registry the `github` tests use, through an
   option like `github.WithImageRegistry`. The adapter's test calls
   `contract.Deployer` with its target, a refused one and its files.

7. **Out of scope.** A Crew's `publishReleases` and a Brief's `releases[]`
   stay GitHub's; a Brief naming DeployTargets is the planner follow-up of
   deploy.md decision 2. The history mirror is no more wanted here than on
   GitHub. GitLab's own environments and CI/CD deploys are not this member:
   it publishes, and the Kubernetes and AWS members deploy.

## Steps

Each step is one issue, filed `status/ready`, naming #1606.

1. (#1803) The GitLab adapter, releases and files: the client in
   `internal/gitprovider`, `gitlabtest`, `model.NextGitLabStep` and
   `model.ReleaseShipment`, and `internal/adapters/gitlab` with `Check`,
   `Ship` and `State`. *Done when* `contract.Deployer` passes for the
   adapter against `gitlabtest`, and the step rule's table covers a fresh
   project, a file to replace, `TagTaken` and a release that is out.
2. (#1804) The `gitlab` member: the API fields and CEL (decision 1), the
   `DeployTargetSource` rename, `model.DeployMemberGitLab`'s row, the
   controllers' settings, `cmd/main.go`'s registration and a sample.
   *Done when* envtest ships a `cli` Container (the `gitea-release`
   Deployment, release files) to a `gitlab` target on `gitlabtest` and its
   entry reaches `Deployed`, every CEL rule has its failing case, and the
   sample passes `make validate-yaml`. Depends on step 1.
3. (#1805) The source on GitLab (decision 4). *Done when* envtest ships a Container
   with `source` set to a `gitlab` target, it reaches `Deployed` with the
   folder and the source tag in `gitlabtest`, and a tree with a secret fails
   `SecretFound`. Depends on step 2.
4. (#1806) The image on GitLab (decision 5). *Done when* envtest ships a Container
   that builds an image to a `gitlab` target, it reaches `Deployed` with
   `status.deploys[].image` in the project's registry, and a project with
   its registry off ships without one. Depends on step 2.
5. (#1807) The UI: the `gitlab` member in `ui/src/model/deploys.ts` and the
   DeployTarget page (where, the source, the image, the Secret's keys).
   *Done when* `make ui-test` passes with cases for a gitlab target, with
   and without `url` and `source`. Depends on step 2.
