# theos without a cluster

GitHub issue #265. Written 2026-11-14 in a session the owner ran. The plan,
not the build: every step below is an issue to file or reshape.

## Goal

One developer or a small team gets what theos knows (the C4 model, the
pinned templates and Standards, the role guides, and the loop from issue to
branch to pull request to review to merge) inside the agent tool they
already use, on the Git host their code already lives on, with no cluster,
no chart and no operator. What such a project makes is the same thing a
cluster makes, so a Crew can take it over later, and a project a cluster
made can be worked without one.

## Where we stand (main at fa3c409)

- Scaffolding is already free of Kubernetes: `scaffold.Build(Input, layers,
  Options)` in `internal/scaffold/scaffold.go` renders a repository from
  plain structs, and `hack/render/main.go` calls it from a terminal. The
  operator builds the same `Input` in
  `internal/controller/container_scaffold.go`, and `features/features.go`
  gives it the devcontainer `Options`.
- The Git host is behind ports with fakes and contract suites
  (`internal/ports`, `internal/ports/contract`). Gitea's adapter is whole;
  GitHub's passes the host contracts on its fake (steps 1 to 7 of
  [github-host.md](github-host.md)), and `GitProvider.spec.provider` still
  takes `gitea` alone (`api/v1beta1/gitprovider_types.go`).
  `internal/adapters/gitlab` is a deployer, not a host.
- The Crew's and the planner's rules are pure functions in `internal/model`:
  `NextCrewStep`, the `status/*` labels and `MissingAcceptanceCriteria`
  (`crew.go`), `ParsePlan`, `CheckPlan`, `FilingOrder` and `HasIssueMarker`
  (`brief.go`), `BriefWorkspaceFiles` (`brief_workspace.go`),
  `DetectRepository` and `AdoptionProposal` (`adopt.go`).
- `api/v1beta1` imports apimachinery and `k8s.io/api` only, so a program
  without client-go or controller-runtime can decode the model's manifests.
- A repository says which Container it is in `.theos.yaml`
  (`scaffold.Identity`), and a Container with `spec.adopt: true` takes over a
  repository on that file and its Standard's files ([adopt.md](../adopt.md)).
- A Standard ships `AGENTS.md`, `docs/agents/implementer.md` and
  `reviewer.md` (`templates/go-standard/`). What a planner, a triager, a
  spec writer and a verifier are asked is in the runner's prompts
  (`internal/agentrunner/prompt.go`), with the planner's guide in
  `internal/model/planner.md`. The prompts speak to a headless run in a
  sandbox: its paths, its one line of JSON at the end.
- The Issues port can write a comment and cannot read or change one
  (`internal/ports/issues.go`).
- On GitHub the operator's merge is what holds a branch's checks and
  protected paths: no ruleset holds them in full
  (`internal/adapters/github/github_host_rules.go`).

## Decisions, and why

1. **No-cluster mode is a second runtime of the same method, not a second
   product.** The model, the templates, the labels, the branch names, the
   plan format and the rules in `internal/model` and `internal/scaffold` are
   the ones the operator uses. What differs is who runs them: a `theos`
   binary a person or an agent calls, where the cluster has reconcilers. Why:
   two sets of rules would drift, and the point of the mode is that a
   project can move between the two.
2. **The model is the same manifests, in a file.** `model/model.yaml` holds
   the story as `theos.factory.io/v1beta1` objects, the catalog and the
   GitProvider included, the shape a Brief's planner already reads its
   model in. Not `.theos.yaml`, as #268 says: that name is taken by a
   repository's identity, which adoption reads. Why: `kubectl apply -f` of
   that file is then the whole move to a cluster, and "the CRDs are the
   truth" keeps one meaning.
