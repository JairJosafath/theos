# Plan: split the Git host into ports, with one contract suite per port

GitHub issue #1094. Agent: otter-1094.

## Goal

`gitprovider.Provider`, one 57-method interface with Gitea as its only
implementation, becomes the Git host's ports as
[design.md](../design.md#factory-components-ports-and-adapters) and
[architecture.md](../architecture.md#ports) decided them (#1093): GitHost,
Issues, CI, AgentAccess and Registry, each a narrow interface in
`internal/ports` with its shared types in its own file, one fake in
`internal/ports/fake` and one contract suite in `internal/ports/contract`.
Gitea moves to `internal/adapters/gitea`, the controllers take the narrowest
port they call, and nothing the controllers do changes: status, conditions,
log lines and the API calls made stay the same.

Done when the steps below have closed: the fake and Gitea run the same
contract suite per port, under `make test` and `make test-gitea`; every port
has its `var _ ports.X = ...` compile checks for Gitea and the fake; the
host-neutral guard passes with today's allowlist; `make check` and
`make test-gitea` pass on each step.

## Where we stand

- **One interface, one implementation.** `Provider`
  (`internal/gitprovider/gitprovider.go:65-248`) holds every role;
  `Client.Provider` (`gitprovider.go:269-276`) returns Gitea from a switch on
  `Config.Kind`. The controllers reach it through `GitClient`
  (`internal/controller/container_scaffold.go:29-32`), which also fetches
  templates.
- **Shared types live in Gitea's files:** `CheckStatus` and `PullRequest`
  (`gitea_agents.go`), `Issue` and `Label` (`gitea_issues.go`), `Milestone`,
  `WorkflowRun`, `Release` and `ReleaseAsset` (`gitea_releases.go`),
  `GoModule` (`gitea_gomodule.go`), `SourceFile` (`gitea_source.go`).
- **The GitHub clients share the package:** `ReleaseTarget` and `SourceTarget`
  (`github.go`, `github_source.go`, the Deployer's first adapter) and
  `IssueTracker` (`github_issues.go`, request intake), each with its own
  interface and its own fake in `internal/controller/fakegithub*_test.go`.
- **The fake is a test file of the controller package:**
  `internal/controller/fakegit_test.go`, 942 lines, used by 9 test files,
  some of which read its fields (`pushes`, `hangs`, the issue and release
  state) directly. Gitea's tests run under `make test-gitea`
  (`hack/testenv.mk:110`, `go test ./internal/gitprovider/ -run
  Integration`). Nothing checks that the two agree.
- **Layering:** `.golangci.yml` has an `adapters-stay-out-of-kubernetes` rule
  for `internal/gitprovider`; nothing yet for `internal/ports` or
  `internal/adapters`, and the controllers may import `gitprovider`.

## Decisions, and why

1. **Methods go to ports by what they do, as the Ports table lists them.**
   - *GitHost:* `Repository`, `EnsureRepository`, `History`,
     `PushInitialCommit`, `ReadFiles`, `PushBranch`, `PushDefaultBranch`,
     `ProtectBranch`, `ProtectTags`, `HasBranch`, `BranchCommit`,
     `CreateBranch`, `DeleteBranch`, `GetTag`, `CreateTag`, the pull request
     methods (`Open`, `Get`, `Comment`, `Merge`, `ForceMerge`, `Update`,
     `Files`, `Close`, `ReviewPullRequest`), `CombinedStatus`, `ReadSource`,
     and the host's releases (`GetRelease`, `OpenReleaseAsset`,
     `SetReleaseBody`): a release lives on the host beside its tag, and the
     CI only fills it.
   - *Issues:* labels, issues, comments, assignment, dependencies
     (`OpenBlockers`, `AddIssueDependency`) and milestones.
   - *CI:* `TagRuns`, `CommitRuns`, `RerunFailedJobs`, `CheckLog` (the log a
     status links to is the CI's), `RunnersOnline`, and the two that came
     after this plan, `RunnerOnline` and `RunJobs` (`WorkflowJob`).
   - *AgentAccess:* `EnsureAgentAccess`, `RemoveAgentAccess`,
     `RevokeOldAgentTokens`.
   - *Registry:* `PublishGoModule`, the optional capability the design names,
     and the package and image methods that came after this plan:
     `PublishReleasePackages`, `PublishImage`, `ImageDigest` and `CopyImage`,
     with their outcome types and `ErrNoRelease`. `Repository.GoProxy` stays
     a field of `Repository` in this issue.

   Two names changed in #1166, both because a port imports no adapter
   library: the Deployer's struct `ports.Registry` (a registry's address and
   login) became `ports.ImageRegistry`, so the port `Registry` could take
   the name the table gives it; and `CopyImage` takes a `ports.ImageRegistry`
   where it took an `oci.Registry`, the Gitea adapter and the controllers'
   fake converting to `oci.Registry` themselves. The tests that name the
   struct were renamed with it; the step's "no test changed" predates
   `CopyImage` (#1590).

   `Provider` stays, as the union of the five, for `GitClient` and
   `cmd/main.go`; helpers take the narrowest one or an inline union of two.
2. **The method names and signatures stay.** The design's "`Merge` takes
   whether to bypass" is met by the pair `MergePullRequest` and
   `ForceMergePullRequest`; both are GitHost's and its contract covers both.
   Renaming them would touch every caller for no change in what they do; the
   second host (#1088) may fold them if its adapter wants one method.
3. **`Label.Exclusive` stays in the port, as port semantics:** adding a label
   removes the issue's others of its scope, and an adapter whose host lacks
   exclusive labels does it itself (design.md's table). Its doc comment says
   so. No field is Gitea-only once that is written.
4. **Shared types move to `internal/ports`, and `gitprovider` keeps aliases**
   (`type Label = ports.Label`) while the controllers still import it, so the
   types step changes no caller. The aliases go with the adapter move.
5. **The GitHub clients move too**, since the controllers may import no
   adapter: `IssueTracker` and its types to `internal/ports/intake.go` (the
   narrow part of Issues the design names for intake), `ReleaseTarget` and
   `SourceTarget` to `internal/ports/deployer.go` (the Deployer's first
   adapter until #1068 and #1070 reshape it), their code to
   `internal/adapters/github`. Their contract suites are not this issue's:
   their tests stay `httptest` ones, and the GitHub fakes stay in the
   controller's tests.
6. **Template fetching is plain Git, no port** (design.md): `FetchTemplate`
   and the go-git code move to `internal/adapters/git`; `GitClient` stays an
   interface in `internal/controller`, its types from `internal/ports`
   (`Config` and `Credentials` move there) and `scaffold`.
7. **The fake becomes a package,** `internal/ports/fake`, one type that
   implements all five ports plus `GitClient`'s `FetchTemplate`, reading the
   templates folder it is given. What the controller tests read of its state
   becomes exported methods. It moves before the suites, so the suites run
   against the same fake the controller tests use.
8. **The suites are functions that take a constructor,** one per port:
   `contract.GitHost(t, env)` and so on, where `env` builds a fresh host
   with an empty owner and says what the host can't do (for example, the
   fake's `AgentAccess` keeps tokens in memory). `internal/ports/fake` runs
   them in `make test`; `internal/adapters/gitea` runs them in its
   `Integration` tests under `make test-gitea`. Where the fake and Gitea
   disagree, the fake follows Gitea, and the controller tests that leaned on
   the difference are fixed in the same step.
9. **The guard is a Go test,** `internal/ports/hostneutral_test.go`: it walks
   the repository's Go files outside `internal/adapters/gitea`, counts
   case-insensitive `gitea` and `.gitea/` per file, and fails when a file
   has more than its allowlist entry or isn't listed. The allowlist starts
   at today's counts; the test also fails on an entry above today's count,
   so a fix must lower it. Templates, charts and docs are not Go code and
   are #1095's.

## Steps

| Step | Issue | Depends on | How we know |
| --- | --- | --- | --- |
| This plan | #1094 (`Refs`) | | it merges |
| Ports and their shared types: `internal/ports` with GitHost, Issues, CI, AgentAccess and Registry, the shared types out of the `gitea_*.go` files, `gitprovider` aliasing them and `Provider` their union; `var _ ports.X = (*gitea)(nil)` per port; a depguard rule for `internal/ports` | #1166 | | it compiles; `make check`; `make lint` denies `net/http` in `internal/ports` |
| Controllers take the narrowest port: helpers and reconciler fields name the port they call, not `Provider` | #1167 | #1166 | `make check`; no change to any controller test |
| The fake as a package: `internal/ports/fake`, its state read through methods, `var _ ports.X = (*fake.Git)(nil)` per port | #1168 | #1167 | `make check`; the controller tests pass unchanged in what they assert |
| Adapters move: `internal/adapters/gitea`, `git` and `github`; `internal/gitprovider` and its aliases are gone; `cmd/main.go` builds the adapters; a depguard rule denies `internal/adapters` in `internal/controller`; `make test-gitea` runs the new path; architecture.md's layer table and *Today* column follow | #1169 | #1168 | `make check`, `make test-gitea` |
| Contract suite for GitHost, run against the fake and Gitea | #1170 | #1169 | `make test` runs it on the fake, `make test-gitea` on Gitea |
| Contract suites for Issues, CI, AgentAccess and Registry, run against the fake and Gitea | #1171 | #1170 | as #1170; architecture.md's *Contract test* column names them |
| The host-neutral guard with today's allowlist | #1172 | #1169 | `make test` runs it; a new `gitea` outside the adapter fails it |

## Done when

Every step has closed with `make check` passing (and `make test-gitea` for
#1169 to #1171). #1094 closes itself with its last step.
