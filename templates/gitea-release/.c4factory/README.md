# gitea-release (Deployment)

Ships a Container's release files on a Gitea release whenever a version tag is
pushed: binaries for a CLI, a `.vsix` for an extension. No registry, no store:
download them from the release page.

- **Supports:** `cli`, `extension`
- **Writes:** `.gitea/workflows/release.yaml`
- **Contract:** the repository's `Makefile` has a `dist` target that takes
  `VERSION=<tag>` and writes the release files, and only those, into `dist/`.
  go-standard's cross-compiles the binaries plus `SHA256SUMS`; ts-standard's
  packages the `.vsix` and fails when the tag doesn't match `package.json`.
- **Needs:** a runner labelled `go` (the Go and Node toolchain image), which the
  checks use too. The workflow token creates the release and uploads the files;
  no secrets.
- **Release:** `git tag v0.1.0 && git push origin v0.1.0`, or create the tag in
  Gitea. Only the operator's Git user may push `v*` tags to a protected
  repository. Running the workflow again for a tag replaces its files.

The workflow is copied byte for byte: its `${{ }}` expressions are Gitea's, not the
factory's. It puts npm's cache in the runner's memory-backed `/tmp` and leaves Go's
build cache where the toolchain image ships it, warm.
