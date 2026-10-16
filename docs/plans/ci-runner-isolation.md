# Plan: isolate the CI runners that run the agents' code

GitHub issue #1076. Agent: otter-1076.

## Goal

A CI job that runs the agents' code gets a fresh pod of its own, reaches only
what a check needs (DNS, the Git host and its package registries, public
HTTPS), and can reach nothing that belongs to another job, another project or
the runner itself. `docs/design.md` says what isolates CI, as a rule any CI
adapter must meet, not only Gitea Actions.

Done when the steps below have closed: table tests show the runner pods and
the NetworkPolicy the operator builds (one pod per job, no registration token
in the job's container, egress only to DNS, the Git host and public HTTPS);
`make validate-yaml` checks the chart's runner fence; a `make test-gitea` test
registers an ephemeral, organization-scoped runner on a real Gitea; envtest
shows a project's runner pool kept and replaced; `docs/design.md` names the
rule and no longer calls the runner open. `make check` passes on each.

## Where we stand

- **Two shared runners in host mode.** `dist/chart/templates/gitea/runners.yaml`
  and `config/runner/{base,go}.yaml` run one long-lived Deployment per label
  (`base:host`, `go:host`) in Gitea's namespace, registered instance-wide with
  the token in Secret `gitea-runner`. Every project's jobs, and the templates
  organization's, run on them, one after another, in the same container.
- **A job sees what earlier jobs left, and what later jobs bring.** `/data`,
  `/data/work` and `/tmp` are emptyDirs that live as long as the pod; host mode
  runs the job as a process of the runner container, so a job that leaves a
  detached process behind (`setsid`, `nohup`) is still there when the next job
  starts, and can read that job's checkout, environment and secrets. The
  `gitea-release` template's release job runs on the same `go` runner, with
  the signing keys.
- **A job holds the runner's credentials.** `RUNNER_TOKEN`, the instance-wide
  registration token, is in the runner container's environment, so a job can
  read it and register a runner of its own that takes any project's jobs.
  `/data/.runner` holds the runner's own token, with which a job can fetch
  other jobs as the runner.
- **A job shares the build cache with every project.** The `go` runner mounts
  `/var/cache/c4factory/go/ci` from the node (`goCache`), writable: a job can
  plant a build result that a later job, of any project, links into its
  binary, a release included.
- **No NetworkPolicy selects the runners.** A job reaches the Kubernetes API,
  Gitea's internals, the operator's metrics and every other in-cluster
  service. The agent sandboxes are fenced (`ensureAgentNetwork`,
  `internal/controller/project_agent_network.go`); their CI is not.
- **The plan that named this left it open.**
  `docs/plans/done/2026-09-30-agent-factory-design.md:157`: "Later: ephemeral
  job containers, a runner per project, and a NetworkPolicy." `docs/design.md`
  says nothing about it.
- **CI is becoming a port** (#1093): its own adapter, credential path and
  contract suite. Runner isolation is what the Gitea Actions adapter provides
  (the owner's comment on #1076), so the rule is written for the port, and
  the operator-run runners live behind it.

## Decisions, and why

1. **The rule, for every CI adapter.** A job that runs code from a Container
   repository runs in an environment made for that job and thrown away after
   it; holds no credential beyond its own job token and the secrets its
   repository gave it; shares nothing writable with another project's jobs,
   nor with a release; and reaches only DNS, the Git host (and the registries
   it serves) and public HTTPS. `docs/design.md` states it next to the agent
   fence; the CI port's contract (#1093) carries it, so a second CI system
   meets the same rule or says which part it can't.
2. **A fresh pod per job, not job containers.** Gitea's runner can start each
   job in a container of its own, but only through a Docker daemon: a
   privileged Docker-in-Docker sidecar, which is a larger hole than host mode.
   Instead each runner takes one job and ends: registered `--ephemeral`
   (Gitea 1.24 and later; the factory runs 1.27.3), in a pod with
   `restartPolicy: Never`. The job's processes, files and memory go with the
   pod. A Deployment can't do this: it restarts an ended container in the
   same pod, emptyDirs kept, with back-off between restarts. So the operator
   keeps the pods, as Actions Runner Controller does for GitHub: a pool of
   idle single-job pods per label, each ended pod deleted and replaced. The
   pod's spec is built by a pure function and checked in table tests, as the
   sandbox's is.
3. **The registration token never reaches the job's container.** An init
   container registers (`gitea-runner register --ephemeral`) and writes
   `/data/.runner`; only it gets the token. The runner container mounts
   `/data` and never has `RUNNER_TOKEN`. Its own runner token is ephemeral:
   Gitea drops it when the one job ends, so a job that copies it gets
   nothing it can use after its own job. Step 1 moves the shared runners'
   registration into an init container already, so the instance-wide token
   leaves the job's reach before the pool lands.
4. **A runner pool per project.** The operator registers each project's pool
   to the project's Git organization (the GitProvider's `owner`), with an
   organization registration token it reads through its admin token
   (`POST /orgs/{org}/actions/runners/registration-token`: Gitea 1.27 serves it
   on POST only), so a project's
   jobs run only on its own pods and a job's registration reach, if it ever
   leaked, is its own organization. The pods run in the project's namespace,
   next to its sandboxes, so the per-project machinery (the namespace's
   Secrets, its NetworkPolicy, its deletion) covers them. Helm's two shared
   runners stay for the templates organization's own CI, registered to that
   organization only.
5. **The network fence, built like the sandbox's.** A NetworkPolicy on the
   runner pods: no ingress; egress to DNS, the Git host's namespace (the
   in-cluster Gitea, as `ensureAgentNetwork` finds it from the project's
   GitProviders) and TCP 443 to addresses outside the private ranges (the Go
   proxy, npm, pub, PyPI). Gitea's Go registry, which `go-standard` points
   `GOPROXY` at, is served by Gitea, so it is covered by the Git host rule.
   The Kubernetes API and every other in-cluster service stay out. Step 1
   puts the same fence on Helm's shared runners in the chart and in
   `config/runner/`, selecting `app.kubernetes.io/component: ci-runner`;
   egress to Gitea there is to the Gitea pod's port 3000 only.
6. **Build caches are per project, and releases get none that a job wrote.**
   A pool's `go` pods mount `<goCache>/<project>` from the node, not the
   shared directory, so one project's job can't plant a build result in
   another's. A release job builds signed artifacts, so it must not link a
   result that an agent's pull request job planted: the release workflow
   runs on its own label, `go-release`, whose pods start from the image's
   warm cache on an emptyDir and keep nothing. The `gitea-release` template
   moves to `runs-on: go-release`, a template change with a new tag, as any.
7. **Where the pool is picked.** Through the CI port #1093 defines: the
   GitProvider's `ci` block, its Gitea Actions member naming the labels,
   images and resources Helm's `runners.*` names today. Steps 2 to 4 wait
   for #1093 so they follow its conventions instead of deciding the port
   again.
8. **What only a real node shows** (not tested before release, AGENTS.md):
   that a pod's processes really die with it, that the NetworkPolicy is
   enforced (the CNI), and that a job queued with the pool empty is picked up
   once a pod is replaced. A human's Kind run checks those; nothing waits for
   it.

## Steps

| Step | Issue | Depends on | How we know |
| --- | --- | --- | --- |
| This plan | #1076 (`Refs`) | | it merges |
| Fence the shared runners: NetworkPolicy and registration in an init container, in the chart and `config/runner/`; `docs/design.md` states the rule (decision 1) and what is still open | #1138 | | `make validate-yaml` checks the rendered chart's runner NetworkPolicy and that no runner container has `RUNNER_TOKEN` |
| Gitea client: an organization's runner registration token, and a runner's state: `gitprovider.OwnerRunners`, an optional capability of the CI port, with its fake | #1139 | #1093 | `make test-gitea`: an ephemeral runner registered to an organization shows in that organization's runners and not in another's |
| The operator keeps a per-project pool of single-job runner pods, under a per-project CI NetworkPolicy and a per-project Go cache | #1140 | #1139 | table tests of the pod and NetworkPolicy specs; envtest: a pool kept at its size, an ended pod replaced, the pool gone with the project; `docs/design.md` drops "open" for the runner |
| Releases on `go-release`, a pool label with no shared cache | #1141 | #1140 | table test of the `go-release` pod spec (an emptyDir cache); `internal/scaffold` test that `gitea-release` runs on it; `make validate-yaml` for the template tag |

## Done when

All four steps have closed, each with `make check` passing, and
`docs/design.md` says what isolates CI as decision 1 puts it. #1076 closes
itself with its last step.
