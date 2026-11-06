# Plan: test environments, the tools a job needs, on the host or leased

Issue #2036, [theos#12](https://github.com/JairJosafath/theos/issues/12)
(reported on 0.30.0).

## Goal

Work that needs a tool the agent sandbox lacks (a browser and its system
libraries, a phone emulator, macOS, a game engine on a GPU) gets that tool
from the factory, not from a person. A **TestEnvironment** is a named set of
tools and resources, such as `browser`, `android`, `ios` or `godot`. A
Standard, a Framework or an issue can ask for one. The agent runs its tests
or picture script there while it works and commits what they produce, and
CI runs the same checks there, so the reviewer and CI see the same results.
A provider supplies each environment, configured per project: a pod on the
cluster first, and later a machine leased from AWS (EC2, including Mac and
GPU instances, and Device Farm for phones), behind a port like the Git
host's. A provider's credentials are Secrets only the operator reads, and
limits cap how many leases run, for how long and at what cost.
`kind/by-hand` is left for work that really needs a person.

## What happens today

- The reporter's VS Code extension makes its pictures with a script that
  drives VS Code in Playwright's Chromium. The sandbox image lacks
  Chromium's system libraries, so the implementer leaves the criterion
  open (*Evidence is never made up*) and the operator files a
  `kind/by-hand` issue. That happened three times in two days, and a
  person's commit can't land anyway, since `main` takes only the
  operator's pull requests.
- An Android app needs an emulator with `/dev/kvm`, an iOS app needs
  macOS, and a Godot game needs the engine and often a GPU. The same wall
  stands before each.
- CI has per-label runner pools (`GitProvider.spec.ci.giteaActions.runners`),
  but they serve CI only, and only what a pod on the host can do. The
  agent sandbox has one image per Agent and nothing else.

## Decisions

