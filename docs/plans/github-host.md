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

1. **A token is the operator's identity; a GitHub App is the agents'.**
   `spec.credentialsSecretRef` holds a `token` of an account that
   administers the owner's repositories, which the operator acts with, and
   the `appId` and `privateKey` of a GitHub App installed on the owner,
   which mints the agents' credentials. The App gives what the fence needs:
   an installation token for one repository, with named permissions, that
   expires in an hour and can be revoked. With a token alone the provider
   is Ready for scaffolding and issues, but AgentAccess is missing, and the
   GitProvider says so (`AgentAccess=False`, reason `NeedsGitHubApp`): a
   person's token never reaches a sandbox. This decision first read "a
   GitHub App is the operator's identity". Steps 2 and 4 found that one App
   can't be both: a ruleset tells accounts apart, not tokens, and nobody
   reviews the pull request they opened. An operator that is an App of its
   own, with a second App for the agents, would need no person's token at
   all; nobody has asked for that yet.
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
   | AgentAccess | an installation token scoped to one repository, revoked by `DELETE /installation/token` | no bot user: the commits' author is the agent's `@agents.theos.factory.io` address, as on Gitea; a token the operator no longer holds runs out instead of being revoked |
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
| 2. Pull requests, reviews, merges and statuses (done) | #75 | #74 | the GitHost contract passes whole; `Cannot` is empty or names what GitHub can't do, with why |
| 3. Issues (done) | #76 | #74 | the Issues contract passes, exclusive labels and refused closes emulated |
| 4. AgentAccess from a GitHub App (done) | #77 | #75 | the AgentAccess contract passes: a token for one repository, a push to main refused, revoked on removal; a provider with a token alone reports `NeedsGitHubApp` |
| 5. CI on GitHub Actions (done) | #78 | #75 | the CI contract passes with `JobRunners` in `Cannot`; `model.CI` has `githubActions`, and every Standard renders its workflow for both hosts |
| 6. Events | #79 | #74 | the Events contract passes: one organization hook, signed deliveries, a bad signature refused |
| 7. Registry | #80 | #74 | the Registry contract passes for images and npm; Go modules resolve from the repository; PyPI is in `Cannot` |
| 8. `provider: github` | #81 | #74 to #80 | the CRD takes it, `cmd/main.go` wires it, envtest scaffolds a Container, runs a Task to a merge and adopts a repository on the fake; the GitProvider's `AgentAccess` condition comes from `ports.AgentAccessState` and no Task starts without it; an agent's credential is minted again before it expires; the organization's `THEOS_CI_*` variables are set from the GitProvider; `TestHostNeutral`'s allowlist has no controller in it |
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

## What step 2 found

The GitHost contract passes whole against the fake, with nothing in
`Cannot`. Getting there settled these:

- **A branch rule is a ruleset and a check the adapter makes.** The ruleset
  `theos: <branch> (rule <digest>)` fences the branch: the rules `update`,
  `deletion` and `non_fast_forward`, bypassed by the repository's admin role
  alone, `pull_request` mode (a merge, never a push) unless the rule says
  `OperatorPushes`, then `always`. Tags get `theos: tags <pattern>` with
  `creation`, `update` and `deletion`. So nobody but the operator merges
  into the branch or pushes to it.
- **The checks and the protected paths are checked by `MergePullRequest`,**
  before it merges: no file of the pull request is under a protected path,
  and every status pattern has a check that passed on the head
  (`model.UnpassedChecks`, the same matching the controllers use).
  `ForceMergePullRequest` skips the check. GitHub can't hold either: a
  ruleset's required checks are exact names where ours are patterns
  (`check / *`), a path restriction belongs to push rulesets, which refuse
  the push of the branch itself and exist on some plans only, and the
  operator bypasses its own ruleset anyway. A person who administers the
  repository and merges in GitHub's own UI is held to neither; that is the
  owner's own doing, as it is on any host.
