# Design Team (v5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Crew's optional designer Agent turns `status/design` ideas into `status/ready` issues, wearing the hats of up to three experts the factory picks per repository at kickoff.

**Architecture:** A design is a Task with `spec.kind: Design` that runs one sandbox with `THEOS_ROLE=design`. The designer pushes its output to `design/<issue>` (and the team to `team/<issue>` at kickoff). The operator reads it, checks it with pure functions in `internal/model`, and applies it to Gitea: rewrites the issue, files split-off issues, merges the team, labels it ready. The Crew starts design Tasks next to implement Tasks.

**Tech Stack:** Go 1.26, kubebuilder/controller-runtime, envtest + Ginkgo, Gitea API, `claude -p` in agent sandboxes.

**Spec:** [docs/plans/done/2026-10-03-design-team-design.md](2026-10-03-design-team-design.md) (decisions are numbered; this plan cites them as D1-D22). GitHub issue #19.

## Global Constraints

- **One pull request per task**, from a branch off an up-to-date main (`feat/design-<topic>`), body `Refs #19`, merged with a merge commit once its checks are green. Only Task 11's pull request says `Closes #19`. Every task leaves main working: nothing designs until a Crew sets `spec.designer` (D16).
- `internal/model` and `internal/scaffold` import no Kubernetes, controller-runtime, `net/http` or `api/`; `make lint` enforces it.
- Never hand-edit `config/crd/bases/*`, `config/rbac/role.yaml`, `zz_generated.*`; run `make manifests generate` after touching `api/`.
- `make check` green before each pull request. `make e2e` is manual-only and not a gate; say so in the pull request.
- Limits, verbatim from the spec: at most **5 issues** per idea, counting the idea (D11); at most **3 experts** (D5), at most **10 checks** per profile (D5); at most **one** proposed expert per run (D10).
- Names: label `status/design`; branches `design/<issue>` and `team/<issue>` (D14); team files `docs/agents/team/<name>.md` (D8); guide `docs/agents/designer.md` (D22); role `design`; Task kind `Implement` (default) or `Design` (D15); phases `Designing`, `Designed`.
- Changed templates get a new version in `templates/versions.txt`; tags never move.
- Never use the Fable model; the designer Agent in samples is Opus (`claude-opus-5-5`).
- Conventional Commits, author `JairJosafath <jairjosafath@gmail.com>`, trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A designed issue whose criteria are all ticked** (`- [x]`): `MissingAcceptanceCriteria` accepts it today, so it would go ready with nothing left to do. Expect a refusal. Test in Task 4.
2. **The operator restarts halfway through applying** (two of four split-off issues filed): reapplying must not file duplicates. Test in Task 8.
3. **A person closes the idea while it is being designed:** apply must file nothing and end `Failed`, reason `IssueClosed`. Test in Task 8.
4. **A team path that escapes or nests** (`docs/agents/team/../reviewer.md`, `docs/agents/team/a/b.md`): expect a refusal. Test in Task 4.
5. **The designer writes no `.design/issue.md`, or an empty one:** expect a refusal with a reason the next round can act on, not a crash. Test in Task 4.

---

## File Structure

| File | Responsibility | Task |
| --- | --- | --- |
| `internal/gitprovider/gitprovider.go`, `gitea_agents.go`, `gitea_issues.go` | Four new Provider calls | 1 |
| `internal/controller/fakegit_test.go`, `fakeissues_test.go` | The fake's versions of them | 1 |
| `internal/agentrunner/agent.go`, `run.go`; `internal/model/task.go`; `internal/controller/task_sandbox.go`, `task_actions.go`; `api/v1alpha1/task_types.go` | Run cost on every Task | 2 |
| `api/v1alpha1/task_types.go`, `crew_types.go` | `spec.kind`, phases, `spec.designer` | 3 |
| `internal/model/design.go` (new) | Design output: parse, check, body helpers | 4 |
| `internal/model/task.go` | Design Task steps | 5 |
| `internal/model/crew.go` | Crew picks design issues | 6 |
| `internal/agentrunner/job.go`, `prompt.go`, `git.go`, `run.go`, `agent.go` | Design role in the runner | 7 |
| `internal/controller/task_design.go` (new), `task_controller.go`, `task_actions.go`, `task_issue.go` | Task runs and applies a design | 8 |
| `internal/controller/crew_controller.go` | Crew starts design Tasks | 9 |
| `templates/{go,ts}-standard/...`, `templates/versions.txt`, `config/samples/webshop/factory.yaml` | `designer.md`, v1.2.0 | 10 |
| `docs/design.md`, `docs/architecture.md`, `docs/results-v5.md`, `config/samples/webshop/` (a Crew), `.claude/skills/factory-evaluator/gather.sh` | Docs, sample, evaluator, Done when | 11 |

