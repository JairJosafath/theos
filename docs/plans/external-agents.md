# An agent outside the cluster fills a Crew spot

GitHub issue #260, written 2026-11-14. The plan, not the build: every step
below is an issue to file. Code is cited as it is on main at 13bf6f6.

## Goal

A Crew spot is filled by an agent the factory does not run: a teammate's
local Claude Code, a cloud session, a hosted agent, or a person. The Crew
hands it a Task, waits for its push, and from there does what it does for
any Task: the pull request, the checks, the review and the merge.

## Read this first

- **The agent needs an account of its own on the Git host,** one that
  does not administer the repository. The factory gives it write access
  to the one repository and takes it away again; its token is made by its
  holder and never enters the cluster. An account that administers the
  repository, or is the operator's, is refused: no rule of the host would
  bind it (decision 7). Its reviews stop counting as a person's (decision
  8): give the agent an account nobody approves with.
- **The agent must reach the Git host, and by default only one on the
  cluster's machine does.** The chart's Gitea answers inside the cluster
  and on a port-forward, and registration is off there
  (`dist/chart/templates/gitea/configmap.yaml:25`). So on the default
  install a person does three things first: runs the port-forward where
  the agent works (it takes a kubeconfig, and there only the agent's own
  runtime keeps the agent from that file), makes the agent's account as
  Gitea's site administrator, whose own account will not do, and hands
  the agent that account's token. An agent anywhere else needs the
  GitProvider's `url` set to a Gitea the user runs at an address it
  reaches, or the chart's Gitea once it has an address (#121), or
  GitHub, which no GitProvider can name before #81 (decision 6).
- **Six things the sandbox gave are gone for that spot:** the fence, the
  model proxy, checkpoints with pause, recorded cost, a credential the
  factory mints and narrows, and a work order nobody else can write. A
  Crew's `taskBudgetUSD` counts nothing of its runs, and the account can
  do on its repository what the host lets any writer do (the loss table).
- **A reviewer and a verifier never run outside,** nor a planner, a judge
  or a Benchmark's Agent (decisions 4 and 5).
- **A silent agent loses its run after an hour.** Twice in a round the
  work is offered again at no cost; then the round fails (decision 3).
- Nothing here is built; steps 3, 4 and 6 wait for #266 and #267.

## The related issues

| Issue | What becomes of it |
| --- | --- |
| #266 the method bundle | kept: the operator posts its work order for the role (no-cluster decision 5) |
| #267 claims | kept, as no-cluster's step 3 reshapes it: its markers, lease and claim table in `internal/model` are the ones used here; step 3 adds rows and five markers in that form, not a protocol |
| #268 `cmd/theos`, #294 `theos mcp` | kept: step 6 teaches `next`, `claim`, `heartbeat`, `done` and `release` to answer an operator's offer (decision 14) |
| #269 hosts, #292, #293, #295 to #300 | kept as filed; nothing here changes them |
| #121 the chart's Gitea has no address outside the cluster | kept: once it has one, an agent anywhere reaches the chart's Gitea; nothing here waits for it |
| #57, #58, #259 | kept: an agent on GitHub waits for #81 (`provider: github`); #58's harnesses run in the sandbox and are entries of the runner's table, where `external` is in the enum and not in that table (step 1); #259, closed since by #301, ships the Standards' `.claude/agents/`, which an outside Claude Code session opened in the Container's worktree finds (no-cluster decision 6) |

## Decisions, and why

1. **`harness: external` means the operator starts nothing and reads the
   run from the Git host.** An Agent with it carries `spec.external`
   (decision 10 lists its fields) and no image, model, credentials Secret,
   resources or env. For its run the operator makes no Sandbox, no pod and
   no Secret of a model credential, leases no test environment and holds
   no agent slot (`MaxSandboxes` caps sandboxes,
   `task_controller.go:123-125`). Why it fits: the next step is decided
   from a `RunState` (started, done, a result, interrupted;
   `internal/model/task.go:196-233`, `:1315`), which today is read from
   the sandbox (`task_controller.go:386`, `task_actions.go:255`). An
   outside run fills the same value from the issue's comments and the
   branch, so the phases, rounds, checks, review and merge after it are
   the ones that exist.
