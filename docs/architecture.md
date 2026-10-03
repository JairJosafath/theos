# Architecture

How the code is split, and which way dependencies may point. Read this before adding
code. [design.md](design.md) says *what* C4 Factory does; this file says *where* the
code for it goes.

## Layers

```
     cmd/main.go            cmd/c4session          cmd/c4agent               ui/ (server.mjs + React)
     (operator manager)     (Session client CLI)   (runner in a sandbox)     (read-only UI)
          │                       │                      │                        │
          ▼                       ▼                      ▼                        ▼
  internal/controller     internal/sessionclient  internal/agentrunner     Kubernetes API, GET only
                                                  (git + agent CLI; result
                                                   in the termination message)
     │        │        │               │
     │        │        └───────────┐   └──► Kubernetes API (Sessions)
     ▼        ▼                    ▼
internal/  internal/        internal/gitprovider
 model  ◄── scaffold         (Gitea over HTTP, Git with go-git)
(pure rules) (pure rendering)
     ▲        ▲
     └────────┴── api/v1alpha1 types never reach these two

features/ embeds the c4session devcontainer feature; the operator writes it into
every scaffolded repository.
```

| Layer | Packages | May import | Must not import |
| --- | --- | --- | --- |
| API types | `api/v1alpha1` | apimachinery | anything under `internal/` |
| Business rules | `internal/model`, `internal/scaffold` | the standard library, `text/template`, YAML; `scaffold` may use `model` | Kubernetes, controller-runtime, `net/http`, `api/`, other `internal/` layers |
| Adapters | `internal/gitprovider` | `net/http`, go-git, business rules | Kubernetes, controller-runtime, `api/`, `internal/controller` |
| Sandbox runner | `internal/agentrunner`, `cmd/c4agent` | the standard library, `internal/model` | Kubernetes, controller-runtime, `api/`, `internal/gitprovider`, `internal/controller` |
| Clients of the API | `internal/controller`, `internal/sessionclient` | everything above | `cmd/` |
| Assets | `features` | `internal/scaffold` | everything else |
| Entry points | `cmd/*` | everything | nothing imports them |

The UI in `ui/` is a separate program. Its server proxies only GET requests to the
Kubernetes API, so it is read-only by construction; it shares nothing with the Go code
but the resource shapes. For the Factory view it also reads pods, nodes and
metrics-server itself (`ui/health.mjs`) and sends the browser only a summary.

`make lint` enforces the rows marked "must not import" with golangci-lint's
`depguard`, so a wrong import fails the build rather than a review.

## Why the split

- **Business rules stay testable without a cluster.** Checking that a catalog entry
  supports a Container type, rendering template layers, detecting two layers writing
  the same path and assembling `devcontainer.json` are plain functions over plain Go
  values. Their tests are table tests that run in milliseconds.
- **Controllers stay thin.** A reconciler reads resources, turns them into plain
  values, calls the business rules and adapters, and writes status. If a controller
  grows an `if` about templates or types, that logic belongs in `internal/model` or
  `internal/scaffold`.
- **Git providers are swappable.** The controllers use the `gitprovider.Provider`
  interface. Gitea is the only implementation in v1; others slot in behind the same
  interface.

## Flow of one scaffold

1. `internal/controller` resolves the Container's references and converts the
   resources into `model` values.
2. `internal/model` checks every catalog entry supports the Container type.
3. `internal/gitprovider` creates the repository and fetches each template at its
   pinned ref.
4. `internal/scaffold` renders the layers in order (Framework, Compute, Deployment,
   Connectors), fails on a path owned by two layers, adds `.devcontainer/` and
   `.c4factory.yaml`, and returns one file tree.
5. `internal/gitprovider` pushes that tree as a single commit.
6. `internal/controller` writes `Ready`, `repositoryURL`, `scaffoldedFrom` and
   `appliedRelationships` with `appliedConnectors` (what each Relationship added).

## Flow of a Standard update

A Ready Container with a Standard follows its Standard's `spec.template.ref`, one
step per reconcile (`internal/controller/container_standard.go`); the minute
recheck polls the pull request, and a Standard's spec change enqueues its
Containers at once.

