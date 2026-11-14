# theos without a cluster: what each issue is filed with

The step-level text of [the plan](../no-cluster.md), for #265. A step's
issue is filed or reshaped with its row of the plan's table (its tests),
the decisions it builds, and its paragraph here. Code is cited as it is on
main at fa3c409. A paragraph goes once its issue carries it.

**Step 1, `cmd/theos`** (#268; decisions 2 to 4, 9, 12). Import rows, for
docs/architecture.md and docs/dev/structure.md: `internal/method`, the
standard library and `internal/model`; `internal/modelfacts`,
`api/v1beta1`, apimachinery, `internal/model`, `internal/scaffold` and
`internal/ports`, plain Go (`.golangci.yml`, `ports-are-plain-go`) that
the moved code uses (`FetchTemplate`, `SameHost` and `DefaultNpmScope`,
`internal/controller/container_scaffold.go:381-395`, `:486-487`);
`internal/nocluster`, what `internal/sessionclient` may, and the API
server's defaulting package; `cmd/theos` imports adapters, as only
`cmd/main.go` does today. Moved into `modelfacts`: what `resolve`,
`scaffoldInput`, `fetchLayers` and `resolveLicense` work out, on objects
the caller read. `protect` builds its rule from what
`internal/controller/protection.go` builds the operator's from
(`model.Protected`, `model.AgentProtectedPaths`, the CI's check contexts,
`model.ReleaseTags`); that file stays. The
schemas are embedded by a Go file beside `config/crd/bases/`. Unverified,
and checked first: that `make manifests` and `make check-generated` leave
that file alone. An object is defaulted untyped, then decoded strictly;
its namespace is the Project's name. `init` writes the catalog entries to
name the theos repository at the release's tag with `template.path`, and
`git-credentials` as the name of the Secret, which no-cluster never reads.
The Gitea adapter's rule names `Credentials.Username`
(`gitea_agents.go:70`), which a token alone does not carry, so the port
gains the credential's own login, as GitHub's adapter already asks for it
(`github_host.go:255`). The binary comes by `go install` at the release's
tag.

**Step 2, the method bundle** (#266; decisions 5 to 7). A work order holds
the issue between its markers, the branch, the Container's `AGENTS.md` and
role guide to read, when to call `theos heartbeat`, and how to say done.
The block that fences an issue's text (`issueBlock`,
`internal/agentrunner/prompt.go:64`) moves to `internal/model`, so both
runtimes use one. The playbook's plan format is rendered from
`internal/model/planner.md`, not written twice, with decision 2's start of
a round in place of the Brief's "a copy of `model/model.yaml`".

**Step 3, claims** (#267; decision 8). `next` offers what `NextCrewStep`
would start (ready issues whose dependencies are closed, an urgent one
first; `internal/model/crew.go:276`) and an issue whose claim ran out, by
the session's clock as a hint: the claim decides, by the host's. A project whose sessions use several accounts
lists them all in `claimants`, and `status` names the markers it did not
count. `theos release --lock`, from a terminal only, deletes a lock with
no counted marker and no commit `main` lacks. Worktrees are under the
project repository's `work/`, which git ignores. The comment contract: a
written comment comes back with its ID, author and the host's time; the
list is whole; a claimant reads until its own comment shows.

**Step 5, `theos plan`** (new; decision 2). Moved into `modelfacts`, out
of `internal/controller`: the API's objects as `PlanObject`s
(`storyObjects`, `brief_workspace.go:456`), and a plan's object onto an
existing one (`storyObject`, `copyStoryText`, `brief_apply.go:373`,
`:429`); `renderWorkspaceModel` is exported. `theos plan` renders
`plan/model.yaml`, checks the round with `ParsePlan` and `CheckPlan`,
writes `model/model.yaml` and files the issues. With no Brief and no Crew,
`CurrentModel` has the Project's name as `Namespace`; `Objects`,
`Catalog`, `Bundles` and `GitProvider` from the two model files;
`NewPrompt` true; `Designer` true and every Container in `CrewDesigners`
(a session can always play the designer); no `BriefContainers` (an
existing Container's `checks` stays the person's edit); and every existing
Container in `Released` (the tool knows of no release, so nothing checks
that a Container another one uses has a tag). `plan/releases.yaml` and
`plan/parallel.yaml` are printed for the person, and nothing acts on them.

**Step 6, `theos mcp`** (new; decision 6). The official MCP SDK for Go
(github.com/modelcontextprotocol/go-sdk) over writing the protocol by
hand, once `go list -deps` and its documentation show it brings neither
client-go nor controller-runtime and serves HTTP as well as stdio.

**Step 7, `update`, `export`, `import`** (new). `update` plans a
Standard's bump with `scaffold.RenderLayer`, `PlanUpdate` and `PlanTake`,
which the operator's update calls
(`internal/controller/container_standard.go:343`, `:417`, `:492`), and
opens a pull request a person merges. The ref the files are at, which a
cluster keeps in status, is read from the history, from the first commit's
`Scaffolded-From` trailer on (`scaffold.ParseCommitMessage`). Unverified,
and checked first: that an update's commit names its ref in a form that
reads back. A GitHub sample joins `make validate-yaml` with #81.

**Step 9, GitLab** (new; decision 11). Each Standard gains its GitLab CI
file under `.theos/ci/`, its version is bumped and docs/templates.md
follows.

**Step 10, the guide** (new). The parts #266 to #269 ask of it: one Task
in Claude Code with subagents, two sessions on one repository, connecting
the MCP server, each host and how its credential is found; and the loss
table. It lists an MCP client only after a person connected it.

**Step 11, the host's rules merge** (new; decision 10). A host that cannot
hold the protected paths refuses a rule with mergers, which is GitHub's
answer. Gitea's settings, as its documentation names them: required
approvals, the approvals allowlist, the merge allowlist and protected file
patterns. Unverified, and read first: that an allowlisted account without
administration cannot merge past a red check or a protected file, that an
author may merge once another account approved, and what a host's terms
say of one person holding three accounts.

**Two issues beside the steps.** "GitLab as a Git host for the cluster",
to follow #57: every port's contract on a fake GitLab, and `provider:
gitlab` last, as #81 is for GitHub. "CI files for the catalog entries that
ship Gitea's alone" (`hello-html`, `k8s-manifests`, `gitea-release`,
`factory-self`), which #269 asked for and step 8 only works around: a
render test per entry for GitHub Actions, the version bumps, `make
validate-yaml`.