2. **The Git host is the only channel. An outside agent holds nothing of
   the cluster.** Not a Session: a Session's heartbeat is written to the
   API server by a client with a ServiceAccount (design.md, *Sessions*),
   which a cloud session cannot reach, and a cluster credential outside
   the cluster is more than a spot needs. The lease is no-cluster's
   ([no-cluster.md](no-cluster.md), decision 8): marker comments on the
   issue, never edited, ordered by the host's own stamps. The operator
   reads them through the comment contract #267 adds: on main the Issues
   port only writes a comment (`internal/ports/issues.go:105-106`).
3. **A run is an offer, a claim, renewals and an end.**

   | Marker | Whose | What it says |
   | --- | --- | --- |
   | `offer` | the operator's | the work order: the Agent and its account, issue, branch, role, round, the round's feedback, the guides to read, the lease, how to answer |
   | `claim`, `renew`, `release` | the agent's | #267's, unchanged: a session took the run, still holds it, or gives it up |
   | `done` | the agent's | the commit it pushed and a summary; a triager's decision in place of a commit |
   | `blocked`, `nothing` | the agent's | the `Blocked:` and `Nothing to do:` notes of design.md, *Agents and Tasks*, with the same ends |
   | `ended` | the operator's | this run is over; its later markers count for nothing |

   - *The offer.* The operator creates the Task's branch if it is missing
     (`CreateBranch`, `internal/ports/githost.go:403-405`), so the lock is
     the operator's, and posts the work order, rendered from
     `internal/method`. It holds only what the host already shows: the
     model's context is not published. It names the repository by owner
     and name, never by address: the agent's address for the host is its
     own. It mentions the account (decision 14).
   - *Counted markers.* An agent's marker counts only from the Agent's
     account, after the offer and before the run's `ended`. An offer and
     an `ended` count only from the operator's account. A marker is a
     whole comment that begins with its marker line; a comment that
     quotes one is none. The operator never begins a comment with
     another's words: an agent's note or summary goes inside the fence
     issue text gets (`issueBlock`, which #266 moves to `internal/model`).
   - *Unclaimed.* An offer waits, `WaitingForAgent`, at no cost; after one
     lease's length it is a Needs-you item and goes on waiting.
   - *Done.* `done` counts once the commit it names is the branch's head.
     The operator then opens the pull request as it does today, as the
     Container's bot (`task_actions.go:248-262`; decision 12).
   - *Silent.* When the lease and the grace look past, the operator posts
     an `ended` that says so. It counts only if the host's stamp on it is
     later than the last counted `claim` or `renew` by the lease and the
     grace; one that came too early counts for nothing, and the operator
     posts again later. So every time compared is the host's, as in
     no-cluster's decision 8, and the operator's clock only decides when
     it tries. The run is then an interrupted one with no checkpoint:
     `AfterInterruption` offers it again, twice in a round (`MaxReruns`,
     `task.go:296`, `:320-330`), and past that the round fails
     (`task.go:1670-1673`). A `release` ends the run the same way without
     the wait. The branch keeps its commits for the next claim.
   - *Rounds.* Failed checks or requested changes start the next round
     with a new offer that carries the feedback, up to the Agent's
     `maxRounds`.
4. **Implementer, spec writer and triager may be external; no other role.**
   Each is judged after it: CI and the reviewer judge a change and a
   spec, and a triager's decision is checked by `TriageProblem` and
   applied by the operator. Not the reviewer or the verifier: they decide
   the merge. Not the planner: a Plan Task has no reviewer
   (`api/v1beta1/task_types.go:56`). Not a Scorer's judge or a
   Benchmark's Agent: their numbers are the measure, and an outside run
   reports no cost. The designer joins the list in the step that gives a
   Crew one: none has it yet (`brief_round.go:376-377`).
5. **The CRD refuses the role, and the operator has no way to run one
   outside.** A spot names its Agent by name, and a CRD's rule reads its
   own object alone, so no rule on a Crew can read an Agent's harness. The
   rule therefore sits where the fact is:
   - *CEL, on the Agent.* `external.roles` is required and is a set from
     the enum `implementer`, `specWriter`, `triager`. An Agent has
     `external` exactly when its harness is `external`, then has no
     `image`, `model`, `credentialsSecretRef`, `resources` or `env`, and
     its harness never changes to or from `external`. So no manifest can
     register an outside reviewer.
   - *The operator.* One function in `internal/model` says, for a role and
     its Agent, whether the run is outside. It is outside only for a role
     the Agent lists, on a Task with an issue. An external Agent named
     anywhere else (a reviewer, verifier, planner, judge or Benchmark
     spot, a role it does not list, a Task with a goal and no issue) stops
     the Crew or the Task with `ExternalNotAllowed`, as `AgentNotFound`
     stops them today (`crew_controller.go:186`, `task_controller.go:257`),
     and nothing runs.

   Why not an admission policy that reads Agents when a Crew is written:
   it would miss an Agent written after the Crew, so the operator's check
   would be needed all the same.
6. **The agent comes to the Crew's Git host; the factory opens no door.**
   The work order is on the host, so whoever reads it has reached it.

   | Where the agent runs | The host | What a person does |
   | --- | --- | --- |
   | on the cluster's machine | the chart's Gitea | the three things of *Read this first* ([getting-started.md](../getting-started.md) has the port-forward); the repository is then at the forwarded address, not the in-cluster one Gitea's pages show (`dist/chart/templates/gitea/configmap.yaml:17`) |
   | anywhere | a Gitea the user runs, whose site administrator's token the operator holds (`gitea_agents.go:207`): not a hosted one where the user is no site administrator | sets the GitProvider's `url` to it; in the chart, `gitProviders[].url` with `gitea.enabled: false` (`dist/chart/values.yaml:512-516`, `:574-578`) |
   | anywhere, a Claude Code cloud session on Anthropic's machines included | GitHub | waits for #81; such a session works with the GitHub access its person connected, so that account is the Agent's, must not administer the repository, and its reviews stop counting |

   **On the chart's own Gitea the default works after those three
   things, which only a person can do.** No setting does them: an outside
   agent holds no kubeconfig (decision 2), an account the operator made
   would leave its token in a cluster Secret that someone carries out by
   hand, and an address for the chart's Gitea is #121's: its root URL is
   the in-cluster one, which CI jobs clone from
   (`api/v1beta1/gitprovider_types.go:156-158`).
7. **One account per external Agent, named in its spec; the factory gives
   and takes its access.** `external.account` is a login on the Git host.
   While a Crew names the Agent, the operator makes that account a
   collaborator with write access to the Container's repository, and
   takes it away after (decision 13): that is how one agent is revoked,
   on one repository. The token is the account's own, made and revoked on
   the host by its holder. The factory bounds the account, not the token
   (`OwnCredential`). Refused, and no work offered: an account that is
   the operator's (`AccountIsOperators`), a Container's bot or another
   Agent's (`AccountInUse`), one that writes the repository some other
   way, as through a team (`AccountAlreadyWrites`: only what the
   operator gave is its to take), or one whose access is administration
   (`AccountAdministers`). A level the adapter does not know, or any
   above write, reads as administration, so a miss refuses an account
   and never admits one. Why not `EnsureAgentAccess`: it makes a bot and
   its token (`gitea_agents.go:181-185`), and on GitHub mints an
   hour-long token of the one App (`github_host_agents.go:198-201`),
   which names no agent and would need a way out of the cluster every
   hour. The ports have no call for a named account, so step 2 adds three.
8. **Told apart by account, and never counted as a person.** The operator
   acts as the GitProvider's account, an in-cluster agent as the
   Container's bot (`model.AgentUsername`; on GitHub, the App), an outside
   agent as its own: the host stamps each comment with it. The operator
   takes as a round's change only the commit a counted `done` names. The
   pull request's description names the Agent, its account and
   `external`, and the Task records the run with `harness: external`
   (`task_types.go:422-450`). A commit's author line is what its writer
   typed, and nothing rests on it. An external Agent's account is one of
   the factory's accounts (`model.FactoryAccount`,
   `internal/model/spec.go:198-205`): its review of a pull request is no
   person's approval.
