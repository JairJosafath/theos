# theos without a cluster

GitHub issue #265, written 2026-11-14. The plan, not the build: every step
below is an issue to file or reshape.

## Goal

One developer or a small team gets what theos knows (the C4 model, the
pinned templates and Standards, the role guides, and the loop from issue to
branch to pull request to review to merge) inside the agent tool they
already use, on the Git host their code already lives on, with no cluster,
no chart and no operator. What such a project makes is what a cluster with
the same settings makes, so a Crew can take it over later, and a project a
cluster made can be worked without one.

## Read this first

- **A person merges every pull request until step 11.** `merge: rules` is
  refused, `RulesMergeNotBuilt`, until then, and step 11 builds it for
  Gitea and Forgejo alone, for a person who makes three accounts there:
  on GitHub it stays refused, `HostHoldsNoPaths` (decision 10).
- **A claim is written on the issue:** a claim comment, about one renewal comment per half hour of work,
  and a release. A session that dies between its branch and its first comment leaves a lock a person
  clears (`LockWithoutClaim`, decision 8).
- **By default the agents hold the person's own credential.** A session can then do on the host what that
  token can: merge any pull request past the checks, the protected paths and the review, change or remove
  the rule itself, and write to every other repository the token reaches, with no expiry theos sets.
  `theos status` says so, and a second account's token narrows it (decision 9).
- Every choice that depends on where a person runs theos is a setting whose
  default needs no cluster (decision 12).

## The related issues, and where the plan departs from them

| Issue | What becomes of it | The departure, and the decision with its reason |
| --- | --- | --- |
| #266 the lead's strategy | step 2; its guide is step 10 | the bundle lives in the project repository, not in each Container's (3, 5) |
| #267 several sessions | step 3 | the lock is the Task's branch under the operator's name, not an assignment or `task/<issue>-<slug>`; the lease is marker comments never edited, not one edited comment or a push; the label is `status/in-progress`, not `status/claimed` (8) |
| #268 the CLI and MCP server | step 1; `agents` is step 2, claims step 3, `plan` step 5, `mcp` step 6 | the model is three files under `model/` in a project repository, not `.theos.yaml` (2); ChatGPT is not a listed MCP client (6) |
| #269 any Git provider | step 8; GitLab without a cluster is step 9 | the credential is `THEOS_GIT_TOKEN` or `git credential fill`, not each host's CLI (9); GitLab as a cluster's host gets an issue of its own, and Bitbucket is not planned (11) |
| #270 Containers in one repository | out of scope | it changes the model for the cluster too and needs its own plan |
| #260 an agent outside the cluster | kept | its plan comes after steps 2 and 3, whose markers, lease and work order it should reuse |
| #259 subagents per role | kept as filed | step 2 writes role subagents in the project repository and changes none of its files |
| #57 GitHub as a host, #58 more harnesses | kept | no-cluster uses #57's adapter as it is, and only a move to a cluster on GitHub needs #81; #58 is about a sandbox, and shares the rule that `AGENTS.md` is the one guide |

## Decisions, and why

Code is cited as it is on main at fa3c409.

1. **No-cluster mode is a second runtime of the same method, not a second
   product.** The types, templates, labels, branch names, plan format and
   rules in `internal/model` and `internal/scaffold` are the operator's; a
   `theos` binary runs them where the cluster has reconcilers. Why: two
   sets of rules would drift, and a project must be able to move between
   the two.
