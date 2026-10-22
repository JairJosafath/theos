# Argo CD CRDs

The `Application` and `AppProject` CRDs from [Argo CD](https://github.com/argoproj/argo-cd)
v3.5.6 (`manifests/crds/application-crd.yaml` and `appproject-crd.yaml` at the
tag), for envtest: the Argo CD adapter's tests (`internal/adapters/argocd`)
start envtest with them. The Kind test environment installs Argo CD core at the
same version (`config/argocd`, step 5 of [docs/plans/k8s-deploy.md](../../../docs/plans/k8s-deploy.md));
keep both at the same version.
