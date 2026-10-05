# A release files bump issues in the Containers that bundle it

**Goal:** When the Crew tags a Container's release, each Container that bundles
it gets an issue asking to move to that release, so the next release down the
chain doesn't wait on a person to file it. The extension's `server.json` after a
server release is the case (extension #72 at tag `archive/product-coupled`).
GitHub issue #118, a follow-up of #78 ([release-tags.md](release-tags.md)).

## The rule

- **Bundling is said on the Relationship.** `spec.bundles: true` on a Relationship
  from Container A to Container B says A ships a pinned release of B. Only a
  Container to Container link may say it (CEL). Most links don't bundle: a
  client calling a service over the network follows the service's deploys, not
  its tags. The field may change; it touches no scaffold.
- **The tagging Crew files it.** A Crew with `tagReleases` that has tagged a
  release (`status.lastRelease`) files one issue per bundling Container, in that
  Container's repository: title `Bump <B> to <tag>`, labelled `kind/bump` and
  `status/ready`, with context, goal and an acceptance criteria checklist, so A's
  Crew picks it up as it stands. It goes in A's first open release (the dated
  `vX.Y.Z` milestone due first), so A's next release carries it; without one it
  has no milestone. A person can move it.
- **Only the newest release.** Bumps follow `status.lastRelease`; a later release
  takes over from one whose bumps never got filed, since bumping to the newer one
  is what A needs.
- **Once per Container and release.** `status.lastRelease.bumps` lists the
  Containers whose issue was filed and the issue's address. Before filing, the
  Crew looks for an open `kind/bump` issue in A with the marker
  `<!-- c4factory:bump <B> <tag> -->`, so a lost status write files no second
  issue.
- **Links at release time.** A Relationship created after the release was tagged
  files no bump for it: the bundler started from a later state. A bundler that
  isn't Ready yet is tried again on the next poll.
- **Reporting.** Each filed issue is a Normal Event `BumpFiled` and the log line
  `Filed a bump issue`; a failure is logged and retried on the next poll.

## Steps

One pull request, `Closes #118`.

1. This plan.
2. `internal/model/bump.go`: `BumpIssue` (title, body with the marker),
   `HasBumpMarker`, `BumpLabel`, and `BundlersToBump` (which links still need an
   issue). Table tests.
3. API: `RelationshipSpec.Bundles` with its CEL rule; `CrewRelease.Bumps`.
   `make manifests generate`.
4. Crew controller: after tagging, file the bumps. Envtest with the fake Git
   host: a bundling and a non-bundling link, a repeated poll, a link added after
   the release, a bundler not Ready.
5. `docs/design.md` next to "The Crew tags a done release". `make check`.
6. In the Kind cluster: a release of a test Container files the bump issue in
   the Container that bundles it. Evidence in `docs/screenshots/`.

## Done when

- `make check` passes.
- Envtest covers each rule above.
- In the Kind cluster a tagged release files the bump issue in the bundling
  Container's Gitea repository, and only there; the Crew's status, the Event and
  the issue are saved in `docs/screenshots/`.

## Out of scope

- Releases tagged by a person: the Crew doesn't watch tags it didn't make.
- Doing the bump: A's Crew and agents do it like any ready issue.
