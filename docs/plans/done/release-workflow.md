# Publish images and the chart from a release workflow

**Goal:** A device installs a released theos without building anything: a
`vX.Y.Z` tag publishes the operator, UI and toolchain images and the Helm chart.
Issue #99; follows the chart (#98, [helm-chart.md](helm-chart.md)); spec in
[user-issues.md](user-issues.md), *Not in this plan*.

## Shape

- `.github/workflows/release.yml` runs on a pushed `v*.*.*` tag. One job builds
  the images in order with the local Docker daemon, so the agent and runner
  images build on the toolchain images just built, then pushes them all.
- **Registry:** GitHub Container Registry, `ghcr.io/<owner>/theos/<name>`,
  authenticated with the workflow's `GITHUB_TOKEN` (`packages: write`).
- **Images**, each tagged with the version without its `v` (`0.1.0`):
  `operator`, `ui`, the toolchains `go` and `base`, and the images built on them,
  `agent-go`, `agent-base`, `gitea-runner-go`, `gitea-runner-base`. The test
  environment's own image `theos-session` is not published; `seed` is since #127. The
  toolchains install `linux-x64` binaries, so every image is `linux/amd64`.
- **Chart:** `helm package dist/chart --version V --app-version V`, pushed as an
  OCI artifact to `oci://ghcr.io/<owner>/theos/charts` and attached to a
  GitHub release for the tag. `values.yaml` defaults the image repositories to
  GHCR and leaves the tags empty, so a packaged chart runs its own version's
  images.
- **Dry run:** a pull request that changes the workflow builds every image and
  packages the chart without pushing or releasing, so the build is checked
  before a tag depends on it.

## Steps

1. The workflow, the values defaults, the chart README's install from GHCR.
2. Docs: README *Try it* points devices at the release, `docs/dev/testenv.md`.

## Done when

- The dry run passes on the pull request.
- `make helm-lint` and `make check` pass.
- Cutting the first release is the user's call: pushing a `v0.1.0` tag runs the
  workflow for real.
