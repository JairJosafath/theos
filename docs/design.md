# C4 Factory: Design

Sep 30, 2026 · @Jeremy Jo.

[![How C4 Factory works today: describe, scaffold once, build from issues, keep current, watch, the fence and where it runs](images/how-it-works.png)](images/how-it-works.svg)

## Purpose

C4 Factory turns an idea into a working, scaffolded repository by first telling its story as a C4-inspired model. The model lives in Kubernetes custom resources, a kubebuilder operator acts on it, and a UI draws the story on top.

- **Storytelling first.** The model shows who uses what, what exists and why, and who is working on it right now.
- **The CRDs are the truth.** Anything can write them: kubectl, GitOps, agents or the UI. The UI enforces nothing; the rules live in the CRD schemas and the operator.
- **A thin factory.** Open-source tools do the real work: uv for Python, each framework's own tooling, ArgoCD or Flux for shipping. The factory scaffolds and reports.
- **Humans and agents are equal.** Both work in devcontainers, and both show up in the model as Sessions.

## Decisions so far

These were settled in the design discussion on 30 September 2026 and shape everything below.

| Topic | Decision |
| --- | --- |
| Source of truth | The custom resources in the cluster. The UI is one client among several. |
| Project scope | One Project per namespace. No references across namespaces; reuse is by copying. |
| Levels | Person, SoftwareSystem, Container and DataStore. Component and Code are dropped for now. |
| Container | An application with its own repository. One repository per Container, no monorepos. |
| Container type | A fixed list: `static-site`, `service`, `job`, `worker`. |
| Catalog | One entry per side of a Container (Framework, Compute, Deployment, Connector), kept per namespace. Bundles can come later, as a team choice. |
| Compatibility | Each catalog entry lists the Container types it supports. The operator checks this before scaffolding; no webhook in v1. |
| Templates | Catalog entries point to platform-managed template repositories at a pinned tag or commit. |
| Repositories | New work only. The operator creates each repository; adopting existing ones is out of scope. |
| Git provider | Its own kind, `GitProvider`, per namespace. Every Container references one. |
| Scaffolding | Once, at creation. Staying compliant afterwards is the Container owner's job. Status records the template versions used. |
| Data stores | Their own kind, `DataStore`, backed by a Connector. A Relationship from a Container to a DataStore adds that Connector to the Container's scaffold. |
| Shipping | Outside the operator. Deployment templates produce standard files that existing tools pick up. The one exception: the operator can copy a Container's releases to a GitHub repository the user names. |
| Sessions | Devcontainers run anywhere. A small client inside registers a Session on its Container and reports minimal status. |
| Status | v1 shows only the factory's own steps plus active Sessions. Runtime health comes later. |

## The model

Seventeen namespaced kinds in `c4.factory.io/v1alpha1`: six tell the story, four track work (Session, Task since v2, Crew since v4, and Brief), and seven configure the factory. Every child names its parent in `spec`; the operator turns that into the ownerReference, so deletion follows the tree.

&#91;embedded content: ownership tree · 9 boxes, 1 relationship\]

Solid arrows mean "owns": deleting a parent deletes its children. The dashed Relationship is the only link that changes a scaffold. Containers also reference a GitProvider and catalog entries, which isn't drawn here.

