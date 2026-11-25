# theos in Claude Code, with no cluster

This guide works a project from a Claude Code session, on a laptop or as a
cloud session on the web, with no Kubernetes anywhere. The session is the
lead: theos tells it what is next, Claude Code subagents play the
implementer and the reviewer, and a person merges. The model files, the
branches, the markers and the labels are the same ones a Crew uses on a
cluster, so the project moves to one unchanged.

[plans/lead-runtime.md](plans/lead-runtime.md) has the design and why; this
page is how to use it.

**Status.** Built and tested at tier 1 on the ports' fake host and GitHub's
fake: the engine, the lead runtime on the Git host, `theos agents`,
`theos mcp`, `theos merge` and `theos plan`. Not yet walked by a person in a real Claude
Code session, on a laptop or on the web: [What is not tested
yet](#what-is-not-tested-yet) lists what that walk settles.

## 1. Get theos and its credential

Download `theos-<os>-<arch>` (Linux or macOS, amd64 or arm64) from the
[releases page](https://github.com/JairJosafath/theos/releases), check it
against the release's `SHA256SUMS`, and put it on your `PATH`:

```sh
VERSION=X.Y.Z    # a release that has the theos binary (#318)
curl -fsSLO "https://github.com/JairJosafath/theos/releases/download/v$VERSION/theos-linux-amd64"
curl -fsSLO "https://github.com/JairJosafath/theos/releases/download/v$VERSION/SHA256SUMS"
sha256sum --ignore-missing -c SHA256SUMS
install -m 0755 theos-linux-amd64 ~/.local/bin/theos
```

Or build it at a release's tag with Go:

```sh
go install github.com/JairJosafath/theos/cmd/theos@v$VERSION
```

A binary built from a checkout with `go build` names no release, so
`theos init` then needs `-ref`.

The credential is yours: `THEOS_GIT_TOKEN`, with `THEOS_GIT_HOST` naming
the one host it is for, else whatever `git credential fill` gives for the
host of the project repository's `origin`. theos never stores or prints
it, sends it to that host alone, and hands git none: a claim's clone asks
git's own credential helper.

## 2. The project repository and its model

The project repository holds the model; each Container gets a repository
of its own. In a new, empty project repository whose `origin` is on your
Git host:

```sh
theos init -project shop            # model/model.yaml, model/setup.yaml, model/settings.yaml
```

`model/model.yaml` is the story: write the SoftwareSystem, its Containers,
DataStores and Relationships there, as on a cluster
([design.md](design.md)), or have a session plan it from an idea with
`theos plan` ([section 7](#7-plan-from-an-idea-theos-plan)). `model/setup.yaml` has the GitProvider, the
catalog at the release, and two Agents, `implementer` and `reviewer`, on
`claude-code`. At its end is a Crew left as a comment: once the story has
a Container, uncomment it and name the Container.

```yaml
apiVersion: theos.factory.io/v1beta1
kind: Crew
metadata:
  name: orders-api
spec:
  containerRef:
    name: orders-api
  implementer:
    name: implementer
  reviewer:
    name: reviewer
  parallel: 1
```

Then:

```sh
theos scaffold     # each Container's repository, its first commit as an operator renders it
theos protect      # main takes changes through pull requests that pass the Standard's checks
theos agents       # CLAUDE.md, AGENTS.md, .claude/agents/theos-*.md and .mcp.json
theos status       # what is scaffolded, what this runtime doesn't honour, the losses
git add . && git commit -m "The model and the lead's files" && git push
```

`theos agents` writes the lead's files for Claude Code (`theos agents
-harness codex` writes Codex's instead, [harness-authoring.md](harness-authoring.md)): `CLAUDE.md`
imports `AGENTS.md`, the lead's playbook; each Agent a Crew names gets a
subagent in `.claude/agents/theos-<agent>.md` on its Agent's `model`, the
reviewer's without `Agent`, `Edit` or `Write`; `.mcp.json` starts
`theos mcp`. Run it again after a change to the Crews or Agents; `theos
status` names a file that changed by hand. A claim clones a Task's branch
into `work/<task>`, which ignores itself.

## 3. One Task in Claude Code on a laptop

File an issue on the Container's repository with an "Acceptance criteria"
checklist and label it `status/ready`. Open Claude Code in the project
repository and approve the `theos` MCP server it asks about. Then ask the
session to work the project: its `AGENTS.md` is the loop.

1. The lead calls `theos_next`. theos runs the Crew's tick, which creates
   the branch `task/orders-api-<issue>`, labels the issue
   `status/in-progress` and posts its progress, then the Task's tick, which
   posts the offer. `theos_next` returns it: the subagent to start,
   `theos-implementer`, and the work order, word for word.
2. The lead starts the subagent with the work order. The subagent calls
   `theos_claim` with its own session name, works in `work/<task>`, runs
   the Container's checks, pushes, and calls `theos_done` with the commit.
3. The lead calls `theos_next`: theos opens the pull request and waits for
   its checks; once they pass, it offers the review to `theos-reviewer`,
   which claims it and gives its verdict with `theos_done`.
4. Changes asked for start round 2, the reviewer's words in the
   implementer's next work order. An approval makes `theos_next` say the
   Task waits for a person, `PersonMerges`.
5. You merge, from your terminal:

   ```sh
   theos merge -task orders-api-<issue>
   ```

   or on the host's pages. The next `theos_next` ends the Task Merged and
   closes its issue.

`theos merge` runs only at a terminal, and `theos mcp` has no merge tool.
It merges only a pull request a session other than its author's approved
on its head, whose checks main requires passed on that head, and which
changes no protected path: theos holds these itself, on every host.

Each tool is also a command, for a person or a runtime without MCP:
`theos next`, `theos claim|heartbeat|release -task T -session S` and
`theos done -task T -session S (-commit SHA | -verdict approve|changes)
-summary TEXT`.

## 4. The same in a Claude Code cloud session

A cloud session is the same runtime: nothing lives on its machine, so a
session that goes away loses only what it didn't push, and the next one
picks up from the host.

- **The token.** Set `THEOS_GIT_TOKEN` and `THEOS_GIT_HOST` in the
  environment, the token as a secret where the environment keeps them,
  never in the repository: theos reads the variable, and a token the
  session's network adds to requests unseen is no credential theos can
  name a host for.
- **The binary.** The environment's setup script installs it:

  ```sh
  curl -fsSL -o /usr/local/bin/theos \
    "https://github.com/JairJosafath/theos/releases/download/v$VERSION/theos-linux-amd64"
  chmod 0755 /usr/local/bin/theos
  ```

- **The repositories.** Add the project repository and every Container
  repository to the session: the subagents clone and push the Containers'.
- **Pushing.** The implementer pushes `task/<task>` branches. Push after
  each green check: work that isn't pushed is gone with the container. A
  session that dies runs out its lease, theos posts `ended`, and the Task is
  offered again on its branch.

## 5. One lead per project

Run one lead per project. A second lead is safe: the host lets one creation
of a `task/` branch through, so it can't start a Task twice, and the claims
keep two sessions off one turn. Between one lead's list of the Tasks and
its start of one, the other may start one too, so the Crew can run one Task
over its `parallel`.

The lease, its grace and the accounts whose markers count are
`model/settings.yaml`'s `lease` (60m), `grace` (5m) and `claimants` (the
credential's own login). A session renews its claim with
`theos_heartbeat` when half an hour passes without a theos call.

## 6. What a session without a cluster loses

`theos status` prints, for each Crew, what this runtime does not honour of
it and its Agents, and, for the host, what working without a cluster
loses. Nothing is dropped silently.

| Reason word | What it says |
| --- | --- |
| `PersonMerges` | theos merges nothing: a person merges each approved pull request (`theos merge`, or the host's pages). A Crew's `approval` rules are not read. |
| `NoSandbox` | no sandbox is made: an Agent's `image`, `resources`, `env`, `credentialsSecretRef` and `timeoutMinutes`, and a Crew's `pauseRuns`, are not read. `image` stays, so the file moves to a cluster unchanged. |
| `NoScheduler` | nothing keeps a clock between commands: a Crew's `schedules`, `tagReleases`, `prepareReleases`, `releaseCadence` and `publishReleases` are not read. |
| `CostUnreported` | no cost or tokens are recorded: a Crew's `taskBudgetUSD` and an Agent's `sessionTokenLimit` are not read. |
| `RoleNotBuilt` | only the implementer and the reviewer play: a Crew's `triager`, `specWriter`, `specApproval` and `verifier` are not read yet (#337). |
| `HarnessNotBuilt` | only `claude-code` has its files written; any other harness gets the work tools over MCP alone. |
| `NoFence` | no NetworkPolicy, ServiceAccount or deadline holds a session: only its runtime's own sandbox. |
| `OwnModelCredential` | each session holds its own model subscription or key. |
| `NoCheckpoint` | a session that dies leaves its commits on its branch; the next claim continues there. |
| `AgentHoldsOwnerCredential` | see below. |
| `ReviewNotEnforced` | no host is asked for a review, and a session's name is its own word. |
| `ChecksByMerge`, `ProtectedPathsByMerge` | on GitHub, the host does not hold the required checks or the protected paths at a merge in its pages; `theos merge` does. |
| `RulesNotValidated` | the CRDs' create-time rules are checked; the admission policy and the rules that compare an update with the object before it run only when the model reaches a cluster. |

**The credential the sessions hold.** By default the sessions hold your own
credential, so a session can do on the host what that token can: merge any
pull request past the checks, the protected paths and the review, change or
remove the rule itself, and write to every other repository the token
reaches, with no expiry theos sets. `theos status` says so. A second
account's token, given to the sessions and named in `claimants`, narrows it.

## 7. Plan from an idea: `theos plan`

`theos plan` turns an idea into the model and filed issues, with a Brief's
plan format and its checks and no Brief, no Crew and no cluster. Tell a
session the idea and have it play the designer:

```sh
theos plan         # round 1: writes plan/model.yaml, the story of model/model.yaml
# the session writes the round in plan/:
#   plan/model.yaml                     the whole story the round leaves
#   plan/choices.md                     one line per choice it made
#   plan/issues/<nn>-<container>.md     front matter (title, labels, after), then the body
#   plan/summary.md                     what the round does, in a sentence or two
theos plan         # checks the round, writes model/model.yaml, files the issues
theos scaffold     # makes the repositories of the round's new Containers
theos plan         # files the issues that waited for them
git add model plan && git commit -m "Round 1" && git push
theos plan -new    # once every issue is filed: the next round starts
```

The format is the one a Brief's planner writes: *What you write* in [the
planner's guide](../internal/model/planner.md). Where the guide names a
Brief's files, the designer reads `model/setup.yaml` for the catalog and
the Bundles, and the Containers' repositories for their open issues; an
issue's body needs an `## Acceptance criteria` checklist. Run again,
`theos plan`:

- refuses a plan with every reason the Brief would give it, prints each as
  `refused: <object or file>: <reason>`, and writes and files nothing;
- stops, and files nothing, while `plan/needs-input.md` asks the person
  something;
- merges the plan into `model/model.yaml` object by object: an object the
  plan leaves alone stays as written, comments included; one it changes
  gets only its new `displayName`, `description`, `needs`, `kpis` or
  `checks`, so `adopt`, `license`, `checkCommand`, `deploy` and
  `operations` stay; one it makes is added at the end;
- files the issues blockers first (`after:`), each into its Container's
  repository once that has its first commit, and records each in
  `plan/filed.yaml`, so a second run files none twice;
- prints `plan/releases.yaml` and `plan/parallel.yaml`. Nothing acts on
  them: a release is a tag a person pushes, and how many Tasks a Crew runs
  at once is its `parallel` in `model/setup.yaml`.

With no cluster, the round is checked as a Brief's round that takes a new
prompt, where a session can always play the designer: a `status/design`
issue is allowed for every Container, and every existing Container counts
as released, so `uses:` of one is not checked for a tag. An existing
Container's `checks` stays the person's edit. A new Container goes on the
GitProvider of `model/setup.yaml`; a model with several, which its
Containers split between, is refused.

## What is not tested yet

These are the plan's open items; a person's walk of one Task on a laptop
and one in a cloud session settles them, with the host's pages as the
evidence in [screenshots/](screenshots/):

- that a Claude Code subagent started in the background can call the
  tools the main session's `.mcp.json` registers, and run its own `Bash`
  in `work/<task>`;
- that a Claude Code cloud session may push a `task/` branch, or what the
  person allows for it;
- that the host takes `THEOS_GIT_TOKEN` from a cloud session's environment
  as it does on a laptop.
