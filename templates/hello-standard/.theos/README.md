# hello-standard (Standard)

The smallest possible quality contract, for the end-to-end tests: `make check`
runs `hello.sh` and expects `hello, world`.

- **Supports:** `cli`; Frameworks: `hello`
- **Writes:** `Makefile`, and `.gitea/workflows/check.yaml` or `.github/workflows/check.yaml`, by the Container's CI system
- **Check contract:** `make check`, which the agents run after each round.
  `.gitea/workflows/check.yaml` runs `sh hello.sh` on a runner labelled `base`
  under the status context `check / check`, which the protected `main` requires
  (Gitea Actions' check contexts, `model.CI` in `internal/model/ci.go`).
- **No role guides:** without `docs/agents/implementer.md` and `reviewer.md` the
  agents work from their prompts alone.

## CI systems

`.theos/ci/` keeps the files of each CI system, and a Container gets its own
host's: `giteaActions/` writes `.gitea/`, `githubActions/` writes `.github/`
(the same `make check`, the same issue and pull request templates). On GitHub
Actions the job is named `check` (and `check (full)` with checks before a
release), which is the check main requires; it runs on the runner the
organization's Actions variable `THEOS_CI_RUNS_ON` names (`ubuntu-latest`
without one) and in the toolchain image `THEOS_CI_IMAGE_BASE` names, which the operator
sets for an organization: GitHub's own runners carry none of the factory's
toolchain. Since v1.3.0, without the variable the job runs in the factory
release's own toolchain image, `ghcr.io/jairjosafath/theos/base:<release>`,
the release the scaffold names (`model.ToolchainRelease` in theos), so a
repository in a personal account, rendered by `hack/render` or adopted by
hand runs its check too (#263). The runner must be able to pull that image:
the variable names another where it can't. `.github/` is always protected
there, as `.gitea/` is on Gitea.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.theos/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files, such as the workflow with its `${{ }}` expressions, are copied
byte for byte.
