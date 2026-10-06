# Briefs: an app idea in plain words

**Goal:** A person describes an app in plain words, and later steers it with
follow-up prompts; a planner Agent turns that into the model (Project, Persons,
SoftwareSystems, Containers, DataStores, Relationships), a Crew per new
Container and the first issues, and says in plain words what it chose. Nobody
writes the model's YAML, runs kubectl or files issues through curl. Asked for
in GitHub issue #240 (cel-lens, by gray-lenovo); the maintainer's decision is
on that issue, and [docs/design.md](../../design.md#briefs-an-idea-in-plain-words)
holds the rules.

**Architecture:** A new namespaced kind, `Brief` (`spec.idea`, append-only
`spec.prompts`). Each round (the idea, then each batch of new prompts) is a
Task with `spec.kind: Plan` owned by the Brief, run in an agent sandbox with
`C4_ROLE=plan`. The planner works in the Brief's own Gitea repository, where
the operator writes the brief, the current model and the installed catalog,
and pushes its plan to `plan/<round>`. The operator reads the plan, checks it
with pure functions in `internal/model`, applies it, makes the Crews, files the
issues, records it all in `Brief.status` and merges the plan into the
repository's `main`, which keeps every round's decisions. The planner never
touches the Kubernetes API: the sandbox has no token, as for every Task.

**Done when:** `make e2e` takes a Brief through the whole loop on Kind with
the stand-in planner, without a Claude token: the Brief ends `Planned`, the
namespace holds the model and a Crew per new Container, every Container is
`Ready`, each has an issue its Crew picks up, and the Brief's status lists
what it created and why. A follow-up prompt ends a second round that changes
no scaffolded Container and files an issue for it. The stand-in's plan passes
`CheckPlan` for any catalog with a Framework (`make test`). The cel-lens
sample and getting-started's short path show a person the real planner; how
well Claude plans their idea is their run, not this plan's gate (step 8).

## Decisions

Numbered so steps and reviews can cite them (B1-B17). The maintainer's
decision on #240 set B1, B3, B9 and B10; the rest fill in what it left open.

| # | Topic | Decision | Why |
| --- | --- | --- | --- |
| B1 | Front door | A namespaced kind `Brief`; anything can write it: the UI's box, kubectl, GitOps | The CRDs are the truth; the front door is one more writer of the model |
| B2 | Spec | `idea` (required, immutable, at most 4000 characters), `prompts` (append-only list, at most 50, each at most 2000 characters), `agents` (planner, implementer, reviewer, designer; LocalRefs defaulting to `planner`, `implementer`, `reviewer`, `designer`), `gitProviderRef` (default `gitea`). CEL keeps earlier prompts unchanged | A person writes one sentence; the defaults match the Agents the chart makes (B14) |
| B3 | Who plans | A Task with `spec.kind: Plan` and `spec.briefRef` (in place of `containerRef`; exactly one of them, by CEL), owned by the Brief, role `plan` | Reuses the Task's sandbox, waiting (`AgentUnavailable`, `Interrupted`), agent slots, cost, history and the Factory board, instead of a second run loop |
| B4 | Where it works | The operator makes a Gitea repository `<owner>/brief-<name>` per Brief, with a bot user that can write only it, and keeps on `main`: `brief.md` (idea and prompts, the new ones marked), `model/` (the namespace's story kinds, the installed catalog with each entry's supported types, each Container's repository and open issue titles) and `PLANNER.md` (the guide, embedded in the operator). Only the operator writes `main` | The planner needs files to read and a place to push, and a person can read every round's choices there; it's the Container repositories' fence, unchanged |
| B5 | The plan | The planner pushes `plan/<round>`: `plan/model.yaml` (story kinds), `plan/issues/<nn>-<container>.md` (front matter: title, labels), `plan/choices.md` (one line per choice, in plain words) and, only when stuck, `plan/needs-input.md` (one sentence). The termination message carries a short summary | The design Task's channel (a branch read by the operator), sized for a whole model |
| B6 | What a plan may write | Project (only when the namespace has none), Person, SoftwareSystem, Container, DataStore, Relationship. Nothing else: no GitProvider, Secret, Agent, Crew, catalog entry, Task or Brief | Everything else costs money or touches access (B10) |
| B7 | Checks on a plan | In `internal/model`, pure: names are DNS labels; kinds are B6's; at most 20 objects and 10 issues per round; a Container names catalog entries that exist and support its type, and the Brief's GitProvider; an existing object changes only its story text (`displayName`, `description`, `needs`, `kpis`); nothing is deleted; an issue targets a Container of the namespace or of the plan and has acceptance criteria (`MissingAcceptanceCriteria`) unless it's a design idea for a Crew with a designer; `choices.md` is not empty. A refused plan starts the next round of the same Task with the reasons, up to the planner Agent's `maxRounds` | The planner is checked, not trusted, like every agent |
| B8 | Applying | In order, each step idempotent: Project, Persons, SoftwareSystems, DataStores, Relationships (before the Containers they start from, so each Connector is in the scaffold, not `AddedAfterScaffold`), Containers; then, once each new Container is `Ready`, its Crew and its issues. Every object gets the label `c4.factory.io/brief: <name>`, no ownerReference to the Brief | Deleting a Brief must not delete the product it built; the label says where an object came from |
| B9 | Crews | One Crew per new Container, named after it, with the Brief's implementer, reviewer and designer, `parallel: 1`, releases off; the planner can't change it | Agents' models and the Crew's size cost money |
| B10 | Asking | The Brief's condition `NeedsInput`, phase `NeedsInput`, with one sentence saying what and why, and nothing applied. Set by the operator when an Agent, the GitProvider, its credentials or a catalog entry the plan needs is missing, and by the planner through `needs-input.md`. Fixing it (or a new prompt) starts the round again | The only time it asks the person, as #240 wants |
| B11 | Issues | Filed in the Container's repository, `status/ready` (or `status/design` once the v5 designer exists and the Crew has one), in no milestone; each body ends with a marker `<!-- c4-brief: <brief>/<round>/<n> -->`, found again before filing, so a restart files no duplicate | v5's designer is not built yet, so the planner writes ready issues with acceptance criteria; the marker is the bumps' pattern |
| B12 | Follow-ups | Each prompt appended while the Brief is idle starts a round; prompts appended during a round wait for the next, which takes them all. Scaffold once holds: a change to a scaffolded Container is an issue in its repository, a new Container is added and scaffolded, a new Relationship from a scaffolded Container shows `AddedAfterScaffold` and its wiring is an issue | The design's rules don't bend for the planner |
| B13 | Status | `phase` (`Pending`, `Planning`, `Applying`, `Planned`, `NeedsInput`, `Failed`), `round`, `promptsPlanned` (how many prompts the last applied round took), `choices` (the last round's, plain words), `created` (kind and name), `issues` (repository, number, URL, title), `history` (one entry per round: summary, choices, cost; newest 20), `repositoryURL`, `costMilliUSD`, conditions `Planned` and `NeedsInput` | The person reads what it chose without opening Gitea |
| B14 | Install | The chart makes, for each project in `gitea.projects`, the catalog entries of the templates the seed pushed and four Agents (`planner` and `designer` on Opus, `implementer` on Sonnet, `reviewer` on Opus) reading Secret `claude-credentials`, each part switchable off in the values. The person still makes that Secret | A plan can only use what is installed, and the token is the person's to give |
| B15 | The UI | The UI server may write exactly two things: create a Brief, and append to a Brief's `spec.prompts` (a JSON patch `add` at `/spec/prompts/-`). Everything else stays GET only; its RBAC grants `create` and `patch` on `briefs` and nothing more. The UI shows the Brief's phase, choices, created objects, issues and the `NeedsInput` sentence | #240 asks for a text box, not kubectl; the rules stay in the CRDs, the UI enforces nothing |
| B16 | Deleting | A Brief's deletion deletes its Tasks and its repository's bot, keeps its repository and the model it made | The product outlives how it was asked for |
| B17 | The first Project | The chart's seed creates Project `<ns>` (display name the namespace) in each of `gitea.projects` when the namespace has none, and never changes or deletes one; a plan then changes only its story text | The UI takes a Brief only in a namespace that holds a Project (B15), and a fresh install had none. Not a Helm object: a hook would be recreated on every upgrade and an ordinary template deleted on uninstall, and either would cascade to the whole model. Added in step 8 |

## Steps

One issue and one pull request each, from an up-to-date main. Every step
leaves main working: nothing plans until step 5 lands. Each issue says
`Part of #240`; step 8's pull request also says `Closes #240`.

| Step | Issue | What | Depends on | Cluster |
| --- | --- | --- | --- | --- |
| 1 | #275 | API: the `Brief` kind (`kubebuilder create api`), B2's spec and CEL, B13's status, a controller that only sets `Pending`; envtest for the CEL rules | none | no |
| 2 | #276 | Model: `internal/model/brief.go`, parse and check a plan (B5, B6, B7), the round's prompts (B12), the issue marker (B11); table tests for every refusal | none | no |
| 3 | #277 | API and runner: Task `spec.kind` (`Implement` default, `Plan`; immutable) and `spec.briefRef` with CEL (exactly one parent; a Plan Task has a brief and no reviewer); `C4_ROLE=plan` in `internal/agentrunner`: clone the brief repository, run the planner with `PLANNER.md`, push `plan/<round>` | 1 | no |
| 4 | #278 | Workspace: the operator makes the brief repository and its bot (B4), writes `brief.md`, `model/` and `PLANNER.md` to `main` before each round; fake provider and envtest | 1, 2 | no |
| 5 | #279 | Controller: the Brief starts a Plan Task per round, reads and checks the plan, applies it (B8), makes Crews (B9), files issues (B11), sets status (B13) and `NeedsInput` (B10), merges the plan; envtest with the fake provider, including a restart halfway through applying | 2, 3, 4 | no |
| 6 | #280 | Chart: B14's catalog entries and Agents per project; `make validate-yaml` | none | no |
| 7 | #281 | UI: the box, the prompt field and the Brief's status (B15); server tests for the two writes and that every other write is still refused | 1 | no |
| 8 | #282 | Sample and docs: `config/samples/brief/cel-lens.yaml`, getting-started's short path, design.md's status line. The stand-in planner plans from the catalog, and an e2e spec takes a Brief through two rounds on Kind without a token (below) | 5, 6, 7 | yes |

## Step 2: the plan's format

What B5 and B7 leave open, settled in step 2 (`internal/model/brief.go`):

- **`plan/model.yaml` is the whole story model the round leaves,** as
  manifests (`apiVersion: c4.factory.io/v1alpha1`, `kind`, `metadata.name`,
  `spec` as in the CRDs), one per document: what the namespace holds, changed
  or not, and what the round adds. A deletion is an object of the namespace
  left out, and is refused. B7's 20 counts the objects the round creates or
  changes, not the unchanged ones, so a big namespace can still be planned.
  A story kind's spec is read strictly: a field the kind doesn't have is a
  parse error naming the file and document.
- **An issue file** is `plan/issues/<nn>-<container>.md`: front matter between
  `---` lines with `title` (required) and `labels`, then the body. `nn` is the
  issue's number in the round (B11's marker), unique in the plan.
- **A design idea** is an issue labeled `status/design`; it needs no
  acceptance criteria when its Container's Crew has a designer: an existing
  Container's Crew names one, or, for a new Container, the Brief does (B9).
- **A plan with `needs-input.md`** may leave out everything else; the
  controller checks a plan only when it has none.
- **Refusals** carry a subject (`Kind/name` or the file) and a reason the
  planner can act on; a refused plan gets them all at once, not the first.
- The operator files an issue's body with the planner's `<!--` escaped, then
  the marker (`BriefIssueBody`), as bump issues do.

## Step 3: Plan Tasks and the planner's run

What B3 and B5 leave open, settled in step 3:

- **A Plan Task's goal is optional.** `brief.md` is the goal; `spec.goal` may
  still say more. A Plan Task has no `issue`, `reviewerRef` or
  `containerRef`, so `allowedProtectedPaths` can't be set either.
  `containerRef` is optional in the schema, and CEL asks for exactly one of
  it and `briefRef`; Go keeps it a value (`omitzero`), so every existing
  reader of `containerRef.name` is unchanged, and the Task-by-Container index
  skips the empty one.
- **The Task controller left a Plan Task alone** until step 5 gave the Brief
  its rounds; step 5 runs it (see below).
- **The runner, `C4_ROLE=plan`,** takes `C4_BRIEF` in place of `C4_GOAL` and
  `C4_CONTAINER`; its branch is `plan/<C4_ROUND>`, and any other `C4_BRANCH`
  is refused. Each run starts from the base branch (`main`), removes `plan/`
  from the working tree (earlier rounds' plans stay in the history), and
  fails without `PLANNER.md` or `brief.md` there. After the agent, the branch
  is one commit on `main` that holds the working tree's `plan/` and nothing
  else, force-pushed, so a rerun of the round replaces it; a run that leaves
  no `plan/` fails and pushes nothing. The summary is cut to 1000 bytes; the
  plan itself is on the branch.
- **The runner doesn't check the plan.** The operator does (B7), and a
  refusal comes back as `C4_FEEDBACK` in the next round.
- **The stand-in planner** (`harness: stand-in`) copies `model/model.yaml` into
  `plan/model.yaml` and writes one choice: a plan that changes nothing, for
  demos and e2e.

## Step 4: the workspace

What B4 leaves open, settled in step 4 (`internal/model/brief_workspace.go`,
`internal/controller/brief_workspace.go`):

- **`model/` is three files:** `model/model.yaml` (the namespace's story
  objects in `plan/model.yaml`'s format, Project first, so the planner copies
  it and edits the copy; a test parses it back), `model/catalog.yaml` (each
  installed entry by side, with its description, `supportedTypes` and a
  Standard's `supportedFrameworks`) and `model/containers.yaml` (each
  Container with a repository: its address and its open issues, `#n title`,
  the first 50 by number and a count of the rest). A Container whose issues
  can't be read is listed with `issuesUnread: true`: one broken repository
  doesn't stop the Brief. Fixed paths, so a write never has to remove a file.
- **`brief.md`** has the idea, marked **new** until a round is in
  `status.history`, every prompt numbered and marked planned or **new**
  (after `status.promptsPlanned`), and the GitProvider every new Container
  names. A prompt's line breaks become spaces so it stays one list item.
- **`PLANNER.md`** is `internal/model/planner.md`, embedded in the operator.
- **The bot** is `model.AgentUsername(<namespace>, brief-<name>)`, made with
  `EnsureAgentAccess` like a Container's; its credentials are in Secret
  `brief-<name>-git`, owned by the Brief, made once (the token isn't rotated
  on every reconcile).
- **Only the operator writes `main`:** its rule (`BranchRule.OperatorPushes`)
  lets the operator's user push and nobody else, set before the bot exists.
  The operator writes with `PushDefaultBranch`, a fast-forward push on the
  head it read, never a force push; an unchanged workspace pushes nothing.
- **When:** in step 4 on every change of the Brief's spec and when a
  GitProvider appears; step 5 calls `prepareWorkspace` before each round.
- **A missing GitProvider or credentials Secret** sets `NeedsInput` naming it;
  the Brief looks again every minute (Secrets aren't watched) and goes back to
  `Pending` once it has what it needs.
- **A Container named `brief-<name>`** would share the Brief's repository
  and bot: the Brief sets `NeedsInput` naming it instead.
- **A changed `gitProviderRef`** makes the repository on the new host: it is
  protected again when `status.repositoryURL` differs, and the bot's Secret
  records the repository it was given access to, so a moved repository gets
  its bot again.
- **Left for step 5:** B16's deletion of the bot user in Gitea (the Secret
  already goes with the Brief; done in #326: a `c4.factory.io/brief-bot`
  finalizer, put on before the bot is made, deletes the user through
  `RemoveAgentAccess`, and lets the Brief go without it when its GitProvider
  or credentials are gone; #335: a host that fails the delete is retried
  with a `BotNotDeleted` Event until the namespace is terminating or ten
  minutes have passed, then the bot is left with a `BotLeft` Event); refusing a plan that creates a Container named
  `brief-*`; and NeedsInput from `needs-input.md`, which the operator's
  "has what it needs" must not clear: step 5 clears only the reasons it set.

## Step 5: rounds, applying and filing

What B3, B8 and B10-B13 leave open, settled in step 5
(`internal/controller/brief_round.go`, `brief_apply.go`, `task_plan.go`,
`internal/model/brief_round.go`):

- **A round is one Plan Task,** `<brief>-plan-<round>`, owned by the Brief,
  labelled `c4.factory.io/brief: <brief>` and `c4.factory.io/brief-round:
  <round>`, its agent the Brief's planner. Its status rounds are the
  planner's tries at that Brief round: `status.round` counts tries, and every
  try pushes to the same `plan/<round>` (`C4_ROUND` is the Brief's round).
- **The Task controller runs a Plan Task** with the Implement Task's machinery
  (`NextTaskStep` with `Plan` set): no Container, protection or queue to wait
  for; the sandbox clones the brief repository with the bot's Secret
  `brief-<name>-git`; agent slots, `AgentUnavailable`, reruns and `maxRounds`
  as for any Task. A try that pushes a plan goes to `Checking`, where it waits
  for the Brief's verdict.
- **The verdict is two annotations on the Task,** written by the Brief:
  `c4.factory.io/plan-checked: <commit>` and, when refused,
  `c4.factory.io/plan-refusals: <reasons>`. The Task controller acts on a
  verdict for its `status.commit` only: accepted ends the Task `Merged`
  ("the Brief accepted the plan"); refused starts its next try with the
  reasons as feedback, or fails it after the planner's `maxRounds`. So the
  Brief never writes a Task's status, and each controller keeps its own.
- **A plan that doesn't parse** is refused like one `CheckPlan` refuses, with
  the parse error as the reason. A plan with `needs-input.md` is accepted
  unchecked and applies nothing.
- **When a round starts:** when no round runs and either none ran yet or
  `spec.prompts` has more than the last round took. The Brief records how many
  it took in `status.roundPrompts` (added to B13) when it starts the round, so
  one appended meanwhile waits for the next; `promptsPlanned` becomes it only
  when the round applies. A round that failed or asked for input is over too:
  the next prompt starts a new round, which takes every prompt not yet planned.
- **The phase is derived,** each reconcile, from the round's Task, the
  history and the Planned condition: no round yet `Pending`; Task running
  `Planning`; Task `Merged`, round not in history `Applying`; round in history
  `Planned`, `NeedsInput` or `Failed` as its outcome left the Planned
  condition (reasons `Applied`, `PlannerNeedsInput`, `RoundFailed`). The
  operator's own NeedsInput (below) shows over any of them while it lasts, and
  clears only its own reasons, never the planner's.
- **The operator's NeedsInput (B10):** before starting a round, a missing
  planner Agent or an empty catalog (no Framework installed); before applying,
  a missing implementer or reviewer Agent, or a catalog entry the plan names
  that has gone; the GitProvider and its Secret as step 4. Nothing is applied
  while it lasts; the Brief looks again each minute and on Agent changes.
- **Applying (B8)** reads the plan again at the Task's commit, in order:
  Project, Persons, SoftwareSystems, DataStores, Relationships, Containers.
  Absent objects are created with the labels `c4.factory.io/brief` and
  `c4.factory.io/brief-round`; present ones get only their story text. Every
  step is idempotent, so a restart applies the rest.
- **Then, per new Container and per Container an issue targets,** once it is
  `Ready` (the round waits for each; a Container that isn't Ready keeps it
  `Applying`, saying which): a new one (labelled with this round) gets its Crew first (B9; an existing Crew of that
  name is left alone), then the round's issues for it, each found by its
  marker among the repository's open issues before it is filed, and recorded
  in `status.issues` right after. Labels: the planner's, with every
  `status/*` replaced by `status/ready`. Crews have no designer yet, so
  `CurrentModel.Designer` is false and a design idea needs acceptance criteria.
- **Merging the plan** is a commit on the brief repository's `main` holding
  `plan/` as on `plan/<round>` (the earlier round's plan files removed), pushed
  by the operator like the workspace; an unchanged `plan/` means it merged.
  Then the round is recorded: history entry, `choices`, `promptsPlanned`,
  `created` (every story object and Crew labelled with the Brief), Planned
  True, Event `Planned`.
- **Events:** `Planned` when a round applies, `PlanRefused` per refused try,
  `NeedsInput` when the Brief starts waiting for the person.
- **Deleting a Brief** deletes its Tasks (owned) and its bot's Secret; the
  model, the Crews and the repository carry no ownerReference to it and stay.
  Deleting the bot user in Gitea needs a provider call that doesn't exist yet,
  filed as its own issue.
- **A plan creating a Container named `brief-*`** is refused (`CheckPlan`).
- **Settled while building it:** a round's start (`round`, `roundPrompts`) is
  written to the status before its Task is made, guarded by the Brief's
  `resourceVersion`, so a stale cache can't start a round twice. Containers
  are created a reconcile after the Relationships that start from them, once
  the cache the Container controller shares shows them, so the Connector is
  in the scaffold. When a round ends the workspace is written again at once,
  and a prompt that waited starts the next round in the same reconcile.
  A plan's labels the factory doesn't define are made in grey (`#ededed`).
- **After review:** while a round runs, `brief.md` lists only the prompts it
  took, so a later try never plans one meant for the next round. The idea
  counts as planned once a round applied (each history entry keeps its
  `outcome`, and the planner's question as `needsInput`, which comes back
  when the operator's own wait ends). Each filed issue keeps its `round` and
  `n`, so one recorded is never filed again, even once closed; the marker
  covers only the window before the status write. Applying runs `CheckPlan`
  again first and fails the round with the reasons when the namespace
  changed. A plan the brief repository doesn't show at the Task's commit
  after 10 minutes in `Checking` is refused ("push it again"). A round start
  that finds the Brief stale writes nothing and reads it again.

## Step 8: the loop on Kind, without a token

The first Done-when ran the real planner on cel-lens: Opus and Sonnet runs
for every try, a different plan each time, and one person's idea as the
factory's test. What the factory owns is the loop: the sandbox runs the
planner, the plan reaches the brief repository, the operator checks and
applies it, Containers scaffold, Crews start and issues are filed. A
scripted planner proves that for everyone, every run, for free, as the
stand-in implementer and reviewer already do for Crews.

- **The stand-in planner plans from the catalog.** `model.StandInPlan` reads
  the workspace the operator wrote. A round that plans the idea adds one
  Container named after the Brief (to the first SoftwareSystem, or a new one
  named after the Brief when there is none). It uses the first Framework in
  the catalog, that Framework's first type, and the first Standard that fits
  both, if any. The round files one issue for that Container. A round of
  prompts changes no object and files one issue per new prompt, for the
  Brief's Container (else the model's first). With no Framework, it writes
  `needs-input.md`. `FakeAgent` runs it for `C4_ROLE=plan`.
- **Tier 1 proves the plan is valid:** for catalogs with and without a
  Standard, for a model with and without a SoftwareSystem, and for a round
  of prompts, `ParsePlan` and `CheckPlan` take the stand-in's plan with no
  refusal.
- **Tier 5 proves the loop:** an e2e spec makes a namespace from
  `test/e2e/testdata/apps/factory.yaml` and a Brief whose three Agents are
  the `stand-in` Agent. It waits for `Planned`, the Container `Ready`, its
  Crew and the filed issue picked up by a Task. Then it adds a prompt and
  waits for round 2, which files an issue and leaves the Container's
  scaffold commit alone. `make e2e` already builds everything it needs.
- **Found on Kind:** the first run's Crew never saw the Brief's issue. The
  Brief filing it and the new Crew's first poll made the repository's labels
  at once, Gitea took each name twice, and then filtered issues by neither.
  `EnsureLabels` now takes turns per repository within the operator
  ([the run](../../screenshots/brief/kind-stand-in-e2e.txt)).
- **Not a gate any more:** the real planner's choices on a real idea. The
  cel-lens sample and getting-started's short path are how a person runs
  that, with their token.

## Review focus

1. **A plan that changes a scaffolded Container's type or catalog refs:**
   refused, and the reason names the field. Step 2.
2. **The operator restarts halfway through applying** (two of four issues
   filed): the round finishes with no duplicate issue or Crew. Step 5.
3. **A prompt appended while a round runs:** that round applies, and the next
   round takes the prompt; none is lost or planned twice. Steps 2 and 5.
4. **A plan naming a GitProvider, Secret or Agent:** refused before anything
   is applied. Step 2.
5. **The UI server asked to patch anything other than `/spec/prompts/-`,** or
   any other kind: refused by the server and by RBAC. Step 7.
