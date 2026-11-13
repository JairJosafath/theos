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
   version. It refuses a version the source on `main` does not already name
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
