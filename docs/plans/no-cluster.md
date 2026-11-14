# theos without a cluster

GitHub issue #265. Written 2026-11-14 in a session the owner ran, and
rewritten the same day after its review. The plan, not the build: every
step below is an issue to file or reshape.

## Goal

One developer or a small team gets what theos knows (the C4 model, the
pinned templates and Standards, the role guides, and the loop from issue to
branch to pull request to review to merge) inside the agent tool they
already use, on the Git host their code already lives on, with no cluster,
no chart and no operator. What such a project makes is what a cluster with
the same settings makes, so a Crew can take it over later, and a project a
cluster made can be worked without one.

## Where this plan departs from the issues

Read these before the rest; each has its reason in the decision named.

- **The model's path.** #268 says `.theos.yaml`. It is three files under
  `model/` in a project repository; `.theos.yaml` stays a repository's
  identity (decision 2).
- **Who merges.** #265 offers a person or the host's rules. Through step 10
  a person merges, always. The host's rules merging is step 11, because it
  needs a change to the branch rule's port and to two protected adapters
  (decision 10).
- **Claims.** #267 offers assignment or a `status/claimed` label, a comment
  that is updated or a push as the heartbeat, and `task/<issue>-<slug>`.
  The lock is the Task's branch under the operator's own name,
  `task/<container>-<issue>`; the lease is marker comments that are never
  edited; the label is `status/in-progress` (decision 8).
- **Where the bundle lives.** #266 puts it in each Container's repository.
  It lives in the project repository (decisions 3 and 5).
- **Hosts and clients.** #269 names four hosts, and GitLab for the cluster
  too. This plan covers Gitea and Forgejo, and GitHub; GitLab without a
  cluster is step 9; GitLab as a cluster's host gets an issue of its own;
  Bitbucket is not planned (decision 11). ChatGPT is not a listed MCP
  client (decision 6).
