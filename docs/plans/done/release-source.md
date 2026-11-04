# Plan: a published release brings its app's source

GitHub issue #902, reported by gray-lenovo on 12 October 2026. Agent:
otter-902.

## Goal

A Crew that publishes its releases to GitHub (`spec.publishReleases`) can
also put the app's source, as of the release tag, into a folder it names in
that same repository, and tag it in a shape it chooses (`engine/v0.6.1`, so
`go install github.com/JairJosafath/cel-lens/engine@v0.6.1` works). The
factory's own working files stay out. Two apps share one repository, each in
its own folder, and never overwrite each other. The release counts as
`Published` only once both its files and its source are there.

Today the reporter copies `engine/` and `vscode/` by hand after each release,
several times a day; a missed copy leaves the public source behind the
published binaries.

Done when the steps below are merged, and design.md, *A release can be
published to GitHub*, says what they built.

## Decisions

1. **One optional field, `publishReleases.source`.** Off unless set:

   ```yaml
   publishReleases:
     github: {repository: JairJosafath/cel-lens, tokenSecretRef: {name: cel-lens-github}}
     tagPrefix: core-
     latest: false
     source:
       path: engine          # the folder in the repository; "." is the whole repository
       tagPrefix: engine/    # optional; defaults to path + "/", "" for "."
   ```

   `path` is a relative folder (`^(\.|[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*)$`,
   at most 100 characters, no `.` or `..` segment inside it and no `.git`
   segment; CEL). The source tag is `source.tagPrefix` + the Crew's tag. Its
   default is what Go asks of a module in a subfolder (`engine/v0.6.1`), so
   the common case needs only `path`. `CrewPublishReleases` is inlined in a
   Brief's `spec.releases` entry, so a Brief gets the field with no work of
   its own; a CEL rule on `spec.releases` refuses two entries of one
   repository whose folders overlap. A prompt doesn't set it, just as it
   doesn't set the repository: the person sets it in the release form.
   Removing `source` stops publishing the source; what is on GitHub stays.

2. **The factory's own files are a list the model already keeps.** Left out
   of the published source: `model.AgentNeverPaths` and
   `model.PlatformProtectedPaths` together (today `AGENTS.md`,
   `docs/agents/`, `.theos.yaml`, `.devcontainer/`, `.gitea/`), as
   `model.FactoryOwnPaths`. They are exactly what the reporter named, and
   they are the paths the operator and the agents' contract own: a path added
   to either list later is left out too, with no second list to forget. A
   table test pins the five. Everything else the Container's repository holds
   at the tag is the app's and is published (`docs/style/`, the Makefile,
   README, LICENSE, `go.mod`). No per-Crew exclude list now; one can come
   when someone asks.

3. **Through GitHub's Git Data API, not a clone.** The operator reads the
   source at the tag from Gitea (the zip archive `PublishGoModule` already
   downloads to a temporary file, with each file's mode: plain, executable,
   symlink; the archive is what `git archive` makes, so the tag's
   `.gitattributes` `export-ignore` and `export-subst` apply, and a
   submodule, which the archive holds only as an empty folder, fails the
   read rather than vanish from the published tree; the archive is capped
   at 256 MiB, as the operator's `/tmp` is 1 GiB), and writes to GitHub with the same token and client as the
   releases: blobs, a tree, a commit, and the refs. Rejected: pushing with
   go-git, which needs a fetch of the public repository each release, and
   whose failures come back as text the `Published` reasons can't be read
   from; the REST client already maps 401, 403, 404, 422 and 5xx to reasons
   and is tested against `httptest`. The token needs no new permission:
   *Contents: Read and write* is what releases already asked for. The client
   test that every request is a release call widens to: under
   `/repos/{owner}/{repo}/releases`, under `/repos/{owner}/{repo}/git/`, or
   `GET /repos/{owner}/{repo}` (the default branch). Files go one blob each,
   streamed from the archive, so the source is never held whole in memory;
   one over GitHub's 100 MiB blob limit fails the publish (`SourceTooLarge`).

4. **One commit per release on the default branch, inside the folder
   only.** The new root tree is the branch head's root tree with only the
   folder's subtree replaced by the source (built whole, so files removed
   from the app disappear from the folder too); every other path is the
   head's, untouched. The commit's parent is the head, and the branch moves
   by a fast-forward, never a force. Its message is `<app name> <tag>:
   source` with a trailer `theos-Source: <namespace>/<container> <tag>`.
   The folder is the app's: whatever was in it before (the hand-made copies)
   is replaced on the first publish. History stays linear; two apps sharing
   the repository each add their own commits.

5. **Idempotent by tree, not by marker.** The model hashes the source the
   way git does (blob and tree SHA-1), so most decisions read only. The
   source tag already pointing at a commit whose folder tree is the source's
   is done; pointing at one whose folder differs is someone else's tag
   (`SourceTagTaken`), left alone. When the head's folder already is the
   source's tree (a retry after the commit, or a release with no source
   change), no commit is made and the tag goes on the head. Blobs the head's
   folder already has are not uploaded again. A ref update GitHub refuses as
   not a fast-forward is another app's commit landing first: it is no
   failure, and the next poll builds on the new head. Since each app only
   ever replaces its own folder, the race loses nothing.

