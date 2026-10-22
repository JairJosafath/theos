# C4 Factory: Design

The spec: what the factory is and does. How it got here, with each step's date
and reasons, is [design-history.md](design-history.md); the plans in
[plans/done/](plans/done/) hold every decision's why. Where this says
*decided, not built*, the issue named builds it.

[![How C4 Factory works today: describe, scaffold once, build from issues, keep current, watch, the fence and where it runs](images/how-it-works.png)](images/how-it-works.svg)

## Purpose

C4 Factory turns an idea into working repositories by first telling its story
as a C4-inspired model. The model lives in Kubernetes custom resources, a
kubebuilder operator acts on it, agents build the apps from issues, and a UI
draws the story on top.

- **The CRDs are the truth.** Anything can write them: kubectl, GitOps, a
  Brief's planner or the UI. The UI and the Session client enforce nothing;
  the rules live in the CRD schemas (CEL), the admission policy and the
  operator.
- **A thin factory.** Open-source tools do the real work: each framework's
  tooling, the Git host's CI, the tool that owns each deploy target.
- **Quality comes from what the platform enforces,** not from trusting the
  agent: the fence, protected files, required checks and a reviewer.
- **Humans and agents are equal,** both in devcontainers, both Sessions.
- **What can be configured is configured.** A choice that depends on where or
  how a user runs the factory (a registry, a scope, an index, a provider) is
  a setting whose default works on the chart's in-cluster Gitea; the design
  never fixes it for the user ([AGENTS.md](../AGENTS.md), "What can be
  configured is configured").

| Topic | Rule |
| --- | --- |
| Scope | One Project per namespace; no references across namespaces, reuse is by copying. |
| Levels | Person, SoftwareSystem, Container and DataStore. No Component or Code level. |
| Container | An application with its own new repository; no monorepos, no adopted repositories. |
| Container type | `static-site`, `service`, `job`, `worker`, `mobile-app`, `cli`, `extension`, `operator`, `function`. The operator never branches on a type; a test keeps it so. |
| Catalog | Framework, Standard, Compute, Deployment and Connector entries per namespace, each a template repository at a pinned tag or commit, never a branch. A Bundle names one Framework, Standard, Compute and Deployment as a team's choice; a Container or the planner names it instead of the four, the operator expands it once, at the scaffold, and `status.bundle` says what it expanded to ([plans/catalog-bundles.md](plans/catalog-bundles.md), #1092, decided, not built). |
| Scaffolding | Once, at creation. Afterwards only the Standard's files and the AWS operations block follow, through pull requests the operator opens and merges. |
| Shipping | CI builds with no deploy credential; the operator hands each release to the tool that owns the target. Monitoring the apps is left to existing tools. |

## The model

Eighteen namespaced kinds in `c4.factory.io/v1alpha1`. Every child names its
parent in `spec`; the operator turns that into the ownerReference, so deletion
follows the tree. `metadata.name` is the stable id; `displayName` and
`description` are on every story kind.

| Kind | Owned by | Parent field | What it is |
| --- | --- | --- | --- |
| Project | nothing | none; name equals the namespace | The whole story, one per namespace |
| Person | Project | none | A user, role or stakeholder |
| SoftwareSystem | Project | none | A system that delivers value, with its needs and KPIs |
| Container | SoftwareSystem | `spec.softwareSystemRef` | An application with its own repository |
| DataStore | SoftwareSystem | `spec.softwareSystemRef` | A database or storage, backed by a Connector |
| Relationship | its source element | `spec.source` | A directed link between two elements |
| Session | Container | `spec.containerRef` | An agent or human at work in a devcontainer |
| Task | Container | `spec.containerRef` | A goal for a Container, implemented, checked, reviewed and merged by agents |
| Crew | Container | `spec.containerRef` | The Agents that work a Container's ready issues, and its releases |
| Brief | nothing | none | An app idea in plain words and its prompts, which a planner Agent turns into the model |
| GitProvider | Project | none | Where repositories are created, with credentials |
| Framework, Standard, Compute, Deployment, Connector | Project | none | Catalog entries: a pinned template and the Container types it supports |
| Agent | Project | none | Who does Tasks: a harness, a model, a toolchain image, credentials and limits |
| DeployTarget | Project | none | A place releases ship to, with its credential |

```yaml
kind: Container                    # the other kinds are in config/samples/
metadata: {name: orders-api, namespace: webshop}
spec:
  softwareSystemRef: {name: webshop-core}
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: python-fastapi}
  standardRef: {name: python-standard}
  computeRef: {name: small}
  deploymentRef: {name: k8s-manifests}
status:
  phase: Ready
  repositoryURL: http://gitea.gitea.svc.cluster.local:3000/webshop/orders-api
  scaffoldedFrom: [python-fastapi@v1.3.0, python-standard@v1.0.0, small@v1.0.1, k8s-manifests@v1.2.0, postgres@v1.1.0]
```

A SoftwareSystem's `location` is `Internal` (the default: the factory builds
it) or `External` (the story only names it, such as a payment provider). An
External system holds no Containers or DataStores: one naming it stays
`Pending`, `ReferencesResolved=False`, reason `ExternalSystem`, and links to
and from it are story only.

## Scaffolding a Container

1. **Resolve references.** The SoftwareSystem (`Internal`), GitProvider and
   catalog entries must exist; until they do the Container is `Pending`,
   naming what is missing.
2. **Check choices.** Every entry used, the Connectors reached through
   Relationships included, must list the Container's type, else `Failed`,
   `ChoicesValid=False` ("python-fastapi does not support static-site"). A
   Framework with `deploysItself: true` (the AWS ones) takes no Compute and
   no Deployment but `gitea-release`, whose release carries its synthesized
   cloud assembly, else reason `DeploysItself`; and `gitea-release` ships a
   function, service or static-site only for such a Framework, else reason
   `UnsupportedType`.
