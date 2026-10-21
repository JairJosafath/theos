# k8s-manifests (Deployment)

A Dockerfile and plain Kubernetes manifests, built with kustomize. Shipping stays
outside C4 Factory: Argo CD, Flux or a pipeline picks these files up.

- **Supports:** `service`, `worker`, `static-site`
- **Writes:** `Dockerfile`, `.dockerignore`, and in `deploy/k8s/`: `kustomization.yaml`,
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

The Deployment runs container `app` from image `<container>:dev`, a placeholder to
override with `kustomize edit set image`. Labels: `app.kubernetes.io/name` (the
Container) and `app.kubernetes.io/part-of` (the SoftwareSystem).

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