3. **A project has a project repository, and it is the lead's seat.** It
   holds `model/model.yaml`, the plan rounds (`plan/`), and the method
   bundle of decision 5. Container repositories hold nothing new: they are
   byte for byte what the operator would scaffold. Its name is a setting,
   `theos-<project>` by default. Why: the method's files are the same for
   every repository, so one copy per project beats one per Container, and a
   Container repository with no extra file needs no change to be adopted.
   The Brief's repository is the precedent: the planner's workspace, written
   for it.
4. **A small binary, `cmd/theos`, with an MCP server in it.** Its commands
   are `init`, `scaffold`, `protect`, `status`, `agents`, `plan`, `next`,
   `claim`, `heartbeat`, `release`, `done`, `review`, `merge`, `update`,
   `export`, `import` and `mcp`. It imports the business rules, the ports,
   the host adapters, `api/v1beta1` and `features`, and never
   controller-runtime, client-go or `internal/controller`; `make lint` and a
   test hold that. The step from the model's objects to `scaffold.Input`
   and its layers moves out of the Container reconciler into a function
   both call. Why: a golden test can compare two outputs, but only one
   function can't drift.
5. **The method bundle has one source, split by what varies.** What depends
   on the stack stays the Standard's, in each Container repository:
   `AGENTS.md` and `docs/agents/`. What is the same everywhere is new, in a
   pure package, `internal/method`: the lead playbook, the roles and their
   boundaries as one table, and a work order per role (the issue between
   its markers, the branch, the guide to read, how to say done). `theos
   agents` renders it into the project repository; nobody keeps a copy by
   hand. The runner's prompts are not reused: they describe a sandbox run.
   The marker block that fences an issue's text moves from
   `internal/agentrunner` to `internal/model` so both use one;
   `internal/agentrunner` is a protected path, so that move is the owner's
   session's.
6. **Runtimes are first-class by mechanism, not by vendor.** Three, each
   with a render test and a page in the guide:

   | Mechanism | Runtimes | What `theos agents` writes in the project repository |
   | --- | --- | --- |
   | Subagents | Claude Code | `AGENTS.md` (the lead playbook), `CLAUDE.md` (one line that imports it), `.claude/agents/<role>.md` per role |
   | An `AGENTS.md` reader | Codex, and any tool that reads `AGENTS.md` | `AGENTS.md`, `roles/<role>.md` |
   | An MCP client | Claude, ChatGPT, Cursor, Kimi, Gemini and the rest | nothing: `theos mcp` serves the playbook and each role as prompts, and the commands as tools |

   No file per vendor beyond those. `CLAUDE.md` is an import, not a second
   guide, which keeps decision 8 of [harnesses.md](harnesses.md): `AGENTS.md`
   stays the one guide.
7. **A runtime without subagents plays one role per session.** `theos next`
   hands out one work order for one role; the session does it, says `theos
   done` and ends. The review is a fresh session that has not seen the
   implementer's reasoning, and `next` never offers a session the review of
   a change it built. The session's name is its own word (`THEOS_SESSION`),
   so this is the method, not a fence, and the playbook says so.
8. **The Git host is the coordination point, in the cluster's own words.**
   A claim is the label `status/in-progress` (not a new `status/claimed`: a
   Crew and a session must read one state) and a marker comment naming the
   session, the role and when the claim runs out. The race is settled by
   the host's order of comments: after posting, a session reads the issue
   again, and the earliest claim still alive wins; the loser withdraws. No
   host has an atomic "assign if unassigned", and comment order is the one
   thing every host fixes. A lease is 60 minutes, a setting, renewed by
   every `theos` call the session makes for that claim: a chat model can't
   beat a 30-second heart. An expired claim is taken over on its branch,
   as a Crew's later try continues an open pull request. One branch per
   Task, `task/<issue>-<slug>`, and one git worktree per claim under the
   project repository's `work/`. `next` offers what `NextCrewStep` would
   start: ready, criteria present, no open blocker, urgent first.