Tasks 1-6 and 10 are independent and can run in parallel. Task 7 needs 4. Task 8 needs 1, 3, 4, 5 and 7. Task 9 needs 3, 6 and 8. Task 11 comes last.

---

### Task 1: GitProvider: read a branch's files, edit an issue, delete a branch, cross-repo dependencies

**Files:**
- Modify: `internal/gitprovider/gitprovider.go` (interface), `internal/gitprovider/gitea_agents.go` (files, branches), `internal/gitprovider/gitea_issues.go` (issues)
- Modify: `internal/controller/fakegit_test.go`, `internal/controller/fakeissues_test.go`
- Test: `internal/gitprovider/gitea_issues_test.go`, `internal/gitprovider/gitea_agents_test.go`

**Interfaces:**
- Produces:
  - `ReadFiles(ctx context.Context, repo Repository, ref, dir string) (map[string]string, error)`: every file under `dir` at `ref`, recursively, keyed by repository path. A missing ref or dir returns an empty map and no error. More than 20 files returns `ErrTooManyFiles`.
  - `EditIssue(ctx context.Context, repo Repository, number int, title, body string) error`: an empty title keeps the title.
  - `DeleteBranch(ctx context.Context, repo Repository, branch string) error`: a branch that is already gone is not an error.
  - `AddIssueDependencyIn(ctx context.Context, repo Repository, number int, blockerRepo Repository, blocker int) error`. `AddIssueDependency(ctx, repo, number, blocker)` becomes a call to it with `repo` twice.

- [ ] **Step 1: Write failing httptest tests** in the style of `TestAddIssueDependency` (`gitea_issues_test.go:387`):
  - `TestReadFiles`: `contents/docs/agents/team?ref=team/7` lists two files; the result is `{"docs/agents/team/vscode-dx.md": "...", "docs/agents/team/k8s-cel.md": "..."}`. A 404 gives `map[string]string{}` and a nil error.
  - `TestEditIssue`: PATCH `/issues/5` body `{"body":"new"}` when the title is empty, `{"title":"t","body":"new"}` otherwise.
  - `TestDeleteBranch`: DELETE `/branches/design%2F5`; both 204 and 404 return nil.
  - `TestAddIssueDependencyIn`: POST to `owner/server/issues/3/dependencies` with `{"owner":"owner","repo":"extension","index":9}`. An existing dependency on `extension#9` is a no-op, but an existing one on `server#9` is not: the dedupe compares `Repository.FullName` and number.
- [ ] **Step 2: Run them; expect them to fail to compile.** `go test ./internal/gitprovider/ -run 'ReadFiles|EditIssue|DeleteBranch|AddIssueDependencyIn'`
- [ ] **Step 3: Implement the four calls on `*gitea`.** `EditIssue` reuses `editIssue` (`gitea_issues.go:364`). For `ReadFiles`, use the contents API (`GET /repos/{o}/{r}/contents/{dir}?ref=`), recurse into `type: dir`, and decode `content` (base64) for each file.
- [ ] **Step 4: Implement them on `fakeProvider`.** Read from the branches that `pushBranch` writes. Keep issue edits and dependencies in `fakeIssueState`, with cross-repo blockers recorded as `owner/name#n`. Add test helpers `branchFiles(owner, name, branch string) map[string]string` and `branchExists(owner, name, branch string) bool`.
- [ ] **Step 5: Run the tests; expect them to pass.** `go test ./internal/gitprovider/ && make test`
- [ ] **Step 6: Commit and open the pull request.** `feat(gitprovider): read branch files, edit issues, delete branches, depend across repositories`

### Task 2: Record each run's cost on its Task

The spec's Done-when step 6 needs it. It records cost rather than tokens, because cost is what the evaluator already compares (`total_cost_usd`). Every role benefits.

**Files:**
- Modify: `internal/agentrunner/agent.go`, `internal/agentrunner/run.go`, `internal/model/task.go`, `internal/controller/task_sandbox.go`, `internal/controller/task_actions.go` (`endRun`), `api/v1alpha1/task_types.go`
- Test: `internal/agentrunner/agentrunner_test.go`, `internal/controller/task_controller_test.go`

