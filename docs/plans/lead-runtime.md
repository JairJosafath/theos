# theos in a Claude Code session: the lead runtime

GitHub issue #331, written 2026-11-24 in the owner's session. The plan, not
the build: each step below is an issue.

## Goal

A person opens Claude Code in a project repository, on a laptop or as a
cloud session on the web. The session then works the project's issues the
way a Crew does on the cluster:
- the same model YAML, its `Crew` and `Agent` objects included;
- the same decision functions (`model.NextCrewStep`, `model.NextTaskStep`);
- the same branches, markers and labels on the Git host.

The session is the lead. theos says what is next, each role is a Claude
Code subagent, and a person merges.

[![theos's lead runtime: one model in YAML, one engine, and two runtimes, the operator on a cluster and the lead in a Claude Code session, both on the Git host](../images/lead-runtime.png)](../images/lead-runtime.svg)

The board's source is [images/lead-runtime.excalidraw](../images/lead-runtime.excalidraw).
Its icons are from theSVG (github.com/JairJosafath/thesvg).

Other harnesses (Codex, Kimi Code, OpenCode, Gemini CLI) are kept in the
shape of every interface here, but only Claude Code is built. Any MCP
client gets the work tools with no adapter.

## Read this first

- **This changes [no-cluster.md](no-cluster.md) for Claude Code.** It
  replaces that plan's decisions 4 to 8 with one engine that both runtimes
  call, and it reads Crews and Agents from the model where that plan kept
  them cluster-only. Decisions 1 to 3 and 9 to 12, the loss table and steps
  1, 4 and 7 to 11 stand. The table under
  [What this changes](#what-this-changes) lists each change.
- **It takes up [external-agents.md](external-agents.md)'s markers.** Both
  runtimes use one grammar: `offer`, `claim`, `renew`, `release`, `done`,
  `blocked`, `nothing`, `ended`. In the lead runtime theos posts the
  `offer` and the `ended`, as the operator does for an outside agent.
- **A person merges** (`PersonMerges`). By default every session holds the
  person's own credential (`AgentHoldsOwnerCredential`), as no-cluster's
  decision 9 says.
- No step needs Kind or `make e2e`.

## Decisions, and why

Code is cited as it is on main at 633ffc0.

1. **Two axes with two names: runtime and harness.**

   | Axis | The question it answers | Values |
   | --- | --- | --- |
   | Runtime | who drives the loop | `operator`: theos's reconcilers start the agents (Kubernetes); `lead`: an agent session calls theos, wherever it runs |
   | Harness | which agent tool does a role's work | `Agent.spec.harness`: `claude-code` here; `stand-in` in tests |

   A laptop session, a desktop session and a Claude Code cloud session are
   all the `lead` runtime, so the runtime is named by who drives, not by
   where it runs. It is not `session`, because `Session` is a kind.
   "Target" is not used either: `DeployTarget` is a kind.

   The runtime is not a field in the manifests. It is whatever reads them:
   the operator reads the API server, the `theos` binary reads `model/`. So
   a model moves between runtimes unchanged, and a Container repository
   still holds nothing that says which runtime made it.
2. **One engine, `internal/engine`, which imports `internal/model` and
   `internal/ports` only.** A tick does three things:
   - reads the facts through ports;
   - calls the decision function;
   - carries out the step through ports.

   The operator calls a tick from each reconcile. The lead runtime calls it
   from each `theos` command (`next`, `claim`, `done`, `status`). Both
   check the current state and move it one step, so a lead session that
   dies loses nothing: the next command starts from the host.

   Why: the decision functions are already pure (`NextCrewStep`,
   `internal/model/crew.go:276`; `NextTaskStep`, `task.go:1315`). What can
   drift between two runtimes is the code around them, which builds the
   facts and acts on the step. no-cluster's `internal/nocluster` would be a
   second copy of that code; the engine is the only copy.
   `internal/modelfacts` already does this for the scaffold.
3. **Two new ports, `TaskStore` and `Workers`, and a `Runtime` that
   bundles them with what it can do.**

   ```go
   // TaskStore keeps a Container's Tasks and their progress.
   type TaskStore interface {
       Tasks(ctx context.Context, container string) ([]model.CrewTask, error)
       Start(ctx context.Context, container string, s model.IssueStart) error // idempotent
       Progress(ctx context.Context, task string) (model.TaskProgress, error)
       SaveProgress(ctx context.Context, task string, p model.TaskProgress) error
   }

   // Workers runs one role's turn on a Task.
   type Workers interface {
       Offer(ctx context.Context, w model.WorkOrder) error
       Run(ctx context.Context, task string, round int, role string) (model.RunState, error)
       End(ctx context.Context, task string, round int, role, why string) error
   }

   type Runtime interface {
       Store() TaskStore
       Workers() Workers
       Capabilities() Capabilities
   }
   ```

   | Port | Operator adapter (`internal/controller`) | Lead adapter (`internal/adapters/hostrun`) |
   | --- | --- | --- |
   | `TaskStore` | Task objects and their status | the branch `task/<container>-<issue>` and its issue's markers |
   | `Workers.Offer` | make the Sandbox (theos pushes the work) | post an `offer` marker; `theos next` hands it out (the session pulls the work) |
   | `Workers.Run` | the Sandbox's pod as a `model.RunState` | the markers as a `model.RunState` (#307) |
   | `Capabilities` | everything | decision 6's table |

   The engine never knows whether the work was pushed or pulled. An
   operator-only behaviour (suspend, resume, pause) is an interface a
   `Workers` has or hasn't, as `internal/agentrunner` already does for a
   harness (`harness.go`, "the runner asks for the interface, never for
   the harness").
4. **The Git host is the lead runtime's only store.**
   - Progress (the round, the phase, the round's feedback) is read back
     from markers.
   - Worktrees live under the project repository's `work/`, which git
     ignores, and can be thrown away at any time.
   - Nothing lives in the session's home directory.

   Why: a Claude Code cloud session's container goes away, and a laptop
   session can be closed. With the state on the host, a new session picks
   up where the last one stopped.
5. **theos makes the lock, not the session.** The engine's Crew tick
   starts a Task. Its `TaskStore.Start` creates the branch (`CreateBranch`
   fails on a branch that exists, `internal/ports/githost.go:403-405`),
   labels the issue `status/in-progress` and posts the `offer`. A session
   only claims an offer; it never creates a branch.
   - A `task/` branch with no offer and no pull request is the engine's own
     start that stopped halfway, and the next tick posts the offer.
   - So no-cluster's third case (`LockWithoutClaim`, a lock a person
     clears) goes. The `task/` prefix is theos's; a person's branch doesn't
     use it.
   - Two lead sessions on one Crew are safe: the host lets one branch
     creation through. They may run one Task over `parallel` between a list
     and a create, and the guide says to run one lead per project.
6. **Crews and Agents are read in the lead runtime, from
   `model/setup.yaml`.** Each field is either honoured or named with a
   reason word by `theos status`. Nothing is dropped silently.

   | Field | Lead runtime | Reason word |
   | --- | --- | --- |
   | Crew `implementer`, `reviewer`, `routes`, `parallel`, `paused`; Agent `maxRounds` | honoured | |
   | Agent `model` | honoured: the subagent's model | |
   | Agent `harness` | `claude-code` honoured; any other value is read for its MCP tools only | `HarnessNotBuilt` |
   | Crew `triager`, `specWriter`, `specApproval`, `verifier` | honoured since step 13 (was `RoleNotBuilt`) | |
   | Crew `schedules`, `tagReleases`, `prepareReleases`, `releaseCadence`, `publishReleases` | not honoured | `NoScheduler` |
   | Crew `approval`, the operator's merge | a person merges | `PersonMerges` |
   | Crew `taskBudgetUSD`; Agent `sessionTokenLimit` | not honoured | `CostUnreported` |
   | Agent `image`, `resources`, `env`, `credentialsSecretRef`, `timeoutMinutes` | not honoured | `NoSandbox` |

   `image` stays required by the CRD. `theos init` writes the chart's
   agent image there, so the file moves to a cluster unchanged. A table
   test has a row per field of `CrewSpec` and `AgentSpec`, so a field
   added later fails the test until someone decides its row.
7. **A harness is an interface. The content is theos's; the format is the
   harness's.**
   - `internal/method` holds the content: the lead playbook, the role
     boundaries and the work order text, as pure renderings (no-cluster
     decision 5).
   - `internal/harness` defines the interface:

     ```go
     type Harness interface {
         Name() string               // the value in Agent.spec.harness
         Capabilities() Capabilities // subagents, parallel, a model per role, MCP
     }
     // Optional parts, asked for by interface, never by name.
     type LeadFiles interface{ Render(p Project) []File }
     ```

   - `internal/harness/claudecode` renders the content into the project
     repository:
     - `CLAUDE.md`: one line that imports `AGENTS.md`;
     - `AGENTS.md`: the lead playbook;
     - `.claude/agents/theos-<agent>.md`: one file for each Agent a Crew
       names, with the Agent's `model` and the role's tools. The reviewer
       gets no `Agent`, `Edit` or `Write` tool: its verdict is its own;
     - `.mcp.json`: starts `theos mcp` over stdio.

     Each file is marked as generated, and `theos status` reports one that
     drifted.
   - These files go in the project repository. A Container repository's
     `CLAUDE.md`, `.claude/` and `.mcp.json` are its Standard's protected
     paths, and theos doesn't touch them there.
   - Three levels, for the other harnesses later (step 14):
     - 0: any MCP client gets the work tools, one role per session, with
       no code;
     - 1: `LeadFiles`;
     - 2: headless in an operator sandbox, today's `agentrunner.Harness`
       (#58).
8. **The loop in a Claude Code session.**

   ```
   lead (main session, in the project repository)
     theos_next  ──► engine: Crew tick, then a Task tick for each Task
                 ◄── open offers, up to parallel, each naming its subagent
     spawns theos-<implementer> with the work order, in the background for parallel
       subagent: theos_claim  (claim marker; theos makes work/<task>, a worktree of the Container)
                 works, runs the Container's own checks (its AGENTS.md), pushes
                 theos_done   (done marker: the commit and a summary)
     theos_next  ──► engine: run done ⇒ OpenPR (theos opens it) ⇒ StartReview ⇒ offer
     spawns theos-<reviewer>  ──► theos_done with its verdict
     theos_next  ──► changes requested ⇒ round 2 with the feedback
                 ──► approved ⇒ Merge, which here means waiting for a person
   person: theos merge, or the host's pages ──► next tick: the Task is done, the issue closes
   ```

   theos opens the pull request and decides every transition; the lead
   only spawns the subagents and passes the work orders through. Any
   `theos` call renews a claim with under half its lease left, and the work
   order tells a role to call `theos_heartbeat` after half an hour without
   one (no-cluster decision 8's lease, unchanged).
9. **A Claude Code cloud session is the same runtime. theos assumes
   nothing about the machine.**
   - The credential comes from `THEOS_GIT_TOKEN`, set as the environment's
     secret, or else from `git credential fill`. theos never stores it.
   - The binary comes from the release (#318), or from `go install` at a
     tag in the environment's setup script.
   - The project repository and every Container repository must be added
     to the session.
   - The work order says to push after each green check: work that isn't
     pushed dies with the container. A session that dies runs out its
     lease, the engine posts `ended`, and the Task is offered again on its
     branch (`AfterInterruption`, `task.go:320`).
10. **The engine posts the `offer` and `ended` markers in both runtimes.**
    In the lead runtime theos and the sessions share one account by
    default. So a marker counts by its kind and by the `claimants` setting
    (no-cluster decision 8), and the session's name in the marker tells
    the sessions apart. The session's name is its own word
    (`ReviewNotEnforced`, unchanged).
11. **The operator moves onto the engine one piece at a time, and envtest
    is the safety net.**
    - The Crew tick moves first (step 1). It is small and already
      separates its decision.
    - Then the host side of a Task: its pull request, checks, verdict and
      merge (step 5).
    - The run side, the Sandbox, moves last (step 12). It is a protected
      path and the largest piece.

    Every envtest spec passes unchanged at each step. Releases, schedules,
    deploys and Briefs stay in the reconcilers until a second runtime needs
    them: abstract what two runtimes use, not everything.
12. **What can be configured is configured.**
    - The host, its owner and the registries are the GitProvider's fields.
    - The roles, models and parallelism are the Crew's and the Agents'.
    - The lease, the grace, `claimants`, `merge` and the project
      repository's name stay in `model/settings.yaml` (no-cluster decision
      12).
    - The lead runtime adds no setting of its own.

## What this changes

| In [no-cluster.md](no-cluster.md) | Now |
| --- | --- |
| Decision 4: `internal/nocluster` holds the commands' logic | it holds the commands; the loop is `internal/engine`'s, and the lead runtime's adapter is `internal/adapters/hostrun` |
| Decision 5: one fixed method bundle | `internal/method`'s content, rendered by a harness; which Agent plays which role, on which model, is the Crew's |
| Decision 6: runtimes are Claude Code, `AGENTS.md` readers, MCP clients | those are harnesses (decision 1 here); Claude Code is built, the rest are level 0 |
| Decision 7: `theos next` hands out one work order | it returns the engine's open offers, up to the Crew's `parallel` |
| Decision 8: a session claims by creating the branch; `LockWithoutClaim` | the engine creates the branch and posts the offer; a session claims an offer; the lock a person cleared is gone |
| "Cluster-only": Crews | Crews are read in both runtimes; schedules, releases and budgets stay cluster-only |

## Steps

Priority 1 is the first wave. Work them in the order below: each one names
what it needs. "Tier" is AGENTS.md's.

| Step | Issue | Priority | Needs | Tests it brings |
| --- | --- | --- | --- | --- |
| 1. The engine and its ports: the Crew tick, the operator's `TaskStore` | #332 | 1 | | tier 4: the Crew's envtest specs unchanged; tier 1: the engine on the fake store and host; `contract.TaskStore`; `TestImports` |
| 2. Claims and the lease in `internal/model` | #267 | 1 | | tier 1: a row per sentence of decision 8's lease, with decision 5's change |
| 3. The run markers as a `RunState` | #307 | 1 | 2 | tier 1: a row per marker; another account's marker changes nothing; a quoted marker is none |
| 4. Crew and Agent in the model files; capabilities | #333 | 1 | | tier 1: a row per field of decision 6; tier 2: a sample setup file |
| 5. The engine's Task tick, host side shared | #334 | 1 | 1, 3 | tier 4: the Task's envtest specs unchanged; tier 1: one Task to a pull request waiting for a person |
| 6. The lead runtime on the Git host, `hostrun` | #335 | 1 | 1, 3, 5 | tier 1: the contracts on the fake and `githubtest`; the whole Task end to end; tier 3 on Gitea |
| 7. The harness interface and Claude Code's files, `theos agents` | #266 | 1 | 4 | tier 1: the files for a Crew; the reviewer's tools; every work order names `AGENTS.md` and `theos heartbeat` |
| 8. `theos mcp`: the work tools | #294 | 1 | 6, 7 | tier 1: an in-process client over stdio; no merge tool; issue text fenced, no credential |
| 9. A person merges, `theos merge` | #292 | 1 | 5 | tier 1: the merge table; refused away from a terminal |
| 10. Release the `theos` binary | #318 | 1 | | the release itself; the owner's session (`.github/`) |
| 11. The guide: Claude Code on a laptop and on the web | #297 | 1 | 6 to 10 | `make doc-links`; a person's walk of one Task in each |
| 12. The operator's Task reconciler on the engine | #336 | 2 | 5 | tier 4 unchanged; `contract.Workers` on the Sandbox adapter |
| 13. Triager, spec writer and verifier in the lead runtime | #337 | 2 | 6 to 8 | tier 1 on the fake host; tier 4 triage unchanged |
| — `theos plan` | #293 | 2 | 6 | its issue's |
| — `theos update`, `export`, `import` | #295 | 2 | 4 | its issue's |
| 14. Harness authoring: `harnesstest`, the guide, Codex | #338 | 3 | 7 | tier 1: the suite on both harnesses |

**Step 1 as built** (`internal/engine/crew.go`, `internal/ports/runtime.go`,
`internal/controller/crew_tasks.go`). Choices the plan left open:
- `CrewTick` returns a `CrewResult`: the step, and the hand-backs that
  failed, by issue. The engine logs nothing; the runtime logs them. A
  failure to read the host is an `*engine.HostError`, so the operator still
  waits on `GitHostUnavailable` while any other error is the store's.
- `CrewInput.Before` runs between the read and the decision, with the facts
  the decision takes. The operator's releases, schedules and onboarding run
  there, as they ran there before. It doesn't run while the Container isn't
  Ready, and the host may then be nil.
- `TaskStore` has `Tasks` and `Start` alone; `Progress` and `SaveProgress`
  come with #334, the first step that calls them. `Tasks` leaves out Triage
  Tasks, which the Crew reads apart.
- `Workers` and `model.WorkOrder` are declared, unused. The operator's
  `Workers()` is nil until #336, as is the fake's until #334.
- `Capabilities` has a field per reason word of decision 6 but
  `HarnessNotBuilt`, which is a harness's: `Merges`, `Sandboxes`,
  `Schedules`, `Costs` and `AllRoles`. The operator has all five.
- The reconciler still lists the Tasks itself, for releases, triage and the
  Paused condition; the store lists them again, live. That is one more
  list a reconcile, and the store's is at least as new as the decision
  needs. The proposed issues a triager reads are read by the reconciler,
  before the tick.
- `contract.TaskStore` runs on the fake and on the operator's adapter over
  controller-runtime's fake client, in `make test`.

**Step 2 as built** (`internal/model/claim.go`). Choices the plan left
open:
- A marker line is `<!-- theos:<kind> key=value ... -->`, keys sorted,
  each value one word, so it reads back as written; #307's kinds take the
  same form. A session's marker names it as `session=<name>`.
- A counted `offer` starts the work afresh: a claim before it, or before a
  later round's offer, counts for nothing.
- A holder is its account and its session's name together, so two
  claimant accounts with one session name are two holders.
- `lease`, `grace` and `claimants` reach `model/settings.yaml` with #294,
  the first step that reads them: the settings decode refuses a key no step
  reads. Until then `DefaultClaimRules` is the only source.

**Step 3 as built** (`internal/model/run_markers.go`). Choices the plan
left open:
- `RunFromMarkers` fills the `RunState` the Task's decision already takes,
  with no pod and no checkpoint. What only a run on the host has goes in
  `RunState.Markers`: the offer, the claim, `Unclaimed`, `EndDue` and
  `DoneWaits`.
- The engine's clock only says that an `ended` is due (`EndDue`:
  `silent` past the lease and the grace, `released` after a release). Only
  a counted `ended` makes the run interrupted. So the run's next offer
  always comes after a stamp the host gave, and an `ended` that came too
  early never lets a new offer wipe a live claim.
- `ClaimRules.Engine` holds the accounts whose `offer` and `ended` count:
  the operator's login for an outside agent. It is empty in the lead
  runtime, where they count from `claimants` by their kind.
- A `done` counts from the holder alone, and the last one counts. An
  implementer's names `commit=`: the full head, or a prefix of at least
  7 characters. A reviewer's names `verdict=`. A triager's decision is the
  one fenced json block of its text, read strictly into `Triage`. A
  takeover needs its own `done`.
- `blocked` and `nothing` close the run with their note. A `blocked`
  with no reason still blocks.

**Step 4 as built** (`internal/model/runtime_fields.go`,
`internal/nocluster`). Choices the plan left open:
- `Capabilities` is `internal/model`'s, as `model.NotHonoured` reads it,
  and `ports.Capabilities` is the same type. `OperatorCapabilities` has
  all of it, `LeadCapabilities` none but `claude-code` among the
  harnesses.
- A field counts as set when its document wrote it, before the CRD's
  defaults: `Model.Written` keeps those, so a defaulted `timeoutMinutes`
  is no `NoSandbox` line, while a written one is. A field written as
  `null` counts as left out. The harness is read whether written or
  defaulted.
- `pauseRuns` is `NoSandbox`: it pauses runs in their sandboxes.
  `specApproval` is `RoleNotBuilt`, with the spec writer. The PersonMerges
  line is the Crew's own, whatever fields it sets.
- A field the table has no row for is `UnknownField`, never dropped;
  `TestEveryFieldHasARow333` fails on a new field of `CrewSpec` or
  `AgentSpec` until it has a row.
- `theos init` writes the Agents `implementer` and `reviewer` from the
  chart's values (description, model, maxRounds) at its agent image, the
  tag the chart's, else Chart.yaml's appVersion, else the release's
  version. It can't write the Crew: a new model has no Container, and a
  Crew must name one. It writes the Crew as a comment to uncomment, and
  `theos status` says, of each Container no Crew works, to add one.
- The sample is `config/samples/webshop/crew.yaml`, kept out of the
  webshop's kustomization: on a cluster the chart makes the Agents.

**Step 5 as built** (`internal/engine/task.go`, `internal/engine/task_host.go`).
Choices the plan left open:
- `TaskStore` keeps a `ports.TaskRecord`: the `model.TaskProgress`, the
  pull request and its URL, the round's summary, when the phase last
  changed, and the reason and message a Task ended with. A pull request
  is no part of `TaskProgress`, which the operator's transitions change.
- `model.WorkOrder` carries what #334 lists: Task, issue, branch, round,
  role, Agent, feedback and lease, and the `Text` #266 renders.
- The host halves both runtimes call are `ReadChecks` (with the CI runs,
  the rerun's state and who cancelled a run; the operator passes the
  runner pods' fate as a function), `ReadPull` with `PullWanted`,
  `ReadBase` and `UpdateBranch`. The Task reconciler's `checkFacts`,
  `pullFacts`, `baseFacts` and `updateBranch` call them, and the
  reconciler's own copies went. `approvalFacts` stays: it reads the
  review's Sandbox, the run side.
- The host actions of `task_actions.go` that write the operator's status
  as they go (open, merge, approve, comment) stay until #336, which moves
  the reconciler onto `TaskTick`. `TaskTick` has its own: it opens the
  pull request, merges where the runtime `Merges`, closes the issue and
  hands a failed one back.
- Capabilities turn steps: a `Merge` in a runtime that merges nothing
  waits with `PersonMerges`, and the tick after a person merged ends the
  Task Merged and closes its issue. A step the engine has no way to carry
  out (a sandbox suspended or resumed in place, a Standard change, a
  spec, a verification) waits with `NotBuilt`.
- A run whose claim ran out (`MarkerRun.EndDue`) is ended by the tick
  that sees it, read back interrupted, and decided on in the same tick:
  `Rerun` offers it again on its branch. A rerun, a retried review and a
  conflict to resolve offer their turn in the same tick, since a turn of
  the same round offered again starts afresh, and the round's earlier
  answers count until it is.
- A request for changes goes on the pull request as a review, or, when
  the host refuses one (an account's own pull request, as where theos
  and the sessions share one), as a comment with the verdict.
- Every Task envtest spec passes unchanged; the Task tick's tests are
  tier 1 on `fake.Tasks`, `fake.Workers` and the fake host.

**Step 6 as built** (`internal/adapters/hostrun`, `internal/ports/comments.go`).
Choices the plan left open:
- The ports gain `ports.IssueComments`, an optional part of `Issues`
  asked for by interface: an issue's comments with the host's IDs,
  authors and stamps, and a comment posted and read back as stamped.
  `CommentIssue` stays as it is for its thirty callers. Gitea lists an
  issue's comments whole; GitHub pages them, and the adapter reads every
  page.
- A Task's progress is a `progress` marker the engine posts at each
  transition, `<!-- theos:progress task=<task> -->`, its `TaskRecord`
  a fenced json block read strictly; the last counted one is the Task's.
  Comments are never edited, so a Task's history is on its issue. A
  session can't post one (`SessionMarker` refuses it), and no run reads
  it.
- `Start` creates the branch at main's head, labels the issue
  `status/in-progress` and posts the first progress, Pending. A branch
  there already is the Task's own, from a start that stopped halfway or a
  lead that raced this one, and the start goes on from it. The offer is
  the Task tick's first step, so a start that stopped before it is
  finished by the next tick, as decision 5 wants, with no step of
  `Start`'s own.
- `Tasks` lists the Tasks at work: the issues labelled
  `status/in-progress`. A Task that ended is not listed: its issue is
  closed, or handed back to a person. `contract.TaskStore` takes that:
  an ended Task is listed final or not at all. The lead runtime starts
  no Spec Task (`NoSpecs`).
- `Workers.Run` is `model.RunFromMarkers` on the issue's comments and the
  branch's head, for the turn the counted offer names, else not started.
  `Offer` posts nothing while the turn is offered and not answered or
  ended. `End` posts `ended` only for a turn still open: a done one needs
  none, the next offer starting afresh.
- A Runtime is one Container's; a Task's issue is read from its name
  (`model.IssueTaskName`).
- Not here yet: `lease`, `grace` and `claimants` in `model/settings.yaml`,
  and `cmd/theos`'s wiring. Both come with `theos mcp` (#294), the first
  command that runs a tick: the settings decode refuses a key no step
  reads, and `hostrun.New` takes its `model.ClaimRules` from the caller.

**Step 7 as built** (`internal/method`, `internal/harness`, `theos agents`).
Choices the plan left open, after reading Claude Code's subagent and MCP
documentation (code.claude.com/docs/en/sub-agents, .../mcp, .../memory):
- A subagent's `model` is the Agent's `model` as written: Claude Code
  takes a full model ID there as well as an alias. Its `tools` are a
  comma-separated list; `mcp__theos__*` gives it every tool of
  `theos mcp`. The implementer has `Read, Grep, Glob, Edit, Write, Bash`,
  the reviewer `Read, Grep, Glob, Bash`: no `Agent`, `Edit` or `Write`.
  Neither has `Agent`, so neither starts another subagent.
- The generated marker is an HTML comment in the Markdown files and a YAML
  comment in a subagent's frontmatter. `.mcp.json` is JSON, which takes
  no comment, and carries none; `theos status` compares every file with
  what `theos agents` writes now, so a hand edit of it is named too.
- An Agent two Crews give two roles is refused: its one subagent file
  can't have both roles' tools.
- `issueBlock` and its intro moved to `internal/model` (`IssueBlock`,
  `IssueIntro`), which `internal/agentrunner` and `method.WorkOrder` call.
  The planner's plan format is not in the playbook yet: `theos plan`
  (#293) is the first step that hands one out.
- The engine renders no text: `TaskInput.OrderText` is the runtime's
  rendering of an offer (`method.WorkOrder`, which #294 passes), and
  `model.WorkOrder.Goal` carries the issue for it to fence.
- `theos agents` writes only a file that differs and says of each whether
  it was written or unchanged; a model with no Crew is refused. `theos
  status` names a file that is missing or changed, when the model has a
  Crew.

**Step 8 as built** (`cmd/theos/lead.go`, `internal/nocluster/lead.go`).
Choices the plan left open:
- The MCP SDK is github.com/modelcontextprotocol/go-sdk v1.8.0: `go list
  -deps` shows it brings neither client-go nor controller-runtime, and
  `cmd/theos`'s `TestImports` keeps it so. It is linked by `cmd/theos`
  alone; the tools' work is `internal/nocluster`'s, on the ports and the
  engine, and `cmd/theos` builds `hostrun` for it (`Env.Runtime`), as it
  builds the host adapters.
- Six tools: `theos_next`, `theos_claim`, `theos_heartbeat`,
  `theos_release`, `theos_done` and `theos_status`, each reading the model
  files afresh. Each is also a command, printing its result as JSON:
  `theos next`, `claim`, `heartbeat`, `release` and `done`, with `-task`,
  `-session`, `-commit`, `-verdict` and `-summary`. No merge tool.
- `theos_next` runs each Crew's tick, then a Task tick for each Task at
  work, and returns the open offers (offered and unclaimed), each with its
  subagent and its work order (`method.WorkOrder`, which fences the
  issue), and a line for every Task at work with its phase and the step's
  reason, such as `PersonMerges`. The Agents of a Task are the Crew's,
  or a route's by the issue's labels, read each tick.
- A claim posts the claim, reads the issue back and holds the work only
  when the host's order names the session (`model.NextClaim`,
  `model.ClaimOf`). It then clones the Task's branch into `work/<task>`
  with git, which asks its own credential helper: theos hands git no
  credential. `work/` holds a `.gitignore` of `*`, so no clone lands in
  the project's commits. A plain clone, not a worktree: the project
  repository is not the Container's.
- `heartbeat` renews under half the lease left and says how long the
  claim holds; `release` and `done` post only for the session that holds
  the claim. `done` takes a verdict of `approve` or `changes` alone.
- `lease`, `grace` and `claimants` are `model/settings.yaml`'s keys now,
  Go durations, 60m and 5m and the credential's own login by default;
  `theos init` spells the two durations out.
- No result carries the credential, and the issue's text reaches a result
  only fenced, in a work order: the test reads every result for both.

**Step 9 as built** (`internal/model/person_merge.go`, `theos merge`).
Choices the plan left open:
- `theos merge -task <task>` holds itself, on every host, what no host
  is trusted to hold for a person: a counted approving verdict on the
  pull request's head, the checks main requires (`model.BaseState` of the
  rule's contexts on the head) and the protected paths (the pull
  request's files against the rule's). The host's merge holds what it
  holds besides, as GitHub's adapter does. The fake host's merge stays
  as the controller's specs know it.
- The verdict counts when it is a reviewer's `done` with `approve` in a
  review run offered after the implement run whose `done` named the
  head, from a holder other than that run's: a session's name tells the
  sessions apart where they share one account (decision 10). A verdict
  of an earlier round is `VerdictOnAnOlderHead`; the author's own is
  `AuthorsOwnVerdict`; one from outside `claimants` counts for nothing,
  so `NoApprovingVerdict`.
- `merge` in `model/settings.yaml` is `person` (the default) or `rules`;
  any other is refused at decode, and `rules` is refused at the merge,
  `RulesMergeNotBuilt`, until #298.
- Away from a terminal (stdin no character device) `theos merge` is
  refused, a speed bump for an agent, not a fence; `theos mcp` has no
  merge tool. The next tick sees the merge and ends the Task Merged, its
  issue closed.
- Not run here: the merge on the fake GitHub and on Gitea. The checks and
  protected paths are theos's own on every host, and the model's table
  and the fake host's run test them; GitHub's adapter merge has its own
  tests; Gitea needs a non-root host for `make test-gitea`.

**Steps 10 and 11 as built.** The release workflow builds `theos` for
Linux and macOS, amd64 and arm64, stamped with the release's version so
`theos init` pins that release's catalog, and attaches it with the other
binaries in `SHA256SUMS` (#318); the README names the download and
`go install`. [no-cluster.md](../no-cluster.md) is the guide (#297): getting
theos and its credential, `init`, `scaffold`, `protect`, `agents`, one Task
on a laptop and in a cloud session, one lead per project, and each reason
word `theos status` prints. A person's walk of one Task in each, with the
host's pages in `docs/screenshots/`, is still to do: it settles
[Not tested, and who can](#not-tested-and-who-can), and the guide says
what it found once it is done.

**Step 12 as built (slice 1: the Workers adapter)** (`internal/controller/task_workers.go`).
#336 lands in slices, each green as its own pull request. This first one is
the operator's `Workers` and the contract run on it. The Task reconciler is
unchanged: nothing outside the tests builds these Workers yet, and every
Task envtest spec is untouched. Choices the plan left open:
- **The Workers are one Task's, for one reconcile** (`r.workers(task,
  status, build)`), as `crewRuntime` is one Crew's. They work on the status
  as the reconcile leaves it, which the reconciler writes once, at the end.
  A turn of another Task is an error.
- **A turn is a Sandbox named as the reconciler names it.** The name is
  `model.SandboxName` of the call's round and role and the status's
  restarts, review attempts and reruns (`sandboxName`). So the engine's
  restart (end the turn, save the moved counters, offer it again) makes a
  new Sandbox, as the reconciler's rerun does today.
- **Offer** makes the Sandbox with `buildSandbox`, from a `sandboxBuild`
  the caller passes: the run's inputs, `runSandbox`'s. Like `startRun`, it
  names the Sandbox in the status and forgets the old pod and
  `agentBackAt`. If the Sandbox is there already, Offer makes nothing; it
  only names it in a status that forgot it.
- **Run** is `runState` on the pod the status says the run started on,
  then `withSuspendedFor` and `withoutNothing`: `facts.Run` as the
  reconciler reads it now. It also keeps the run's first pod in the status,
  as `facts` does, so a replacement pod reads as an interrupted run.
- **End** of the current run is `endRun`: the run is counted, recorded
  and its leases taken. Its Sandbox is deleted after `writeStatus` inside
  a Reconcile (`withEndedRuns`, #922), and at once outside one. End of
  another turn deletes that Sandbox the same way. So the engine's `next`
  and `restart`, which end a turn before they save the progress, lose no
  run when the status write fails.
- **A run ended for its claim** (`silent`, `released`). A Sandbox has no
  claim, and the operator's Run never says an end is due. But the contract
  asks that such a run read interrupted until its turn is offered again,
  and an outside agent on a cluster (#260) would have a claim. So End
  marks the Sandbox `theos.factory.io/ended-run=<why>` and suspends it at
  once, and a current run is counted and recorded first. Agent Sandbox
  deletes its pod and starts no other. Run then reads it done and
  interrupted. The turn's next End deletes it. An Offer of the turn deletes
  it and makes a new one, and errs while the old one is still being
  deleted.
- **The contract's lapse** on Sandboxes is a pod evicted with no answer
  (`podDisrupted`). Run reads that as done and interrupted, which the
  contract takes.
- Tests: tier 1, `TestSandboxWorkersContract336` (`contract.Workers` on
  controller-runtime's fake client, with the UIDs an interceptor gives) and
  the adapter's own cases: `TestSandboxWorkersNameTheTurnAsTheReconciler336`,
  `TestSandboxWorkersDeleteTheSandboxAfterTheStatus336`,
  `TestSandboxWorkersKeepARunEndedForItsClaim336` and
  `TestSandboxWorkersRunTheirTaskAlone336`. Tier 4, "The operator's Workers
  keeps the Workers contract on Sandboxes": `contract.Workers` in envtest,
  as the operator's ServiceAccount under the generated role. It runs as a
  subtest of the suite's own `*testing.T` (`suiteT`), since a contract
  takes one.

What the later slices of #336 must do:
- **Slice 2, the run side on the tick.** `Reconcile` reads the Task, calls
  `engine.TaskTick` and writes the status. Its runtime's store keeps the
  status in memory (`Progress` from it, `SaveProgress` into it, the one
  write still `writeStatus`). Its Workers are `r.workers(task, status,
  build)`, with `build` from `runSandbox`'s inputs. The steps before a run
  can wait or end the Task: `runInputs` (the Agent's credentials, the
  Standard on main, a closed issue), `continueEarlierTry` and
  `startAtReview`, `leaseEnvironments` and a Benchmark's base branch. Offer
  returns only an error, so these need a place of their own: an optional
  part of `Workers` the engine asks for before it offers, or a typed wait
  that `build` returns and the tick turns into a wait. Choose one and
  record it here. The first run's comments (`announce`) and the `Running`
  condition go with it.
- **Suspend, resume and pause** become optional parts of `Workers` the
  engine asks for by interface (`ports.Suspender`, `ports.Resumer` and a
  pauser), as `agentrunner` asks for a harness's. The operator's Workers
  implement them with `suspendRun`, `resumeRun` and `syncPause`. The Task
  tick's `carry` turns those steps into `NotBuilt` waits today.
- **What the tick doesn't carry yet**: the operator's own host actions
  (open, merge, approve and comment as they write status), the Standard
  change, specs, verification, a person's approval and rules, budgets, and
  the Plan, Triage and Score Tasks' reconciles. Their roles' Sandboxes are
  named by `model.SandboxName` too, so the same Workers serve them.
- `slotHolders` counts a Sandbox marked `ended-run` as holding a slot while
  it lasts. In the engine it lasts only until the same tick's restart ends
  the turn. A slice that lets it last longer leaves it out of the count.

**Step 13 as built** (`internal/engine/triage.go`, `internal/engine/task_roles.go`,
`internal/model/answers.go`, `internal/model/lead_roles.go`). All three
roles are built; none is left `RoleNotBuilt`. Choices the plan left open:
- **The triage tick** is `engine.TriageTick`, on `ports.TriageStore`, an
  optional part of a `TaskStore` asked for by interface: its Triage Tasks
  and a start. The operator's Crew reconciler calls it where `r.triage` was,
  its store listing the Triage Tasks live; the proposed issues are read in
  the tick, with a triager on a Container that is Ready and protected (a
  Crew on an unprotected one no longer reads them, which decided nothing).
  A decision's writes to the issue are `engine.ApplyTriageActions`, which
  the operator's Task reconciler calls too; the rest of its triage stays
  there until #336.
- **One parser.** `ParseTriage` and `ParseVerdict` moved from
  `internal/agentrunner` to `internal/model` (`answers.go`), in the
  owner's session, as #311 asks; the runner calls them. A triager's `done`
  is read by `ParseTriage`, which replaces step 3's strict reading of one
  fenced block: the last JSON object with a decision counts, fenced or
  not, and an unknown field is ignored, as the runner reads an agent. A
  reviewer's or a verifier's `done` may end with a JSON verdict in the
  runner's format, read by `ParseVerdict` for its follow-ups and open
  criteria; one whose JSON says another verdict than its `verdict=` gave
  none.
- **A Triage Task on the host** is its progress markers on its issue, the
  record marked `Triage`: no branch, no label, as the issue stays
  `status/proposed` until the decision is applied. `TriageTasks` reads the
  proposed issues' markers (`model.HostTriageTask`); one that decided, on
  an issue a person labelled `status/proposed` again, counts as answered,
  as a chat answer does on a cluster, so the issue is triaged again. A
  claim clones `main`. One whose issue a person moved out of
  `status/proposed` mid-run is no longer ticked; its open offer is
  harmless, and `PlanTriage` would skip it anyway. The lead runtime has no
  test environments, so a decision's `environments` label nothing.
- **A Spec Task** is marked `Spec` in its record, which `Tasks` reads (the
  contract's `NoSpecs` is gone). Its issue keeps `status/in-progress` until
  the spec merges, as the lead lists its Tasks by that label; the
  operator's `status/spec-review` is not used. A person's approval is a
  review by anyone but theos's own accounts (`ClaimRules.Ours`: the
  claimants and the engine's), or their merge, which is the only one where
  everything is one account. The agents' approval goes on the pull request
  as a comment: a host refuses an account's review of its own pull
  request. The spec merged, `PlanSpecMerged` writes its criteria into the
  issue, which stays open and `status/ready`.
- **The verifier** runs before the merge, as design.md's Verify says: "a
  verifier run after a merge files its follow-up" is read as what the
  round leaves being filed once a person merged. What an approving review
  and verification leave (`model.Findings`, kept by `KeepFindings` in the
  `TaskRecord`, as their runs end before the merge) is filed by the tick
  that sees the merge: the reviewer's follow-ups as proposed issues and the
  open criteria as one `kind/by-hand` issue, in `Later`, as the operator
  files them. The by-hand issue is found again by its marker; a tick that
  fails between the follow-ups and its progress may file them twice, as
  the operator's may.
- **Subagents.** The triager has `Read, Grep, Glob` and theos's tools, no
  `Bash`: it writes no file. The verifier adds `Bash`, to run the build,
  and no `Edit` or `Write`. The spec writer has the implementer's tools;
  its role is `method.RoleSpecWriter` (`spec`), as its run is the
  implementer's turn of a Spec Task (`WorkOrder.Spec`). `harness.Grants`
  gains `Shell`, and `harnesstest` checks each boundary. An Agent may be
  both the implementer and the spec writer (`method.Shares`): its one
  subagent keeps both roles' boundaries; any other pair is refused.
- **Capabilities.** The four rows are honoured by every runtime, and
  `RoleNotBuilt` and `Capabilities.AllRoles` are gone.
- Still `NotBuilt` in the lead runtime: a sandbox suspended, paused or
  resumed, and a Standard change.
- Tests: tier 1 `TestTriageToReady337`, `TestTriageSentBackByAPerson337`,
  `TestSpecApprovedThenImplemented337`, `TestSpecRefusedAndDeclined337` and
  `TestVerifierFilesItsFollowUp337` (hostrun on the fake host),
  `TestLeadTriages337` (`theos next`, `claim`, `done`), and the model's,
  method's and harnesses' rows; tier 4, the Crew's triage specs unchanged.

**Step 14 as built** (`internal/harness/harnesstest`, `internal/harness/codex`,
[harness-authoring.md](../harness-authoring.md)). Choices the plan left open:
- The harness is the lead's tool, which the person picks: `theos agents
  -harness codex`, Claude Code by default. `Agent.spec.harness` stays the
  program an operator's sandbox runs (`claude-code`, `stand-in`): a Codex
  Agent runs in no sandbox yet (level 2, #58), so the enum is unchanged.
- `harness.RoleGrants` is the part a harness has when its tool enforces
  each role's tools. Claude Code has it; Codex, whose custom agents set no
  tools and no sandbox, hasn't, and its reviewer's boundary is its
  instructions alone, which the guide says.
- `harnesstest.Check` returns the problems, `Run` reports them, so a test
  can show that a harness that drops the reviewer's boundary fails.
- `theos status` checks the files of each harness whose mark file the
  project has (`CLAUDE.md`, `.codex/config.toml`), else Claude Code's.

Steps 1, 2 and 4 need nothing and can run side by side. Step 10 can run
whenever the owner's session takes it.

**Protected paths,** the owner's session's; a Crew's agent given the step
says so and stops (AGENTS.md):
- steps 1, 6 and 7: depguard's rows for `internal/engine`,
  `internal/adapters/hostrun` and `internal/harness`, in `.golangci.yml`;
- step 10: `.github/`;
- step 12: `internal/controller/task_sandbox.go`;
- any step that adds a make target: `Makefile`.

## Not covered

- Other harnesses beyond level 0, until step 14. #58 keeps the operator's
  side of more harnesses.
- An outside agent filling a Crew spot on a cluster (#260). It uses the
  same markers and `theos` tools, and #310 becomes those tools with the
  engine running in the operator.
- `merge: rules` (no-cluster step 11, #298), GitLab (#296), Containers in
  one repository (#270).

## Done when

- A tier-1 test drives one Task through the lead runtime on the ports'
  fake. It goes from a `status/ready` issue to a pull request waiting for
  a person, then merged and closed, with the sessions played by a test
  that posts their markers (step 6).
- The operator's envtest specs pass unchanged with the Crew tick and the
  host side of the Task tick on the engine (steps 1 and 5).
- A person walks one Task in Claude Code on a laptop and one in a Claude
  Code cloud session, with the host's pages as evidence (step 11).

## Not tested, and who can

Nothing here was run against Claude Code or a cloud session.

- That a Claude Code subagent started in the background can call MCP tools
  the main session's `.mcp.json` registers, and runs its own `Bash` in a
  worktree outside its working directory. Step 11's walk shows it, and
  step 7 reads Claude Code's subagent documentation first.
- That a Claude Code cloud session may push a `task/` branch. A cloud
  session's Git access may be limited to the branches its environment
  allows. Step 11's walk on the web settles it, and if pushes are limited
  the guide says how the person allows them.
- That `api.github.com` takes `THEOS_GIT_TOKEN` from a cloud session's
  network proxy as it does on a laptop. Step 11's walk shows it.