| # | Topic | Decision | Why |
| --- | --- | --- | --- |
| T1 | The kind | A new kind, `TestEnvironment`, group `theos.factory.io`, version `v1beta1` only, namespaced in a project's namespace and scaffolded with `kubebuilder create api --group theos --version v1beta1 --kind TestEnvironment --resource --controller`. Its name is what jobs ask for (`browser`). Its spec has a `description` (one line for people and the planner); one provider member, `local` first and `aws` later, with CEL allowing exactly one, as a DeployTarget's members do; `limits`; and `ci` (T8). Its status has `leases` (one entry per live lease: who holds it, since when, the provider's ID, the cost so far), `spentToday`, and the conditions `Ready` and `CredentialsValid`. | What a project can run, and how, is a setting that differs per place (AGENTS.md, *What can be configured is configured*), and a DeployTarget already follows this shape: a named object per project, one member per provider, a credential Secret. A kind of its own lets the planner list a project's environments, lets a person add one with kubectl or GitOps, and keeps lease state where the operator can find it after a restart. Every kind is served at `v1beta1` alone. |
| T2 | Who asks | A Standard or a Framework lists names in `spec.testEnvironments`, and the Task's run gets each one the project has. A name the project lacks gives the Container `TestEnvironments=False`, reason `NotInProject`, and a needs-you item, and the runs go on without it. An issue asks with a label `env/<name>`, which the triager or planner sets. That one is required: a Task whose issue needs an environment the project lacks waits, reason `WaitingForEnvironment`, message `NotInProject`, costing no try, until a person adds it. | A Standard asks for every Container of its kind, and most of that work never needs the tool, so blocking every Task on it would stop a project. An issue that asks needs the tool for that work: running without it would only spend tokens on a criterion left open. |
| T3 | When a lease is held | One lease per agent run that needs it: the implementer's and the verifier's runs. The reviewer, triager, spec writer and planner get none. The operator leases before it makes the run's sandbox and returns the lease when the run ends, fails or is suspended (paused, agent unavailable, interrupted). A resumed run leases again. Waiting for a lease costs no round, as `WaitingForSlot` doesn't. | A Task spends most of its time in CI and review, and a remote lease costs money for every minute it is held. Environments carry no state between runs (T4), so letting one go and taking another loses nothing. |
| T4 | How the sandbox uses it | Commands run in the environment, on a copy of the sandbox's working tree. The environment runs `theos-env serve`, a small static binary (`cmd/theos-env`) that an init container copies in from the theos-agent image, so any image works. The sandbox gets `THEOS_ENV_<NAME>_URL`, `_TOKEN` and `_CA` for each lease, and runs `theos-env run <name> -- <command>`. A local lease is reached at its pod's address over plain HTTP inside the cluster, which the fence guards, so its `_CA` is empty; `_CA` pins a remote one's certificate. The lease's token reaches the environment pod and the sandbox each through a Secret, never a pod spec. That sends the tracked and untracked, not ignored, files as a tar (capped at 512 MiB), runs the command in `/work` (in the folder `run` was called from), streams its output and exit code back, and writes the files the command changed or added back into the sandbox's tree, where the agent commits them; the files the tree's `.gitignore` files ignore (`node_modules`, build output) stay in the environment, and a file the command deleted stays in the sandbox. Within a lease the environment's own processes keep running (an emulator booted once), and `/work` is replaced on each run. | It works the same for every provider (a pod, an EC2 machine over HTTPS, a Mac) and every tool (Playwright, Electron, an emulator, Godot). The sandbox image stays the Agent's, and the workspace and its checkpoint stay in the sandbox. A sidecar in the sandbox pod was rejected: it ties the sandbox's pod spec to each environment, puts devices into the agent's pod, and can't be remote. A remote browser (Playwright's `run-server`) was rejected: VS Code and Electron must launch next to the script. |
| T5 | The fence | An environment runs the agents' code, so it is fenced like a sandbox. Local: no ServiceAccount token, no user Secret, not privileged, devices only through the cluster's device plugins (T7). The project's NetworkPolicy `theos-test-environment` selects pods with `app.kubernetes.io/component: test-environment`: ingress only from the project's agent sandboxes on the `theos-env` port (not from CI runner pods, as first written: CI runs on the environment's image itself, T8, so no CI pod calls one), egress only to cluster DNS and public HTTPS (no Git host, no model proxy, no private ranges, `model.PrivateRanges`). The sandbox fence gets one egress rule more, to those pods on that port in its own namespace. Each lease has a token of its own, generated by the operator and given only to the run that holds the lease, so one Task can't use another's environment. Remote: reached over public HTTPS on port 443, the one port the sandbox fence lets out to a public address, with the environment's certificate pinned by `_CA` (the certificate itself, not a host name, since the machine's address comes after it is made). The machine gets no instance role and no SSH key, its metadata service is off (`HttpEndpoint=disabled`) before the lease is Ready, and its security group, the operator's own and its only one, lets in only `theos-env`'s port and lets out only DNS and HTTPS to public addresses. As first written, the metadata service was off at launch: a machine reads its user data from it, so one launched with it off never starts `theos-env` (step 7's *As built*). | The fence's rule is that agents' code reaches only DNS, the Git host and public HTTPS (*The fence*). An environment needs neither the Git host nor the model, since it gets the tree from the sandbox. A token per lease, rather than a NetworkPolicy per lease, keeps the policies fixed per project. An AWS machine with a role or the metadata service would hand the agents' code the user's cloud. |
| T6 | The port | `internal/ports/testenv.go`, `TestEnvironments`, plain Go: `Lease(ctx, LeaseRequest) (Lease, error)` (idempotent on the request's ID, the Task, run and environment), `Get(ctx, env, id) (Lease, error)` (`ErrLeaseNotFound` for none), `Return(ctx, env, id) error` (idempotent), and `Leases(ctx, env) ([]Lease, error)`, which lists the leases the provider holds for the environment, tagged with it, so the operator returns ones it lost track of. Each call names the `Environment` (namespace, name, UID, the member's settings, a remote member's Secret), which every provider needs to find a lease: as first written, `Get`, `Return` and `Leases` took only an ID or an owner. `Lease` has `ID`, `State` (`Pending`, `Ready`, `Failed`, `Ended`), `Endpoint`, `CA`, `Message`, `Holder`, `Started` and `CostMilliUSD`. The rules live in `internal/model/testenv.go`: which environments a run gets (T2), whether a lease may start (T10), the lease's ID and deadline, the variable names (T4), the port, labels and policy name the fence uses, whether an image is pinned and whether a lease is an orphan. `internal/model` imports no Kubernetes, so the env pod's spec is `localenv.BuildPod` and the policy's `testEnvPolicySpec` (`internal/controller/project_testenv_network.go`), each table-tested where it is built. The fake is in `internal/ports/fake` and the contract suite is `contract.TestEnvironments(t, env)`. `cmd/main.go` maps each member to its adapter, as it does for DeployTargets. Device Farm, which runs test packages rather than commands, is an optional capability, `TestRuns` (T12, `internal/ports/testruns.go`). | It is cut by what the controller asks (a ready place to run commands, and giving it back), not by one provider's API, the rule for every port (*Factory components*). `architecture.md` gets a row for it in step 1. |
| T7 | The local provider | `internal/adapters/localenv` (client-go, as runtime adapters may use): one Pod per lease in the project's namespace, built from `local.image` (a tag or digest, never `latest`, as `hack/check-pinned-images.sh` already checks), with `local.resources`, `local.devices` (extended resource requests whose names are the cluster's device plugins', such as `devices.kubevirt.io/kvm: 1` or `nvidia.com/gpu: 1`, a setting since plugins differ), `local.runtimeClassName`, `nodeSelector` and `tolerations`. `activeDeadlineSeconds` is the lease's deadline, as a backstop, and the Pod's ownerReference is the TestEnvironment. `theos-env serve` is the env container's command, whatever the image's entrypoint; `theos-env` comes from `--test-environment-tools-image` (the chart's `testEnvironments.toolsImage`, the release's `agent-base`), mounted read-only; `/dev/shm` is a 1Gi memory volume, which browsers need. Ready once `theos-env` answers its readiness probe. A Pod rather than a Deployment, because each lease is used once and thrown away, as a CI job's pod is. Cost 0. | It needs nothing outside the cluster, works on the chart's in-cluster default, and covers `browser` (no device) and later `android` (KVM) and `godot` (GPU) on a host that has them. A Deployment would restart a pod and let one lease see the last one's leftovers. |
| T8 | CI | A local TestEnvironment with `ci.size` set (0 by default, at most 8) adds a runner pool for the label `env-<name>` to the project's Gitea Actions pool. Its pods are built from the environment's image and devices, with the runner binary copied in by the init container that already registers (T4's way), and run under the CI fence and the env's devices. A Standard's workflow names `runs-on: env-browser`. The label `env-<name>` may not also appear in `GitProvider.spec.ci.giteaActions.runners`, and a clash makes the Project `CIRunners=False`, reason `LabelClash`. Remote environments in CI come later (step 8): a leased machine registers as an ephemeral runner when a job for its label waits. | CI must check what the agent produced on the same tools, or the reviewer judges results CI can't reproduce. Reusing the per-label pools keeps one runner mechanism, with its one-job pods, fence and backoff. The env's image needs nothing added for CI. |
| T9 | Credentials | `aws.credentialsSecretRef`, keys `accessKeyId`, `secretAccessKey` and optional `sessionToken`, as the `cloudFormation` DeployTarget uses, plus an optional `aws.account` that a credential of another account is refused against. Only the operator reads them, and they never reach a sandbox, an environment or a CI job. A local environment needs none. | The rule *A user's credential never reaches code the agents wrote*. The same keys as the existing AWS member, so a user who has one has the other. |
| T10 | Limits | `limits.maxLeases` (live leases at once, default 2, at most 16), `limits.maxLeaseMinutes` (default 180, the agent run's deadline, and a lease ends when it passes), and `limits.budgetUSDPerDay` (required by CEL on a remote member, unused for local). The cost so far comes from the provider (`CostMilliUSD`: on-demand price times time, with a Mac host's 24-hour minimum, T11). At `maxLeases` a run waits (`WaitingForEnvironment`, `AllLeased`). Past the day's budget no lease starts (`OverBudget`, a needs-you item), and leases held finish their run. | Limits are the user's, since they pay. A remote member without a budget could spend without bound, so CEL requires one. The run deadline already bounds an agent run, so the default lease lasts no longer. |
| T11 | AWS | The `aws` member, `internal/adapters/awsenv` on the AWS SDK the `cloudformation` adapter already uses: `region`, `instanceType`, `imageId` (an AMI with the env's tools; `theos-env serve` started from user data) and `subnetId`, all the user's settings; the security group is the operator's own (T5), not `securityGroupIds`, as first written, since a machine's groups add up and a user's could only widen the fence. Instances are tagged with the lease and owner, terminated on `Return`, and listed by tag for `Leases`. EC2 Linux, including GPU types, comes first. A Mac (`mac2.metal` and the like) needs a dedicated host with a 24-hour minimum, so the adapter keeps a host allocated across leases while the day's budget allows (`aws.mac.keepHostHours`, default 24) and counts the host, not the instance, against the budget. | EC2 covers GPU and macOS, which no ordinary cluster node has. Tags make leases findable after a restart without the operator's memory. A Mac host torn down after every lease would cost a full day each time. |
| T12 | Device Farm | Step 10, as the optional `TestRuns` capability of a member of its own, `deviceFarm` (`projectArn`, `devicePoolArn`, `account`, `credentialsSecretRef`, and a budget, as `aws` has; Device Farm serves us-west-2 alone). As first written, `theos-env test` went "through the operator", which the sandbox fence can't reach, and the credential must never reach the run; so the run asks over its Git host, which the fence lets it reach. `theos-env test <name> --type <type> --app <file> --tests <file>` pushes a request commit (the request, signed with the lease's token, the app and the test package; its parent the run's commit) to the lease's branch `theos-env/<lease>`, the lease's endpoint (`git:theos-env/<lease>`). The operator reads it on its poll, as it reads CI's checks, starts the run on the provider when the lease's token signed it and the day's budget allows, pushes the report and its artifacts to the same branch, where theos-env writes them into the run's working tree, and comments the outcome on the Task's pull request, where the reviewer and people see it. Android is served first by a local `android` environment with KVM (T7), which is cheaper. | Device Farm runs test packages, not commands, so it can't fit T4. A capability of its own keeps the port narrow. The Git host is reached already, its writes are the run's own, and a branch other than `main` and `release-check/**` starts no CI. Rejected: letting the sandbox reach the operator, which holds every credential; a pod with the credential that the sandbox reaches, a user Secret where the agents' code can reach it (T5); Device Farm's pre-signed upload addresses handed to the run, which still need a way back for them and for the report, the Git host again. |
| T13 | By hand only for a person | The implementer's and verifier's prompts list the run's environments, with each one's description and how to use it. The guides say that a criterion a listed environment can check is never "no sandbox can". The planner's and triager's workspaces list the project's TestEnvironments, and their guides say to label `env/<name>` where they would have filed `kind/by-hand` for missing tools. `kind/by-hand` stays for accounts, settings, hardware no environment has, and other people's work. | This is the issue's last point. The operator's by-hand filing is unchanged: a criterion still left open with its reason still goes to a person. |
| T14 | The chart's default | The seed makes a local `browser` TestEnvironment in each `gitea.projects` namespace (`testEnvironments.browser.enabled`, true by default), on the release's `env-browser` image, with `ci.size: 0`. | A fresh install handles the reporter's case with nothing added, and a local lease costs nothing until a run asks for it. CI pods stay off until a project's workflow names the label. |

The design's *Test tiers* table has a "Kind" row, and `docs/dev/testenv.md`
is "the test environment". Those mean the Kind cluster, not this kind. Step 1
says so in `docs/dev/testenv.md`'s first line.

## Steps

Each step is one pull request and leaves main working. Nothing leases before
step 2.

1. **The local provider with a `browser` environment.** The design:
   `docs/design.md` gets a section *Test environments* after *The CI
   fence* (T1 to T10, T13), a row in *Factory components*'s port table,
   and a sentence in *The fence* for the env pods. `docs/architecture.md`
   gets the port's row, and `docs/dev/testenv.md`'s first line says it
   means the Kind cluster. Then the code:
   - the kind, scaffolded with `kubebuilder create api`, with the `local`
     member and `limits`, then `make manifests generate`;
   - the port, its fake and contract suite (T6);
   - `internal/model/testenv.go` with the pod and NetworkPolicy specs;
   - `internal/adapters/localenv` (T7);
   - `cmd/theos-env`, serve and run (T4), shipped in the theos-agent
     image;
   - `hack/env-browser/Dockerfile`: Playwright's image pinned by digest,
     with Xvfb, published as `theos/env-browser` with the release;
   - the TestEnvironment controller: `Ready` when the image is pinned
     and the project's env NetworkPolicy exists; leases are returned when
     their holder is gone;
   - the project's `theos-test-environment` policy (T5);
   - `config/samples/` with a `browser` sample.
   Tests:
   - `internal/model` table tests: the pod spec has no token, isn't
     privileged, requests the devices, and has the deadline; the policy's
     ingress and egress match T5.
   - `cmd/theos-env` unit tests: a run sends the tree and brings back a
     changed file and a new file, passes the exit code, refuses a wrong
     token, and refuses a tree over the cap.
   - `contract.TestEnvironments` on the fake, and on `localenv` against
     envtest: a lease's pod is made, a second `Lease` with the same ID
     makes no second pod, `Return` deletes it, and `Leases` finds an
     orphan.
   - `make validate-yaml` on the sample, with the CEL failing cases (no
     member, two members, `latest`) in `test/invalid`.
   *Done when:* those tests and `make check` pass. Running Chromium in
   the image on a real node is for a human's run.

   *As built:* the pod spec's table tests are `internal/adapters/localenv`'s
   (`TestBuildPodIsFenced`, `TestBuildPodRunsTheosEnv`,
   `TestBuildPodPlacesAndRequestsDevices`, `TestLeaseOf`), not
   `internal/model`'s, which imports no Kubernetes, and the policy's is
   `TestTestEnvironmentFence` in `internal/controller/sandbox_fence_unit_test.go`.
   `internal/model/testenv.go` has the rules both use (`TestImagePinned`,
   `TestLeaseID`, `TestOrphanLease`). The TestEnvironment's name is a DNS
   label of at most 32 characters that starts with a letter (CEL), since it
   becomes `env/<name>`, `env-<name>` and `THEOS_ENV_<NAME>_*`. `Ready`'s
   reasons are `Ready`, `ImageNotPinned` and `FenceMissing`. The controller
   returns a lease whose holder Task is gone (step 2: one whose holder no
   longer lists it, a minute after it started). The CEL failing case "two
   members" waits for a second member (step 7); `test/invalid` has no
   member, `latest`, and a device that is no extended resource. The
   env-browser image adds `xvfb-run` and `xauth` to Playwright's, and starts
   no display: a headed program runs under `xvfb-run -a`, which is how VS
   Code's own extension tests run on Linux CI.
2. **Tasks lease their environments** (`internal/controller/task_*`,
   `api/v1beta1` for `testEnvironments` on Standard and Framework, the
   prompts). T2 and T3: a run's environments from its Standard, Framework
   and `env/<name>` labels; lease before the sandbox; the variables in
   its pod template; return at end or suspend; `WaitingForEnvironment`
   with `NotInProject`, `AllLeased` and `OverBudget`; the sandbox fence's
   extra egress rule; the Container's `TestEnvironments` condition; the
   prompts' list (T13). Envtests with the fake provider:
   - an issue labelled `env/browser` gets a lease and the variables in
     its sandbox;
   - the lease is returned when the run ends and when it is suspended,
     and a resumed run leases again;
   - an issue needing an environment the project lacks waits without
     spending a try, and starts once one is added;
   - a Standard's ask the project lacks sets the Container's condition
     and the run goes on;
   - at `maxLeases` a third run waits;
   - the reviewer's run gets no lease.
   Table tests: the sandbox spec has the variables, and the fence has the
   one new rule and nothing more.
   *Done when:* those specs and `make check` pass.

   *As built:* a run's leases are in the Task's `status.environments`
   (name, lease, sandbox, since), written before its sandbox is made; each
   lease's token is a key of Secret `<task>-test-environments`, which the
   Task owns, and the sandbox reads its own key by `secretKeyRef`. The
   Task waits before its run's progress moves on, so a waiting Task keeps
   its phase and spends no round; it looks again every 15 seconds
   (the Task reconciler's `EnvironmentRecheck`), with no watch on
   TestEnvironments. A lease that failed is returned and taken afresh on
   the next pass (`WaitingForEnvironment`, `Failed`), one starting waits
   `Pending`; `AllLeased` counts the provider's live leases, not the
   TestEnvironment's status. `OverBudget` is a rule of `model.LeaseMayStart`
   that no local member reaches: budgets come with the `aws` member (step
   7). A spec writer's run leases none, as T3 says, though its role is the
   implementer's. A final Task returns any lease it still lists, and the
   TestEnvironment controller returns a lease its holder no longer lists a
   minute after it started. The Brief lists `NotInProject` and
   `OverBudget` waits under Needs you at once, the others past the slot's
   four hours, and a Container's `TestEnvironments=False` as its Needs-you
   item. The prompt's list reaches theos-agent as `THEOS_ENVIRONMENTS`.
3. **CI on the environment** (T8, `project_ci_runners.go`,
   `ci_runner_pod.go`). Envtests: `ci.size: 1` keeps one `env-browser`
   runner pod built from the env's image and devices; a size of 0 keeps
   none; a label clash gives `LabelClash`. Table tests: the pod's CI fence
   and the runner binary copied in by the init container. `make
   validate-yaml`: the chart's runner fence covers the env pool.
   *Done when:* those tests and `make check` pass.

   *As built:* the register init container, which alone holds the token,
   also copies the runner, its configuration, the runner image's entrypoint
   and its /tmp sweep into a volume (`RUNNER_COPY_TO`,
   `hack/runner/register.sh`), and the job container runs that entrypoint
   on the environment's image (`RUNNER_BIN_DIR`, `entrypoint.sh`); the
   entrypoint is a bash script, so an image a pool runs on needs bash, as
   Playwright's has. The pods register with `ci.runnerImage`, else the
   image of the GitProvider's first runner. A size of 0 keeps the pool, at
   no pod, so a job a pod still runs ends before it goes. The pool's pods
   carry the CI runner label, so the project's own CI fence, which the
   operator makes (`ensureCINetwork`), selects them:
   `TestBuildEnvRunnerPod` checks it. The chart renders no project pool,
   only the shared runners and their fence, so `make validate-yaml` has
   nothing of the env pool to check, against what the step first said.
   Envtests: "CI runner pool of a test environment"; table tests:
   `TestBuildEnvRunnerPod`, `TestBuildEnvRunnerPodContainers`, `TestEnvRunnerPool`;
   `hack/runner/register_test.sh`.
4. **By hand only for a person** (T13, T14). The planner's and triager's
   workspaces list the project's environments, and their guides use
   `env/<name>`; the implementer's and verifier's built-in guides; the
   seed's `browser` default and its chart value; `docs/getting-started.md`
   gets *Give agents a browser*. Table tests:
   - the workspace files list the environments;
   - `model` byhand cases: a criterion left open on a listed
     environment's tool isn't filed `kind/by-hand` without its reason.
   `make seed-local` makes the `browser` TestEnvironment, and `make
   validate-yaml` renders the chart with it on and off.
   *Done when:* those tests and `make check` pass.

   *As built:* the planner reads `model/test-environments.yaml` (each
   environment's name, description, `label` and `runsOn`); the triager's
   prompt lists the project's environments (`THEOS_ENVIRONMENTS`, listed,
   not leased) and its decision may name them in `environments`, which the
   operator keeps in `status.triage.environments` and labels a ready issue
   `env/<name>` for, each the project has, making the label on the Git
   host when it isn't there. The implementer's and verifier's prompts say
   a criterion a leased environment can check is never left open for a
   person. "Not filed without its reason" became: a Task keeps the names
   of the environments its runs leased (`status.leasedEnvironments`), and
   the by-hand issue its merge files names them, and gives a criterion left
   open with no why one that says the runs had them. `make seed-local`
   seeds a local Gitea with no kubectl, so it can't make a TestEnvironment,
   against what the step first said: the seed applies the chart's catalog
   ConfigMap (`hack/seed/catalog.sh`), where the browser now is, and its
   Role may apply `testenvironments`; `test/chart`'s
   `TestSeedAppliesTheCatalogInPlace` applies it on envtest under that
   Role, and `make validate-yaml` renders the chart with the browser on
   (in project `apps`, on the `env-browser` image) and off. Tests:
   `TestBriefWorkspaceEnvironments`, `TestPlannerGuideGivesAgentsOnlyCriteriaTheyCanMeet`
   and `TestPlannerGuideAsksForNoTestThatStartsADesktopApp` (the env
   wording), `TestNormalTriage`, `TestPlanTriage`,
   `TestByHandIssueNamesTheTestEnvironments`,
   `TestPromptsNeverLeaveAnEnvironmentsCriterionOpen`; envtest "Crew
   triage" (a ready issue labelled env/browser for the environment the
   triager names, and its run leases it).
5. **Devices on the host: `android` and `godot`** (samples and
   `hack/env-android`, `hack/env-godot`, pinned). Table tests: the pod
   spec carries the extended resources and `runtimeClassName` as given.
   `make validate-yaml` checks both samples. Whether KVM and the GPU work
   is for a human's run on a node that has them.
   *Done when:* those tests and `make check` pass.
6. **The UI** (`ui/`). The Project area lists its TestEnvironments with
   live leases, today's spend and limits, and the Task page shows a run's
   leases. UI tests on a demo fixture.
   *Done when:* the UI tests pass and a screenshot is in
   `docs/screenshots/`.
7. **AWS EC2, Linux and GPU** (T9 to T11, `internal/adapters/awsenv`, the
   `aws` member, `make manifests generate`).
   `contract.TestEnvironments` on a fake EC2 API, as the `cloudformation`
   adapter's tests fake AWS. Instances get no instance profile, metadata
   off and the lease's tags; `Return` terminates; `Leases` finds an
   orphan by tag; cost accrues by time. Envtests: a wrong account and
   missing keys give `CredentialsValid=False`, and past the budget a run
   waits `OverBudget`. CEL failing cases: `aws` without
   `budgetUSDPerDay`.
   *Done when:* those tests and `make check` pass.

   *As built:* the `aws` member has `region`, `account`, `instanceType`
   (no `mac*`: step 9), `imageId`, `subnetId` and `credentialsSecretRef`,
   and `limits.budgetUSDPerDay` (whole dollars a UTC day, which CEL requires
   with `aws`). The adapter makes one security group per environment in
   the subnet's VPC, tagged with the environment's UID, and sets its rules
   to the fence's on each launch, revoking any added by hand: tcp 443 in
   from anywhere (the lease's token and the pinned certificate guard it),
   and out tcp 443 to `model.PublicRanges()` (every IPv4 block outside
   `model.PrivateRanges()`, 40 blocks) and DNS; Amazon's resolver is never
   filtered by a group. RunInstances (`awsenv.LaunchInput`, table-tested in
   `TestLaunchInputIsFenced`) has no instance profile and no key, one
   network interface in that group alone with a public address, shutdown
   behaviour `terminate`, and the metadata service on with IMDSv2 only, one
   hop, no tags and no IPv6: the machine reads its user data from it, so
   T5's "off at launch" would start nothing. The adapter turns it off once
   `theos-env` answers `/healthz` with the lease's key (tagged
   `theos.factory.io/theos-env-key`; `PinnedProbe`), and a lease is Ready
   only once that applied (`TestMetadataServiceIsOffBeforeReady`); no run
   reaches the machine before, since the sandbox gets the endpoint only
   then. The certificate and key come from the lease's token
   (`model.TestEnvCertificate`, Ed25519, fixed dates, no host name), so the
   user data carries the token alone and `theos-env serve --tls` makes the
   same pair; `Lease` returns the certificate as `CA`, and `theos-env run`
   now checks the server's chain against `_CA` without a host name (it
   checked the host before, which a machine's later address can't meet).
   The user data shuts the machine down at the deadline, downloads
   `theos-env` from `--test-environment-tools-url` (the chart's
   `testEnvironments.toolsURL`, by default the `theos-env-linux-${arch}` the
   release now attaches beside `theos-session`, in its signed `SHA256SUMS`)
   and checks it, and serves on 443 under `systemd-run`. Machines are tagged
   with the lease, holder, environment, its UID (which `Leases` lists by, so
   two factories on one account never return each other's machines),
   deadline and price; a second machine of one lease, which a lost
   RunInstances answer would leave, is terminated. The cost is the
   on-demand Linux price from the AWS Price List (`pricing:GetProducts` in
   us-east-1, the highest it lists, read once per type) times the time
   held, a minute at least, rounded up (`model.LeaseCostMilliUSD`); the
   price is tagged at launch. A launch AWS refuses on its own grounds (no
   capacity, no such AMI) is a Failed lease with AWS's message, so the run
   waits `Failed` and takes another. The port gained two optional
   capabilities: `TestEnvironmentCredentials` (`CheckCredentials`: STS
   identity, `aws.account`, and the price) and `TestEnvironmentReleaser`
   (`Release`: the security group). The TestEnvironment controller reads
   the Secret live each poll and sets `CredentialsValid` (`Valid`,
   `SecretMissing`, `SecretIncomplete`, `CredentialRefused`, `Unreachable`);
   while it is False, `Ready` is False, reason `CredentialsInvalid`, its
   leases aren't listed, and a run waits `WaitingForEnvironment`,
   `CredentialRefused` (a Needs-you item), as it does when `Lease` refuses
   the credential. A remote environment carries the finalizer
   `theos.factory.io/test-environment-leases`: deleting it returns its
   leases and releases it, and gives up after 15 minutes, or at once with
   no Secret or a refused credential, leaving the machines to their
   deadline. The Task counts today's spend as the environment's status had
   it for the ended leases and as the provider counts the live ones now
   (`spentToday`) before a new lease. Tests: `internal/adapters/awsenv`
   (`TestTestEnvironmentsContract` on the fake EC2, `TestLaunchInputIsFenced`,
   `TestLaunchInputTagsTheLease`, `TestUserData`, `TestSecurityGroupIsTheFence`,
   `TestMetadataServiceIsOffBeforeReady`, `TestCostAccruesByTime`,
   `TestCredentials`, `TestLeasesFindsMachinesByTag`,
   `TestLaunchFailureIsAFailedLease`, `TestPastTheDeadline`,
   `TestReleaseDeletesTheGroup`, `TestLeaseOf`, `TestOnDemandHourly`,
   `TestPinnedProbe`), `internal/model` (`TestLeaseCostMilliUSD`,
   `TestPublicRanges`, `TestTestEnvCertificate`, `TestTestEnvCredentials`,
   `TestLeaseTokenSafe`, and the attention case for `CredentialRefused`),
   `cmd/theos-env` (`TestRunPinsARemoteEnvironmentsCertificate`), envtest
   "TestEnvironment on AWS" (a missing Secret or key and a key of another
   account give `CredentialsValid=False`, a run waits `CredentialRefused`
   and starts once the key is of the account, deletion returns and
   releases; past the budget a run waits `OverBudget` while the held one
   goes on, and starts once the budget is raised) and the CEL cases (two
   members, `aws` without a budget, a Mac type, an image that is no AMI),
   with `test/invalid/testenvironment-two-members.yaml` and
   `testenvironment-aws-without-budget.yaml`. What only a real account
   shows (the AMI booting, cloud-init running the user data, the Price
   List's filters matching, a GPU) is a human's run.
8. **Remote environments in CI.** An optional capability of the CI port,
   `WaitingJobs(labels)`, on Gitea, in `make test-gitea`. The operator
   leases an `aws` environment for a waiting `env-<name>` job, within its
   limits, and the machine registers as an ephemeral runner from user
   data. Envtest with the fakes: a waiting job leases one machine, and
   the lease returns when the job ends.
   *Done when:* those tests and `make check` pass.

   *As built:* the capability is `ports.JobRunners` (`internal/ports/ci_jobs.go`):
   `WaitingJobs(labels)`, the owner organization's jobs a runner with those
   labels could take (Gitea lists them `status=queued`; `waiting` is a job
   blocked on `needs`), and `RegisterJobRunner`, since the machine doesn't
   register itself, against what T8 and this step first said: the operator
   registers an ephemeral runner over Gitea's runner protocol with the
   organization's registration token, and the machine gets the runner's
   `.runner` file (its own token, good for its one job: Gitea removes an
   ephemeral runner, and its token, when that job ends) and a `config.yaml`.
   Neither "delete the token after registering" nor "make it short-lived"
   works: Gitea's registration token is one per organization, the same on
   every call until reset, which would break the pool pods' registrations,
   and a machine's user data stays readable from EC2's API and in
   cloud-init's copies on its disk. So the registration token never reaches
   a machine, and the operator deletes the runner when it returns its lease.
   The provider side is the capability `ports.TestEnvironmentRunners`
   (`RunnerMissing`, `LeaseRunner`; `internal/adapters/awsenv/ci.go`): the
   machine is launched, fenced and tagged as a run's is (`RunnerLaunchInput`
   is `LaunchInput` but for its user data), and its user data
   (`RunnerUserData`) downloads `theos-env` and the CI runner
   (`--test-environment-runner-url`, the chart's `testEnvironments.runnerURL`,
   by default the Gitea runner of the runner image's version, which Release
   now attaches beside `theos-env` in `SHA256SUMS` after checking it against
   the sums `hack/runner/Dockerfile` pins), writes the runner's files for root
   alone in `/var/lib/theos-runner` (`model.RemoteRunnerDir`), serves
   `theos-env` until the metadata service stops answering, so step 7's probe,
   metadata off and Ready apply unchanged and the job never reads the user
   data (T5), then runs the runner, and shuts down when it exits. The Git
   host's public address is `GitProvider.spec.ci.giteaActions.publicURL`
   (CEL: `https://` on port 443, the one port the machine's fence lets out),
   else `url` when that is such an address at a public host
   (`model.RemoteRunnerAddress`); the chart's in-cluster Gitea has neither,
   so remote CI is off until a person sets it. A remote environment turns CI
   on with `ci: {}` (CEL refuses `ci.size` and `ci.runnerImage` on `aws`),
   and its condition `CI` says `Serving`, `NoGitProvider`, `NoPublicAddress`,
   `LabelClash`, `Unsupported` (no runner address, or a host without
   `JobRunners`), `Unreachable` (the Git host didn't answer) or
   `OverBudget`. The TestEnvironment controller, not the
   Project's pool, does it (`internal/controller/testenvironment_ci.go`):
   each poll, every 15 seconds while a job waits or a CI lease starts, it
   reads Pending CI leases back with `Get`, which moves them on, returns
   Failed ones, and for each GitProvider leases a machine for each waiting
   job beyond the CI leases whose runner isn't busy yet, oldest first, while
   `model.LeaseMayStart` allows, against `maxLeases` and the budget it shares
   with the runs. A CI lease's ID is `model.CILeaseID` (the environment, its
   UID, the GitProvider, the job and a count), the runner is named by it, and
   its holder is `ci/<GitProvider>/<runner id>` (`model.CIHolder`; a Task's
   name has no slash). The orphan rule learned CI holders
   (`model.CILeaseHeld`): held while the runner runs its job; not once Gitea
   removed it, its job ended, or once no job of its label waits; while it is
   online, yes; before it first calls Gitea, for 20 minutes
   (`model.CIStartGrace`). A GitProvider whose host doesn't answer keeps its
   leases. A job a runner takes in the moment its idle lease is returned
   fails as one whose runner went away. A remote environment's `env-<name>`
   clashes with a `ci` block's label as a local one's does (`LabelClash` on
   the Project). `test/chart`'s `TestReleaseAttachesWhatRemoteMachinesDownload`
   checks that both builds of what the chart's defaults have a remote
   machine download from the release, `theos-env` and the CI runner, are
   attached and in `SHA256SUMS`, and `hack/chart-manager.sh` keeps
   `--test-environment-tools-url` and `--test-environment-runner-url`.
   Tests: `internal/model`
   (`TestCIHolder`, `TestCILeaseID`, `TestCILeaseHeld`,
   `TestRemoteRunnerAddress`), `internal/adapters/gitea` (`TestWaitingJobs`,
   `TestRegisterJobRunner`, and `TestCIContractIntegration`'s two new
   subtests in `make test-gitea`), `contract.CI`'s subtests on the fake,
   `internal/adapters/awsenv` (`TestRunnerUserData`,
   `TestRunnerLaunchInputIsFenced`, `TestLeaseRunner` on the fake EC2),
   envtest "TestEnvironment on AWS in CI" (a waiting job leases one machine
   whose runner file holds its own token and the public address, kept while
   its runner runs the job, returned once Gitea removed it; a machine whose
   job another runner took is returned and its runner deleted; `maxLeases`
   and the budget hold jobs back; no public address, no lease; a publicURL
   on another port refused) and the CEL cases (`ci.size` and
   `ci.runnerImage` on `aws`), with
   `test/invalid/testenvironment-aws-ci-size.yaml` and
   `gitprovider-public-url-port.yaml`, `TestReleaseAttachesWhatRemoteMachinesDownload`
   and `hack/chart-manager_test.sh`.
   What only a real account and host show (the runner reading the files the
   operator wrote and taking its job, the metadata service's answer once it
   is off, a job's checkout from Gitea's `ROOT_URL`) is a human's run.
9. **EC2 Mac for `ios`** (T11's host reuse). Fake EC2 tests: a host is
   allocated once and reused across leases, kept for `keepHostHours`,
   released after, and counted at the 24-hour minimum.
   *Done when:* those tests and `make check` pass.

   *As built:* CEL takes a Mac `instanceType` now, and the `aws` member has
   `mac.keepHostHours` (24 to 168, default 24, from each host's allocation;
   CEL keeps `mac` to a Mac type). `internal/adapters/awsenv/mac.go`
   allocates a host in the subnet's zone (auto-placement and host recovery
   off, idempotent on a token of the environment's UID and the lease) only
   when every host it keeps for the environment, tagged with its UID and
   within its kept time, has a machine on it, and places the machine on a
   free one (tenancy `host`, its host ID). EC2 scrubs a Mac host after each
   machine, about an hour, during which it takes none: a lease waits
   `Pending` for a host kept that is being scrubbed rather than allocating
   another for a day more, which the plan didn't foresee. Each host is
   tagged with the end of its kept time and its price, and `Leases`, which
   the operator calls on each poll, releases a host past it once no machine
   is on it; a lease after that allocates a new host. "Counted at the
   24-hour minimum" is per host, not per lease: the lease whose machine is
   the first on a host (tagged on the host, `host-claimed-by`) counts the
   host's price for its kept hours at once, a lease on a host kept costs
   nothing until they are over, and one held past them pays for its time
   past them (`model.HostLeaseBilled`), so each hour of a host is counted
   once, and a host whose first machine never started is counted by the next
   machine on it, more, never less. The price is the Price List's Dedicated
   Host product of the type's family (`productFamily` `Dedicated Host`,
   `instanceType` the family, such as `mac2`). The budget must cover a
   host's kept hours, which CEL can't check without the price; the sample
   and the guide say so. Deleting a Mac environment releases its hosts, which
   EC2 refuses within their 24 hours: `Release` says so and is asked again,
   and the finalizer waits a host's 24 hours and 15 minutes for a Mac
   (`testEnvReleaseWithin`) instead of 15 minutes, since a host has no
   deadline to end at. The user data is portable to macOS: `uname -s` gives
   `${os}` (`linux`, `darwin`), `shasum -a 256` checks where `sha256sum`
   is missing, `launchctl submit` keeps `theos-env` up where there is no
   `systemd-run`, and the runs' folder is `/private/var/theos-env/work`, as
   macOS's root volume is read-only. The release attaches
   `theos-env-darwin-amd64` and `-arm64`, in its `SHA256SUMS`, and the
   chart's `testEnvironments.toolsURL` is `theos-env-${os}-${arch}`; a Mac
   whose address names neither `${os}` nor `darwin` fails its lease. An EC2
   Mac runs no CI jobs (step 8): CEL refuses its `ci`, since the CI runner a
   machine downloads is Linux's alone and a host per job would be billed 24
   hours each (`model.TestEnvRemoteCI`, which the CI leases also go by).
   Tests: `internal/adapters/awsenv` (`TestMacTestEnvironmentsContract`,
   `TestMacLaunchInputIsPlacedOnTheHost`, `TestMacHostIsReusedAcrossLeases`,
   `TestMacAllocatesAHostWhenEveryHostIsBusy`,
   `TestMacHostIsKeptForKeepHostHoursThenReleased`,
   `TestMacLeaseCostCountsTheHostsMinimum`, `TestMacNeedsTheDarwinBuild`,
   `TestReleaseReleasesTheMacHosts`, `TestMacLeaseCostSurvivesARestart`,
   `TestUserData` on both systems), `internal/model` (`TestTestEnvMac`,
   `TestKeepHost`, `TestHostLeaseBilled`), `internal/controller`
   (`TestTestEnvReleaseWithin`, envtest "TestEnvironment on an EC2 Mac", and
   the CEL cases `mac` on a type that is no Mac and a host kept less than 24
   hours), `make validate-yaml` with `config/samples/webshop/testenvironment-ios.yaml`
   and `test/invalid/testenvironment-mac-host-on-linux.yaml`. What only a
   real account shows (a Mac host allocating, ec2-macos-init running the
   user data, `launchctl submit` keeping `theos-env` up, the Dedicated Host
   price's filters matching, how long scrubbing takes) is a human's run.
10. **Device Farm** (T12, `TestRuns`). Fake Device Farm API tests: an
    upload and a run, and the report and artifacts back through
    `theos-env test`.
    *Done when:* those tests and `make check` pass.

    *As built:* the member is `deviceFarm` (`projectArn` and `devicePoolArn`
    in us-west-2, by CEL; `account`; `credentialsSecretRef`, an `aws`
    member's keys; CEL requires `limits.budgetUSDPerDay`), not a block of
    `aws`, whose machine settings it has none of. It is remote
    (`model.TestEnvRemote`): `CredentialsValid` (STS identity, `account`,
    the device pool), the finalizer and the budget work as for `aws`; it has
    no machine to run a CI job's runner on, so CEL refuses its `ci` and step
    8's CI leases skip it (`model.TestEnvRemoteCI`). The
    decision on how a run reaches the provider is T12's: over the Git host,
    never the operator. A lease holds no device: it is a tag on the project
    (`theos.factory.io/lease:<uid>:<lease>`, its holder, start and deadline),
    so a restarted operator finds it, Ready at once at
    `git:theos-env/<lease>` (`THEOS_ENV_<NAME>_URL`; no `_CA`), Failed past
    its deadline or once its pool is gone; it costs its runs' metered device
    minutes at Device Farm's list price, 0.17 US dollars a minute
    (`model.DeviceFarmMilliUSDPerMinute`: Device Farm isn't in the Price
    List the EC2 member reads), and `Return` stops its runs and takes the tag
    off. `internal/model/testruns.go` has the request
    (`.theos-env/request.json`, `app/`, `tests/`; `model.TestRunRequest`,
    its MAC an HMAC-SHA256 under the lease's token over everything it asks
    for), the types (`instrumentation`, `xctest`, `xctest-ui`), the caps
    (256 MiB up; artifacts 16 MiB each, 64 MiB together, videos left on the
    console), the report (`.theos-env/result/report.json`, `request`,
    `artifacts/`), its summary and the pull request's comment. `theos-env
    test` builds the request commit in an index of its own, so the run's
    tree and index stay as they are, pushes it, polls the branch, checks the
    answer names its request, writes `report.json`, `summary.txt` and
    `artifacts/` into `--out` (`test-runs/<name>/`), and exits 0 when every
    test passed, 1 when one didn't, 125 when nothing ran. The operator's side
    is the TestEnvironment reconciler's (`testenvironment_testruns.go`): on
    each poll, 15 seconds while a lease is live, it reads each live lease's
    branch on its holder's Container's Git host; a request it hasn't seen it
    checks (`model.CheckTestRun`) and the day's spend, and either answers it
    `Rejected` with why, or starts it (`StartTestRun`: the uploads, once
    each, then `ScheduleRun` on the pool, a job at most the lease's time
    left and 150 minutes) and follows it until it is done; the answer is a
    commit on `main`'s head with the result's files, pushed only while the
    branch still holds the request. Each run is in the TestEnvironment's
    `status.testRuns` (lease, holder, request, commit, state, result,
    outcome, cost, the answer), its comment kept until the holder has a pull
    request; a returned lease's runs are dropped once commented, and its
    branch deleted. The run's prompt marks such an environment (`testRuns`
    in `THEOS_ENVIRONMENTS`) and tells it to use `theos-env test`. Device
    Farm runs are found by name (`ListRuns` on the project), which a project
    with many runs pages through. Tests: `internal/adapters/awsenv`
    (`TestDeviceFarmContract`, `TestDeviceFarmLeaseIsItsBranch`,
    `TestDeviceFarmRunsATestPackage`, `TestDeviceFarmArtifactsAreCapped`,
    `TestDeviceFarmUploadRefused`, `TestDeviceFarmReturnStopsTheLeasesRuns`,
    `TestDeviceFarmCredentials`, `TestAWSDeviceFarmClientsAreInUSWest2`, on
    a fake Device Farm with an httptest server for its storage),
    `internal/model` (`TestTestRunBranch`, `TestCheckTestRun`,
    `TestCheckTestRunFiles`, `TestDeviceFarmCostMilliUSD`,
    `TestTestRunResultOf`, `TestTestRunSummary`, `TestTestEnvRemoteMembers`),
    `cmd/theos-env` (`TestTestAsksOverTheGitHost`,
    `TestTestOfAnotherLeasesTokenRunsNothing`,
    `TestTestRefusesWhatItCantAskFor`, on a bare repository standing in for
    the Git host), `internal/agentrunner`
    (`TestPromptsListTestRunEnvironments`), envtest "TestEnvironment that
    runs test packages" (a run's request started with its files, answered
    on its branch with the report and the artifacts, commented on the pull
    request once it opens, the branch deleted once the lease is returned; an
    unsigned request and one past the budget answered with nothing run; the
    CEL cases no budget, a project outside us-west-2, a pool that is no
    device pool, and a second member), and `make validate-yaml` with
    `config/samples/webshop/testenvironment-devices.yaml` and
    `test/invalid/testenvironment-devicefarm-without-budget.yaml`. What only
    a real account shows (Device Farm processing the uploads, the devices
    running the package, the artifacts' types and sizes, the console's
    address) is a human's run.

## Done when

Steps 1 to 4 are merged, and #2036's case runs with nothing by hand: an
issue for the VS Code extension's pictures is labelled `env/browser`, its
implementer runs the picture script through `theos-env run browser`,
commits the pictures, CI runs the same script on `env-browser`, and no
`kind/by-hand` issue is filed for it. The proof is the envtests of steps 2
and 3 and `make check`. The steps after that each add a provider or
device, with the tests named. What only a real node or a real AWS account
shows (Chromium starting on the node, KVM, a GPU, a Mac host) is a human's
run, and isn't tested before release.
