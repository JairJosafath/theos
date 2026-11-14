# go-operator-standard (Standard)

How a Kubernetes operator in Go, built with kubebuilder, is written, checked and
reviewed in a theos Container: kubebuilder's Makefile and golangci-lint
config turned into the `make check` contract CI runs, the agent guide with
kubebuilder's rules, role guides, offline style digests, and issue and pull
request templates.

- **Supports:** `operator`; Frameworks: `go-kubebuilder`
- **Writes:** `Makefile`, `.golangci.yml`, `.editorconfig`, `AGENTS.md`,
  `docs/style/`, `docs/agents/implementer.md`, `docs/agents/reviewer.md`,
  `.gitea/workflows/check.yaml`, `.gitea/ISSUE_TEMPLATE/task.md`,
  `.gitea/PULL_REQUEST_TEMPLATE.md`. These are the files `kubebuilder init`
  writes that go-kubebuilder leaves out (`Makefile`, `.golangci.yml`,
  `AGENTS.md`), plus go-standard's.
- **Lints:** golangci-lint v2.14 with the config kubebuilder v4.16.0 scaffolds
  (copyloopvar, depguard against `sort`, dupl, errcheck, ginkgolinter, goconst,
  gocyclo, govet, ineffassign, lll, modernize, misspell, nakedret, prealloc,
  revive's comment-spacings and import-shadowing, staticcheck, unconvert,
  unparam, unused), without its `logcheck` module plugin, which needs a custom
  golangci-lint build the toolchain image doesn't have. gofmt and goimports
  as formatters. What kubebuilder scaffolds, `create api` included, lints clean.
- **Check contract:** `make check` = `go mod download`, `go mod tidy -diff`,
  the generated-files check, `golangci-lint fmt --diff`, `go vet`,
  `golangci-lint run`, `go test -race -count=1` on every package but
  `test/e2e`, with envtest. The generated-files check snapshots the working
  tree, runs `make manifests generate`, and fails listing every file they
  added, changed or deleted; uncommitted work that is already generated
  passes. `make test-e2e` (a Kind cluster) is never part of it.
  `.gitea/workflows/check.yaml` is go-standard's: it runs `make check` on pull
  requests and on `main` on a runner labelled `go`, under the status context
  `check / check`, skips the steps for a change to only `CHANGELOG.md`,
  `docs/` or images, and fetches the factory's `theos.local` modules from
  the package registry alone, as go-standard's README describes.
- **Checks before a release** (`checksBeforeRelease: true`): with the
  Container's `checks: BeforeRelease`, `check.yaml` renders a `check` job that
  runs `make check-light` (`go mod download`, `go mod tidy -diff`, the
  generated-files check, `golangci-lint fmt --diff`, `go vet`, `golangci-lint
  run`; no tests) on pull requests and on `main`, with the same skip, and a
  `full` job that runs `make check` on a push to a `release-check/**` branch,
  whatever the push changes (status context `check / full (push)`). With
  `EveryChange`, the default, it renders as before.
- **Tools:** kubebuilder v4.16.0, controller-gen v0.22.0, kustomize v5.8.1 and
  setup-envtest (the controller-runtime version in `go.mod`) come from PATH
  when the one there prints the pinned version, as in the toolchain image;
  else `make` downloads them once into `bin/` under a versioned name. The
  envtest binaries come from `/usr/local/kubebuilder/bin` when they are the
  Kubernetes minor of `go.mod`'s `k8s.io/api`, else setup-envtest downloads
  them into `bin/`. golangci-lint comes from PATH at any version, else v2.14.0
  into `bin/`. Nothing needs a cluster.
