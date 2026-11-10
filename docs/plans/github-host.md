# GitHub as a Git host

GitHub issue #57, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-10 in a session the owner ran. It follows
[done/git-host-ports.md](done/git-host-ports.md), which cut the ports this
plan fills a second time.

## Goal

A `GitProvider` with `provider: github` makes theos work where most people's
code already is: Containers are scaffolded as repositories of the user's
organization on github.com (or a GitHub Enterprise Server), a Crew takes a
labelled issue there to a merged pull request, CI is GitHub Actions, and
`spec.adopt` takes over a repository that exists. Nothing moves into a Gitea,
and the chart's Gitea becomes optional.

## Where we stand (main at cf21ea3)

- `GitProvider.spec.provider` is an enum of one, `gitea`
  (`api/v1beta1/gitprovider_types.go`).
- The host is five ports, plus Events (`internal/ports`): GitHost (34
  methods), Issues (22), CI, AgentAccess, Registry, Events. Each has a fake
  (`internal/ports/fake`) and a contract suite (`internal/ports/contract`)
  that the fake passes in `make test` and the Gitea adapter in
  `make test-gitea`, against a real local Gitea.
- `internal/adapters/github` already talks to GitHub for other ends: App
  installation tokens (`github_app.go`), the release copy
  (`deployer.go`, `github_source.go`) and a Brief's requests
  (`github_issues.go`). `internal/adapters/gitlab/gitlabtest` and
  `matrix/matrixtest` are fakes of a remote API served over HTTP in tests.
- `model.HostCI("github")` is an error; `model.CI` has one row,
  `giteaActions`, and each Standard keeps its CI files under
  `.theos/ci/giteaActions/`.
- `TestHostNeutral` (`internal/ports/hostneutral_test.go`) counts every
  mention of Gitea outside its adapter against an allowlist.

## Decisions, and why

1. **A GitHub App is the operator's identity; a token is enough to try it.**
   The App gives what the fence needs: an installation token for one
   repository, with named permissions, that expires in an hour and can be
   revoked. `spec.credentialsSecretRef` holds the App's id and private key,
   or a `token` (a fine-grained personal access token). With a token alone
   the provider is Ready for scaffolding and issues, but AgentAccess is
   missing, and the GitProvider says so (`AgentAccess=False`, reason
   `NeedsGitHubApp`): a person's token never reaches a sandbox.
2. **`spec.url` names the instance.** `https://github.com` means the API at
   `api.github.com`; any other URL is a GitHub Enterprise Server, API under
   `/api/v3`. `spec.owner` is an organization, or the user the token is of.
3. **What GitHub lacks the adapter emulates, or the port makes optional,**
   as the design says of every host:

   | Port | GitHub gives | Emulated or optional |
   | --- | --- | --- |
   | GitHost | repositories, Git over HTTPS, refs, tags, releases, pull requests, reviews, squash merges with a head SHA, update-branch, commit statuses | branch rules as a repository ruleset; the protected paths stay the operator's own check of a pull request's files, which no host setting replaces |
   | Issues | issues, labels, milestones, comments, assignees, the dependencies API (`blocked_by`) | an exclusive scoped label: adding `status/x` removes the issue's other `status/*`; closing an issue with an open dependency is refused by the adapter (`ErrOpenDependencies`), since GitHub closes it |
   | CI | Actions runs of a commit, re-runs, job logs | a project's own runners (`JobRunners`) are optional: GitHub's hosted runners are the default; a pool of just-in-time runners is a later step |
   | AgentAccess | an installation token scoped to one repository, revoked by `DELETE /installation/token` | no bot user: the commits' author is the agent's `@agents.theos.factory.io` address, as on Gitea |
   | Registry | GHCR for images, npm on `npm.pkg.github.com` | Go modules come straight from the repository (`GOPRIVATE`, the token as a Git credential) with no proxy; PyPI is missing, so `spec.packages` must name an index or Python packages aren't published |
   | Events | an organization webhook, `X-Hub-Signature-256` | none |

4. **A fake GitHub in the repository, the real one for a human.**
   `internal/adapters/github/githubtest` serves the REST endpoints the
   adapter calls and Git's smart HTTP (go-git's server side) from memory.
   The contract suites run against it in `make test` on every change. The
   same suites run against a real organization in a tier a human starts
   (`make test-github`, with `THEOS_GITHUB_ORG` and a token), as
   `make test-gitea` does for Gitea; agents never hold a GitHub credential.
   A fake drifts from the real API, so each step's pull request says which
   documented behaviour the fake encodes, with the link.
