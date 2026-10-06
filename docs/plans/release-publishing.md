# Plan: publish a Container's releases to a GitHub repository

## Goal

A device names, once, a GitHub repository where a Container's releases go, and
hands over a token for that repository only. From then on every release the
Crew tags also shows up there, with the same version, the same notes and the
same files the release run attached in Gitea (the `.vsix`, the CLI binaries,
`SHA256SUMS`). The factory only adds releases there: it never pushes a commit
and never changes a file. A publish that fails is visible on the Crew, with
the reason, until it succeeds.

Reported as #249 by `gray-lenovo`: their app cel-lens is built in a Kind
cluster on a laptop, so its releases live only in that cluster's Gitea, behind
a port-forward, and are gone with the cluster. The repository they named is
`github.com/JairJosafath/cel-lens`, private, holding only `idea.md`.

## Decisions

- **The operator publishes, not CI.** The Crew already watches the release
  runs (`status.lastRelease.run`); once they succeed, the operator copies the
  Gitea release to GitHub. The other way, a step in `gitea-release`'s workflow,
  is rejected for three reasons. The token would sit in a Gitea Actions secret,
  in the same job that runs `make dist`, which is code the agents write, so a
  Makefile could read it or push with it. Containers already scaffolded keep
  their workflow (scaffold once; only Standard files follow a ref bump), so
  cel-lens, scaffolded on v0.3.0, would never get it. And a workflow step
  can't say on the Crew why it failed beyond "the run failed". In the operator,
  the token never leaves the operator's pod.
- **Declared on the Crew,** next to `tagReleases` and `prepareReleases`, since
  the Crew is what makes a release and watches its run:

  ```yaml
  kind: Crew
  spec:
    tagReleases: true
    publishReleases:
      github:
        repository: JairJosafath/cel-lens   # owner/name on github.com
        tokenSecretRef: {name: cel-lens-github}  # key "token"
      tagPrefix: ""                          # optional, e.g. "cli-"
  ```

  It needs `tagReleases` (CEL: `!has(self.publishReleases) || self.tagReleases`),
  since only a release the Crew tagged is published. `repository` matches
  `^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`; the host is always `github.com` (GitHub
  Enterprise can be a `url` field later). Removing `publishReleases` stops
  publishing; what is already on GitHub stays.
- **Not a second git provider.** `GitProvider` is where repositories are
  created and agents work; this is a release target, with five calls. It lives
  in `internal/gitprovider` as its own small interface, `ReleaseTarget`, with a
  GitHub implementation (`github.go`) that has release calls only: find a
  release, create a draft, upload an asset, list assets, publish the draft. A
  test asserts every request it makes is under `/repos/{owner}/{repo}/releases`.
  The "Git providers other than Gitea" line of *Out of scope* stays true.
- **The credential is the user's.** The user makes a fine-grained personal
  access token with *Repository access: Only select repositories* set to that
  one repository and *Contents: Read and write* (GitHub asks for it to create
  releases; *Metadata: Read* comes with it), and stores it:
  `kubectl -n <project> create secret generic cel-lens-github --from-literal=token=...`
  (the `secrets` skill: a credential only the user has). The chart renders no
  Secret and takes no token. The token could push code; the factory's guarantee
  that it only adds releases is the client's surface, tested as above. A GitHub
  App, whose permissions can't reach code, can come later.
- **Same version, same notes, same files.** The GitHub tag is `tagPrefix` +
  the Crew's tag (`v0.2.0`, or `cli-v0.2.0` when two Containers share one
  repository), and the release is named after it. GitHub creates the tag at
  the default branch's head: a ref, no commit, no file. The notes are the
  Gitea release's body when a person wrote one; otherwise the operator writes
  them, the titles of the issues merged into the release's milestone
  (`- Title (#N)`, without links into the private Gitea), and sets that same
  body on the Gitea release first, so both read the same. The files are the
  Gitea release's assets, streamed from Gitea to GitHub with their size, never
  held whole in memory.