- **Releases:** `make build` writes `bin/<container>`; `make dist` cross-compiles
  `./cmd` into `dist/<container>_<os>_<arch>[.exe]` for Linux, macOS and
  Windows on amd64 and arm64, with `CGO_ENABLED=0`, `-trimpath` and
  `-X main.version=$(VERSION)`, then runs `make build-installer` with
  `IMG=$(IMG)` into `dist/install.yaml`, and writes a `SHA256SUMS` of the
  binaries and `install.yaml`. `IMG` is the manager image, for `docker-build`
  too; it defaults to `controller:$(VERSION)`, a tag no registry has. `make
  dist` and `make clean` remove only those files, so a `dist/chart/` from
  kubebuilder's helm plugin stays. `build-installer` and `deploy` set the
  image on a copy of `config/`, so no committed file changes. `VERSION`
  defaults to `git describe --tags --always --dirty`; gitea-release passes
  the tag, and the image it built, by digest on the Git host's registry
  where the factory pushes it, as `IMG` (#1458).
- **Framework contract:** kubebuilder's `go/v4` layout: the main package in
  `./cmd`, `hack/boilerplate.go.txt` for controller-gen's headers, the
  kustomize tree in `config/` with `config/default` and the manager image
  named `controller` in `config/manager`, e2e tests (build tag `e2e`) under
  `test/e2e`. `cmd/main.go`'s header names the year (`Copyright 2026.`) that
  `make generate` writes into generated files, so they don't change in a new
  year. A `version` string variable in package main is stamped by `-X` when
  present; go-kubebuilder's has none, and the linker ignores `-X` without it.
- **Role guides:** `docs/agents/implementer.md` and `docs/agents/reviewer.md`,
  which the factory gives its implementer and reviewer Agents, read from `main`.
  They add kubebuilder's rules: CLI scaffolding, generated files, envtest.
- **Role subagents** (since v1.8.0, #259): `.claude/agents/` holds the Claude
  Code subagents the role guides name: the reviewer's three read-only lenses
  (`review-correctness`, `review-security`, `review-style`) and the
  implementer's read-only `explorer` and its `test-writer`. The guides say
  when to delegate and that the role's decision is never delegated. The five
  files are go-standard's, the same in go-standard, go-operator-standard,
  ts-standard, python-standard and aws-standard; a test in theos keeps them so.
- **Issues:** `.gitea/ISSUE_TEMPLATE/task.md` has the sections the factory needs;
  an issue without an unchecked item under "Acceptance criteria" is never picked up.
- **Style digests:** `docs/style/` is go-standard's, with every `[lint]` mark
  set to what this `.golangci.yml` enforces and the rest `[review]`.
  `docs/style/SOURCES.md` lists the sources with dates.
- **Protect:** list `AGENTS.md`, `Makefile`, `.golangci.yml`, `.editorconfig`,
  `docs/style/`, `docs/agents/`, `.claude/agents/`, `.claude/`, `CLAUDE.md` and
  `.mcp.json` in the catalog entry's `protectedPaths`;
  `.gitea/` is always protected. `.claude/`, `CLAUDE.md` and `.mcp.json`
  configure the agents that check a change, so no agent changes them, not
  even through a Standard change (#259). Never the generated files: agents regenerate
  them in normal pull requests.

## CI systems

`.theos/ci/` keeps the files of each CI system, and a Container gets its own
host's: `giteaActions/` writes `.gitea/`, `githubActions/` writes `.github/`
(the same `make check`, the same issue and pull request templates). On GitHub
Actions the job is named `check` (and `check (full)` with checks before a
release), which is the check main requires; it runs on the runner the
organization's Actions variable `THEOS_CI_RUNS_ON` names (`ubuntu-latest`
without one) and in the toolchain image `THEOS_CI_IMAGE_GO` names, which the operator
sets for an organization: GitHub's own runners carry none of the factory's
toolchain. Since v1.7.0, without the variable the job runs in the factory
release's own toolchain image, `ghcr.io/jairjosafath/theos/go:<release>`,
the release the scaffold names (`model.ToolchainRelease` in theos), so a
repository in a personal account, rendered by `hack/render` or adopted by
hand runs its check too (#263). The runner must be able to pull that image:
the variable names another where it can't. `.github/` is always protected
there, as `.gitea/` is on Gitea.

Before `make check`, the job on GitHub Actions says where the owner's own
packages are. GitHub serves a Go module from its repository, so `GOPRIVATE`
is the owner's module paths and the job's token is Git's credential in
`~/.netrc`. `~/.npmrc` sends the owner's npm scope to GitHub's npm registry,
and `~/.netrc` logs in to a Python index only when the GitProvider's
`spec.packages` names one: GitHub has none. The job's token reads this
repository and the packages this repository may read, and goes to GitHub
alone.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.theos/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files, such as the workflow with its `${{ }}` expressions, are copied
byte for byte.
