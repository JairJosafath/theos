# API types, controllers and logging

How to add or change a kind, a reconciler or a log line. The rules in
[../../AGENTS.md](../../AGENTS.md) still apply.

## Kubebuilder commands

### Create API (a new kind in this project)
```bash
kubebuilder create api --group c4 --version v1beta1 --kind <Kind> --resource --controller
```

Catalog kinds (Framework, Standard, Compute, Deployment, Connector, Domain, Agent) have
no reconciler of their own; create them with `--controller=false`. GitProvider's
reconciler only registers the operator's event hook and reports `Events`
([plans/done/git-events.md](../plans/done/git-events.md)).

### Controller for Core Kubernetes Types
```bash
kubebuilder create api --group core --version v1 --kind Pod \
  --controller=true --resource=false
```

### Webhooks
v1 has no webhooks by design: CEL rules and a ValidatingAdmissionPolicy cover
validation, and the operator checks compatibility after creation. If one is added
later, create all types together with
`kubebuilder create webhook --group c4 --version v1beta1 --kind <Kind> --defaulting --programmatic-validation`.

## API design

Every kind has a type in `api/v1alpha1` and in `api/v1beta1`; `v1beta1` is
the hub and the storage version (`+kubebuilder:storageversion` sits on its
types alone; `TestEveryCRDStoresTheHub` in `internal/controller` holds every
generated CRD to that), and each `v1alpha1` type converts to it
([plans/done/api-versions.md](../plans/done/api-versions.md)). A field change goes to
both versions, with the conversion, and `test/compat` holds each served
version to the last release's schema. Moving the storage marker to a newer
version needs nothing else: `StorageMigrator` (`internal/controller`) rewrites
the stored objects at the next start and logs `Rewriting the stored objects at
the storage version` and `Rewrote the stored objects at the storage version`
per CRD, with `storedVersions`, `storage` and `objects`; a failed pass logs
`Failed to migrate the stored objects; trying again` and the wait.

**Key markers for** `api/*/*_types.go`:

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"

// On fields:
// +kubebuilder:validation:Required
// +kubebuilder:validation:Enum=static-site;service;job;worker
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="softwareSystemRef is immutable"
// +kubebuilder:default="value"
```

**After a change to `*_types.go` or its markers**, regenerate what they
describe, and commit it with the change:

```bash
make manifests  # config/crd/bases, config/rbac/role.yaml, and the chart's copies of them
make generate   # the zz_generated DeepCopy methods
```

`make manifests` writes `dist/chart/templates/crd` and the chart's manager
role too (`hack/chart-crds.sh --write`, `hack/chart-role.sh --write`), so a
regenerated base never lands without the chart's copy: that happened three
times in a day under yolo mode, where nothing checks before the merge, and
main went red each time (#1670, #1689). `hack/chart-crds.sh` and
`hack/chart-role.sh` (in `make validate-yaml`, so in `make check`) still fail
when the copies differ, and CI's *Lint* job then runs `make check-generated`,
which fails when a generated file differs from what is committed.

- **Use** `metav1.Condition` for status (not custom string fields). Phases are a
  convenience on top of conditions.
- **Use predefined types**: `metav1.Time` instead of `string` for dates
- **Follow K8s API conventions**: Standard field names (`spec`, `status`, `metadata`)
- **Keep fields minimal.** `displayName` and `description` on every story kind;
  `metadata.name` is the stable id.
- **A `DeployTarget` has exactly one member** (CEL), each with its adapter
  ([architecture.md](../architecture.md), Deployer): `github` (`repository`,
  `latest`) and `gitlab` (`project`, and `url` for a self-hosted GitLab,
  unset for gitlab.com) need `credentialsSecretRef`; `argoCD` is an Argo CD in the
  operator's cluster and needs none (the rule reads "set credentialsSecretRef
  unless the member is argoCD"): `namespace` where Argo CD runs and its
  Applications live (default `argocd`), `project` (default `default`),
  `destination.namespace` (required) and `destination.server` (default
  `https://kubernetes.default.svc`). Which Containers may name a member is
  `deployMembers` in `internal/model/deploy.go`: `argoCD` needs a Deployment
  that writes `deploy/k8s/`; the rest is `Incompatible`.

## Controller design

**RBAC markers in** `internal/controller/*_controller.go`:

```go
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
```

The envtest suite runs every reconciler as the operator's ServiceAccount, bound
to the generated `config/rbac/role.yaml`, so a call a marker doesn't grant fails
the spec that makes it, with `forbidden` in the reconciler's log.

**Implementation rules:**
- **Idempotent reconciliation**: Safe to run multiple times; a repeat reconcile of a
  `Ready` Container changes nothing
- **Re-fetch before updates**: `r.Get(ctx, req.NamespacedName, obj)` before `r.Update` to avoid conflicts
- **Structured logging**: `log := log.FromContext(ctx); log.Info("msg", "key", val)`
- **Owner references**: Enable automatic garbage collection (`SetControllerReference`)
- **Watch secondary resources**: Use `.Owns()` or `.Watches()`, not just `RequeueAfter`
- **Thin reconcilers**: Decisions about types, layers and files belong in
  `internal/model` and `internal/scaffold`, not in the controller
- **Read a Container's catalog choices through `Container.Choices()`**, never
  `Spec.FrameworkRef` and its siblings: a Container may name a Bundle
  (`spec.bundleRef`), and `Choices()` gives the spec's refs, else the
  expansion in `status.bundle`. Only `resolve` reads the Bundle to expand it,
  and only until the Container is scaffolded; after that `reportBundleCurrent`
  reads it to report `BundleCurrent`, never to apply it.
- **A Ready Container only reports.** `resolve` reads the system's
  `spec.domainRefs` once, for the scaffold; after that `reportDomainsApplied`
  (`container_domains.go`) compares them with `status.scaffoldedDomains`, the
  names the scaffold recorded (and its commit's `Scaffolded-Domain` trailers,
  for a recovered one), and reports `DomainsApplied`, never rescaffolds. The
  Standard update renders with those same Domains, not the system's current
  ones. They are recorded, never inferred from `scaffoldedFrom`: a changed
  choice or a Domain named like a Connector would be misread.

## Logging

**Follow Kubernetes logging message style guidelines:**

- Start from a capital letter
- Do not end the message with a period
- Active voice: subject present (`"Deployment could not create Pod"`) or omitted (`"Could not create Pod"`)
- Past tense: `"Could not delete Pod"` not `"Cannot delete Pod"`
- Specify object type: `"Created repository"` not `"Created"`
- Balanced key-value pairs

```go
log.Info("Starting reconciliation")
log.Info("Created repository", "url", repoURL)
log.Error(err, "Failed to push initial commit", "container", name)
```

**Reference:** https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines

## References

### Essential Reading
- **Kubebuilder Book**: https://book.kubebuilder.io
- **controller-runtime FAQ**: https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md
- **Good Practices**: https://book.kubebuilder.io/reference/good-practices.html
- **C4 model**: https://c4model.com

### API Design & Implementation
- **API Conventions**: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md
- **CEL validation rules**: https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules
- **ValidatingAdmissionPolicy**: https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/
- **Markers Reference**: https://book.kubebuilder.io/reference/markers.html

### Tools & Libraries
- **controller-runtime**: https://github.com/kubernetes-sigs/controller-runtime
- **Gitea API**: https://docs.gitea.com/development/api-usage
- **Dev Container features**: https://containers.dev/implementors/features/
