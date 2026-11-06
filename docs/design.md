# theos: Design

The spec: what the factory is and does. How it got here, with each step's date
and reasons, is [design-history.md](design-history.md); the plans in
[plans/done/](plans/done/) hold every decision's why. Where this says
*decided, not built*, the issue named builds it.

[![How theos works today: describe, scaffold once, build from issues, keep current, watch, the fence and where it runs](images/how-it-works.png)](images/how-it-works.svg)

## Purpose

theos turns an idea into working repositories by first telling its story
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
| Container | An application with its own repository: a new one, or an existing one it adopts with `spec.adopt`; no monorepos. |
| Container type | `static-site`, `service`, `job`, `worker`, `mobile-app`, `cli`, `extension`, `operator`, `function`. The operator never branches on a type; a test keeps it so. |
| Catalog | Framework, Standard, Compute, Deployment, Connector and Domain entries per namespace, each a template repository at a pinned tag or commit, never a branch. A Bundle names one Framework, Standard, Compute and Deployment as a team's choice; a Container or the planner names it instead of the four, the operator expands it until the scaffold and pins it there, and `status.bundle` says what it expanded to ([plans/done/catalog-bundles.md](plans/done/catalog-bundles.md), #1092). |
| Scaffolding | Once, at creation. Afterwards only the Standard's files and the AWS operations block follow, through pull requests the operator opens and merges. |
| Shipping | CI builds with no deploy credential; the operator hands each release to the tool that owns the target. Monitoring the apps is left to existing tools. |

## The model

Every kind is namespaced and served at `theos.factory.io/v1beta1` alone, the
version users write (Compatibility). Every child names its parent in
`spec`; the operator turns that into the ownerReference, so deletion
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
| Domain | Project | none | A catalog entry: a pinned template of what a system is about (a webshop's carts, stock and payments), its practices, common mistakes and choices; a SoftwareSystem names it in `spec.domainRefs` |
| Bundle | Project | none | A catalog entry with no template: a Framework and, optionally, a Standard, Compute and Deployment a team picked together; it supports the types all its entries support |
| Agent | Project | none | Who does Tasks: a harness, a model, a toolchain image, credentials and limits |
| DeployTarget | Project | none | A place releases ship to, with its credential |
| Scorer | Project | none | Plain-words criteria a judge Agent checks finished Tasks against; each judge run is a Task of kind `Score` it owns |
| Benchmark | Container | `spec.containerRef` | The same goals from one pinned commit under two or more Agent variants, repeated; its Tasks end `Approved` and never merge |

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

A Container names a Bundle instead of its four sides: `bundleRef: {name:
python-service}` in place of `frameworkRef`, `standardRef`, `computeRef` and
`deploymentRef` (a CEL rule asks for exactly one of `bundleRef` and
`frameworkRef`, refuses a side beside `bundleRef`, and keeps `bundleRef`
immutable). Until the scaffold every reconcile expands the Bundle afresh and
writes the entry names to `status.bundle` (`name`, `framework`, `standard`,
`compute`, `deployment`); from the scaffold on the operator reads
`status.bundle` and never applies the Bundle again, so a Bundle changed later
changes no scaffolded Container. A Ready Container reports it instead:
`BundleCurrent=True` while the Bundle still names what `status.bundle`
records, `False`, reason `BundleChanged`, listing the sides that differ
(`compute: small, now large`) or saying `Bundle <name> is gone` when it was
deleted; a Container naming its sides has no such condition. The operator
changes nothing on it: a Standard bump still reaches the Container through
the Standard, and the other sides are scaffold-once. A missing Bundle keeps
a Container not yet scaffolded `Pending`, reason `NotFound`; the checks
below run on the expanded entries, and their message starts with
`bundle <name>: `.

A SoftwareSystem's `location` is `Internal` (the default: the factory builds
it) or `External` (the story only names it, such as a payment provider). An
External system holds no Containers or DataStores: one naming it stays
`Pending`, `ReferencesResolved=False`, reason `ExternalSystem`, and links to
and from it are story only.