5. **GitHub Actions is a second row of `model.CI`,** `githubActions`:
   workflows under `.github/workflows/`, the check context of its job, and
   `.github/` among the paths no agent may change. Each Standard gets a
   `.theos/ci/githubActions/` beside `giteaActions/`; the scaffold already
   writes the files of the Container's CI system. The two differ in runner
   labels and little else, and a test renders both for every Standard.
6. **The controllers learn nothing about GitHub.** `cmd/main.go` picks the
   adapter by `Config.Kind`; where a controller branches on the host today,
   the allowlist of `TestHostNeutral` shrinks instead of gaining a second
   name.
7. **The order is the contract suites',** smallest first, each step a
   mergeable pull request that leaves `provider: github` unreachable until
   step 8 turns it on: nothing half-built ships.

## Steps

Each step is its own issue, names #57 and lands with `make check`.

| Step | Issue | Depends on | Done when |
| --- | --- | --- | --- |
| 1. The fake GitHub, and repositories, files and refs (done) | #74 | | `githubtest` serves repositories, Git over HTTP, refs and tags; the adapter passes the GitHost contract's repository, file, branch and tag subtests, the rest listed in `Cannot` |
| 2. Pull requests, reviews, merges and statuses | #75 | #74 | the GitHost contract passes whole; `Cannot` is empty or names what GitHub can't do, with why |
| 3. Issues (done) | #76 | #74 | the Issues contract passes, exclusive labels and refused closes emulated |
| 4. AgentAccess from a GitHub App | #77 | #75 | the AgentAccess contract passes: a token for one repository, a push to main refused, revoked on removal; a provider with a token alone reports `NeedsGitHubApp` |
| 5. CI on GitHub Actions | #78 | #75 | the CI contract passes with `JobRunners` in `Cannot`; `model.CI` has `githubActions`, and every Standard renders its workflow for both hosts |
| 6. Events | #79 | #74 | the Events contract passes: one organization hook, signed deliveries, a bad signature refused |
| 7. Registry | #80 | #74 | the Registry contract passes for images and npm; Go modules resolve from the repository; PyPI is in `Cannot` |
| 8. `provider: github` | #81 | #74 to #80 | the CRD takes it, `cmd/main.go` wires it, envtest scaffolds a Container, runs a Task to a merge and adopts a repository on the fake; `TestHostNeutral`'s allowlist has no controller in it |
| 9. The guide, and a run on the real thing | #82 | #81 | "Use the factory on your GitHub organization" in the guide, the App's permissions listed with why; a human runs `make test-github` and the first-app Crew on a test organization, with the output as evidence |

## What step 1 found

- The fake serves Git with Git's own `git-http-backend` through
  `net/http/cgi`, from bare repositories on disk: a push and a clone in the
  tests are real ones, and the fake's refs endpoints run `git` on the same
  repositories, so the REST and the Git views can't disagree. A machine
  whose Git lacks `git-http-backend` skips the tests.
- One endpoint answers three questions: `GET /repos/{owner}/{repo}/commits/{ref}`
  resolves a SHA, `heads/<branch>` and `tags/<tag>`, peels an annotated tag,
  and carries the tree, so `BranchCommit`, `HasBranch`, `GetTag` and
  `CommitTree` share it. An empty repository answers 409 there, a missing
  ref 422.
- An owner that is neither an organization the token sees nor the token's
  own account is refused before anything is made: falling back to
  `POST /user/repos` unasked would put the repository in the wrong account.
- "pushes the default branch" is in the contract's `Cannot` until step 2,
  because the subtest ends by pushing past a branch rule; the adapter's own
  test covers the pushes before that point.

## What step 3 found

- GitHub's dependencies API is `…/issues/{n}/dependencies/blocked_by`, takes
  the blocking issue's `issue_id` (its id, not its number) and works across
  repositories; adding one that is there answers 422, which the adapter
  tells apart by reading the list.
- A label's exclusive scope is kept as a note ending its description,
  " (one per issue)". `EnsureLabels` sets and clears only that note, so a
  description a person edited stays.
- `AddIssueLabel` of an exclusive label sets the issue's whole label list in
  one `PUT`: two writers at once leave one label of the scope, the last
  one's. The cost is that a label of another scope added in the same instant
  by someone else can be lost; GitHub has no call that does both.
- A milestone's ID in the port is GitHub's milestone number, which is what
  its API takes. GitHub refuses a second milestone of one title, where
  Gitea makes it; nothing in the contract relies on the second.
- Lists are read 100 a page, to the end, and fail past 5,000 entries
  instead of coming back cut short. Pull requests, which GitHub lists among
  the issues, are left out.

## Done when

Step 9 is closed: a person with an organization on GitHub and a cluster
follows the guide and gets a merged pull request from an issue, and
[docs/comparison.md](../comparison.md) says "GitHub" in theos's Git host row
without "not yet".
