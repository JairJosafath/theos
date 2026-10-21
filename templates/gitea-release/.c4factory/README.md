# gitea-release (Deployment)

Ships a Container's release files on a Gitea release whenever a version tag is
pushed: binaries for a Go CLI, an npm tarball for a Node CLI, a wheel and an
sdist for a Python CLI, a `.vsix` for an extension, the manager binaries and
`install.yaml` for an operator. No registry, no store: download them from the
release page.

- **Supports:** `cli`, `extension`, `operator`
- **Writes:** `.gitea/workflows/release.yaml`, and `RELEASING.md`: how to
  cut a release, check one locally and recover from a failed run.
- **Contract:** the repository's `Makefile` has a `dist` target that takes
  `VERSION=<tag>` and writes the release files, and only those, into `dist/`.
  go-standard's cross-compiles the binaries plus `SHA256SUMS`; ts-standard's
  packages the `.vsix` (`vscode-extension`) or packs the `.tgz` with `npm pack`
  (`node-cli`), and fails when the tag doesn't match `package.json`;
  python-standard's builds the wheel and sdist with `uv build` (`python-cli`)
  and fails when the tag doesn't match `pyproject.toml`; go-operator-standard's
  cross-compiles the manager binaries and writes `install.yaml` (kubebuilder's
  `make build-installer`, the image set to `IMG`) and a `SHA256SUMS` of both.
  Only files directly in `dist/` are attached, so a `dist/chart/` from
  kubebuilder's helm plugin is not.
- **An operator's image** (#1458): for a Container of type `operator` the
  workflow has an `image` job first, as k8s-manifests' `image.yaml`: rootless
  BuildKit on the `image` runner builds the root `Dockerfile` for linux/amd64
  and linux/arm64 with an SBOM and provenance into `<repository>-<tag>.oci.tar`,
  attached to the release, and fails without a Dockerfile. The factory pushes
  the archive to the Git host's container registry as
  `<host>/<owner>/<repository>` (lowercase), and the `release` job names that
  image by its index digest: `make dist VERSION=<tag>
  IMG=<host>/<owner>/<repository>@<digest>`, so `install.yaml` as attached
  applies. Other types have no image job.
- **Needs:** a runner labelled `go` (the Go and Node toolchain image, with uv
  for Python projects), which the checks use too, and for an operator one
  labelled `image` (BuildKit). The workflow token creates the release and
  uploads the files; no secrets.
- **Release:** `git tag v0.1.0 && git push origin v0.1.0`, or create the tag in
  Gitea. Only the operator's Git user may push `v*` tags to a protected
  repository. Running the workflow again for a tag replaces its files.

The workflow is a template only to pick the operator's `image` job: its `${{ }}`
expressions are Gitea's, not the factory's, written as `{{`${{ … }}`}}`. It puts
npm's cache in the runner's memory-backed `/tmp`, uv's cache
and Python in `.uv/` in the memory-backed checkout, and leaves Go's build cache
where the toolchain image ships it, warm.
