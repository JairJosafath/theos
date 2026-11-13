# go-standard (Standard)

How Go code in a theos Container is written, checked and reviewed: a strict
golangci-lint v2 config, the agent guide and role guides, offline style digests,
issue and pull request templates, and the `make check` contract CI runs.

- **Supports:** `cli`; Frameworks: `go-cli`
- **Writes:** `.golangci.yml`, `Makefile`, `.editorconfig`, `AGENTS.md`,
  `docs/style/`, `docs/agents/implementer.md`, `docs/agents/reviewer.md`,
  `.gitea/workflows/check.yaml`, `.gitea/ISSUE_TEMPLATE/task.md`,
  `.gitea/PULL_REQUEST_TEMPLATE.md`
- **Lints:** golangci-lint v2.14 with the standard linters plus 21 more and a
  curated revive rule set; gofumpt and goimports as formatters. No exclusion
  presets, no per-line deduplication, no caps on findings. Every linter was
  checked to fire on code written to break it.
- **Check contract:** `make check` = `go mod download`, `go mod tidy -diff`,
  `golangci-lint fmt --diff`, `go vet`, `golangci-lint run`,
  `go test -race -count=1`. `.gitea/workflows/check.yaml` runs it on pull
  requests and on `main` on a runner labelled `go`, under the status context
  `check / check`, which the protected `main` requires (Gitea Actions' check contexts, `model.CI` in `internal/model/ci.go`).
  A change to only `CHANGELOG.md`, `docs/` or images (`.png`, `.jpg`, `.gif`,
  `.svg`) skips the steps after the checkout and passes, so the context still
  reports; any other file, `README.md` included, or a first push runs them.
  It keeps the toolchain image's Go build cache, which the image ships warm;
  nothing in this Standard redirects `GOCACHE`. The factory publishes its
  apps' released modules to the owner's Gitea package registry, under
  `theos.local` paths: a step before `make check` downloads the ones
  `go.sum` lists with `GOPROXY` set to the registry alone (the job's token
  in `~/.netrc`), and `make check` runs with `GOPRIVATE=theos.local`, so
  no proxy is asked for them; other modules come from proxy.golang.org. The
  factory's agent pods send those paths to the registry alone too.
- **Checks before a release** (`checksBeforeRelease: true`): with the
  Container's `checks: BeforeRelease`, `check.yaml` renders a `check` job that
  runs `make check-light` (`go mod download`, `go mod tidy -diff`,
  `golangci-lint fmt --diff`, `go vet`, `golangci-lint run` and `make build`;
  no tests) on pull requests and on `main`, with the same skip, and a `full`
  job that runs `make check` on a push to a `release-check/**` branch,
  whatever the push changes (status context `check / full (push)`). With
  `EveryChange`, the default, it renders as before.
- **Releases:** `make build` writes `bin/<container>`; `make dist` cross-compiles
  `dist/<container>_<os>_<arch>[.exe]` for Linux, macOS and Windows on amd64 and
  arm64, with `CGO_ENABLED=0`, `-trimpath`, `-X main.version=$(VERSION)` and a
  `SHA256SUMS`. `VERSION` defaults to `git describe --tags --always --dirty`;
  gitea-release passes the tag.
- **Framework contract:** the main package is the module root (`.`) and has a
  package-level `version` string variable for `-ldflags -X`, as go-cli's
  `main.go` does.
- **Role guides:** `docs/agents/implementer.md` and `docs/agents/reviewer.md`,
  which the factory gives its implementer and reviewer Agents, read from `main`.
- **Issues:** `.gitea/ISSUE_TEMPLATE/task.md` has the sections the factory needs;
  an issue without an unchecked item under "Acceptance criteria" is never picked up.
- **Style digests:** `docs/style/` distils Effective Go, Go Code Review Comments,
  the Google Go Style Guide, Go Doc Comments and the Go Proverbs, plus
  cross-language principles. Every rule is marked `[lint]` or `[review]`;
  `docs/style/SOURCES.md` lists the sources with dates.
- **Protect:** list `AGENTS.md`, `Makefile`, `.golangci.yml`, `.editorconfig`,
  `docs/style/` and `docs/agents/` in the catalog entry's `protectedPaths`;
  `.gitea/` is always protected.

## CI systems

`.theos/ci/` keeps the files of each CI system, and a Container gets its own
host's: `giteaActions/` writes `.gitea/`, `githubActions/` writes `.github/`
(the same `make check`, the same issue and pull request templates). On GitHub
Actions the job is named `check` (and `check (full)` with checks before a
release), which is the check main requires; it runs on the runner the
organization's Actions variable `THEOS_CI_RUNS_ON` names (`ubuntu-latest`
without one) and in the toolchain image `THEOS_CI_IMAGE_GO` names, which the operator
sets: GitHub's own runners carry none of the factory's toolchain. `.github/`
is always protected there, as `.gitea/` is on Gitea.

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