- **The rule is kept in the Actions variable `THEOS_BRANCH_RULES`, and the
  ruleset says which rule.** The adapter has to read the rule back at merge
  time, and a repository variable is the one free-form place GitHub's API
  gives a repository. It is not an administrator's alone: GitHub lets
  anyone with write access set a variable through the API. So the
  ruleset's name, which only an administrator writes, carries the first 32
  hex digits of the rule's SHA-256, and a variable whose rule is another,
  or is gone, refuses every merge into that branch until `ProtectBranch`
  runs again. Weakening the variable stops merges; it never loosens one.
  `ProtectBranch` writes the variable first, so a call cut short leaves the
  branch refusing merges, not unchecked.
- **A check run is a status.** Actions reports each job as a check run under
  the job's name, and a commit status is what anyone else reports, so
  `CombinedStatus` and `CommitStatuses` read both as one list by context: a
  run is pending until completed, then success for `success`, `neutral`
  and `skipped`, failure for anything else. A commit with no check has no
  state, where GitHub says `pending`.
- **`Behind` is asked of the comparison,** `compare/{base}...{head}`'s
  `behind_by`: `mergeable_state` says `behind` only on a branch whose
  protection wants branches up to date. `update-branch` answers 202 and
  moves the head a moment later; "no new commits on the base branch" (422)
  is no error, a conflict (422) is `ErrConflict`.
- **A draft release is found in the list.** `releases/tags/{tag}` finds
  published releases only, so `GetRelease` reads the list for a draft of
  the tag. `SetReleaseBody` of `""` makes no call: GitHub would empty the
  notes.
- **The source comes from the zipball,** which is `git archive`'s zip as
  Gitea's is, so both adapters read it with `git.ReadArchive`, moved out
  of the Gitea adapter. A merge deletes the head branch itself, and
  repositories are made with `delete_branch_on_merge`, whichever comes
  first.
- **For step 4: the operator and the agents can't be one App.** A ruleset
  tells actors apart, not tokens. An installation token minted for an
  agent, however few its permissions, is the same App as the operator's
  own, so a ruleset that lets the operator merge into main or make a `v*`
  tag lets the agent's token do it too: `contents: write`, which an agent
  needs to push its branch, is all either takes. Decision 1 and #77 have
  one App for both, and that can't hold the fence. What this step built
  fits the other way round: the operator acts with a token of an account
  that administers the repositories, whose admin role is the ruleset's
  bypass actor, and the App mints the agents' tokens, which are outside
  it. An operator that is itself an App needs `Integration` as the bypass
  actor and a second App for the agents. Step 4 settles which and rewrites
  decision 1. Either way the agents' tokens get `contents` and
  `pull_requests` and never `administration`.
- **The operator's token needs** `administration` (rulesets), `variables`
  of Actions, `contents`, `pull_requests`, `statuses` and `checks: read`,
  for step 9's list of what the operator's token and the agents' App are
  given.

What the fake encodes of GitHub's documented behaviour, by the page it is
taken from (docs.github.com/en/rest/...): `pulls/pulls` (422 for a second
pull request of a head and for a head with no commits, `mergeable` and the
size of the change on one pull request only, merge with `sha` answering 409
on a moved head and 405 on a conflict, `update-branch` 202 and its two
422s), `pulls/reviews` (422 when the author approves or asks for changes on
their own pull request, `PENDING` and `DISMISSED` states),
`commits/statuses` (the combined status pages its statuses, a description
is at most 140 characters), `checks/runs` (`filter=latest` by default),
`repos/rules` (the list carries no rules; 403 without administration),
`actions/variables` (201, 204, 409 for a name taken; collaborator access is
enough), `releases` (a draft is not found by its tag; an asset's bytes with
`Accept: application/octet-stream`) and `repos/contents` (the zipball's one
top directory). Not verified against github.com, which no agent holds a
credential for: that the admin role's `pull_request` bypass lets a merge
through the `update` rule, that variables are served for a repository
with Actions turned off, and that a fine-grained personal access token
reads the check runs of a private repository. Step 9's run on a real
organization is where those three show.

