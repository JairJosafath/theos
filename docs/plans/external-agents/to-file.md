# An agent outside the cluster: what each issue is filed with

The step-level text of [the plan](../external-agents.md), for #260. A
step's issue is filed with its row of the plan's table (its tests), the
decisions it builds, and its paragraph here. Code is cited as it is on
main at 13bf6f6. A paragraph goes once its issue carries it.

**Step 1, the `external` harness** (decisions 1, 4, 5, 10). `image` is
required today (`api/v1beta1/agent_types.go:38-41`). It becomes optional
and gains `omitempty`, under a rule that asks for it unless the harness
is `external`. The compat test counts a newly required field as a break,
not this (`test/compat/compat_test.go:178-181`). `resources` is a struct
a typed client always sends (`agent_types.go:88`), so the rule reads its
fields. `timeoutMinutes` and `sessionTokenLimit` are defaulted
(`agent_types.go:70-84`) and mean nothing for an external Agent.
`leaseMinutes` is 10 to 1440 and `graceMinutes` 1 to 60.

**Step 2, the ports** (decision 7). New files beside the adapters, which
leave `gitea_agents.go`, `github_host_agents.go` and
`github_host_rules.go` as they are; `EnsureAgentAccess` is never called
for a named account. On GitHub the account must first accept an
invitation: until then the read says none and the Task waits,
`AccountNotInvited`. The GitProvider's status gains the operator's own
login, which step 6's client is told. Whichever of step 2 and #268 lands
first adds the port's call for the credential's own login
([no-cluster/to-file.md](../no-cluster/to-file.md), step 1), and the other
uses it. Checked first, on a real Gitea at the version the chart pins
(`dist/chart/values.yaml:587`): what the call for an account's access
answers, to whom, and what it says of a site administrator.