- **Idempotent through a draft and a marker.** The operator creates the GitHub
  release as a draft whose body ends with a hidden marker,
  `<!-- c4factory: <namespace>/<container> <tag> -->`, uploads each asset the
  draft doesn't have yet (same name and size counts as uploaded; a different
  size is deleted and uploaded again), then publishes the draft. A retry finds
  the draft by its marker (drafts aren't found by tag, so it lists the
  releases) and carries on where it stopped; nobody sees a release with half
  its files. A published release with the marker for this tag is done. A
  release or tag of that name without the marker is someone else's: the
  operator leaves it alone (`TagTaken`).
- **One release at a time, the last one.** Like the run watch, only
  `status.lastRelease` is published, after its runs `Succeeded`; a `Failed`
  run publishes nothing until a re-run turns it `Succeeded`. A release the Crew
  tags before the previous one was published leaves that one to a person: a
  Warning Event `ReleaseNotPublished` naming its tag. Once published, a release
  is not touched again; files a re-run replaces in Gitea are not re-sent.
- **Failure is on the Crew.** `status.lastRelease.publish` holds `state`
  (`Pending`, `Publishing`, `Published`, `Failed`), `url` (the GitHub release)
  and `message`. The condition `Published` says how the last release's publish
  went, with a reason: `Published`, `Waiting` (runs not succeeded yet),
  `SecretMissing`, `Unauthorized` (401, or 403 without rate limiting),
  `RepositoryNotFound` (404, also what GitHub says to a token that can't see a
  private repository), `EmptyRepository` (no default branch to tag),
  `NoReleaseFiles` (the Gitea release is missing or has no assets), `TagTaken`,
  `GitHubError` (5xx, rate limits, network). Each failure is a Warning Event
  `ReleasePublishFailed` and the log line `Release publish failed`, once per
  reason; success is a Normal Event `ReleasePublished` and the log line
  `Published release`. Every failure is retried on the next poll, so a fixed
  Secret or a repository made reachable is picked up without a restart.
- **The decision is a model rule.** `internal/model.NextPublishStep` takes the
  facts (spec set, run state, Secret present, the Gitea release and its
  assets, the GitHub release found by marker and its assets) and returns one
  step: wait, write the notes, create the draft, upload an asset, publish the
  draft, done, or fail with a reason. The controller runs one step per poll,
  like the Standard update.

## Steps

Each step is its own pull request and leaves main working; publishing is off
until step 4, since nothing reads `publishReleases` before it.

1. **The rule.** `internal/model`: `NextPublishStep`, the GitHub tag and the
   notes (`ReleaseNotes` from issue titles), the marker; table tests for every
   step and reason.
   *Done when:* `make check` passes with table tests covering each step and
   each failure reason above, including retry from every half-done state.
2. **Gitea's side.** `internal/gitprovider`: read a release by tag with its
   assets, open an asset's download as a stream with its size, set a
   release's body; local-Gitea tests (`make test-gitea`).
   *Done when:* `make test-gitea` reads a release and streams its asset
   against the local Gitea, and `make check` passes.
3. **GitHub's side.** `internal/gitprovider`: the `ReleaseTarget` interface
   and its GitHub implementation, tested against an `httptest` server with a
   generated token (`rand.Text()`): the calls, streaming upload with
   `Content-Length`, the mapping of 401, 403, 404, 422 and 5xx to reasons, and
   that every path is under `/releases`.
   *Done when:* `make check` passes with those tests, and the path test fails
   if a non-release call is added.
4. **The Crew publishes.** API: `spec.publishReleases`, its CEL rules,
   `status.lastRelease.publish`, the `Published` condition (`make manifests
   generate`); the controller runs `NextPublishStep` each poll, with Events
   and log lines; envtest with a fake GitHub (`httptest`) and Gitea; the
   sample and the docs: `docs/getting-started.md` gets *Publish releases to
   GitHub* (the token's settings, the Secret, the Crew field, how to read a
   failure).
   *Done when:* envtest shows a tagged release with a succeeded run ending
   `Published` on the fake GitHub with its notes and files, a missing Secret
   and a 404 ending `Failed` with their reasons and Events, and a retry after
   the Secret appears ending `Published`; the CEL rules are tested in
   `make validate-yaml`; `make check` passes.

Trying it on the real `JairJosafath/cel-lens` needs the reporter's token and a
release; that is the reporter's, on their device, once step 4 ships in a
release.

## Done when

The four steps are merged, and #249's reporter can follow *Publish releases to
GitHub* in `docs/getting-started.md` with nothing else to know.