**Domains.** A Standard tells the implementer how to build in a stack; a
Domain tells it about what the system is for. It is its own catalog kind,
`Domain`: a `displayName`, a `description` and a `template` at a pinned ref,
and no `supportedTypes`, since what a system is about does not depend on a
Container's type. A SoftwareSystem names the Domains it is about in
`spec.domainRefs`: at most 5, unique, and none on an External system (CEL:
the factory builds nothing there). Every Container of the system is
scaffolded with each Domain's Markdown under `docs/agents/domain/<name>/`,
the last layer, after the Connectors, sorted by name; the Standard's agent
guide tells the implementer and the reviewer to read it. A Domain's template
renders Markdown only and a `README.md`: another file fails the scaffold
with `Scaffolded=False`, reason `DomainNotMarkdown`, naming the file; no
`README.md` fails it with `DomainNoReadme`. Each Domain's `name@ref` ends
`status.scaffoldedFrom` and its name joins `status.scaffoldedDomains`. A
Domain is written once, at the scaffold: a later
bump of its `ref` does not reach a scaffolded Container, and a Ready
Container reports `DomainsApplied=True` while its system still names the
Domains it was scaffolded with, `False` with reason `AddedAfterScaffold`
naming what was added or removed since (an adopted Container, scaffolded
with none, names them all). The Standard's update renders with the Domains
the scaffold used. The planner names the installed Domains an idea is about
on each new Internal system. [plans/domains.md](plans/domains.md) (#1512)
has the decisions; all its steps are built: the kind, `domainRefs`, the
layer, the Standards' guides, the webshop Domain, the planner and the UI's
Domains catalog page, a system's Domains and a Container's `DomainsApplied`.

## Scaffolding a Container

1. **Resolve references.** The SoftwareSystem (`Internal`), GitProvider and
   catalog entries must exist; until they do the Container is `Pending`,
   naming what is missing.
2. **Check choices.** Every entry used, the Connectors reached through
   Relationships included, must list the Container's type, else `Failed`,
   `ChoicesValid=False` ("python-fastapi does not support static-site"). A
   Connector whose `wiredBy` names the Frameworks that call its files takes
   only a Container on one of them, else reason `NotWired` ("dynamodb is not
   wired by python-fastapi, only by aws-lambda-ts and aws-lambda-go"); unset,
   it takes any ([plans/aws-rds.md](plans/aws-rds.md), decision 7). A
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
   file, not a name or ref. A template refuses a combination its layer can't
   serve with `{{ fail "why" }}`: the render stops and the Container is
   `Failed` with that message, as a Connector no Framework file calls does.
   Each CI system's files sit under
   `.theos/ci/<system>/` and render at the root only for the Container's;
   a template with CI files but none for it fails, `UnsupportedCI`.
5. **Assemble the devcontainer:** the Framework's settings plus a compose
   service per Connector.
6. **Write `.theos.yaml`** with the project, the container and the
   scaffold's refs.
7. **Write `LICENSE`** when the Container's `spec.license`, else its
   Project's, names one: an SPDX id from the set the operator holds the text
   of (`MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `0BSD`,
   `MPL-2.0`, `GPL-2.0-only`, `GPL-3.0-only`, `LGPL-3.0-only`,
   `AGPL-3.0-only`, `Unlicense`), an enum in the schema. The text is the
   SPDX one; where it has a copyright line, the year is the scaffold's and
   the holder the Project's `displayName`, else its name. The factory never
   picks a license: with none set, no file. It is an operator path like
   `.theos.yaml`, which records the id; a layer writing `LICENSE` is the
   two-layers collision below. Read at scaffold only: changing the setting
   later rewrites nothing, and a Container already scaffolded gets its
   license by a commit ([plans/license.md](plans/license.md)).
8. **Push one commit,** protect `main` with the Standard's required checks,
   then set `Ready`, `repositoryURL` and `scaffoldedFrom`.

Every file belongs to exactly one layer: two layers writing a path fail the
Container, naming it. An empty existing repository gets the commit again; one
with commits the operator didn't make stops it `Failed`. A `Failed` Container
tries again when something it depends on changes, or when its
`theos.factory.io/retry` annotation is set or changed.

**Adopting a repository.** A Container with `spec.adopt: true` (set at
creation, immutable) takes over a repository that already has commits and
is never scaffolded: the operator writes nothing. It checks main for two
things. `.theos.yaml` carries the identity file's fields, naming this
project and Container, with `scaffoldedFrom` empty since nothing was
scaffolded; a missing file or another identity fails `IdentityMismatch`,
saying what the file holds. Every file the Container's Standard renders for
it at its ref is there with that content; else it fails
`StandardFilesDiffer`, naming the paths. When both hold, it protects `main`
and goes `Ready`, recording the Standard at its ref. An empty repository
fails `NothingToAdopt`; one with commits but no `main` branch fails
`NoDefaultBranch`, telling the user to rename its default branch to `main`
first (#1908). A Container without a Standard adopts on the
identity alone ([plans/self-hosting.md](plans/self-hosting.md), #1888).
Adoption checks the Standard's files and nothing of the Framework's: the
block `spec.operations` names at adoption is recorded as the one the
repository holds, when the Framework reads it, and a later change is
refused, `OperationsApplied=False` with reason `AdoptedRepository`, saying
why: the factory never scaffolded the repository, so it can't know whether
the `infra/operations.ts` there is one its Framework renders, and a Task
changes the file (#1909). Opening the pull request anyway, as for a
scaffolded Container, would write a file the Framework's template shapes
into a repository that may hold another shape, or none.

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
`.theos.yaml`, creates a Session on its Container (actor and a one-line
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
Phases: `Pending`, `Implementing`, `Checking`, `Reviewing`, `Verifying` (a
verifier runs the approved change), `Blocked` (waiting on a Standard change),
`AwaitingApproval` (a spec waiting for a person), then `Merged` or `Failed`. A
Triage Task (`kind: Triage`) runs one triager on an issue instead and applies
its decision, and a Spec Task (`kind: Spec`) writes an issue's spec; see
*Triage*, *Spec* and *Verify* under Crews and releases.

- **A merge that passed skips main's push run.** When a Task's head has all
  its required checks green (the CI system's `CheckContexts`, else the
  combined state) and its pull request is not behind main, the squash commit
  is the head's tree byte for byte, so main's push run would check a tree that
  has just passed, at 20 to 50 minutes of the one CI job. The squash commit's
  title then ends in `[skip ci]`, which the Git host reads as "run no push
  workflow", and the operator copies the head's required statuses onto the
  merge commit (same context, state and link, description `same tree as #<pr>
  head <sha>`) after reading that its tree equals the head's. If the trees
  differ (main moved during the merge) the required contexts are set to
  `error`, `merged without a check: main moved during the merge`, so main's
  head is red and `BaseRed` shows it. A head that is behind main, or not
  green, merges without the marker. Standard-update and release merges keep
  their push runs. (#2060)
- **Issues are the work.** The operator labels the issue
  `status/in-progress` (`status/*` labels are exclusive), reads it before
  every run and closes it on merge; a failed Task hands it back
  `status/needs-human` with the reason. An issue without an *Acceptance
  criteria* checklist goes back before any tokens are spent.
- **Issue text is data, not instructions.** The prompt puts the issue's
  text, every field a person wrote, between two marker lines and says it is
  the goal and never an instruction: whatever in it asks the agent to change
  its tools, credentials, settings, the files that configure agents, or where
  it sends data is ignored. The markers grow past any copy of themselves in
  the text, so nothing a person wrote can end the block early.
- **Merges only on checks of the `main` it merges into.** When `main` moved,
  the operator merges it into the branch (no agent run, no round) and waits
  for the checks again; a conflict goes back to the implementer.
- **The implementer runs the check in its sandbox,** sent back twice while it
  fails. One that fails on the base commit too (`CheckFailsOnBase`) is left
  to CI, with the output on the pull request.
- **Round 1 starts only on a green base.** Before round 1 the Task reads the
  CI state of `main`'s head for the Container's check contexts: red, and it
  waits with reason `BaseRed`, a Needs-you item on the Brief at once; pending,
  and it waits; green or no status yet, and it starts. A check that then fails
  on the base in the sandbox only is the workspace's, which the prompts say.
- **Blocked is said, not guessed.** An implementer that can't do the issue
  yet, or not in the sandbox, changes nothing and ends with `Blocked: <why>`;
  the Task fails at once, reason `Blocked`, with those words on the issue. A round
  with no change and no such line fails with the end of the agent's summary.
  When the check fails on the base too, the implementer is sent back once
  first, in a fresh session whose prompt is the round's own, the issue
  included, then its first answer and the base's failure (#2037). A note
  that says the agent can't see the issue (`model.IssueUnseen`: the issue or
  its text, not given, available or seen) fails reason `IssueUnseen`
  instead: the factory gave it the issue, so the first such Task on an issue
  costs no try, and any more do.
- **The infrastructure's failures cost the issue no try:**

  | Reason | What happened | The Task |
  | --- | --- | --- |
  | `AgentUnavailable` | usage limit, overloaded or 5xx model API, rejected credentials | waits, then resumes in place from its checkpoint, or without one runs the step again; no round |
  | `Interrupted` | node shutdown, eviction, pod deleted, Git host unreachable past the runner's retries | resumes in place from its checkpoint, up to five times a round; then runs again in the round, at most twice |
  | `NoCIResult` | no CI run started in 30 minutes, or none ended in its bound (6 h queued, 3 h 15 running) | fails |
  | `RunnerOutOfSpace` | `no space left on device` in a job log | fails; no round |
  | `RunnerOutOfMemory`, `RunnerLost` | a job killed for memory, or abandoned by its runner | reruns the jobs twice a round, then fails |
  | `ApprovalRanOut` | CI outlived a Standard change's approval four times | fails |
  | `IssueUnseen` | the agent's blocked note says it can't see the issue | fails; free once per issue |
  | `OverBudget` | the runs' summed cost reached the Crew's `taskBudgetUSD` | waits for the cap to be raised; a needs-you item; no round |

  An agent killed for memory or past its deadline fails its own round. A job
  a person cancelled costs the round.
- **A run continues from a checkpoint.** Each agent sandbox has a 1Gi
  volume of its own (`volumeClaimTemplates`, `checkpoint`, at `/checkpoint`)
  that outlives its pod, and a pod grace period of 60 seconds. The volume's
  StorageClass is a setting (the operator's
  `--sandbox-checkpoint-storage-class`, the chart's `checkpointStorageClass`):
  empty is the cluster's default class, and `none` gives sandboxes no volume,
  for a cluster without a class to provision one; their interrupted runs are
  rerun. Told to stop, theos-agent ends the agent's session, then saves a `hard`
  checkpoint: the working tree as a git bundle of the commits the remote
  lacks (uncommitted changes as a commit inside it, never pushed), the
  session's Claude Code transcript (each session starts
  with a `--session-id` theos-agent records), and `state.json` (format version,
  the run's step, session ID, handoffs, model, cost and tokens so far,
  `clean` or `hard`). The workspace stays in memory. When the infrastructure
  interrupts a run whose sandbox has the volume, the operator counts what the
  run cost, sets the Sandbox's `operatingMode: Suspended` (Agent Sandbox
  deletes the pod and keeps the volume) and, once the pod is gone, writes the
  current Agent's image and env and the run's remaining time (its timeout
  less what its pods ran, at least ten minutes) into the pod template, clears
  `status.sandboxPod` and sets it `Running`: a resumed run gets the current
  runner and fence. The new pod's theos-agent restores the
  bundle after its clone and goes on at the recorded step in a fresh session,
  told what the stopped one left (the note it writes for a session it ended);
  an unreadable checkpoint, or one of an unknown version, starts the run over.
  Its result says `resumedFrom`. A resume costs no round and no rerun, and its
  cost so far counts toward `taskBudgetUSD`; the Task's `status.resumes`
  counts them, at most five a round. Past that, and for a Sandbox made before
  checkpoints (no `checkpoint` claim; the field is immutable), an interrupted
  run is rerun in a new sandbox as before. So is a suspended run that can't
  resume: its sandbox reached its shutdown time, or its old pod (kept, or
  stuck terminating) did not go within ten minutes of the suspend. The
  operator records the counted pod (`status.countedPod`), so a suspend that
  fails after the count is tried again without counting the run twice. A
  pod whose agent container was lost with its node, or that is gone, ran
  for an unknown time and counts none of it, so each such resume gets the
  run's full timeout again; `MaxResumes` bounds them. A restore that fails
  half way puts the clone back as it was cloned before the run starts over.
  A `clean` checkpoint is saved when a run pauses at a tool boundary (below);
  on the same model it resumes the same session with `claude -p --resume`.
  Between tool calls, `pause-guard` also has theos-agent save a `hard`
  checkpoint when the last is 10 minutes old, and the session goes on; a
  pod lost without warning (a power loss, `wsl --shutdown`) resumes from it.
- **An unavailable agent's run waits, suspended.** theos-agent saves a
  checkpoint before it reports `Unavailable`: `clean` when the agent ended
  on the service's error, its transcript whole, `hard` when theos-agent had to
  stop it. Its result says it saved one (`checkpoint`). The operator counts
  what the run cost so far, suspends the Sandbox as for an interruption,
  marked with when the agent is back (`theos.factory.io/agent-back-at`: the
  service's `RetryAt`, or 15 minutes on) and without its shutdown time, and
  resumes it then with the current Agent. The resumed session is told the
  service was unavailable. The wait costs no round, rerun or resume. Once
  its pod is gone a waiting run holds no agent slot, nor does a paused
  one, and its resume waits for a free slot as a start does. A run
  that saved no checkpoint, or in a Sandbox without the volume, ends, and
  its step runs again once the agent is back; a waiting run that can't
  resume runs its step again with no rerun spent. A Task still waiting past
  when its agent was due back is a needs-you item, as before.
- **Roles get their own model and guide.** An Agent has a `harness` and a
  `model`. A Standard ships `docs/agents/implementer.md` and `reviewer.md`,
  and may ship `verifier.md`, read from the base branch so a change can't
  rewrite its own review. The reviewer reports each criterion and may name
  up to three follow-ups, filed `status/proposed` in the *Later* milestone.
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
  `.theos.yaml` stay out of reach.
- **A Task keeps its record:** `status.history`, `status.runs` per agent run,
  the cost (`status.costMilliUSD`) and tokens (`status.tokens`, with
  `largestSession`) its agents reported. `WaitingForSlot` says it waits for
  an agent slot, `WaitingForEnvironment` for a test environment (*Test
  environments*), and `BaseRed` that it waits for `main`'s checks to pass. The Work area charts the runs against their Agent's
  `sessionTokenLimit` ([plan](plans/done/token-chart.md)): the line and the
  "Past the limit" counts use the Agent's current limit and the Task's current
  Agent, since a run records neither, so changing either rejudges past runs.
- **A long session hands off.** Past its Agent's `sessionTokenLimit` (default
  130000) a session writes where it stands and stops; the runner, `theos-agent`,
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
environment, readable inside it. A test environment a run leases runs the
agents' code too and is fenced the same way (*Test environments*); the
sandbox fence lets out one more port, `theos-env`'s, to the project's own
environment pods.

### The CI fence

CI runs the agents' code too. A job running a Container repository's code runs
in an environment made for it and thrown away; holds only its job token and its
repository's secrets; shares nothing writable with another project's jobs or a
release; and reaches only DNS, the Git host (and its registries) and public
HTTPS. Every CI adapter meets this or says which part it can't
([plans/done/ci-runner-isolation.md](plans/done/ci-runner-isolation.md)).

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
ended pod and starts a fresh one; the pool goes with the Project: once the
Project is gone, the operator deletes every pool pod and lets the kept
records go at once, since no Task is left to read them (#1697). The pool
follows its `ci` block: an idle pod above a lowered `size`, or built from
another `image` than the block now says, is replaced, a busy one once its
job ends, and a dropped block takes the pool's Secret, ConfigMap and
NetworkPolicy with it (#1700). A pod
that failed on its own (its registration or its runner ended non-zero) is
replaced only after a backoff, 10s doubling per failure in a row up to 5
minutes, until a pod runs its job; a GitProvider whose host gives no
registration token keeps its pods, gets none added or replaced, and the
Project's `CIRunners` condition says `ProviderUnavailable` until the host
answers (#1699). The pods
sit under the project's own CI NetworkPolicy (no ingress; DNS, the Git host's
namespace and public HTTPS out), hold no ServiceAccount token, get the
registration token only in an init container, and the `go` label's pods
mount a Go build cache of the project's own on the node. Releases get no
cache a job wrote: a release builds signed artifacts, so gitea-release's
release job runs on `go-release`, whose pods (`freshGoCache`) start from the
image's warm Go cache copied into an emptyDir of their own, mount nothing
from the node and keep nothing (#1141). The shared runners
Helm installs register to the templates organization and serve the
templates' checks only; the GitProviders the chart renders carry a pool of a
`base`, a `go` and a `go-release` runner, the labels the templates' checks
and releases run on, so an install needs nothing added for a project's
checks and releases to run. Who cancelled a job is read from its pod: a pod that
ended its one job on its own after the cancel was a person's; one deleted,
evicted or killed before its job ended was the runner's, and the Task reruns
the checks at no cost to a round (`model.Canceller`). The isolation left is
the node's: the pods share it with everything else
([getting-started](getting-started.md#what-the-factory-trusts)).

### Test environments

Work that needs a tool the agent sandbox lacks (a browser and its system
libraries, a phone emulator, a GPU, later macOS) gets it from a
**TestEnvironment**, not from a person
([plans/test-environments.md](plans/test-environments.md)). A
TestEnvironment is a named set of tools in a project's namespace, such as
`browser`, `android` or `godot` (`v1beta1`, as every kind): a one-line
`description` for people and agents, exactly one provider member (`local`
today, `aws` later; CEL), and `limits`. Its status lists the live leases
(holder, since, state, cost so far), what they cost today, and `Ready`: the
image is pinned and the project's fence for its pods exists.

- **Who asks.** A Standard or a Framework lists names in
  `spec.testEnvironments`, and its Containers' runs get each one the project
  has; one the project lacks gives the Container `TestEnvironments=False`,
  reason `NotInProject`, a Needs-you item, and the runs go on without it. An
  issue asks with a label `env/<name>`, which the triager or planner sets;
  that ask is required: a Task whose issue needs an environment the project
  lacks waits, reason `WaitingForEnvironment`, message `NotInProject`,
  costing no try, until a person adds it.
- **When a lease is held.** One lease per agent run that needs it: the
  implementer's and the verifier's runs; the reviewer, triager, spec writer
  and planner get none. The operator leases before it makes the run's
  sandbox and returns the lease when the run ends, fails or is suspended
  (paused, agent unavailable, interrupted); a resumed run leases again.
  Waiting for a lease costs no round. An environment keeps no state between
  leases, so letting one go loses nothing.
- **How the sandbox uses it.** The sandbox gets `THEOS_ENV_<NAME>_URL` and
  `_TOKEN` (and `_CA`, the certificate an environment over public HTTPS is
  pinned to) per lease, and runs `theos-env run <name> -- <command>`. That
  sends the working tree's tracked and untracked, not ignored, files (512 MiB
  at most), runs the command in the same folder of a fresh `/work`, streams
  its output and exit code back, and writes the files it changed or added,
  the ignored ones left out, into the sandbox's tree, where the agent commits
  them. The environment runs `theos-env serve`, a static binary an init
  container copies in from the theos-agent image, so any image with the tools
  works; within a lease its own background processes (an emulator booted
  once) keep running.
- **The fence.** An environment runs the agents' code, so it is fenced like
  a sandbox. A local lease is one pod, from the environment's image, with no
  ServiceAccount token, no user Secret, not privileged, its devices only
  through the cluster's device plugins (`local.devices`, a setting), and the
  lease's deadline as its own. The project's NetworkPolicy
  `theos-test-environment` selects `app.kubernetes.io/component:
  test-environment` and lets in only the project's agent sandboxes, on
  `theos-env`'s port, and out only cluster DNS and public HTTPS: no Git host,
  no model proxy, no private range. Each lease has a token of its own, which
  the operator makes and gives only to the run that holds the lease, so one
  Task can't use another's environment. A remote environment is reached over
  public HTTPS with its certificate pinned, gets no cloud role and no metadata
  service, and lets in only `theos-env`'s port.
- **The chart's browser.** The seed makes a local `browser` in each
  project it sets up (`testEnvironments.browser.enabled`, on by default),
  on the release's `env-browser` image, with no CI pool: a fresh install
  handles a picture script with nothing added, and a lease costs nothing
  until a run asks for one.
- **The port and its providers.** `ports.TestEnvironments` leases, reads,
  returns and lists leases (*Factory components*); `cmd/main.go` maps each
  member to its provider. The local provider makes a pod per lease (never a
  Deployment: no lease sees the last one's leftovers) and costs nothing. A
  remote provider's credential is a Secret only the operator reads.
- **Limits.** `limits.maxLeases` (default 2, at most 16) bounds the live
  leases; a run past it waits, `WaitingForEnvironment`, `AllLeased`.
  `limits.maxLeaseMinutes` (default 180, the agent run's own deadline) ends a
  lease that outlives it. A remote member needs `limits.budgetUSDPerDay`;
  past the day's budget no lease starts (`OverBudget`, a Needs-you item) and
  leases held finish their run. The operator returns any lease whose holder
  no longer lists it.
- **CI on the same tools.** An environment with `ci.size` adds a runner pool
  for the label `env-<name>` to the project's Gitea Actions pool, built from
  the environment's image and devices under the CI fence, so a Standard's
  workflow runs on `env-browser` what the agent ran with `theos-env run`.
  Each pod registers, and gets its runner copied in, from `ci.runnerImage`,
  else the image of the GitProvider's first runner; the jobs run on the
  environment's image. A `ci` block that names `env-<name>` itself makes
  the Project's `CIRunners` False, reason `LabelClash`.
- **By hand only for a person.** The implementer's and verifier's prompts
  list the run's environments, with each one's description and how to use
  it, and the guides say that a criterion a listed environment can check is
  never one "no sandbox can" meet. The planner's and triager's workspaces list
  the project's TestEnvironments, and they label `env/<name>` where they
  would have filed `kind/by-hand` for missing tools. `kind/by-hand` stays for
  accounts, settings, hardware no environment has, and other people's work.
- **In the UI.** The Project page lists the project's TestEnvironments, each
  with `Ready`, its live leases against its limits and who holds each, and
  today's spend; each has a page of its own, read only. A Task's page shows
  the leases its run holds, the environments its runs leased, and why its
  run waits for one.

## Crews and releases

A Crew per Container names the implementer and reviewer Agents, polls the
repository's `status/ready` issues and starts up to `parallel` Tasks (one by
default; `Blocked` ones don't count), with three tries per issue.
`spec.paused` stops new starts; running Tasks run to their end, unless
`spec.pauseRuns` pauses them too.
`spec.taskBudgetUSD` caps what one Task's agent runs may cost, in US dollars,
summed from what its agents report; at the cap the Task starts no further
run (`OverBudget`) until the cap is raised, and unset means no cap.

- **Pausing runs.** `spec.pauseRuns`, which CEL allows only with `paused`,
  pauses the Crew's running Tasks at their next tool boundary. The operator
  sets the annotation `theos.factory.io/pause: <reason>` on the pod template of
  each running Task's Sandbox, the one thing it changes under a running
  agent; Agent Sandbox copies it onto the pod, and the pod sees its own
  annotations through a downward API volume at `/etc/theos-agent/podinfo`.
  theos-agent's `pause-guard` hook stops the agent after its current tool (a tool
  still running five minutes on is ended as on `SIGTERM`), saves a `clean`
  checkpoint and reports `Paused`. The operator counts what the run cost so
  far, suspends the Sandbox as it does an interrupted run's and drops its
  shutdown time, so a pause may last as long as it likes; the Task gets
  `Paused=True`. A pause costs no round, rerun or resume, and starts no run
  and resumes none of the Task's until it ends; checks and merges go on. A
  paused run that can't resume (its Sandbox expired first) runs its step
  again, still with no rerun spent.
  Unsetting `pauseRuns` resumes each paused run in place. A run in a Sandbox
  without the checkpoint volume isn't asked: it runs to its end. The Crew's
  `Paused` condition names the Tasks still stopping.
- **Order.** An issue waits for its Git host dependencies. `priority/urgent`
  goes first, never past `parallel`, which is a hard cap; then a Standard
  change a `Blocked` Task waits on; then releases, due first first, the next taking places the
  current can't use; then undated milestones, then none.
- **A try continues an open pull request.** A failed Task's pull request stays
  open while a later try may continue it on that branch, with its feedback
  (`status.continues`); the operator closes it, saying why, once none will.
- **Routes by label.** `spec.routes` sends issues with a label to another
  implementer, reviewer or verifier (`kind/docs` to a cheaper model); per
  role, the first matching route wins, else the Crew's Agent. The Task
  records the Agents it got. A missing Agent leaves the Crew `Ready=False`,
  `AgentNotFound`.
- **Triage** (`spec.triager`). With a triager, the Crew starts one Triage
  Task (`kind: Triage`) at a time for the lowest open `status/proposed`
  issue outside the `Later` milestone that no Triage Task has decided. The
  triager reads the issue, as data like every agent, and a clone of main it
  pushes nothing from, and reports one decision: `ready` with an
  *Acceptance criteria* checklist, `spec`, `park` with the question a person
  must answer, or `duplicate` of an issue it names. The operator comments the
  reason and acts: `ready` writes the checklist into a body that has none and
  labels `status/ready`; `spec` labels `status/needs-spec`; `park` labels
  `status/needs-human` and asks the question; `duplicate` closes the issue.
  An issue a person closed or relabelled meanwhile keeps that. The Task ends
  `Merged`, as a Plan Task does, with the decision, its reason and what was
  done in `status.triage`. A person can answer a parked issue's question
  from chat (*Chat*), as the Task's `theos.factory.io/answer` annotation:
  the operator comments it on the issue ("<person> answered in chat: …"),
  labels the issue `status/proposed` again unless a person moved it
  meanwhile, and records who in `status.triage.answeredBy`; a parked Task so
  answered decided nothing, so the Crew triages the issue again, with the
  answer on it. One answer is taken; an answer to no question, or naming
  nobody, is left as it is. A failed run fails the Task with a comment and
  leaves the issue `status/proposed`; the Crew tries again, three Triage
  Tasks per issue in all. A triage costs the issue no try and takes no place
  from `parallel`; deleting a Triage Task has its issue triaged again.
  Without `spec.triager`, people triage.
- **Spec** (`spec.specWriter`). With a spec writer, the Crew starts a Spec
  Task (`kind: Spec`, `<container>-<issue>-spec`) for an open
  `status/needs-spec` issue as it starts an Implement Task for a ready one:
  in the same order and within `parallel`, with three tries of its own; the
  issue needs no acceptance criteria yet. It runs the implement flow with
  the spec writer's prompt (and a Standard's `docs/agents/spec-writer.md`):
  the agent writes `docs/specs/<issue>.md`, the problem, the approach and an
  *Acceptance criteria* checklist, and nothing else. theos-agent refuses a
  round that changes another file, and so does the operator, once the
  round's checks pass, for a pull request with another file or a spec
  without criteria, as a scope refusal: two in a row stop the Task. The pull
  request is titled `Spec: <issue title>`, doesn't close the issue, and the
  issue is labelled `status/spec-review`. Once CI and the reviewer pass it,
  the Task waits at its review point, phase `AwaitingApproval`, with a
  `NeedsYou` condition and the reviewer's approval on the pull request; it
  takes no place from `parallel` meanwhile. `spec.specApproval: Person` (the
  default) merges it once a person, not the operator's user or an agent bot,
  approves the reviewed commit on the Git host, or from chat (*Chat*): the
  Task's `theos.factory.io/approve-spec` annotation naming that commit; a
  person's request for changes, a review on the Git host or the
  `theos.factory.io/spec-changes` annotation (`<commit> <text>`), starts the
  next round with their words, and outweighs any approval. A word from chat
  counts as the person its `-by` annotation names, which the admission policy
  holds to whoever wrote it; one about another commit, or naming nobody,
  counts for nothing and the Task's condition says so. `Reviewer` merges on
  the reviewer's approval alone, with no wait. A push to the branch is checked
  and reviewed again. After the merge the operator writes the spec's
  criteria into the issue's description, in place of any it had, with a link
  to the spec, and labels it `status/ready`, so the Crew builds it; the issue
  stays open, and `status.spec` records who approved (`approvedInChat` when
  from chat) and what was done. A
  person who closes the pull request unmerged declines the spec: the Task
  fails (`SpecDeclined`) and the issue goes back `status/needs-human`.
  Without `spec.specWriter`, people write specs.
- **Verify** (`spec.verifier`). With a verifier, the Crew's Implement Tasks
  name it in `spec.verifierRef` (a route's `verifier` takes its label's
  issues, even in a Crew without one). Once the reviewer approves, the
  review's approval goes on the pull request, what it left for the merge
  (follow-ups, open criteria) is kept in `status.review`, and the Task
  enters `Verifying` in the same round: the verifier runs in a sandbox of
  its own (role `verify`) on the reviewed head, which pushes nothing. It
  builds and starts the change the way the repository says (`make verify`
  when the Makefile has that target, else its README), exercises each
  acceptance criterion against what runs, and reports in the review's
  verdict format: `approve`, or `changes` with each failing criterion and its
  evidence, the command it ran and what that printed. A Standard's
  `docs/agents/verifier.md`, read from the base branch, guides it; without
  one the built-in prompt applies. `changes` goes on the pull request as a
  request for changes and starts the next round with the evidence as its
  feedback, as a reviewer's does (same rounds, `maxRounds`); `approve` merges
  the change as a review's approval does, taking in a main that moved
  without a second verification. A criterion no sandbox can meet stays open
  with why, never faked, and goes to the by-hand issue with the review's.
  A verifier run without a verdict runs again at no round, and the third in
  a round fails the Task, as a reviewer's does; its outages, pauses and
  interruptions are handled as any run's. `status.verification` keeps the
  verdict, the summary, the head it ran and the criteria it left open; the
  history shows the `Verifying` steps. A spec
  and a Standard change are never verified (CEL refuses a `verifierRef` on
  any Task but an Implement Task without `allowedProtectedPaths`): a spec has
  its own review point, and a Standard change's gated merge needs the
  approval of a review run that still lasts. Without a verifier, the
  reviewer's approval merges.

A milestone titled `vMAJOR.MINOR.PATCH` with a due date is a release.

- **Tagging** (`spec.tagReleases`). A release with no open issue and no Task
  at work is done: the Crew tags it at its newest merged issue's merge commit
  and closes the milestone. Tags go out in due-date order, never move, and
  never go behind the previous release (`BehindPreviousRelease`).
  A release with no issue at all is not done and holds no later one; one
  before the release tagged closes untagged with it (#2025).
  `status.lastRelease` records it; `Released` says why one waits on a person.
- **Cadence** (`spec.releaseCadence`,
  [plans/release-cadence.md](plans/done/release-cadence.md)). `every: Release`,
  the default, tags each release once it is done, as above. `Day` or
  `Week` releases on a schedule: `at` (`HH:MM`, default `18:00`) in
  `timeZone` (an IANA name, default `UTC`), on `weekday` (only with
  `Week`, default `Friday`). Until the cut the first open release waits
  (`Released=False`, `WaitingForCut`: "v0.15.0 holds 4 merged issues and
  goes out at 18:00 UTC"; `status.nextCut`). At the cut, the first `at`
  after the last cut (daylight saving as the time zone has it), the Crew
  cuts that release: it marks the milestone cut, so no more work goes in;
  a release whose merged work is all fixes (`kind/bug`, `kind/test`) takes
  the next patch title after the last release; its open issues move, each
  with a comment, to the next open release that can still take work, one
  the Crew makes when there is none (the next minor, due at the next cut,
  marked `<!-- theos-crew: <crew>/carry <title> -->`). Then it goes out
  as above: prepare, full check, tag. With no merged work in any open
  release a cut cuts nothing; a release with none of its own ahead of one
  with some is carried on and closed untagged. A merged issue labelled
  `release/now` (a fix of what
  users hit in the latest release, or a feature a person wants out now)
  cuts its release at once, and those before it, with the work merged so
  far. Under a cadence the prepare issue waits for no other Task and the
  Crew keeps starting them; a Task ready to merge outside the cut release
  waits (`WaitingForRelease`, no round spent, no place taken from
  `parallel`) until it is tagged, so the changelog holds what the tag
  carries. A time zone the operator can't load stops the Crew
  (`Ready=False`, `InvalidTimeZone`) until it is fixed.
- **Preparing** (`spec.prepareReleases`). A done release with work a Task
  merged gets `Prepare vX.Y.Z`, `kind/release`, listing what the tag carries;
  its agents set the version files (`make release-version`) and the
  changelog. With `every: Release` it starts once no other Task works and
  runs alone (`PrepareWaitsForTasks`). Work merged after it leaves the release to a
  person (`MergedAfterPrepare`).
- **The full check.** `Container.spec.checks` is `EveryChange` (the default:
  CI runs `make check` on every change) or `BeforeRelease`: changes run the
  Standard's `make check-light` and the full check runs on
  `release-check/vX.Y.Z` before the tag (`FullCheckRunning`,
  `status.fullCheck`); a failure files `Full check fails for vX.Y.Z` and tags
  nothing. A check cut off (cancelled at the runner's time limit, or its
  runner lost) runs again once first (`status.fullCheck.reruns`), and an
  issue filed after says it was cut off (#2028). It applies only where the
  Standard sets `checksBeforeRelease` and the Makefile has `check-light`;
  `ChecksApplied` says why not.
- **The release run.** `status.lastRelease.run` sums the runs the tag started
  (`Waiting`, `Running`, `Failed`, `Succeeded`), with Events
  `ReleaseRunFailed` and, past an hour, `ReleaseRunOverdue`. Setting or
  changing `theos.factory.io/rerun-release` on a `Failed` run re-runs its failed
  jobs, once per value. While a failed full check holds the next release, the
  value re-runs that check first: its issue holds the release no more, is
  closed if the check passes, and holds it again if it fails (#2028).
- **Bundles.** Once B's release is built (and published, if B publishes),
  each Container A with a `bundles: true` Relationship to B gets a ready
  `Bump B to vX` issue, `kind/bump`, in A's next unprepared release.

### Publishing a release

The operator moves bytes with credentials it holds; it builds nothing.

- **A Go module.** A tagged repository with a root `go.mod` is published to
  the Git host's Go registry (`status.lastRelease.goModule`).
  `ModulePublished` turns True once `go get <path>@<version>` finds it: a
  `theos.local/` path through the registry, any other through the public
  proxy (`--go-proxy`), else `NotFetchable` or `ProxyUnavailable`. Agent pods
  and the Go Standard's CI fetch `theos.local/...` from the registry.
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
  `<scope>/<container>`; the scope is a setting there too, `@theos` by
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
  (`Published=False`) until `theos.factory.io/accept-repository: <id>` accepts
  it. The same copy is a `github` DeployTarget's (the Deploys section), and
  the Crew's field ships through the same code path, its `Published`
  condition and reasons kept. The field stays valid and working in
  `v1beta1` (Compatibility); the version after `v1beta1` drops it, once
  nothing writes it (#1082). To move
  over by hand: a DeployTarget with the same repository and the same Secret
  (key `token`, unchanged), a `spec.deploy` entry on the Container, then the
  field removed. Both may exist at once: each finds the release the other
  made and leaves it alone. A Brief's `releases[]` keeps writing the field
  on its Crews until a Brief can name DeployTargets.
- **The source on GitHub** (deprecated by #1070). `publishReleases.source.path`
  also commits the app's source at its tag into that folder, without the
  factory's own files (`model.FactoryOwnPaths`), tagged `<path>/vX.Y.Z` so
  `go install` finds it. Crews publishing to one repository may not overlap
  (`FolderOverlaps`). The same checks as a `github` target's run before the
  first write of a version (`model.PublishChecks`, below), failing
  `Published` with `LicenseMissing`, `ReadmeMissing` or `SecretFound`. It
  moves over with the copy, as the target's `source`.

## Measuring the factory

Built as [plans/done/measure-and-improve.md](plans/done/measure-and-improve.md). A change to how the factory works (an Agent's
model, a route, a Standard's agent guide, `docs/project-notes.md`) should
be judged by numbers the factory keeps itself.

- **The record is on the Task.** Each run records the Agent, harness and
  model that ran it (`status.runs[].agent`, `harness`, `model`, copied onto
  its Sandbox as `theos.factory.io/run-*` annotations when it is made or
  resumes); a merged Task records how many of its pull request's commits a
  person wrote (`status.personCommits`: any not by an agent's
  `@agents.theos.factory.io` email and not a merge) and its size
  (`status.pullRequestSize`), read from the Git host when it merges, and
  later for a Task merged before. The UI computes the
  metrics from Tasks: merged, autonomy (merged with no person's commit;
  approving a spec or a deploy is a decision, not code), cycle time per
  stage, and cost per merged Task by size, per period and per
  configuration: *How well it works*, in the Work area. No metrics store.
- **A Scorer judges finished Tasks.** Plain-words criteria, a judge Agent
  (`judgeRef`), what it selects (kinds, Containers, outcomes; by default
  merged Implement Tasks), a sample picked by a hash of the Scorer and the
  Task (`samplePercent`), and a daily cap (`maxPerDay`) and budget
  (`budgetUSD`); it judges only Tasks that finish after it was made. For each,
  the operator makes a Task of kind `Score`, owned by the Scorer and named
  after the pair, so none is judged twice: one `score` run of the judge on a
  clone that pushes nothing, told the criteria (`THEOS_CRITERIA`) apart from
  the evidence in its goal (the outcome, the issue's title, the summary, the
  review, the merge commit to `git show`), which it reads as data. Its
  verdicts, `Pass`, `Fail` or `Unclear` per criterion with the judge's why,
  are its `status.score`; a failed run leaves each `Unclear`. The Scorer's
  status counts the verdicts of its current generation. Score Tasks take an
  agent slot but hold no other Task on their Container up, and no Container
  adopts them. Scores advise; the reviewer, the checks and the fence still
  decide what merges. A Task's page shows its Scores, and the Work area each
  Scorer's pass rates by configuration (*What the Scorers found*).
- **A Benchmark compares configurations on the same work.** The same goals
  (text, or an issue read once), from one pinned commit (`base`, 40 hex),
  under two to four variants (an implementer, reviewer and verifier Agent
  each), repeated (`repetitions`), `parallel` at a time, within a required
  `budgetUSD`; it can't change once made. Its Tasks are Implement Tasks with
  `spec.benchmark`, owned by the Benchmark and adopted by no Container,
  made repetition by repetition so an early stop compared the same goals.
  Each branches from `base` (the operator makes `task/<name>` there), opens
  a pull request titled as one that never merges, never takes in main, and
  ends in the phase `Approved` where it would have merged; it touches no
  issue, takes no Crew slot and holds no other Task up. The Scorers it
  names (`scorerRefs`) judge every finished run, whatever their sample and
  selection, on `git diff <base> <head>`; once they have, the Benchmark
  closes the run's pull request and deletes its branch. Its status gives
  each variant's approval rate, mean rounds, median minutes and cost, and
  each Scorer criterion's pass rate, each rate with its 95% Wilson
  interval, and says "no clear difference" rather than pick a winner while
  the intervals overlap, which its page in the UI shows. Neither its runs
  nor the judge runs alarm anyone: the Chat and the Needs-you list leave
  them out.
- **Self-improvement files an issue, never a change.** With
  `spec.improve`, a criterion that failed `failures` times among a
  configuration's newest `window` Scores files one `status/proposed`,
  `kind/improvement` issue in the newest failing Task's Container's
  repository: what fails, the judge's whys and the proposal (a line in
  `docs/project-notes.md`, or the Standard's agent guides). It files no other
  while that one is open, and the normal loop decides.

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
`FilesBehind`), until a person sets `theos.factory.io/standard-take` (the
operator merges the Standard's version) or `theos.factory.io/standard-keep`
naming the ref. No agent takes part. `scaffoldedFrom` keeps the scaffold's
refs, `status.standard` the current. A Container that got its Standard from a
Bundle keeps following the Standard itself; a changed Bundle is only reported
(`BundleCurrent`, above), never applied.

An update whose CI fails (`ChecksFailed`) is the Crew's to fix
([plans/standard-update-fails.md](plans/standard-update-fails.md), #1447).
The fix lands on main in the Crew's own pull request; once main is no longer
at the head the update was planned from (`status.standard.update.base`), the
operator closes the update's pull request and plans it again from the new
main (#1489). Before it closes a pull request it drops, here or when main
conflicts with it, the operator records why in
`status.standard.update.dropping`: a pass cut short after the close, by a
restart or a Git error, finishes the drop and plans again, and never reads
the operator's own close as a person's `UpdateClosed` (#2035). The operator files `Standard <name> <ref> fails this
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
  Connector adds a table to the stack, the `rds` Connector an Aurora
  PostgreSQL Serverless v2 cluster with its secret; an AWS Container keeps
  relational data in `rds`, never `postgres`, which refuses an AWS Framework
  ([plans/aws-rds.md](plans/aws-rds.md)).
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
  next round gets the reasons. A plan's Container may name a Bundle instead
  of the four sides; the check expands it as the operator would, so a Bundle
  that doesn't support the type fails the round with the message the
  Container would show, and the Container is created on the Bundle. The operator makes a Crew per new Container
  and files the issues once it is Ready, ordered by their `after:` front
  matter as Git host dependencies. What it made carries the label
  `theos.factory.io/brief`. A failed step says where and why
  (`Planned=False`, `ApplyFailed`). A round that waits for a Container that
  stopped (phase `Failed`) keeps waiting and says why: its `Planned` message
  carries the Container's failing condition's reason and message after the
  phase ("Container x (Failed: UnpinnedRef, Framework y: ...)"), so a fix
  to the Container (a pinned ref, a Markdown domain, a layer moved) lets the
  round go on without a retry.
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
  naming a Container with a GitHub repository and token Secret), on the
  cadence `spec.releaseCadence` says (default `every: Day`, `at: 18:00`,
  `timeZone: UTC`), which the Brief writes onto the Crews it made; a Crew
  it didn't make keeps its own. Under a cadence every issue and taken
  suggestion of a round, a tending round's too, goes into the release in
  progress, the first open release that can still take work, made only
  when none is open, so a day's work is one release. The planner marks
  what is bigger, `release: now` on an issue or `now: true` on a taken
  suggestion (refused on a skip), and the operator labels it
  `release/now`; a tending round marks nothing. With `every: Release`,
  fixes go into the next patch release, other work into the next minor:
  an empty one an earlier round made is joined, not passed over (#2025). A
  Crew whose target was edited away from the Brief's publishes nothing
  until they agree (`ReleaseTarget=False`, `DiffersFromBrief`).
  `status.apps[].nextRelease` copies the Crew's next cut.
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
  round; `Paused` names the Tasks still running. `spec.pauseRuns`, only with
  `paused`, passes on to the Crews it paused, as `paused` does, and pauses
  its planner Task's run too; `Paused` then names the Tasks still stopping. No issue may ask to change
  `model.AgentNeverPaths` (`AGENTS.md`, `docs/agents/`, `.theos.yaml`).
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

- **The shell** ([plans/done/ui-shell.md](plans/done/ui-shell.md)): a sidebar with
  the project switcher and the areas (*Needs you* with its count; *Build*;
  *Operate*; About and Settings at the bottom), folding to icons; a top bar
  with where you are, a *Demo data* chip in demo mode, freshness, *Search or
  jump* (⌘K) and the area's one primary action, such as *New brief*, which
  opens its form in a modal; and a theme (System, Light or Dark) and density
  kept in the browser only.
- **Seven areas,** each a summary that fits a wide screen, in two or three
  columns, and one column on a phone: *Needs you* (what waits on a person,
  across projects, in groups per project that fold and with chips by kind),
  *Loop* (one project's development cycle in ten stages, from an idea or a
  bug to a monitored release, with the two reviews that wait on a person
  drawn apart; `ui/src/model/loop.ts`, from the resources alone; the ring
  beside the stage a node selects, opening on the first review with items),
  *Briefs* (a card per Brief that opens its page), *Story* (system context
  and one system's containers, with a side panel that folds), *Work* (the
  headline numbers, a one-line *Needs a look*, then tabs: the board of Tasks
  by stage, Crews, releases, time and tokens), *Factory* (a dashboard of the
  health, the last hour and the agent slots) and *About*. It opens on *Needs
  you* when it has items, else *Story*. An area's tab or the Loop's stage is
  `tab=` in the address; a fold is kept in the browser only.
- **Object pages** open in a drawer over the area they came from (full width
  with `full=1`); the address keeps `ns`, `kind` and `name`, so a link opens
  one. They share one header (`ui/src/components/ObjectHeader.tsx`):
  kind, name and breadcrumb, the lifecycle as a stepper from
  `ui/src/model/lifecycle.ts` and what a person does now; then tabs, as the
  kind has them, kept as `otab=`: *Overview* (its own sections), *Status*
  (conditions), *Activity* (timeline, rounds, runs) and *Related* (children
  and links). A step the UI can't take shows a link or a `kubectl` line to
  copy.
- ***Needs you*** lists what the resources flag (`ui/src/model/needs.ts`),
  each in its condition's own words, every item from `status`, none from
  timing.
- **Versions** of the UI, operator, chart and CRD API at the foot of the
  sidebar; a UI and an operator of different versions get a warning.
- **The factory's health** comes from the UI server (`GET /factory/health`,
  sampled every 15 seconds, an hour kept). No Prometheus or Grafana.
- **The project's Chat** (*Chat*, below) on About and in Settings, read
  only: its homeserver, each room by name with a `https://matrix.to/#/` link
  to its alias (else its ID), and `Ready` with its message; nothing when the
  namespace has no Chat.

### UI actions

With actions off (the default) the UI server writes three things: create a
Brief, append a prompt, pause or resume one. With `ui.actions.enabled`, each
row below is one allowlisted request in `ui/server.mjs`, refused there for any
other path, method, content type or field, and by RBAC for anything else:

| Kind | Request | Fields |
| --- | --- | --- |
| Brief | `POST` | a new Brief |
| Brief | `PATCH`, JSON patch | append to `spec.prompts`; `spec.paused`, `spec.pauseRuns`, `spec.autopilot`, `spec.parallel`, `spec.releases` (after a `test` of the `resourceVersion`) |
| Crew | `PATCH`, JSON patch | `spec.paused`, `spec.pauseRuns`, `spec.parallel`, `spec.tagReleases`, `spec.prepareReleases`, `spec.publishReleases` (after a `test` of the `resourceVersion`) |
| Crew | `PATCH`, merge patch | annotation `theos.factory.io/rerun-release` |
| Container | `PATCH`, merge patch | annotations `theos.factory.io/retry`, `theos.factory.io/standard-take`, `theos.factory.io/standard-keep` |
| Container | `PATCH`, JSON patch | `spec.checks`; or one `spec.deploy` entry's `approved`, after a `test` of its `target` and a `test` of the approval the page showed: its old `approved`, or, when it had none, the whole entry as read (JSON Patch can't test a missing member), so a stale page fails, and says it is out of date, rather than overwrite a newer approval, a first one too |
| Task | `POST` | `kind: Implement`, `containerRef`, `agentRef`, `reviewerRef`, `goal` |
| Standard | `PATCH`, JSON patch | `spec.template.ref` |

Over `kubectl port-forward` the writes are the UI ServiceAccount's. Behind an
Ingress a person signs in with OIDC (code flow with PKCE) and every write
impersonates them, with prefixed names and groups, so the cluster's RBAC
decides; the chart refuses actions behind an Ingress without OIDC. The browser
never sees a token. Out of reach: deletes, Secrets, the Git host and editing
the model ([plans/done/ui-actions.md](plans/done/ui-actions.md)).

## Chat: the factory in Matrix

People follow and steer a project from a chat app as well as the UI
([plans/done/chat-matrix.md](plans/done/chat-matrix.md)). A `Chat`, one per
project namespace, names a Matrix homeserver (unset: the operator's
`--chat-homeserver`, the chart's own), a Secret with its bot's access token
under `accessToken`, the Space's and rooms' names, the updates digest and its
`people`: each Matrix user ID with the Kubernetes user and groups it acts as
(`system:` ones refused).

- **The operator makes the rooms and only reports.** It makes one Space,
  named after the Project, with six rooms (`updates`, `general`, `triage`,
  `specs`, `reviews`, `alerts`), private, invite only and unencrypted, invites
  the `people`, and writes the IDs and `Ready` into status. It finds them again
  by the IDs in status, then by alias (`#<aliasPrefix>-<purpose>:<server>`,
  `theos-<namespace>` unless set, immutable), so a restart makes nothing
  twice; an alias another account holds is never taken over (`AliasTaken`).
  A second Chat in the namespace is `Duplicate` and does nothing.
- **Chat enforces nothing.** A command becomes one request from a closed
  allowlist in `internal/model` (`CheckChatRequest`), the UI's own entries
  for what the UI can do (a Brief, its prompts, pause and resume, a
  Container's retry and a deploy's approval) and three annotations a person
  writes only from chat: `theos.factory.io/approve-spec` (the head commit),
  `theos.factory.io/spec-changes` and `theos.factory.io/answer` on a Task,
  each with its companion `<annotation>-by` naming the writer's Kubernetes
  user. `theos-chat` sends it as the person, by impersonation, and posts the
  API server's refusal, or what was done, back in the thread. The operator
  acts on the three as *Spec* and *Triage* say.
- **`theos-chat` posts and listens.** A separate binary
  (`cmd/theos-chat`, `internal/chatbot`) with its own ServiceAccount
  (`config/chat`), for each Ready Chat: a thread per item that needs a
  decision (a spec waiting for a person, a parked issue's question, a deploy
  waiting for approval, a failed scaffold), its root edited as the item moves
  and marked ✅ once done; plain posts for the rest; merges, releases,
  deploys and Brief rounds batched into one `updates` message per
  `digestMinutes`. Its first pass tells no history: only live threads. What
  it posted (item key to event ID) and where it read the rooms up to (the
  `/sync` token) are in its ConfigMap `theos-chat` in the namespace, so a
  restart says nothing twice and runs no command twice; a homeserver or room
  error is retried with backoff and keeps the other rooms going. Only a
  Chat's `people` are obeyed; anyone else gets a one-line refusal.
  Each post about a Task or a Container links to its page in the UI under
  `theos-chat`'s `--ui-url` (the chart's `chat.uiURL`, by default the UI's
  own address), next to the Git host's links; without it, it links to the
  Git host alone.
- **Nobody acts in someone else's name.** `theos-chat` may impersonate users
  and groups, so two admission policies (`config/policy`) bound it: a Chat's
  `people` entry names only its writer's own user and groups
  (`request.userInfo`), unless that exact entry was there before, so each
  person adds themselves; and the three chat annotations and their `-by`
  are written only by a person, never a `system:` identity (the operator's
  ServiceAccount, `theos-chat`'s acting as itself, an agent's), and each
  `-by` names the writer. The operator counts a word only with its `-by`.
- **Matrix is behind the ChatHost port;** the adapter speaks the
  client-server API over `net/http`, with no SDK, and tests run on a fake
  homeserver (`internal/adapters/matrix/matrixtest`). No end-to-end
  encryption until it is built.
- **The chart** installs `theos-chat` with `chat.enabled` (off by default)
  and gives the operator `--chat-homeserver`: `chat.homeserverURL` (your own
  homeserver, or matrix.org), else, with `chat.homeserver.enabled`, an
  in-cluster [Tuwunel](https://github.com/matrix-construct/tuwunel), pinned
  by digest, with federation and registration off. Its accounts are made
  through its shared-secret registration by a Job after every install and
  upgrade: the bot, whose token it puts in Secret `matrix-bot` in each of
  `gitea.projects`, and the people listed, each with a first password in a
  Secret for the admin to hand over. The secret and the passwords are made in
  the cluster, never rendered.

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
| ChatHost | a Space and its rooms, invites, messages, thread replies, edits, reactions, sync | Matrix |
| TestEnvironments | lease a place to run commands with tools the sandbox lacks, read it back, return it, list the leases held | a pod per lease on the cluster (`local`) |

- **A port is a narrow Go interface** with one fake and one contract suite,
  plain Go with no Kubernetes in `internal/ports`; each adapter is its own
  package under `internal/adapters`, and only `cmd/main.go` imports adapters
  (`make lint` enforces it). The Deployer and the Git host's five ports are
  there today, with Gitea in `internal/adapters/gitea`, and ChatHost, with
  Matrix in `internal/adapters/matrix`.
- **What a host lacks** its adapter emulates, or the port makes an optional
  capability (a second small interface, a condition reason when missing):
  Go modules, npm and PyPI, a project's own runners, webhooks.
- **What a CI system writes is a business rule:** one row per CI system in
  `model.CI` (workflow paths, required checks, protected paths); a
  Container's is its host's own (`model.HostCI`).
- **Polling is the contract.** Webhooks (#1084) only shorten waits: the
  operator registers a hook and reconciles what a signed delivery concerns
  at once. While the GitProvider's `Events` is True (`Receiving`), the
  polls that wait for the host (the Crew's issues, a Task's checks and
  Standard change, a Container's checks) stretch to 10 minutes; error
  retries and waits on the cluster keep their intervals. The chart's
  install is still to come ([plans/done/git-events.md](plans/done/git-events.md)).
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
  member saying what it is (`github`, `argoCD` and
  `cloudFormation` today, `gitlab` and `flux` later, each with its adapter), a
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
  phase, the tool's health, URL, the image a `github` target copied),
  summed by `Deployed`.
- **Monitor** ([plans/done/sdlc-loop.md](plans/done/sdlc-loop.md)): when an entry's
  health turns `Degraded`, or its deploy `Failed`, for the version shipped,
  the operator files one issue in the Container's repository: `Deploy
  <version> to <target> is <state>`, labelled `kind/bug` and
  `status/proposed`, with the entry's message, when the operator saw it, and
  the marker `<!-- theos-monitor: <target> <version> <state> -->`. The entry
  records it in `status.deploys[].issue` (`number`, `version`, `state`, `url`),
  kept through a recovery, so the same state of the same version files once;
  after a lost status the marker on the repository's open bugs stops a
  second. A new version or another state files again; recovering to `Healthy`
  files nothing. A failure to reach the Git host is not filed.
  (`model.DeployMonitorDue`.)
- **Release images** (#1069): built by the `image` runner, pushed by digest,
  shipped by digest ([plans/images.md](plans/images.md)). When the
  Container's choice builds one (`model.BuildsImage`: `k8s-manifests`,
  `gitea-release` for an `operator`, and `gitea-release` with
  `aws-fargate-ts`), the
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
- **GitHub targets** (#1070, [plans/done/github-deploy.md](plans/done/github-deploy.md)):
  a `github` target is how a Container is published as open source, for
  every type. It ships three things of each release, each by its own rule
  (`model.ReleaseShipment`): the app's source at the tag, when the member's
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
  whose folders overlap on one target, or whose folders go out under one
  source tag prefix (a `source.tagPrefix` the target sets), fail
  `FolderOverlaps`: the one whose name sorts first keeps the folder, and the
  message names both. Before the
  first write of a version the tree to publish is checked
  (`model.PublishChecks`): a license and a README at the folder's root, and
  no secret the factory recognises (a private key block, a token of a known
  shape, the value of a credential the operator holds); a problem fails the
  entry `LicenseMissing`, `ReadmeMissing` or `SecretFound`, naming the file
  and line and never the value, until another version is wanted.
  `LicenseMissing` says how a license gets there: `spec.license` on the
  Project or the Container before it is scaffolded (Scaffolding a
  Container), or a committed license file, the only way for a Container
  already scaffolded. The
  values compared are the GitProvider's token, the Container's agent Git
  token, its namespace's Agents' token Secrets (their keys naming a token, key, secret, password or credential) and the target's own Secret.
  A line marked `theos:allow-secret` is skipped, and the message says
  how many when nothing else is found; release files and images are not
  scanned. The image
  goes by digest to `ghcr.io/<owner>/<repository>/<name>` (the repository
  alone when `tagPrefix` is `""`), tagged with the version, before the
  release is published; `status.deploys[].image` reports
  `<repository>@<digest>` and the release notes name it. The same digest
  already under the tag copies nothing; another fails the entry `TagTaken`,
  naming the image. Without `packagesToken` no image is copied and the
  release ships without one. The Secret holds `token`, a
  fine-grained token for the one repository, or a GitHub App's `appId`,
  `installationId` and `privateKey`, from which the operator mints
  installation tokens; `packagesToken` is a classic token with
  `write:packages`, since GHCR takes neither. A GitHub Enterprise Server
  target sets the member's `url` (#1605): the server's address alone,
  `https://host[:port]`, with no path, and never github.com; the operator
  calls its API at `/api/v3` and uploads at `/api/uploads`, and copies the
  image to `containers.<host>` (the server's container registry, which
  needs subdomain isolation) in place of `ghcr.io`. The credentials and
  calls are the same. GitLab is its own member, below.
- **GitLab targets** (#1606, [plans/gitlab-deploy.md](plans/gitlab-deploy.md)):
  a `gitlab` target does for gitlab.com or a self-hosted GitLab what a
  `github` target does, by the same rules: what ships of each type, the
  source snapshot and its checks, the tag prefixes, `FolderOverlaps` and
  `TagTaken`. The member names the `project` by its full path
  (`group/sub/project`), a self-hosted instance's `url` (`https://host[:port]`,
  a path allowed for a relative URL root; never gitlab.com), `tagPrefix` and
  `source` as `github`'s; it has no `latest`, since GitLab's latest release is
  simply the newest. GitLab has no drafts, so the release is the last write,
  made of the source commit with the notes and links to the files: the source
  commit goes in through the Commits API, one commit per release (a source
  tree holding a symlink fails `SourceUnsupported`, since that API carries
  no symlinks), and the release's files into the project's generic package
  registry (package the Container's name, version the release's). The image goes by digest to the
  project's container registry, at its `container_registry_image_prefix`
  and `/<name>` (the prefix alone when `tagPrefix` is `""`), when the
  registry is on and the token has `api` (it grants the whole registry,
  #1980), or else both `read_registry` and `write_registry`; else the
  release ships without one. The Secret holds `token`, a project access
  token with the Maintainer role and the `api` scope, which covers the
  container registry (a group or personal access token works the same); a
  deploy token can't call the API and is not taken.
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
  `registryPassword`, a credential that reads tags as well as pushes, and
  `kustomize.images` names the copy. The copy speaks HTTPS unless
  `imageRegistryInsecure` is set (a lab registry over plain HTTP). It
  resolves the destination tag first: a tag already naming the digest is
  left alone and nothing is written, so a registry with immutable tags
  takes a retried Ship; a tag naming another image fails the deploy with
  that reason, and is not retried as transient. A credential that may
  push but not read (a token scoped to push alone) fails the deploy as a
  refused credential naming the read it lacks, and writes nothing: a
  blind write could move a tag (#1877).
  `imageRepositoryLayout` says where the copy goes below `imageRegistry`:
  `nested` (the default, `<imageRegistry>/<owner>/<name>`) or `flat`
  (`<imageRegistry>/<name>`, for a registry such as Docker Hub that takes
  one segment under the namespace) (#1770).
- **AWS targets** (#1071, [plans/aws-deploy.md](plans/aws-deploy.md)): a
  `cloudFormation` target names a region and, as a guard, an account. CI
  synthesizes the CDK app and the release carries the cloud assembly
  (templates, manifests and file assets, no image); the operator, with the
  target's credential, assumes the CDK bootstrap roles, publishes the assets
  and creates or updates the stack through one CloudFormation change set,
  bootstrapping the environment when the credential may (else *Needs you*
  says the command). State comes from the stack's status and its newest
  event, the URL from the `EntryPoint` output. The factory never deletes a
  stack. Fargate's image comes by digest from the Registry (#1069),
  copied into the bootstrap's ECR repository as its image-publishing
  role, which reads tags as well as pushes (the copy resolves its tag
  first; a role without `ecr:BatchGetImage` refuses the credential, #1877).

## Distribution and issues

This repository develops the factory and holds no product. A `vX.Y.Z` tag
publishes the Helm chart at `oci://ghcr.io/jairjosafath/theos/charts` and
its images on GHCR. The release installs:

| Part | Where | Notes |
| --- | --- | --- |
| Operator, CRDs, RBAC, admission policy | release namespace | always |
| UI | release namespace | `ui.enabled`, on by default; actions off |
| Gitea and its CI runners `base` and `go` | `gitea.namespace` | `gitea.enabled`, on by default; secrets generated in the cluster, never rendered by Helm |
| Seed job | Gitea's namespace | after every install and upgrade: templates, a GitProvider and Project per `gitea.projects` entry, the catalog and Agents applied in place |
| Project namespaces | each of `gitea.projects` | made when missing, labeled `theos.factory.io/shared-secrets: "true"`, kept on uninstall |
| theos-chat | release namespace | `chat.enabled`, off by default; the operator image, its own ServiceAccount, and the operator's `--chat-homeserver` |
| Matrix homeserver | release namespace | `chat.homeserver.enabled`, off by default: Tuwunel pinned by digest, federation and registration off, its volume kept on uninstall; a Job makes its accounts and the bot's token Secrets |
| Shared Secrets | copied from the release namespace | the pull Secrets; a project's own Secret of that name wins. The Agents' token is read by the model proxy there and never copied |
| GitProviders | named namespaces | `gitProviders`, for a Gitea the release doesn't run |

A device installs Agent Sandbox first and gives the Agents' token once. An
upgrade leaves running work alone
([plans/done/upgrade-in-place.md](plans/done/upgrade-in-place.md)). A device
reports issues through the form *Report from a device*.

## Compatibility

**An object a client stored keeps validating across upgrades.** `theos.factory.io`
serves every kind at `v1beta1` alone, the version users write and every kind
stores. A release keeps every object of the one before valid and meaning what it
meant. Within a served version a release never:

- removes or renames a field, or changes its type;
- adds a required field, or makes an optional one required;
- removes an enum value;
- tightens a CEL rule or a bound so an object that validated before fails;
- changes what a default, or an unset field, means.

Adding an optional field, an enum value, a condition type or reason, or a kind
is fine. A change these rules forbid is a new version, never a change to a
served one. The status is held to them as the spec is; what the operator does
with an object may change, as the release notes say.

**Across versions** ([plans/done/api-versions.md](plans/done/api-versions.md), #1082).
`v1alpha1` was dropped before theos's first release without the 90 days
below ([plans/done/drop-v1alpha1.md](plans/done/drop-v1alpha1.md), #1963):
`theos.factory.io` is a new group, installed fresh with no upgrade path from
c4factory ([plans/done/theos-rename.md](plans/done/theos-rename.md)), and no
release ever served `theos.factory.io/v1alpha1` (c4factory's v0.30.0
deprecated `c4.factory.io/v1alpha1`, another group), so no install stores an
object at it and the promise covered no theos user. The next version comes
beside `v1beta1` under these rules:

- **A served version keeps these rules** from the first release that serves
  it, as `v1beta1` does.
- **Every served version converts to and from the storage version** without
  failing and without loss. A field with no home in the target version rides
  in the annotation `theos.factory.io/<version>-fields` as JSON, so a round trip
  gives the object back exactly.
- **A release deprecates a version** by saying so under *Deprecated* in its
  notes and marking the CRD version `deprecated: true`, with a
  `deprecationWarning` that names the successor and the rule for the
  earliest release that may drop it (90 or more days after this one), never
  a date: the date lives in the drop issue's body alone (#1983).
- **A deprecated version stays served at least 90 days** after the release
  that deprecated it, and is dropped only once every CRD's
  `status.storedVersions` holds the storage version alone, under *Removed*
  in the notes, by its own issue, filed when the version is deprecated with
  the earliest date in its body.
- **The operator rewrites what etcd holds at an older version.** `v1beta1`
  is the storage version of every kind until the next version takes over.
  At start, the leader lists each kind whose CRD's `storedVersions` names
  another version (one that converts by the webhook once the webhook
  serves), patches every object back at the storage version,
  and brings that CRD's `storedVersions` down to it; a start after the pass
  does nothing, and a pass that fails is tried again until it succeeds,
  with the operator serving meanwhile.

`make check` guards what a schema shows: `test/compat` compares each served
version of the CRDs with the last release's, in `test/compat/baseline/`
(`hack/release.sh` copies them), a default's value included, and refuses a
version the baseline served that is missing, unless `test/compat/dropped.txt`
names it with the release notes' line. Until theos's first release the
baseline is c4factory's v0.30.0, renamed, so `dropped.txt` names `v1alpha1`.
CEL rules, a default added to a field that had none, and what the operator
reads into an unset field are left to review.

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

- **Out of scope:** monorepos (an existing repository of one application
  may be adopted with `spec.adopt`); Component and Code
  levels; provisioning data stores (a Connector only scaffolds: the files
  it writes may run the store, as postgres's StatefulSet, dynamodb's table or rds's cluster,
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
