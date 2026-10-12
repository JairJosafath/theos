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
| Container type | A fixed list: `static-site`, `service`, `job`, `worker`, `mobile-app`, `cli`, `extension`, `operator`. |
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
  scaffoldedFrom: [python-fastapi@v1.3.0, small@v1.0.1, k8s-manifests@v1.2.0, postgres@v1.1.0]
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
- **Versions:** Project status carries `factory: {operatorVersion, chartVersion}`, the operator's release (`dev` for an unreleased build, stamped at build time in `internal/buildinfo`) and the Helm chart's it was installed with (`C4_CHART_VERSION`, empty outside the chart). The UI shows them beside its own version and the CRD API version it reads, and warns when its version and the operator's differ (two `dev` builds don't; no Project reporting one yet shows "unknown").
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
  required checks at scaffold time, and `status.checks` reports CI on `main`. A change
  that touches only the changelog, `docs/` or images passes CI without running
  `make check`; the required context still reports.
- **CI in Gitea.** Gitea Actions with a runner in the cluster on the same toolchain
  image the agents and devcontainers use. Both toolchain images, `base` and `go`,
  carry one pinned uv, so a Python project checks on either runner and releases
  on `go`, with the Python its `.python-version` pins. The `go` image also
  carries the kubebuilder operator tools (kubebuilder, controller-gen, kustomize,
  setup-envtest) and the envtest binaries, so an operator's tests run without a
  cluster.
- **Task: a goal for a Container.** The operator runs the Agent in an
  [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox), which works on
  branch `task/<name>`; opens a pull request; waits for CI; runs the Agent again as
  reviewer; and squash-merges on approval. A pull request merges only on checks
  that ran against the `main` it merges into: when `main` moved since the
  approved head's checks ran, the operator merges `main` into the branch through
  the Git host, with no agent run and no round spent, waits for the checks
  again, and merges when they pass, without another review. A Standard
  change's gated merge needs the approving review run to last: when it ends
  while CI runs again, the change is checked and reviewed again in the same
  round, not failed, and spends no try and none of the reviewer's three runs
  without a verdict. When an approval runs out a fourth time in a round, CI
  keeps taking longer than a review run lasts: the Task fails, reason
  `ApprovalRanOut`, costing no try. A branch that
  conflicts with `main` goes back to its implementer in the same round instead.
  Only that merge through the Git host decides a conflict: the host's
  "mergeable" flag is stale while it tests the pull request against a `main`
  that just moved, so a branch that has `main`'s head waits for it rather than
  going back. An implementer sent to resolve conflicts that finds none commits
  nothing; its branch, with its pull request, is checked and reviewed again as
  it is, and merges when approved.
  Failed checks, requested changes or a failed run start the next round with
  that feedback, up to the Agent's `maxRounds`. An implementer that finds its issue can't be done yet (it waits
  on work not merged or released) or can't be done in the sandbox changes
  nothing and ends its summary with a line `Blocked: <what it waits on and
  why>`; the Task then fails at once, reason `Blocked`, with those words in its
  message and on the issue, and the Brief lists the issue as kind `blocked`. A
  round with no change and no such line fails with the end of the agent's
  summary (its last 400 bytes, from a whole word), where it usually said why. A
  line that says nothing blocks the issue (`Blocked: none`, `n/a`,
  `No blockers.`, `nothing blocks this issue`) is not a blocked note; one that
  goes on to name something (`none of the engine's releases has the fix`) is.
  The implementer runs the Container's check in its sandbox and is sent back
  twice to fix it while it fails; a round whose check still fails then fails
  with its output. The check that fails on the branch runs once on the base
  commit too, in a separate worktree. When it fails there as well, the
  sandbox can't judge the change (a download or service it can't reach):
  the fix attempts are told so and to leave the rest to CI, the round pushes
  with no error and reports the base's output (`CheckFailsOnBase`), and the
  operator goes on to the pull request and CI, which runs the same check and
  decides. The operator shows the difference: the round's status message
  says the check fails on main in the agent's workspace too and CI decides,
  a Normal Event `CheckFailsOnBase` names the output's last line, and a
  comment on the pull request names the check and carries the output, so a
  person can give the workspace what CI has. It costs no round and no try,
  and is no condition: the Task's outcome is CI's and the review's. A
  Standard change is then proposed as on a passing check; its own
  gated Task runs CI before it merges. A blocked note is checked the same
  way: when the check fails on the base, the implementer is sent back once
  to do the issue, since that is not a reason to block, and a second blocked
  note stands. A base that can't be checked out counts as passing
  ([plan](plans/workspace-check-differs.md)).
  The
  Container's Crew decides how many Tasks run at once.
- **A Task waits for checks a runner will run.** The commit status gives the
  checks' result; the Git host's Actions runs at the head commit say whether
  the job is queued or running, and its runners whether one is online. A run
  queued while a runner is online, behind other jobs, is waited for (up to 6
  hours), and a running one up to 3 hours 15 minutes: its runner's 3 hour job
  timeout, plus 15 minutes for the runner to report the result. The Task
  fails, reason `NoCIResult`, when no run starts within 30 minutes, when its
  run is queued with no runner online 30 minutes in, or past those bounds.
  That failure is the infrastructure's, so it costs the issue no try; a later
  try that continues its pull request, with green checks on the commit the
  failed try pushed, goes straight to review
  ([plan](plans/checks-wait-for-runner.md)). Checks that fail because the
  runner ran out of room (the log says `no space left on device`) are the
  infrastructure's too: the Task fails, reason `RunnerOutOfSpace`, costing no
  round and no try. The runners keep `/tmp` from job to job, so they clear
  what jobs left there over two hours ago and run jobs without Node's compile
  cache, which no later job reads. Checks that fail because the machine killed
  a job for lack of memory (the log says `signal: killed`, `Killed`, exit code
  137 or `JavaScript heap out of memory`) are the infrastructure's as well, but
  pass once the machine has room: the Task runs the failed jobs again in the
  same round and tells the agent nothing, up to twice a round; a third killed
  job fails the Task, reason `RunnerOutOfMemory`, costing no try. When an
  implementer pushes again the commit whose checks failed, having found
  nothing to fix, its failed jobs run again before the Task reads the checks;
  a failure from before a rerun is never read as its result
  ([plan](plans/done/checks-run-again.md)).
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
  `WaitingForSlot`. An agent sandbox holds a slot until its pod ends; one kept
  after its run, a reviewer's while its Task waits for CI, holds none.
- **A Task says what its runs cost.** Each agent run reports its cost as the
  agent says it (for Claude Code, `total_cost_usd` in its stream-json result
  line); the Task adds them up in `status.costMilliUSD`. A run whose agent
  didn't say, or that ended without a result, counts in
  `status.runsWithoutCost`, and the UI says the cost wasn't reported instead
  of leaving it blank. A Brief's rounds take their Plan Task's.