## What step 5 found

- **The CI contract passes** against the fake with its two `JobRunners`
  subtests in `Cannot`: GitHub's hosted runners take the jobs, so the
  adapter is no `ports.JobRunners` and no `ports.OwnerRunners`, and
  `RunnersOnline` says it doesn't know. Whoever needs a pool of
  just-in-time runners adds both then.
- **A run names a branch or a tag, never a ref.** GitHub's `head_branch` is
  the tag's name for the push of a tag. `TagRuns` asks for the push runs
  of that name at the commit and says `refs/tags/<tag>`; `CommitRuns` says
  `refs/heads/<name>` for every push, a tag's too, and `refs/pull/<n>/head`
  for a pull request's. The one reader of a run's ref compares it with a
  branch (`crew_full_check.go`).
- **A check's page is its job's:** `.../actions/runs/<run>/job/<job>`, and
  the log is `actions/jobs/<job>/logs`, a redirect the client follows
  without the token. GitHub gives no log of a job that still runs, where
  Gitea gives what was sent so far: `ErrNoCILog` until the job ends.
- **The row.** `githubActions`: the required contexts are `check*`, as on
  Gitea, but what reports is the job's own name with no workflow or event
  in it, so a Standard's jobs there are named `check` and `check (full)`.
  All of `.github/` is protected, not the workflows alone: the pull
  request template and the code owners are there too. It has no fallback
  paths; GitHub reads workflows from one place.
- An agent's token has no `actions` permission: it can't run a job again.
- **Six Standards render for both systems:** aws-standard,
  go-operator-standard, go-standard, hello-standard, python-standard and
  ts-standard keep `.theos/ci/githubActions/` beside `giteaActions/`, with
  the same `make check` and the same issue and pull request templates
  (`TestStandardsRenderForEveryCISystem`). Their tags moved a minor
  version; what they render for Gitea Actions is byte for byte what it
  was. factory-self keeps one system: the factory's own repository has
  workflows of its own on GitHub, so adopting it there names those checks
  (`spec.checkContexts`) and takes no workflow from a Standard.
- **GitHub's runners carry none of the factory's toolchain,** and a
  Standard's `make check` calls its tools from the `PATH`, as it does in an
  agent's sandbox and on a Gitea runner. So the job runs in the toolchain
  image, named by an Actions variable of the organization:
  `THEOS_CI_IMAGE_GO` for the Standards that run on the `go` label on
  Gitea, `THEOS_CI_IMAGE_BASE` for those on `base`, and on the runner
  `THEOS_CI_RUNS_ON` names, `ubuntu-latest` without one. Nothing of that
  is in the file. A job whose image isn't named fails saying so. Step 8
  has the operator set the three from the GitProvider (`ports.OwnerActions`,
  which the adapter gets then), the images defaulting to the release's own
  runner images; those must be ones the organization's jobs can pull.
- **The checkout is `actions/checkout` pinned to a commit,** the one the
  factory's own workflows pin. The workflow asks for `contents: read` and
  nothing else, and a newer push ends the run of an older one on the same
  pull request.
- **No registry step yet.** The Gitea workflows fetch the factory's own
  packages from the owner's registry before `make check`. Where those come
  from on GitHub is step 7's, so a Container that depends on another of
  the project's packages has no check that passes on GitHub until then.

