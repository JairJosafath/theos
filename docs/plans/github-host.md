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
| 6. Events (done) | #79 | #74 | the Events contract passes: one organization hook, signed deliveries, a bad signature refused |
| 7. Registry (done) | #80 | #74 | the Registry contract passes for images and npm; Go modules resolve from the repository; PyPI is in `Cannot` |
| 8. `provider: github` | #81 | #74 to #80 | the CRD takes it, `cmd/main.go` wires it, envtest scaffolds a Container, runs a Task to a merge and adopts a repository on the fake; the GitProvider's `AgentAccess` condition comes from `ports.AgentAccessState` and no Task starts without it; an agent's credential is minted again before it expires; the organization's `THEOS_CI_*` variables are set from the GitProvider; the GitProvider's hook names its Containers' and Briefs' repositories (`ports.EventHook.Repositories`), for an owner that is a user's account; a Framework's Go module path is the repository's own where the host serves modules from repositories, `DecideModule` takes such a module as published, a run and a job get a credential that reads the project's other repositories, and the GitProvider's own registries reach a job (`ports.OwnerActions`); `TestHostNeutral`'s allowlist has no controller in it |
| 9. The guide, and a run on the real thing | #82 | #81 | "Use the factory on your GitHub organization" in the guide, the App's permissions listed with why; a human runs `make test-github` and the first-app Crew on a test organization, with the output as evidence |

## Step 8's plan (#81)

Written before the step is built; "What step 8 found" replaces it. The
adapter already is a whole `ports.Provider`; what is missing is around it.
Five pull requests, and the one that lets a GitProvider say `github` is
the last, so nothing reachable is half-built (decision 7).

**Part 1, what the controllers need of a host that is not shaped like the
first.** Host-neutral, and tested on the in-memory fake.

- **The provider's Secret reaches the adapter.** Every place that builds a
  `ports.Config` builds it through one function, which carries the
  Secret's keys beside the user name and the token (`ports.Config.Secret`).
- **The GitProvider says what its host can't do,** one condition a
  capability: `Events` is there; `AgentAccess` comes from
  `ports.AgentAccessState`, and the host's own runners and its Python
  index get one each. No Task starts on a GitProvider whose `AgentAccess`
  is False: it waits with the provider's reason.
- **A run starts with a credential that outlives it.** The agent's Secret
  keeps when its token ends (`ports.Credentials.Expires`). Before a run
  the operator mints again when the kept token would end before the run's
  time limit does. An Agent whose `timeoutMinutes` no credential of the
  host outlives starts no run, and says so: nothing has to reach a pod
  that is running.
- **The hook names the repositories.** The GitProvider's reconciler fills
  `ports.EventHook.Repositories` from its Containers and Briefs.

**Part 2, the Go module path follows the host.** `scaffold.Input` gets the
module path, as it has the npm scope: `theos.local/<project>/<container>`
on a host with a module proxy, the repository's own path on one that
serves modules from repositories (`Repository.GoPrivate`). The three
Frameworks that write a `go.mod` use it. `model.DecideModule` takes a
module its host serves as published.

