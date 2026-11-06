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
  `/var/cache/theos/go/ci` from the node (`goCache`), writable: a job can
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

## Step 3 choices (#1140, wren-1140, 2026-10-22)

Decided before building, from the code as it stands after #1138 and #1139:

- **Picked by `GitProvider.spec.ci.giteaActions.runners`.** A new optional
  `ci` block whose only member is `giteaActions` (the member name
  [ci-system-input](done/ci-system-input.md) reserved; a CEL rule allows exactly
  one member once there is a second). Its `runners` list has one entry per
  label: `label`, `image`, `size` (idle pods kept, default 1), `resources`,
  `goCache` (bool: mount the project's cache). No block, no pool: the Helm
  runners serve as today. The root of the per-project Go caches is an
  operator flag `--ci-go-cache` (as `--sandbox-go-cache`), the pods
  mounting `<root>/<project namespace>`; empty root, no cache mount. The
  root is `/var/cache/theos/go/projects` in the manifests and the chart,
  a sibling of the shared runners' `go/ci`, so the shared `go` runner never
  mounts a project's cache.
- **The ProjectReconciler keeps the pool** (`project_ci_runners.go`), owning
  the pods (`Owns(&corev1.Pod{})`, controller reference to the Project so
  deletion cascades) and reconciling on GitProvider spec changes, which it
  already watches. Pods are named by `GenerateName` `ci-<provider>-<label>-`
  and labelled `theos.factory.io/ci-runner: "true"`, `theos.factory.io/git-provider`
  and `theos.factory.io/ci-label`. The pool counts pods not ended (Pending or
  Running, no deletionTimestamp) per provider and label, creates up to
  `size`, deletes ended ones, and deletes pods of a provider or label that is
  no longer in any `ci` block. RBAC: pods `create;delete` added to the
  Project's markers (the Task's already has get/list/watch).
- **The registration token is a Secret per GitProvider** in the project's
  namespace, `c4-ci-runner-<provider>`, key `token`, written by the
  reconciler from `OwnerRunners.RunnerRegistrationToken` (through
  `providerFor`, `container_checks.go:63`); a provider without
  `OwnerRunners` fails the Project's condition `CIRunners` with reason
  `UnsupportedCI`. The init container alone gets it
  (`secretKeyRef`), with `RUNNER_EPHEMERAL=1` and `RUNNER_NAME` from the
  downward API (`metadata.name`), so Gitea names the runner after the pod.
  `hack/runner/register.sh` adds `--ephemeral` when `RUNNER_EPHEMERAL` is set
  (`register_test.sh` case).
- **The pod spec is a pure function** `buildRunnerPod(runnerPodInput)` in
  `internal/controller/ci_runner_pod.go`, table-tested as `buildSandbox` is:
  `restartPolicy: Never`, `automountServiceAccountToken: false`, the init
  container `register`, the `go-cache` init container when `goCache`, the
  `runner` container with no `RUNNER_TOKEN`, `work` and `tmp` emptyDirs in
  memory, the cache as `hostPath` `<root>/<namespace>` `DirectoryOrCreate`
  and the `job.env` variables in ConfigMap `c4-ci-runner-env` per project,
  mounted as the runner's `env_file`, as the chart's shared runners have
  (the runner reads `env_file`, not its environment, so the literal env first
  proposed here was not kept).
- **The CI NetworkPolicy** `c4-ci-runner` selects
  `theos.factory.io/ci-runner: "true"`; `ensureAgentNetwork`'s egress build
  moves into `fenceEgress(gitNamespaces)` so both policies share it,
  table-tested.
- **Who cancelled (the folded-in #1261).** `gitprovider.WorkflowJob` and
  `model.CIJob` gain `RunnerName` (Gitea's `runner_name`; the fake's jobs name
  their runner). Each pool pod carries the finalizer
  `theos.factory.io/ci-runner-record`, so a pod deleted by anyone stays, with its
  final status, until the pool removes the finalizer `RunnerRecordKept`
  (2h) after it ended; the Task reads the pod by the job's runner name. The
  pool classifies a pod the first time it sees it ended, in a pure function
  `runnerPodFate(pod)`, and writes the verdict as annotation
  `theos.factory.io/runner-fate`: `lost` when the pod has a deletionTimestamp
  and no verdict yet (someone deleted it before it ended), `podDisrupted`,
  phase Failed or a runner container that ended non-zero; `ended` when phase
  Succeeded. The pool annotates before it deletes an ended pod, so its own
  delete never reads as someone else's. The Task's `canceller`
  (`task_controller.go`) looks each cancelled job's runner name up as a pod
  of its namespace: `CIJob.PodFate` ended means a person cancelled, lost the
  runner, and a job with no pod keeps today's online rule. A pod gone past
  `RunnerRecordKept` reads as unknown, today's rule.
- **Helm's shared runners** register to the templates organization: the
  Gitea credentials container makes Secret `gitea-runner` from
  `POST /orgs/templates/actions/runners/registration-token` instead of a
  random instance token (`deployment.yaml:66`); `hack/seed/seed.sh:288` does
  the same for the seed's token.
- **envtest.** No kubelet: a test moves a pod's status by the status
  subresource (Succeeded, Failed, Evicted) and deletes pods; an unscheduled
  pod with no finalizer goes at once, one with the finalizer stays, which is
  what the record relies on.

Order of work: the API block and `make manifests`; `buildRunnerPod`,
`fenceEgress` and their tables; `register.sh`; the pool reconciler, Secret,
policy and envtest; `RunnerName`, `PodFate`, `Canceller` tables and the Task
envtest; the chart and seed; `docs/design.md`.

## Step 4 choices (#1141)

- **`freshGoCache` on a runner of the `ci` block,** a bool that excludes
  `goCache` (a CEL rule on `CIRunnerSpec`): `buildRunnerPod` gives the pod
  the same `go-cache` init container (`c4-go-cache /cache/go`, which seeds an
  empty cache from the image's) and job.env as a `goCache` pod, but the
  volume is an emptyDir on the node's disk (16Gi limit; the warm cache is
  about 3 GB, too much for memory), not a hostPath, and it needs no
  `--ci-go-cache`. A field, not a rule on the label's name, so a user who
  names the label otherwise still gets it.
- **`go-release` is a pool label only:** `runners.go-release` in the chart's
  values has `enabled: false` (no shared Deployment: the templates
  organization releases nothing) and `pool.size: 1` on the `go` image, and
  `theos.gitProviderCI` writes `freshGoCache: true` for it.
- **gitea-release v1.9.0** moves the release job to `runs-on: go-release`;
  a Container pinned to an earlier tag keeps running releases on `go`.

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
