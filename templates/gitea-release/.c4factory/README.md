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
  `make build-installer`, the image set to `controller:<tag>`) and a
  `SHA256SUMS` of both. Only files directly in `dist/` are attached, so a
  `dist/chart/` from kubebuilder's helm plugin is not. No image is built or
  pushed: whoever installs an operator's release builds the image and sets it
  in `install.yaml`, as the operator's README says.
- **Needs:** a runner labelled `go` (the Go and Node toolchain image, with uv
  for Python projects), which the checks use too. The workflow token creates the release and uploads the files;
  no secrets.
- **Release:** `git tag v0.1.0 && git push origin v0.1.0`, or create the tag in
  Gitea. Only the operator's Git user may push `v*` tags to a protected
  repository. Running the workflow again for a tag replaces its files.

The workflow is copied byte for byte: its `${{ }}` expressions are Gitea's, not the
factory's. It puts npm's cache in the runner's memory-backed `/tmp`, uv's cache
and Python in `.uv/` in the memory-backed checkout, and leaves Go's build cache
where the toolchain image ships it, warm.