9. **A person registers an external Agent, as themselves.** An admission
   policy refuses a write that sets or changes an Agent's `external`
   block from a ServiceAccount or a node (`system:serviceaccount:`,
   `system:node:`): not the operator, the UI acting as itself, the
   Session client, an agent or a GitOps controller, so an external Agent
   is applied by hand. Not every `system:` name, as the chat annotations'
   policy has it (`config/policy/task_chat_annotations_policy.yaml:40`):
   k3s's administrator is `system:admin`. On such a write
   `external.delegateOf` must equal the API server's name for the
   writer, so the Agent says whose delegate it is. A write that leaves
   the block as it was is anyone's whom RBAC lets, the operator's
   ownerReference included. Which person: the cluster's RBAC on Agents
   in the project's namespace.
10. **What can be configured is configured.** `spec.external` holds
    `account`, `roles`, `delegateOf`, and `leaseMinutes` (60, from 10 to
    1440) and `graceMinutes` (5, from 1 to 60), no-cluster's defaults, so
    one client serves both. The host is the GitProvider's, as today. How
    often a waiting run's issue is read when no delivery wakes the Task
    is the operator's `--external-recheck` (30 seconds).
11. **Cost is unreported, never the agent's word.** An outside run adds
    one to `runsWithoutCost` and nothing to `costMilliUSD`
    (`RunState.Cost`, `task.go:248-257`), so `taskBudgetUSD`, which sums
    reported cost (`task.go:1327`), never stops it. Why not a number in
    the `done` marker: nobody can check it, and the same series feed the
    metrics and the Benchmarks.