**Part 3, CI on a host whose runners are its own.** `spec.ci` gets a
second member, `githubActions`: the runner a job runs on and the toolchain
images, which the chart fills from the release's own. The adapter becomes
a `ports.OwnerActions` (an organization's Actions variables and secrets),
and the Project's reconciler sets the organization's `THEOS_CI_*`
variables and the packages' variables and secrets from the GitProvider.

**Part 4, the controllers name no host.** Every file of
`internal/controller` that is not a test, and `cmd/main.go`, leaves
`TestHostNeutral`'s allowlist: what they call Gitea is the source host of
a release, and is named that.

**Part 5, the switch.** `spec.provider` takes `github`; `spec.url` may be
left out for it and means github.com; a CEL rule keeps each `spec.ci`
member to its host. `cmd/main.go` picks the adapter by the kind. envtest
runs the reconcilers against the real adapter on the fake GitHub: a
Container scaffolded with its workflow under `.github/workflows/`, a
Crew's issue taken to a merged pull request, a repository adopted, and a
provider with a token alone that scaffolds and starts no Task. The chart
installs with no Gitea and a `gitProviders` entry of `provider: github`.
docs/design.md names two hosts.

**What the issue asks that this step does differently, and why.**

- The issue has the Secret hold "a `token`, or a GitHub App's `appID` and
  `privateKey` (CEL: one or the other)". Step 4 found one identity can't
  be both the operator and the agents, so the Secret holds a `token`, and
  an App's `appId` and `privateKey` beside it for the agents. A CRD's
  rules can't read a Secret either. What a Secret lacks is a condition:
  `AgentAccess=False`, `NeedsGitHubApp`.
- "The Standard's files arriving as the operator's pull request" on
  adoption is #97, which is open: until it is built, a repository whose
  Standard files differ fails its adoption on GitHub as on every host.

**Left open after step 8.** An agent's token and a job's token each read
one repository. A Go module or a package of another private repository of
the owner needs a second, read-only credential and Git told which
repository takes which, which a `.netrc` can't say. Until someone builds
that, a Container on GitHub depends on the owner's public modules and on
the packages its own repository may read.

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
- **A Standard's own files name the system's folder, not `.gitea/`.** Its
  agent guides and the TypeScript Standards' Prettier ignore list said
  `.gitea/`; they say `{{ .CI.Dir }}` now, `model.CI`'s folder, so a
  repository on GitHub is told `.github/` is the platform's and where its
  pull request template is. Three guides became templates for it.
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

## What step 7 found

The Registry contract passes against the fake, with nothing in `Cannot`.
It has two subtests, both of Go modules. It has none for images, npm or
PyPI, so those are covered by the adapter's own tests against the fake's
registries (`TestImagesOnGitHub`, `TestPackagesOnGitHub`), as Gitea's are
by its own, and nothing of PyPI is in `Cannot`.

- **Where things are.** `Repository` names, for an owner on github.com:
  images at `ghcr.io/<owner>`, npm at `https://npm.pkg.github.com/` under
  the scope `@<owner>`, no Go proxy, and a Python index only when
  `spec.packages` names one. On an Enterprise Server the two registries
  are `containers.<host>` and `https://npm.<host>/`, which is how GitHub
  names them with subdomain isolation, and the container registry needs
  that anyway; an instance without it gives `spec.packages.npm.registry`.
- **The scope.** GitHub's npm registry takes a package under its owner's
  scope and no other, in lower case. So the scope defaults to `@<owner>`
  there, and a `spec.packages.npm.scope` that is another fails the
  provider with the reason, before anything is scaffolded under a name
  that could never be published. A registry that is not GitHub's takes any
  scope, and defaults to `@theos` as on every host.
- **Images** go through `internal/oci`: push the release's `*.oci.tar`,
  resolve a tag, copy by digest. GitHub links an image to a repository by
  the image's own `org.opencontainers.image.source` label and has no call
  that links one after the push, so the link is the build's to make:
  whatever builds a release's image on GitHub sets the label.
- **npm**: the publish document, with the operator's token or
  `spec.packages.npm`'s. A manifest that names no repository gets this
  one in the document, which is what links the package.
- **PyPI**: published where `spec.packages.pypi` says. Without one a wheel
  or an sdist is `Invalid`, with the reason that there is no index.
- **Go modules: nothing is uploaded.** GitHub has no module proxy, and `go`
  reads a module of github.com from its repository at the tag, so the tag
  is the publication. `PublishGoModule` checks what `go` would need: a
  `go.mod` at the tag, a version the path allows, and a path that is the
  repository's own (`github.com/<owner>/<name>`), since that is the only
  place `go` looks. It answers `Exists`, never `Published`. The contract's
  module subtest learned both: the path a host wants (`ModulePath`) and a
  host with nothing to upload (`NoUpload`). The path has no port: an
  Enterprise Server on a port of its own can serve no Go module.
- **A run fetches them with `GOPRIVATE`.** `ports.Repository.GoPrivate` is
  the prefix of the owner's module paths on a host that serves modules
  from repositories, `github.com/<owner>`. A sandbox there gets
  `GOPRIVATE` for it and no proxy of ours, and `go` fetches with the Git
  credential the agent already has in `~/.netrc`. A host with a module
  proxy keeps the loopback proxy (`TestBuildSandboxGoModules`).
- **A GitHub job finds the owner's packages.** The check workflow of the
  five Standards whose check installs packages has the step step 5 left
  out, the same in all five: Git's credential and `GOPRIVATE` for the
  owner's Go modules, `~/.npmrc` for the owner's scope on GitHub's
  registry, and a login for a Python index only when the GitProvider names
  one. The job's token may read packages (`packages: read`) and goes to
  GitHub alone: a registry the GitProvider names gets the GitProvider's
  token for it, or none. `TestGitHubJobsFindTheOwnersPackages` runs the
  step's script as a job would and reads back what it wrote. The five
  moved a minor version; what they render for Gitea Actions is unchanged.
- **The publishing code is shared.** The reading of a release's package
  files and the npm and PyPI protocols moved out of the Gitea adapter into
  `internal/adapters/packages`, as `git.ReadArchive` did in step 2. Each
  adapter reads the release and signs its own requests. `.golangci.yml`
  lists the adapters that stay out of Kubernetes by name, and the package
  is added to it. CodeQL read the moved code as new and found the npm
  publish document joined by hand around the tarball; it is marshalled
  whole now and cut where the tarball is streamed in, byte for byte what
  it was (`TestNpmPublishDocument`).
- **GitHub's registries take a classic personal access token and no
  other** kind of a person's token, and an installation token has no
  permission for packages. So the operator's token must be a classic one
  with `write:packages` for anything to be published, which narrows what
  steps 2 and 6 asked of it: the guide (step 9) says so, and names
  `spec.packages.npm`'s own token as the way to publish with another.
- **The operator's token goes to GitHub's own registries only.** A
  registry or an index the GitProvider names gets the token the
  GitProvider names for it, or none.
- **Left to step 8, and why.** A Framework's `go.mod` says
  `theos.local/<project>/<container>` today, which `PublishGoModule`
  reports `Invalid` on GitHub: the path must be the repository's, a
  scaffold value as the npm scope is. `DecideModule` must take a module
  its host serves as published, where it asks a public proxy today. An
  agent's token and a job's token each read one repository, so a module or
  a package of another private repository needs a credential that reads
  it. And the Actions variables and secrets that carry a GitProvider's own
  registry to a job are set only through `ports.OwnerActions`, which the
  adapter gets in step 8. All four need a GitProvider that reaches GitHub
  to be tested end to end.

The fake encodes `working-with-the-npm-registry` (a scoped name, in lower
case, under an account's scope; a Bearer token),
`working-with-the-container-registry` (Basic auth with a token as the
password; an Enterprise Server's registry at `containers.<host>`) and
`repos/contents` (a file at a ref, its bytes for the raw media type). Not
verified against github.com: that the container registry doesn't check the
user name sent with the token; that the npm registry answers 404 for a
package it doesn't have, 409 for a version it has and 200 or 201 for a
publish; that it links a package by the `repository` of the publish
document where the tarball's own manifest names none; that the contents
call answers a file's bytes at a commit when asked for the raw media type;
and that a job's token with `packages: read` installs a package linked to
its repository. No Standard's workflow has run on GitHub Actions.

## What step 6 found

The Events contract passes against the fake, with nothing in `Cannot`.

- **One webhook, the organization's.** `EnsureEventHook` reads the
  organization's webhooks, makes the one to the receiver's path or edits
  it, and removes the others with that path; a webhook to another path is
  somebody else's and stays. It asks for the nine events the issue lists
  (`issues`, `issue_comment`, `pull_request`, `pull_request_review`,
  `status`, `check_run`, `workflow_run`, `push`, `release`), as JSON, with
  the receiver's certificate checked. A branch or a tag made or deleted
  arrives as a `push`, so `create` and `delete` are not asked for.
- **The secret is set each time, and its fingerprint is in the URL.**
  GitHub shows `********` for a secret, so nothing can be compared. As on
  Gitea, the URL the webhook holds carries `?fingerprint=`, the first 12
  hex digits of the secret's SHA-256: a webhook whose URL is the one
  wanted has the secret wanted, and `changed` is true only for a webhook
  that was made or whose URL, so its secret, changed. GitHub's edit takes
  a new secret, where Gitea 1.27's does not, so a rotation edits the
  webhook and keeps its id and its list of deliveries. Every ensure, each
  10 minutes, sets the events, the content type and the secret again, so
  what somebody changed in the organization's settings is put back.
- **A user's account has no webhook of its own,** and the issue asks for
  the repository's there. The port could not say which repositories, so
  `ports.EventHook` has `Repositories` now: the owner's repositories the
  operator works in. A host whose owner holds the hook ignores it. For an
  owner that is the token's own account the adapter ensures the webhook on
  each of them that exists, and on no other repository of the account: a
  person's other repositories are none of the factory's. Step 8 fills it
  from the GitProvider's Containers and Briefs; until then a user's
  account has no webhook and the operator polls. A repository made after
  an ensure is hooked at the next. A webhook on a repository whose
  Container is gone stays, and what it delivers concerns nothing.
- **404 means two things on GitHub:** an owner that is no organization,
  and webhooks the token may not manage. The adapter asks who the token is
  to tell them apart, and reads the repository for a user's account, so
  the error names what to grant instead of saying "Not Found".
- **What a delivery concerns:**

  | Event | Number | Branch |
  | --- | --- | --- |
  | `issues`, `issue_comment` | the issue's, or the pull request's a comment is on | |
  | `pull_request`, `pull_request_review` | the pull request's | its head, unless the head is in a fork |
  | `push` | | the branch; none for a tag |
  | `check_run` | | its check suite's `head_branch` |
  | `workflow_run` | | its `head_branch`, which is a tag's name for the run of a tag |
  | `status` | | the one branch whose newest commit it is on |
  | `release` | | |

  A status lists up to ten branches that hold its commit, and the commit
  "may or may not be the head of the branch": a status on a commit that is
  the newest of none of them, or of several, names no branch, and the
  receiver enqueues every unfinished Task of the repository, as it does
  for Gitea's status.
- **GitHub pings a new webhook,** and an organization's ping names no
  repository. `ParseEvent` reads a signed delivery without one as an Event
  with its kind alone: the receiver finds nothing to reconcile, answers
  202 and counts it as verified. So the GitProvider's `Events` says
  `Receiving` as soon as GitHub reaches the receiver, before anything
  happens in a repository, and GitHub's page of the webhook shows a
  delivery that arrived.
- **The signature is `X-Hub-Signature-256`,** `sha256=` and the hex
  HMAC-SHA256 of the body. `TestParseEventSignature` checks it against the
  secret, the payload and the signature GitHub's documentation gives to
  test a receiver with. The SHA-1 header GitHub also sends is not read.
- **For step 9's guide.** The operator's token takes `admin:org_hook`, or,
  fine-grained, the organization's "Webhooks" permission to write; for a
  user's account `admin:repo_hook`, or the repository's "Webhooks". A
  cluster GitHub can't reach gets no deliveries: `Events` stays
  `Registered` and GitHub's page of the webhook lists the deliveries that
  failed. The two ways out are an ingress that serves the receiver's path
  (`/git-events/`) and nothing else, with `gitEvents.url` its public
  address, or `gitEvents` left off and polling alone. The webhook checks
  the receiver's certificate, so one GitHub doesn't trust fails every
  delivery; there is no setting that turns the check off.

`TestParseEvent` maps each event's payload as github.com/octokit/webhooks
records GitHub's deliveries (the examples GitHub's documentation showed),
cut down to what says what a delivery concerns; the adapter read the whole
examples to the same events while this was built. The fake encodes
`orgs/webhooks` and `repos/webhooks` (a name that must be `web`, a secret
shown as `********`, an edit whose config replaces the config whole, 404
for an owner that is no organization and for a token that may not manage
webhooks, the `repository_hooks` permission of an installation token) and
`webhooks/webhook-events-and-payloads` (the delivery's headers, a ping for
a new webhook, each payload's shape, a status's branches). It delivers
before the call that caused the event returns, where GitHub delivers a
moment later. Not verified against github.com: that a second webhook with
the URL of another is refused (422), which the pages don't say and the
adapter doesn't rely on; and that a classic token without the scope gets
404, not 403, for an organization's webhooks.

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
