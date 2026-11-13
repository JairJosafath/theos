# Architecture

How the code is split, and which way dependencies may point. Read this before adding
code. [design.md](design.md) says *what* theos does; this file says *where* the
code for it goes.

[![theos's architecture: what runs where in the test rig's Kind cluster, the code layers, and where state lives](images/architecture.png)](images/architecture.svg)

The board's source is [images/architecture.excalidraw](images/architecture.excalidraw).
It draws the test rig, a Kind cluster of one node. An install is a cluster
of any number of nodes, where the same parts run as pods the scheduler
places ([design.md](design.md#why-kubernetes)).

## Layers

```
     cmd/main.go            cmd/theos-session          cmd/theos-agent               ui/ (server.mjs + React)
     (operator manager)     (Session client CLI)   (runner in a sandbox)     (the UI)
          │                       │                      │                        │
          ▼                       ▼                      ▼                        ▼
  internal/controller     internal/sessionclient  internal/agentrunner     Kubernetes API: GETs, Brief writes, actions
                                                  (git + agent CLI; result
                                                   in the termination message)
     │        │        │               │
     │        │        └───────────┐   └──► Kubernetes API (Sessions)
     ▼        ▼                    ▼
internal/  internal/        internal/ports ◄── internal/adapters/*
 model  ◄── scaffold         (interfaces)      (Gitea over HTTP, Git with go-git, …)
(pure rules) (pure rendering)
     ▲        ▲
     └────────┴── api/v1beta1 types never reach these two

features/ embeds the theos-session devcontainer feature; the operator writes it into
every scaffolded repository.
```

| Layer | Packages | May import | Must not import |
| --- | --- | --- | --- |
| API types | `api/v1beta1`, the one version served and stored | apimachinery | anything under `internal/` |
| Business rules | `internal/model`, `internal/scaffold` | the standard library, `text/template`, YAML; `scaffold` may use `model` | Kubernetes, controller-runtime, `net/http`, `api/`, other `internal/` layers |
| Ports | `internal/ports` and its `fake`, `contract` (the Deployer from #1161; the Git host's from #1094) | the standard library, business rules | Kubernetes, `net/http`, `api/`, adapters, `internal/controller` |
| Adapters | `internal/adapters/*`: `gitea`, `git`, `github`, `gitlab`, `packages`, `argocd`, `cloudformation`, `matrix`, `localenv` | ports, business rules; host adapters `net/http` and go-git, the chat adapter `net/http`; runtime, deploy and test environment adapters client-go | `api/`, `internal/controller`; host and chat adapters also Kubernetes |
| OCI images | `internal/oci` | the standard library, `net/http`, go-containerregistry; host adapters use it | Kubernetes, controller-runtime, `api/`, any other `internal/` package |
| Sandbox runner | `internal/agentrunner`, `cmd/theos-agent` | the standard library, `internal/model` | Kubernetes, controller-runtime, `api/`, `internal/adapters`, `internal/controller` |
| Test environment tool | `cmd/theos-env`: `run` in a sandbox, `serve` in a leased environment ([plans/done/test-environments.md](plans/done/test-environments.md), T4) | the standard library, `internal/model`, go-git's gitignore matcher | Kubernetes, controller-runtime, `api/`, `internal/adapters`, `internal/controller` |
| Model proxy | `internal/modelproxy`, `cmd/model-proxy` | the standard library; `cmd/model-proxy` also `internal/modelproxy` and `internal/buildinfo` | Kubernetes, controller-runtime, `api/`, `internal/controller`, `internal/adapters`; `internal/modelproxy` any other `internal/` package |
| Readings of the API's resources | `internal/taskfacts`: a Task as the plain facts the factory's numbers are computed from, which the operator's Prometheus series and `theos-session report` both read through it (#142) | `api/v1beta1`, apimachinery, `internal/model` | controller-runtime, client-go, any other `internal/` package |
| Clients of the API | `internal/controller`, `internal/sessionclient`, `internal/chatbot` (theos-chat) | everything above but adapters; their tests wire adapters onto fakes | `cmd/`, `internal/adapters/*` |
| Build info | `internal/buildinfo` | nothing | everything; only `cmd/*` reads it |
| Assets | `features` | `internal/scaffold` | everything else |
| Entry points | `cmd/*` | everything | nothing imports them |

The UI in `ui/` is a separate program. Its server proxies GET requests to the
Kubernetes API, under `/apis/theos.factory.io/` only. With its actions off (the
default), it is read-only by construction but for three writes: creating a Brief (`POST` of kind `Brief` into a namespace that
holds a Project; only its name and spec go upstream), appending a follow-up
prompt to one (a JSON patch whose only operation is `add` at `/spec/prompts/-`),
and pausing or resuming one or its running Tasks (a JSON patch of nothing but
`add` of a boolean at `/spec/paused`, at `/spec/pauseRuns`, or one at each).
The server refuses every other write, and its RBAC grants only `create` and
`patch` on `briefs` beyond its reads, so what a Brief may say stays the CRD's to
check. With `ui.actions.enabled`, the server's action table (`ACTIONS` in
`ui/server.mjs`, listed in [ui/README.md](../ui/README.md)) takes a few more,
each one method, path, content type and body shape: a Brief's and a Crew's
settings, a release re-run, a Container's scaffold retry, checks and answer to
its Standard's bump, a Task from a goal and a Standard's `ref`. They are made as
the UI's ServiceAccount over port-forward, or as the person signed in with OIDC
behind an Ingress, so the cluster's RBAC decides. It shares nothing with the Go code but the resource shapes. For the Factory and Work areas it also reads pods, nodes and
metrics-server itself (`ui/health.mjs`) and sends the browser only a summary;
pods only in the components' and the Projects' namespaces.

`make lint` enforces the rows marked "must not import" with golangci-lint's
`depguard`, so a wrong import fails the build rather than a review. The row
of `internal/taskfacts` is held by the package's own test (`TestImports`,
in `make test`), and by the compiler for `internal/controller`, which
imports it.

## Why the split

- **Business rules stay testable without a cluster.** Checking that a catalog entry
  supports a Container type, rendering template layers, detecting two layers writing
  the same path and assembling `devcontainer.json` are plain functions over plain Go
  values. Their tests are table tests that run in milliseconds.
- **Controllers stay thin.** A reconciler reads resources, turns them into plain
  values, calls the business rules and adapters, and writes status. If a controller
  grows an `if` about templates or types, that logic belongs in `internal/model` or
  `internal/scaffold`.
- **The Git host sits behind an interface, but only Gitea is proven.** The
  controllers use the ports in `internal/ports`, whose shape is Gitea's. The ports a
  second host plugs into, and the rule for calling one swappable, are in
  [Ports](#ports).

## Ports

The factory's swappable parts, decided in
[design.md](design.md#factory-components-ports-and-adapters). A new port or
adapter changes its row here in the same pull request; the maintainer reviews
it against this table. Where the code lives:

- **Ports** are in `internal/ports`, one file per port with its interface and
  shared types, and may import only the standard library, `internal/model`
  and `internal/scaffold`. Their fakes are in `internal/ports/fake`, their
  contract suites in `internal/ports/contract` (`contract.GitHost(t, env)`
  and so on), which each adapter's tests call.
- **Adapters** are in `internal/adapters/<implementation>`, one package each:
  `gitea` implements GitHost, Issues, CI, AgentAccess and Registry,
  `agentsandbox` implements AgentRuntime, `github`, `argocd` and
  `cloudformation` implement the Deployer, `gitlab` follows, `matrix`
  implements ChatHost, and `localenv` and `awsenv` implement TestEnvironments. Plain Git over HTTPS (go-git: template fetches and pushes) is
  shared in `internal/adapters/git`, and so is the reader of a host's
  archive of a tag (`ReadArchive`). What the host adapters share of
  publishing a release's npm and Python packages is in
  `internal/adapters/packages`. A host adapter imports no Kubernetes;
  the runtime, deploy and test environment adapters may use client-go,
  never `api/` or `internal/controller`.
- **Controllers take ports.** Only `cmd/main.go` imports adapters: it builds
  the adapters a GitProvider names and gives each reconciler the ports it
  calls (and `cmd/theos-chat` the Matrix adapter for `internal/chatbot`). A
  depguard rule denies `internal/adapters` in `internal/controller`,
  `internal/sessionclient` and `internal/chatbot` outside their tests, which
  wire adapters onto the fakes.
- **A host's events reach the controllers through the operator's receiver**
  (`internal/controller/git_events.go`, from #1084): the adapter checks a
  delivery's signature and says what it concerns, the receiver finds the
  GitProvider and its Secret, which needs Kubernetes, and each controller
  maps the delivery to its own objects.
- **The per-CI table** (`model.CI`: its name, which names the templates'
  `.theos/ci/<name>/` folder, check contexts, protected paths) is in
  `internal/model`, so `internal/scaffold` reads it without an adapter.
- **The host-neutral guard** is a Go test beside the ports,
  [`internal/ports/hostneutral_test.go`](../internal/ports/hostneutral_test.go):
  it walks the Go files outside `internal/adapters/gitea` and counts
  case-insensitive `gitea` (which `.gitea/` matches) per file. Its allowlist
  holds today's files and counts and may only shrink: a file not in it, or
  with more hits than its entry, fails the test, and so does an entry above
  its file's count, so a change that removes hits lowers the entry.
- **The product-neutral guard** is beside it,
  [`internal/ports/productneutral_test.go`](../internal/ports/productneutral_test.go)
  (#151): a port with more than one product behind it names none, in a
  type, a reason or a comment. It reads the request intake port
  (`internal/ports/intake.go`) and its two suites line by line for an issue
  tracker's or a chat host's name, whatever its case, an import's path left
  out; the ChatHost port and its suite join its list with #152.

`internal/ports` holds the Deployer (#1161) and the Git host's five ports
(#1166): GitHost, Issues, CI, AgentAccess and Registry, one file each with
its shared types and errors, and `ports.Provider`, the union of the five
that `GitClient` (`internal/controller`) returns; the Gitea adapter is
`internal/adapters/gitea`, the controllers' fake `internal/ports/fake`. The
GitHub clients' interfaces are in `internal/ports/deployer.go`
(`ReleaseTarget`, `SourceTarget`) and `internal/ports/intake.go`
(`IssueTracker`, request intake), their code in `internal/adapters/github`.
The column *Today* says where each port is now.

| Port | Interface | Picked by | Implementations | Contract test | Credential path | Today |
| --- | --- | --- | --- | --- | --- | --- |
| GitHost | repositories, files, pushes, branches, tags, branch rules, pull requests, merges (with bypass), commit statuses, source at a tag; optional: Events, a hook on the owner and signed deliveries (#1084, [plans/done/git-events.md](plans/done/git-events.md)) | `GitProvider.spec.provider` | Gitea; GitHub (`internal/adapters/github/github_host*.go`, #74, #75 and #79, no GitProvider reaches it yet), whose Events is the organization's webhook, or, for a user's account, the webhook of each repository the operator names | `contract.GitHost(t, env)` (`internal/ports/contract/githost.go`, #1170) and `contract.Events(t, env)` (`internal/ports/contract/events.go`): the fake (`internal/ports/fake`) in `make test`, Gitea (`TestGitHostContractIntegration`, `TestEventsContractIntegration`) in `make test-gitea`, and the GitHub adapter on its fake GitHub (`TestGitHostContract`, `TestEventsContract`) in `make test` | GitProvider Secret, operator only; Events: the delivery Secret the operator makes | `ports.GitHost` (`internal/ports/githost.go`); Gitea in `internal/adapters/gitea` |
| Issues | issues, comments, labels (exclusive by scope), assignment, milestones, dependencies; request intake is a port of its own, with any tracker behind it: list, comment, find comment, close, and a team's members as an optional capability (`ports.TeamMembers`) | `GitProvider.spec.provider`; intake by the Brief's `releases[].requests` | Gitea; GitHub for intake only | `contract.Issues(t, env)` (`internal/ports/contract/issues.go`, #1171): the fake in `make test`, Gitea (`TestIssuesContractIntegration`) in `make test-gitea`; intake has its own, `contract.IssueTracker(t, env)` (`internal/ports/contract/intake.go`, #61): the open issues, oldest first, each with a key of its own for people to read (`#12` on GitHub) and when it was filed, with no closed one and no pull request, a list that is not modified until an issue is filed, edited or closed, a comment returned with its ID, author and page and found by its marker when the factory's account wrote it (the first of two, none by an empty marker), the two reasons to close, a closed issue closed again with the new reason, an issue's text never changed, and the reason of six failures (`IssueNotFound`, `Unauthorized`, `TokenLacksIssues`, `TrackerNotFound`, `IssuesDisabled`, `TrackerUnavailable`; not `TrackerError`, which no subtest reads), and `contract.TeamMembers(t, env)` (`internal/ports/contract/teammembers.go`, #151) for a tracker that has teams: a team's active member alone, no stranger, no team the tracker lacks and no invitation that isn't accepted, and the reason of three failures, each with no member (`TokenLacksMembers`, `Unauthorized`, `TrackerUnavailable`); the GitHub client on its `httptest` fake GitHub (`TestIssueTrackerContract`, `TestTeamMembersContract`) in `make test`, where its paging, its ETag over pages and what a moved or locked issue answers stay in its own tests | GitProvider Secret; intake by the Brief's token Secret; operator only | `ports.Issues` (`internal/ports/issues.go`) and `IssueTracker` (`internal/ports/intake.go`), which names no product (`TestProductNeutral`, #151) |
| CI | runs of a commit or tag, rerun failed jobs, a job's log, runners online (may be unknown); the per-CI table in `internal/model`; optional: OwnerRunners, the owner organization's runner registration token, its runners and their state, deleting one (#1139, [plans/done/ci-runner-isolation.md](plans/done/ci-runner-isolation.md)); JobRunners, the owner's jobs that wait for a runner of some labels, and an ephemeral runner registered for one job whose files a machine outside the cluster runs it with, the registration token kept in the operator ([plans/done/test-environments.md](plans/done/test-environments.md), step 8) | the host's own, else `GitProvider.spec.ci.<member>` | Gitea Actions; GitHub Actions (`internal/adapters/github/github_host_ci.go`, #78, no GitProvider reaches it yet), with neither optional part: GitHub's hosted runners take the jobs | `contract.CI(t, env)` (`internal/ports/contract/ci.go`, #1171), what needs no runner to see (no runs without a workflow, runners online or unknown, `ErrNoCILog`): the fake in `make test`, Gitea (`TestCIContractIntegration`, a job taken by a runner registered over the runner protocol that sends nothing) in `make test-gitea`; OwnerRunners: Gitea in `make test-gitea`, its fake beside the controllers'; JobRunners: subtests of `contract.CI` (a queued job listed by its label alone, a registered runner's files without the registration token), on the fake and on Gitea (a workflow pushed for a label no runner takes); the GitHub adapter on its fake GitHub (`TestCIContract`, the JobRunners subtests in `Cannot`) in `make test` | jobs: none of the user's, the host's job token scoped to the repository; the operator reads runs, and an owner's runner registration token, with the GitProvider Secret | `ports.CI` (`internal/ports/ci.go`); `OwnerRunners` (`internal/ports/ci.go`); `JobRunners` (`internal/ports/ci_jobs.go`); the per-CI table `model.CI` (`internal/model/ci.go`); the per-project runner pool `project_ci_runners.go` and its pod spec `ci_runner_pod.go` in `internal/controller` |
| AgentAccess | mint a credential that writes to one repository, with its expiry; revoke old ones; remove | `GitProvider.spec.provider` | Gitea (a bot user per Container or Brief); GitHub (`internal/adapters/github/github_host_agents.go`, #77, no GitProvider reaches it yet): an installation token of the GitHub App the provider's Secret names, for the one repository | `contract.AgentAccess(t, env)` (`internal/ports/contract/agentaccess.go`, #1171): writes its repository, not another, cannot push to a protected `main`, removed means refused, `RevokeOldAgentTokens` keeps the current one; the fake (its `PushAs`) in `make test`, Gitea (`TestAgentAccessContractIntegration`, pushing with git as the bot) in `make test-gitea`, and the GitHub adapter on its fake GitHub (`TestAgentAccessContract`) in `make test`; `ports.AgentAccessState` says what a provider's condition says of it, by what the adapter's `CheckAgentAccess` finds missing | minted from the GitProvider Secret into the Container's agent Git Secret, passed to its sandboxes | `ports.AgentAccess` (`internal/ports/agentaccess.go`) |
| Registry | Go modules (optional), Go proxy address, a release's image archive pushed by digest (#1069, [plan](plans/images.md)), a release's npm tarballs and Python wheels and sdists published to the npm and PyPI registries, with the scope, registry and index addresses a repository's apps install from (#1229, [plan](plans/done/app-packages.md)) | the host's own, else `GitProvider.spec.registry.<member>`; npm scope and registry, PyPI index and upload by `GitProvider.spec.packages` | Gitea packages; GitHub (`internal/adapters/github/github_host_registry.go`, #80, no GitProvider reaches it yet): images in its container registry, npm packages in its npm registry under the owner's scope, Python packages only where `spec.packages` names an index, and Go modules served from their repositories, with nothing to upload | `contract.Registry(t, env)` (`internal/ports/contract/registry.go`, #1171), Go modules: no `go.mod` is an outcome, a repeat publishes nothing, and a host with nothing to upload says the module exists; the fake in `make test`, Gitea (`TestRegistryContractIntegration`) in `make test-gitea`, the GitHub adapter on its fake GitHub (`TestRegistryContract`) in `make test`; packages: a fake registry in `make test`, Gitea with npm and uv installs in `make test-gitea`, the fake GitHub's npm and container registries in `make test` | GitProvider Secret for the operator; CI only attaches release files with its job token | `ports.Registry` (`internal/ports/registry.go`), `Repository.GoProxy`, or `Repository.GoPrivate` on a host that serves modules from repositories, `Repository.NpmScope`, `NpmRegistry`, `PyPIIndex`, `PyPIUpload`; `PublishReleasePackages`, whose reading of the files and whose npm and PyPI protocols both adapters share (`internal/adapters/packages`) |
| AgentRuntime | start a run, its state and result, stop it, list held runs; the fence is the adapter's | Helm installs one; no field until a second | Agent Sandbox | to write; the sandbox's pod spec and fence tests in `make test` | operator ServiceAccount; the sandbox gets no Kubernetes token | `SandboxGVK`, `buildSandbox`, `runState` in `internal/controller` |
| Deployer | check a target's credential, ship a built release to it (one write per call where the adapter is the tool, as for GitHub releases), read its state back; a Deployment template writes the tool's files | the `DeployTarget`'s member, by a map from member name to adapter in `cmd/main.go` (`github`; `argoCD` #1072 ([plans/k8s-deploy.md](plans/k8s-deploy.md)), `cloudFormation` #1071 ([plans/aws-deploy.md](plans/aws-deploy.md)), `gitlab` #1606 ([plans/gitlab-deploy.md](plans/gitlab-deploy.md)), `flux` later); the member requirement table is `model.DeployMember` (`internal/model/deploy.go`) | GitHub releases (`internal/adapters/github`), which the Crew's `publishReleases` copy calls too; Argo CD (`internal/adapters/argocd`, #1586): one `Application` per Container by server-side apply, wired to the `argoCD` member (#1588); AWS CloudFormation (`internal/adapters/cloudformation`, #1608): a release's cloud assembly published to the CDK bootstrap's bucket and deployed as change sets through the bootstrap's roles, wired to the `cloudFormation` member (#1609); GitLab releases (`internal/adapters/gitlab`, #1803): a release's files in the project's generic package registry, the release made last and linking them, the image copied by digest to the project's container registry (#1806), wired to the `gitlab` member (#1804) | `contract.Deployer(t, env)` ([plans/done/deploy.md](plans/done/deploy.md)): the fake (`internal/ports/fake`), the GitHub adapter on the publish tests' fake GitHub, and the Argo CD adapter on envtest with Argo CD's CRDs (`test/crds/argocd`) and a fake Argo CD that syncs each Application, and the CloudFormation adapter on a fake AWS API, and the GitLab adapter on a fake GitLab (`internal/adapters/gitlab/gitlabtest`), in `make test`; the state rule is `model.ArgoCDDeployState`, table-tested | the DeployTarget's `credentialsSecretRef` (and a Crew's `publishReleases.github.tokenSecretRef`, which the Crew hands the adapter as the target's Secret), operator only; CI never holds it | `ports.Deployer` (`internal/ports/deployer.go`); the GitHub adapter calls `ports.ReleaseTarget`, built by `internal/adapters/github/github.go`, the only GitHub writer |
| ChatHost | the bot's user, a Space and its rooms found by ID or alias or made (private, invite only, unencrypted, linked both ways), invites, messages with Matrix HTML, thread replies, edits, reactions, `/sync` from a since token; a 429 waited out ([plans/done/chat-matrix.md](plans/done/chat-matrix.md)) | `Chat.spec.homeserver`, else the operator's `--chat-homeserver`; Matrix the only adapter, by `matrix.Hosts` in `cmd/main.go` and `cmd/theos-chat` | Matrix (`internal/adapters/matrix`) | `contract.ChatHost(t, env)` (`internal/ports/contract/chathost.go`, #61): the bot's user and a refused credential, a private room made once and found by its ID, then its alias, a room's name brought to the one asked for, a room made with its topic, a Space and the rooms in it each a room of their own (the three in subtests of their own, so a host without names, topics or Spaces skips only that one), a taken alias, invites, messages as the bot, thread replies, edits, reactions, a sync from a token with each message once and none of the bot's own, and a sync with a wait of 0 that returns at once when nothing is new; the Matrix adapter on the fake homeserver `internal/adapters/matrix/matrixtest` (`TestChatHostContract`) in `make test`. What Matrix does its own way (the Space's links, the events written, a 429 waited out, an idle sync's wait) is in the adapter's tests on that fake, which the `Chat` controller's and theos-chat's envtest share, and theos-chat's posting tests on it with a fake API server, in `make test` | the Chat's `credentialsSecretRef`, key `accessToken`: the operator, and `theos-chat` (`get` on Secrets, `config/chat/rbac.yaml`) | `ports.ChatHost` (`internal/ports/chathost.go`); the rooms' rules, what is posted where, the commands and their allowlist in `internal/model/chat*.go`; the `Chat` controller in `internal/controller/chat_controller.go`; theos-chat in `internal/chatbot` |
| TestEnvironments | lease a place to run commands with tools the sandbox lacks (a browser, an emulator, a GPU), read it back (Pending, Ready with its endpoint and pinned certificate, Failed, Ended), return it, and list the leases held for an environment, lost ones included ([plans/done/test-environments.md](plans/done/test-environments.md), T6) | the `TestEnvironment`'s member, by a map from member name to provider in `cmd/main.go` (`local`, `aws`, `deviceFarm`); a remote provider may check a credential (`TestEnvironmentCredentials`), release what it keeps beside its leases (`TestEnvironmentReleaser`) and lease a machine that runs one CI job (`TestEnvironmentRunners`, step 8), and one whose environment runs test packages rather than commands has `TestRuns` (`internal/ports/testruns.go`), which the operator calls for the requests a run pushes to its lease's branch | the local provider (`internal/adapters/localenv`): a pod per lease in the project's namespace, from the environment's image, with `theos-env` copied in from `--test-environment-tools-image` and the lease's token in a Secret of its own; the aws provider (`internal/adapters/awsenv`, the AWS SDK): an EC2 machine per lease, fenced in its RunInstances call, that downloads `theos-env` from `--test-environment-tools-url` and serves it over HTTPS with a certificate its token makes, a Mac's on a dedicated host it keeps, or, for a CI job, also the CI runner from `--test-environment-runner-url`, which it starts once its metadata service is off (`internal/adapters/awsenv/ci.go`); the deviceFarm provider (`internal/adapters/awsenv/devicefarm.go`): a lease recorded on the Device Farm project, and the test runs its run asks for | `contract.TestEnvironments(t, env)` (`internal/ports/contract/testenv.go`): the fake (`internal/ports/fake`), `localenv` on envtest and `awsenv` on a fake EC2 and a fake Device Farm, in `make test` | none for local; the `aws` and `deviceFarm` members' `credentialsSecretRef`, operator only; each lease's token goes to the run that holds it alone | `ports.TestEnvironments` (`internal/ports/testenv.go`); the rules in `internal/model/testenv.go`; the `TestEnvironment` controller in `internal/controller/testenvironment_controller.go`, its CI leases in `testenvironment_ci.go` and its test runs in `testenvironment_testruns.go`; the lease pods' fence `project_testenv_network.go` |

No port is swappable yet: none has two real adapters passing a full contract (Issues on GitHub covers intake only). A port is called
swappable here once its fake and two real adapters pass its contract.

## Flow of one scaffold

1. `internal/controller` resolves the Container's references and converts the
   resources into `model` values.
2. `internal/model` checks every catalog entry supports the Container type.
3. The Gitea adapter creates the repository and `internal/adapters/git` fetches each template at its
   pinned ref.
4. `internal/scaffold` renders the layers in order (Framework, Standard, Compute,
   Deployment, Connectors, Domains), fails on a path owned by two layers, adds `.devcontainer/` and
   `.theos.yaml`, and returns one file tree.
5. The Gitea adapter pushes that tree as a single commit.
6. `internal/controller` writes `Ready`, `repositoryURL`, `scaffoldedFrom`,
   `scaffoldedDomains` and `appliedRelationships` with `appliedConnectors` (what each Relationship added).

## Flow of a Standard update

A Ready Container with a Standard follows its Standard's `spec.template.ref`, one
step per reconcile (`internal/controller/container_standard.go`); the minute
recheck polls the pull request, and a Standard's spec change enqueues its
Containers at once.

A Container that adopted its repository and names a Standard `main` doesn't
hold has no `status.standard` yet: the same pass first brings the Standard
in (`internal/controller/container_standard_adopt.go`, the rule in
`internal/model/standardadoption.go`). It opens one pull request with the
files that differ, kept in `status.standardAdoption`, which a person merges
or closes; the operator never merges it. Once `main` holds the files the
Standard protects, the Standard is recorded and the updates below take
over. Until then the Task and the Crew read `standardNotOnMain` and start
no run.

1. `internal/controller` takes the ref the Container's Standard files are at from
   `status.standard`, or before the first update from `scaffoldedFrom`
   (`model.ScaffoldedRef`). It compares it with the Standard's ref.
2. `internal/adapters/git` fetches the template at both refs; `internal/scaffold`
   renders each with the scaffold's input (`RenderLayer`); the Gitea adapter reads
   `main`'s copy of every path either render has (`ReadFiles`).
3. `internal/scaffold.PlanUpdate` decides by whole files what to write, remove
   and leave out. Nothing to write: the controller records the new ref.
4. The Gitea adapter pushes one commit onto the `main` commit it read
   (`PushBranch`, branch `model.StandardUpdateBranch`) and opens a pull request
   with `model`'s title and body. The controller records it in
   `status.standard.update`.
5. Each later reconcile reads the pull request: merged, closed, conflicting,
   pushed to by someone else, or waiting for CI. `model.NextStandardStep`
   decides every step from the facts the controller reads, one read at a time:
   whether to plan, record a merge, drop, stop, wait or merge. Once CI is green,
   `model.StandardUpdateRefusal` checks the required checks and the paths, and
   the Gitea adapter merges it as admin (`ForceMergePullRequest`). The controller
   moves `status.standard.ref` and reports each step in the condition
   `StandardUpdated`.

## Flow of a Git host's delivery

Polling stays the contract; a delivery only shortens the wait
([plans/done/git-events.md](plans/done/git-events.md), #1084).

1. The GitProvider reconciler (`internal/controller/gitprovider_controller.go`)
   makes the delivery Secret `<gitprovider>-git-events` and registers the
   owner's hook to `<--git-events-url>/git-events/<namespace>/<gitprovider>`
   through `ports.Events`.
2. The host posts a delivery there. The receiver
   (`internal/controller/git_events.go`, a Runnable on the leader, listening on
   `--git-events-bind-address`, off by default) reads at most 5 MiB (413 past
   it), finds the GitProvider in the cache (404 when there is none or its
   adapter has no Events), reads its Secret live, and has the adapter's
   `ParseEvent` check the signature (401 when it fails) and say what the
   delivery concerns: a repository, an issue or pull request, a branch.
3. It sends the Event, without blocking, to one buffered `source.Channel` per
   controller and answers 202; it writes nothing. The Container controller
   enqueues the Container named after the repository, the Crew controller its
   Crews, the Task controller its Tasks whose issue, Standard-change issue,
   pull request or branch the delivery names (the `indexTaskRepository`
   index), or every unfinished one when it names neither, and the Brief
   controller the Brief whose repository is `brief-<name>`, whose Plan Tasks
   the Task controller enqueues the same way. Only objects on that
   GitProvider are enqueued; each reconcile reads the host as a poll would.
4. The receiver remembers each GitProvider's last verified and last refused
   delivery, and enqueues the GitProvider when the later of the two changes;
   its reconciler writes `Events` `Receiving` (True) or `DeliveryRefused`.

### Alerts on the same listener

The listener also serves `POST /alerts/<namespace>/<alertsource>`
(`model.AlertsPath`), so `--git-events-bind-address` turns it on too
([alerts.md](alerts.md), [plans/done/alerts.md](plans/done/alerts.md), #64). An alert
is not a hint: nobody can be asked again what fired, so the delivery is the
fact.

1. `controller.AlertReceiver` (`internal/controller/alert_receiver.go`,
   mounted as `GitEventsReceiver.Alerts`) reads at most 1 MiB (413), finds
   the AlertSource (404), reads its Secret live and checks the delivery with
   `model.AlertDeliveryAuthentic` (401), parses it with `model.ParseAlerts`
   (400), puts its alerts in the source's inbox in memory (503 when the inbox
   is full or the operator is stopping) and answers 202.
2. The AlertSource reconciler (`internal/controller/alertsource_controller.go`,
   `alertsource_drain.go`) drains the inbox: for each alert its Container
   (`model.AlertContainer`), the facts from the Git host, `model.AlertStep`,
   the issue or comment through `ports.Issues`, then one status write; only
   then is the alert dropped from the inbox.
3. On SIGTERM the receiver stops taking deliveries and drains until
   `--alert-drain-timeout` (the chart's `alerts.drainTimeout`); on leadership
   lost it drains nothing.

## Flow of one Session

1. The theos-session feature installs the client in the devcontainer and runs
   `theos-session start` after every start.
2. `internal/sessionclient` finds `.theos.yaml`, creates a Session on that
   Container and patches its status with a heartbeat every 30 seconds.
3. `internal/controller` adopts the Session under its Container, counts it in
   `activeSessions`, and marks it `Stale` two minutes after the last heartbeat.
4. On SIGTERM the client deletes its Session.

Each step is safe to repeat: see "Two rules keep this safe to retry" in
[design.md](design.md#scaffolding-a-container).

## Flow of one Task

1. `internal/controller` reads the Task live (not from the cache: steps have side
   effects), adopts it under its Container, and gathers the facts: is the
   Container Ready, is an earlier Task still active on it, what did the current
   sandbox report, what do the checks say, and whether an unavailable agent's
   wait (`status.agentBackAt`) is over.
2. `internal/model.NextTaskStep` turns the facts into one step: wait, start the
   implementer or reviewer, open the pull request, merge, next round, wait for an
   unavailable agent, or fail. The model also decides what each step does to the
   Task (`internal/model/task_transition.go`): the controller copies the status
   into a `TaskProgress`, applies one transition and copies it back.
   `StartRun`, `OpenedPR`, `NextRound`, `ResolveConflicts`, `UpdatedBranch`,
   `RetryReview`, `Rerun`, `Recheck` and `Proposed` move the round, phase and feedback;
   `DecideMerge` and `AfterSquash` choose what to do with an approved pull
   request; `AgentBackAt` sets an unavailable agent's wait; and `IssueClosed`,
   `PullRequestClosed` and `GatedMergeRefused` end the Task.
3. To start a run, the controller makes sure the Container's agent bot user exists
   (`ports.EnsureAgentAccess`, token in Secret `<container>-agent-git`) and
   creates an Agent Sandbox `Sandbox` owned by the Task, with the role's Agent
   (`spec.agentRef` implements, `spec.reviewerRef` reviews) and its harness and
   model. Its pod mounts no ServiceAccount token; the project's NetworkPolicy lets
   it reach only DNS, the Git host and public HTTPS. A Task with `spec.issue`
   reads the issue before every run and passes it as the goal; on its first run
   it labels the issue `status/in-progress`, assigns the bot and comments.
   `c4Context` gives the agent the Container's place in the story: the system's
   needs and KPIs, its users, each Relationship, and the related Containers'
   repositories.
4. In the pod, `cmd/theos-agent` reads its job from the environment, and
   `internal/agentrunner` clones with the bot's token, clones the related
   repositories read-only, reads the role guide (`docs/agents/<role>.md`) from the
   base branch, runs the agent, runs `make check` and sends the agent back while
   it fails, pushes the branch, and writes one JSON `Result` to the termination
   message. A reviewer run answers with a JSON verdict and up to three follow-ups,
   each with up to three acceptance criteria, instead. A usage limit, an overload, a 5xx server error or rejected credentials come back as
   `unavailable`: the Task keeps its round and tries again later.
   Which agent runs is the job's harness, one entry of the table in
   `internal/agentrunner/harness.go`: how its agent is made for a job, and
   what its CLI prints when its service can't go on. Recording sessions and
   resuming one are interfaces an agent has or hasn't; the runner never
   asks which harness it is. A new harness is an entry there, a value in
   `Agent.spec.harness`'s enum, a case of the contract test
   (`harness_test.go`) and its transcripts in `testdata/harness/<name>/`;
   the tests fail until the four agree
   ([plans/harnesses.md](plans/harnesses.md)).
5. The controller reads the result from the pod status, opens the pull request as
   the agent bot (Gitea refuses a review of one's own pull request), polls the
   combined commit status until CI is done, runs the reviewer, posts its verdict
   as a pull request review, and squash-merges on approval. For an issue it then
   comments, files the follow-ups as `status/proposed` issues in the Later
   milestone, outside any release, and closes it; a failed Task hands the issue
   back with `status/needs-human`. Status is patched
   with an optimistic lock, so a stale write is retried rather than repeating a
   step. Notes on the Git host (labels, comments, reviews) are logged when they
   fail; they never decide a step.
6. **A Standard change.** After a round the runner splits the agent's commits:
   edits to eligible protected paths (the Makefile, `.gitea/`, lint config,
   `docs/style/`, `.devcontainer/`; never `AGENTS.md`, `docs/agents/` or
   `.theos.yaml`) go to a proposal branch `standard/<task>` and are reverted
   on the Task branch; an ineligible path fails the round. The result carries the
   proposal (branch, paths, reason).
7. **`Blocked`.** The controller files a `kind/standard-change` issue as the
   operator (with an acceptance checklist, the paths and the branch), makes it
   block the Task's issue in Gitea, records `status.standardChange` and sets the
   phase `Blocked`. A `Blocked` Task runs no agent and spends no round. A third
   Standard change fails the Task ("more than two"), and so does a proposal from
   a Task with a goal or one no Crew started: only a Crew runs the change.
8. **Resume.** When a Task on the change's issue has reached `Merged`, the
   `Blocked` Task goes back to `Implementing` in the same round, and the runner
   merges `main` in. If the change closed unmerged or failed, the Task fails with
   that reason. If the branch then equals `main`, the Task ends `Merged` with
   reason `DoneByStandardChange` and the operator closes the issue. Once the
   change merged or will not, the operator deletes the proposal branch.
9. **Gated merge.** The Crew gives the change's Task `spec.allowedProtectedPaths`,
   copied from the waiting Task's status; a label alone grants nothing. The
   runner allows exactly those paths. Gitea's protected-file rule blocks the
   normal merge, so the operator checks CI, the approval and every changed path
   (a rename's old path too) itself, then merges with Gitea's admin
   `force_merge`. CI passes only if each required `check*` pattern has a
   matching context that succeeded and every other match succeeded too or was
   skipped (a push run off main), as branch protection requires, so a renamed
   workflow or job is refused. CI still pending: it
   waits while the review run that approved it lasts. When that run ends first,
   on the reviewed commit or on the branch with `main` merged in, the change
   goes back to `Checking` in the same round (reason `ApprovalEnded`), no try
   spent, and is reviewed again once CI passes, so no reviewer run is spent on
   a CI wait and nothing merges without a lasting approval. A pull request too large to list (1000+
   files) is refused too. A gated change may touch only its allowed paths, so
   neither its implementer nor its reviewer is asked for tests; the check
   command passing and the reviewer's three checks decide. The gated
   implementer's prompt overrides old guides:
   Containers on Standard v1.0.0 have guides that say "stop", and a Standard
   may ship none and an `AGENTS.md` that says "Leave .devcontainer/,
   .theos.yaml and .gitea/ alone"; the prompt says to follow it instead. Only
   `go-standard` and `ts-standard` (v1.1.0 and later) and `python-standard` ship guides
   that describe Standard changes.

## Go modules of the factory's apps

An app's Go module path is `theos.local/<project>/<container>` (the Go
templates' `go.mod`), which no public proxy knows. Agent sandboxes of a
Container's Tasks get `THEOS_GO_REGISTRY=<gitea>/api/packages/<owner>/go`,
`GOPROXY=http://127.0.0.1:3127,direct` and `GONOSUMDB=theos.local`
(`buildSandbox`, from the GitProvider's URL and owner through
`ports.Repository.GoProxy`). theos-agent serves `agentrunner.GoProxy` there
while the agent works: `theos.local/` paths go to the registry and nowhere
else, with the bot's credentials over https (`ports.AgentTokenScopes`
lets it read the owner's packages; the token's name carries its scopes, so an
operator upgrade that changes them makes a new token beside the old one
instead of revoking what a running sandbox of the Container holds. The agent
Git Secret's `theos.factory.io/old-tokens-before` annotation records when; at
the start and end of each run, once every sandbox of the Container started
after it (`model.OldTokensUnused`), the operator revokes the old tokens
(`ports.RevokeOldAgentTokens`) and drops the annotation), and every
other path to
proxy.golang.org. The go command has no per-path proxy, and with a list of
the registry, then the public proxy, it would ask the public proxy for a
factory module the registry lacks (it goes on after a 404), leaking the
project's and Container's names. go-standard's CI (v1.1.5) downloads the
`theos.local/` modules of `go.sum` with `GOPROXY` set to the registry
alone, then runs `make check` with `GOPRIVATE=theos.local`, so no proxy is
asked for them. [plans/done/app-modules.md](plans/done/app-modules.md) has why.

## npm and PyPI packages of the factory's apps

The same sandboxes get `THEOS_NPM_SCOPE`, `THEOS_NPM_REGISTRY` and `THEOS_PYPI_INDEX`
(`buildSandbox`, through `ports.Repository.NpmScope`, `NpmRegistry` and
`PyPIIndex`): `GitProvider.spec.packages` where it is set, else the scope
`@theos` and the host's own registries,
`<gitea>/api/packages/<owner>/npm/` and `.../pypi/simple`. The planner's gets
none, as with Go. The token a pod installs with is the agent bot's for the
host's own registry, else `THEOS_NPM_TOKEN` or `THEOS_PYPI_TOKEN` from the registry
Secret's `readToken` (`readTokenEnv`); with neither, installs send no
credential. theos-agent writes `~/.npmrc` (mode 0600, `agentrunner.WriteNpmrc`)
with the scope's registry and the token as `_authToken` for the registry's
host and path, and uv reads the `~/.netrc` it writes for Git. No loopback
proxy: npm sends a scope, and uv a package, to its own registry. When
`spec.packages` departs from the default, `ProjectReconciler` sets the three
names as the owner's Actions variables and the read tokens as its Actions
secrets, and deletes them when it returns to it
(`internal/controller/project_packages.go`, `ports.OwnerActions`), so a
workflow reads them with the host's own as the fallback.
[plans/done/app-packages.md](plans/done/app-packages.md), decision 5, has why.

The Crew publishes a release's packages to the same registries
(`internal/controller/crew_packages.go`, `publishPackages`): once the
release runs succeed, it reads the release's npm tarballs, wheels and sdists
and uploads each to the npm registry or the PyPI index, by default the
host's own under the owner, with the GitProvider's credentials
(`ports.Registry.PublishReleasePackages`, `gitea_packages.go`; the
registries come from `packagesOf` in `gitprovider_packages.go`). A file is
published only at the tag's version and only under the scope; one outside
either, or a version the registry already has, is an outcome the decision
names, not an error (`model.DecidePackages`, `internal/model/packages.go`).
The decision lands in `status.lastRelease.packages` and the condition
`PackagesPublished` (`reportPackages`), an upload is a Normal Event
`PackagePublished` and an invalid file a Warning `InvalidPackage`, once per
release.

## Go build caches

A Go Task runs `make check` three or four times a round: the agent while it
works, `internal/agentrunner` after it, CI on the `go` runner and the reviewer.
Two caches on a node's disk keep compiled packages between pods, so
each run compiles only what changed. They were built for the test rig's one
node: on a cluster of several, each node holds its own (#120).

| Cache | Node directory | Mounted by | Set up by |
| --- | --- | --- | --- |
| sandboxes | `/var/cache/theos/go/sandbox`, the operator's `--sandbox-go-cache` | every agent sandbox, at `/cache/go`, with `GOCACHE` and `GOLANGCI_LINT_CACHE` pointing into it | an init container in the agent's image |
| CI, shared | `/var/cache/theos/go/ci` | only the shared `go` runner (`config/runner/go.yaml`), which serves the templates organization; jobs get the variables from the runner's `env_file` | an init container, and a sidecar hourly |
| CI, per project | `/var/cache/theos/go/projects/<namespace>`, under the operator's `--ci-go-cache` | the project's `go` pool pods, at `/cache/go`; the variables come from ConfigMap `c4-ci-runner-env` in the namespace, the pod's `env_file` | an init container of each pod |
| CI, releases | none: an emptyDir per pod | each `go-release` pool pod (`freshGoCache`), at `/cache/go`, with the same `env_file`; gitea-release's release job runs there, so the signed release files link nothing a pull request's job wrote (#1141) | an init container of each pod, copying the image's warm cache |

- **CI's cache is its own.** Go doesn't check what it reads from a cache, so a
  sandbox that could write CI's cache could plant a compiled package that CI,
  the gate, then trusts. No sandbox mounts it. The sandbox cache is shared by
  all projects: a sandbox's checks are not the gate.
- **`c4-go-cache DIR`** (`hack/toolchain-go/go-cache.sh`, in the toolchain-go
  image) seeds an empty cache from the image's warm one (the Kubernetes CEL
  libraries, about 3 GB) and keeps it under 8 GB: it drops entries unused for a
  day, then all of them, and seeds again. It deletes entry files only, under a
  lock, so a build running at the same time sees a miss, not an error. An agent
  image without Go has no `c4-go-cache`, and its init container exits at once.
- hostPath, not a PVC: it assumes one node, one directory per cache, and
  that no project namespace enforces Pod Security. That holds on the rig and
  not on every cluster (#120). Remove the flag, or the runner's volume, to
  go back to cold builds.

## Flow of one Crew

1. Every minute, when a Task on its Container changes phase or the Container
   turns Ready, and when a delivery of the Git host names the Container's
   repository, `internal/controller` reads the Crew, adopts it under its
   Container, checks its Agents exist, and lists the Container's Tasks live.
2. Through the Issues port it makes sure the status labels exist, lists the open
   issues labelled `status/ready`, and reads each one's open dependencies.
3. `internal/model.NextCrewStep` decides: issues without an acceptance criteria
   checklist, already merged, or tried three times go back to a person; issues
   with open dependencies wait; up to `parallel` Tasks work on the Container at
   once, and `Blocked` Tasks don't count, so a Crew can't fill its slots with
   Tasks that wait; otherwise the lowest-numbered ready issues get a Task
   `<container>-<issue>` (with `-<attempt>` from the second try), with the Crew's
   implementer and reviewer. For a `kind/standard-change` issue it sets the
   Task's `spec.allowedProtectedPaths` from the filing Task's
   `status.standardChange`, leaving out any ineligible path; such an issue
   starts before the other ready issues, so the `Blocked` Task's branch doesn't
   age behind the backlog.
4. The controller labels the handed-back issues `status/needs-human` with the
   reason, creates the Task, and writes the Crew's phase, active Task and its
   ready and waiting issues.

The operator never branches on a Container type: `internal/controller/typeneutral_test.go`
fails when controller, scaffold or runner code names one. Everything type-specific
lives in the catalog's templates.

## Flow of the factory's health

1. Every 15 seconds, and when a browser asks for `GET /factory/health`,
   `ui/health.mjs` lists the pods, the nodes and their metrics, timing the node
   list as the API server's response time. Answers are cached for 5 seconds.
2. It matches pods to the factory's components by namespace and labels
   (`DEFAULT_COMPONENTS`, overridable with `FACTORY_COMPONENTS`), finds the agent
   sandboxes by their label, and reads the operator's `--max-agent-sandboxes`.
3. It returns states, restarts, usage and the last hour of samples, never a pod
   spec. A sample holds the cluster's CPU and memory, every node's added up.
4. The browser combines that with the Tasks, Agents, Containers, Sessions,
   Crews and Briefs it watches (`src/model/factory.ts`): the board, each Task's timeline
   from `status.history`, the Crews, the time per stage and the "Needs a look"
   list, each item naming the Brief whose `status.attention` or `needsYou`
   lists it.

The Task controller writes `status.history` in `writeStatus`, through
`model.RecordTransition`.