**Step 3, the markers** (decisions 3 and 14). The table is #267's with
rows added: an offer is a counted marker from the operator's account, and
an offered issue with no live claim is no-cluster's second case (counted
markers, none alive), taken with a `claim` on the branch as it is. The
offer's text is rendered from `internal/method`'s work order (#266), and
its last line is the loss words. The lease and the grace of an offered
run are the Agent's, which the offer states: the lease a `claim` carries
(#267) is not read there.

**Step 4, the operator's path** (decisions 3, 7, 8 and 11 to 13).

- *Reading markers.* Through the comment contract #267 adds to the Issues
  port: each comment's ID, author and the host's time, and the comment
  back from a write. Step 4 is a reconciler's first use of it, on the
  fake, Gitea and GitHub as #267 leaves them.
- *Preparing a run.* What `runInputs` does for a sandbox
  (`internal/controller/task_actions.go:395-444`): it waits for the
  Standard on `main` (`:400-402`), brings the host's rules up to date
  before an agent gets access (`:413-419`), makes the bot and its Git
  Secret (`:424`) and reads the issue (`:429-441`). An outside run skips
  only the Agent's model credential (`:404-408`).
- *Giving and taking access.* The record is written before the access is
  given and cleared after it is removed, so a stop between the two leaves
  an account to remove, never one the operator takes for somebody else's.
  The Container's reconciler hears of a Crew's change only when its last
  release changed (`container_controller.go:500`, `lastReleaseChanges`);
  it comes to hear of the spots and of the Agents. Its comments say a
  Container carries no finalizer (`:113-114`, `:175`), and its reconcile
  of a deleted one only forgets (`container_controller.go:195-199`). The
  new finalizer goes on before the first account is recorded, as
  `holdForBot` goes before a bot (`brief_workspace.go:165-183`), and
  comes off after the last record is cleared, so it is on only while an
  account is listed; on a deletion it follows `deleteBot`'s rule
  (decision 13).
- *Waking.* The hosts' hooks already deliver `issue_comment`
  (`gitea_events.go:40`, `github_host_events.go:49`);
  `--external-recheck` covers a delivery that never came. While a Crew's
  `pauseRuns` holds, no offer is posted: a pause starts no agent
  (`internal/model/task.go:1320-1324`).

**Step 6, the client** (decision 14). The client counts an offer only
from the operator's login it was given (`THEOS_OPERATOR`), and reads the
lease from the offer. Without the login it is no-cluster's client, and a
claim it posts on an issue the operator never offered counts for nothing
with the operator (decision 3).

**Step 9, the guide.** The three things a person does on the chart's
Gitea, in order, with the port-forward of
[getting-started.md](../../getting-started.md). The scopes the agent's
token needs on Gitea to push and to comment on an issue: `repository` and
`issue`, both to write. The loss table. The walk is a person's, on a
cluster of theirs, and the guide says of a host only what the walk saw.

**Protected paths,** the owner's session's; a Crew's agent given the step
says so and stops (AGENTS.md). Steps 1, 2, 4 and 5: `dist/chart/`, where
`make manifests` writes the CRDs' copies and the policy has its copy.
Step 4: `dist/chart/` also for the flag's value; its path is a file of
its own, and `task_sandbox.go` and `protection.go` are not changed. Step
7: `internal/agentrunner/`, which the parser leaves. Steps 3 and 4, if
the controller's import of `internal/method` needs a depguard row:
`.golangci.yml`. Any step that adds a make target: `Makefile`.

## What was read

Public pages and Gitea's source on its main branch, on 2026-11-14.
Nothing was run against a host or an agent.

- **Kubernetes.** "All validation rules are scoped to the current
  object: no cross-object or stateful validation rules are supported"
  (kubernetes.io, *Extend the Kubernetes API with
  CustomResourceDefinitions*): decision 5. "Service accounts authenticate
  with the username `system:serviceaccount:(NAMESPACE):(SERVICEACCOUNT)`"
  (*Authenticating*), and a kubelet has "a username of
  `system:node:<nodeName>`" (*Using Node Authorization*): decision 9.
- **k3s.** Its kubeconfig "grants access as the `system:admin` user and
  `system:masters` group" (docs.k3s.io/cluster-access): decision 9.
- **Gitea.** Collaborators "could have `Read`, `Write` and `Admin`
  permissions", and write on issues and on pull requests is "Add labels,
  assign, close" (docs.gitea.com/usage/permissions). A token's `issue`
  scope covers the `issues/*`, `labels/*` and `milestones/*` routes and
  `repository` the rest of `/repos/*`
  (docs.gitea.com/development/oauth2-provider): the loss table. In the
  source: an issue is edited by its poster or an account that may write
  issues (`routers/api/v1/repo/issue.go`); the access levels are `none`,
  `read`, `write`, `admin` and `owner` (`models/perm/access_mode.go`); a
  site administrator and the repository's owner read as `owner`
  (`models/perm/access/repo_permission.go`); and the call for an
  account's access answers a site administrator, an administrator of the
  repository and the account itself
  (`routers/api/v1/repo/collaborators.go`): decision 7. The chart's
  version was not read, which is why step 2 checks first.
- **GitHub.** An invited collaborator gets a notification "which they
  must accept or decline"; the `permission` read "provides the legacy
  base roles of admin, write, read, and none"; and after a removal "a
  user can still have access to the repository through organization
  permissions" (docs.github.com/en/rest/collaborators/collaborators):
  decisions 7 and 13. Write includes "Edit anyone's issue titles and
  bodies", labels, closing and assigning (*Repository roles for an
  organization*): the loss table. "Had your username @mentioned" is a
  default subscription (*About notifications*): decision 14. Gitea's
  notice of a mention was not read.
- **Claude Code.** "repository cloning in Anthropic-hosted environments
  and pull request creation require GitHub", with a repository on another
  host sent to a self-hosted environment; a session clones and pushes
  through the GitHub connection its person made, and its replies on a
  pull request are "posted using your GitHub account"
  (code.claude.com/docs/en/claude-code-on-the-web): decision 6.

**Unverified, and who verifies it.**

- Gitea's read of an account's access on the version the chart pins, a
  site administrator's included: step 2, first, on a real Gitea.
- That Gitea tells a mentioned account, and that a Claude Code cloud
  session works a Crew's repository: a person's walk (step 9), the
  second once #81. `theos next` does not rest on the first.
- The order of an issue's comments, and that a list is whole: #267's
  ([no-cluster.md](../no-cluster.md), *Not tested*).