The fake encodes `actions/workflow-runs` (the `head_sha`, `event` and
`branch` filters; a re-run of failed jobs is a new attempt, refused while
the run is not completed) and `actions/workflow-jobs` (`filter=latest`; a
job's log by a redirect, 404 without one). Not verified against github.com:
that `branch=<tag>` selects the push of a tag. And no Standard's workflow
has run on GitHub Actions: they render, parse and name the check the row
requires, and whether a job with an empty `container:` reaches its first
step, to say which variable is missing, is for step 9's run to show.

## What step 4 found

The AgentAccess contract passes against the fake, with nothing in `Cannot`.

- **Who is who.** Decision 1, rewritten: the operator is the Secret's
  `token`, the agents are a GitHub App's installation tokens. Nothing of
  step 2 changed for it: the ruleset's bypass actor is the admin role, the
  operator's account holds it and the App doesn't. The pull request an
  agent opens is the App's, so the operator's account may approve it, which
  GitHub refuses the account that opened it.
- **What a token is asked for.** One repository, and `contents: write`,
  `pull_requests: write`, `issues: read`. Never `workflows`,
  `administration` or `actions_variables`. A token GitHub answers with that
  may do more than was asked is revoked and never handed on.
- **What holds it.** Another repository answers 404. `main` and the `v*`
  tags are the rulesets'. A push that changes a file under
  `.github/workflows/` is refused by GitHub for want of the `workflows`
  permission, on any branch; that matters there, because a pull request's
  checks run the workflow of its own branch. The rest of `.github/` and
  the Standard's protected paths are the merge's own check
  (`MergePullRequest`). The rulesets, the kept rule and a check's status
  need permissions the token lacks.
- **A token is revoked with itself alone.** GitHub has no call that revokes
  another token, and no bot user to delete. So the adapter keeps the tokens
  it minted, in the operator's memory, by repository and agent:
  `EnsureAgentAccess` adds one and leaves the earlier ones to a run that
  may hold them, `RevokeOldAgentTokens` revokes all but the newest, and
  `RemoveAgentAccess` all of them. A token minted before the operator
  restarted is not among them and runs out within its hour.
- **An hour.** `ports.Credentials` has `Expires` now, zero for a token that
  lives until revoked. Step 8 makes the controller mint the agent's Secret
  again before that and gets the new token to a sandbox that is still
  running.
- **The provider's Secret reaches the adapter whole.** `ports.Config.Secret`
  is what the Secret holds beside the user and the token; the adapter reads
  `appId`, `privateKey` and, where it is given, `installationId` (else it
  asks GitHub for the installation that reaches the repository). Step 8's
  controller fills it.
- **The condition.** An adapter that can say whether it mints is a
  `ports.AgentAccessChecker`, and `ports.AgentAccessState` turns its answer
  into a condition's status, reason and message: `Available`,
  `NeedsGitHubApp` for a token alone or half an App, `GitHubAppInvalid` for
  an id or a key that can't be read. Step 8 sets the GitProvider's
  condition from it and starts no Task without it.
- **A refused deletion is no branch gone.** `DeleteBranch` read every 422 as
  "Reference does not exist"; a ruleset's refusal is a 422 too. It reads
  the message now.
- **The fake holds everyone to the rulesets now,** the operator too: a push
  in each repository's `pre-receive` hook, a merge and a ref over REST in
  the handler. Step 2's tests pass unchanged against it: the operator's
  merges go through by the `pull_request` bypass, its pushes only where the
  rule says `OperatorPushes`.
- **For step 7 (#80).** An agent's token reaches one repository and has no
  `packages` permission. A Go module in a sibling private repository can't
  be fetched with it as a Git credential, and GitHub's npm registry and
  GHCR won't read for it. Step 7 decides what an agent reads the owner's
  packages with: a second, read-only token, or a proxy.

What the fake encodes, by the page it is taken from: `apps/apps` (a JSON
Web Token of at most ten minutes, signed by the App's key; an installation
by repository, organization or user; a token for `repositories` and
`permissions`, 422 for a repository the installation doesn't reach or a
permission the App lacks), `apps/installations` (`DELETE
/installation/token`), "About rulesets" (`GH013: Repository rule violations
found`), and the push refusal "refusing to allow a GitHub App to create or
update workflow ... without `workflows` permission". Not verified against
github.com, for step 9's run: that Git over HTTPS takes an installation
token with the agent's name as the user (GitHub documents `x-access-token`),
and the exact status of a merge and of a ref deletion that a ruleset
refuses over REST, which the fake answers 405 and 422.

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