| Kind | Owned by | Parent field | What it is |
| --- | --- | --- | --- |
| Project | nothing | none; name equals the namespace | The whole story, one per namespace |
| Person | Project | none | A user, role or stakeholder |
| SoftwareSystem | Project | none | A system that delivers value, with its needs and KPIs |
| Container | SoftwareSystem | `spec.softwareSystemRef` | An application with its own repository |
| DataStore | SoftwareSystem | `spec.softwareSystemRef` | A database or storage, backed by a Connector |
| Relationship | its source element | `spec.source` | A directed link between two elements |
| Session | Container | `spec.containerRef` | An agent or human at work in a devcontainer |
| GitProvider | Project | none | Where repositories are created, with credentials |
| Framework, Compute, Deployment, Connector | Project | none | Catalog entries: a pinned template plus the Container types it supports |
| Standard (v2) | Project | none | Catalog entry: coding rules, checks and CI, rendered right after the Framework it fits |
| Agent (v2) | Project | none | Who does Tasks: an image with the agent CLI and toolchain, credentials and limits |
| Task (v2) | Container | `spec.containerRef` | A goal for a Container, implemented, checked, reviewed and merged by agents |
| Crew (v4) | Container | `spec.containerRef` | The Agents that implement and review a Container's issues, and pick up the ready ones |
| Brief | nothing | none | An app idea in plain words and its follow-up prompts, which a planner Agent turns into the model ([Briefs](#briefs-an-idea-in-plain-words)) |

The story kinds, abbreviated:

```yaml
kind: SoftwareSystem
metadata: {name: webshop-core, namespace: webshop}
spec:
  displayName: Webshop Core
  description: Catalog, cart and ordering
  needs: [Customers can reorder in two clicks]
  kpis: [Checkout conversion]
---
kind: Container
metadata: {name: orders-api, namespace: webshop}
spec:
  softwareSystemRef: {name: webshop-core}
  displayName: Orders API
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: python-fastapi}
  computeRef: {name: small}
  deploymentRef: {name: k8s-manifests}
status:
  phase: Ready
  repositoryURL: http://gitea.gitea.svc.cluster.local:3000/webshop/orders-api
  scaffoldedFrom: [python-fastapi@v1.3.0, small@v1.0.0, k8s-manifests@v1.1.0, postgres@v1.1.0]
  activeSessions: 1
---
kind: DataStore
metadata: {name: orders-db, namespace: webshop}
spec:
  softwareSystemRef: {name: webshop-core}
  displayName: Orders DB
  connectorRef: {name: postgres}
---
kind: Relationship
metadata: {name: orders-api-to-orders-db, namespace: webshop}
spec:
  source: {kind: Container, name: orders-api}
  target: {kind: DataStore, name: orders-db}
  description: Reads and writes orders
```

The factory kinds, abbreviated:

```yaml
kind: GitProvider
metadata: {name: gitea, namespace: webshop}
spec:
  provider: gitea                  # the only value in v1
  url: http://gitea.gitea.svc.cluster.local:3000
  owner: webshop                   # organization for Container repositories
  credentialsSecretRef: {name: gitea-credentials}
---
kind: Framework                    # Compute, Deployment and Connector look the same
metadata: {name: python-fastapi, namespace: webshop}
spec:
  displayName: Python, FastAPI (uv)
  supportedTypes: [service]
  template:
    url: http://gitea.gitea.svc.cluster.local:3000/templates/python-fastapi
    ref: v1.2.0                    # tag or commit, never a branch
---
kind: Session
metadata: {name: orders-api-k7f3q, namespace: webshop}
spec:
  containerRef: {name: orders-api}
  actor: agent                     # or human
  summary: Adding order validation
status:                            # written by the client; the operator only marks it Stale
  phase: Active
  lastHeartbeat: "2026-09-30T09:12:00Z"
  openInEditor: ""                 # set only when the devcontainer is reachable
```

Fields stay minimal on purpose. Metadata such as `displayName` and `description` is on every story kind; `metadata.name` is the stable id and can't change.

## How it works

A Container is scaffolded exactly once, from the catalog entries it picks and the DataStores it points to. After that, the factory reports, and carries only its Standard's updates to it (since v4, [A Standard's fixes reach its Containers](#v4-issues-crews-and-roles)).

### Scaffolding a Container

1. **Resolve references.** The SoftwareSystem, GitProvider and catalog entries must exist. Until they do, the Container stays `Pending` with the missing name in its condition.
2. **Check types.** Every catalog entry used, including the Connectors reached through Relationships, must list the Container's type. If not, the Container is `Failed` with `ChoicesValid=False`, for example "python-fastapi does not support static-site".
3. **Create the repository** in the GitProvider's organization, named after the Container.
4. **Render the layers** in a fixed order: Framework, Compute, Deployment, then each Connector. Templates are Go `text/template` files that receive the project, system, container and type names, the names of the chosen Framework, Compute and Deployment (empty when the Container picks none), plus the list of Connectors.
5. **Assemble the devcontainer.** The operator writes `.devcontainer/devcontainer.json` itself: the Framework's base settings plus one compose file per Connector, such as a Postgres service for local development and tests.
6. **Write the identity file** `.c4factory.yaml` with the project and container names.
7. **Push one initial commit,** then set `Ready`, `repositoryURL` and `scaffoldedFrom`.

Two rules keep this safe to retry. Every file belongs to exactly one layer, so two layers writing the same path fail the Container with that path in the message. And if the repository exists but is empty, the operator retries the commit; if it has commits the operator didn't make, it stops with `Failed`. A `Failed` Container tries again when something it depends on changes (its spec, a catalog entry, the GitProvider, a DataStore or a Relationship), or when someone sets or changes its `c4.factory.io/retry` annotation, for example after emptying the repository.

### Relationships

- **Container to DataStore:** applies that DataStore's Connector at scaffold time.
- **Everything else** (Person to SoftwareSystem, Container to Container): story only in v1.
- **Added after scaffolding:** not applied, by design. The Relationship shows `Applied=False` with reason `AddedAfterScaffold`, so the gap is visible. That includes a Relationship deleted and recreated under the same name with another target: the Container records which DataStore and Connector each applied Relationship added.
- **Fixed ends:** a Relationship's source and target, and a DataStore's Connector, can't change, so `Applied` keeps meaning what the scaffold holds. To point a link elsewhere, create a new Relationship.

### Sessions

1. A devcontainer feature installs the client, a small Go CLI.
2. On start, the client reads `.c4factory.yaml` and creates a Session on its Container, with the actor (agent or human) and a one-line summary of the work.
3. It sends a heartbeat every 30 seconds and deletes the Session when the devcontainer stops.
4. The operator marks a Session `Stale` after 2 minutes without a heartbeat and counts the Active ones on the Container.

In v1, the client authenticates with a per-namespace ServiceAccount that may only create, update and delete Sessions.

### Status

- **Container phase:** `Pending`, `Scaffolding`, then `Ready` or `Failed`, with conditions `ReferencesResolved`, `ChoicesValid`, `RepositoryCreated` and `Scaffolded`.
- **Roll-up:** SoftwareSystem and Project show counts, such as "3 of 4 containers Ready, 2 active sessions".
- **kubectl:** printer columns (Type, Phase, Sessions, Age) make `kubectl get containers` a quick text view.

## v2: agents build the apps

Settled step by step on 30 September 2026; the full design is
[plans/done/2026-09-30-agent-factory-design.md](plans/done/2026-09-30-agent-factory-design.md)
and the plan [plans/done/agent-factory.md](plans/done/agent-factory.md).

Agents build complete apps when asked, and stay inside their workspace while they
do. Quality does not come from trusting the agent but from what the platform
enforces.

- **A new Container type, `mobile-app`.** The operator never branches on a type;
  a test keeps it that way. A later `function` type (AWS serverless) is one enum
  value plus templates.
- **Standard: the rules as a layer.** Lint and format config, `AGENTS.md`, offline
  style digests, the `make check` contract and its CI workflow, rendered right after
  the Framework it lists. A Container with a Standard gets `main` protected by
  required checks at scaffold time, and `status.checks` reports CI on `main`.
- **CI in Gitea.** Gitea Actions with a runner in the cluster on the same toolchain
  image the agents and devcontainers use. Both toolchain images, `base` and `go`,
  carry one pinned uv, so a Python project checks on either runner and releases
  on `go`, with the Python its `.python-version` pins.
- **Task: a goal for a Container.** The operator runs the Agent in an
  [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox), which works on
  branch `task/<name>`; opens a pull request; waits for CI; runs the Agent again as
  reviewer; and squash-merges on approval. Failed checks, requested changes or a
  failed run start the next round with that feedback, up to the Agent's
  `maxRounds`. The Container's Crew decides how many Tasks run at once.
- **The fence.** The sandbox pod has no ServiceAccount token and reports through
  its termination message; a per-Container Gitea bot user can write to that one
  repository only; `main` takes no pushes, only the operator merges (the reviewed
  commit only), and the Git host refuses changes to the platform's and the
  Standard's protected files (only the operator's gated merge of a Standard change
  passes, after its own checks) and keeps `v*` tags to the operator; a per-project
  NetworkPolicy allows only DNS, the Git host and public HTTPS; each run has a
  deadline. The Claude token is readable inside the sandbox; Agent Substrate's
  egress credential injection is the later fix. The token's one source is a
  Secret in the release namespace that the operator copies into project
  namespaces, so the fix reads one Secret; a namespace receives copies only
  with a label a namespace's own users can't set.
- **App values are needs, not platform rules.** "Works offline, no login, no ads"
  is a need on a SoftwareSystem, not a rule of the platform; the agents get the
  needs with every Task.

## v3: watching the factory

Settled on 1 October 2026; the plan is
[plans/done/factory-monitoring.md](plans/done/factory-monitoring.md) and what it found is in
[results-v3.md](results-v3.md).

The factory runs whole Task loops on its own, so it needs to be seen and run
without kubectl, and cheaply: it lives on a laptop.

- **A Task keeps its timeline.** `status.history` gets an entry whenever the
  phase, round or Progressing reason changes (the newest 64), so where a Task's
  time went stays readable after it ends. A Task waiting for an agent slot says
  `WaitingForSlot`.
- **A Task says what its runs cost.** Each agent run reports its cost as the
  agent says it (for Claude Code, `total_cost_usd` in its JSON output); the
  Task adds them up in `status.costMilliUSD`. A run whose agent didn't say, or
  that ended without a result, counts in `status.runsWithoutCost`, and the UI
  says the cost wasn't reported instead of leaving it blank. A Brief's rounds
  take their Plan Task's.
- **The UI has a Factory view** next to the story, across all projects: component
  health with CPU and memory, the last hour, the Tasks on a board by stage with
  their timelines, where the time goes, agent slots, and what needs a look.
- **The UI server reads the factory's health itself** (`GET /factory/health`):
  pods, nodes and metrics-server, summarized; pod specs never reach the browser,
  which still reads only `c4.factory.io`. It samples every 15 seconds and keeps
  an hour in memory, including its own API round trip.
- **No Prometheus or Grafana.** Process data lives in the resources, live usage
  comes from metrics-server (about 30 MiB), and the last hour from the UI server.
- **Lifecycle targets:** `make down` stops the Kind node, `make up` brings it back
  and gets pods out of back-off, `make doctor` checks every part.
- **Checks work in memory.** CI jobs and, with `--sandbox-workspace-memory`,
  agent sandboxes write their builds to memory-backed volumes: on the disk, they
  stalled etcd and with it the whole control plane.

## v4: issues, crews and roles

Decided on 2 October 2026; the full design is
[plans/done/2026-10-02-issue-driven-factory-design.md](plans/done/2026-10-02-issue-driven-factory-design.md)
and the plan [plans/done/issue-driven-factory.md](plans/done/issue-driven-factory.md).

v2's apps were built one Task per app, and their quality was only "ok for
testing". v4 makes the work smaller and clearer, and gives each role the agent,
model and context it needs.

- **Issues are the work.** A Task can take its goal from an issue of its
  Container's repository (`spec.issue`). The operator reads it before every run,
  labels it `status/in-progress`, comments who works on it, opens a pull request
  that closes it, and closes it on merge; a failed Task hands it back with
  `status/needs-human` and the reason. The `status/*` labels are exclusive, so an
  issue is in one state at a time.
- **Ready means clear.** An issue needs an "Acceptance criteria" checklist before
  an agent gets it; without one it goes back to a person before any tokens are
  spent, with an example checklist in the comment. The Standards' issue templates
  ask for context, goal, criteria, scope and notes.
- **A Crew per Container picks up the work.** It names the implementer and the
  reviewer Agents, polls the repository's `status/ready` issues, waits for their
  Gitea dependencies (also across repositories) to close, and starts up to `parallel`
  Tasks at a time (one by default; a `Blocked` Task doesn't count), lowest number
  first after the Standard changes that `Blocked` Tasks wait on, up to three
  tries per issue. Milestones group the
  backlog; Gitea 1.27 has no API for project boards.
- **Releases go one at a time.** A milestone with a due date is a release. The
  Crew starts only the ready issues of the release due first that still has work
  (a ready issue, or a Task working on one of its issues); later releases wait,
  so each release's tag holds only its own issues. Issues in an undated
  milestone, or in none, come after every release. Two kinds start whatever
  their release, or something would wait for ever: a Standard change a Task
  waits on, and an issue the current release's issues depend on. An issue handed
  back to a person (`status/needs-human`) no longer holds its release, so the
  factory keeps working. Running Tasks are never stopped.
- **The Crew tags a done release** when its `spec.tagReleases` is true (off by
  default). A release whose title is `vMAJOR.MINOR.PATCH` is done when its
  milestone has no open issue, `status/needs-human` included, and no active Task
  works on one of its issues. The Crew tags it, as the operator's Git user, at
  the merge commit of its most recently merged issue (the Task records it in
  `status.mergeCommit`; issues closed by hand don't count), then closes its
  milestone. Tags go out in due-date order, and a tag is never moved: a tag
  already at that commit only closes the milestone, one at another commit is
  left to a person. A release is also left to a person when it has no merged
  issue, or when the tag would carry a later release's merged work. Each release
  is an Event, the log line `Released milestone` and `status.lastRelease`; a
  release left to a person is a Warning Event, once per reason, and the Crew's
  `Released` condition says why. A paused Crew still tags: its release's work
  is already merged.
- **A release reaches the Containers that bundle it.** A Relationship from
  Container A to Container B with `spec.bundles: true` says A ships a pinned
  release of B, as an extension ships its server; only a Container to Container
  link can say it, and most don't (a client calling a service follows its
  deploys, not its tags). When B's Crew tags a release, it files `Bump B to vX`
  in A's repository, labelled `kind/bump` and `status/ready` with acceptance
  criteria, in A's first open release, so A's Crew picks it up and A's next
  release carries it. Each Container gets one issue per release, recorded in
  `status.lastRelease.bumps` and found again by a marker in its body; only the
  newest release is bumped, a link created after the release files nothing, and
  a bundler not yet Ready gets its issue once it is. Each is an Event
  `BumpFiled`. See [plans/done/release-bumps.md](plans/done/release-bumps.md).
- **The Crew prepares a release** when its `spec.prepareReleases` is true (off
  by default, independent of `tagReleases`). Once the first open release is
  done and an issue of it was merged by a Task, and the milestone has no issue
  labelled `kind/release` (open or closed, whoever filed it), the Crew files
  `Prepare vX.Y.Z` there, `kind/release` and `status/ready`, listing the merged
  issues: its agents set the version files (`make release-version` checks them
  against the tag) and write the changelog. Its merge is then the release's
  newest, so the tag carries both. A release has one prepare issue; work merged
  after it leaves the release to a person (`MergedAfterPrepare`), who files
  another `kind/release` issue or tags it. A release with no merged work gets
  none, so a milestone made ahead of its issues isn't released on its own.
- **The Crew watches the release run.** It reads the CI workflow runs the last
  release's tag started and sums them up in `status.lastRelease.run`: `Waiting`,
  `Running`, `Failed` as soon as one ended other than success or skipped, and
  `Succeeded` once all ended well, with `runURL` linking to the failed or newest
  run. A run that fails is a Warning Event `ReleaseRunFailed` and the log line
  `Release run failed`; runs that end well are a Normal Event. The Crew watches
  until the runs succeed, counting each workflow's newest run, so a failed run a
  person re-runs, or a tag a person deletes and pushes again at the same commit,
  turns `Succeeded`. Only the last release is watched, at the commit the Crew
  tagged. Runs that haven't ended an hour after tagging (one no runner picks
  up never starts) are a Warning Event `ReleaseRunOverdue`, once per release,
  the log line `Release run overdue`, and a note on the `Released` condition's
  message until they end; the watch carries on and reports their end as usual.
- **A release can be published to GitHub.** A Crew with
  `spec.publishReleases.github` (`repository: owner/name`, and a Secret the
  user creates holding a token for that one repository) copies its last release
  there once the release runs succeed: the same tag (with an optional
  `tagPrefix`), the same notes (the Gitea release's body, or the titles of the
  release's merged issues, which the operator writes on both) and the same
  files, streamed from the Gitea release. The operator does it, not CI, so the
  token never reaches the code the agents write. It only adds releases: a draft
  marked with the Container and tag, its files, then published, so a retry
  carries on and nobody sees half a release; a release of that tag it didn't
  make is left alone. `status.lastRelease.publish` and the condition
  `Published` say how it went, with the reason; a failure is a Warning Event
  `ReleasePublishFailed`, once per reason, and is retried each poll. See
  [plans/done/release-publishing.md](plans/done/release-publishing.md).
- **Urgent goes first.** A person labels a ready issue `priority/urgent` to have
  it picked up at once: the Crew starts it on its next poll, before every other
  issue, whatever its release, even when every place is taken (once running, it
  counts towards `parallel`). It still waits for its open dependencies, and a
  paused Crew starts nothing. A Standard change filed for an urgent issue is
  urgent too. The operator keeps the label in every repository it polls.
- **Roles get their own model and guide.** Agents have a `harness` and a `model`;
  a Sonnet implementer can pair with an Opus reviewer. A Standard ships
  `docs/agents/implementer.md` and `reviewer.md`, read from the base branch so a
  change can't rewrite its own review. The reviewer reports each acceptance
  criterion, posts its verdict as a pull request review, and may name up to three
  follow-ups, each with its own acceptance criteria, which become
  `status/proposed` issues in the Later milestone, outside any release; a person
  who moves one into a release can ready it as it stands. Criteria arrive whole:
  the reviewer keeps each to one sentence of at most 200 characters, and text
  that must be shortened to fit ends at a whole word with "…", never mid-word.
  For a Brief's apps, the person answers them in a prompt instead
  ([Briefs](#briefs-an-idea-in-plain-words)).
- **Evidence is never made up** (#467). The implementer never presents a
  drawn or generated picture, a recording or a message as the product's: a
  screenshot comes from running the product, a quoted message from the code.
  A criterion that needs what no agent's sandbox has (a desktop editor, a
  device, an account) is left open with why, never faked. The reviewer opens
  every image the change adds (the runner lists them with their sizes), checks
  quoted output against the code, and requires a change for made-up evidence;
  it approves an honestly open criterion of that kind and reports it in the
  verdict's `open`. On merge the operator files those criteria as one
  `kind/by-hand` issue, `status/needs-human` in the Later milestone, so a
  person does them; a Brief lists it with its failed issues and may close it,
  never retry it. The planner doesn't give agents a person's work. See
  [plans/honest-evidence.md](plans/honest-evidence.md).
- **Context comes from the C4 model.** Agents get the system's needs and KPIs, its
  users, the Container's Relationships, and read-only clones of the related
  Containers' repositories: the agent of a client reads the service it calls.
- **Waiting is not failing.** A usage limit, an overloaded API or rejected
  credentials don't cost a round: the Task waits (`AgentUnavailable`) and tries
  the same step again. Neither does a run the infrastructure ended (a node
  shutdown or restart, an eviction, a preemption, an outage that lasted past
  its pod's deadline or its sandbox's shutdown time, a pod deleted before it
  finished, which Agent Sandbox would replace and start over): it runs again in the same round
  (`Interrupted`, then `status.reruns`), at most twice per round, so a run that
  is cut short every time still spends rounds. An out-of-memory kill, or a run
  that ran out its time while the cluster ran, is the agent's run failing, not
  an interruption.
- **A protected file without a person.** An issue that needs a protected file, such
  as the Makefile or a CI workflow, gets its own gated Task: the operator files a
  `kind/standard-change` issue, the waiting Task is `Blocked`, and the operator
  merges the reviewed change itself. The agents' rules and `.c4factory.yaml` stay
  out of reach. See
  [plans/done/2026-10-02-standard-change-design.md](plans/done/2026-10-02-standard-change-design.md).
- **A Standard's fixes reach its Containers.** Bumping a Standard's pinned `ref`
  updates every Ready Container that uses it. The operator renders the Standard
  at the Container's ref and at the new one, and opens a pull request of its own
  with the difference: the new render of each file the repository still holds
  as the old ref rendered it, and the removal of files the new ref no longer
  renders. A file the repository changed since (a Standard change, or a person)
  is left out and named in the pull request; a person brings it up to date by
  hand. No agent takes part: the update is the template's own content, reviewed
  here where the template changed, and it reaches the agents' rules, which
  agents never change. The operator merges it as admin once CI passes on it,
  with every check `main` requires, and it changes only the planned files.
  Closing the pull request drops the update until the Standard's ref changes
  again. `status.standard` records the ref the Standard's files are at and the
  update in flight, the condition `StandardUpdated` how it went;
  `scaffoldedFrom` and `.c4factory.yaml` keep the scaffold's refs. See
  [plans/done/standard-update.md](plans/done/standard-update.md).
- **New shapes, same operator.** Container types `cli` and `extension`, with
  their templates in the template catalog in `templates/`; a Go and Node toolchain whose image ships a warm build cache, a CI runner
  labelled `go`, and two agent sandboxes at once.

## Briefs: an idea in plain words

Decided on 6 October 2026 for GitHub issue #240; the plan, with every
decision and why, is [plans/done/brief.md](plans/done/brief.md).

Writing the model, the catalog, the Agents and the Crew as YAML, and filing the
first issue through Gitea's API, is operating the factory, not describing a
product. A Brief lets a person give the idea in plain words and steer with
follow-up prompts; a planner Agent does the rest and says what it chose.

- **A Brief is one more writer of the model.** `spec.idea` is the idea,
  immutable; `spec.prompts` takes follow-ups and only grows. Anything can
  write it: the UI's box, kubectl or GitOps. The UI server writes nothing but
  this: creating a Brief and appending a prompt.
- **A planner Agent plans each round** (the idea, then each batch of new
  prompts) as a Task with `spec.kind: Plan` owned by the Brief, in an agent
  sandbox like any Task. It works in the Brief's own repository, where the
  operator writes the brief, the namespace's current model and the installed
  catalog, and pushes its plan to a branch: the story kinds, the first issues
  and its choices in plain words.
- **The operator checks and applies the plan.** A plan writes only story
  kinds (Project, Person, SoftwareSystem, Container, DataStore, Relationship),
  names only installed catalog entries that support the Container's type,
  changes only the story text of what exists and deletes nothing; anything
  else refuses it, and the planner's next round gets the reasons. The operator
  applies it (Relationships before the Containers they start from, so their
  Connectors are part of the scaffold), makes a Crew for each new Container
  with the Brief's Agents and one place, and files the issues in each
  Container's repository once it is Ready, ready for its Crew. What it made
  carries the label `c4.factory.io/brief`; deleting the Brief keeps it. When
  a step fails, the Brief stays `Applying` and tries again, and its `Planned`
  condition, reason `ApplyFailed`, and an `ApplyFailed` Event say where it
  stopped and why: a Brief never looks busy without a reason.
- **A round's issues keep the plan's order** (#428). An issue that needs
  another issue's work merged first names it in its front matter, `after:`
  with the round numbers of its blockers, in any of the round's Containers.
  The plan is refused when `after:` names no issue of the round, the issue
  itself or a design idea, or makes a cycle. Applying the round, the operator
  files blockers first, files an ordered issue without `status/ready`, makes it
  depend in Gitea on each blocker (across repositories too), names them in its
  body, and only then labels it ready; the Crew waits for open dependencies, so
  the issue starts once its blockers' pull requests merge. See
  [plans/brief-issue-order.md](plans/brief-issue-order.md).
- **One app uses another's release, never its branch** (#430). Apps stay one
  repository each and share code the way separate repositories do: an issue
  that builds on another Container's code names it in its front matter,
  `uses:`. The plan is refused when the used Container is the issue's own, has
  no Relationship from the issue's Container, or has no release planned or
  made. When the Crew tags a release of a repository with a `go.mod` at its
  root, the operator publishes the module to the owner's Go registry in Gitea;
  agent pods and the Go Standard's CI fetch `c4factory.local/...` modules
  through it. A `uses:` issue is labelled ready only once each app it uses has
  a published version, which its body names to pin. See
  [plans/app-modules.md](plans/app-modules.md).
- **It asks only for what costs money or touches access.** Agents, their
  models, a Crew's size, GitProviders and credentials are never the planner's
  to make or change. When it needs one, or can't go on without a decision, the
  Brief's condition `NeedsInput` says what in one sentence, and the round waits.
- **Follow-ups keep scaffold once.** A change to a scaffolded Container
  becomes an issue in its repository; a new Container is added and scaffolded;
  a Relationship added from a scaffolded Container shows `AddedAfterScaffold`
  and its wiring becomes an issue.
- **A Brief's apps are released when the plan says so.** A plan names the
  Containers that ship releases (`plan/releases.yaml`); the Crew the Brief
  made for each then tags and prepares releases, and each round files that
  Container's issues in a release milestone of its own, the next minor
  version, so a release follows once they are done. A person turns releases on
  with a prompt, or by naming the Container in `spec.releases` with a GitHub
  repository and a Secret holding a token for that repository only, which
  also has the Crew publish its releases there. Releases only turn on; a Crew
  the Brief didn't make is left alone. `status.apps` and the Brief's card show
  each app's latest release and where it went, and say plainly when releases
  are off and how to turn them on. See
  [plans/done/brief-releases.md](plans/done/brief-releases.md).
- **The loop is tested without a model.** A planner Agent with
  `harness: stand-in` plans from the catalog alone: a Container named after
  the Brief on the first Framework, with an issue for it, and an issue for
  each follow-up prompt. `make e2e` takes a Brief through two rounds that way,
  with no token, and *Verify Release*, when a human dispatches it, takes one
  through a round on the published chart, whose Agent `stand-in` any project can name.
  How well a real planner plans an idea is the person's run.
- **The person answers the reviewers' suggestions in a prompt** (#375). A
  reviewer's follow-up waiting, `status/proposed`, in the repository of a
  Container the Brief made is listed in the Brief's `status.suggestions` and
  card, and in the workspace's `model/suggestions.yaml`; the Brief reads them
  every reconcile, when one of those Containers' Tasks ends, and every ten
  minutes. A plan answers only the ones a new prompt speaks to, in
  `plan/suggestions.yaml`: `take`, which needs acceptance criteria, or `skip`,
  each with why. The operator checks the answers with the plan; applying the
  round, it takes a suggestion out of the Later milestone and labels it
  `status/ready`, or closes it, with a comment quoting the planner's why and the
  round's prompts. A suggestion no prompt answers keeps waiting: nothing is
  built without the person's say, and labels and milestones stay the
  operator's.
- **A failed issue shows on the Brief and is tried again from it** (#429).
  An open issue handed back with `status/needs-human` in the repository of a
  Container the Brief made is listed in the Brief's `status.failed` and card,
  with why in one sentence, taken from the cluster: the newest failed Task's
  message, or the Crew's reason for refusing it (no acceptance criteria,
  already merged, three tries used). `status.failedWaiting` is a column of
  `kubectl get brief`, the condition `IssuesFailed` says the first one and
  its reason, and each is a Warning Event `IssueFailed`; the phase stays
  `Planned`. The person answers in a prompt, as with suggestions: the planner
  reads `model/failed.yaml` and writes `plan/failed.yaml`, `retry` (with an
  optional clarification the operator appends to the issue's body, since an
  agent's goal is the issue's title and body) or `close`, each with why, only
  for the ones a new prompt speaks to. A retry is refused when the Crew would
  refuse the issue again; an issue whose three tries are used is closed and
  filed again, reworded or split, so the Crew's guard stays. Applying the
  round, the operator labels a retried issue `status/ready` in place of
  `status/needs-human`, or closes it, with a comment quoting the why and the
  round's prompts. See [plans/brief-failed-issues.md](plans/brief-failed-issues.md).
- **A Brief looks after its own handed-back work** (#486, planned).
  `spec.autopilot` is `Off`, `Ask` or `On`; unset is `Ask`. The Brief watches
  only what it made: failed issues, its Containers' Tasks and Crews. What
  heals by itself (a usage limit, an agent-slot queue, a Standard change in
  review, a Git host coming back) is left alone for a window from one table
  in `internal/model`. When a failed issue is due, a tending round, a planner
  round with no new prompt, answers it `retry`, `close` (with new issues to
  split), `wait` (until a Task merges, an app releases or 24h pass) or `ask`;
  in `Ask` every answer waits for the person. Everything else that is due,
  and every `ask`, is one entry in `status.needsYou` with the choice the
  Brief would make and why. The Crew's three tries still hold, an issue is
  answered once per failure, and tending rounds come at most hourly. Each
  is a history entry, outcome `Tended`, so a prompt can overrule it. See
  [plans/brief-autopilot.md](plans/brief-autopilot.md).
- **Deleting a Brief deletes its bot on the Git host** and keeps its
  repository and the model it made. A host that fails the delete (down,
  unreachable, or answering with an error) is tried again, each failure a
  `BotNotDeleted` Event naming the user and the error, until the namespace is
  terminating or ten minutes have passed; then the Brief goes and leaves the
  bot, with a `BotLeft` Event and log line saying so. A Brief whose
  GitProvider or credentials are gone leaves its bot at once.
- **The Brief's status tells the story of the plan:** its phase, the last
  round's choices, what it created, the issues it filed, each round's summary
  and cost. A round's summary is written for the person, about what the round
  did for their app: the planner's `plan/summary.md`, or what the plan holds
  when it wrote none, never the planner's reply. A summary too long for the
  history ends on its last whole sentence, never mid-word. The chart makes each project's namespace, Project, catalog and four default
  Agents. A person gives the Agents' token once, as a Secret in the release
  namespace, and the operator shares it with every project; a project with a
  Secret of that name of its own keeps it
  ([plan](plans/shared-project-credentials.md)). A Brief whose Agent's token
  is missing says where to give it: in the release namespace for every
  project, or in its own for this one, and names the namespace label when
  its namespace hasn't opted in.

## Distribution and issues

This repository develops the factory and holds no product. The factory ships
as a Helm release, and other devices build their products with it. Every issue
on `JairJosafath/c4factory` carries one source label: `source/dev` when it was
made while developing the factory, or `source/user` with one `user/<name>`
label when a device running the release reported it. A device reports through
the issue form *Report from a device*, or with `gh issue create` and a body
whose `### Reported by` heading names the device; the intake workflow
(`hack/lib/github.sh intake`) labels the issue, for trusted authors only, and
files it as `status/proposed` for the owner to ready like any other issue.

A `vX.Y.Z` tag publishes the release: the chart at
`oci://ghcr.io/jairjosafath/c4factory/charts` and its images on GHCR. The
release installs the whole factory:

| Part | Where | Notes |
| --- | --- | --- |
| Operator, CRDs, RBAC, admission policy | release namespace | always |
| UI | release namespace | `ui.enabled`, on by default |
| Gitea | `gitea.namespace` (`gitea`) | `gitea.enabled`, on by default; admin password and the runners' registration token generated in the cluster by Gitea's pod into Secrets `gitea-admin` and `gitea-runner` when missing, never rendered by Helm, and kept on upgrade; namespace and volume kept on uninstall |
| Seed job | Gitea's namespace | a hook Job after every install and upgrade: template organization and tags, a `gitea-credentials` Secret, a GitProvider and, when the namespace has none, a Project for each project in `gitea.projects`; labels a project namespace the chart didn't make so it receives the shared Secrets |
| Project namespaces | each of `gitea.projects` | `projectNamespaces.create`, on by default; made when missing, labeled `c4.factory.io/shared-secrets: "true"` and kept on uninstall; a namespace that already exists is left to the person |
| Shared Secrets | the release namespace, copied to each project namespace | the Agents' token Secret (`agents.credentialsSecret`) and the pull Secrets (`global.imagePullSecrets`), which the person makes once; the operator copies each into every labeled namespace with a Project, keeps its copies in step with the source, and never touches a Secret of that name it didn't make, which is how a project keeps a token of its own; Task sandboxes list the pull Secrets |
| CI runners `base` and `go` | Gitea's namespace | on the released `gitea-runner-*` images |
| GitProviders | named namespaces | `gitProviders`, for a Gitea the release does not run (`gitea.enabled=false`); each names a Secret the user creates (`existingSecret`), as the chart renders no Secret and takes no token in its values |

A device installs itself only what is another project's or its own: Agent
Sandbox (its controller and CRDs) before the chart, the Agents' token in the
release namespace, and, while the GHCR packages are private, a pull secret in
the release namespace and Gitea's or the images loaded into its cluster. Adding
a project is adding its name to `gitea.projects`. The Kind test environment of this repository keeps its
make targets (`make gitea seed deploy`) and locally built images.

## Out of scope for v1

- **Shipping.** No builds, registries or rollouts; existing tools pick up the scaffolded files. Since v4 a Crew can copy its releases to a GitHub repository: a release target, not a git provider, where the factory never pushes.
- **Runtime health of the apps.** Reading back from ArgoCD or monitoring comes later. (The factory watches its own health since v3.)
- **Existing repositories and monorepos.** New work only, one repository per Container.
- **Component and Code levels.** Dropped until Containers work end to end.
- **Template updates.** Scaffolding happens once; no pull requests to existing repositories.
- **Provisioning data stores.** A Connector only scaffolds; creating real databases is left to tools such as Crossplane.
- **Git providers other than Gitea.** The GitProvider interface allows more later.

## Test tiers

Go tests are the gate: each rule is verified at the lowest tier that can see it, and `make check` runs the first three tiers before every merge. A Kind cluster exists only when a human asks to run tests in it; agents never create, lease or deploy to one. What only a real node would catch is not tested before release.

| Tier | Verifies |
| --- | --- |
| Table tests (`make test`) | The decisions in `internal/model`, the rendering of every template in `internal/scaffold`, and the Agent Sandbox and pod specs the controllers build, the agent fence included. |
| Offline YAML (`make validate-yaml`, in `make check`) | Samples, e2e fixtures, kustomize builds, the Helm chart and rendered manifests, against the built-in schemas and the CRDs, with their create-time CEL rules. |
| envtest (`make test`) | Reconcilers wired to a real API server: status, ownerReferences, the admission policy, every CEL rule's failing case at create and on update, the reaction to faked Sandbox and pod status, and the generated RBAC role against the calls the controllers make. |
| Components (`make test-gitea`, `make seed-local`) | The Gitea API's behaviour, the seed and what Gitea shows of Actions, against the same Gitea binary the cluster runs, started per checkout. |
| Kind (`make e2e`, only when a human asks) | The whole loop on stand-in agents with no model keys: a hello-world page scaffolded, planned from a Brief, a Task taken through two rounds to a merge, a Standard bump carried to main. |

The plans that build these tiers are [verify-without-cluster.md](plans/done/verify-without-cluster.md) and [test-without-cluster.md](plans/done/test-without-cluster.md).

## Test environment: the Kind cluster

One kind cluster on a laptop holds Gitea, the operator, a sample project and a test Session, so the whole loop runs with nothing but Docker. It is the Kind tier of [Test tiers](#test-tiers): a human creates it and runs `make e2e` in it when they ask to; agents never create, lease or deploy to it, and no change waits for it. Every part is picked to stay light on resources.

&#91;embedded content: test environment · 1 cluster, 3 namespaces\]

The highlighted operator is the main thing being built. Gitea and kind are off the shelf; the seed job, templates and Session client are small.

| Part | Namespace | Role |
| --- | --- | --- |
| Gitea | `gitea` | Single container with SQLite and a small volume. Holds the template repositories and the Container repositories. Its admin password is generated per cluster into Secret `gitea-admin`, never committed. |
| Seed job | `gitea` | Creates the `templates` and `webshop` organizations, pushes and tags the template repositories, and stores an access token as Secret `gitea-credentials` in `webshop`. |
| Operator | `c4-factory-system` | The kubebuilder manager with all controllers. |
| Sample project | `webshop` | Project, GitProvider, catalog entries and the story resources. |
| Test Session | `webshop` | A pod that clones `orders-api` from Gitea and runs the client under the Session ServiceAccount. |

The webshop sample uses the smallest set of seeded templates that exercises every layer:

- `python-fastapi` (Framework): a uv project with FastAPI, ruff and pytest, plus the base devcontainer settings.
- `small` (Compute): resource requests and limits for the manifests, as a `deploy/k8s/compute.yaml` patch.
- `k8s-manifests` (Deployment): a Dockerfile and plain Kubernetes manifests. It patches in `compute.yaml` when a Compute is chosen, and lists `connector-<name>.yaml` for each Connector, so a Connector used with it ships that file.
- `postgres` (Connector): a compose service for the devcontainer and connection settings in `deploy/k8s/connector-postgres.yaml`.

The sample in `config/samples/webshop/` tells a small story: a `customer` Person uses `webshop-core`, whose `orders-api` service reads and writes the `orders-db` DataStore.

The agent loop runs on a second project, the e2e fixture `apps` in `test/e2e/testdata/apps`: one hello-world page scaffolded from the templates `hello-html` (Framework) and `hello-html-standard` (Standard, the agents' role guides), whose CI runner and stand-in agent use the minimal `c4factory/base:dev` toolchain (Node, bash, git, make). `make e2e` builds no Flutter or Go toolchain image.

Make targets:

- `make cluster`: create the kind cluster.
- `make gitea`: install Gitea.
- `make seed`: run the seed job.
- `make deploy`: build and install the operator. During development, `make run` runs it from the laptop instead.
- `make sample`: apply the webshop sample.
- `make e2e`: all of the above, plus the minimal toolchain, its CI runner, Agent Sandbox and the `apps` fixture, then run the four specs: the page scaffolded, planned from a Brief, a Task taken through two rounds to a merge, a Standard bump carried to main. Every Agent in the fixture runs on the stand-in; the suite stops before it starts if one doesn't, or if one names a model credential Secret.

Gitea keeps the cluster light: one container and SQLite, no separate database. To open a scaffolded repository in a local VS Code devcontainer, reach Gitea through `kubectl port-forward`.

## Risks and open points

- **Gitea instead of GitHub or GitLab.** Chosen to keep the test cluster light; other providers can follow behind the same GitProvider interface.
- **Shared files between layers.** A Connector often needs a dependency in `pyproject.toml`, which the Framework owns. In v1 the Framework template receives the Connector list and wires the ones it knows. Revisit if that couples Frameworks to Connectors too much.
- **Session credentials.** The per-namespace ServiceAccount lets any client touch any Session in its project. Limiting a client to its own Session needs short-lived tokens, later.
- **Compatibility is checked late.** The operator checks types after creation, so an invalid Container exists briefly. A webhook can reject it up front later.
- **Open: the "type" side of a SoftwareSystem** in the drawing. Its meaning, for example internal versus external, isn't decided yet.