**Interfaces:**
- Produces:
  - `agentrunner.CostReporter interface{ CostMilliUSD() int64 }`, implemented by `*ClaudeAgent`, which adds up `total_cost_usd × 1000` (rounded) over its `Run` calls.
  - `Result.CostMilliUSD int64 `json:"costMilliUSD,omitempty"``, mirrored as `model.RunResult.CostMilliUSD`.
  - `TaskStatus.CostMilliUSD int64 `json:"costMilliUSD,omitempty"``, the sum over all the Task's runs. The printcolumn stays as it is.

- [ ] **Step 1: Write failing tests.**
  - `TestClaudeAgentCost`: decode two JSON outputs with `total_cost_usd` 0.1234 and 0.5; `CostMilliUSD()` is 623.
  - `TestRunReportsCost`: a stand-in agent that implements `CostReporter` with 42 gives `Result.CostMilliUSD == 42`.
  - In envtest, `It("adds each run's cost to the Task, once")`: two sandboxes ending with `costMilliUSD` 100 and 250 give `status.costMilliUSD == 350`, and it stays 350 after further reconciles (`Consistently` for two seconds).
- [ ] **Step 2: Run them; expect failure.** `go test ./internal/agentrunner/ -run Cost` and `make test`
- [ ] **Step 3: Implement.** `ClaudeAgent.Run` already decodes the JSON output (`agent.go:40`); also read `total_cost_usd`. `ClaudeAgent` methods move to a pointer receiver if needed. `Run` (run.go) sets `result.CostMilliUSD` before returning, from any agent that implements `CostReporter`. `runState` copies it, and `endRun` adds it once per sandbox. Guard with the sandbox name so a re-reconcile doesn't count it twice; the implementer picks how, for example remembering the last counted sandbox in status. Run `make manifests generate`.
- [ ] **Step 4: Run the tests; expect them to pass.** `make test`
- [ ] **Step 5: Commit and open the pull request.** `feat(task): record each run's cost on its Task`

### Task 3: API: Task kind and design phases, Crew designer

**Files:**
- Modify: `api/v1alpha1/task_types.go`, `api/v1alpha1/crew_types.go`; regenerate with `make manifests generate`
- Test: `internal/controller/task_controller_test.go` (CEL), `internal/controller/crew_controller_test.go` (CEL)

**Interfaces:**
- Produces:
  - `type TaskKind string`, Enum `Implement;Design`, with consts `TaskKindImplement`, `TaskKindDesign`. Field `Kind TaskKind `json:"kind,omitempty"``: `+kubebuilder:default=Implement`, `+optional`, field-level `self == oldSelf` ("kind is immutable").
  - Struct-level rules on `TaskSpec`:
    - `self.kind != 'Design' || has(self.issue)` ("a design Task works on an issue")
    - `self.kind != 'Design' || !has(self.reviewerRef)` ("a design Task has no reviewer")
    - `self.kind != 'Design' || !has(self.allowedProtectedPaths)`
  - The TaskPhase Enum gains `Designing;Designed`, with consts `TaskDesigning`, `TaskDesigned`. `isFinal` (`task_controller.go:329`) treats `Designed` as final.
  - `CrewSpec.Designer *LocalRef `json:"designer,omitempty"``, documented as "turns status/design ideas into ready issues; without it the Crew leaves them alone". A `Designer` printcolumn.

- [ ] **Step 1: Write failing envtest specs.**
  - `It("rejects a design Task with a reviewer")`: Create returns an error containing "a design Task has no reviewer".
  - `It("rejects a design Task without an issue")`.
  - `It("rejects changing a Task's kind")`.
  - `It("accepts a Crew with a designer")`.
- [ ] **Step 2: Run them; expect failure.** `make test`
- [ ] **Step 3: Add the fields and markers, then `make manifests generate`.**
- [ ] **Step 4: Run the tests; expect them to pass.** `make test`
- [ ] **Step 5: Commit and open the pull request.** `feat(api): Task kind Design, design phases and the Crew's designer`

### Task 4: Model: the design output, parsed and checked

**Files:**
- Create: `internal/model/design.go`, `internal/model/design_test.go`
- Modify: `internal/model/crew.go` (`LabelDesign` in `StatusLabels()`)