6. **Folders never overlap.** Two Crews publishing source to one repository
   (owner/name compared case-insensitively, in any namespace) whose folders
   are equal, or one inside the other (`.` is inside nothing and holds
   everything), both fail with `FolderOverlaps`, naming the other Crew, and
   publish nothing until one moves: refusing both is deterministic, where
   first-come would depend on poll order. A Brief's entries are checked at
   admission (decision 1).

7. **Source first, then the release, on the same commit.** With `source`
   set, the publish steps are: read the source, publish it (blobs, tree,
   commit, branch, source tag), then the release as today, except that the
   draft is created with `target_commitish` set to the source commit, so the
   release's tag (`core-v0.6.1`) and the source tag (`engine/v0.6.1`) name
   the same commit and the release's *Source code* archive holds that app's
   source. A draft made before `source` was set is retargeted (a `PATCH` of
   the release) before it is published. When the source tag and the
   release tag are the same name, the release takes the existing tag. The
   release is published only after the source, so `Published` means both
   are out. A release already `Published` is not touched again: turning
   `source` on applies from the next release.

8. **What the Crew says.** `status.lastRelease.publish` gains `source`:
   `commit`, `tag` and `url` (the folder at the tag,
   `https://github.com/<repo>/tree/<tag>/<path>`), set once the source is
   out. `state` stays the whole publish's. New reasons of `Published`:
   `SourceTagTaken`, `FolderOverlaps`, `SourceTooLarge`, and
   `BranchProtected` (GitHub refuses the commit on a protected default
   branch; the message says to let the token's user push, or to drop the
   protection). The existing ones keep their meaning for the source calls
   (`Unauthorized`, `RepositoryNotFound`, `EmptyRepository`, `GitHubError`).
   A Gitea archive that fails to download is logged and tried on the next
   poll, as Gitea reads are today. Source out is a Normal Event
   `SourcePublished` and the log line `Published source`; a failure is the
   existing `ReleasePublishFailed`, once per reason.

9. **A Go module that won't install says so, and still publishes.** When the
   published folder has a `go.mod` whose module path isn't
   `github.com/<repo>` + `/<path>` (a `/vN` suffix allowed), or the source
   tag isn't `<path>/vX.Y.Z` (`vX.Y.Z` for `.`), `go install` from GitHub
   can't find it. The operator doesn't rewrite the app's module path (that
   would touch every import, and the Gitea registry publishes the path
   `go.mod` declares); it publishes and raises a Warning Event
   `SourceNotGoInstallable` once per release, with the path and tag Go
   expects, so an issue can fix the app. The release still counts as
   published.

10. **The decision is the model's.** `internal/model.NextPublishStep` gains
    the source steps, with the facts it needs (the source's files and modes,
    the head's folder tree, the source tag's commit and folder tree, the
    overlapping Crews), and stays one step per poll.

## Steps

Each step is its own pull request and leaves main working; nothing reads
`publishReleases.source` before step 4, which turns it on.

1. **The rule** (#911). `internal/model`: `FactoryOwnPaths`, filtering the
   source, git blob and tree hashing, the source tag and its default, folder
   overlap, the Go install check, and `NextPublishStep`'s source steps
   (decisions 2, 5, 6, 7, 9, 10).
   *Done when:* `make check` passes with table tests for each source step and
   reason, retries from every half-done state, the hashes matching
   `git hash-object` and `git mktree` for fixed inputs, and the five
   factory paths pinned.
2. **Gitea's side** (#913). `internal/gitprovider`: read a repository's files
   at a tag from the zip archive, as a stream of path, mode and content.
   *Done when:* `make test-gitea` reads a tagged repository with an
   executable and a symlink and gets their modes, and `make check` passes.
3. **GitHub's side** (#914). `internal/gitprovider`: the Git Data calls (the
   default branch, read a ref, a commit and a tree, create a blob streamed,
   a tree, a commit, a ref, and a fast-forward update) and a draft's target
   commit, against `httptest`, with the failures mapped to reasons (a
   non-fast-forward kept apart from a protected branch).
   *Done when:* `make check` passes with those tests, and the path test
   fails if a call outside decision 3's three paths is added.
4. **The Crew publishes source** (#915, depends on #911, #913 and #914). API:
   `publishReleases.source` and its CEL rules (Crew and Brief), the
   `status.lastRelease.publish.source` fields, the reasons and Events; the
   controller; envtest with a fake GitHub; `docs/getting-started.md`,
   *Publish releases to GitHub*, gets the source folder and the Go tag; the
   sample; design.md's bullet says it is built.
   *Done when:* envtest shows two Crews sharing one fake repository each
   publish their folder and tag with the other's folder untouched, a retry
   after a non-fast-forward ending `Published`, overlapping folders ending
   `FolderOverlaps` for both, a taken source tag ending `SourceTagTaken`, and
   the release's tag on the source commit; the CEL rules' failing cases are in
   `test/invalid`; `make check` passes.
5. **The UI** (#916, depends on #915). The release form (Crew and Brief) gets the
   source folder and tag prefix, with the default shown; the Crew page shows
   the source link and the new reasons in words.
   *Done when:* the UI tests cover the form's patch and the page's display,
   and `make check` passes.

What only the reporter's device shows: a real publish to
`JairJosafath/cel-lens` and `go install` of the engine from it, once step 4
ships in a release.
