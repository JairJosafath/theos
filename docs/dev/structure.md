# Project structure

Where things live. Read [../architecture.md](../architecture.md) for which way
dependencies may point.

```
cmd/main.go                        Manager entry (registers controllers, Session feature flags)
cmd/theos-session/                     Session client CLI, run in devcontainers
cmd/theos-agent/                       Task runner, run in agent sandboxes
cmd/theos-env/                     theos-env: runs an agent's commands in a leased test environment (run), and serves them there (serve); in the agent image
cmd/model-proxy/                   Model proxy: keeps the Claude token out of sandboxes, a second binary in the operator image
cmd/theos-chat/                    theos-chat: posts in each Chat's Matrix rooms and runs its people's commands as them, a third binary in the operator image
cmd/theos/                         theos with no cluster: init, scaffold, protect, status, agents, plan (#293), and update, export and import (#295) on a project repository's model/ files, and the lead runtime's next, claim, heartbeat, release and done, also as theos mcp's tools over stdio (lead.go, #294), and a person's merge at a terminal (#292); wires the host adapters and hostrun onto internal/nocluster (#268)
api/v1beta1/*_types.go             CRD schemas for every kind (+kubebuilder markers), the one version served and stored; the operator imports it
api/v1beta1/shared_types.go        Refs, Container types, condition names and labels
api/*/zz_generated.*               Auto-generated (DO NOT EDIT)
internal/controller/*              Reconcilers: read resources, call rules, write status
internal/model/                    Pure business rules: choices, bundles, relationships, roll-up, staleness
internal/scaffold/                 Pure rendering: layers, conflicts, devcontainer, identity, commit trailers; a Standard's update rendered and planned once, for the operator and theos update (standardplan.go), and the version its files are at read back from the commit trailers (standardhistory.go, #295)
internal/adapters/gitea/           The Gitea adapter: the Git host ports over REST (+ go-git through adapters/git)
internal/adapters/packages/        What the host adapters share of publishing a release's packages: npm tarballs, wheels and sdists read and sent to an npm registry and a Python index
internal/adapters/linear/          The Linear adapter: request intake (ports.IssueTracker) over Linear's GraphQL API, for one team and one label, wired to nothing yet; lineartest/ is its fake Linear
internal/adapters/jira/            The Jira Cloud adapter: request intake (ports.IssueTracker) over the REST API version 3, for one project and one label, wired to nothing yet; jiratest/ is its fake Jira Cloud
internal/adapters/matrix/          The Matrix adapter: the ChatHost port over the client-server API; matrixtest/ is its fake homeserver
internal/adapters/github/          The GitHub adapter: the release copy, a Brief's requests, a GitHub App's tokens, and the Git host ports as docs/plans/github-host.md adds them (GitHost, Issues, AgentAccess, CI, Events and the Registry, so far); githubtest/ is its fake GitHub, REST and Git over HTTP
internal/adapters/hostrun/         The lead runtime on the Git host's ports (#335): a Task's branch and its issue's markers as ports.TaskStore and ports.Workers; no Kubernetes, no net/http
internal/adapters/localenv/        The local TestEnvironment provider: a fenced pod per lease, with client-go
internal/adapters/awsenv/          The aws TestEnvironment provider: a fenced EC2 machine per lease (a Mac's on a kept host; one that runs a CI job in ci.go), and the deviceFarm one's test runs, with the AWS SDK
internal/taskfacts/                What the factory's numbers read of a Task: the one mapping from the API's Task to model.TaskMetricFacts, for the operator's metrics and theos-session report
internal/modelfacts/               What the scaffold works out of the objects a caller read: resolve, layers, render, license and the one branch rule, for the Container reconciler and the theos binary (#268); the story as a plan's objects and back, for the Brief reconciler and theos plan (story.go, #293); no controller-runtime, no client-go
internal/nocluster/                The theos binary's commands on ports, and the model files' decoder: each object defaulted from the CRDs config/crd/bases embeds (crds.go), checked against their create-time rules, then decoded strictly (#268); theos plan's round in plan/ and its merge into model/model.yaml (plan.go, planmerge.go, #293); theos update's Standard update from main's history (update.go), and the stream export prints and the model files import makes of a cluster's objects (export.go, import.go, #295); no controller-runtime, no client-go, no adapter
internal/engine/                   The loops both runtimes run, on the ports (docs/plans/lead-runtime.md, decision 2): the Crew tick on ports.TaskStore, the operator's adapter in internal/controller/crew_tasks.go (#332), and the Task tick on ports.Workers too, the operator's Sandboxes in internal/controller/task_workers.go and the Task reconcile on the tick in internal/controller/task_tick.go, a turn's offer, end and restart, a run suspended, resumed or paused in place, and the round's pull request, its checks run again and a failure, through the operator's host in internal/controller/task_host.go, the engine's (#336); no Kubernetes, no API types, no net/http
internal/method/                   The lead runtime's content (#266): the lead playbook, each role's boundaries and the work order's text, rendered from the model
internal/harness/                  The harness interface (Harness, LeadFiles, RoleGrants); claudecode/ and codex/, each harness's files for a project repository, which theos agents writes (#266, #338); harnesstest/, the conformance suite (docs/harness-authoring.md)
internal/sessionclient/            The Session client's logic: register, heartbeat, delete
internal/chatbot/                  theos-chat's logic: news to threads, posts and the updates digest; commands sent impersonating the person; its state ConfigMap
internal/agentrunner/              The Task runner's logic: clone, run the agent, check, push, report
internal/modelproxy/               The model proxy's handler: drop the client's credential, add the Secret's, forward unbuffered
internal/oci/                      Images in OCI registries: push a layout archive as a stream, resolve a tag, copy by digest
internal/oci/ocitest/              Test archives: the OCI layout tar of a two-platform index with an attestation
internal/buildinfo/                What a build stamps into the binary: the release Version (-ldflags -X)
features/src/theos-session/            The theos-session devcontainer feature, embedded by the operator
config/crd/bases/*                 Generated CRDs (DO NOT EDIT)
config/rbac/role.yaml              Generated RBAC (DO NOT EDIT)
config/policy/                     ValidatingAdmissionPolicies: Project name equals namespace; a Chat's people name only themselves; the chat annotations are a person's own
config/chat/                       theos-chat's ServiceAccount and RBAC (reads, its ConfigMap, impersonate), by hand: no markers
config/gitea/                      Gitea for the test environment
config/samples/webshop/            The sample story (edit these)
config/runner/                     The Gitea CI runner for Container repositories
config/ui/                         The UI's Deployment and RBAC: reads, and the Brief writes
config/metrics-server/             metrics-server for Kind (CPU and memory for the UI and kubectl top)
config/agent-sandbox/              Agent Sandbox for Kind, without leader election
dist/chart/                        The Helm chart: generated by `make chart` (its CRDs and manager role by `make manifests` too), except templates/ui/, templates/gitprovider/, templates/chat/, files/, templates/manager/manager.yaml and values.yaml
templates/<name>/                  Template repositories, pushed and tagged into Gitea by the seed job
templates/versions.txt             The tag each template is released at
hack/testenv.mk                    Make targets for the Kind test environment
hack/dev/                          The project shell's environment and zsh setup, for ./dev
hack/seed/                         The seed job image and manifests
hack/session/                      The Session client image and the test Session pod
hack/toolchain-base/               The minimal toolchain (Node, uv, bash, git, make) for the e2e hello-world Container and Python
hack/toolchain-go/                 The Go, Node and uv toolchain, with a warm Go build cache and the kubebuilder operator tools, for Go, TypeScript and Python Containers
hack/runner/, hack/agent/          The CI runner and agent images, built on the toolchain
hack/env-browser/                  The browser test environment's image: Playwright's, with a virtual display
hack/env-android/                  The android test environment's image: the Android SDK, an emulator and its AVD, JDK 17, and android-emulator, which boots it
hack/env-godot/                    The godot test environment's image: the Godot engine, its renderers' libraries and a virtual display
hack/gh-runner/                    Self-hosted GitHub Actions runners for this repository (make gh-runner RUNNERS=N)
hack/render/                       Renders templates locally, the way the operator scaffolds
hack/gta/                          The Go packages a change affects, selected by digitalocean/gta: what make test and make check test, vet and lint
hack/check.sh                      make check's steps, each run only when the change can have broken it, and the block that says what ran
test/chart/                        The chart against envtest: its upgrade-only hooks (make test)
test/compat/                       The compatibility guard: each served version against the last release's CRDs in baseline/ (make test)
test/crds/                         Pinned third-party CRDs for envtest (Agent Sandbox, Argo CD)
test/e2e/                          End-to-end tests against the Kind cluster (make e2e)
test/e2e/testdata/apps/            The e2e fixture project: one hello-world Container, Agents and Tasks
test/e2e/standin/                  The guard that keeps the e2e fixture's Agents on the stand-in (runs in make test)
ui/                                Read-only UI (React + a GET-only proxy)
docs/                              Design, architecture, plans and screenshots
Makefile                           Build, test, cluster and deploy commands
dev                                The development environment in .dev/ (DEVELOPMENT.md)
PROJECT                            Kubebuilder metadata, auto-generated (DO NOT EDIT)
```

The project uses the **single-group layout**: one group, `c4`, with domain
`factory.io`. Do not convert it to multi-group.
