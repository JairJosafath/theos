# go-kubebuilder (Framework)

A Kubernetes operator in Go 1.26: what `kubebuilder init --plugins go/v4` (kubebuilder
4.16.0) writes, with controller-runtime v0.25.0 and client-go v0.37.0, ready for
`kubebuilder create api`.

- **Supports:** `operator`
- **Layout:** kubebuilder's `go/v4` layout: the manager in `cmd/main.go`, the
  kustomize manifests under `config/` (`config/default` deploys it, `config/manager`
  is the manager's Deployment `controller-manager` with its container `manager`),
  the e2e suite under `test/e2e` and its helpers under `test/utils`. `api/` and
  `internal/controller/` appear with the first `kubebuilder create api`.
- **Writes:** `go.mod`, `go.sum`, `cmd/main.go`, `PROJECT`, `config/**`,
  `hack/boilerplate.go.txt`, `test/e2e/**`, `test/utils/**`, `Dockerfile`,
  `.dockerignore`, `.gitignore`, `README.md`. The rest of what `kubebuilder init`
  writes is not here: its `Makefile`, `.golangci.yml` and `AGENTS.md` are the
  Standard's (go-operator-standard writes its own), and `.devcontainer/`,
  `.github/` and `.custom-gcl.yml` are dropped.
- **Module:** `c4factory.local/<project>/<container>`, so the Project's other
  Containers can import the operator's `api/` package. Rename it with
  `go mod edit -module` once the code has a real home.
- **Names:** the domain is `<project>.c4factory.local`, one per Project, so two
  operators' API groups stay apart; `PROJECT`'s `projectName`, the `config/` name
  prefix and namespace (`<container>-system`) and the e2e suite's names come from
  the Container's name; the leader election ID is
  `<container>.<project>.c4factory.local`. The license header,
  `hack/boilerplate.go.txt`, says `Copyright YEAR.`; the generated Go files say
  `Copyright 2026.`
- **Compute:** `config/manager/kustomization.yaml` lists `compute.yaml` under
  `patches` when a Compute is chosen. That Compute writes
  `config/manager/compute.yaml`, a strategic-merge patch on the Deployment
  `controller-manager`, container `manager`; small does.
- **Standard:** a Standard brings the `Makefile` and the lint config;
  go-operator-standard's runs kubebuilder's targets (`manifests`, `generate`,
  `test` with envtest, `build-installer`) and builds `./cmd`. Other Standards must
  do the same: `make manifests generate` is how `kubebuilder create api` expects
  the project to be regenerated.
- **Devcontainer:** `.c4factory/devcontainer.json` uses `c4factory/go:1.26`, the
  toolchain CI and agents use, which has kubebuilder, controller-gen, kustomize,
  setup-envtest and the envtest binaries. gopls formats with gofmt, not gofumpt,
  as the Standard's goimports and gofmt formatters do.
- **Regenerated, never hand-edited.** `make template-go-kubebuilder` in C4 Factory
  runs the pinned `kubebuilder init` with placeholder names, sorts every path it
  writes by the table in `hack/template-go-kubebuilder/main.go` (and fails on a
  path no rule matches), turns the placeholders into template fields, escapes the
  files' own `{{ }}`, adds the Compute patch to
  `config/manager/kustomization.yaml`, and appends `/dist/` to `.gitignore` (the
  Standard's `make dist` writes there, and `.gitignore` is the Framework's). It replaces everything here except
  `.c4factory/`. To change a file, change the tool, or bump `KUBEBUILDER_VERSION`
  in C4 Factory's `Makefile`, and review the diff.
- **Every `.go` file is a `.tmpl`**, even without template expressions: C4
  Factory is a Go module too, and a plain `.go` file here would join it and
  break its build. Other files are `.tmpl` only when they name the Project or
  Container.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte. Paths are never rendered, only contents.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Standard`, `.Compute` and `.Deployment` (empty when none is chosen),
and `.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, `dart`,
plus the `text/template` builtins.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
