# C4 Factory - AI Agent Guide

C4 Factory is a kubebuilder operator. Users describe software as a C4-style model in
custom resources (group `c4.factory.io`, version `v1alpha1`); the operator scaffolds
one Git repository per Container from pinned templates, then only reports. Read
[docs/design.md](docs/design.md) for the what and why, and
[docs/architecture.md](docs/architecture.md) for where code goes.

## Project Structure

```
cmd/main.go                        Manager entry (registers controllers, Session feature flags)
cmd/c4session/                     Session client CLI, run in devcontainers
cmd/c4agent/                       Task runner, run in agent sandboxes
api/v1alpha1/*_types.go            CRD schemas for all fifteen kinds (+kubebuilder markers)
api/v1alpha1/shared_types.go       Refs, Container types, condition names and labels
api/v1alpha1/zz_generated.*        Auto-generated (DO NOT EDIT)
internal/controller/*              Reconcilers: read resources, call rules, write status
internal/model/                    Pure business rules: choices, relationships, roll-up, staleness
internal/scaffold/                 Pure rendering: layers, conflicts, devcontainer, identity, commit trailers
internal/gitprovider/              GitProvider interface and the Gitea implementation (REST + go-git)
internal/sessionclient/            The Session client's logic: register, heartbeat, delete
internal/agentrunner/              The Task runner's logic: clone, run the agent, check, push, report
features/src/c4session/            The c4session devcontainer feature, embedded by the operator
config/crd/bases/*                 Generated CRDs (DO NOT EDIT)
config/rbac/role.yaml              Generated RBAC (DO NOT EDIT)
config/policy/                     ValidatingAdmissionPolicy (Project name equals namespace)
config/gitea/                      Gitea for the test environment
config/samples/webshop/            The sample story (edit these)
config/samples/toolbox/            The agent factory story: six tool apps, Agents and Tasks
config/runner/                     The Gitea CI runner for Container repositories
config/ui/                         The read-only UI's Deployment and read-only RBAC
templates/<name>/                  Template repositories, pushed and tagged into Gitea by the seed job
templates/versions.txt             The tag each template is released at
hack/testenv.mk                    Make targets for the Kind test environment
hack/seed/                         The seed job image and manifests
hack/session/                      The Session client image and the test Session pod
hack/toolchain-slim/               The slim Flutter toolchain (no Android SDK) for CI checks and agents
hack/toolchain/                    The full Flutter toolchain with the Android SDK, for releases and devcontainers
hack/runner/, hack/agent/          The CI runner and agent images, built on the toolchain
hack/render/                       Renders templates locally, the way the operator scaffolds
test/crds/                         Pinned third-party CRDs for envtest (Agent Sandbox)
test/e2e/                          End-to-end tests against the Kind cluster (make e2e)
ui/                                Read-only UI (React + a GET-only proxy)
docs/                              Design, architecture, plans and screenshots
Makefile                           Build, test, cluster and deploy commands
PROJECT                            Kubebuilder metadata, auto-generated (DO NOT EDIT)
```

The project uses the **single-group layout**: one group, `c4`, with domain
`factory.io`. Do not convert it to multi-group.

## Project Architecture

Read `docs/architecture.md` before adding code. Business rules live in
`internal/model` and `internal/scaffold` and must not import Kubernetes,
controller-runtime, `net/http` or `api/`; `make lint` enforces the layering.
Run `make check` before finishing a change.

## Critical Rules

### Never Edit These (Auto-Generated)
- `config/crd/bases/*.yaml` - from `make manifests`
- `config/rbac/role.yaml` - from `make manifests`
- `**/zz_generated.*.go` - from `make generate`
- `PROJECT` - from `kubebuilder [OPTIONS]`

### Never Remove Scaffold Markers
Do NOT delete `// +kubebuilder:scaffold:*` comments. The CLI injects code at these markers.

### Keep Project Structure
Do not move files around. The CLI expects files in specific locations, and the
layering rules above depend on the package paths.

### Always Use CLI Commands
Always use `kubebuilder create api` to scaffold new kinds. Do NOT create API or
controller files manually.

### The Design Doc Is the Spec
Behaviour comes from [docs/design.md](docs/design.md). If code and design disagree,
fix the code or propose a change to the design; don't silently diverge. Settled rules
worth repeating:
- **The CRDs are the truth.** The UI and the Session client enforce nothing; rules
  live in CRD schemas (CEL), the admission policy and the operator.
- **Scaffold once.** A Container is scaffolded at creation and never updated
  afterwards. Relationships added later show `Applied=False`, reason `AddedAfterScaffold`.
- **Every file belongs to exactly one layer.** Two layers writing the same path fail
  the Container, with the path in the message.
- **Templates are pinned.** A template `ref` is a tag or commit, never a branch.
- **Children name their parent in `spec`;** the operator sets the ownerReference so
  deletion cascades.

### E2E Tests Require an Isolated Kind Cluster
The e2e tests run against the dedicated Kind cluster created by `make cluster`
(named `c4factory`), never against a real dev or prod cluster.

## After Making Changes

**After editing `*_types.go` or markers:**
```
make manifests  # Regenerate CRDs/RBAC from markers
make generate   # Regenerate DeepCopy methods
```

**After editing `*.go` files:**
```
make lint-fix   # Auto-fix code style
make test       # Run unit and envtest tests
```

**Before finishing any change:**
```
make check      # manifests, generate, fmt, vet, lint and test in one go
```

## How to Work

- **Plan before building.** For work that spans more than one package or milestone,
  write a short plan in `docs/plans/` first: goal, steps, and how you'll know it's done.
- **One milestone at a time.** The six milestones in the design build on each other.
  Finish one, including its "Done when" check in the Kind cluster, before starting the next.