**Interfaces:**
- Produces, in `internal/model`:
  - Labels and limits:
    - `LabelDesign = "status/design"`, exclusive, color and description in the style of the other status labels.
    - `DesignDir = ".design/"`, `TeamDir = "docs/agents/team/"`.
    - `MaxDesignIssues = 5`, `MaxExperts = 3`, `MaxExpertChecks = 10`.
  - Names: `DesignBranch(issue int) string` gives `"design/<n>"`; `TeamBranch(issue int) string` gives `"team/<n>"`.
  - Types:
    - `type Design struct{ Title, Body string; Pieces []DesignPiece; Expert *FollowUp; Question string }`
    - `type DesignPiece struct{ Number int; Title, Body, Repo string; Depends []int }`. Number 2-5 comes from the file name; `Depends` 1 means the idea.
    - `type DesignChecks struct{ Kickoff bool; Team map[string]string; Related []string }`
  - Functions:
    - `func ParseDesign(files map[string]string) (Design, error)`
    - `func CheckDesign(d Design, c DesignChecks) string` returns "" if the design is fine, else a reason the designer can act on.
    - `func DesignedFrom(repoFullName string, issue int) string` returns `"Designed from <owner>/<repo>#<n>."`
    - `func DesignedBody(designed, original string) string`: `designed`, then `## Original idea` with `original` quoted line by line (`> `), per D13.