12. **An outside run is prepared as a sandbox run is, less the model
    credential: the Container's bot and its Git Secret exist before the
    first offer.** Only a sandbox run's preparation makes them today
    (`ensureAgentGit`, `task_actions.go:424`, `:537-549`), and `openPR`
    signs in with that Secret (`:251`, `:1349`): a Container whose first
    Task ran outside would have no bot to open its pull request. Why the
    bot still opens it: the host refuses a review of one's own pull
    request, and the operator's account reviews (`:1337-1340`). The
    bot's token stays in the cluster.
13. **The Container records the access, and its reconciler takes it
    away.** That reconciler gives the access, lists the accounts in
    `status.outsideAccounts`, which it alone writes
    (`container_controller.go:384`), and removes each one no Crew of the
    Container leads to any more: the spot names another Agent, the Agent
    is deleted or names another account, or the Crew is deleted. A
    finalizer holds the Container while one is listed, so deleting the
    Container removes the access first. It never strands the deletion,
    by the rule a Brief's bot has (`deleteBot`,
    `brief_workspace.go:195-233`): with the GitProvider or its Secret
    gone there is no host to ask, and the access is left; a host that
    fails is asked again every 30 seconds until the namespace is
    terminating or ten minutes have passed since the deletion (the
    Brief's `BotDeleteTimeout`, `brief_controller.go:120`), and then the
    access is left too. What is left is said in an Event that names the
    account and the repository, `OutsideAccessLeft`: that account writes
    the repository until a person removes it on the host. Why not the
    Agent or the Crew: an Agent has no status and no reconciler
    (`agent_types.go:116-128`), and a Crew has no finalizer and stops at
    `AgentNotFound` first (`crew_controller.go:182-187`). A Task made by
    hand runs outside only while a Crew of its Container names its Agent
    in that role, as the Crew is what gives the access; otherwise it
    stops, `ExternalNotAllowed`.
14. **The operator schedules; an outside agent only answers.** The offer
    mentions the account, so the host tells its holder, and an agent asks
    `theos next`: with the operator's login set (`THEOS_OPERATOR`) it
    lists the offers that name the account, that no counted `done`,
    `blocked`, `nothing` or `ended` has closed, and that no live claim
    holds.
    There `next` and `claim` take nothing else and create no branch:
    never a `status/ready` issue, which is the Crew's to start, or the
    client would be the second scheduler no-cluster rules out. An offered
    issue is `status/in-progress` (`task_issue.go:59`) with no claim,
    which design.md's *Without a cluster* said a session never takes:
    that sentence now says "no counted marker" where it said "no counted
    claim marker", as no-cluster's decision 8 does, and the work order
    is a counted marker. Why change that sentence and not write an
    exception here: one rule then reads the same in both sections, and a
    session takes an offered issue as decision 8's second case, with a
    `claim` on the branch as it is.

## What the spot loses, and how each loss is shown

Shown in four places: the Crew's `OutsideAgents` condition, which lists
the spots and the words below; the last line of every work order and of
the pull request's description; and the UI's mark on the spot and its runs.

| The sandbox gives | An outside spot | Said as |
| --- | --- | --- |
| The fence: a NetworkPolicy, a pod with no ServiceAccount token, a deadline | whatever the agent's own runtime has; the factory adds none; a run that keeps renewing has no end the factory sets | `NoFence` |
| The model proxy: no model credential near the agent | the agent brings its own | `OwnModelCredential` |
| Checkpoints, resume and pause | a run that dies leaves its commits on the branch for the next claim; a pause offers nothing new and stops no run under way | `NoCheckpoint` |
| Recorded cost and tokens, and the Crew's budget | each run counts in `runsWithoutCost`; the budget does not see it | `CostUnreported` |
| A credential the factory mints and narrows: it pushes code and reads packages, can write no issue, and on GitHub is refused a push that changes a workflow | the account's own token, with the scopes and the life its holder gives it. With write access the account does on that one repository what the host lets a writer do: push every branch but `main`, and label, edit, assign and close issues and pull requests, so the labels that queue work and the text a reviewer judges against are within its reach. On GitHub nothing refuses its push of a changed workflow; *The CI fence* is what holds there, as on Gitea. The factory takes the access away; it cannot narrow or end the token | `OwnCredential` |
| A work order that only the operator can give: the sandbox's prompt | a comment on the issue, told from a look-alike only by its author. `theos` checks the author against the operator's login (the GitProvider's status has it, step 2); an agent that reads the issue without `theos` must do so itself, and nothing makes it | `OfferIsAComment` |

Also not given: the model's context and the related repositories' clones,
a test environment, and the check run before the push. CI still runs the
check.

**What still holds, all on the Git host,** for an account that passed
decision 7: `main` takes no push and only the operator merges; a merge
that changes a protected path or has a red required check is refused, by
the host on Gitea and by the operator's merge on GitHub; `v*` tags are
the operator's (design.md, *The fence*). The in-cluster reviewer, the
verifier and the Crew's approval rules judge the change as they judge any.

## Steps

Each is a new issue; "tier" is AGENTS.md's. No step needs Kind or e2e.

| Step | Scope | Needs | Tests it brings |
| --- | --- | --- | --- |
| 1. The `external` harness in the API, and where it may stand | the enum value, `spec.external`, decision 5's rules and function; an allowed spot waits, `ExternalNotBuilt`, until step 4 | | tier 4: each CEL rule's failing case (a role outside the enum; an empty `roles`; `external` without the harness and the reverse; each of `image`, `model`, `credentialsSecretRef`, `resources` and `env` beside it; a sandbox Agent without an `image`; a harness changed in place; a lease or a grace outside its bounds); a Crew whose reviewer is external waits `ExternalNotAllowed` and starts no Task, and so do a hand-made Task and one whose external `agentRef` has no `reviewerRef`, where the Agent would review its own work; tier 1: the function's table, a row per role; `TestTheHarnessEnumIsTheRunnersTable` reads the runner's table plus `external`; tier 2: a sample Agent passes `make validate-yaml`; `test/compat` passes |
| 2. Ports: a named account's access to a repository | GitHost gains reading an account's access (none, read, write, administration; what a host reports above write, Gitea's `owner` included, or in a word the adapter does not know, reads as administration), giving write and removing it; the fake, Gitea and GitHub; the GitProvider's status gains the operator's login | | tier 1: the GitHost contract's new subtests on the fake and `githubtest` (given, read back, removed; an administrator read as one; a level the adapter does not know read as administration; an account that does not exist is an error that names it); tier 3: the same on Gitea, a site administrator included |
| 3. The markers of an offered run | `internal/model`: a `RunState` from an issue's comments, the branch's head, the two accounts, the lease and a time; and the offer's text, from `internal/method`'s work order | #266, #267 | tier 1: a table row for each sentence of decision 3, a marker from any other account changing nothing, a comment that quotes a marker after other text counting as none, and the `ended` that came too early; the offer holds every part decision 3 names and the loss line, fences the round's feedback as issue text is fenced, and holds nothing of the model's context |
| 4. The operator offers, waits and ends a run | the controller's outside path for an implementer and a spec writer; the preparation (12); access given, recorded in the Container's `status.outsideAccounts` and removed, with the Container's finalizer (7, 13); `FactoryAccount` and the pull request's line (8); the run's record (11); the condition and the two last lines; `--external-recheck` | 1, 2, 3 | tier 4, on the fake host with a test posting the agent's markers: a Task from offer to merge with no Sandbox made; the first Task of a Container, its implementer outside, gets its pull request opened as the Container's bot; a silent run offered twice more, then a failed round; `done` for a commit that is not the head waits; an outside account's approval leaves a spec waiting for a person; access removed when the Crew stops naming the Agent, when the Agent is deleted, when its account changes, when the Crew is deleted and when the Container is; a Container deleted while the host fails the removal is held and then let go after the timeout, and one whose GitProvider is gone goes at once, each with an `OutsideAccessLeft` Event that names the account; an account that already writes the repository refused, and each other refused account of decision 7; a hand-made Task with no Crew naming its Agent stops |
| 5. Who registers | the admission policy of decision 9, in `config/policy` and the chart | 1 | tier 4: a ServiceAccount's write refused, a person's taken, a `delegateOf` naming someone else refused; the operator's write of an ownerReference on an external Agent taken; a writer named `system:admin` taken; tier 2: `make validate-yaml` and the chart's test render it |
| 6. `theos` answers an offer | `next`, `claim`, `heartbeat`, `done`, `release` and their MCP tools, for an account that works a Crew's repository (decision 14) | #268, #267, 3; the tools with #294 | tier 1, on the ports' fake, with the operator's login set: `next` lists an unclaimed offer that names the account; a `status/ready` issue is neither listed nor claimed, and no branch is made; a claim on the operator's offer posts one marker; an offer from any other account is refused and posts nothing; `done` names the pushed head |
| 7. A triager outside | a Triage Task's run from a `done` that carries the decision | 4 | tier 1: the decision parsed by the parser the runner uses, moved to `internal/model`; tier 4: an outside triage applied, and a malformed one refused as today |
| 8. The UI shows an outside spot | the mark and the words on the Crew's spot, the Agent and each run | 4 | `make ui-test` |
| 9. The guide and the design | `docs/external-agents.md`: registering, the account, reaching the host, the loss table; design.md's sections follow what main does | 4, 5, 6 | `make doc-links`. A person's walk of one Task with a local Claude Code, on a cluster of theirs, is asked of the owner and no step waits for it |

**To file.** The nine steps. What each issue is filed with beyond its row
and the protected paths each step touches, which are the owner's
session's (AGENTS.md), are in
[external-agents/to-file.md](external-agents/to-file.md).

## Not covered

- Stopping an outside run that is under way, and an account the operator
  makes for the agent (decision 6).
- A grant narrower than write access, such as a Gitea team's: not tried,
  and the loss is stated instead (`OwnCredential`).

## Done when

This plan and design.md's *Agents outside the cluster* are merged, and
the nine steps are filed. A step is done when its row's tests pass.

## Not tested, and who can

Nothing here was run against a host or an agent. What is said of them is
their public documentation and Gitea's source, as read on 2026-11-14;
[to-file.md](external-agents/to-file.md) has each page, and what is
unverified with the step that checks it.
