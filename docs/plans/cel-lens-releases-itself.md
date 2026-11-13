# Plan: cel-lens releases from its own repository

The owner, 13 November 2026: "Continue work via theos on cel-lens." Written
by the session the owner ran that day. It follows
[cel-lens-in-a-cloud-session.md](cel-lens-in-a-cloud-session.md) and
[cel-lens-security-score.md](cel-lens-security-score.md).

## Where it stands (13 November, JairJosafath/cel-lens `main` at 01d4ec3)

The 9 and 10 November session built the owner's core request (cel-lens#6:
the line above every CEL rule, and *Give it a type* with `.cellens`) in 24
reviewed pull requests, and all of it is on GitHub `main`. Nobody can use
it:

- **No release carries it.** The newest releases are core v0.27.0 and the
  extension's v0.35.0, both of 4 November. The extension still pins engine
  v0.25.1 (`vscode/core-version.json`), which draws no line and knows none
  of the new commands.
- **Nothing can publish one.** Releases were built in the Gitea of the
  factory on the owner's PC and copied to GitHub by its operator, with the
  owner's GitHub token (`publishReleases`). That factory has published
  nothing since 4 November, and its Gitea is now 30 pull requests behind
  GitHub. A cloud session's factory has no GitHub token and may not be
  given one, and its container is reclaimed when the session idles.
- **The session's Gitea went with its container.** Its issues and pull
  requests are gone; the titles in the two plans above are what is left of
  the follow-ups. The source lost nothing: GitHub `main` held the same
  commits.

Still open on JairJosafath/cel-lens: #6 (unreleased, and its README picture
is missing) and the five requests #1 to #5.

## Goal

A person installs the extension from the releases page and sees the line
above their CEL rules, and a release never again waits for one machine.

Done when core v0.28.0 and the extension's v0.36.0 are on the releases page,
built and attested by the repository's own workflow, the extension pins that
engine, the README shows the line and *Give it a type*, and cel-lens#6 is
closed.

## Decisions

1. **A workflow of JairJosafath/cel-lens builds each release from `main`.**
   `release.yml`, run by hand with the part (`engine` or `vscode`) and the
   version, builds the head of `main`. It refuses a version the source there does not already name
   (the changelog's heading, and for the extension `package.json`), builds
   with the part's own `make dist`, creates the two tags the repository has
   always had (`engine/vX.Y.Z` and `core-vX.Y.Z`, or `vscode/vX.Y.Z` and
   `vscode-vX.Y.Z`) and the release with the files and the changelog's
   section as its notes.

   Why there: the repository's own token can do it, with no token of the
   owner's kept anywhere; any session, or the owner from a phone, can start
   it; and it survives whatever machine the factory runs on. Rejected: a
   GitHub token for the session's factory (the session may not hold one, and
   the operator would copy files nobody can trace to a build), and waiting
   for the owner's PC (the owner: "you have to do everything, I'm not doing
   anything manually").

2. **The files carry build provenance, since this workflow does build
   them.** cel-lens-security-score.md refused provenance for
   `sign-release.yml`, which only sees files someone else built. Here the
   workflow compiles them from the commit it tags, so it attests that
   (`actions/attest-build-provenance`, the attestation attached to the
   release as `<file>.intoto.jsonl`), and SECURITY.md gains the command that
   verifies it. Scorecard's Signed-Releases is 8 today and gives 10 for
   provenance.

3. **The release still gets its Sigstore signatures.** A release made with
   the workflow's own token starts no other workflow, so `sign-release.yml`
   would never run by itself; `release.yml` starts it for the tag.

4. **The factory still decides what a release holds.** The version, the
   changelog and the pin are a reviewed pull request in Gitea (*Prepare
   vX.Y.Z*), as before. Only the last step moves: who compiles and uploads.
   Gitea's own release workflow stays as it is and keeps building the
   Gitea release.

5. **The front page stays in the factory.** `release.yml` is a file of
   `public-repo/` in cel-lens-vscode, with tests beside the other workflows'
   tests, like `sign-release.yml`.

6. **When the owner's factory runs again,** its Crews' `publishReleases`
   for cel-lens must be off, or it fails on tags that exist. Its Gitea
   starts again from GitHub `main`, as this session's did.

## Steps

Each is an issue in the session's Gitea, built by an implementer, judged by
a separate reviewer that sees only the issue and the change, and checked by
CI there, then mirrored to JairJosafath/cel-lens.

| Step | Repository | What | Needs |
| --- | --- | --- | --- |
| 1 | cel-lens-vscode | `public-repo/.github/workflows/release.yml`, its tests, SECURITY.md's provenance command, CONTRIBUTING.md's release section | |
| 2 | cel-lens-core | Prepare v0.28.0 (`make release RELEASE=0.28.0`) | |
| 3 | GitHub | steps 1 and 2 on `main`; run `release.yml` for the engine | a merge to `main` |
| 4 | cel-lens-vscode | pin engine v0.28.0; the README's pictures of the line and *Give it a type* | step 3's release |
| 5 | cel-lens-vscode | Prepare v0.36.0 | step 4 |
| 6 | GitHub | steps 4 and 5 on `main`; run `release.yml` for the extension; close cel-lens#6 | a merge to `main` |

After that the requests go on in the owner's order, starting with cel-lens#1
(what a rule says about example objects, at the rule), each with its own
plan.

## Status

- **13 November, start.** The factory is being rebuilt in the session's
  container from `cloud-session/`. Nothing of the steps is built.
- **13 November, afternoon.** Steps 1 and 2 are built and approved in the
  session's Gitea: cel-lens-vscode#3 (`release.yml`, two review rounds; the
  second round made a new run finish a release whose tags a failed run
  left at the commit) and cel-lens-core#6 (Prepare v0.28.0, the wording of
  two lines fixed after the review). Both are on JairJosafath/cel-lens
  branch `claude/jolly-ritchie-onxx00` (d003b85, 80caee7). The merge in
  Gitea with the operator's account was refused to the session as a merge
  without review, so step 3 waits for the owner to allow merging, in
  Gitea and on GitHub. Follow-ups: cel-lens-vscode#4 (the review's three
  non-blocking findings).
- **13 November, evening.** GitHub's first checks of the release
  (JairJosafath/cel-lens#19) found two things no test here could: a new Go
  advisory (GO-2026-6859, fixed in Go 1.26.11; cel-lens-core#13) and a
  CodeQL alert on `release.yml`, whose `build` job checked out the commit
  named by an input and ran `make` (cache poisoning). The commit input is
  removed rather than the alert dismissed: a release is built from the head
  of `main`, right after its *Prepare* change merges. That also takes away
  the risk that `GITHUB_TOKEN` may not tag an earlier commit, and the
  provenance now names exactly the commit released.
- **13 November, 09:17 UTC. Step 3 is done.** JairJosafath/cel-lens#19
  merged (80c7315) with all eight checks green, and the first run of
  `release.yml` (run 43399180197) published
  [cel-lens core v0.28.0](https://github.com/JairJosafath/cel-lens/releases/tag/core-v0.28.0):
  both tags at the merge commit, six binaries and `checksums.txt`, each
  with its `.sigstore.json`, and `provenance.intoto.jsonl`; not marked
  latest. Checked here with cosign v3.1.4: `SECURITY.md`'s `verify-blob`
  command on `checksums.txt` and the provenance of the Linux binary both
  verify, and the binary matches `checksums.txt`. Not run: `gh
  attestation verify`, the command `SECURITY.md` gives for provenance.
  Step 4 is cel-lens-vscode#7.
