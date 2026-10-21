# k8s-manifests (Deployment)

A Dockerfile and plain Kubernetes manifests, built with kustomize. Shipping stays
outside C4 Factory: Argo CD, Flux or a pipeline picks these files up.

- **Supports:** `service`, `worker`, `static-site`
- **Writes:** `Dockerfile`, `.dockerignore`, (since v1.6.0, for Gitea Actions)
  `.gitea/workflows/image.yaml`, and in `deploy/k8s/`: `kustomization.yaml`,
  `deployment.yaml`, `service.yaml` for types `service` and `static-site`, and
  (since v1.4.0) `README.md`: how to build the image, set it, preview and apply
  the manifests, what the other layers' files there are, the port and probes.
- **Expects from other layers:** `deploy/k8s/compute.yaml` from the Compute layer (a
  patch listed under `patches`), and `deploy/k8s/connector-<name>.yaml` from each
  Connector (listed under `resources`). The Deployment loads a ConfigMap
  `<container>-<connector>` per Connector through `envFrom`. With the
  `postgres` Connector (since v1.5.0) it also sets `PGPASSWORD` from Secret
  `<container>-postgres`, key `password`.
- **The Dockerfile follows the Framework** (`.Framework`):
  - `python-fastapi`: a uv project with an ASGI app at `app.main:app`, served by
    uvicorn on port 8000 as user 1000; the probes ask `GET /healthz`.
  - `python-cli` (since v1.3.0): the same uv build, running `python -m app` as
    user 1000; it serves nothing, so the Deployment has no port and no probes
    (Kubernetes restarts the container when it exits) and, as a `worker`, no
    Service. The worker stops on the SIGTERM a pod gets when it is stopped.
  - `react-vite`: `npm ci` and `npm run build` on Node 24, then
    `nginxinc/nginx-unprivileged` serves `dist/` on port 8080 as user 101; the
    probes ask `GET /`, the page itself.
  - Any other Framework gets no `Dockerfile` or `.dockerignore`: the
    repository brings its own, and the manifests assume port 8000 and
    `GET /healthz`.

**The release builds the image** (since v1.6.0). On each `v*` tag,
`.gitea/workflows/image.yaml` runs on the `image` runner: with a `Dockerfile` at
the root, `buildctl-daemonless.sh` (rootless BuildKit, no Docker) builds it for
`linux/amd64` and `linux/arm64`, with an SBOM (BuildKit's Syft scanner, pinned
by digest) and provenance (`mode=min`), into an OCI image layout archive,
`<repository>-<tag>.oci.tar`, attached to the tag's Gitea release (created when
missing). The operator pushes it from there to the Git host's container
registry. Without a Dockerfile the run says so and attaches nothing. Nothing
emulates the other platform, so **a Dockerfile must cross-build**: it `RUN`s
only in stages `FROM --platform=$BUILDPLATFORM` and its target stage only
`COPY`s. The factory's do: uv installs the target platform's wheels
(`--python-platform`), and Vite builds once. A repository's own Dockerfile that
`RUN`s on the target platform fails the release run.

The Deployment runs container `app` from image `<container>:dev`, a placeholder to
override with `kustomize edit set image`. Labels: `app.kubernetes.io/name` (the
Container) and `app.kubernetes.io/part-of` (the SoftwareSystem).

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
