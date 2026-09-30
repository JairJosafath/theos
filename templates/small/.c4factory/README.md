# small (Compute)

Resource requests and limits for one small replica: requests 50m CPU and 64Mi memory,
limits 250m CPU and 256Mi memory.

- **Supports:** `service`, `worker`, `job`
- **Writes:** `deploy/k8s/compute.yaml`, a strategic-merge patch for the workload
  named after the Container (a Deployment, or a Job for type `job`), setting the
  resources of its container `app`. The Deployment layer lists it under `patches`
  in `deploy/k8s/kustomization.yaml`.

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