- **A Task says what tokens its runs used, and how big a session got.** Beside
  the cost, `status.tokens` adds up the input, output, cache-read and
  cache-write tokens its agents reported (for Claude Code, from its
  stream-json result line: `modelUsage`, subagents included, else `usage`)
  and keeps `largestSession`: the most context any one agent session was
  given on a turn, which is what makes an agent's work sloppier; a subagent is
  a session of its own. A run with a call that reported none adds no tokens
  and counts in `tokens.runsWithout`. A Brief's rounds take their Plan Task's,
  and the Brief adds its rounds up. The UI shows them on one line beside the
  cost, on a Task and on each Brief round (`$6.12 · 1.4M tokens, 1.1M of them
  cache reads · largest run 142k`), marking a largest agent run past its
  Agent's `sessionTokenLimit`, and says when the agent reported no tokens; when
  an agent run handed off to a fresh one, the line ends with how often (`handed
  off 2×`). The UI calls these *agent runs*: "session" there means only a
  Session. Plan:
  [plans/done/session-tokens.md](plans/done/session-tokens.md).
- **A long session hands off to a fresh one.** A session whose context passes
  its Agent's `sessionTokenLimit` (default 130000) is told, at its next tool
  call, to write where it stands and stop; c4agent starts a fresh session on
  the same job with that note and the working tree, and ends a session that
  doesn't stop (past the limit by half again), writing that note itself from
  the session's last words and `git status`; such a session ends without a
  result line, so its tokens come from its turns and the run's cost goes
  unreported. After three handoffs in one call the last session runs to its
  end. A custom `C4AGENT_COMMAND` runs one session, unguarded. The Task counts
  handoffs in `status.tokens.sessionHandoffs`, with a `SessionHandedOff` Event
  for each run that handed off, saying the sizes it happened at. It also
  keeps a record of each agent run as it ends in `status.runs` (the newest
  64): its round and role, what it cost and used, and the sizes its sessions
  handed off at, so the Task page names the run that handed off ("Run 3
  (round 2, implement) handed off 2 times, at 131k and 138k tokens") without
  reading Events.
- **The UI shows the factory at work,** across all projects (*The UI* below):
  component health with CPU and memory, the last hour, the Tasks on a board by
  stage with their timelines, where the time goes, agent slots, and what needs
  a look.
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
- **Open pull requests are the work in flight.** A failed Task's pull request
  stays open while a later try at its issue may continue it, and the operator
  closes it, with a comment naming the Task and why, once none will: its issue
  is closed, a newer try at the issue opened its own pull request (the comment
  links it), the issue's tries are used up, or the Task had no issue. A newer
  try that continues it, or still runs and may, keeps it open. The branch stays, so a person can
  reopen it; `status.pullRequestEnded` says how it ended. A Failed Task looks
  again every 10 minutes, since the Git host doesn't say when an issue closes,
  so tries from before an upgrade are cleaned up too while their Task exists.
- **Ready means clear.** An issue needs an "Acceptance criteria" checklist before
  an agent gets it; without one it goes back to a person before any tokens are
  spent, with an example checklist in the comment. The Standards' issue templates
  ask for context, goal, criteria, scope and notes.
- **A Crew per Container picks up the work.** It names the implementer and the
  reviewer Agents, polls the repository's `status/ready` issues, waits for their
  Gitea dependencies (also across repositories) to close, and starts up to `parallel`
  Tasks at a time (one by default; a `Blocked` Task doesn't count), lowest number
  first after the Standard changes that `Blocked` Tasks wait on, up to three
  tries per issue (a `NoCIResult`, `RunnerOutOfSpace`, `RunnerOutOfMemory` or `ApprovalRanOut` failure is not one). A try continues an earlier try's pull request that is
  still open: it works on that branch, with that try's feedback, and reports
  to that pull request (`status.continues` names the earlier Task). When the
  earlier try ended waiting for the checks of its commit (red checks
  included) before a reviewer ran in any of its rounds, that commit is still
  the head, and its checks now pass, the try skips the implementer: it enters
  `Checking` at that commit and starts the review. A round
  that changes only the pull request, such as its description, and commits
  nothing goes to the reviewer as it is, and merges when approved; without a
  pull request, a round with no commit has nothing to review
  ([plan](plans/done/try-continues-open-pr.md)). Milestones group the
  backlog; Gitea 1.27 has no API for project boards.
- **Releases go in order.** A milestone with a due date is a release. The Crew
  starts ready issues release by release, the one due first first: the release
  in progress (the first that still has work: a ready issue, one waiting on
  dependencies, or a Task working on one of its issues) has first claim on any
  free place, and the places it can't use, because its remaining issues wait
  on each other or already run, go to the ready issues of the next releases,
  due first first, up to `parallel`. Idle places buy nothing (#873). Issues in
  an undated milestone, or in none, come after every release. Ahead of every
  release go an urgent issue and a Standard change a Task waits on; an issue
  the current release's issues depend on, directly or through each other,
  goes with the current release, or it would wait for ever. An issue handed
  back to a person (`status/needs-human`) no longer holds its release, so the
  factory keeps working. Running Tasks are never stopped. Tags still go out in
  order (below): a later release that is done waits for the earlier one, and
  when the earlier one's tag carries the later one's merged work, that work
  moves into the earlier milestone, so each tag holds what its milestone says.
- **The Crew tags a done release** when its `spec.tagReleases` is true (off by
  default). A release whose title is `vMAJOR.MINOR.PATCH` is done when its
  milestone has no open issue, `status/needs-human` included, and no active Task
  works on one of its issues. The Crew tags it, as the operator's Git user, at
  the merge commit of its most recently merged issue (the Task records it in
  `status.mergeCommit`; issues closed by hand don't count), then closes its
  milestone. An issue its Standard change did whole (the Task ends Merged as
  `DoneByStandardChange`, with no pull request of its own) counts as merged at
  that change's merge commit and time; when that merge is lost, it doesn't
  hold the release; while it waits for its commit, it holds the release, and
  the message says that a person can move the issue out of the milestone. Tags go out in due-date order, and a tag is never moved: a tag
  already at that commit only closes the milestone, one at another commit is
  left to a person. A release is also left to a person when it has no merged
  issue. When the tag would carry a later release's merged work (its issue was
  handed back, and the Crew went on), the Crew tags it anyway: first it moves
  each closed issue of a later release that merged before the tag's commit into
  this release's milestone, with a comment, so each milestone holds what its tag
  carries; an open issue stays in its release. A later release left with no
  issue closes untagged, its version skipped. See
  [plans/done/release-carries-later-work.md](plans/done/release-carries-later-work.md).
  Each release is an Event, the log line `Released milestone` and `status.lastRelease`; a
  release left to a person is a Warning Event, once per reason, and the Crew's
  `Released` condition says why. A paused Crew still tags: its release's work
  is already merged.
- **A release reaches the Containers that bundle it.** A Relationship from
  Container A to Container B with `spec.bundles: true` says A ships a pinned
  release of B, as an extension ships its server; only a Container to Container
  link can say it, and most don't (a client calling a service follows its
  deploys, not its tags). When B's Crew tags a release and its runs have built
  the release's files (`status.lastRelease.run` is `Succeeded`, or no run
  started within the hour a release's runs get), and, when B's Crew has
  `spec.publishReleases`, the release is published there with its files
  (`status.lastRelease.publish` is `Published`; the hour doesn't apply, since
  publishing needs the runs to succeed), it files `Bump B to vX`
  in A's repository, labelled `kind/bump` and `status/ready` with acceptance
  criteria, in A's first open release that has no prepare issue (open or
  closed), so A's Crew picks it up and A's next release carries it; a release
  already being prepared would otherwise wait for a person once the bump
  merged after its prepare issue (#834). When every open release is being
  prepared, it goes in no milestone, where the next round of a Brief that
  releases A picks it up. Each Container gets one issue per release, recorded in
  `status.lastRelease.bumps` and found again by a marker in its body; only the
  newest release is bumped, a link created after the release files nothing, and
  a bundler not yet Ready gets its issue once it is. A release whose runs fail
  or never end, or whose publishing fails, files nothing until a run of it
  again succeeds or it is published: a bundler pins the published files, which
  don't exist before (#864). Each is an Event
  `BumpFiled`. See [plans/done/release-bumps.md](plans/done/release-bumps.md).
- **The Crew prepares a release** when its `spec.prepareReleases` is true (off
  by default, independent of `tagReleases`). Once the first open release is
  done and an issue of it was merged by a Task, and the milestone has no issue
  labelled `kind/release` (open or closed, whoever filed it), the Crew files
  `Prepare vX.Y.Z` there, `kind/release` and `status/ready`, listing the merged
  issues, later releases' merged issues that its tag will carry included: its agents set the version files (`make release-version` checks them
  against the tag) and write the changelog. Its merge is then the release's
  newest, so the tag carries both. A release has one prepare issue; work merged
  after it leaves the release to a person (`MergedAfterPrepare`), who files
  another `kind/release` issue or tags it. A release with no merged work gets
  none, so a milestone made ahead of its issues isn't released on its own.
  The prepare step is built on the main it merges into and lists what the
  tag carries (#956): while any Task is at work (a Blocked one aside,
  whatever its issue's release), the done release's prepare issue waits
  (`Released=False`, reason `PrepareWaitsForTasks`, no Event) and the Crew
  starts no other issue but an urgent one or a Standard change a Blocked
  Task waits on, so the Tasks drain; a prepare issue, filed by the Crew or
  a person, starts only once no other Task is at work, and runs alone: no
  other issue but those starts until its Task ends.
- **A release's tag comes after the one before it.** The newest closed release
  milestone's tag is the previous release; a release whose newest merge is
  that tag's commit, or a Task's merge that ended before the merge the tag
  names, would ship older code under a newer version, so the Crew leaves it
  to a person (`BehindPreviousRelease`) instead of tagging it. A previous tag
  at a commit no Task merged can't be placed and doesn't hold the release.
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
- **A failed release run re-runs on request.** Setting or changing the
  annotation `c4.factory.io/rerun-release` on a Crew with `tagReleases` whose
  `status.lastRelease.run` is `Failed` makes the operator re-run the failed
  jobs of that release's failed runs (each workflow's newest run) through the
  GitProvider (Gitea's `rerun-failed-jobs`), once per value: the Crew records
  the value it answered in `status.rerunRelease`, and a second reconcile with
  the same value does nothing. The re-run is a Normal Event
  `ReleaseRunRerun` and the log line `Re-ran the release runs`, and the watch
  follows the runs' new attempts as it does when a person re-runs them in
  Gitea; the hour they have to end counts from the re-run
  (`status.lastRelease.rerunTime`), and a re-run's overdue runs get their own
  `ReleaseRunOverdue`. A value set while the run hasn't failed re-runs
  nothing, now or later: it is a Normal Event `NoFailedReleaseRun`. A re-run the Git host
  refuses is a Warning Event `ReleaseRunRerunFailed`; a new value tries again.
  The operator asks for each failed run's re-run even after one is refused;
  when the host refused only some, the Warning names the runs it re-ran and the
  ones it refused, the release stays `Failed`, and a new value re-runs only the
  refused ones, since the others are no longer failed.
- **A released Go app is a module others can fetch.** Right after the Crew
  tags a release, it publishes the repository's Go module at that tag to the
  Git host's Go package registry (Gitea's, per owner), under the module path
  `go.mod` declares and with the tag as its version, so another app can
  `go get` that release. A version is published once and never replaced;
  `status.lastRelease.goModule` records the path, version and state
  (`Published`, `NotGo` for a repository with no `go.mod` at its root,
  `Invalid` for a version Go refuses, such as a `v2.0.0` whose path lacks
  `/v2`, and `Failed` while the host can't be reached, retried each poll), and
  the condition `ModulePublished` says why it isn't published. An upload is a
  Normal Event `ModulePublished` and the log line `Published Go module`; an
  invalid module is a Warning Event `InvalidModule`, once per release. See
  [plans/done/app-modules.md](plans/done/app-modules.md).
- **A release can be published to GitHub.** A Crew with
  `spec.publishReleases.github` (`repository: owner/name`, and a Secret the
  user creates holding a token for that one repository) copies its last release
  there once the release runs succeed: the same tag (with an optional
  `tagPrefix`), the same notes (the Gitea release's body, or the titles of the
  release's merged issues, which the operator writes on both) and the same
  files, streamed from the Gitea release. The release is named after the app
  and its version (the Container's `displayName`, else its name, then the tag:
  `CEL Lens for VS Code v0.6.1`), so a repository several apps publish to says
  whose each release is. It becomes the repository's Latest unless
  `publishReleases.latest` is `false`: on a shared repository, every app but
  the one people install sets it, so a library's release doesn't take Latest
  from it. The operator does it, not CI, so the
  token never reaches the code the agents write. It only adds releases: a draft
  marked with the Container and tag, its files, then published, so a retry
  carries on and nobody sees half a release; a release of that tag it didn't
  make is left alone. `status.lastRelease.publish` and the condition
  `Published` say how it went, with the reason; a failure is a Warning Event
  `ReleasePublishFailed`, once per reason, and is retried each poll. See
  [plans/done/release-publishing.md](plans/done/release-publishing.md).
  With `publishReleases.source.path`, the release also brings the app's
  source as of its tag into that folder of the repository (`.` is the whole
  repository), the factory's own files (`model.FactoryOwnPaths`: `AGENTS.md`,
  `docs/agents/`, `.c4factory.yaml`, `.devcontainer/`, `.gitea/`) left out,
  tagged `source.tagPrefix` + the tag (by default `<path>/vX.Y.Z`, the tag Go
  reads in a subfolder). It is one commit on the default branch that replaces
  only that folder, made through GitHub's Git Data API with the same token,
  fast-forward only: a branch that moved on is built on at the next poll.
  Folders of Crews publishing to one repository may not overlap (both fail
  `FolderOverlaps`; a Brief's `spec.releases` refuses it at admission), and a
  source tag on another folder tree is left alone (`SourceTagTaken`). The
  source goes first and the release's tag names the same commit, so
  `Published` means both are out; `status.lastRelease.publish.source` holds
  the commit, tag and folder link, with a Normal Event `SourcePublished`. A
  `go.mod` whose module path or tag `go install` can't find from GitHub
  raises a Warning Event `SourceNotGoInstallable`, and is published anyway.
  See [plans/release-source.md](plans/release-source.md).
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
  never retry it. The planner doesn't give agents a person's work: it files
  it as its own plan issue labelled `kind/by-hand`, which the operator files
  `status/needs-human` in Later, and no issue waits on it. See
  [plans/honest-evidence.md](plans/honest-evidence.md).
- **Context comes from the C4 model.** Agents get the system's needs and KPIs, its
  users, the Container's Relationships, and read-only clones of the related
  Containers' repositories: the agent of a client reads the service it calls.
- **A project's notes to its agents** (#909). `docs/project-notes.md`, when a
  repository has it, is the project's own word to its agents about its
  workspace and ways, such as how CI runs `make check`. No Standard protects
  it, so any issue changes it by a normal pull request; the implement and
  review prompts tell the agent to read it after `AGENTS.md`, which wins with
  the role guide where they disagree. The factory writes no such file. See
  [plans/done/out-of-reach-asks.md](plans/done/out-of-reach-asks.md).
- **Waiting is not failing.** A usage limit, an overloaded API, a temporary
  server error from the model's API (a 5xx) or rejected credentials don't cost
  a round: the Task waits (`AgentUnavailable`) and tries
  the same step again. Neither does a run the infrastructure ended (a node
  shutdown or restart, an eviction, a preemption, an outage that lasted past
  its pod's deadline or its sandbox's shutdown time, a pod deleted before it
  finished, which Agent Sandbox would replace and start over, a Git host that
  stayed unreachable through the runner's retries of a clone or push, about
  five minutes, enough for Gitea to restart on an upgrade): it runs again in the same round
  (`Interrupted`, then `status.reruns`), at most twice per round, so a run that
  is cut short every time still spends rounds. An out-of-memory kill, or a run
  that ran out its time while the cluster ran, is the agent's run failing, not
  an interruption.
- **A protected file without a person.** An issue that needs a protected file, such
  as the Makefile or a CI workflow, gets its own gated Task: the operator files a
  `kind/standard-change` issue, the waiting Task is `Blocked`, and the operator
  merges the reviewed change itself. The agents' rules and `.c4factory.yaml` stay
  out of reach. The gated Task may also change files no Standard protects where
  the repository's checks fail without them, such as a test that pins what the
  protected file produces; its history and pull request name them, and its
  reviewer checks they change only that. Any other protected file is refused.
  A round turned back for touching files the Task may not change, twice in a
  row, ends the Task `Blocked` instead of spending its last round, and the Task
  that waits on that Standard change ends `Blocked` too, so the Brief lists
  both issues with the files that hold them. A Standard change weakens no
  check: it removes or skips no target, test, lint rule or CI job, not even
  for some changes. When the issue can't be met without that, the reviewer
  names the rule and what the author can do instead (for CI that runs the full
  check less often, the Container's `checks`), and the implementer stops on
  purpose with a `Blocked:` line saying both rather than undoing the change.
  A round that leaves every path the change is for as on `main` ends the Task
  at once, `Blocked`, before any review: there is nothing to merge, and its
  message carries what the round before was sent back with, which names the
  rule; the waiting Task ends `Blocked` with it (#929). The change's proposal branch
  (`standard/<task>`) lives as long as its `kind/standard-change` issue is
  open, also after the waiting issue was handed back, so a retry of the
  change finds it; the operator deletes it when the change merges or a
  failed issue's answer closes it. A Standard change whose branch is gone
  anyway says so where failed issues are answered, and a retry of it is
  refused: its Task would have nothing to merge. See
  [plans/done/2026-10-02-standard-change-design.md](plans/done/2026-10-02-standard-change-design.md)
  and [plans/done/standard-change-scope.md](plans/done/standard-change-scope.md).
- **A Standard's fixes reach its Containers.** Bumping a Standard's pinned `ref`
  updates every Ready Container that uses it. The operator renders the Standard
  at the Container's ref and at the new one, and opens a pull request of its own
  with the difference: the new render of each file the repository still holds
  as the old ref rendered it, and the removal of files the new ref no longer
  renders. A file the repository changed since (a Standard change, or a person)
  is merged line by line, three-way, and written when the two changes don't
  overlap, named in the pull request as merged with the repository's changes
  (#880). One whose changes overlap is left out and stays in
  `status.standard.behind`, with the ref its copy was last brought to, through
  later updates until it is settled: `StandardUpdated` is False, reason
  `FilesBehind`, the Container is in *Needs a look*, and the Brief that made it
  lists it in `needsYou`. Where the choice is asked, the Standard's change to
  each such file shows: the diff of the Standard's renders of it at its ref
  and at the new one, and the repository's lines a take would drop, per file
  in the update's pull request and, bounded, in `status.standard.behind` and
  on the Container's page (#975). The person settles it with one annotation naming the
  ref: `c4.factory.io/standard-take` has the operator open and merge a pull
  request of the Standard's version (the repository's other changes kept, the
  lines it drops listed), `c4.factory.io/standard-keep` keeps the repository's
  (`status.standard.kept`) until the Standard changes the file again. The
  operator removes the annotation once it acted on it; one naming another ref,
  given while an update is in flight, or both at once is ignored and removed.
  A take runs like an update (branch `standard-take/<standard>-<ref>`, one
  update or take in flight at a time); closing it leaves the files behind. No
  autopilot takes it for the person. No agent takes part: the update is the
  template's own content, reviewed
  here where the template changed, and it reaches the agents' rules, which
  agents never change. The operator merges it as admin once CI passes on it,
  with every check `main` requires, and it changes only the planned files.
  Closing the pull request drops the update until the Standard's ref, or the
  Container's checks, change again. `status.standard` records the ref the Standard's files are at and the
  update in flight, the condition `StandardUpdated` how it went;
  `scaffoldedFrom` and `.c4factory.yaml` keep the scaffold's refs. See
  [plans/done/standard-update.md](plans/done/standard-update.md),
  [plans/standard-left-out.md](plans/standard-left-out.md) and
  [plans/standard-left-out-diff.md](plans/standard-left-out-diff.md).
- **The owner chooses when the full check runs.** A Container's
  `spec.checks` is `EveryChange` (the default: CI runs `make check` on every
  pull request and push to main) or `BeforeRelease`: each change runs the
  Standard's `make check-light` (build, vet or type check, lint; no test
  suites, no build for every platform), trusting the agent's own `make check`
  in its workspace, and the full `make check` runs on the commit a release
  will be tagged at, before the tag. It takes effect only when the Standard
  says it can (`spec.checksBeforeRelease`); otherwise the full check stays on
  every change and the Container's `ChecksApplied` is False
  (`StandardRunsFullChecks`). The Standard's `check.yaml` renders the choice;
  changing it reaches the repository through the Standard update's pull
  request, and `status.standard.checks` records what the repository runs:
  the scaffold's (also in `.c4factory.yaml`), then each merged update's.
  Until it matches what the Container asks, `ChecksApplied` is False: reason
  `Pending` naming the update's pull request, the update's stop as its reason
  (such as `ChecksLeftOut` or `UpdateClosed`), or `AddedAfterScaffold` for a
  Standard the scaffold didn't have, whose CI runs the full check on every
  change. An
  update whose change of checks would leave out a file the repository changed
  since, such as a hand-edited `check.yaml`, isn't opened: `StandardUpdated`
  is False (`ChecksLeftOut`) naming the file, and the checks stay as they are
  until the Container's checks or the Standard's ref change again.
  With `BeforeRelease` and `tagReleases`, a done release is not tagged at
  once: the Crew pushes `release-check/vX.Y.Z` at the commit, the workflow's
  `full` job runs `make check` there, and its success tags the release and
  deletes the branch. Meanwhile `Released` says `FullCheckRunning` and
  `status.fullCheck` records the branch's commit, when it was pushed and how
  its runs go; only the runs of that branch's push count. A failure tags nothing (`FullCheckFailed`) and files
  `Full check fails for vX.Y.Z`, `kind/bug`, in the release's milestone,
  whose merge doesn't count as merged after the prepare issue; no run within
  the hour leaves the release to a person (`NoFullCheck`). A person who tags
  by hand pushes the branch and waits for `full` the same way. See
  [plans/checks-before-release.md](plans/checks-before-release.md).
- **New shapes, same operator.** Container types `cli` and `extension`, with
  their templates in the template catalog in `templates/`; a Go and Node toolchain whose image ships a warm build cache, a CI runner
  labelled `go`, and two agent sandboxes at once.
- **A Container type `operator`:** a Kubernetes operator, built with
  kubebuilder. It is not a `service` or a `worker`: it ships CRDs and RBAC and
  needs cluster-wide permissions, so the planner tells it apart. The factory
  never installs it; a release publishes what a person, Argo CD or Flux needs
  to. The catalog builds it with Framework `go-kubebuilder` (what a pinned
  `kubebuilder init` writes, regenerated by `make template-go-kubebuilder`),
  Standard `go-operator-standard` (kubebuilder's Makefile with `make check` and
  `make dist`, its lint config and the agents' rules) and Compute `small`, which
  patches the manager's resources; `make test-templates-operator` renders that
  combo and runs its `make check` before and after `kubebuilder create api`,
  then its `make dist`. Deployment `gitea-release` ships an operator's release
  on a version tag: the manager binaries for Linux, macOS and Windows,
  `install.yaml` (the CRDs, RBAC and the manager, from kubebuilder's
  `make build-installer`) and `SHA256SUMS`. No image is built or pushed:
  `install.yaml` names the image `controller:<tag>`, and the operator's README
  says to build and push the image and set it there before applying it. Until a catalog entry supports
  a type, the planner answers that it needs input rather than planning it.
  GitHub issue #723 has the plan, [plans/kubebuilder-operators.md](plans/kubebuilder-operators.md).

## Briefs: an idea in plain words

Decided on 6 October 2026 for GitHub issue #240; the plan, with every
decision and why, is [plans/done/brief.md](plans/done/brief.md).

Writing the model, the catalog, the Agents and the Crew as YAML, and filing the
first issue through Gitea's API, is operating the factory, not describing a
product. A Brief lets a person give the idea in plain words and steer with
follow-up prompts; a planner Agent does the rest and says what it chose.

- **A Brief is one more writer of the model.** `spec.idea` is the idea,
  immutable; `spec.prompts` takes follow-ups, at most 50 listed at once, and
  an earlier prompt never changes. The write that appends to a full list
  retires the oldest planned prompts in the same write: it takes them out of
  the list and counts them in `spec.promptsRetired`, which only grows, so a
  Brief takes prompts for as long as its project runs (#835). The status
  says how many were planned (`status.promptsPlanned`); a prompt no round
  planned is never retired. The status counts every prompt written, retired
  ones included, and the planner's `brief.md` keeps their numbers and says
  how many retired, the model holding what they asked for. The operator
  never writes the spec, so GitOps keeps working (#855): the UI server's
  append retires the planned prompts past the newest ten for the person, its
  patch guarded by the prompts it read, so a prompt appended meanwhile is
  never lost. With kubectl or in Git, the person drops the oldest planned
  prompts and raises `promptsRetired` by as many in the commit that adds the
  51st; a 51st alone is refused with a message that says so, and so is a
  retirement that drops anything but the oldest or miscounts, or that
  raises `promptsRetired` past `status.promptsPlanned`: a CEL rule refuses
  the write rather than a condition reporting the prompt lost (#859). It
  reads the stored status, so it holds only on updates (a Brief made with
  prompts retired is taken) and status writes pass; `promptsPlanned` only
  grows, so a stale read can only refuse a sound write, which a retry
  passes, never let an unplanned prompt go.
  Anything can write it: the UI's box, kubectl or GitOps. With actions off, the UI server
  writes nothing but this: creating a Brief, appending a prompt (retiring
  planned ones when the list is full), and setting
  `spec.paused` from the card's Pause and Resume buttons. With them on, it
  takes the actions under *UI actions* below.
- **A planner Agent plans each round** (the idea, then each batch of new
  prompts) as a Task with `spec.kind: Plan` owned by the Brief, in an agent
  sandbox like any Task. It works in the Brief's own repository, where the
  operator writes the brief, the namespace's current model and the installed
  catalog, and pushes its plan to a branch: the story kinds, the first issues
  and its choices in plain words.
- **The operator checks and applies the plan.** A plan writes only story
  kinds (Project, Person, SoftwareSystem, Container, DataStore, Relationship),
  names only installed catalog entries that support the Container's type,
  changes only the story text of what exists (and a Container's `checks`,
  below) and deletes nothing; anything
  else refuses it, and the planner's next round gets the reasons. The operator
  applies it (Relationships before the Containers they start from, so their
  Connectors are part of the scaffold), makes a Crew for each new Container
  with the Brief's Agents and as many places as the Brief says (below), and
  files the issues in each Container's repository once it is Ready, ready for
  its Crew. What it made carries the label `c4.factory.io/brief`; deleting the
  Brief keeps it. When
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
  [plans/done/brief-issue-order.md](plans/done/brief-issue-order.md).
- **One app uses another's release, never its branch** (#430). Apps stay one
  repository each and share code the way separate repositories do: an issue
  that builds on another Container's code names it in its front matter,
  `uses:`. The plan is refused when the used Container is the issue's own, is
  not in the model, has no Relationship from the issue's Container, or has
  neither a release made nor one the round makes (named in `releases.yaml`,
  with an issue of the round for it). When the Crew tags a release of a repository with a `go.mod` at its
  root, the operator publishes the module to the owner's Go registry in Gitea;
  agent pods and the Go Standard's CI fetch `c4factory.local/...` modules
  through it. A `uses:` issue is filed without `status/ready` and labelled
  ready only once each app it uses has a published version: the round's
  release for an app the round releases, else its newest. Its body then gets
  `Uses <module> <version>: go get it at that version.` per app. While it
  waits, the Brief stays `Applying` and its message names the issue, the app
  and why, such as a release that published no Go module. See
  [plans/done/app-modules.md](plans/done/app-modules.md).
- **It asks only for what costs money or touches access.** Agents, their
  models, a Crew's size, GitProviders and credentials are never the planner's
  to make or change. When it needs one, or can't go on without a decision, the
  Brief's condition `NeedsInput` says what in one sentence, and the round waits.
- **A Brief says how much its Crews build at once** (#533). The
  Brief owns the `parallel` of the Crews it made and sets it every reconcile,
  earlier rounds' Crews included; a Crew it didn't make is left alone.
  `spec.parallel` (1 to 5) wins; else the number a prompt gave, which the
  planner relays in `plan/parallel.yaml` (`parallel: N`, `why:` quoting the
  prompt) only when a new prompt asks, never of its own accord, and the round
  that applies it records in `status.parallel`; else the operator's default,
  3. A plan writing it with no new prompt, out of 1 to 5, or while
  `spec.parallel` is set is refused, and `brief.md` tells the planner the
  number now and where it comes from. A hand
  edit of such a Crew's `parallel` is set back, and `status.apps` shows each
  Crew's, which the Brief's card shows per app ("3 at a time"); the create
  form can set `spec.parallel`. The agent-slot queue
  still caps how many agents run. See
  [plans/done/brief-parallel.md](plans/done/brief-parallel.md).
- **A Brief sets when its apps' CI runs the full check** (#977). A
  Container's `checks` (`EveryChange` or `BeforeRelease`) is the Brief's to
  set, as its Crews' `parallel` is: a plan's Container may say `checks` in
  `plan/model.yaml`, only when a new prompt asks, never of the planner's
  own accord, and the round sets it on the Container, a new one or one the
  Brief made. A Container the plan writes without `checks` keeps its
  setting, so one a person made isn't undone; `model/model.yaml` leaves it
  out so a copy does too, and `model/containers.yaml` shows each
  Container's setting, `model/catalog.yaml` each Standard's
  `checksBeforeRelease`. A plan setting it with no new prompt, to another
  value, or on a Container the Brief didn't make is refused. The Standard
  still decides whether it takes effect (`ChecksApplied`, reason
  `StandardRunsFullChecks`), and the applied round's summary says so in a
  sentence the operator adds.
- **A Brief pauses and resumes its Crews in one step** (#677). While
  `spec.paused` is true, the Brief sets `spec.paused` on every Crew it made,
  marking each with `c4.factory.io/paused-by: brief`, starts no new round and
  tends no failed issue; a Task already started, its planner's included, runs
  to its end. Its condition `Paused` names the Tasks still running, then says
  nothing is. Set back to false, it unpauses only the Crews it marked, so a
  Crew a person paused by hand stays paused. The Brief's card pauses and
  resumes it. See [plans/upgrade-in-place.md](plans/upgrade-in-place.md).
- **A Brief files no issue that no change may make** (#909). `AGENTS.md`,
  `docs/agents/` and `.c4factory.yaml` (`model.AgentNeverPaths`) change
  through no pull request, so `brief.md` names them and a prompt that asks
  for a change to them gets no issue: the round's summary and choices say
  what wasn't planned, why, and that notes for the agents go in
  `docs/project-notes.md`, which a prompt may have an issue write instead. A
  request asking for one is answered `skip` with that why, or taken as such
  an issue. The plan check refuses an issue whose title or acceptance
  criteria name one of those paths, so the planner's next round, not a
  Crew's tries and a release, pays for the mistake. See
  [plans/done/out-of-reach-asks.md](plans/done/out-of-reach-asks.md).
- **Follow-ups keep scaffold once.** A change to a scaffolded Container
  becomes an issue in its repository; a new Container is added and scaffolded;
  a Relationship added from a scaffolded Container shows `AddedAfterScaffold`
  and its wiring becomes an issue.
- **A Brief's apps are released when the plan says so.** A plan names the
  Containers that ship releases (`plan/releases.yaml`); the Crew the Brief
  made for each then tags and prepares releases, and each round files that
  Container's issues in release milestones, so a release follows once they
  are done. Each issue goes where it belongs: a fix (`kind/bug` or
  `kind/test`, or a taken suggestion the plan marks `fix: true`) into the
  patch release right after the release in progress, joining an open one that
  takes work or else made and due before the planned releases; any other work
  into the round's next minor version, due after every open release. A
  milestone is made only when an issue goes into it. Applying a round also puts
  such a Container's open ready or in-progress issues that sit in no
  milestone (filed before releases were on) into its first open release with
  no prepare issue, so work it already has never waits behind its own later releases (#599); a
  proposed suggestion, a `kind/by-hand` issue and one handed back to a person
  stay where they are. A person turns releases on
  with a prompt, or by naming the Container in `spec.releases` with a GitHub
  repository and a Secret holding a token for that repository only, which
  also has the Crew publish its releases there. Releases only turn on; a Crew
  the Brief didn't make is left alone. `status.apps` and the Brief's card show
  each app's latest release and where it went, and say plainly when releases
  are off and how to turn them on. See
  [plans/done/brief-releases.md](plans/done/brief-releases.md) and
  [plans/done/brief-patch-releases.md](plans/done/brief-patch-releases.md)
  and [plans/done/fixes-patch-next.md](plans/done/fixes-patch-next.md).
- **The loop is tested without a model.** A planner Agent with
  `harness: stand-in` plans from the catalog alone: a Container named after
  the Brief on the first Framework, with an issue for it, and an issue for
  each follow-up prompt and each request it takes. `make e2e` takes a Brief through two rounds that way,
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
  round, it takes a suggestion out of the Later milestone, into the round's
  release milestone when its Container ships releases, and labels it
  `status/ready`, or closes it, with a comment quoting the planner's why and the
  round's prompts. A suggestion no prompt answers keeps waiting: nothing is
  built without the person's say, and labels and milestones stay the
  operator's.
- **A request from GitHub is planned like a prompt** (#796). A Brief whose
  `spec.releases` entry takes requests keeps the allowed authors' open issues
  in `status.requests`; one let through (at once, or once its number is in
  `accepted`) starts a round as a new prompt does, unless the Brief is paused,
  and is never written into `spec.prompts`. The round takes every request let
  through (`status.roundRequests`); the workspace lists them in
  `model/requests.yaml` and `brief.md` says each is a request from that GitHub
  user. The plan answers each in `plan/requests.yaml`: `take` with the round's
  issues it became, or `skip` with why. A plan that leaves one out, answers
  one the round didn't take, or takes one with no issue of the round is
  refused like any bad plan. Applying the round records each request's issues
  and release, then comments on the GitHub issue once, with a marker: what it
  became and the release it is in, or the skip's why, closing a skipped one as
  not planned. A round that fails or asks for input hands its requests back
  to the next round, which they don't start by themselves. Once every issue a
  request became is closed and a published release holds each (its
  milestone's, or a later one), the Brief comments once "Shipped in vX.Y.Z"
  with the published page, or "Done in" the issue for one in a Container
  that publishes nowhere, closes the GitHub issue as completed, and drops it
  from `status.requests`. A request a person closed on GitHub meanwhile hears
  nothing more. `accepted` and `declined` hold at most 200 numbers each, and
  the operator never writes them: it lists in `status.requestsSettled` the
  numbers whose issues are closed and no request has, the UI's *Accept* and
  *Decline* drop those, and a kubectl user may (#824). See
  [plans/brief-requests.md](plans/brief-requests.md).
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
  round's prompts. It closes an issue that waits on another the round closes
  (a Standard change) after that one, whatever the plan's order; one that
  waits on an open issue the round doesn't close the Git host won't close, so
  it stays handed back, with a comment saying so, and `Planned` names it
  while the rest of the round applies (#976). See [plans/brief-failed-issues.md](plans/done/brief-failed-issues.md).
- **A Brief looks after its own handed-back work** (#486).
  `spec.autopilot` is `Off`, `Ask` or `On`; unset is `Ask`. `status.autopilot`
  reports the mode in effect, set every reconcile, so the UI copies no default
  (#750). The Brief watches
  only what it made: failed issues, its Containers' Tasks and Crews. What
  heals by itself (a usage limit, an agent-slot queue, a Standard change in
  review, a Git host coming back) is left alone for a window from one table
  in `internal/model`. When a failed issue is due, a tending round, a planner
  round with no new prompt, answers it `retry`, `close` (with new issues to
  split), `wait` (until a Task merges, an app releases or 24h pass) or `ask`;
  in `Ask` every answer waits for the person, and the round's summary says
  what it proposes, not the planner's `summary.md` (#872). Everything else that is due,
  and every `ask`, is one entry in `status.needsYou` with the choice the
  Brief would make and why. The Crew's three tries still hold, an issue is
  answered once per failure, and tending rounds come at most hourly. A
  failed release step (a `kind/release` issue) is judged by its milestone as
  it is now: the planner sees the issues Tasks merged into it, and when, next
  to when its last try stopped, and a plan that closes it, or asks
  recommending to, while work merged after that try is refused, so it is
  tried again (#624). Each
  is a history entry, outcome `Tended`, so a prompt can overrule it. A
  tending round's plan holds only `plan/failed.yaml`, `plan/summary.md` and
  new issues; it leaves the story, the `Planned` condition and the prompts
  planned as the last prompt round left them, and none starts while that
  round failed or asked for input: a prompt goes first. The Brief's card
  shows one *Needs you* box (`needsYou` with `NeedsInput`), its tending
  rounds and its mode, and the create form lets the person choose the mode;
  the Work area's *Needs a look* names the Brief that watches an item and
  when it acts. A failed Task leaves *Needs a look* once it is answered: a
  newer Task works the same issue of the same Container, or a round tended
  the issue with a retry or a close after the Task failed; so an issue that
  failed several times shows one line, its newest attempt. See [plans/done/brief-autopilot.md](plans/done/brief-autopilot.md).
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
  Agents, and an upgrade updates the catalog and Agents in place. A person gives the Agents' token once, as a Secret in the release
  namespace, and the operator shares it with every project; a project with a
  Secret of that name of its own keeps it
  ([plan](plans/done/shared-project-credentials.md)). A Brief whose Agent's token
  is missing says where to give it: in the release namespace for every
  project, or in its own for this one, and names the namespace label when
  its namespace hasn't opted in.

## The UI: areas and object pages

The UI draws the story and the factory from the resources, live through
watches. It is one client among several: the CRDs are the truth, and the UI
enforces nothing; a write it makes is checked by the API server like any
other, and a refusal shows the API server's message. Where it needs a default
to describe an object, it says "the factory's default" and shows a number
only when `status` carries the effective one. The plan behind it is
[plans/done/ui-lifecycle.md](plans/done/ui-lifecycle.md).

- **Six areas,** in the navigation in this order:
  - *Needs you*: what waits on a person, across projects (below).
  - *Briefs*: one project's Briefs, a card each, and the box to describe a
    new app.
  - *Story*: one project's model, the system context (C4 level 1) and one
    system's containers (level 2), with the project's story in one line from
    its Briefs to its releases.
  - *Work*: across projects, the Tasks on a board by stage, the Crews and
    their releases, where the time goes, and what needs a look; beside
    them, the factory's health and agent slots.
  - *Factory*: the factory's health (`GET /factory/health`) and the agent
    slots.
  - *About*: the versions running, and links to the running release's docs
    and notes and to the repository.

  The UI opens on *Needs you* when it has items, else on *Story*, and stays
  where it opened as items come and go. Only the area on screen watches the
  cluster.
- **An object page** for each kind the UI shows: Brief, Container, Crew,
  Task, Project, SoftwareSystem, Relationship, Session, Standard, Compute,
  Deployment and GitProvider (its Secret's name, never its data). Every page
  opens with the same header (`ui/src/components/ObjectHeader.tsx`): the kind
  and name with a breadcrumb to what it belongs to; the kind's lifecycle as a
  stepper, the current phase marked and side exits such as `Failed` or
  `Blocked` off the path; what a person does now; then its conditions, its
  timeline where the kind keeps one, its children and its links out. Each
  kind's path and what a person does at each step live in one table,
  `ui/src/model/lifecycle.ts`, read only from `status`, so no page invents
  its own. A step the UI can't take shows the exact next one: a link to the
  pull request or issue, or a `kubectl` line to copy.
- ***Needs you*** lists, across projects and grouped by project, every item
  the resources flag for a person (`ui/src/model/needs.ts`): a Brief whose
  `NeedsInput` or `NeedsYou` is True; a Container `Failed`, or whose
  `StandardUpdated` says its update pull request waits on a person or its
  Standard files are behind (`FilesBehind`); a Task
  `Blocked`, or `Failed` and not yet answered by a newer Task or a round; a
  Crew whose `Released` leaves a release to a person, or whose
  `status.lastRelease.run` is `Failed`. Each says why in the condition's own
  reason and message, since when, and where it is done: the object's page, a
  link out, or a `kubectl` line. Every item comes from `status`, none from
  timing.
- **Versions,** in the footer of every page and in *About*: the UI's own
  (stamped by its build, `dev` otherwise), the operator's and the chart's
  from Project status (*Status* above), and the CRD API version it reads. A
  UI and an operator of different versions get a warning to upgrade them
  together; two `dev` builds don't, and no Project reporting one yet shows
  "unknown".
- **The writes.** With actions off, three: create a Brief, append a prompt
  to one, and pause or resume one, each from the Brief's card or page. With
  `ui.actions.enabled`, those under *UI actions* below, each offered on the
  page of the object it changes. Nothing else: no deletes, no Secrets,
  nothing in the Git host.
- **Every page is a link.** The address is query parameters:
  `area=needs|briefs|story|work|factory|about`; an object page is
  `ns=<namespace>&kind=<Kind>&name=<name>`; `project=`, `view=containers`
  and `system=` pick Story's project and diagram; `demo=1` is demo mode. Back
  works. Links from before the areas still open what they showed:
  `view=factory` opens *Work* (with `task=<namespace>/<name>`, that Task's
  page), and `view=`, `system=` or `project=` alone open *Story*; the
  address is rewritten to the area's.

## UI actions: as the person signed in

Decided on 9 October 2026 for GitHub issue #706; the plan, with every decision
and why, is [plans/done/ui-actions.md](plans/done/ui-actions.md).

Most of what a person does to steer the factory is a field on a resource, so
the UI can take it, made by the API server like any other write: the CRD
schemas, CEL rules and the admission policy decide, and the UI shows the API
server's message when it refuses one. The UI still enforces nothing.

- **Actions are off by default** (`ui.actions.enabled: false`). Off, the UI
  server writes only the three Brief writes above.
- **Who acts depends on how the page is reached.** Over `kubectl
  port-forward` (`ui.allowedHosts` empty), writes are made as the UI's
  ServiceAccount, with no sign-in: whoever reaches the page can already reach
  the cluster. Behind an Ingress, a person signs in with OIDC (authorization
  code with PKCE) and every write is made as them, with `Impersonate-User`
  and `Impersonate-Group`, so the cluster's RBAC decides. The chart refuses
  `ui.actions.enabled` with `ui.allowedHosts` unless `ui.auth.oidc` is set; a
  trusted proxy header is never enough, since anything else that reaches the
  pod can send it. Reads stay the UI's ServiceAccount's, as today.
- **Impersonation, not the person's token,** so it works on any cluster,
  Kind included, without configuring the API server for OIDC. The UI server
  checks the ID token (issuer, audience, expiry, signature against the
  issuer's keys) and impersonates the user `ui.auth.oidc.usernamePrefix` +
  the configured claim (`oidc:` + `email` by default) and the token's groups
  with `ui.auth.oidc.groupsPrefix`, never a name the browser sends, so no
  signed-in person can become a cluster user or `system:` group. The
  ServiceAccount gets `impersonate` on users and groups only when
  `ui.auth.oidc` is set; RBAC can't narrow that to the prefixed names, so
  the UI pod is as strong as the strongest user it may impersonate. A
  username from the `email` claim needs `email_verified`. The OIDC client secret is a Secret the person makes
  and the chart names (`ui.auth.oidc.existingSecret`); the browser never sees
  it, nor the tokens: the session is an opaque, `HttpOnly`, `Secure`,
  `SameSite=Lax` cookie keyed to server memory, so a restart of the UI signs
  everyone out.
- **The writes, each one allowlisted request** in `ui/server.mjs`, refused
  there for any other path, method, content type or field, and by RBAC for
  any other resource or verb:

  | Kind | Request | Fields |
  | --- | --- | --- |
  | Brief | `POST` | a new Brief (as today) |
  | Brief | `PATCH`, JSON patch | append to `spec.prompts`; `spec.paused`, `spec.autopilot`, `spec.parallel`, `spec.releases` (only after a `test` of the `resourceVersion` it was worked out from; a stale one is refused) |
  | Crew | `PATCH`, JSON patch | `spec.paused`, `spec.parallel`, `spec.tagReleases`, `spec.prepareReleases`, `spec.publishReleases` (set, or removed to stop publishing; only after a `test` of the `resourceVersion` it was worked out from, and a stale one is refused) |
  | Crew | `PATCH`, merge patch | annotation `c4.factory.io/rerun-release` |
  | Container | `PATCH`, merge patch | annotations `c4.factory.io/retry`, `c4.factory.io/standard-take` and `c4.factory.io/standard-keep` |
  | Container | `PATCH`, JSON patch | `spec.checks` |
  | Task | `POST` | `kind: Implement`, `containerRef`, `agentRef`, `reviewerRef`, `goal` |
  | Standard | `PATCH`, JSON patch | `spec.template.ref` |

  A *Needs you* item's one-click answer (take or skip a suggestion; retry,
  close or wait on a failed issue) is a prompt appended to the Brief, worded
  from the item, since a person answers those in a prompt. The UI's
  ClusterRole `<ui>-actions` grants exactly `create` and `patch` on these
  kinds; over port-forward it is bound to the ServiceAccount, and behind an
  Ingress the chart binds it to `ui.auth.oidc.actionGroups`, when set, for a
  cluster admin to grant the signed-in people.
- **A Crew re-runs its failed release run on request.** Setting or changing
  `c4.factory.io/rerun-release` on a Crew whose `status.lastRelease.run` is
  `Failed` re-runs that release's failed workflow runs on the Git host, once
  per value, and the Crew watches them as before. A value set while the run
  hasn't failed does nothing, with a Normal Event saying so.
- **Retrying a Container's scaffold warns first.** The UI says the
  repository must be empty and links to it before it sets
  `c4.factory.io/retry`.
- **Bumping a Standard's `ref` asks first.** The dialog names how many
  Containers on that Standard will get an update pull request.
- **Out:** deletes (they cascade and can't be undone), Secrets and
  credentials (the browser never sees them), anything in the Git host
  (merges, reviews, labels: the UI links there), and editing the model
  itself (Systems, Containers, DataStores, Relationships), which would be a
  model editor of its own; a Container's `spec.checks` is a setting, not the
  model.

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
| UI | release namespace | `ui.enabled`, on by default; its actions off until `ui.actions.enabled`, and behind an Ingress only with `ui.auth.oidc` (*UI actions*) |
| Gitea | `gitea.namespace` (`gitea`) | `gitea.enabled`, on by default; admin password and the runners' registration token generated in the cluster by Gitea's pod into Secrets `gitea-admin` and `gitea-runner` when missing, never rendered by Helm, and kept on upgrade; namespace and volume kept on uninstall |
| Seed job | Gitea's namespace | a hook Job after every install and upgrade: template organization and tags, a `gitea-credentials` Secret, a GitProvider and, when the namespace has none, a Project for each project in `gitea.projects`; labels a project namespace the chart didn't make so it receives the shared Secrets; applies the catalog, the Agents and the GitProviders the chart renders into ConfigMap `c4factory-catalog` with server-side apply, so an upgrade updates them in place, keeping their UIDs and the fields the chart doesn't set |
| Project namespaces | each of `gitea.projects` | `projectNamespaces.create`, on by default; made when missing, labeled `c4.factory.io/shared-secrets: "true"` and kept on uninstall; a namespace that already exists is left to the person |
| Shared Secrets | the release namespace, copied to each project namespace | the Agents' token Secret (`agents.credentialsSecret`) and the pull Secrets (`global.imagePullSecrets`), which the person makes once; the operator copies each into every labeled namespace, before it has a Project too (a Brief there needs the token for the round that makes it), keeps its copies in step with the source, and never touches a Secret of that name it didn't make, which is how a project keeps a token of its own; Task sandboxes list the pull Secrets |
| CI runners `base` and `go` | Gitea's namespace | on the released `gitea-runner-*` images |
| GitProviders | named namespaces | `gitProviders`, for a Gitea the release does not run (`gitea.enabled=false`); each names a Secret the user creates (`existingSecret`), as the chart renders no Secret and takes no token in its values; a hook Job on the seed image applies them in place from ConfigMap `c4factory-gitproviders` |

A device installs itself only what is another project's or its own: Agent
Sandbox (its controller and CRDs) before the chart, the Agents' token in the
release namespace, and, while the GHCR packages are private, a pull secret in
the release namespace and Gitea's or the images loaded into its cluster. Adding
a project is adding its name to `gitea.projects`. The Kind test environment of this repository keeps its
make targets (`make gitea seed deploy`) and locally built images.

**An upgrade leaves running work alone** (#677). A device upgrades in place
with `helm upgrade`, while Tasks run. A Task's state lives in its status, so
the restarted operator picks up its sandbox, round and pull request where
they were; a sandbox is never changed under its agent and keeps the token it
started with; the Agents, catalog and GitProviders are updated in place, never
deleted; and a sandbox waits out a Gitea restart, a run that still can't
reach it being rerun without spending the round. New Agent values apply from
the next run, and a new Standard ref through its update pull request. A
person who wants a quiet moment pauses a Brief and waits for its `Paused`
condition to say nothing is running. See
[plans/upgrade-in-place.md](plans/upgrade-in-place.md).

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
- `k8s-manifests` (Deployment): a Dockerfile and plain Kubernetes manifests. The Dockerfile follows the Framework: uvicorn for `python-fastapi`, a Vite build served by an unprivileged nginx for `react-vite` (type `static-site`). It patches in `compute.yaml` when a Compute is chosen, and lists `connector-<name>.yaml` for each Connector, so a Connector used with it ships that file.
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