3. **Create the repository** in the GitProvider's organization.
4. **Render the layers** in order: Framework, Standard, Compute, Deployment,
   then each Connector. Templates are Go `text/template` files given the
   names of the project, system, container, type and chosen entries, the
   Connectors, the CI system and, after the Framework, the paths it rendered
   (`{{ if has .FrameworkFiles "infra/operations.ts" }}`), so a layer tests a
   file, not a name or ref. Each CI system's files sit under
   `.c4factory/ci/<system>/` and render at the root only for the Container's;
   a template with CI files but none for it fails, `UnsupportedCI`.
5. **Assemble the devcontainer:** the Framework's settings plus a compose
   service per Connector.
6. **Write `.c4factory.yaml`** with the project, the container and the
   scaffold's refs.
7. **Push one commit,** protect `main` with the Standard's required checks,
   then set `Ready`, `repositoryURL` and `scaffoldedFrom`.

Every file belongs to exactly one layer: two layers writing a path fail the
Container, naming it. An empty existing repository gets the commit again; one
with commits the operator didn't make stops it `Failed`. A `Failed` Container
tries again when something it depends on changes, or when its
`c4.factory.io/retry` annotation is set or changed.

**Relationships.** Container to DataStore applies the DataStore's Connector at
scaffold. Container to Container with `spec.bundles: true` says the source
ships a release of the target ([Crews](#crews-and-releases)). Every other link
is story only. One added after the scaffold shows `Applied=False`,
`AddedAfterScaffold`. A Relationship's ends and a DataStore's Connector can't
change, so `Applied` keeps meaning what the scaffold holds.

**Status.** A Container is `Pending`, `Scaffolding`, then `Ready` or `Failed`,
with conditions `ReferencesResolved`, `ChoicesValid`, `RepositoryCreated`,
`Scaffolded` and those below. SoftwareSystem and Project roll counts up;
Project status carries `factory: {operatorVersion, chartVersion}`.

**Sessions.** A devcontainer feature installs a Go client that reads
`.c4factory.yaml`, creates a Session on its Container (actor and a one-line
summary), heartbeats every 30 seconds and deletes it on stop. The operator
marks one `Stale` after 2 minutes without a heartbeat and counts the Active
ones. The client's per-namespace ServiceAccount may only write Sessions.

## Agents and Tasks

A Task is a goal for a Container, usually an issue of its repository
(`spec.issue`). The operator runs the implementer Agent in an
[Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) on branch
`task/<name>`, opens a pull request, waits for CI, runs the reviewer, and
squash-merges on approval. Failed checks, requested changes or a failed run
start the next round with that feedback, up to the Agent's `maxRounds`.
Phases: `Pending`, `Implementing`, `Checking`, `Reviewing`, `Blocked` (waiting
on a Standard change), then `Merged` or `Failed`.

- **Issues are the work.** The operator labels the issue
  `status/in-progress` (`status/*` labels are exclusive), reads it before
  every run and closes it on merge; a failed Task hands it back
  `status/needs-human` with the reason. An issue without an *Acceptance
  criteria* checklist goes back before any tokens are spent.
- **Merges only on checks of the `main` it merges into.** When `main` moved,
  the operator merges it into the branch (no agent run, no round) and waits
  for the checks again; a conflict goes back to the implementer.
- **The implementer runs the check in its sandbox,** sent back twice while it
  fails. One that fails on the base commit too (`CheckFailsOnBase`) is left
  to CI, with the output on the pull request.
- **Blocked is said, not guessed.** An implementer that can't do the issue
  yet, or not in the sandbox, changes nothing and ends with `Blocked: <why>`;
  the Task fails at once, reason `Blocked`, with those words on the issue. A round
  with no change and no such line fails with the end of the agent's summary.
- **The infrastructure's failures cost the issue no try:**

  | Reason | What happened | The Task |
  | --- | --- | --- |
  | `AgentUnavailable` | usage limit, overloaded or 5xx model API, rejected credentials | waits, then runs the step again; no round |
  | `Interrupted` | node shutdown, eviction, pod deleted, Git host unreachable past the runner's retries | runs again in the round, at most twice |
  | `NoCIResult` | no CI run started in 30 minutes, or none ended in its bound (6 h queued, 3 h 15 running) | fails |
  | `RunnerOutOfSpace` | `no space left on device` in a job log | fails; no round |
  | `RunnerOutOfMemory`, `RunnerLost` | a job killed for memory, or abandoned by its runner | reruns the jobs twice a round, then fails |
  | `ApprovalRanOut` | CI outlived a Standard change's approval four times | fails |

  An agent killed for memory or past its deadline fails its own round. A job
  a person cancelled costs the round.
- **Roles get their own model and guide.** An Agent has a `harness` and a
  `model`. A Standard ships `docs/agents/implementer.md` and `reviewer.md`,
  read from the base branch so a change can't rewrite its own review. The
  reviewer reports each criterion and may name up to three follow-ups, filed
  `status/proposed` in the *Later* milestone.
- **Context comes from the model:** the system's needs and KPIs, its users,
  the Container's Relationships, read-only clones of related repositories,
  and `docs/project-notes.md`, the project's own word to its agents.
- **Evidence is never made up.** A criterion no sandbox can meet (a desktop,
  a device, an account) is left open with why, never faked; the operator
  files those as a `kind/by-hand` issue for a person.
- **A protected file without a person.** An issue needing a file a Standard
  protects (the Makefile, a CI workflow) gets a gated Task: the operator files
  a `kind/standard-change` issue, the waiting Task is `Blocked`, and the
  operator merges the reviewed change after its checks. It never weakens a
  check (no target, test, lint rule or CI job removed or skipped): the
  implementer stops with a `Blocked:` line instead. The agents' rules and
  `.c4factory.yaml` stay out of reach.
- **A Task keeps its record:** `status.history`, `status.runs` per agent run,
  the cost (`status.costMilliUSD`) and tokens (`status.tokens`, with
  `largestSession`) its agents reported. `WaitingForSlot` says it waits for
  an agent slot. The Work area charts the runs against their Agent's
  `sessionTokenLimit` ([plan](plans/done/token-chart.md)): the line and the
  "Past the limit" counts use the Agent's current limit and the Task's current
  Agent, since a run records neither, so changing either rejudges past runs.
- **A long session hands off.** Past its Agent's `sessionTokenLimit` (default
  130000) a session writes where it stands and stops; the runner, `c4agent`,
  starts a fresh one with that note (`status.tokens.sessionHandoffs`).

### The fence

The sandbox pod has no ServiceAccount token and reports through its
termination message. A per-Container bot user on the Git host writes to that
repository only. `main` takes no pushes: only the operator merges, the
reviewed commit only; the Git host refuses changes to the platform's and the
Standard's protected files (the gated merge aside) and keeps `v*` tags to the
operator. A per-project NetworkPolicy allows only cluster DNS (where `--cluster-dns`
says it runs; kube-dns in kube-system by default), the Git host and public
HTTPS. Each run has a deadline. The factory's token never enters a sandbox: a
sandbox on the shared token reaches Claude through the model proxy in the
release namespace, which adds the token to the agent's requests, and the fence
lets it reach the proxy alone
([plans/done/model-proxy.md](plans/done/model-proxy.md)). The token's one
place is that Secret in the release namespace; the operator never copies it
into a project namespace, and deletes the copies an earlier release made. An
Agent naming a Secret of its own gets that Secret's keys as the sandbox's
environment, readable inside it.

### The CI fence

CI runs the agents' code too. A job running a Container repository's code runs
in an environment made for it and thrown away; holds only its job token and its
repository's secrets; shares nothing writable with another project's jobs or a
release; and reaches only DNS, the Git host (and its registries) and public
HTTPS. Every CI adapter meets this or says which part it can't
([plans/ci-runner-isolation.md](plans/ci-runner-isolation.md)).

The Gitea runners meet the network and the token: a NetworkPolicy on their pods
(`app.kubernetes.io/component: ci-runner`) lets nothing in and only cluster
DNS, Gitea's port 3000 and public HTTPS out, and the registration token goes
only to the init container; `make validate-yaml` checks both. Its DNS peers
are the chart's `clusterDNS`, the same entries the operator gets as
`--cluster-dns`, so a cluster whose pods resolve through NodeLocal DNSCache or
through DNS in another namespace sets it once for both fences (#1257). The `image` runner builds
release images (#1069) with rootless BuildKit: its job container alone runs
seccomp and AppArmor `Unconfined`, which BuildKit's user namespaces need, as uid
1000, never privileged, with no added capability and no node directory;
`make validate-yaml` checks that too. Each job starts its own BuildKit, its
state in the job's temporary directory, removed from inside BuildKit's user
namespace when the build ends, since the runner outside it can't remove a file
a `RUN` step gave another user.

A project's checks run on its own pool of single-job runner pods, which the
operator keeps in the project's namespace, one pool per label the project's
GitProvider `ci` block names, registered `--ephemeral` to the project's Git
organization so each pod takes one job and ends. The operator deletes an
ended pod and starts a fresh one; the pool goes with the Project. The pods
sit under the project's own CI NetworkPolicy (no ingress; DNS, the Git host's
namespace and public HTTPS out), hold no ServiceAccount token, get the
registration token only in an init container, and the `go` label's pods
mount a Go build cache of the project's own on the node. The shared runners
Helm installs register to the templates organization and serve the
templates' checks only; the GitProviders the chart renders carry a pool of a
`base` and a `go` runner, the labels the templates' checks run on, so an
install needs nothing added for a project's checks to run. Who cancelled a job is read from its pod: a pod that
ended its one job on its own after the cancel was a person's; one deleted,
evicted or killed before its job ended was the runner's, and the Task reruns
the checks at no cost to a round (`model.Canceller`). The isolation left is
the node's: the pods share it with everything else
([getting-started](getting-started.md#what-the-factory-trusts)).

## Crews and releases

A Crew per Container names the implementer and reviewer Agents, polls the
repository's `status/ready` issues and starts up to `parallel` Tasks (one by
default; `Blocked` ones don't count), with three tries per issue.
`spec.paused` stops new starts; running Tasks are never stopped.

- **Order.** An issue waits for its Git host dependencies. `priority/urgent`
  goes first, even past `parallel`; then a Standard change a `Blocked` Task
  waits on; then releases, due first first, the next taking places the
  current can't use; then undated milestones, then none.
- **A try continues an open pull request.** A failed Task's pull request stays
  open while a later try may continue it on that branch, with its feedback
  (`status.continues`); the operator closes it, saying why, once none will.
- **Routes by label.** `spec.routes` sends issues with a label to another
  implementer or reviewer (`kind/docs` to a cheaper model); per role, the
  first matching route wins, else the Crew's Agent. The Task records the
  Agents it got. A missing Agent leaves the Crew `Ready=False`,
  `AgentNotFound`.

A milestone titled `vMAJOR.MINOR.PATCH` with a due date is a release.

- **Tagging** (`spec.tagReleases`). A release with no open issue and no Task
  at work is done: the Crew tags it at its newest merged issue's merge commit
  and closes the milestone. Tags go out in due-date order, never move, and
  never go behind the previous release (`BehindPreviousRelease`).
  `status.lastRelease` records it; `Released` says why one waits on a person.
- **Preparing** (`spec.prepareReleases`). A done release with work a Task
  merged gets `Prepare vX.Y.Z`, `kind/release`, listing what the tag carries;
  its agents set the version files (`make release-version`) and the
  changelog. It starts once no other Task works and runs alone
  (`PrepareWaitsForTasks`). Work merged after it leaves the release to a
  person (`MergedAfterPrepare`).
- **The full check.** `Container.spec.checks` is `EveryChange` (the default:
  CI runs `make check` on every change) or `BeforeRelease`: changes run the
  Standard's `make check-light` and the full check runs on
  `release-check/vX.Y.Z` before the tag (`FullCheckRunning`,
  `status.fullCheck`); a failure files `Full check fails for vX.Y.Z` and tags
  nothing. It applies only where the Standard sets `checksBeforeRelease` and
  the Makefile has `check-light`; `ChecksApplied` says why not.
- **The release run.** `status.lastRelease.run` sums the runs the tag started
  (`Waiting`, `Running`, `Failed`, `Succeeded`), with Events
  `ReleaseRunFailed` and, past an hour, `ReleaseRunOverdue`. Setting or
  changing `c4.factory.io/rerun-release` on a `Failed` run re-runs its failed
  jobs, once per value.
- **Bundles.** Once B's release is built (and published, if B publishes),
  each Container A with a `bundles: true` Relationship to B gets a ready
  `Bump B to vX` issue, `kind/bump`, in A's next unprepared release.

### Publishing a release

The operator moves bytes with credentials it holds; it builds nothing.

- **A Go module.** A tagged repository with a root `go.mod` is published to
  the Git host's Go registry (`status.lastRelease.goModule`).
  `ModulePublished` turns True once `go get <path>@<version>` finds it: a
  `c4factory.local/` path through the registry, any other through the public
  proxy (`--go-proxy`), else `NotFetchable` or `ProxyUnavailable`. Agent pods
  and the Go Standard's CI fetch `c4factory.local/...` from the registry.
- **npm and PyPI packages** (#1087): the release's npm tarballs, wheels and
  sdists, published once to the npm registry and the PyPI index
  `GitProvider.spec.packages` names, by default the Git host's own, by the
  Crew once the release runs succeed. `status.lastRelease.packages` says how
  it went (`Waiting`, `Published` with one `{ecosystem, name, version}` per
  package, `NoPackage`, `Invalid` when a file's version isn't the tag's,
  `Failed` while the host can't be reached, tried again each poll), and the
  condition `PackagesPublished` turns True at `Published` or `NoPackage`; an
  upload is a Normal Event `PackagePublished`, an invalid file a Warning
  `InvalidPackage`, once per release. npm packages are named
  `<scope>/<container>`; the scope is a setting there too, `@c4factory` by
  default, and a tarball outside it is left alone and named in the message
  with the scope it lacks. The factory claims no name on
  npmjs.org or any public registry (#1234): by default nothing leaves the
  cluster, and a user who publishes elsewhere sets a scope they hold, their
  registry's and index's URLs and a credential Secret of their own
  ([plans/done/app-packages.md](plans/done/app-packages.md)).
- **A copy on GitHub** (deprecated by #1070). `spec.publishReleases.github`
  (a repository and a token Secret for it alone) copies the tag, notes and
  files once the runs succeed; the operator does it, not CI, so the token
  never meets the agents' code. It refuses a renamed or replaced target
  (`Published=False`) until `c4.factory.io/accept-repository: <id>` accepts
  it. The same copy is a `github` DeployTarget's (the Deploys section), and
  the Crew's field ships through the same code path, its `Published`
  condition and reasons kept. The field stays valid and working in
  `v1alpha1` (Compatibility); dropping it is `v1beta1`'s (#1082). To move
  over by hand: a DeployTarget with the same repository and the same Secret
  (key `token`, unchanged), a `spec.deploy` entry on the Container, then the
  field removed. Both may exist at once: each finds the release the other
  made and leaves it alone. A Brief's `releases[]` keeps writing the field
  on its Crews until a Brief can name DeployTargets.
- **The source on GitHub** (deprecated by #1070). `publishReleases.source.path`
  also commits the app's source at its tag into that folder, without the
  factory's own files (`model.FactoryOwnPaths`), tagged `<path>/vX.Y.Z` so
  `go install` finds it. Crews publishing to one repository may not overlap
  (`FolderOverlaps`). It moves over with the copy, as the target's `source`.

## Standards and the catalog

**A Standard** is the rules as a layer: lint and format config, `AGENTS.md`
and the role guides, the `make check` contract and its CI workflow. A change
touching only the changelog, `docs/` or images passes CI without `make check`.
`status.checks` reports CI on `main`.

**A Standard's fixes reach its Containers.** Bumping its pinned `ref` updates
every Ready Container on it: the operator renders the Standard at the old and
new ref and opens a pull request with the difference, which it merges as
admin once CI passes. A file the repository changed since is merged three-way
when the changes don't overlap; one that overlaps is left out, listed in
`status.standard.behind` with its diff (`StandardUpdated=False`,
`FilesBehind`), until a person sets `c4.factory.io/standard-take` (the
operator merges the Standard's version) or `c4.factory.io/standard-keep`
naming the ref. No agent takes part. `scaffoldedFrom` keeps the scaffold's
refs, `status.standard` the current.

An update whose CI fails (`ChecksFailed`) is the Crew's to fix
([plans/standard-update-fails.md](plans/standard-update-fails.md), #1447).
The fix lands on main in the Crew's own pull request; once main is no longer
at the head the update was planned from (`status.standard.update.base`), the
operator closes the update's pull request and plans it again from the new
main (#1489). The operator files `Standard <name> <ref> fails this
repository's check`, `kind/bug` and `status/ready`, in the repository's next
unprepared release, once per target ref (`status.standard.failureIssue`),
linking the pull request and the run (#1491). A check a Standard tightens
says in its message what to write.

**The templates** live in `templates/`; [templates.md](templates.md) scores
each. The toolchain images (`base`, and `go` with Go, Node, the kubebuilder
tools and envtest) run every check without a cluster.

- **`operator`:** Framework `go-kubebuilder`, Standard
  `go-operator-standard`, Compute `small`. The factory never installs it;
  Deployment `gitea-release` ships its binaries, `install.yaml` and
  `SHA256SUMS` on a version tag, with the image its release built
  (#1458): `install.yaml` names it by digest on the Git host's registry,
  where the operator pushes it.
- **AWS** (`function`, `service`, `static-site`, `worker`, `job`): the
  `aws-*` Frameworks, each a CDK app in TypeScript that deploys itself, with
  the `aws-standard` Standard; `make check` synthesizes the stacks offline
  under CDK Nag and cfn-lint, and CI deploys nothing. The `dynamodb`
  Connector adds a table to the stack.
- **The tier is where an AWS app runs.** Every AWS repository synthesizes
  both; a deploy target picks one, a hand deploy is `development`.
  `production` keeps and protects data, with point-in-time recovery, a daily
  backup plan, alarms, X-Ray and a year of logs; `development` drops the
  costly parts. What the app promises in any tier is
  `Container.spec.operations` (retention, copy region, RPO, RTO, who alarms
  tell), rendered into `infra/operations.ts`, its defaults from
  `internal/model`; a change reaches the repository by a pull request the
  operator merges (`OperationsApplied`). A Framework that renders the block
  says so, `readsOperations: true`; a Container on any other Framework shows
  no `OperationsApplied` until the block is set, and then
  `False`/`NotReadByFramework`, naming the Framework, and a Brief's planner
  refuses `operations` on it. Each has `docs/runbook.md`
  ([plans/aws-production.md](plans/aws-production.md)).

## Briefs: an idea in plain words

A Brief lets a person give an app idea in plain words and steer with
follow-up prompts; a planner Agent turns it into the model, Crews and first
issues, and says what it chose ([plans/done/brief.md](plans/done/brief.md)).

- **One more writer of the model.** `spec.idea` is immutable; `spec.prompts`
  lists at most 50 follow-ups, and appending to a full list retires the
  oldest planned ones (`spec.promptsRetired`); a CEL rule refuses a write that
  loses an unplanned prompt. The operator never writes the spec.
- **A planner Agent plans each round** (the idea, then each batch of new
  prompts or requests) as a Task, `spec.kind: Plan`, in the Brief's own
  repository, where the operator writes `brief.md`, the current model and
  the catalog. It pushes the story kinds, the issues and its choices
  (`plan/summary.md`).
- **The operator checks and applies the plan.** A plan writes only story
  kinds and installed entries that fit, changes only story text of what
  exists, adds nothing to an External system and deletes nothing; else the
  next round gets the reasons. The operator makes a Crew per new Container
  and files the issues once it is Ready, ordered by their `after:` front
  matter as Git host dependencies. What it made carries the label
  `c4.factory.io/brief`. A failed step says where and why
  (`Planned=False`, `ApplyFailed`).
- **One app uses another's release, never its branch.** An issue's `uses:`
  names a related Container; it turns ready once that app's release has
  settled and published something other apps install, a Go module, an npm
  package or a PyPI package, and its body gets a line per thing published
  saying how to fetch it (`go get`, `npm install --save-exact`, `uv add`
  pinned to the Git host's index). A settled release that published nothing
  keeps the issue waiting, and the Brief says why.
- **It asks only for what costs money or touches access.** Agents, models,
  GitProviders and credentials are never the planner's; it says what it needs
  in `NeedsInput`. The Crews' `parallel` (`spec.parallel`, else a prompt's
  number, else 3) and a Container's `checks` change only when the person asks.
- **Releases when the plan says so** (`plan/releases.yaml`, or `spec.releases`
  naming a Container with a GitHub repository and token Secret). Fixes go
  into the next patch release, other work into the next minor. A Crew whose
  target was edited away from the Brief's publishes nothing until they agree
  (`ReleaseTarget=False`, `DiffersFromBrief`).
- **Requests from GitHub.** An entry that takes requests plans the allowed
  authors' GitHub issues like prompts, answers each, and closes it once
  shipped.
- **The person answers in prompts.** Reviewers' suggestions
  (`status/proposed`) and failed issues (`status/needs-human`) in the Brief's
  apps are listed in its status; a plan answers only those a new prompt
  speaks to (`take`, `skip`; `retry`, `close`). Nothing unanswered is built.
- **Autopilot** (`spec.autopilot`: `Off`, `Ask` by default, `On`). A tending
  round, at most hourly and with no new prompt, answers a due failed issue
  `retry`, `close`, `wait` or `ask`; in `Ask` each waits in
  `status.needsYou` with the choice the Brief would make.
- **Pause.** `spec.paused` pauses the Crews the Brief made and starts no
  round; `Paused` names the Tasks still running. No issue may ask to change
  `model.AgentNeverPaths` (`AGENTS.md`, `docs/agents/`, `.c4factory.yaml`).
- **Follow-ups keep scaffold once:** a change to a scaffolded Container is an
  issue in its repository, a new Relationship's wiring too.
- **Its status tells the story:** each round's summary for the person, its
  choices, cost and tokens, what it made, and each app's latest release.
  Deleting a Brief deletes its Git host bot, keeping repository and model. A
  planner with `harness: stand-in` plans from the catalog alone, for tests.

## The UI: areas and object pages

The UI draws the story and the factory from the resources, live through
watches, and enforces nothing: its writes are checked by the API server, whose
refusal it shows ([plans/done/ui-lifecycle.md](plans/done/ui-lifecycle.md)).

- **Six areas:** *Needs you* (what waits on a person, across projects),
  *Briefs*, *Story* (system context and one system's containers), *Work*
  (Tasks by stage, Crews and releases, time and tokens, what needs a look),
  *Factory* (health and agent slots) and *About*. It opens on *Needs you*
  when it has items, else *Story*.
- **Object pages** share one header (`ui/src/components/ObjectHeader.tsx`):
  kind, name and breadcrumb, the lifecycle as a stepper from
  `ui/src/model/lifecycle.ts`, what a person does now, then conditions,
  timeline, children and links. A step the UI can't take shows a link or a
  `kubectl` line to copy.
- ***Needs you*** lists what the resources flag (`ui/src/model/needs.ts`),
  each in its condition's own words, every item from `status`, none from
  timing.
- **Versions** of the UI, operator, chart and CRD API in every footer; a UI
  and an operator of different versions get a warning.
- **The factory's health** comes from the UI server (`GET /factory/health`,
  sampled every 15 seconds, an hour kept). No Prometheus or Grafana.

### UI actions

With actions off (the default) the UI server writes three things: create a
Brief, append a prompt, pause or resume one. With `ui.actions.enabled`, each
row below is one allowlisted request in `ui/server.mjs`, refused there for any
other path, method, content type or field, and by RBAC for anything else:

| Kind | Request | Fields |
| --- | --- | --- |
| Brief | `POST` | a new Brief |
| Brief | `PATCH`, JSON patch | append to `spec.prompts`; `spec.paused`, `spec.autopilot`, `spec.parallel`, `spec.releases` (after a `test` of the `resourceVersion`) |
| Crew | `PATCH`, JSON patch | `spec.paused`, `spec.parallel`, `spec.tagReleases`, `spec.prepareReleases`, `spec.publishReleases` (after a `test` of the `resourceVersion`) |
| Crew | `PATCH`, merge patch | annotation `c4.factory.io/rerun-release` |
| Container | `PATCH`, merge patch | annotations `c4.factory.io/retry`, `c4.factory.io/standard-take`, `c4.factory.io/standard-keep` |
| Container | `PATCH`, JSON patch | `spec.checks`; or one `spec.deploy` entry's `approved`, after a `test` of its `target` and a `test` of the approval the page showed: its old `approved`, or, when it had none, the whole entry as read (JSON Patch can't test a missing member), so a stale page fails, and says it is out of date, rather than overwrite a newer approval, a first one too |
| Task | `POST` | `kind: Implement`, `containerRef`, `agentRef`, `reviewerRef`, `goal` |
| Standard | `PATCH`, JSON patch | `spec.template.ref` |

Over `kubectl port-forward` the writes are the UI ServiceAccount's. Behind an
Ingress a person signs in with OIDC (code flow with PKCE) and every write
impersonates them, with prefixed names and groups, so the cluster's RBAC
decides; the chart refuses actions behind an Ingress without OIDC. The browser
never sees a token. Out of reach: deletes, Secrets, the Git host and editing
the model ([plans/done/ui-actions.md](plans/done/ui-actions.md)).

## Factory components: ports and adapters

The factory is built from parts a device can swap: another Git host, its own
Argo CD, later another CI or agent runtime. A port is cut by what the
controllers call, not by one product's API;
[architecture.md](architecture.md#ports) keeps one row per port.

| Port | What the operator asks of it | Today |
| --- | --- | --- |
| GitHost | repositories, files, branches, tags, branch rules, pull requests, merges, commit statuses | Gitea |
| Issues | issues, comments, labels, milestones, dependencies | Gitea; request intake on GitHub |
| CI | runs of a commit or tag, reruns, job logs, runners | Gitea Actions |
| AgentAccess | a credential for one repository, and revoking it | Gitea bot users |
| Registry | Go modules and the proxy; npm, PyPI and images to come | Gitea packages |
| AgentRuntime | start a run, read its state and result, stop it | Agent Sandbox |
| Deployer | ship a release to a target, read its state back | the GitHub release copy |

- **A port is a narrow Go interface** with one fake and one contract suite,
  plain Go with no Kubernetes in `internal/ports`; each adapter is its own
  package under `internal/adapters`, and only `cmd/main.go` imports adapters
  (`make lint` enforces it). The Deployer is there today; the Git host is
  still one interface, `gitprovider.Provider`, until #1094 splits it.
- **What a host lacks** its adapter emulates, or the port makes an optional
  capability (a second small interface, a condition reason when missing):
  Go modules, npm and PyPI, a project's own runners, webhooks.
- **What a CI system writes is a business rule:** one row per CI system in
  `model.CI` (workflow paths, required checks, protected paths); a
  Container's is its host's own (`model.HostCI`).
- **Polling is the contract.** Webhooks (#1084, decided, not built) would
  only shorten waits ([plans/git-events.md](plans/git-events.md)).
- **Helm decides what is installed, custom resources what is used:**
  `GitProvider.spec.provider` names the host (`gitea`), CI and registry
  default to the host's own (`spec.packages` names another npm registry or
  PyPI index, and the npm scope), a DeployTarget names each deploy's tool.
- **A user's credential never reaches code the agents wrote:** only the
  operator reads the GitProvider's, a Brief's and a DeployTarget's Secrets,
  and a registry Secret's `token`. The one exception is a registry Secret's
  `readToken`, which the user makes for installs alone: it reaches agent
  pods and CI as the agent bot's token does.
- **A port is swappable only once its fake and two real adapters pass its
  contract;** none is yet.

## Deploys: each release to its targets

CI builds what ships; the operator holds the target's Secret and hands each
release to the tool that owns the target. It never builds, runs repository
code or writes rollouts ([plans/done/deploy.md](plans/done/deploy.md)).

- **A place is a `DeployTarget`,** shared by any number of Containers: one
  member saying what it is (`github` and `argoCD` today;
  `cloudFormation` with #1071, `flux` later, each with its adapter), a
  `credentialsSecretRef` (optional only for an in-cluster `argoCD` target), a
  `tier` (`development` by default, or `production`), an `approval`
  (`Required` or `None`; empty is `Required` in production) and non-secret
  `parameters`. `Ready` says whether the target takes the credential. A
  `github` target's `tagPrefix` goes before each release's tag: unset, it is
  the Container's name and `-` (`orders-cli-v0.1.0`), so any number of
  Containers share a target; set, it is used as it is, `""` giving bare
  tags. Its `latest` (default true) says whether each release becomes the
  repository's Latest
  ([plans/done/github-deploy-state.md](plans/done/github-deploy-state.md)).
- **A Container names its targets in `spec.deploy`:** entries by `target`,
  with an optional `after` (an entry that runs a version first), `version` (a
  pinned tag, how a person rolls back) and `approved` (the version a person
  let in). The list never scaffolds.
- **On each release, in order:** the release its Crew tagged, once its runs
  succeed, to each entry after its `after` and its approval. A rollout the
  tool reports failed isn't retried by itself. Targets read files the
  Deployment or Framework rendered at scaffold; a Container without them is
  `Incompatible` there.
- **Reports:** `status.deploys[]` per target (version running and wanted,
  phase, the tool's health, URL), summed by `Deployed`.
- **Release images** (#1069): built by the `image` runner, pushed by digest,
  shipped by digest ([plans/images.md](plans/images.md)). When the
  Container's choice builds one (`model.BuildsImage`: `k8s-manifests`, and
  `gitea-release` for an `operator`), the
  operator pushes `status.newestRelease`'s `*.oci.tar` to the owner's
  registry once and reports it in `status.image` (`tag`, `repository`,
  `digest`, `platforms`, `state`, `message`, `publishedTime`) and
  `ImagePublished` (True when `Published`, else False with the state:
  `Pushing`, `NoImage`, `Invalid`, `TagTaken`, or `Failed`, retried each
  poll). The push runs beside the reconciler, one per Container, and the
  reconcile its end wakes writes the result, so a slow registry holds no
  Container's reconcile (#1572); a ship waits while it runs. Each
  ship's `Shipment.Image` is `<repository>@<digest>` of the version shipped,
  a pin's resolved in the registry.
- **GitHub targets** (#1070, [plans/github-deploy.md](plans/github-deploy.md)):
  a `github` target is how a Container is published as open source, for
  every type. It ships three things of each release, each by its own rule
  (`model.GitHubShipment`): the app's source at the tag, when the member's
  `source` is set; a GitHub release of the tag, always, with its notes and
  the files when the release has them, its image archive (`*.oci.tar`) left
  out; and the release's image on GHCR, when the Container publishes one
  and the Secret holds `packagesToken`. A release with none of the three is
  `ArtifactMissing`, "nothing to publish: no release files, no source, no
  image". The source is a
  snapshot, one commit per release into `source.path` (the Container's name
  by default, `"."` for the whole repository), without the factory's own
  files (`model.FactoryOwnPaths`), the release tag on the published tree and
  the folder tagged `<source.tagPrefix><version>` (`<path>/vX.Y.Z` by
  default, so `go install` finds it); never the history, whose agent
  commits and Gitea issue numbers don't belong in public. Two Containers
  whose folders overlap on one target fail `FolderOverlaps`: the one whose
  name sorts first keeps the folder, and the message names both. Before the
  first write of a version the tree to publish is checked
  (`model.PublishChecks`): a license and a README at the folder's root, and
  no secret the factory recognises (a private key block, a token of a known
  shape, the value of a credential the operator holds); a problem fails the
  entry `LicenseMissing`, `ReadmeMissing` or `SecretFound`, naming the file
  and line and never the value, until another version is wanted. The image
  goes by digest to `ghcr.io/<owner>/<repository>/<name>` (the repository
  alone when `tagPrefix` is `""`), tagged with the version, and
  `status.deploys[].image` reports it. The Secret holds `token`, a
  fine-grained token for the one repository, or a GitHub App's `appId`,
  `installationId` and `privateKey`, from which the operator mints
  installation tokens; `packagesToken` is a classic token with
  `write:packages`, since GHCR takes neither. GitHub Enterprise (a `url` on
  the member) and GitLab (its own member) come later.
- **Kubernetes targets** (#1072, [plans/k8s-deploy.md](plans/k8s-deploy.md)):
  an `argoCD` target names Argo CD's namespace and project and the
  destination; the operator writes one Argo CD `Application` per Container
  and version (the repository at the release tag, path `deploy/k8s`, the
  image by digest in `kustomize.images`, automated sync) and reads sync and
  health back into `status.deploys[]`. Nothing is written to the repository
  at deploy. It needs a `k8s-manifests` Deployment (`service`, `worker`,
  `static-site`, `job`); operators stay out. The factory never installs
  Argo CD. Argo CD runs on the factory's own cluster unless the target's
  Secret holds a `kubeconfig`, from which the operator reaches another
  cluster's. There, `gitURL` is the Git host as that cluster sees it, for
  each `Application`'s `repoURL`, and `imageRegistry` is a registry its
  nodes can pull from: the operator copies the image there by digest,
  tagged with the version, with the Secret's `registryUsername` and
  `registryPassword`, and `kustomize.images` names the copy.
- **AWS targets** (#1071, [plans/aws-deploy.md](plans/aws-deploy.md)): a
  `cloudFormation` target names a region and, as a guard, an account. CI
  synthesizes the CDK app and the release carries the cloud assembly
  (templates, manifests and file assets, no image); the operator, with the
  target's credential, assumes the CDK bootstrap roles, publishes the assets
  and creates or updates the stack through one CloudFormation change set,
  bootstrapping the environment when the credential may (else *Needs you*
  says the command). State comes from the stack's status and its newest
  event, the URL from the `EntryPoint` output. The factory never deletes a
  stack. Fargate's image comes by digest from the Registry (#1069).

## Distribution and issues

This repository develops the factory and holds no product. A `vX.Y.Z` tag
publishes the Helm chart at `oci://ghcr.io/jairjosafath/c4factory/charts` and
its images on GHCR. The release installs:

| Part | Where | Notes |
| --- | --- | --- |
| Operator, CRDs, RBAC, admission policy | release namespace | always |
| UI | release namespace | `ui.enabled`, on by default; actions off |
| Gitea and its CI runners `base` and `go` | `gitea.namespace` | `gitea.enabled`, on by default; secrets generated in the cluster, never rendered by Helm |
| Seed job | Gitea's namespace | after every install and upgrade: templates, a GitProvider and Project per `gitea.projects` entry, the catalog and Agents applied in place |
| Project namespaces | each of `gitea.projects` | made when missing, labeled `c4.factory.io/shared-secrets: "true"`, kept on uninstall |
| Shared Secrets | copied from the release namespace | the pull Secrets; a project's own Secret of that name wins. The Agents' token is read by the model proxy there and never copied |
| GitProviders | named namespaces | `gitProviders`, for a Gitea the release doesn't run |

A device installs Agent Sandbox first and gives the Agents' token once. An
upgrade leaves running work alone
([plans/done/upgrade-in-place.md](plans/done/upgrade-in-place.md)). A device
reports issues through the form *Report from a device*.

## Compatibility

**An object a client stored keeps validating across upgrades.** All of
`c4.factory.io` serves `v1alpha1` only, and a release keeps every `v1alpha1`
object of the one before valid and meaning what it meant. Within `v1alpha1` a
release never:

- removes or renames a field, or changes its type;
- adds a required field, or makes an optional one required;
- removes an enum value;
- tightens a CEL rule or a bound so an object that validated before fails;
- changes what a default, or an unset field, means.

Adding an optional field, an enum value, a condition type or reason, or a kind
is fine. A breaking change waits for `v1beta1` (#1082), served beside
`v1alpha1` with conversion. The status is held to it as the spec is; what the
operator does with an object may change, as the release notes say.

`make check` guards what a schema shows: `test/compat` compares the CRDs with
the last release's, in `test/compat/baseline/` (`hack/release.sh` copies
them), a default's value included. CEL rules, a default added to a field that
had none, and what the operator reads into an unset field are left to review.

## Test tiers

Each rule is verified at the lowest tier that can see it; `make check` runs
the table, offline YAML and envtest tiers before every merge. A Kind cluster exists only when a
human asks; agents never create, lease or deploy to one. What only a real
node would catch is not tested before release.

| Tier | Verifies |
| --- | --- |
| Table tests (`make test`) | `internal/model`, every template's rendering in `internal/scaffold`, and the sandbox and pod specs the controllers build, the fence included. |
| Offline YAML (`make validate-yaml`) | Samples, fixtures, kustomize builds and the chart against the CRDs and their create-time CEL rules; the CI fence. |
| envtest (`make test`) | Reconcilers on a real API server under the generated RBAC role: status, ownerReferences, the admission policy, every CEL rule's failing case, the reaction to faked Sandbox and pod status. |
| Components (`make test-gitea`, `make seed-local`) | The Gitea API, the seed and Actions, against the Gitea binary the cluster runs. |
| Kind (`make e2e`, a human only) | The whole loop on stand-in agents: a page scaffolded, planned from a Brief, a Task through two rounds to a merge, a Standard bump carried to main. |

The Kind environment, the `config/samples/webshop/` story and the e2e fixture
`test/e2e/testdata/apps` are in [dev/testenv.md](dev/testenv.md).

## Out of scope and open points

- **Out of scope:** existing repositories and monorepos; Component and Code
  levels; provisioning data stores (a Connector only scaffolds: the files
  it writes may run the store, as postgres's StatefulSet or dynamodb's table,
  but the operator creates none); monitoring the apps beyond what a deploy
  target's tool reports.
- **One Git host.** Gitea is the only one built; another plugs into the
  [ports](#factory-components-ports-and-adapters) (#1094).
- **Shared files between layers.** A Connector often needs a line in a file
  the Framework owns, so the Framework gets the Connector list and wires the
  ones it knows. Revisit if that couples them too much.
- **Session credentials.** A client may touch any Session in its project;
  short-lived tokens would fix it.
- **Choices are checked after creation,** so an invalid Container exists
  briefly; a webhook could reject it up front.
- **Trust.** Until the gaps in [The fence](#the-fence) and
  [The CI fence](#the-ci-fence) close, one install is one trust domain.