2. **The model is the CRDs' manifests in two files, plus one file of
   settings,** in the project repository. `model/model.yaml` is the story:
   the six kinds a plan writes, as whole `theos.factory.io/v1beta1`
   manifests, fields a plan can't write included (`adopt`, `license`,
   `checkCommand`, `checkContexts`, `deploy`). `model/setup.yaml` is what
   a person makes and no plan may write: the GitProvider, the catalog
   entries and the Bundles. `model/settings.yaml` holds what no CRD does
   (decision 12). Not `.theos.yaml`, as #268 says: that is a repository's
   identity (`internal/scaffold/identity.go:32`). The manifests are read
   through `api/v1beta1`'s types and get their defaults from the CRD
   schemas, embedded in the binary and applied by the API server's own
   defaulting package: a list kept by hand would drift, and `go list
   -deps` of that package and of `api/v1beta1` lists no client-go and no
   controller-runtime. They are never read by the plan's parser, which is
   strict on a subset and would refuse those fields
   (`internal/model/brief.go:395-407`, `:516-522`). So
   a round is never a copy of `model/model.yaml`: `theos plan` renders
   `plan/model.yaml` from it and merges an accepted plan back object by
   object, which keeps what a plan can't write. A cluster does not take
   every such file as it is: its CEL rules, enums and admission policy
   don't run locally (`RulesNotValidated`), and it refuses `provider:
   github` until #81 and `gitlab` until GitLab is its host
   (`api/v1beta1/gitprovider_types.go:27`). The enum is the cluster's word
   that it can host there, so this plan does not widen it.
3. **A project has a project repository, the lead's seat,** named
   `theos-<project>` unless set. It holds `model/`, the plan rounds and
   the method bundle. Container repositories hold nothing new: each is
   what an operator with the same settings scaffolds, the settings being
   the four flags it renders with (`cmd/main.go:188-249`), kept in
   `model/settings.yaml`. Why: the method's files are the same for every
   Container, and a repository with no extra file needs no change to be
   adopted.
4. **A binary, `cmd/theos`, with an MCP server in it, over three new
   packages.** `internal/method` is the bundle of decision 5, pure.
   `internal/modelfacts` is the model's API objects as the plain values the
   rules take: the step to `scaffold.Input` and its layers, and a plan's
   objects there and back. It moves out of the Container and Brief
   reconcilers so that the operator and the tool call one function: a
   golden test can compare two outputs, but only one function can't drift.
   A reconciler collects the objects and hands them over; the function
   reads no cluster. `internal/nocluster` is the commands' logic and the
   MCP server, on ports. None imports controller-runtime, client-go or an
   adapter.
5. **The method bundle has one source, split by what varies.** What depends
   on the stack stays the Standard's, in each Container repository:
   `AGENTS.md` and `docs/agents/`. What is the same everywhere is new, in
   `internal/method`: the lead playbook, the roles and their boundaries,
   and a work order per role. `theos agents` renders it into the project
   repository; nobody keeps a copy by hand. The runner's prompts and a
   Brief's workspace are not reused: they speak to a sandbox run.
6. **Runtimes are first-class by mechanism, not by vendor.**

   | Mechanism | Runtimes | What the project repository gets |
   | --- | --- | --- |
   | Subagents | Claude Code | `AGENTS.md` (the lead playbook), `CLAUDE.md` (one line that imports it), `.claude/agents/<role>.md` per role |
   | An `AGENTS.md` reader | Codex, and any tool that reads `AGENTS.md` | `AGENTS.md`, `roles/<role>.md` |
   | An MCP client | any client that starts a local server over stdio or reaches HTTP on localhost | nothing: `theos mcp` serves the commands as tools, and the playbook and each work order as tool results, and as prompts where a client shows them |

   `theos agents` writes both sets of files every time: a team with a
   Claude Code session and a Codex one needs both. From each runtime's
   documentation:
   - Claude Code reads `AGENTS.md` by itself only with no `CLAUDE.md` in
     the working directory or above, so `CLAUDE.md` stays, as an import
     (decision 8 of [harnesses.md](harnesses.md)), and the work order
     names a Container worktree's `AGENTS.md`.
   - It finds project subagents by walking up from the working directory,
     and a subagent may spawn its own: a Standard's lens subagents (#259)
     are found only from a session opened in the Container's worktree, and
     the project's reviewer file lists its tools without `Agent`.
   - Codex reads `AGENTS.md` from the Git root down to the working
     directory. ChatGPT is not listed: OpenAI's developer-mode page names
     a server URL over "SSE and streaming HTTP" and does not say it reaches localhost, and a
     hosted endpoint is out of scope.
7. **A runtime without subagents plays one role per session.** `theos next`
   hands out one work order for one role; the session does it, says `theos
   done` and ends. A review is a fresh session, and `next` never offers a
   session the review of a change it built. The session's name is its own
   word (`THEOS_SESSION`), so this is the method, not a fence.
8. **The Git host is the coordination point, and a claim is three things
   on it.**
   - *The Task's branch is the lock:* a Crew's Task's name,
     `task/<container>-<issue>` (`model.TaskBranch` of
     `model.IssueTaskName`), which `CreateBranch` makes once: it fails on
     a branch that exists (`internal/ports/githost.go:403-405`).
   - *Marker comments are the lease, never edited.* `claim`, `renew` and
     `release` are each a new comment, ordered by the host's creation
     time, then ID: every time compared is the host's stamp, never a
     session's clock. A claim holds from its comment's time for its lease.
     The holder's `renew` before that extends it; a later one counts for
     nothing. Another session's `claim` counts once the lease and a grace
     period are past; the old holder's own needs no grace. Why not one
     edited comment, as #267 offers: an edit moves a stamp after others
     read it, so a slow holder would beat the session that took over.
   - *`status/in-progress` is what people and a Crew read,* not a new
     `status/claimed`: a Crew and a session must read one state.

   Markers count only from the accounts in `claimants`, by default the
   credential's own: anyone may comment on a public repository. Who works
   is one rule over what `theos claim` finds, in three cases that don't
   overlap:
   - *Another session's counted claim, alive:* taken. The session posts
     nothing and takes another issue.
   - *Counted markers, none alive* (run out, or released): a takeover. The
     session posts a `claim`, reads the issue until its own comment shows,
     and works only if the markers name it, on the branch as it is.
   - *No counted marker:* no comment has a time to compare, so the branch
     decides. With no branch and no `status/in-progress`, the session
     creates the branch: the one the host lets through posts the first
     `claim` and works, the one it refuses takes another issue. With a
     branch or the label, the issue is never taken: it is a claimer's
     first seconds, a session that died in them, a Crew's Task, a claim
     from outside `claimants` or a person's work, and nothing on the host
     tells these apart or ages them. `claim` and `next` refuse it,
     `status` lists it (`LockWithoutClaim`), and a person frees it: they delete the branch and set the label back to `status/ready`.

   So a first claimer need not read back: nobody may take its issue before
   its marker shows, nor while its lease runs. The price: a session that
   dies between its branch and its first comment leaves a lock for a
   person. The lease is 60 minutes, the grace 5; `theos` renews when a
   call finds under half the lease left, and a role calls `theos
   heartbeat` after half an hour without one. One git worktree per claim.
9. **The agents' credential decides how far the host's rule binds them.**
   `theos` reads it from `THEOS_GIT_TOKEN`, else from `git credential
   fill`, and never stores one. By default the sessions run with the
   person's own, the one that ran `theos protect`: it can merge past the
   rule, so no rule binds them (`AgentHoldsOwnerCredential`). It also reaches what the token reaches beyond this project, and only the runtime's own sandbox keeps it on the machine. The narrower
   credential is the token of a second account of the person's, with
   write access and no administration: then the host keeps the sessions
   off `main` and off the merge. The AgentAccess port is not used: on
   Gitea it needs a site administrator's token (`gitea_agents.go:207`), on
   GitHub an App.
10. **A person merges. `merge: rules` is refused, `RulesMergeNotBuilt`,
    until step 11 builds it, for Gitea and Forgejo.** Why not now:
    `ports.BranchRule` holds checks, protected paths and whether the
    protecting account may push, and cannot ask a host for a review
    (`internal/ports/githost.go:143-152`). The account that may merge is
    the protecting one on Gitea (`gitea_agents.go:69-70`) and an
    administrator on GitHub (`github_host_rules.go:274`), and no rule
    binds it: `ForceMergePullRequest` passes the rule on both. A person
    merges with `theos merge` from their terminal, or in the host's pages.
    On GitHub nothing checks for them there: checks and paths are held by
    the adapter's merge (`github_host_pulls.go:228-240`). `theos merge`
    calls that merge, and first refuses a pull request with no approving
    verdict on its head from a session other than its author's; a verdict
    is a marker, counted under `claimants`. `theos
    mcp` has no merge tool and `theos merge` refuses away from a terminal:
    a speed bump for an agent, not a fence. Why a person first: where the
    platform can't tell the agent from its owner it enforces no review,
    and theos's quality rests on what is enforced.

    Step 11 makes `rules` real: `BranchRule` gains required approvals and
    the accounts that may merge, and Gitea's adapter sets them beside the
    checks and paths its host already holds at the merge. The lead's
    `theos merge` then merges what the rule passes; a person still merges
    what touches a protected path, a Standard's update and an adoption.
    - *Three accounts.* Both hosts refuse an approval by a pull request's
      author, and no rule binds the protecting account. So `rules` needs
      the person's account, one that opens and merges pull requests, and
      one that reviews. `model/settings.yaml` names the last two; they are
      the person's own to make, and theos holds no account for anyone.
      With fewer, `rules` is refused, `OneIdentity`.
    - *Not GitHub.* No branch ruleset there holds protected paths
      (`github_host_rules.go:39-42`; GitHub's documentation gives path
      rules to push rulesets, for a private or internal repository), so an
      account the ruleset lets merge could merge a changed workflow
      without `theos merge`. `rules` stays refused there,
      `HostHoldsNoPaths`.
11. **Hosts: Gitea and Forgejo, and GitHub, now; GitLab without a cluster
    next; Bitbucket not.** The first two have adapters that pass the
    contracts; GitLab is a new adapter for the subset below (step 9).
    GitLab as a cluster's host leaves #269 for an issue of its own: a
    cluster's host must pass every port's contract, which took #57 nine
    steps that are not all done, and #269 itself puts it "after #57
    lands". Until then a GitLab project cannot move to a cluster where it
    is. Bitbucket waits for someone who needs it. The host is never
    guessed: `theos init` reads `github.com` and `gitlab.com` from the
    remote, asks for any other, and always writes `provider`.
12. **What can be configured is configured, in the model where a field
    exists** (AGENTS.md). The provider, its URL and owner, the registries
    and the npm scope are the GitProvider's fields, with today's defaults;
    templates are catalog entries at pinned refs, and any entry may name
    another URL. `model/settings.yaml` holds the rest, each with a default
    that needs no cluster: the project repository's name; `lease`, `grace`
    and `claimants`; `merge` (`person`) and, for `rules`, the two agent
    accounts; and the operator's four flags.

**What no-cluster needs of a host** is a subset of the ports, a named
contract, `contract.NoCluster`: repositories and the first commit, files,
branches, the branch rule, pull requests with their files, merges and
statuses (GitHost); labels, issues, dependencies and comments (Issues);
and three things the ports lack today (`internal/ports/issues.go:106`): an
issue's comments listed with each one's ID, author and creation time, the
comment back from a write, and the credential's own login, which Gitea's
rule names and a token alone does not carry (`gitea_agents.go:70`). Not needed: editing a comment,
AgentAccess, CI runs and runners, the Registry, Events, tags and releases.

## What no-cluster mode loses, and how each loss is said

Each loss has a reason word. `theos status` prints the ones that apply, the
lead playbook opens with them, the guide has this table, and every pull
request a session opens ends with a line naming them.

| The cluster gives | Without it | Said as |
| --- | --- | --- |
| The fence: a NetworkPolicy, a pod with no ServiceAccount token, a deadline | the runtime's own sandbox and permissions, whatever they are; theos adds none | `NoFence` |
| The model proxy: no model credential in a sandbox | the runtime holds its own subscription or key | `OwnModelCredential` |
| Checkpoints, resume and pause | a session that dies leaves its commits on the branch; the next claim continues there | `NoCheckpoint` |
| Recorded cost and tokens, the metrics, Scorers and Benchmarks | nothing is recorded; the record of the work is the host's: the issue, the pull request, its reviews | `CostUnreported` |
| The operator's merge | a person merges, with `theos merge` or in the host's pages | `PersonMerges` |
| A credential per agent that writes one repository, expires, and can neither push to `main` nor merge | the person's own, unless a second account's is given (decision 9): it merges past the rule, can change or remove the rule, and reaches every repository its token does | `AgentHoldsOwnerCredential` |
| A review no merge skips | no host is asked for one; `theos merge` wants a verdict from another session, and a session's name is its own word | `ReviewNotEnforced` |
| Required checks and protected paths on GitHub, held by the operator's merge | held by `theos merge`; by the person's eye in the host's pages | `ChecksByMerge`, `ProtectedPathsByMerge` |
| CEL rules, enums and the admission policy | a strict decode, the CRDs' defaults and `internal/model`'s checks; the rest runs when the model reaches a cluster, or in `kubectl-validate` | `RulesNotValidated` |

**What the Git host still holds,** for an account its rule binds: `main`
takes no push, and a change arrives as a pull request. On Gitea and Forgejo
the host also refuses a merge with a red check or a changed protected
path; on GitHub those two are `theos merge`'s. The protecting account can
merge past both on either host.

## One source of truth with the cluster

`theos export` prints the two model files as one stream, with `adopt: true`
on every Container that has a repository (`adopt` is immutable) and a Crew
that holds every change for a person, as [adopt.md](../adopt.md)'s model
does. `theos import` reads a model exported with `kubectl get -o yaml` and
writes the project repository.

| The move | What the person does | What stays behind |
| --- | --- | --- |
| To a cluster, in place on a Gitea or Forgejo it reaches | has the sessions release their claims (a Crew starts `status/ready` issues only); makes the Secret the GitProvider names, with a site administrator's token, and the Agents; applies the stream | nothing: issues, pull requests and reviews stay. The repositories need no commit where the cluster's four flags equal `model/settings.yaml`; where they differ, the Standard arrives as the operator's pull request, as for any adopted repository |
| To a cluster, onto the chart's own Gitea | follows adopt.md from its step 3 and pushes each history | issues, pull requests, reviews and claims; open issues are filed again or finished first |
| From a cluster | pauses the Crew, runs `theos import`, then `theos protect` so the rule names their account, and labels what a Task left `status/in-progress` as `status/ready` | the Crew, the Agents and every status |

On GitHub in place the move to a cluster waits for #81; on GitLab, for
GitLab as a cluster's host. **Not both at once:** a Crew and outside
sessions on one Container would be two schedulers on one queue. A person
keeps them apart, and the label is all that checks it: a Crew starts only
`status/ready` issues, and a session never takes a `status/in-progress`
issue without a counted marker. Between a session's lock and its label
there is a moment a Crew sees neither. An outside agent joins a Crew
through #260.

## Steps

Each is an issue; "tier" is AGENTS.md's. No step needs Kind or `make e2e`.

| Step | Issue | Needs | Tests it brings |
| --- | --- | --- | --- |
| 1. `cmd/theos`: `init`, `scaffold`, `protect`, `status` | #268, reshaped | | tier 1: `modelfacts` gives the webshop sample's tree and branch rule, and the reconciler's envtest specs pass on it (tier 4); a model that leaves a defaulted field out renders the tree of one that spells it; an unknown field and an unknown `provider` are refused; with a token alone the rule names the token's account; a second `scaffold` pushes nothing; a test like `taskfacts`'s `TestImports` keeps controller-runtime and client-go out |
| 2. The method bundle and `theos agents` | #266, reshaped | 1 | tier 1: both sets of files render every role and the playbook; the reviewer's subagent file lists no `Agent` tool and its verdict is never delegated; an implementer never merges; every work order names the Container's `AGENTS.md`; every reason word of the loss table is in the playbook |
| 3. Claims: `next`, `claim`, `heartbeat`, `release`, `done` | #267, reshaped | 1 | tier 1: `model`'s claim table has a row for each sentence of decision 8's lease and each case of its rule, a refused `claim` posting nothing; on the fake and `githubtest`, of two claims of a free issue one `CreateBranch` goes through, and the comment contract holds; tier 3: the same on Gitea |
| 4. `review` and `merge` by a person | new: "A person merges without the operator" | 2, 3 | tier 1: `model`'s merge table (no verdict, the author's own session's, one on an older head and one from outside `claimants` are refused; `rules` reads `RulesMergeNotBuilt`); `merge` away from a terminal is refused; on the fake and `githubtest`, a pull request merged, one with a red check and one that touches a protected path refused; tier 3 on Gitea |
| 5. `theos plan` | new: "Plan from an idea without a Brief" | 1 | tier 1: a refused plan files nothing and prints `CheckPlan`'s refusals; an applied one writes `model/model.yaml`, keeps `adopt`, `license`, `checkCommand` and `deploy`, and files its issues in `FilingOrder`; a model that carries those fields still plans; tier 4: the Brief's envtest specs pass on the moved mappings |
| 6. `theos mcp` | new: "The local tool as an MCP server" | 2 to 5 | tier 1: an in-process client lists and calls every tool on the ports' fake, over stdio and over HTTP on localhost; there is no merge tool; the playbook and a work order come back as tool results; every result fences issue text and none carries the credential |
| 7. `update`, `export`, `import` | new: "Move a project to a cluster and back" | 1 | tier 1: `update` plans what the operator's update plans for the same trees; `export` puts `adopt: true` on every Container that has a repository; tier 2: an exported Gitea project under `config/samples/` passes `make validate-yaml`; tier 4: envtest adopts a repository the tool scaffolded on the fake host, with no pull request opened |
| 8. Hosts: detection and `contract.NoCluster` | #269, reshaped | 1, 3, 4 | tier 1: the remote's host is read or asked for, never guessed; a `github.com` or `gitlab.com` address under another `provider` is refused; the ports' fake and `githubtest` pass `contract.NoCluster`; no catalog entry `init` writes ends in `UnsupportedCIError`; tier 3: Gitea passes the contract in `make test-gitea` |
| 9. GitLab without a cluster | new: "GitLab for no-cluster" | 8 | tier 1: a GitLab adapter passes `contract.NoCluster` on a fake GitLab; `model.CI`'s new row has its table test; a GitLab Container renders with its Standard's new CI file; tier 2: `make validate-yaml` with the new pins |
| 10. The guide, `docs/no-cluster.md` | new | 2 to 4, 6, 8 | `make doc-links`; a person walks one Task in Claude Code and one in a single-role runtime, with the host's pages as evidence |
| 11. The host's rules merge | new: "The host's rules merge on Gitea, for agents with accounts of their own" | 4 | tier 1: the GitHost contract's new subtests on the fake (no merge without another account's approval; with it the authoring account merges, and can neither push to `main` nor merge a changed protected path), and `githubtest` refuses the rule; `model`'s merge table gains `rules`, `OneIdentity` and `HostHoldsNoPaths`; tier 3 on Gitea; tier 4: the operator's own merge is unchanged |

**To file.** Steps 4, 5, 6, 7, 9, 10 and 11, and two issues beside them:
"GitLab as a Git host for the cluster", to follow #57, and "CI files for
the catalog entries that ship Gitea's alone". What each issue is filed
with beyond its row (the package rows, the code that moves, the settings
and what a step checks first) is in
[no-cluster/to-file.md](no-cluster/to-file.md).

**Protected paths,** the owner's session's; a Crew's agent given the step
says so and stops (AGENTS.md). Steps 1 and 2: the depguard rules for the
new packages, in `.golangci.yml`; step 2 also `internal/agentrunner/`.
Step 9: the chart's template pins, in `dist/chart/`. Step 11: `internal/adapters/gitea/gitea_agents.go`, `internal/adapters/github/github_host_rules.go` (its `ProtectBranch` refuses a rule with mergers), and `internal/controller/protection.go` if the operator's rule changes. Any
step that adds a make target: `Makefile`.

## Cluster-only, and not covered

- Cluster-only, and staying so: Agent Sandbox and the fence, the model
  proxy, Sessions, checkpoints, Crews with their schedules, routes and
  budgets, Briefs' rounds and autopilot, releases and deploys, test
  environments, the CI runner pools, alerts, cost, chat and the UI.
  Without a cluster a release is a tag a person pushes.
- Not covered: a hosted MCP endpoint, which would hold a person's Git
  credential on the internet; Bitbucket; GitLab as a cluster's host;
  `merge: rules` on GitHub; Containers sharing a repository (#270); the
  CRDs' CEL rules in the local tool; a signed release file of the binary,
  which touches `.github/` and `hack/`, both protected.

## Done when

- This plan and design.md's *Without a cluster* are merged, and the steps
  are filed or reshaped as the Steps section says.
- After step 4, one person takes an issue to a merged pull request on Gitea
  and on GitHub with no cluster, and `theos status` names every loss that
  applies. After step 7, envtest adopts a tool-scaffolded repository with
  no change to it.

## Not tested, and who can

Nothing here was run against a runtime or a host. What is said of them is
their public documentation as read on 2026-11-14.

- Claude Code (code.claude.com/docs/en/memory and /sub-agents) and Codex
  (learn.chatgpt.com/docs/agent-configuration/agents-md): the render tests
  hold the files, and step 10's walk holds that a runtime follows them.
- That ChatGPT reaches `theos mcp` on localhost, and that Cursor, Kimi or
  Gemini show MCP prompts: unverified. Step 6 serves everything as tool
  results so that nothing rests on prompts.
- GitHub lists an issue's comments "ordered by ascending ID"
  (docs.github.com/en/rest/issues/comments). Gitea's order was not read,
  so the rules sort by creation time and ID themselves. That a host which
  lists a session's own comment lists every earlier one too is assumed,
  not shown: a takeover rests on it, and a first claim, which rests on
  the branch, does not. Step 3 reads the hosts' documentation on it first,
  and has a taker read once more before its first push.
- That of two creations of one branch in the same moment a host lets one through: the contract and step 3
  hold it on the fake, `githubtest` and a real Gitea. For github.com it is not shown: its REST page for
  creating a reference lists 201, 409 and 422 and does not say which an existing one gets. A first claim
  rests on it, so step 10's walk has two sessions claim one issue there.
- An author's own approval is refused on GitHub by its page on approving a
  pull request with required reviews, and on Gitea by its API's code
  (`routers/api/v1/repo/pull_review.go`); no page of Gitea's was read that
  says so. Gitea's branch protection is from
  docs.gitea.com/usage/access-control/protected-branches.
- What a host's terms say of one person holding several accounts. github.com's allow one free machine
  account beside a personal one, which covers decision 9's second account there. A hosted Gitea's or
  Forgejo's were not read: step 11 reads them before `rules` asks anyone for three.
- Rulesets and the administrator's bypass on a personal GitHub account,
  which is what one developer has, and #269's "required check runs green
  on GitHub Actions and on GitLab CI": runs on the real host, a person's,
  in step 10's walk. Forgejo runs on Gitea's adapter untried, until a
  person points `make test-gitea` at its binary.