Output contract (the runner's prompt in Task 7 states it verbatim):
- `.design/issue.md`: optional front matter `title:` (a new title for the idea); the body is the rewritten issue.
- `.design/issues/NN-<slug>.md`, with `NN` from `02` to `05`. Front matter: `title:` (required), `repo:` (a Container name; empty means this repository), and `depends:` (comma-separated: `1` for the idea, or the `NN` of a lower-numbered piece).
- `.design/expert.md` (optional): front matter `title:`, plus a body. It becomes the proposed "Add an expert" issue.
- `.design/question.md` (optional, D20): a question only a person can answer, such as product direction. When it is there, nothing else is needed or applied.
- Front matter is `---` lines around `key: value` lines; no YAML library.

- [ ] **Step 1: Write failing table tests.** `TestParseDesign`:
  - "the idea alone"
  - "a title from front matter"
  - "two pieces with dependencies"
  - "an expert proposal"
  - "a question" (only `.design/question.md`)
  - "a piece without a title is an error"
  - "a piece numbered 06 is an error"
  - "unknown files under .design/ are an error"

  `TestCheckDesign` (want reason substring):
  - "a good design" → ""
  - "a question alone" → "" (no issue needed)
  - "no .design/issue.md" → "write .design/issue.md"
  - "an empty issue" → "write .design/issue.md"
  - "the idea without acceptance criteria" → "Acceptance criteria"
  - "criteria all ticked" → "an unticked"
  - "a piece without criteria" → "piece 02"
  - "six issues" → "at most 5 issues"
  - "a piece depending on a later piece" → "depends on 03"
  - "a piece for an unrelated repository" → "not related"
  - "a team outside kickoff" → "only at kickoff"
  - "kickoff without a team" → "at least one expert"
  - "four experts" → "at most 3 experts"
  - "a profile with 11 checks" → "at most 10 checks"
  - "a profile missing ## Sources" → "## Sources"
  - "a team path that escapes" (`docs/agents/team/../reviewer.md`) → "docs/agents/team/<name>.md"
  - "a nested team path" → "docs/agents/team/<name>.md"

  `TestDesignedBody` quotes a two-line idea under `## Original idea`. `TestStatusLabelsIncludeDesign` checks the label is exclusive.
- [ ] **Step 2: Run them; expect them to fail to compile.** `go test ./internal/model/ -run 'Design|StatusLabels'`
- [ ] **Step 3: Implement `design.go`.** `CheckDesign` uses `MissingAcceptanceCriteria` and also requires at least one unticked item (`- [ ]`). A team path must match `^docs/agents/team/[a-z0-9-]+\.md$`. A profile needs `## Sources`, `## References` and `## Checklist` headings, and its checks are the list items under `## Checklist`.
- [ ] **Step 4: Run the tests; expect them to pass.** `go test ./internal/model/ && make lint`
- [ ] **Step 5: Commit and open the pull request.** `feat(model): parse and check a designer's output`

### Task 5: Model: the design Task's steps

**Files:**
- Modify: `internal/model/task.go`
- Test: `internal/model/task_test.go`

**Interfaces:**
- Produces:
  - `TaskDesigning = "Designing"`, `TaskDesigned = "Designed"`, `RoleDesigner = "design"`, `TaskKindDesign = "Design"`.
  - `TaskFacts.Kind string` ("" means implement) and `TaskFacts.DesignRefusal string`.
  - `ActionStartDesign`, `ActionApplyDesign`.

Behaviour of a design Task, in `nextTaskStep`:
- `Pending` with the Container Ready → `ActionStartDesign`.
- `Designing`: a run in flight → wait, as for implementing. Unavailable → `ActionAgentUnavailable`. A run error, or a `DesignRefusal`, → `roundFailed(f, reason)`: `ActionRetry` with the reason as feedback, or `ActionFail` at `MaxRounds`. Otherwise → `ActionApplyDesign`.
- `Designed`/`Failed` → `ActionNone`.
- `NextTaskStep` turns `ActionStartDesign` into a wait under `NoFreeSlot` or `AgentBackIn`, exactly as for `ActionStartImplement`.

- [ ] **Step 1: Write failing cases** in `TestNextTaskStep` (Kind `Design`):
  - "a design starts when the Container is Ready" → `ActionStartDesign`
  - "a finished design is applied" → `ActionApplyDesign`
  - "a refused design is a failed round" → `ActionRetry` with feedback == refusal
  - "a refused design in the last round fails" → `ActionFail`
  - "a designer's usage limit waits" → `ActionAgentUnavailable`

  Add one case to `TestNextTaskStepQueuesOnlyForSlots`: a design start waits for a slot.
- [ ] **Step 2: Run them; expect failure.** `go test ./internal/model/ -run NextTaskStep`
- [ ] **Step 3: Implement it as a `Kind == TaskKindDesign` branch at the top of `nextTaskStep`, reusing `roundFailed` and `agentUnavailable`.**
- [ ] **Step 4: Run the tests; expect them to pass.** `go test ./internal/model/`
- [ ] **Step 5: Commit and open the pull request.** `feat(model): steps of a design Task`

### Task 6: Model: the Crew picks design issues

**Files:**
- Modify: `internal/model/crew.go`
- Test: `internal/model/crew_test.go`

**Interfaces:**
- Produces:
  - `Issue.Design bool`, `CrewTask.Design bool`, `IssueStart.Design bool`.
  - `CrewFacts.Designer bool` (the Crew names a designer) and `CrewFacts.Team bool` (main has `docs/agents/team/`).
  - `DesignTaskName(container string, issue, attempt int) string` returns `<container>-<n>-design`, plus `-<attempt>` from attempt 2.

Rules (D16-D18):
- Without `Designer`, design issues appear nowhere in the step: not in Ready, Waiting, NotReady or Start.
- Design issues skip `MissingAcceptanceCriteria` and never wait for `OpenBlockers`.
- Attempts for an issue are counted separately for design and implement Tasks. At `MaxIssueAttempts` design attempts, the issue goes to NotReady with the reason "designed 3 times without a result".
- They share the order (`startRank`, `holdLaterReleases`) and the `parallel` places with ready issues.
- Without `Team`, at most one design Task is active or starting at a time.

- [ ] **Step 1: Write failing cases** in `TestNextCrewStep`, with a helper `idea(n int) Issue`:
  - "design issues wait for a designer" (Designer false → empty step)
  - "an idea starts a design Task" → `Start[0].Design`, Task `orders-api-4-design`
  - "an idea doesn't wait for its dependencies"
  - "an idea needs no acceptance criteria"
  - "ideas and ready issues share the places"
  - "before kickoff only one design runs" (Team false, two ideas, parallel 2 → one start)
  - "after kickoff designs run in parallel"
  - "three failed designs hand the idea back"
  - "an implement Task's failures don't count against the design"
- [ ] **Step 2: Run them; expect failure.** `go test ./internal/model/ -run NextCrewStep`
- [ ] **Step 3: Implement it in `NextCrewStep`.**
- [ ] **Step 4: Run the tests; expect them to pass.** `go test ./internal/model/`
- [ ] **Step 5: Commit and open the pull request.** `feat(model): the Crew picks status/design ideas`

### Task 7: Runner: the design role

**Files:**
- Modify: `internal/agentrunner/job.go` (`RoleDesign = "design"`, validation at l.186), `prompt.go`, `git.go`, `run.go`, `agent.go` (`FakeAgent`)
- Test: `internal/agentrunner/agentrunner_test.go`

**Interfaces:**
- Consumes: `model.DesignDir`, `model.TeamDir`, `model.TeamBranch`, `model.MaxDesignIssues`, `model.MaxExperts`, `model.MaxExpertChecks` (Task 4).
- Produces:
  - `roleGuides[RoleDesign] = "docs/agents/designer.md"`.
  - `Workspace.Team map[string]string`: the profiles on main, keyed by path.
  - `func DesignPrompt(job Job, ws Workspace) string`.
  - `(*repo).showBaseDir(ctx context.Context, base, dir string) map[string]string`.

Behaviour:
- **Clone.** For the design role, the work branch always starts from `origin/<base>`, even if `design/<issue>` exists from an earlier round. The push is a force push.
- **Prompt.** `DesignPrompt` holds:
  - the goal (the idea issue) and the feedback;
  - the C4 context and related repositories (`writeContext`, `writeRelated`);
  - the guide (`writeGuide`);
  - the team's profiles, or, when `ws.Team` is empty, the kickoff instructions (at most `MaxExperts` profiles, each with `## Sources`, `## References`, `## Checklist` of at most `MaxExpertChecks` items, under `TeamDir`; the related repositories' teams may be reused);
  - the research rule ("look up the platform's guidelines and two or three reference products on the web before writing; cite their links in the Design brief");
  - Task 4's output contract, verbatim, with the limits from the model constants;
  - "change no other file".