1. `internal/controller` takes the ref the Container's Standard files are at from
   `status.standard`, or before the first update from `scaffoldedFrom`
   (`model.ScaffoldedRef`). It compares it with the Standard's ref.
2. `internal/gitprovider` fetches the template at both refs; `internal/scaffold`
   renders each with the scaffold's input (`RenderLayer`); `gitprovider` reads
   `main`'s copy of every path either render has (`ReadFiles`).
3. `internal/scaffold.PlanUpdate` decides by whole files what to write, remove
   and leave out. Nothing to write: the controller records the new ref.
4. `internal/gitprovider` pushes one commit onto the `main` commit it read
   (`PushBranch`, branch `model.StandardUpdateBranch`) and opens a pull request
   with `model`'s title and body. The controller records it in
   `status.standard.update`.
5. Each later reconcile reads the pull request: merged, closed, conflicting,
   pushed to by someone else, or waiting for CI. Once CI is green,
   `model.StandardUpdateRefusal` checks the required checks and the paths, and
   `gitprovider` merges it as admin (`ForceMergePullRequest`). The controller
   moves `status.standard.ref` and reports each step in the condition
   `StandardUpdated`.

## Flow of one Session

1. The c4session feature installs the client in the devcontainer and runs
   `c4session start` after every start.
2. `internal/sessionclient` finds `.c4factory.yaml`, creates a Session on that
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
   unavailable agent, or fail.
3. To start a run, the controller makes sure the Container's agent bot user exists
   (`gitprovider.EnsureAgentAccess`, token in Secret `<container>-agent-git`) and
   creates an Agent Sandbox `Sandbox` owned by the Task, with the role's Agent
   (`spec.agentRef` implements, `spec.reviewerRef` reviews) and its harness and
   model. Its pod mounts no ServiceAccount token; the project's NetworkPolicy lets
   it reach only DNS, the Git host and public HTTPS. A Task with `spec.issue`
   reads the issue before every run and passes it as the goal; on its first run
   it labels the issue `status/in-progress`, assigns the bot and comments.
   `c4Context` gives the agent the Container's place in the story: the system's
   needs and KPIs, its users, each Relationship, and the related Containers'
   repositories.
4. In the pod, `cmd/c4agent` reads its job from the environment, and
   `internal/agentrunner` clones with the bot's token, clones the related
   repositories read-only, reads the role guide (`docs/agents/<role>.md`) from the
   base branch, runs the agent, runs `make check` and sends the agent back while
   it fails, pushes the branch, and writes one JSON `Result` to the termination
   message. A reviewer run answers with a JSON verdict and up to three follow-ups
   instead. A usage limit, an overload or rejected credentials come back as
   `unavailable`: the Task keeps its round and tries again later.
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
   `.c4factory.yaml`) go to a proposal branch `standard/<task>` and are reverted
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
   waits until the review run ends, then refuses (reason `GatedMergeRefused`); no
   reviewer run is spent on a CI wait. A pull request too large to list (1000+
   files) is refused too. A gated change may touch only its allowed paths, so
   neither its implementer nor its reviewer is asked for tests; the check
   command passing and the reviewer's three checks decide. The gated
   implementer's prompt overrides old guides:
   Containers on Standard v1.0.0 have guides that say "stop", and
   `flutter-standard` ships none and its `AGENTS.md` says "Leave .devcontainer/,
   .c4factory.yaml and .gitea/ alone"; the prompt says to follow it instead. Only
   `go-standard` and `ts-standard` (v1.1.0 and later) ship guides that describe Standard
   changes.

## Flow of one Crew

1. Every minute, and when a Task on its Container changes phase or the Container
   turns Ready, `internal/controller` reads the Crew, adopts it under its
   Container, checks its Agents exist, and lists the Container's Tasks live.
2. Through `gitprovider` it makes sure the status labels exist, lists the open
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
   spec.
4. The browser combines that with the Tasks, Agents, Containers, Sessions and
   Crews it watches (`src/model/factory.ts`): the board, each Task's timeline
   from `status.history`, the Crews, the time per stage and the "Needs a look"
   list.

The Task controller writes `status.history` in `writeStatus`, through
`model.RecordTransition`.