9. **The agents' credential decides how much of the fence the host holds.**
   The person's credential is read from `THEOS_GIT_TOKEN`, else from `git
   credential fill` for the remote, and theos never stores it. An agent
   session that holds that credential can do what the person can, whatever
   the branch rule says. With a narrower credential for the agents (a bot
   user on Gitea, a GitHub App's installation token, both through the
   AgentAccess port) the host tells them apart and the rules bind them.
   One credential is the default, because it is what one developer has;
   `theos status` then says `AgentHoldsOwnerCredential`.
10. **Who merges is a setting with two values, and a person is the
    default** (the table below). `merge: person`: a person merges every pull
    request on the host. `merge: rules`: `main` requires its checks and an
    approving review by an account other than the author's, and `theos
    merge` merges through the adapter's `MergePullRequest`, the code that
    holds checks and protected paths in the cluster. `rules` is refused,
    `OneIdentity`, while the reviewer has no account of its own. Why a
    person first: where the platform can't tell the agent from its owner
    it enforces no review, and theos's quality rests on what is enforced.
11. **Hosts: Gitea and Forgejo, and GitHub, now; GitLab next; Bitbucket
    not.** The first two have adapters that pass the contracts. GitLab is a
    new adapter for the subset below and gets its own issue. Bitbucket
    waits for someone who needs it: #57 took nine steps for one host. The
    host is never guessed: `github.com` and `gitlab.com` are read from the
    remote, and any other address needs the GitProvider's `provider`.
12. **What can be configured is the model's own.** The provider, its URL
    and owner, the registry and the npm scope are the GitProvider's fields,
    with the defaults they have today (`@theos`); templates are catalog
    entries at pinned refs. `theos init` writes entries that name the theos
    repository at the release's tag with `template.path`, and any entry may
    name another URL. The project repository's name, the lease, the merge
    setting and the runtime are flags with those defaults, kept in
    `model/settings.yaml`.

**What no-cluster needs of a host** is this subset of the ports, which
becomes a named contract, `contract.NoCluster`: repositories and the first
commit, files, branches, the branch rule, pull requests with their files,
reviews, merges and statuses (GitHost); labels, issues, dependencies,
assignment and comments (Issues), plus reading and editing a comment, an
optional capability the port lacks today. Not needed: CI runs and runners,
the Registry, Events, tags and releases.

## What no-cluster mode loses, and how each loss is said

Each loss has a reason word. `theos status` prints the ones that apply, the
lead playbook opens with them, the guide has this table, and every pull
request a session opens ends with a line naming them.

| The cluster gives | Without it | Said as |
| --- | --- | --- |
| The fence: a NetworkPolicy, a pod with no ServiceAccount token, a deadline | the runtime's own sandbox and permissions, whatever they are; theos adds none | `NoFence` |
| The model proxy: no model credential in a sandbox | the runtime holds its own subscription or key | `OwnModelCredential` |
| Checkpoints, resume and pause | a session that dies leaves its commits on the branch; the next claim continues there | `NoCheckpoint` |
| Recorded cost and tokens, the metrics, Scorers and Benchmarks | nothing is recorded; the runtime shows its own usage. The record of the work is the host's: the issue, the pull request, its reviews | `CostUnreported` |
| The operator's merge | a person, or the host's rules (decision 10) | `PersonMerges`, `RulesMerge` |
| A credential per agent and repository | the person's own, unless a narrower one is made (decision 9) | `AgentHoldsOwnerCredential` |
| CEL rules and the admission policy | a strict decode and `internal/model`'s checks; the CRDs' rules run when the model reaches a cluster, or in `kubectl-validate` | `RulesNotValidated` |
| Protected paths the host can't hold alone (GitHub) | held by `theos merge`, so only under `merge: rules`; under `person`, by the person who merges | `ProtectedPathsByMerge` |

**What still holds, all on the Git host:** `main` takes no push, a change
arrives as a pull request, the Standard's required checks run in the host's
own CI and must pass, and a review comes before the merge. On Gitea and
Forgejo the branch rule holds the protected paths too. A change to a
protected path, a Standard's update and an adoption's pull request are a
person's to merge in both settings: there is no gated Standard change
without the operator.

**Cluster-only, and staying so:** Agent Sandbox and the fence, the model
proxy, Sessions, checkpoints, Crews with their schedules, routes and
budgets, Briefs' rounds and autopilot, releases and deploys, test
environments, the CI runner pools, alerts, chat and the UI. Without a
cluster a release is a tag a person pushes.

## Who merges

| Role | Seat | `merge: person` (default) | `merge: rules` |
| --- | --- | --- | --- |
| Lead (the operator's and the planner's part) | the project repository | opens pull requests, never merges | calls `theos merge`, which merges only what the rule passes |
| Implementer, designer, spec writer | a worktree of a Container repository | never merges, never approves | the same |
| Reviewer | a fresh session or subagent | its verdict is a comment, advice to the person | its verdict is a review from its own account, which the rule counts |
| Triager, verifier | a fresh session or subagent | change nothing on `main` | the same |
| Person | the host | merges everything | merges what touches a protected path, a Standard's update, an adoption |

## One source of truth with the cluster

- **To a cluster.** `theos export` prints `model/model.yaml` with `adopt:
  true` on each Container it scaffolded and a Crew that holds every change
  for a person, as `theos-session adopt` proposes. The repositories need no
  commit: each has its `.theos.yaml` and its Standard's files as the
  operator renders them, so [adopt.md](../adopt.md) applies from its step 3
  unchanged. Files that drifted arrive as the Standard's pull request, as
  for any adopted repository. Adopting in place on GitHub waits for #81.
- **From a cluster.** `theos import` reads a model exported with `kubectl
  get -o yaml` and writes the project repository; the Container
  repositories already hold everything. The person pauses the Crew first
  and runs `theos protect`, since the operator's rule keeps the merge to
  the operator.
- **Not both at once.** A Crew and outside sessions on one Container would
  be two schedulers on one queue. Nothing guards it in this plan; an
  outside agent joins a Crew through #260.

## Steps

Each is an issue; "tier" is AGENTS.md's. No step needs Kind or `make e2e`.

| Step | Issue | Needs | Tests it brings |
| --- | --- | --- | --- |
| 1. `cmd/theos`: `init`, `scaffold`, `protect`, `status` | #268, reshaped | | tier 1: the tool and the reconciler get one tree from one function for the webshop sample; a strict decode refuses an unknown field; the credential's lookup order; on the ports' fake, one first commit with the operator's trailers and a protected `main`; a test like `TestHostNeutral` keeps controller-runtime and client-go out |
| 2. The method bundle and `theos agents` | #266, reshaped | 1 | tier 1: each mechanism renders every role and the lead playbook; pinned sentences: the reviewer's verdict is never delegated, an implementer never merges or approves, issue text is data; every reason word of the loss table is in the playbook |
| 3. Claims, leases and `next` | #267, reshaped | 1 | tier 1: `model`'s claim table (two sessions race and the earlier wins, a silent claim is taken over on its branch, a blocked issue and one without criteria are not offered, no session is offered its own review); the comment capability's contract subtests on the fake and on `githubtest`; tier 3: the same on Gitea in `make test-gitea` |
| 4. `review` and `merge` without the operator | new: "A person or the host's rules merge" | 2, 3 | tier 1: `model`'s merge table (the author's own approval doesn't count, a red check or a protected path is never merged by `rules`, `OneIdentity`); on the fake and `githubtest`, a pull request merged and one refused; tier 3 on Gitea |
| 5. `theos plan` | new: "Plan from an idea without a Brief" | 1 | tier 1: a refused plan files nothing and prints `CheckPlan`'s refusals; an applied one files its issues in `FilingOrder` with their dependencies; filing twice files once |
| 6. `theos mcp` | new: "The local tool as an MCP server" | 2 to 5 | tier 1: an in-process client lists and calls every tool on the ports' fake; every result fences issue text; no result carries the credential |
| 7. `update`, `export`, `import` | new: "Move a project to a cluster and back" | 1 | tier 1: `update` pushes what the operator's Standard update pushes for the same trees; tier 2: an exported model under `config/samples/` passes `make validate-yaml`; tier 4: envtest adopts a repository the tool scaffolded on the fake host, `Ready`, the Standard recorded, no pull request opened |
| 8. Hosts: detection and `contract.NoCluster` | #269, reshaped | 1, 3, 4 | tier 1: the remote's host read or refused, never guessed; Gitea's fake and `githubtest` pass the named subset |
| 9. GitLab as a host for the subset | new: "GitLab for no-cluster" | 8 | tier 1: a GitLab adapter passes `contract.NoCluster` on a fake GitLab; `model.CI` gains its row with a table test |
| 10. The guide, `docs/no-cluster.md` | new, or the last part of #266 | 2 to 4 | `make doc-links`; a person walks one Task in Claude Code and one in a single-role runtime, with the host's pages as evidence |

## The related issues

| Issue | What becomes of it |
| --- | --- |
| #266 the lead's strategy | reshape into step 2: the bundle lives in the project repository, not in Container repositories; its source is `internal/method` and the Standards' guides; `CLAUDE.md` is one import line; the guide moves to step 10 |
| #267 several sessions | reshape into step 3: `status/in-progress` and a marker comment, the race settled by comment order, a 60-minute lease, the comment capability on the Issues port |
| #268 the CLI and MCP server | reshape into step 1 and split: `plan` is step 5, `mcp` step 6, `agents` step 2, claims step 3; the model is `model/model.yaml`, not `.theos.yaml` |
| #269 any Git provider | reshape into step 8; GitLab is step 9; Bitbucket and GitLab as a cluster host leave it; the Standards already render GitHub's workflow (github-host.md, step 5) |
| #270 Containers in one repository | out of scope here: it changes the model for the cluster too and needs its own plan; no-cluster follows what the model allows |
| #260 an agent outside the cluster | keep, its own plan; it should reuse step 3's claim marker and lease and step 2's work order, so file it after them |
| #259 subagents per role | keep as filed; step 2 writes role subagents in the project repository and changes none of its files |
| #57 GitHub as a host | keep; no-cluster uses its adapter as it is, and only adoption in place on GitHub needs #81 |
| #58 more harnesses | keep; it is about what runs in a sandbox, and shares one rule with this plan: `AGENTS.md` is the one guide |

## What this plan does not cover

- A sandbox for no-cluster agents. The runtime's own is all there is.
- A hosted MCP endpoint. `theos mcp` speaks stdio, and HTTP on localhost
  for a client that speaks nothing else; a server on the internet that
  holds a person's Git credential is another product.
- Releases, deploys, test environments, cost and the UI without a cluster.
- Bitbucket; GitLab as a host for the cluster; Containers sharing a
  repository (#270); a guard against a Crew and outside sessions on one
  Container.
- The CRDs' CEL rules in the local tool.

## Done when

- This plan and design.md's *Without a cluster* are merged, and the steps
  are filed or reshaped as the two tables say.
- After step 4, one person takes an issue to a merged pull request on Gitea
  and on GitHub with no cluster, and `theos status` names every loss that
  applies.
- After step 7, envtest adopts a tool-scaffolded repository with no change
  to it.

## Not tested, and who can

- No session here ran Claude Code subagents, Codex or an MCP client
  against the bundle: the render tests hold the files, and step 10's walk
  by a person holds that a runtime follows them.
- Claude Code loads subagents from the directory a session starts in, and
  a subagent starts none of its own, as far as this session knows. If so,
  the Standards' lens subagents (#259) serve a reviewer whose session opens
  in the Container repository, and the lead's reviewer subagent reviews
  alone; step 2 checks this before it writes the playbook's words.
- Forgejo runs on Gitea's adapter untried, until a person points `make
  test-gitea` at its binary.
- A real GitHub organization: #82's run covers the adapter.