- **After the agent runs:**
  - Changed paths may only be under `DesignDir`, plus `TeamDir` when `ws.Team` was empty. Anything else gives `Result.Error` "the designer may only write .design/ (and docs/agents/team/ at kickoff), not <path>".
  - Team paths go out with `proposeBranch(ctx, base, model.TeamBranch(job.Issue), teamPaths, "docs(agents): the team for <container>")`, then `restorePaths`.
  - `.design/` is committed and force-pushed to `job.Branch`.
  - No `.design/issue.md` gives `Result.Error` "no .design/issue.md written".
- **The stand-in.** `FakeAgent` with the design role writes a minimal valid `.design/issue.md`, with one unticked criterion, so the Kind stand-in can run a design.

- [ ] **Step 1: Write failing tests:**
  - `TestJobFromEnvAcceptsDesign`
  - `TestDesignPromptCarriesTeam`: two profiles appear by name; no "kickoff" text
  - `TestDesignPromptKickoff`: an empty team gives the kickoff text with "at most 3"
  - `TestDesignPromptContract`: it contains `.design/issue.md`, `depends:` and "at most 5 issues"
  - `TestDesignRunPushesDesignBranch`: a scripted agent writes `.design/issue.md`; origin's `design/7` has it and nothing else changed
  - `TestDesignRunKickoffPushesTeam`: `team/7` holds only `docs/agents/team/*.md`, and `design/7` has no team files
  - `TestDesignRunRefusesOtherPaths`: writing `main.go` gives an error naming `main.go`
  - `TestDesignRunStartsFromBase`: a stale `design/7` on origin is replaced

  Reuse the local-origin helpers already in `agentrunner_test.go`.
- [ ] **Step 2: Run them; expect failure.** `go test ./internal/agentrunner/`
- [ ] **Step 3: Implement it.** Add a `design(ctx, job, agent, r, ws, result)` beside `implement`/`review`, and dispatch on `job.Role` in `Run`. `showBaseDir` uses `git ls-tree -r --name-only origin/<base> -- <dir>`, then `showBase` for each file.
- [ ] **Step 4: Run the tests; expect them to pass.** `go test ./internal/agentrunner/ && make lint`
- [ ] **Step 5: Commit and open the pull request.** `feat(agentrunner): the design role writes .design/ and, at kickoff, the team`

### Task 8: Controller: a design Task runs and applies its design

**Files:**
- Create: `internal/controller/task_design.go`, `internal/controller/task_design_test.go`
- Modify: `internal/controller/task_controller.go` (`runRole`, `facts`), `task_actions.go` (`apply`, `startRun`, `ActionRetry`), `task_issue.go` (`announce` wording for a design)

**Interfaces:**
- Consumes: Tasks 1, 3, 4, 5 and 7.
- Produces: `func (r *TaskReconciler) applyDesign(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container) (ctrl.Result, error)` and `func (r *TaskReconciler) designFacts(ctx, provider, repo, task, status, container) (refusal string, err error)`.

