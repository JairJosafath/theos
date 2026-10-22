# gitea-release (Deployment)

Ships a Container's release files on a Gitea release whenever a version tag is
pushed: binaries for a Go CLI, an npm tarball for a Node CLI, a wheel and an
sdist for a Python CLI, a `.vsix` for an extension, the manager binaries and
`install.yaml` for an operator, the synthesized CloudFormation cloud assembly
for an AWS Container. No registry, no store: download them from the release
page, or, for AWS, let a `cloudFormation` deploy target ship them.

- **Supports:** `cli`, `extension`, `operator`, and `function`, `service`,
  `static-site`, `worker` and `job` for the AWS Frameworks, the one Deployment
  a Framework that deploys itself takes (#1607; `worker` and `job` since
  v1.8.0, #1773)
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
  `make build-installer`, the image set to `IMG`) and a `SHA256SUMS` of both;
  aws-standard's synthesizes both tiers and writes their cloud assemblies,
  flat (`<tier>.manifest.json`, `<tier>.<stack>.template.json`,
  `<tier>.<stack>.assets.json`, `asset.<hash>.zip` or `asset.<hash>.<ext>`).
  Only files directly in `dist/` are attached, and no dotfile, so neither a
  `dist/chart/` from kubebuilder's helm plugin nor the `dist/.gitignore`
  aws-standard writes is. `RELEASING.md` names the files per Framework.
- **An operator's image** (#1458): for a Container of type `operator` the
  workflow has an `image` job first, as k8s-manifests' `image.yaml`: rootless
  BuildKit on the `image` runner builds the root `Dockerfile` for linux/amd64
  and linux/arm64 with an SBOM and provenance into `<repository>-<tag>.oci.tar`,
  attached to the release, and fails without a Dockerfile. The factory pushes
  the archive to the Git host's container registry as
  `<host>/<owner>/<repository>` (lowercase), and the `release` job names that
  image by its index digest: `make dist VERSION=<tag>
  IMG=<host>/<owner>/<repository>@<digest>`, so `install.yaml` as attached
  applies. The factory pushes a tag once, so the `release` job asks the
  registry for the tag's digest first and names it when it holds one; only a
  first run names the digest just built. An `aws-fargate-ts` service's
  release has the same `image` job (#1610), before a `release` job that names
  no image: its stack takes the image as the `ImageUri` deploy parameter, and
  the deployer copies the digest the factory pushed to the cloud. Every other
  Container has no image job (`model.BuildsImage`).
- **Needs:** a runner labelled `go` (the Go and Node toolchain image, with uv
  for Python projects), which the checks use too, and for a release that
  builds an image one labelled `image` (BuildKit). The workflow token creates the release and
  uploads the files; no secrets.
- **Release:** `git tag v0.1.0 && git push origin v0.1.0`, or create the tag in
  Gitea. Only the operator's Git user may push `v*` tags to a protected
  repository. Running the workflow again for a tag replaces its files.

The workflow is a template only to pick the `image` job: its `${{ }}`
expressions are Gitea's, not the factory's, written as `{{`${{ … }}`}}`. It puts
npm's cache in the runner's memory-backed `/tmp`, uv's cache
and Python in `.uv/` in the memory-backed checkout, and leaves Go's build cache
where the toolchain image ships it, warm.
