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

## Flow of one Crew

1. Every minute, and when a Task on its Container changes phase or the Container
   turns Ready, `internal/controller` reads the Crew, adopts it under its
   Container, checks its Agents exist, and lists the Container's Tasks live.
2. Through `gitprovider` it makes sure the status labels exist, lists the open
   issues labelled `status/ready`, and reads each one's open dependencies.
3. `internal/model.NextCrewStep` decides: issues without an acceptance criteria
   checklist, already merged, or tried three times go back to a person; issues
   with open dependencies wait; while a Task works on the Container nothing
   starts; otherwise the lowest-numbered ready issue gets a Task
   `<container>-<issue>` (with `-<attempt>` from the second try), with the Crew's
   implementer and reviewer.
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