- **Test the rules where they live.** Table tests for `internal/model` and
  `internal/scaffold`, envtest for controllers, `make e2e` for the whole loop.
- **Show, don't claim.** When a milestone is done, save evidence (test output,
  `kubectl get` output, UI or Gitea screenshots) in `docs/screenshots/`.
- **Report honestly.** If a check fails or a step is skipped, say so in the commit or
  PR with the output.

## CLI Commands Cheat Sheet

### Create API (a new kind in this project)
```bash
kubebuilder create api --group c4 --version v1alpha1 --kind <Kind> --resource --controller
```

Catalog kinds (Framework, Standard, Compute, Deployment, Connector, Agent) and
GitProvider have no reconciler of their own; create them with `--controller=false`.

### Controller for Core Kubernetes Types
```bash
kubebuilder create api --group core --version v1 --kind Pod \
  --controller=true --resource=false
```

### Webhooks
v1 has no webhooks by design: CEL rules and a ValidatingAdmissionPolicy cover
validation, and the operator checks compatibility after creation. If one is added
later, create all types together with
`kubebuilder create webhook --group c4 --version v1alpha1 --kind <Kind> --defaulting --programmatic-validation`.

## Testing & Development

```bash
make test                # Unit and envtest tests (real K8s API + etcd, no cluster needed)
make check               # manifests, generate, fmt, vet, lint and test
make cluster gitea seed  # Kind cluster, Gitea, and the seeded templates
make deploy sample       # Build and deploy the operator into Kind, apply the webshop sample
make session             # Run the test Session pod; make session-kill kills its client
make test-gitea          # gitprovider against the seeded Gitea
make toolchain runner agent-sandbox agent-image  # Slim toolchain, CI runner, Agent Sandbox, agent image
make toolchain-android runner-android  # Optional: the 20 GB release runner for signed APKs
make toolbox             # The toolbox story; then kubectl apply -k config/samples/toolbox/tasks
make claude-secret CLAUDE_TOKEN=...  # The real agent's token, from `claude setup-token`
make e2e                 # All of the above, then check every milestone in Kind
```

Controller tests use **Ginkgo + Gomega** (BDD style) with the real manager and every
reconciler; see `internal/controller/suite_test.go`. A fake Git host serves the real
`templates/`, so envtest also proves the templates render together. Business-rule tests
use the standard `testing` package with table tests. In tests, write with merge patches
(`patch(obj, func(){...})`), not `Update`: the operator may adopt the object meanwhile.

`make run` runs the operator on your machine, but it can't reach Gitea's in-cluster
address; use `make deploy` for anything that scaffolds.

## Deployment Workflow

```bash
# 1. Regenerate manifests
make manifests generate

# 2. Build, load into Kind, deploy
make deploy            # builds c4factory/operator:dev, kind-loads it, applies config/default, restarts

# 3. Try it
make sample            # the webshop story
kubectl get c4 -n webshop

# 4. Debug
kubectl logs -n c4-factory-system deployment/c4-factory-controller-manager -c manager -f
make gitea-forward     # then open http://localhost:3000
```

### API Design

**Key markers for** `api/v1alpha1/*_types.go`:

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"

// On fields:
// +kubebuilder:validation:Required
// +kubebuilder:validation:Enum=static-site;service;job;worker
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="softwareSystemRef is immutable"
// +kubebuilder:default="value"
```

- **Use** `metav1.Condition` for status (not custom string fields). Phases are a
  convenience on top of conditions.
- **Use predefined types**: `metav1.Time` instead of `string` for dates
- **Follow K8s API conventions**: Standard field names (`spec`, `status`, `metadata`)
- **Keep fields minimal.** `displayName` and `description` on every story kind;
  `metadata.name` is the stable id.

### Controller Design

**RBAC markers in** `internal/controller/*_controller.go`:

```go
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
```

**Implementation rules:**
- **Idempotent reconciliation**: Safe to run multiple times; a repeat reconcile of a
  `Ready` Container changes nothing
- **Re-fetch before updates**: `r.Get(ctx, req.NamespacedName, obj)` before `r.Update` to avoid conflicts
- **Structured logging**: `log := log.FromContext(ctx); log.Info("msg", "key", val)`
- **Owner references**: Enable automatic garbage collection (`SetControllerReference`)
- **Watch secondary resources**: Use `.Owns()` or `.Watches()`, not just `RequeueAfter`
- **Thin reconcilers**: Decisions about types, layers and files belong in
  `internal/model` and `internal/scaffold`, not in the controller

### Logging

**Follow Kubernetes logging message style guidelines:**

- Start from a capital letter
- Do not end the message with a period
- Active voice: subject present (`"Deployment could not create Pod"`) or omitted (`"Could not create Pod"`)
- Past tense: `"Could not delete Pod"` not `"Cannot delete Pod"`
- Specify object type: `"Created repository"` not `"Created"`
- Balanced key-value pairs

```go
log.Info("Starting reconciliation")
log.Info("Created repository", "url", repoURL)
log.Error(err, "Failed to push initial commit", "container", name)
```

**Reference:** https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines

## References

### Essential Reading
- **Kubebuilder Book**: https://book.kubebuilder.io
- **controller-runtime FAQ**: https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md
- **Good Practices**: https://book.kubebuilder.io/reference/good-practices.html
- **C4 model**: https://c4model.com

### API Design & Implementation
- **API Conventions**: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md
- **CEL validation rules**: https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules
- **ValidatingAdmissionPolicy**: https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/
- **Markers Reference**: https://book.kubebuilder.io/reference/markers.html

### Tools & Libraries
- **controller-runtime**: https://github.com/kubernetes-sigs/controller-runtime
- **Gitea API**: https://docs.gitea.com/development/api-usage
- **Dev Container features**: https://containers.dev/implementors/features/
