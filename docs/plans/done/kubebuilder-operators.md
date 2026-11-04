# Plan: the catalog builds Kubernetes operators with kubebuilder

GitHub issue #723, asked for by the owner on 2026-10-09; the shape decided by
the maintainer on the issue. Agent: otter-723.

## Goal

A Brief whose idea extends Kubernetes ("an operator that backs up every PVC
labelled `backup: nightly`") gets a Container of type `operator`, scaffolded
from a pinned `kubebuilder init` under a kubebuilder-aware Standard. Its repo's
own CI passes (manifests and generated code up to date, lint, envtest), its
agents add kinds with `kubebuilder create api`, and a version tag publishes the
binaries and `dist/install.yaml` on a Gitea release. The factory never installs
the operator, and nothing here needs a cluster.

Done when the steps below are merged, and with them #723's *Done when*: the
type exists and the planner picks it for an idea that extends Kubernetes; the
`go-kubebuilder`, `go-operator-standard`, `small` and `gitea-release` combo
scaffolds with no layer conflict and the rendered repo's `make check` passes,
before and after `kubebuilder create api`; the release attaches the binaries
and `install.yaml`; one command regenerates the template; design.md and the
templates' `.theos/README.md` contracts say what ships.

## Where things stand

- `ContainerType` (`api/v1alpha1/shared_types.go:47`) has `static-site`,
  `service`, `job`, `worker`, `mobile-app`, `cli`, `extension`, mirrored in
  `internal/model/model.go`, `internal/model/planner.md:391`,
  `ui/src/k8s/types.ts:66` and `ui/src/model/describe.ts:83`. The operator
  never branches on a type (design.md, *A new Container type*).
- A template renders `.tmpl` files with `text/template` and copies the rest;
  it cannot run a command. Two layers writing one path fail the Container
  (`internal/scaffold/scaffold.go`).
- A catalog entry ships whole (catalog-breadth C8): its folder, a
  `templates/versions.txt` pin, a chart `catalog.entries` item at the same
  ref, a `templateCombos` row (`internal/scaffold/templates_test.go`), its
  `.theos/README.md` and a `docs/getting-started.md` line.
- The `go` toolchain image (`hack/toolchain-go/Dockerfile`) has Go 1.26,
  Node, uv and golangci-lint 2.14.0, and warms client-go v0.37.0 and
  controller-runtime v0.25.0 into its build cache. The default agent image,
  `agent-go`, is built on it; `gitea-release` and the checks run on its
  runner. Neither has kubebuilder, setup-envtest, controller-gen or kustomize.
- This repo is itself a kubebuilder 4.16.0 project: controller-tools v0.22.0,
  kustomize v5.8.1, envtest for the `k8s.io/api` version in `go.mod`.

## Decisions, and why

The maintainer's comment on #723 settled the type, the Framework and
Standard split, the first-cut release, the images, the planner cue and the
opt-in test. These are the choices it left to the plan.

1. **The tools go in the `go` toolchain image, not only the agent image.**
   kubebuilder 4.16.0, setup-envtest, the envtest binaries for Kubernetes
   1.37 (client-go v0.37) in `/usr/local/kubebuilder/bin` (envtest's default
   `KUBEBUILDER_ASSETS`), controller-gen v0.22.0 and kustomize v5.8.1, each
   pinned by an `ARG` at the top of the Dockerfile. *Why:* `agent-go` is built
   on that image, so agents get them; the runner needs controller-gen and
   kustomize for the up-to-date check and `make build-installer` and envtest
   for `make test`; the minimal agent image has no Go to run kubebuilder
   with. A runner job that downloads them every run would be slow and
   flaky. The few tens of MB on the runner are the cost.
2. **The Standard's Makefile prefers the image's tools.** kubebuilder's
   Makefile downloads its tools into `bin/`. go-operator-standard's keeps
   that as the fallback for a person's laptop, but takes a tool from `PATH`
   (and the envtest binaries from `/usr/local/kubebuilder/bin`) when its
   version matches the pin, so CI and agents run offline of tool downloads.
   *Why:* one Makefile works in the runner, the agent and on a laptop.
3. **What `kubebuilder init` writes, and who owns it.**

   | Path | Layer |
   | --- | --- |
   | `go.mod`, `go.sum`, `cmd/main.go`, `PROJECT`, `config/**`, `hack/boilerplate.go.txt`, `test/e2e/**`, `test/utils/**`, `Dockerfile`, `.dockerignore`, `.gitignore`, `README.md` | `go-kubebuilder` (Framework) |
   | `Makefile`, `.golangci.yml`, `AGENTS.md`, `docs/agents/**`, `docs/style/**`, `.editorconfig`, `.gitea/workflows/check.yaml`, the PR and issue templates | `go-operator-standard` |
   | `.devcontainer/**`, `.github/**`, `.custom-gcl.yml` | dropped |

   *Why:* the Framework is the program, the Standard is the rules (design.md,
   *Standard: the rules as a layer*). `.github/` is replaced by the
   Standard's Gitea workflow; `.custom-gcl.yml` builds a custom golangci-lint
   with kubebuilder's `logcheck` plugin, which would mean compiling a linter
   in every CI run, so the Standard uses the toolchain's golangci-lint with
   kubebuilder's linters minus that plugin. Every `.go` file is a `.tmpl`
   (go-cli's rule) so the module path renders.
4. **Names.** Module `theos.local/<project>/<container>` (go-cli's);
   domain `<project>.theos.local`; `PROJECT`'s `projectName` and the
   `config/` name prefix the Container's name. *Why:* the module path lets
   App modules import the operator's `api/` package; one domain per Project
   keeps two operators' groups apart.
5. **The regeneration script is a make target in this repo,**
   `make template-go-kubebuilder`: it downloads the pinned kubebuilder into
   `.dev/make/bin`, runs `kubebuilder init --plugins go/v4` with placeholder
   names in a temp dir, applies the table in decision 3, turns the
   placeholders into template fields, renames `.go` files to `.go.tmpl`, and
   replaces `templates/go-kubebuilder/` except its `.theos/`. A Go test
   holds the placeholder list and the drop list, so a kubebuilder bump that
   writes a new path fails the test instead of slipping into a layer. *Why:*
   a bump is one command and a diff to review, not hand edits.
6. **The opt-in target is `make test-templates-operator`.** It renders the
   operator combo through `internal/scaffold` into a temp dir, `git init`s
   it, runs its `make check`, then `kubebuilder create api --group demo
   --version v1 --kind Widget --resource --controller`, `make manifests
   generate`, and its `make check` again, and fails if `create api` wrote a
   protected path. It runs on the device (Go, golangci-lint and the tools of
   decision 1 fetched into `.dev/make/bin` when missing), no cluster, no
   Docker. `make check` doesn't run it; a step that touches either operator
   template runs it and pastes its tail into the pull request. *Why:* a
   kubebuilder project drifts more than a CLI, and the agents' first act on
   it is `create api`.
7. **Steps land hidden until the Framework can be scaffolded.** The type
   lands first with no Framework supporting it; the planner's honest-first
   rule (catalog-breadth C1) already answers `needs-input.md` for a type the
   catalog can't build, so main never offers an operator it can't scaffold.
   The Framework and the Standard land together, with the chart entries and
   `small` supporting `operator`, in one step: a user sees them together.
8. **Release, first cut.** go-operator-standard's `make dist VERSION=<tag>`
   cross-compiles `./cmd` like go-standard (plus `SHA256SUMS`) and runs
   `make build-installer IMG=controller:<tag>` into `dist/install.yaml`.
   gitea-release attaches `dist/` unchanged, as for every other type. The
   scaffolded README says to build and push the image and set it in
   `install.yaml` (`kustomize edit set image` or a text replace) before
   applying it. *Why:* the maintainer kept image publishing out of this
   issue; a placeholder that names the tag is the clearest thing to replace.

## Steps

Each step is its own issue and pull request and leaves main working.

1. **The design.** (#723) This plan. *Done when:*
   merged.
2. **The operator tools in the `go` toolchain image.** (#728) Decision 1, in
   `hack/toolchain-go/Dockerfile`, each version an `ARG` in one place, and
   the image docs (DEVELOPMENT.md or `docs/dev/testenv.md`, wherever the
   toolchain's contents are listed) saying so. *Done when:* `make check`
   passes, and where the device has Docker, the image builds and runs
   `kubebuilder version`, `controller-gen --version`, `kustomize version`
   and `ls /usr/local/kubebuilder/bin` (the pull request says if it
   didn't). A human's Kind run would show an agent sandbox running
   `kubebuilder version`.
3. **The `operator` Container type.** (#729) The enum value and constant in
   `api/v1alpha1/shared_types.go`, `make manifests generate`, the mirrors in
   `internal/model/model.go`, `planner.md`'s type list, `ui/src/k8s/types.ts`
   and `describe.ts` (with its Vitest), and design.md's type text. No
   catalog entry supports it yet. *Done when:* `make check` passes with an
   envtest that a Container of type `operator` is accepted, and the UI's
   tests pass.
4. **go-kubebuilder and go-operator-standard.** (#730) Depends on steps 2 and 3.
   Decisions 2 to 6: the regeneration target and its test, both templates
   with their `.theos/README.md` contracts, the opt-in
   `make test-templates-operator`, and catalog-breadth C8 for both (tags,
   `versions.txt`, chart entries, `small` supporting `operator`,
   `templateCombos` row, `docs/getting-started.md`), design.md's catalog
   text. go-operator-standard's `dist` target lands here (decision 8);
   gitea-release doesn't support `operator` yet. *Done when:* `make check`
   and `make validate-yaml` pass with the combo rendered and no layer
   conflict, `make template-go-kubebuilder` on a clean tree leaves no diff,
   and `make test-templates-operator` passes (its tail in the pull request).
5. **The planner picks an operator.** (#731) Depends on step 4. `planner.md` learns
   the cue: the idea extends Kubernetes, or watches and reconciles cluster
   objects; `go-kubebuilder`'s catalog description carries it. *Done when:*
   `make check` passes with a test or planner fixture that the guide states
   the cue and that the PVC-backup idea maps to `operator` with
   `go-kubebuilder`.
6. **gitea-release ships an operator.** (#732) Depends on step 4. gitea-release
   bumps to support `operator` (its README, the chart entry,
   `versions.txt`), the scaffolded README's image note (decision 8), and
   design.md's release text. *Done when:* `make check` and
   `make validate-yaml` pass with the combo `go-kubebuilder`,
   `go-operator-standard`, `small`, `gitea-release` rendered, and the
   rendered repo's `make dist VERSION=v0.1.0` writes the binaries,
   `SHA256SUMS` and `install.yaml` into `dist/` (run by
   `make test-templates-operator`, extended to run it). A human's Kind run
   would show a pushed tag producing the release.

## Not in this plan

- **Images.** Building the operator's image (`ko` to Gitea's registry)
  changes design.md ("no builds, registries") and passes a registry
  credential. Filed as its own issue once step 6 lands; the credential part
  is the owner's (GOVERNANCE.md).
- **Installing the operator.** The factory never installs CRDs or
  ClusterRoles. kubebuilder's Kind `test/e2e` stays in the repo for a person
  (`make test-e2e`), outside `make check`.

## Done

The chart's catalog lists `go-kubebuilder` and `go-operator-standard`; a
Brief whose idea extends Kubernetes gets an `operator` Container that
scaffolds to Ready, passes its own CI, takes `kubebuilder create api` from its
agents, and ships its binaries and `install.yaml` on a tag.