- **Containers in one repository** (#270) are out of scope: that changes
  the model for the cluster too and needs its own plan.

Every choice that depends on where a person runs theos is a setting whose
default needs no cluster (decision 12; AGENTS.md, "What can be configured
is configured").

## Where we stand (main at fa3c409)

- Scaffolding is free of Kubernetes: `scaffold.Build(Input, layers,
  Options)` in `internal/scaffold/scaffold.go` renders a repository from
  plain structs, and `hack/render/main.go` calls it from a terminal. The
  operator builds the `Input` in `internal/controller/container_scaffold.go`
  from the model and from four values it takes as flags: `--session-feature`
  and `--session-binary-url` (`features.ScaffoldOptions`), `--ci-image-go`
  and `--ci-image-base` (`cmd/main.go`).
- The Git host is behind ports with fakes and contract suites
  (`internal/ports`, `internal/ports/contract`). Gitea's adapter is whole;
  GitHub's passes the host contracts on its fake (steps 1 to 7 of
  [github-host.md](github-host.md)). `GitProvider.spec.provider` is an enum
  of `gitea` alone, `credentialsSecretRef` is required
  (`api/v1beta1/gitprovider_types.go`), and `internal/adapters/gitlab` is a
  deployer, not a host.
- What a host is asked to hold on `main` is `ports.BranchRule`: required
  checks, protected paths, and whether the protecting account may push. It
  has no review requirement and no list of who may merge
  (`internal/ports/githost.go`). On Gitea the host refuses a merge with a
  red check or a changed protected path, and lets one account merge, the
  one that protected the branch (`internal/adapters/gitea/gitea_agents.go`).
  On GitHub a ruleset keeps everyone but the repository's administrators
  off the branch, and checks and paths are checked inside the adapter's
  `MergePullRequest`, not by the host (`github_host_rules.go`,
  `github_host_pulls.go`). On both, the protecting account can merge past
  the rule (`ForceMergePullRequest`, in the contract).
- `CreateBranch` fails on a branch that exists, on the fake, Gitea and
  GitHub (`internal/ports/contract/githost.go`).
- The Issues port writes a comment and gets nothing back, and cannot list
  comments (`internal/ports/issues.go`). The request-intake port has the
  shape to follow: its `Comment` returns a `TrackerComment` with an ID and
  an author, and it counts only its own account's markers
  (`internal/ports/intake.go`).
- The Crew's and the planner's rules are pure functions in `internal/model`:
  `NextCrewStep`, the `status/*` labels, `IssueTaskName` and `TaskBranch`,
  `ParsePlan`, `CheckPlan` and `FilingOrder`, `DetectRepository` and
  `AdoptionProposal`. In a Brief's repository `model/model.yaml` is the
  story alone, six kinds, and `CheckPlan` refuses any other kind in a plan;
  `model/catalog.yaml` there is a summary, not manifests
  (`brief_workspace.go`, `brief.go`).
- `api/v1beta1` imports apimachinery and `k8s.io/api` only, so a program
  without client-go or controller-runtime can decode the model's manifests.
- A repository says which Container it is in `.theos.yaml`
  (`scaffold.Identity`), and a Container with `spec.adopt: true` takes over
  a repository on that file and its Standard's files
  ([adopt.md](../adopt.md)), today on the factory's own Git host.
- A Standard ships `AGENTS.md`, two role guides under `docs/agents/`, and
  CI files per CI system under `.theos/ci/`, for Gitea Actions and GitHub
  Actions; a Container on another CI system fails with
  `UnsupportedCIError`. What the other roles are asked is in the runner's
  prompts (`internal/agentrunner/prompt.go`), which speak to a headless
  run in a sandbox.

## Decisions, and why

1. **No-cluster mode is a second runtime of the same method, not a second
   product.** The model's types, the templates, the labels, the branch
   names, the plan format and the rules in `internal/model` and
   `internal/scaffold` are the ones the operator uses. What differs is who
   runs them: a `theos` binary a person or an agent calls, where the
   cluster has reconcilers. Why: two sets of rules would drift, and the
   point of the mode is that a project can move between the two.
2. **The model is the CRDs' manifests in two files, plus one file of
   settings.** In the project repository:
   - `model/model.yaml` is the story: Project, Person, SoftwareSystem,
     Container, DataStore, Relationship. It is the file a Brief's workspace
     has under that name, so `ParsePlan` and `CheckPlan` read it as they
     are and a plan round is `plan/model.yaml`.
   - `model/setup.yaml` is what a person makes and no plan may write: the
     GitProvider and the catalog entries. Not `model/catalog.yaml`, which
     in a Brief is a summary in another format.
   - `model/settings.yaml` is what no CRD holds (decision 12). It is not a
     manifest, and it is the one file with a format of its own.

   Not `.theos.yaml`, as #268 says: adoption and the Session client read
   that name as a repository's identity, and it is on the agents'
   never-paths.

   "The same manifests" means this: every object in the first two files is
   a `theos.factory.io/v1beta1` object, decoded strictly into
   `api/v1beta1`'s types and then given the defaults the generated CRD
   schemas declare (57 today; without `provider: gitea`, `model.HostCI`
   fails). The schemas are embedded in the binary and the API server's own
   defaulting package applies them (`k8s.io/apiextensions-apiserver`,
   already in go.mod; `go list -deps` shows it brings `k8s.io/apiserver`
   and neither client-go nor controller-runtime). A list of defaults kept
   by hand would drift.

   It does not mean a cluster takes every such file as it is:
   - CEL rules, enums and the admission policy do not run locally
     (`RulesNotValidated`). `theos` itself refuses a `provider` it has no
     host adapter for.
   - A cluster refuses `provider: github` until #81, and `gitlab` until
     GitLab is a cluster's host. This plan does not widen the enum: a value
     there is the cluster's word that it can host there, which #81 gives
     last for that reason.
   - `credentialsSecretRef` names a Secret that no-cluster never reads.
     `theos init` writes the name `git-credentials`; the person makes that
     Secret when the project moves.
3. **A project has a project repository, and it is the lead's seat.** It
   holds `model/`, the plan rounds (`plan/`) and the method bundle of
   decision 5. Its name is a setting, `theos-<project>` by default.
   Container repositories hold nothing new: each is what an operator with
   the same settings scaffolds, where "settings" are the four flag values
   named above, kept in `model/settings.yaml` with the flags' defaults.
   Why one repository for the method: its files are the same for every
   Container, and a Container repository with no extra file needs no
   change to be adopted. `theos plan` does not render a Brief's workspace
   (`brief.md`, `PLANNER.md`, the summaries): those speak to a sandboxed
   planner. The lead reads the playbook, which carries the plan format.
4. **A binary, `cmd/theos`, with an MCP server in it, over three new
   packages.** Its commands are `init`, `scaffold`, `protect`, `status`,
   `agents`, `plan`, `next`, `claim`, `heartbeat`, `release`, `done`,
   `review`, `merge`, `update`, `export`, `import` and `mcp`.

   | Package | Holds | May import |
   | --- | --- | --- |
   | `internal/method` | the bundle of decision 5, pure | the standard library, `internal/model` |
   | `internal/scaffoldinput` | the step from the model's objects to `scaffold.Input`, its layers and `Options`, moved out of the Container reconciler so the operator and the tool call one function | what `internal/taskfacts` may, plus `internal/scaffold` and `features` |
   | `internal/nocluster` | the commands' logic and the MCP server, on ports, tested on the fakes | what `internal/sessionclient` may: everything but adapters and `internal/controller` |
   | `cmd/theos` | flags, and the wiring of host adapters | everything |

   None imports controller-runtime or client-go. docs/architecture.md and
   docs/dev/structure.md gain the rows, and "only `cmd/main.go` imports
   adapters" becomes `cmd/main.go` and `cmd/theos`. Why one shared
   function: a golden test can compare two outputs, but only one function
   can't drift. A person gets the binary with `go install
   github.com/JairJosafath/theos/cmd/theos@<tag>`.
5. **The method bundle has one source, split by what varies.** What depends
   on the stack stays the Standard's, in each Container repository:
   `AGENTS.md` and `docs/agents/`. What is the same everywhere is new, in
   `internal/method`: the lead playbook, the roles and their boundaries as
   one table, and a work order per role (the issue between its markers, the
   branch, the Container's `AGENTS.md` and role guide to read, when to call
   `theos heartbeat`, how to say done). `theos agents` renders it into the
   project repository; nobody keeps a copy by hand. The runner's prompts
   are not reused: they describe a sandbox run. The marker block that
   fences an issue's text moves from `internal/agentrunner` to
   `internal/model` so both use one.
6. **Runtimes are first-class by mechanism, not by vendor.**

   | Mechanism | Runtimes | What the project repository gets |
   | --- | --- | --- |
   | Subagents | Claude Code | `AGENTS.md` (the lead playbook), `CLAUDE.md` (one line that imports it), `.claude/agents/<role>.md` per role |
   | An `AGENTS.md` reader | Codex, and any tool that reads `AGENTS.md` | `AGENTS.md`, `roles/<role>.md` |
   | An MCP client | any client that starts a local server over stdio or reaches HTTP on localhost | nothing: `theos mcp` serves the commands as tools, and the playbook and each work order as tool results, and as prompts for a client that shows prompts |

   `theos agents` writes both sets of files every time, with no setting:
   they are small, they don't collide, and a team with a Claude Code
   session and a Codex one on one repository needs both. What the
   runtimes' own documentation said on 2026-11-14 shapes the rest:
   - Claude Code reads `AGENTS.md` by itself only where no `CLAUDE.md` is
     in the working directory or above, and not in every version or
     session (code.claude.com/docs/en/memory). So `CLAUDE.md` stays, as an
     import, which keeps decision 8 of [harnesses.md](harnesses.md). The
     price: a Container worktree's `AGENTS.md` under the project
     repository does not load by itself, so the work order names it.
   - Claude Code finds project subagents "by walking up from the current
     working directory" to the repository root, and "a subagent can spawn
     subagents of its own, up to three layers" by default
     (code.claude.com/docs/en/sub-agents). So the lead's role subagents are
     the project repository's, and a Standard's lens subagents (#259) are
     found only by a session whose working directory is the Container's
     repository. The project's reviewer file lists its tools without
     `Agent`: from the lead's session it could hand work only to the
     project's other roles, never to a lens. A review that wants the
     lenses is a session opened in the Container's worktree.
   - Codex reads `AGENTS.md` from the Git root down to the working
     directory (learn.chatgpt.com, "AGENTS.md"), so a role's session
     opened in a Container's worktree reads the Standard's guide.
   - ChatGPT is not listed. OpenAI's developer-mode page names "SSE and
     streaming HTTP", a server URL and tools only; whether it reaches a
     server on localhost is not said, and a hosted endpoint is out of
     scope. OpenAI's route is Codex.
7. **A runtime without subagents plays one role per session.** `theos next`
   hands out one work order for one role; the session does it, says `theos
   done` and ends. The review is a fresh session that has not seen the
   implementer's reasoning, and `next` never offers a session the review of
   a change it built. The session's name is its own word (`THEOS_SESSION`),
   so this is the method, not a fence, and the playbook says so.
8. **The Git host is the coordination point, and a claim is three things
   on it, each for what it settles.**
   - *The Task's branch is the lock.* Its name is the one a Crew's Task
     gets, `task/<container>-<issue>` (`model.TaskBranch` of
     `model.IssueTaskName`), the same for every session. `theos claim`
     creates it at `main`'s head. `CreateBranch` fails on a branch that
     exists, so of two sessions that claim a free issue at once the host
     lets one through, and the other takes another issue. Only an account
     that may push can take the lock.
   - *Marker comments are the lease, and they are never edited.* `claim`
     (session, role, lease in minutes), `renew` and `release` are each a
     new comment. `model` reads them oldest first, by the host's creation
     time and then the comment's ID. A claim holds from its comment's time
     for its lease. A `renew` by the holder before that time extends it
     from the renewal's time; one that comes later counts for nothing, so
     a claim that ran out is never renewed, only claimed anew. A `claim`
     by another session counts only once the holder's time plus a grace
     period has passed. Every time compared is one the host stamped on a
     comment, never a session's clock. Why not one comment that is edited,
     as #267 offers: an edit moves a stamp after others have read it,
     which is how a slow holder would beat the session that took over.
   - *`status/in-progress` is what people and a Crew read.* Not a new
     `status/claimed`: a Crew and a session must read one state.

   Markers count only from the accounts in `claimants`, by default the
   credential's own account. Anyone may comment on a public repository,
   and a stranger's marker is ignored. A takeover is for a branch that
   exists with no claim alive: it ran out, was released, or its session
   died before its first marker. The session posts a `claim`, reads the
   issue until its own comment shows, and works only if the rules above
   name it; it continues on the branch, as a Crew's later try continues an
   open pull request. The lease is 60 minutes and the grace period 5.
   `theos` posts a `renew` when a call for the claim finds under half of
   the lease left, and a role calls `theos heartbeat` after half an hour of
   work with no other `theos` call: about one line of comment per half
   hour. `next` offers what `NextCrewStep` would start (ready, criteria
   present, no open blocker, urgent first) and an issue whose claim ran
   out, by the session's clock as a hint; the claim decides, by the
   host's. It never offers an issue that is `status/in-progress` with no
   marker that counts: that is a Crew's Task or a person's. One git
   worktree per claim, under the project repository's `work/`, which git
   ignores.
9. **The agents' credential decides how far the host's rule binds them.**
   `theos` reads a credential from `THEOS_GIT_TOKEN`, else from `git
   credential fill` for the remote, which asks whatever helper the
   person's git already has; it never stores one. By default the sessions
   run with the person's own, the one that ran `theos protect`. That
   account can merge and can merge past the rule, so no rule binds such a
   session, and `theos status` says `AgentHoldsOwnerCredential`. The
   narrower credential is the token of a second account of the person's,
   with write access and no administration: then the host keeps the
   sessions off `main` and off the merge. The AgentAccess port is not
   used: on Gitea it makes bot accounts with a site administrator's token,
   which a person on a Gitea they don't run doesn't hold, and on GitHub it
   needs an App.
10. **A person merges. The host's rules merging is step 11, and `merge:
    rules` is refused, `RulesMergeNotBuilt`, until it is built.** Why not
    now: no host can be asked for "a review by another account" through
    `BranchRule`; the account that may merge is the protecting one, which
    is the one no rule binds; and with the narrower credential a session
    cannot merge at all. So "the lead merges what the rule passes" holds
    under neither credential today. A person merges in one of two ways:
    - `theos merge <pull request>` from their own terminal. It calls the
      adapter's `MergePullRequest`, the code that holds checks and
      protected paths in the cluster, and first refuses a pull request
      with no approving verdict on its head from a session other than its
      author's. `theos mcp` has no merge tool and `theos merge` refuses
      away from a terminal: a speed bump for an agent, not a fence.
    - the host's own pages, where on GitHub nothing checks for them.

    Step 11 makes `rules` real: `ports.BranchRule` gains required
    approvals and the accounts that may merge; the Gitea adapter sets
    them; the GitHub adapter's ruleset requires the review and the checks
    whose names are exact, with no bypass for the agents' account; and
    `rules` is refused, `OneIdentity`, while agents and person share an
    account. Why a person first: where the platform can't tell the agent
    from its owner it enforces no review, and theos's quality rests on
    what is enforced.
11. **Hosts: Gitea and Forgejo, and GitHub, now; GitLab without a cluster
    next; Bitbucket not.** The first two have adapters that pass the
    contracts. GitLab is a new adapter for the subset below (step 9).
    GitLab as a cluster's host leaves #269 for an issue of its own, filed
    to follow #57: a cluster's host must pass every port's contract (CI
    runs and runners, AgentAccess, the Registry, Events), which took #57
    nine steps that are not all done, and #269 itself puts it "after #57
    lands". Until then a GitLab project cannot move to a cluster where it
    is. Bitbucket waits for someone who needs it. The host is never
    guessed: `github.com` and `gitlab.com` are read from the remote, and
    any other address needs the GitProvider's `provider`.
12. **What can be configured is configured, in the model where a field
    exists.** The provider, its URL and owner, the registry and the npm
    scope are the GitProvider's fields, with the defaults they have today
    (`@theos`); templates are catalog entries at pinned refs, which `theos
    init` writes to name the theos repository at the release's tag with
    `template.path`, and any entry may name another URL.
    `model/settings.yaml` holds the rest, each with a default that needs
    no cluster: the project repository's name; `lease` (60 minutes),
    `grace` (5) and `claimants`; `merge` (`person`); and the operator's
    four flags under their own names (`sessionFeature`, `sessionBinaryURL`,
    `ciImages.go`, `ciImages.base`) with the flags' defaults.

**What no-cluster needs of a host** is this subset of the ports, which
becomes a named contract, `contract.NoCluster`: repositories and the first
commit, files, branches, the branch rule, pull requests with their files,
merges and statuses (GitHost); labels, issues, dependencies and comments
(Issues), plus two things the port lacks today: listing an issue's
comments with each one's ID, author and creation time, and getting the
comment back from a write, as `TrackerComment` has it. Not needed: editing
a comment, AgentAccess, CI runs and runners, the Registry, Events, tags
and releases.

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
| The operator's merge | a person merges, with `theos merge` or in the host's pages | `PersonMerges` |
| A credential per agent and repository | the person's own, unless a second account's is given (decision 9) | `AgentHoldsOwnerCredential` |
| A review no merge skips | no host is asked for one; `theos merge` wants a verdict from another session, and a session's name is its own word | `ReviewNotEnforced` |
| Required checks on GitHub, held by the operator's merge | held by `theos merge`; by the person's eye in the host's pages | `ChecksByMerge` |
| Protected paths on GitHub, held by the operator's merge | the same | `ProtectedPathsByMerge` |
| CEL rules, enums and the admission policy | a strict decode, the CRDs' defaults and `internal/model`'s checks; the rest runs when the model reaches a cluster, or in `kubectl-validate` | `RulesNotValidated` |

**What the Git host still holds,** for an account its rule binds: `main`
takes no push, and a change arrives as a pull request. On Gitea and Forgejo
the host also refuses a merge with a red check or a changed protected
path. On GitHub those two are `theos merge`'s. The protecting account can
merge past the checks and the paths on either host, which is why the
default says `AgentHoldsOwnerCredential`. There is no gated Standard
change without the operator: a Standard's update and an adoption arrive
as pull requests a person reads.

**Cluster-only, and staying so:** Agent Sandbox and the fence, the model
proxy, Sessions, checkpoints, Crews with their schedules, routes and
budgets, Briefs' rounds and autopilot, releases and deploys, test
environments, the CI runner pools, alerts, chat and the UI. Without a
cluster a release is a tag a person pushes.

## Who merges

A person, through step 10. Per role:

| Role | Seat | What it may do |
| --- | --- | --- |
| Lead (the operator's and the planner's part) | the project repository | plans, files issues, hands out work orders, opens pull requests; never merges |
| Implementer, designer, spec writer | a worktree of a Container repository | commits on its Task's branch; never merges, never gives a verdict |
| Reviewer | a fresh session or subagent | writes its verdict as a comment on the pull request's head; `theos merge` reads it, the host does not |
| Triager, verifier | a fresh session or subagent | change nothing on `main` |
| Person | their terminal, or the host | merges everything |

After step 11, under `merge: rules`, the lead's `theos merge` merges what
the host's rule passes with the agents' own account, the reviewer's verdict
is a review from an account the rule counts, and a person still merges what
touches a protected path, a Standard's update and an adoption.

## One source of truth with the cluster

- **To a cluster.** `theos export` prints the two model files as one
  stream, with `adopt: true` on every Container that has a repository
  (`adopt` can't change later) and a Crew that holds every change for a
  person, as `theos-session adopt` proposes. What the move takes depends
  on where the repositories are:
  - *On a Gitea or Forgejo the cluster reaches, in place.* The person
    makes the Secret the GitProvider names, with a site administrator's
    token (the operator makes the agents' bot accounts with it), then the
    Agents, and applies the stream. The repositories need no commit: each
    has its `.theos.yaml` and its Standard's files. Issues, pull requests
    and reviews stay where they are. Sessions release their claims first:
    a Crew starts `status/ready` issues only. The operator's rule then
    keeps the merge to its own account.
  - *Onto the chart's own Gitea.* [adopt.md](../adopt.md) from its step 3:
    the person pushes each history. Issues, pull requests, reviews and
    claims do not follow a push; open issues are filed again or finished
    first.
  - *On GitHub, in place:* after #81. *On GitLab:* not until GitLab is a
    cluster's host.
- **From a cluster.** `theos import` reads a model exported with `kubectl
  get -o yaml` and writes the project repository; the Container
  repositories already hold everything. The host must be one the person's
  machine reaches with a credential that administers the repositories. The
  person pauses the Crew, runs `theos protect` so the rule names their
  account, and labels what a Task left `status/in-progress` as
  `status/ready`.
- **Not both at once.** A Crew and outside sessions on one Container would
  be two schedulers on one queue. A person keeps them apart; the one thing
  that checks is the label: a Crew starts only `status/ready` issues, and
  a session never takes a `status/in-progress` issue without a marker.
  Between a session's lock and its label there is a moment neither sees.
  An outside agent joins a Crew through #260.

## Steps

Each is an issue; "tier" is AGENTS.md's. No step needs Kind or `make e2e`.

| Step | Issue | Needs | Tests it brings |
| --- | --- | --- | --- |
| 1. `cmd/theos`: `init`, `scaffold`, `protect`, `status` | #268, reshaped | | tier 1: the tool and the reconciler get one tree from `internal/scaffoldinput` for the webshop sample; a model that leaves a defaulted field out renders the tree of one that spells it; a strict decode refuses an unknown field, and an unknown `provider` by name; unset settings give the operator's flag defaults; the credential's lookup order; on the ports' fake, one first commit with the operator's trailers and a protected `main`; a second `scaffold` pushes nothing (`model.ScaffoldHistory`), and a Relationship added later reads `AddedAfterScaffold` in `status`; a test like `taskfacts`'s `TestImports` keeps controller-runtime and client-go out |
| 2. The method bundle and `theos agents` | #266, reshaped | 1 | tier 1: both sets of files render every role and the lead playbook; pinned: the reviewer's verdict is never delegated and its subagent file lists no `Agent` tool, an implementer never merges or gives a verdict, issue text is data, every work order names the Container's `AGENTS.md` and `theos heartbeat`; every reason word of the loss table is in the playbook |
| 3. Claims, leases and `next` | #267, reshaped | 1 | tier 1: `model`'s claim table over a list of markers (the first claim holds; a renewal in time extends it; a late one counts for nothing; a takeover inside the grace period is ignored and one after it holds; a release frees; a marker from an account outside `claimants` is ignored; order is creation time, then ID); `next` offers no blocked issue, none without criteria, no `status/in-progress` issue without a marker, and no session its own review; on the fake and `githubtest`: of two claims of a free issue one `CreateBranch` goes through; the comment capability's contract subtests (a written comment comes back with its ID, author and the host's time; the list is whole and oldest first; a claimant reads until its own comment shows); tier 3: the same on Gitea in `make test-gitea` |
| 4. `review` and `merge` by a person | new: "A person merges without the operator" | 2, 3 | tier 1: `model`'s merge table (no verdict, the author's own session's verdict and a verdict on an older head are refused; `rules` reads `RulesMergeNotBuilt`); `merge` away from a terminal is refused; on the fake and `githubtest`, a pull request merged, one with a red check refused and one that touches a protected path refused; tier 3 on Gitea |
| 5. `theos plan` | new: "Plan from an idea without a Brief" | 1 | tier 1: a refused plan files nothing and prints `CheckPlan`'s refusals; an applied one files its issues in `FilingOrder` with their dependencies; filing twice files once |
| 6. `theos mcp` | new: "The local tool as an MCP server" | 2 to 5 | tier 1: an in-process client lists and calls every tool on the ports' fake, over stdio and over HTTP on localhost; there is no merge tool; the playbook and a work order come back as tool results and as prompts; every result fences issue text; no result carries the credential |
| 7. `update`, `export`, `import` | new: "Move a project to a cluster and back" | 1 | tier 1: `update` pushes what the operator's Standard update pushes for the same trees; `export` puts `adopt: true` on every Container that has a repository; tier 2: an exported Gitea project under `config/samples/` passes `make validate-yaml` (a GitHub one joins with #81); tier 4: envtest adopts a repository the tool scaffolded on the fake host, `Ready`, the Standard recorded, no pull request opened |
| 8. Hosts: detection and `contract.NoCluster` | #269, reshaped | 1, 3, 4 | tier 1: the remote's host read or refused, never guessed; Gitea's fake and `githubtest` pass the named subset; every catalog entry `init` writes renders for the detected host's CI, none ends in `UnsupportedCIError` |
| 9. GitLab without a cluster | new: "GitLab for no-cluster" | 8 | tier 1: a GitLab adapter passes `contract.NoCluster` on a fake GitLab; `model.CI` and its host table gain GitLab CI's row with a table test; each Standard gains its GitLab CI file under `.theos/ci/`, and a render test shows a GitLab Container gets it with no `UnsupportedCIError`; the Standards' versions are bumped and docs/templates.md follows; tier 2: `make validate-yaml` with the new pins |
| 10. The guide, `docs/no-cluster.md` | new | 2 to 4, 6, 8 | `make doc-links`; the guide parts #266 to #269 ask for: one Task in Claude Code with subagents, two sessions on one repository, connecting the MCP server, each host and how its credential is found; a person walks one Task in Claude Code and one in a single-role runtime, with the host's pages as evidence, and the guide lists an MCP client only after a person connected it |
| 11. The host's rules merge | new: "The host's rules merge, for agents with an account of their own" | 4 | tier 1: the GitHost contract's new subtests on the fake and `githubtest` (the host refuses a pull request without another account's approval and merges one with it under the agents' credential; the agents' account can neither push to `main` nor merge a changed protected path); `model`'s merge table gains `rules` and `OneIdentity`; tier 3 on Gitea; tier 4: the operator's own merge is unchanged under the wider rule |

**Protected paths.** These parts are the owner's session's; a Crew's agent
given the step says so and stops (AGENTS.md).

- Steps 1 and 2: the depguard rules for the new packages, in
  `.golangci.yml`. Step 2 also: the marker block's move out of
  `internal/agentrunner/`.
- Step 9: the chart's template pins, in `dist/chart/`.
- Step 11: `internal/adapters/gitea/gitea_agents.go`,
  `internal/adapters/github/github_host_rules.go`, and
  `internal/controller/protection.go` if the operator's rule changes.
- Any step that adds a make target: `Makefile`.

## The related issues

| Issue | What becomes of it |
| --- | --- |
| #266 the lead's strategy | reshape into step 2: the bundle lives in the project repository; its source is `internal/method` and the Standards' guides; `CLAUDE.md` is one import line; the guide moves to step 10 |
| #267 several sessions | reshape into step 3: the Task's branch as the lock, marker comments never edited as the lease, `status/in-progress`, a 60-minute lease, the two comment capabilities on the Issues port |
| #268 the CLI and MCP server | reshape into step 1 and split: `plan` is step 5, `mcp` step 6, `agents` step 2, claims step 3; the model is `model/model.yaml` and `model/setup.yaml`, not `.theos.yaml` |
| #269 any Git provider | reshape into step 8: detection, the subset's contract, and the credential from `THEOS_GIT_TOKEN` or `git credential fill` in place of each host's CLI; GitLab without a cluster is step 9; GitLab as a cluster's host and Bitbucket leave it |
| #270 Containers in one repository | out of scope here: it changes the model for the cluster too and needs its own plan; no-cluster follows what the model allows |
| #260 an agent outside the cluster | keep, its own plan; it should reuse step 3's markers and lease and step 2's work order, so file it after them |
| #259 subagents per role | keep as filed; step 2 writes role subagents in the project repository and changes none of its files |
| #57 GitHub as a host | keep; no-cluster uses its adapter as it is, and only a move to a cluster on GitHub needs #81 |
| #58 more harnesses | keep; it is about what runs in a sandbox, and shares one rule with this plan: `AGENTS.md` is the one guide |

**To file:** steps 4, 5, 6, 7, 9, 10 and 11; "GitLab as a Git host for the
cluster", to follow #57; and "CI files for the catalog entries that ship
Gitea's alone" (`hello-html`, `k8s-manifests`, `gitea-release`), which
#269 asked for and step 8 only works around.

## What this plan does not cover

- A sandbox for no-cluster agents. The runtime's own is all there is.
- A hosted MCP endpoint. `theos mcp` speaks stdio, and HTTP on localhost
  for a client that speaks nothing else; a server on the internet that
  holds a person's Git credential is another product.
- Releases, deploys, test environments, cost and the UI without a cluster.
- Bitbucket; GitLab as a cluster's host; Containers sharing a repository
  (#270).
- The CRDs' CEL rules in the local tool.
- A signed release file of the binary: it touches `.github/` and `hack/`,
  both protected.
- CI files for the catalog entries that ship Gitea's alone: `init` leaves
  such an entry out for another host (step 8's test).

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
  against the bundle. What decision 6 says of them is their documentation
  as read on 2026-11-14; the render tests hold the files, and step 10's
  walk by a person holds that a runtime follows them.
- That ChatGPT reaches `theos mcp` on localhost, and that Cursor, Kimi or
  Gemini show MCP prompts: unverified. Step 6 serves everything as tool
  results so that nothing rests on prompts, and step 10 lists a client
  only after a person connected it.
- GitHub lists an issue's comments "ordered by ascending ID" (its REST
  documentation). Gitea's order was not read here, so the rules sort by
  creation time and ID themselves. That a host which lists a session's
  own comment lists every earlier one too is assumed, not shown: it
  matters for two takeovers of one run-out claim in the same moment, and
  never for a first claim, which rests on the branch. Step 3 reads the
  hosts' documentation on it first, and has a taker read once more before
  its first push.
- Step 11's host settings (Gitea's required approvals and merge list,
  GitHub's ruleset rules for a review and for checks) are named from the
  review and from memory, not from the hosts' documentation: step 11
  opens with that reading.
- Rulesets and the administrator's bypass on a personal GitHub account,
  which is what one developer has: #57 asks for a run on a test
  organization. Step 10's walk is on a personal account.
- Step 6 uses the official MCP SDK for Go
  (github.com/modelcontextprotocol/go-sdk) over writing the protocol by
  hand, and first checks, with `go list -deps` and its documentation, that
  it brings neither client-go nor controller-runtime and serves HTTP as
  well as stdio.
- Forgejo runs on Gitea's adapter untried, until a person points `make
  test-gitea` at its binary.
