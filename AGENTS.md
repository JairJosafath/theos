# theos - AI Agent Guide

theos is a kubebuilder operator. Users describe software as a C4-style model in
custom resources (group `theos.factory.io`, version `v1beta1`); the operator scaffolds
one Git repository per Container from pinned templates, then only reports. Read
[docs/design.md](docs/design.md) for the what and why, and
[docs/architecture.md](docs/architecture.md) for where code goes.

This file is loaded into every session, so it holds only what is true for every
task. Read the rest when the task needs it:

| When you | Read |
| --- | --- |
| change what the factory does | [docs/design.md](docs/design.md): the spec |
| add or move code | [docs/architecture.md](docs/architecture.md) and [docs/dev/structure.md](docs/dev/structure.md) |
| change API types, reconcilers or log lines | [docs/dev/api-and-controllers.md](docs/dev/api-and-controllers.md) |
| run tests | [docs/dev/testenv.md](docs/dev/testenv.md) |
| make, store, pass or test a credential, or a secret scanner fails a pull request | the `secrets` skill |
| work on a plan step | its plan in [docs/plans/](docs/plans/); finished plans are in [docs/plans/done/](docs/plans/done/) |

## You Are an Agent in a Crew

You are an Agent in a Crew. The operator gives you one Task from a Gitea
issue; you commit on the branch it names; the operator opens the pull request,
the reviewer judges it and the operator merges it. Never push to main, merge,
or touch a cluster. The paths in the factory-self Standard's protectedPaths
are the owner's: if the Task needs one changed, say so and stop.

## Project Architecture

Business rules live in `internal/model` and `internal/scaffold` and must not
import Kubernetes, controller-runtime, `net/http` or `api/`; `make lint` enforces
the layering. `make check` gates every change.

## Critical Rules

### Never Edit These (Auto-Generated)
- `config/crd/bases/*.yaml` - from `make manifests`
- `config/rbac/role.yaml` - from `make manifests`
- `dist/chart/templates/crd/*.yaml` and `dist/chart/templates/rbac/manager-role.yaml` - from `make manifests` (and `make chart`)
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
- **Scaffold once.** A Container is scaffolded at creation and never scaffolded
  again; only its Standard's files follow a bump of the Standard's pinned `ref`,
  through a pull request the operator opens and merges. Relationships added
  later show `Applied=False`, reason `AddedAfterScaffold`.
- **Every file belongs to exactly one layer.** Two layers writing the same path fail
  the Container, with the path in the message.
- **Templates are pinned.** A template `ref` is a tag or commit, never a branch.
- **Children name their parent in `spec`;** the operator sets the ownerReference so
  deletion cascades.

### Agents Never Touch a Cluster
Go tests are the gate: `make check` for every change, `make test-gitea` and
`make seed-local` for the Gitea code and the seed. Agents never create or
deploy to a cluster, and never run `make e2e`. What only a real node would
catch is not tested before release.

## After Making Changes

**After editing `*_types.go` or markers:**
```
make manifests  # Regenerate CRDs/RBAC from markers, and dist/chart's copies of them
make generate   # Regenerate DeepCopy methods
```

**After editing `*.go` files:**
```
make lint-fix   # Auto-fix code style
make test       # Run unit and envtest tests
```

**Before finishing any change:**
```
make check      # manifests, generate, fmt, vet, lint, test and the UI's check in one go
```

## How to Work

- **Plan before building.** For work that spans more than one package or issue,
  write a short plan in `docs/plans/` first: goal, steps, and how you'll know it's done.
- **Designing is our job.** Whoever files an issue says what they want; how the
  factory does it is ours to decide. When the design doesn't settle something,
  make the choice you think works, write it and why in a plan in `docs/plans/`
  (or the change to `docs/design.md`), and go on. Never ask the reporter how to
  build it, and never hand the user a list of open design questions. Ask the
  reporter only to elaborate, when what they want is unclear. When you're
  unsure which way to go, ask the user with the choice you'd make and why, so
  they can steer.
- **What can be configured is configured.** A choice that depends on where or
  how a user runs the factory is a setting, never a fixed pick: a registry, a
  package scope, an index, a provider, a name the user holds somewhere. Its
  default works on the chart's in-cluster Gitea, and a user who runs
  elsewhere gives their own value. The design never hard-codes such a choice
  for the user, and no plan asks the owner to hold a name or an account on a
  user's behalf (#1234: `@theos` on npmjs.org was proposed as the one
  scope; the scope and the registry are settings, the scope's default is
  `@theos` and the registry's the in-cluster Gitea's npm registry, so
  nothing leaves the cluster unless the user says where). The accounts and
  credentials the project itself needs stay the owner's ([GOVERNANCE.md](GOVERNANCE.md), "Reserved to the owner").
- **Test the rules where they live,** at the lowest tier that can see them:
  1. `internal/model` and `internal/scaffold` table tests, the Agent
     Sandbox and pod specs the controllers build, the fence included, and
     the generated RBAC role naming only real CRD resources and
     subresources (`make test`).
  2. `make validate-yaml`: samples, fixtures, kustomize builds and the chart,
     offline, against the CRDs; the valid YAML passes their create-time CEL
     rules.
  3. `make test-gitea` and `make seed-local`: a local Gitea, no Kubernetes.
  4. envtest (`make test`): reconcilers, status, the admission policy, and
     every CEL rule's failing case, create-time ones included, the reaction
     to faked Sandbox and pod status. The reconcilers run under the
     generated RBAC role, so a call a spec makes that the role doesn't
     grant fails that spec; a call no spec makes goes unchecked.
  5. Kind and `make e2e`, only when a human asks: the whole loop on stand-in
     agents. No agent runs it, and nothing waits for it.

  `make check` runs tiers 1, 2 and 4 for every change, and the UI's own
  check (`make ui-test`: typecheck, eslint, Prettier, its tests). Node and kubelet
  behaviour and the Gitea Actions runner itself are not tested before release.
- **A bug fix brings its test.** It adds a test that failed before the fix
  and names the issue in the spec or table case (`#1019`), at the lowest tier
  that sees the bug, and the commit message names the test.
- **Show, don't claim.** When a plan is done, save evidence in
  `docs/screenshots/`. Test output is the evidence; no plan asks an agent for
  Kind evidence (`kubectl get` output, screenshots of a running factory).
- **Report honestly.** If a check fails or a step is skipped, say so in the commit or
  PR with the output.
