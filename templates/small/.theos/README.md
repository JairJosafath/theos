# small (Compute)

Resource requests and limits for one small replica: requests 50m CPU and 64Mi memory,
limits 250m CPU and 256Mi memory.

- **Supports:** `service`, `worker`, `job`, `static-site`, `operator`
- **Writes:** for every type but `operator`, `deploy/k8s/compute.yaml`, a
  strategic-merge patch for the workload named after the Container (a Deployment,
  or a Job for type `job`; a `static-site`'s web server is a Deployment), setting
  the resources of its container `app`. The Deployment layer lists it under
  `patches` in `deploy/k8s/kustomization.yaml`.
- **Operator:** for type `operator`, `config/manager/compute.yaml` instead, a
  strategic-merge patch on kubebuilder's manager Deployment `controller-manager`
  (namespace `system` before kustomize's prefix), setting the resources of its
  container `manager`. The go-kubebuilder Framework lists it under `patches` in
  `config/manager/kustomization.yaml` when a Compute is chosen, so it ships in
  `config/default` and `dist/install.yaml`.

Each file renders to nothing for the types it isn't for, and a `.tmpl` that renders
to only whitespace is left out. See the python-fastapi template's
`.theos/README.md` for how templates are rendered.