Behaviour:
- **`runRole`:** `TaskDesigning` maps to `model.RoleDesigner`. `forRole` returns the Task's `agentRef` Agent for the design role.
- **`startRun` for a design:** phase `Designing`; branch `model.DesignBranch(issue)`; no protected or allowed paths in the sandbox. `announce` comments "<agent> designs this issue".
- **`ActionRetry`:** sets the phase back to `Designing` for a design Task.
- **`facts`:** a finished design run with no error fills `DesignRefusal` from `designFacts`, which works as follows:
  - It reads `.design/` on `design/<n>`.
  - Kickoff means `TeamDir` is empty on main. At kickoff it also reads `TeamDir` on `team/<n>`.
  - It runs `ParseDesign` and then `CheckDesign`, with `Related` set to the Container's related Container names. A parse error is the refusal.
- **`applyDesign`, in order and safe to repeat:**
  1. `GetIssue`. If it is closed: finish `Failed`, reason `IssueClosed`, with nothing filed. If the design has a `Question`: `handBack` with the question (`status/needs-human`), then finish `Failed`, reason `NeedsHuman`, with nothing filed.
  2. `EditIssue` with the title and `DesignedBody(body, original)`. Skip it if the body already has `## Original idea`: an earlier apply rewrote it, and quoting it again would lose the person's text.
  3. For each piece, in the target repository (its own, or a related Container's repository via `gitRepo`), look among the open issues for one with the same title whose body has `DesignedFrom(...)`. If there is none, `CreateIssue` with the body plus that line, label `status/ready`, and the idea's milestone (`EnsureMilestone` by title, or none).
  4. `AddIssueDependencyIn` for every `depends` entry.
  5. Expert proposal: the same search-then-create, as `status/proposed` in the `Later` milestone.
  6. At kickoff: `OpenPullRequest(team/<n> → main)`. If any `PullRequestFiles` path is outside `TeamDir`, fail with the paths. Otherwise `ForceMergePullRequest`. Skip all of this if main already has a team.
  7. `CommentIssue` naming the team's profiles and the issues filed. `DeleteBranch(design/<n>)`. `RemoveIssueLabel(in-progress)`, `AddIssueLabel(ready)`.
  8. Finish with phase `Designed`, reason `Designed`, and a message listing the issues.

- [ ] **Step 1: Write failing envtest specs** in `task_design_test.go`. Create the Task directly with Kind `Design`, push the designer's files to the fake with `pushBranch`, and end the sandbox with a termination message `{"role":"design","summary":"..."}`:
  - `It("rewrites the idea, files its pieces and marks it ready")`: the body has `## Original idea`, two pieces are `status/ready` with dependencies on the idea, `design/5` is deleted, and the phase is `Designed`.
  - `It("merges the kickoff's team and comments it")`: a forced merge of `team/5` is recorded, and the comment names both profiles.
  - `It("files a piece in a related Container's repository")`.
  - `It("sends a refused design back with the reason")`: an issue without criteria gives round 2 with feedback containing "Acceptance criteria".
  - `It("files nothing twice when applied again")`: run apply twice (for example by resetting the phase), and the open issue count is unchanged. (Review Focus 2.)
  - `It("files nothing for a closed idea")`: phase `Failed`, reason `IssueClosed`. (Review Focus 3.)
  - `It("refuses a team pull request with other files")`.
  - `It("hands a question back to a person")`: the issue is `status/needs-human` with the question as a comment, and nothing else is filed.
- [ ] **Step 2: Run them; expect failure.** `make test`
- [ ] **Step 3: Implement `task_design.go` and the wiring.** Use `gitCall` for every provider call, as `task_issue.go` does.
- [ ] **Step 4: Run the tests; expect them to pass.** `make lint-fix && make test`
- [ ] **Step 5: Commit and open the pull request.** `feat(controller): a design Task applies its design to Gitea`

### Task 9: Controller: the Crew starts design Tasks

**Files:**
- Modify: `internal/controller/crew_controller.go`
- Test: `internal/controller/crew_controller_test.go`

**Interfaces:**
- Consumes: Tasks 1, 3, 6 and 8.

Behaviour:
- **The Agent check** (l.105) includes `Designer` when it is set.
- **When `Designer` is set,** the Crew also lists `status/design` issues (with `Design: true`) and sets `facts.Designer`. `facts.Team` is true when `ReadFiles(repo, "main", model.TeamDir)` is non-empty.
- **`crewTask` maps `Design` from `Spec.Kind`.**
- **A design start creates a Task** named `DesignTaskName(...)`, with `Kind: Design`, `AgentRef: *crew.Spec.Designer`, no `ReviewerRef`, and the same labels as other Tasks.

- [ ] **Step 1: Write failing envtest specs:**
  - `It("designs an idea, then implements what it became")`: an idea labelled `status/design` gets a design Task. After its apply, the Crew starts `orders-api-<n>` on the now-ready issue.
  - `It("leaves ideas alone without a designer")`: no design Task after two poll intervals.
  - `It("runs one design at a time before kickoff")`.
- [ ] **Step 2: Run them; expect failure.** `make test`
- [ ] **Step 3: Implement it.**
- [ ] **Step 4: Run the tests; expect them to pass.** `make test`
- [ ] **Step 5: Commit and open the pull request.** `feat(controller): the Crew turns ideas into design Tasks`

### Task 10: Templates: `designer.md` in the Standards (v1.2.0)

**Files:**
- Create: `templates/go-standard/docs/agents/designer.md`, `templates/ts-standard/docs/agents/designer.md` (identical; the designer is language-neutral)
- Modify: both `AGENTS.md.tmpl` (role list), both `.gitea/ISSUE_TEMPLATE/task.md` (the top comment mentions `status/design` for a short idea), `templates/versions.txt` (`go-standard v1.2.0`, `ts-standard v1.2.0`), `config/samples/webshop/factory.yaml` (both refs `v1.2.0`), `docs/architecture.md` l.162

**`designer.md` decides the craft, not the format** (the format lives in the prompt, Task 7). It has these sections:
1. **Research first:** the platform's official guidelines, plus two or three widely used products that solve the same thing. Read them, compare them, and link them in "How others do it" and "Guidelines followed".
2. **Wear only the hats the idea touches,** and name them.
3. **Acceptance criteria a reviewer can check** by a test or a screenshot. Turn quality limits into criteria (for example "the hover's first line is the type").
4. **Native patterns first:** use the platform's own surfaces before inventing.
5. **Small issues:** each one fits one Task. Split instead of growing; say what is out of scope.
6. **Ask a person only about product direction,** never about what research can answer.
7. **Kickoff:**
   - Experts fit the Container's platform and the system's domain, from the C4 context.
   - A profile is sources, references and a checklist, and its title is only a label.
   - Reuse a related repository's domain profile when it fits.

- [ ] **Step 1: Write the two files and the template edits; bump the versions.**
- [ ] **Step 2: Verify.** `make test`; the envtest loads every `versions.txt` line. Also run `grep -rn "go-standard\|ts-standard" config/samples docs/architecture.md` and check that it shows v1.2.0.
- [ ] **Step 3: Commit and open the pull request.** `feat(templates): go-standard and ts-standard v1.2.0 ship the designer's guide`

### Task 11: Docs, sample, evaluator and the Done-when run

**Files:**
- Modify: `docs/design.md` (a "v5: a design team per Container" section after v4, linking the spec and this plan), `docs/architecture.md` (the design flow and `task_design.go`), `docs/dev/api-and-controllers.md` (any new log lines), `config/samples/webshop/` (a Crew with a `designer` Agent, model `claude-opus-5-5`), `.claude/skills/factory-evaluator/gather.sh` (sum `status.costMilliUSD` per Task kind)
- Create: `docs/results-v5.md`, `docs/screenshots/v5-*`

- [ ] **Step 1: Docs, sample and evaluator edits.** `make check`
- [ ] **Step 2: Deploy to Kind** with the `factory-ops` skill: rebuild the agent image (runner changed) and the operator, run the seed (templates v1.2.0), and apply the Crews sample.
- [ ] **Step 3: As a person (c4admin), add `docs/agents/designer.md` from the Standard to the two CEL Lens repositories** (D22), through a pull request merged by c4admin.
- [ ] **Step 4: File "Hovers show too much text" as `status/design` in the CEL Lens server repository.** Take a screenshot of the current hover first (`docs/screenshots/v5-hover-before.png`).
- [ ] **Step 5: Check the spec's Done when, items 3-6.**
  - The team is on main with at most 3 profiles, and is commented on the issue.
  - The issue is ready, with a Design brief citing the VS Code guidelines and at least 2 reference extensions, and checkable criteria.
  - A linked issue exists in the extension repository.
  - Both are merged by the Crews, with `v5-hover-after.png`.
  - `kubectl get tasks -o custom-columns=NAME:.metadata.name,KIND:.spec.kind,COST:.status.costMilliUSD` is saved to `docs/screenshots/v5-costs.txt`.
- [ ] **Step 6: Write `docs/results-v5.md`:** what happened, what it cost, findings and fixes. Report failures honestly.
- [ ] **Step 7: Commit and open the pull request with `Closes #19`.** `docs: v5 design team, results and evidence`
