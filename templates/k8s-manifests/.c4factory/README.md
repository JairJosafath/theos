# k8s-manifests (Deployment)

A Dockerfile and plain Kubernetes manifests, built with kustomize. Shipping stays
outside C4 Factory: Argo CD, Flux or a pipeline picks these files up.

- **Supports:** `service`, `worker`
- **Writes:** `Dockerfile`, `.dockerignore`, and in `deploy/k8s/`: `kustomization.yaml`,
  `deployment.yaml`, and `service.yaml` for type `service` only.
- **Expects from other layers:** `deploy/k8s/compute.yaml` from the Compute layer (a
  patch listed under `patches`), and `deploy/k8s/connector-<name>.yaml` from each
  Connector (listed under `resources`). The Deployment loads a ConfigMap
  `<container>-<connector>` per Connector through `envFrom`.
- **v1 limitation:** the Dockerfile assumes the python-fastapi Framework: a uv project
  with an ASGI app at `app.main:app`, served by uvicorn on port 8000 with `GET /healthz`.
  Other Frameworks need their own Deployment template or a Dockerfile of their own.

The Deployment runs container `app` from image `<container>:dev`, a placeholder to
override with `kustomize edit set image`. Labels: `app.kubernetes.io/name` (the
Container) and `app.kubernetes.io/part-of` (the SoftwareSystem).

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
