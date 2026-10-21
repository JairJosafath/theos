# Plan: build each Container's image on release

GitHub issue #1069, filed by the owner (`source/dev`). Agent: otter-1069. It
builds on the deploy model of [deploy.md](done/deploy.md) (#1068): CI builds what
ships with no deploy credential, and the operator ships it.

## Goal

Each release of a Container that ships as an image has one: built at the
release tag from the repository's Dockerfile, for `linux/amd64` and
`linux/arm64`, with an SBOM and provenance, and pushed by digest to the Git
host's container registry. The Container's status names the image and its
digest, and the deploys of #1070 to #1072 copy that digest to their target's
registry with the target's credential.

Done when every step below is merged: a release of a `k8s-manifests`
Container yields an image pushed to the owner's registry whose digest shows in
`Container.status.image`, and an operator's `install.yaml` names its image by
digest. Go tests and envtest verify it against an in-memory registry, and
`make test-gitea` against Gitea's own. No test runs BuildKit; building the
image is the one tier only Kind shows.

## Where we stand

- **No image is ever built.** `k8s-manifests` writes a Dockerfile and
  `deploy/k8s/` naming the placeholder `<container>:dev`, and has no release
  workflow at all. The operator Framework's `install.yaml` names
  `controller:<tag>` (`templates/go-kubebuilder/README.md.tmpl`).
- **The runners can't build.** The `base` and `go` runners run jobs in host
  mode in an unprivileged pod with no Docker
  ([ci-runner-isolation.md](ci-runner-isolation.md)).
- **The release workflow is the precedent.** gitea-release's
  `release.yaml` runs `make dist` on the tag and attaches `dist/` to the
  Gitea release with the job's token. The Crew counts a release's runs, and
  the Container keeps `status.newestRelease`, the newest release whose runs
  succeeded.
- **Publishing is the operator's.** The Go module (`PublishGoModule`) and,
  per [app-packages.md](app-packages.md) (#1229, #1232), the npm and PyPI
  files go from the operator to Gitea's registries with the GitProvider's
  credential, never from CI: no package-write credential in a job, and no
  question of whether Gitea's Actions token may write packages.
- **The Deployer already has a slot.** `ports.Shipment.Image` is "the app's
  image by digest, "" until CI builds one (#1069)".

## Decisions

1. **CI builds the image into the release; the operator pushes it.** The
   release run builds an OCI image layout archive and attaches it to the
   release as a file named `<repository>-<tag>.oci.tar`, as `make dist`'s
   files are attached. Once the release's runs succeed, the operator streams
   that file from the release into the owner's container registry on the Git
   host, by digest, with the GitProvider's credential, and tags it with the
   release tag.

   Why not CI pushing with its job token, as the issue suggested: that puts
   a package-write credential in the job that runs the Dockerfile the agents
   wrote, which app-packages ruled out for npm and PyPI; Gitea's Actions
   token may not even write packages; and an upload from the operator is
   tested below Kind, against an in-memory registry. The digest stays the
   build's: pushing a layout moves its bytes unchanged, so the index digest
   BuildKit reports is the digest in the registry.

2. **The build is rootless BuildKit, daemonless, on a runner label of its
   own: `image`.** A third runner image puts `gitea-runner` on a pinned
   rootless BuildKit image (`moby/buildkit:<version>-rootless`, by digest)
   with `git`, `curl` and `jq`. The job runs `buildctl-daemonless.sh`, which
   starts `buildkitd` for that job only with its state under the job's work
   directory, so no layer, cache or process outlives the job. No Docker, no
   privileged container, no added capability.
   - **The one loosening:** rootless BuildKit makes user namespaces, which
     the runtime's default seccomp and AppArmor profiles refuse, so the
     `image` runner's job container runs with both profiles `Unconfined`,
     as uid 1000, and `buildkitd` with `--oci-worker-no-process-sandbox`.
     Only that container of that runner gets them; `make validate-yaml`'s
     runner fence checks it, and the CI fence's NetworkPolicy (label
     `app.kubernetes.io/component: ci-runner`) fences it like the others.
   - **No credential reaches the build.** The job holds only its job token,
     which reads the repository and attaches the file; no Secret of the
     user's, no registry credential. `RUN` steps reach DNS, the Git host and
     public HTTPS, so base images come from their public registries.
   - **The shared runner's gap is #1076's.** Like `base` and `go`, the
     `image` runner is one long-lived host-mode pod per replica until
     #1076's per-project, one-job pods replace it; then the `image` label
     follows. The design's CI fence names the `image` runner in its "Still
     open".

3. **Both platforms, and no emulation.** Every image is built for
   `linux/amd64` and `linux/arm64` (the AWS Frameworks run arm64, most
   clusters amd64). Running an arm64 `RUN` on an amd64 node needs QEMU
   registered in the node's kernel, which needs a privileged pod, so there
   is none: a Dockerfile `RUN`s only in stages built `FROM
   --platform=$BUILDPLATFORM`, cross-compiles for `$TARGETPLATFORM`, and its
   target-platform stages only `COPY`. The factory's Dockerfiles do so, and a
   scaffold test reads each Dockerfile a template renders and fails a `RUN`
   in a stage that isn't on `$BUILDPLATFORM`. A repository's own Dockerfile
   that `RUN`s on the target platform fails the release run with BuildKit's
   error, and the release is `Failed` as any release run is. The
   Deployment's README says so to the agents.

4. **SBOM and provenance travel in the index.** The build asks BuildKit for
   an SBOM (`attest:sbom`, BuildKit's pinned Syft scanner) and provenance
   (`attest:provenance=mode=min`: no build arguments or secrets in it). They
   are attestation manifests in the same image index, so they are pushed
   with it and copied with it, under the same digest. Signing the image
   (cosign, keyless or with a key) is a later issue: it needs a key or an
   identity the factory doesn't have.

5. **Which releases have an image: a table in `internal/model`.** A row per
   catalog choice says whether its release builds one: `k8s-manifests` (any
   type) and `gitea-release` for type `operator`. The rule is
   `model.BuildsImage(deployment, type)`. A Container whose choice builds
   none has no `status.image` and no condition; one whose release lacks the
   archive (a Framework with no Dockerfile at the root) is `NoImage`.

6. **The workflows.**
   - **`k8s-manifests`** gains `.c4factory/ci/giteaActions/.gitea/workflows/image.yaml`,
     a release run on `v*` tags, `runs-on: image`. With a `Dockerfile` at
     the root it builds both platforms with the attestations into
     `<repository>-<tag>.oci.tar` and attaches it to the tag's release,
     creating the release when missing, as `release.yaml` does; without one
     it says so and attaches nothing. The python-fastapi and react-vite
     Dockerfiles move their `RUN` stages to `$BUILDPLATFORM` (uv installs
     the target platform's wheels with `--python-platform`; Vite's output
     is the same on every platform).
   - **`gitea-release` for an operator** gets an `image` job before its
     `release` job (`needs: image`), rendered only for type `operator`. The
     job builds and attaches the archive and outputs the digest; the
     release job runs `make dist VERSION=<tag> IMG=<host>/<owner>/<repository>@<digest>`,
     the host being the Git host's (`gitea.server_url`), so `install.yaml`
     names the image the operator is about to push, by digest, before it is
     pushed. go-operator-standard's `make dist` takes `IMG`, defaulting to
     `controller:<tag>` as now. kubebuilder's Dockerfile already
     cross-compiles; its builder stage moves to `$BUILDPLATFORM`.

7. **The push: `internal/oci`, then the Registry.**
   - **`internal/oci`** is plain Go over go-containerregistry, with no
     Kubernetes: push a layout archive read as a stream (each blob uploaded
     as its tar entry comes, its digest checked, never held whole; the
     manifests, at most 4 MiB each, kept until `index.json` names the
     root), resolve a tag to a digest, and copy an index by digest from one
     registry to another with each side's credential (the primitive #1070
     to #1072 use). It refuses an archive whose `index.json` names other
     than one root, a blob whose content isn't its digest, and a manifest
     over 4 MiB. It reports the platforms, attestations left out.
   - **The Gitea adapter** gets `PublishImage(ctx, repo, tag)`, beside
     `PublishGoModule` (on the Registry port once #1166 makes it): it finds
     the release's `*.oci.tar` file, pushes it to
     `<host>/<owner>/<repository>:<tag>` and links the package to the
     repository. A tag is published once and never replaced: the same
     digest again is a no-op, another digest is `TagTaken`. No archive is
     `NoImage`, two or a refused archive `Invalid`, an unreachable host an
     error, retried. Gitea's token challenge names `ROOT_URL`, which may
     not resolve from the operator, so the adapter asks the token endpoint
     at the GitProvider's own address. `Repository.ImageRegistry` is the
     registry's address, built as `GoProxy` is.
   - **The release copy to GitHub leaves `*.oci.tar` out:** an image
     belongs in a registry, and #1070 copies it to GHCR.

8. **What the Container reports.** The Container reconciler, which owns its
   status and already keeps `newestRelease`, publishes the newest release's
   image once its runs succeed:
   - **`status.image`:** `tag`, `repository` (`<host>/<owner>/<repository>`),
     `digest`, `platforms`, `state` (`Published`, `NoImage`, `Invalid`,
     `TagTaken`, `Failed` while the host can't be reached, retried each
     poll), `message` and `publishedTime`.
   - **The condition `ImagePublished`:** True when the newest release's
     image is pushed; else False with the state as its reason. A Normal Event
     `ImagePublished` and the log line `Published image`; a Warning
     `InvalidImage` or `ImageTagTaken` once per release.
   - **Deploys get the digest.** `Shipment.Image` is
     `<repository>@<digest>` for the version shipped: `status.image`'s when
     the version is its tag, else (a pin) the tag resolved in the registry,
     "" when there is none. What a target does with no image is its
     member's row in the requirement table (#1072's).

9. **How a build reaches the cluster's resources.** One build at a time per
   `image` runner (capacity 1), `runners.image.replicas` of them (default
   1), each limited by its pod (`runners.image.resources`, default requests
   1 CPU and 2 GiB, limits 4 CPUs and 8 GiB) and its work directory, an
   `emptyDir` on disk with a `sizeLimit` (default 20 GiB), never in memory:
   layers are large. Builds beyond that queue in Gitea, and a release whose
   runs haven't ended after an hour says so as today (`ReleaseRunOverdue`).
   `HEAVY_JOBS` is the development device's queue for `make`; it doesn't
   apply in a cluster. The archive is a release file, so Gitea's
   attachment limit bounds it; the chart sets it to 2 GiB.

## Steps

Each step is a GitHub issue that names #1069, lands on its own with `make
check`, and leaves main working.

| Step | Issue | Waits on | How we know |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1069 (`Refs`) | | landed in `docs/plans/` and `docs/design.md` |
| 2. `internal/oci`: push a layout archive, resolve, copy | #1453 | | unit tests against go-containerregistry's in-memory registry: a two-platform index with attestations pushed from a stream, a repeat pushing nothing, a corrupt blob and a second root refused, a manifest over 4 MiB refused, a tag resolved, a copy keeping the digest and the attestations, a bearer-token registry |
| 3. The Gitea adapter's `PublishImage` | #1454 | #1453 | unit tests on a fake Gitea with the in-memory registry for each outcome and the token address; `make test-gitea` pushes a generated two-platform archive to the local Gitea, reads it back by digest and repeats as a no-op; the GitHub copy leaves `*.oci.tar` out (its publish test) |
| 4. `status.image` and `ImagePublished` | #1455 | #1454 | the status type, `model.BuildsImage` table-tested; envtest: a Ready `k8s-manifests` Container whose Crew's release runs succeed reports the image's digest, each state and Event, a pin's `Shipment.Image` resolved, a Container whose choice builds none has no `status.image` |
| 5. The `image` runner | #1456 | | the runner image (`make runner-image`), `config/runner/image.yaml` and the chart's `runners.image`; `make validate-yaml`'s fence: `Unconfined` only on that runner's job container, no privileged, no capability, the ci-runner label; the CI fence in `design.md` names it |
| 6. `k8s-manifests` builds its image | #1457 | #1456 | `image.yaml`; the two Dockerfiles on `$BUILDPLATFORM`; the scaffold test failing a `RUN` on the target platform; the template's README; its pinned version bumped |
| 7. An operator's image | #1458 | #1455, #1456 | gitea-release's `image` job for type `operator`, go-operator-standard's `IMG`, kubebuilder's builder stage on `$BUILDPLATFORM`, the `BuildsImage` row; `make test-templates-operator` runs `make dist IMG=...` and reads the digest in `install.yaml` |

A human run of Kind would show what no tier below it can: the `image` runner
building both platforms of a scaffolded `python-fastapi` service and an
operator, and the Container's `status.image` naming the pushed digest.

### Step 4, as built

- `model.BuildsImage(deployment, type)` is the table `imageBuilders`, one row
  (`k8s-manifests`, any type); `model.ImageEvent` decides the Event once per
  release's state, `model.ImageSettled` that only `Failed` is tried again.
- The newest-release read moved out of `deploy` into `keepNewestRelease`, run
  in `Reconcile` before `publishImage` (`internal/controller/container_image.go`)
  and `deploy`, so the image is pushed before a deploy ships it.
- A push runs when the newest release's tag differs from `status.image.tag`
  or the last state was `Failed`; a `Failed` push asks for the next poll
  (`DeployPoll`). The Reconciler remembers the last Event per Container, so a
  status write that fails after its Event doesn't say it twice.
- A pin's image is `ImageDigest` on `<ImageRegistry>/<lower name>`; a
  registry that can't be reached fails the ship as `GitHostUnreachable`.
- Envtest (`container_image_test.go`) drives the fake provider's
  `setImage`, `failImagePushes` and `setRegistryImage`.

## Out of scope

- Copying the image to GHCR (#1070), ECR (#1071) or a cluster's registry
  (#1072), and setting the image in `deploy/k8s/` at deploy (#1072).
- Signing images; a build cache shared between releases (with #1076's
  per-project runners); a pull request check that builds the image.
- `aws-fargate-ts`'s Docker asset, which CDK builds at synth (#1071 decides
  whether it takes this image instead).
