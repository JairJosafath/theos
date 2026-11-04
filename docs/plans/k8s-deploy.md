# Plan: deploy Kubernetes apps to a cluster

GitHub issue #1072, filed by the owner (`source/dev`, priority/2: the
webshop's path, [fresh-start.md](fresh-start.md) decision 3). Agents:
otter-1072 (research), heron-1072 (this plan). It builds on the deploy model
of [deploy.md](done/deploy.md) (#1068) and the release images of
[images.md](images.md) (#1069): CI built the image, the operator pushed it by
digest, and a DeployTarget's adapter ships each release.

## Goal

A release of a Kubernetes Container runs on the cluster its DeployTarget
names, at the release's image digest, in the standard GitOps way: the
operator writes one Argo CD `Application` that points at the Container's
repository at the release tag and sets the image, and Argo CD rolls it out.
The Container's status shows the version running, the sync state and the
health. The first cluster is the one the factory runs on, the webshop's path;
another cluster is a later step.

Done when every step below is merged: a release of a `k8s-manifests`
Container with an `argoCD` target produces its `Application` with the digest
in `kustomize.images`, and `status.deploys[]` shows `Deployed` and `Healthy`
once Argo CD reports it. Go tests and envtest with Argo CD's CRD verify it;
the rollout itself is the one tier only Kind shows.

## Where we stand

- **The manifests exist and ship nowhere.** `k8s-manifests` writes a
  Dockerfile and `deploy/k8s/{kustomization,deployment,service,auth}.yaml`
  for `service`, `worker` and `static-site`, with the image placeholder
  `<container>:dev` and no `images:` in the kustomization. Its README says
  shipping stays outside the factory. No template renders a `job` for
  Kubernetes. An operator's `install.yaml` is a release file, not in the
  repository at the tag (`/dist/` is gitignored), and the factory never
  installs it ([design.md](../design.md), "Frameworks").
- **The image exists.** Since #1069, `Shipment.Image` is
  `<repository>@<digest>` of the version shipped, on the Git host's
  registry (`gitea.gitea.svc.cluster.local:3000/<owner>/<name>` in the
  release's cluster).
- **The deploy loop exists.** The DeployTarget has one member, `github`;
  the port is `ports.Deployer` (`Check`, `Ship`, `State`), the member table
  `model.DeployMember` with its needs, the adapters mapped by member name in
  `cmd/main.go`, and the Container reconciler ships each release to each
  entry of `spec.deploy` and reports `status.deploys[]` (version, phase,
  health, URL, reason, message). #1445 changes `State` to take the wanted
  version; an adapter whose target runs one version at a time ignores it.
- **The cluster can read the source but not the image.** The chart's Gitea
  takes anonymous reads (`REQUIRE_SIGNIN_VIEW=false`) and repositories are
  public, so Argo CD in the same cluster clones without a credential. The
  kubelet can't pull: a node resolves no cluster DNS name, and containerd
  refuses plain HTTP; Gitea's Service is a ClusterIP with no fixed address
  and the nodes' containerd knows nothing of it.
- **Nothing reads health back.** The design's "Reading back from Argo CD
  comes later" is now.

## Decisions

1. **Argo CD, as a member `argoCD` of `DeployTarget.spec`.** One object,
   `Application`, carries the source, the destination, the sync policy and
   the image override, and reports sync and health in its status; Flux needs
   two objects and reports no health in one place. Flux is a later member
   with its own adapter; the port stays the same. The factory never installs
   Argo CD on a user's cluster, as it never installs Agent Sandbox: the
   getting-started names what to install. The fields:
   - `namespace`: where Argo CD runs and its Applications live (default
     `argocd`); `project`: the Argo CD project (default `default`).
   - `destination`: `namespace` (required, where the app runs) and `server`
     (default `https://kubernetes.default.svc`, the cluster Argo CD runs on).
   - The cluster the operator writes the Application to is its own, with its
     own ServiceAccount, until step 6 adds a `kubeconfig` key to the Secret
     for another cluster. So `credentialsSecretRef` becomes optional, with
     the CEL rule "set credentialsSecretRef unless the member is argoCD"; an
     `argoCD` target without one is in-cluster, and `Ready` needs no Secret.
   - RBAC: a kubebuilder marker grants the operator `applications` and
     `appprojects` of `argoproj.io` (get, list, watch; create and patch on
     applications; never delete), so the chart's role follows from
     `make manifests`. The CRDs are Argo CD's; the role names them whether
     or not Argo CD is installed, as it names Agent Sandbox's.

2. **`Ship` writes one `Application`; nothing is written to the
   repository.** Server-side apply, field manager `theos`, in
   `argoCD.namespace`, named `<container namespace>-<container name>`,
   labeled `theos.factory.io/namespace` and `theos.factory.io/container`, with
   Argo CD's `resources-finalizer` so a person deleting the Application
   removes the app. The spec:
   - `source`: `repoURL` the Container repository's clone URL (new on the
     port: `Shipment.Repository`, "" where the target holds no source),
     `targetRevision` the release tag, `path` `deploy/k8s`, and
     `kustomize.images: ["<container>=<Shipment.Image>"]`. The image is set
     in the Application, never in a file at the tag: scaffold once holds,
     and a tag is immutable.
   - `destination` from the member; `syncPolicy.automated` with `prune` and
     `selfHeal`, `syncOptions: [CreateNamespace=true]`.
   - A repeat with the same version applies the same object, a no-op, as
     the port asks. A release with no image (`Shipment.Image` "") is
     `ErrArtifactMissing` with "the release has no image", as a missing
     release file is at a `github` target.
   - The factory never deletes an Application: removing an entry of
     `spec.deploy`, or the Container, leaves what runs, as a GitHub release
     stays published. The Application has no ownerReference (another
     namespace); the labels find it.
   - The joined name is readable, not unique: `a-b/c` and `a/b-c` both
     join to `a-b-c` (#1669). As two layers writing one file fail the
     Container with the path, `Ship` and `State` refuse an Application
     whose `theos.factory.io/namespace` and `theos.factory.io/container` labels
     name another Container, with that Container in the message
     ("belongs to Container a/b-c, whose name joins to the same: rename
     one of them"); `Ship` writes nothing, and `State` reports nothing.
     The error is a `model.DeployNameError`, not one of the port's: the
     read step reports it `Failed`, reason `NameRejected`, condition
     `DeployFailed`, with the message, and reads it again each poll, so a
     rename of either Container clears it without a new release (#1677);
     the other Container keeps its Application. No hash suffix: a
     collision is a naming mistake the owner should see, and a suffix
     makes every Argo CD listing opaque. `Ship` and `State` also refuse a
     joined name over 63 characters (`model.ArgoCDApplicationName`
     returns the error) before anything is written: under label tracking
     Argo CD writes the name into the `app.kubernetes.io/instance` label of
     every resource it manages, and a label value holds 63; Argo CD 3.x
     tracks by annotation by default, so not every install would hit it,
     but the guard is cheap and deterministic.

3. **`State` reads the Application back, by a pure rule in
   `internal/model`.** `ArgoCDDeployState` (table-tested, like
   `GitHubDeployState`) takes the Application's `spec.source.targetRevision`,
   `status.sync` (`status`, `comparedTo.source.targetRevision`),
   `status.health` (`status`, `message`) and `status.operationState`
   (`phase`, `message`, and the operation's revision:
   `syncResult.source.targetRevision`, or
   `operation.sync.source.targetRevision` before the result exists):
   - no Application: the zero state, unless the Argo CD namespace isn't
     there either, which refuses the credential as `Ship` does (a Get in a
     missing namespace is NotFound like a missing Application, so `State`
     gets the Namespace to tell, #1668); an operation `Running`, or no sync
     yet, or `OutOfSync`: `Deploying` at the wanted tag;
   - an operation `Failed` or `Error` at the wanted tag: `Failed` at it,
     reason `SyncFailed`, Argo CD's message. One of another revision is
     the previous version's, which Argo CD hasn't replaced yet: `Deploying`
     at the wanted tag (#1667). An operation whose revision Argo CD didn't
     record counts as the wanted tag's;
   - `Synced` at the tag with the operation `Succeeded`: `Deployed` at the
     tag.
   - Health is Argo CD's `Healthy`, `Progressing` and `Degraded` as they
     are; `Suspended`, `Missing` and `Unknown` are `Unknown` with Argo CD's
     message. The URL is empty: Argo CD core has no UI, and where the app
     answers is an ingress matter, later.
   - The wanted version #1445 passes is ignored: one version runs at a
     time, and the Application says which.

4. **`Check` asks the cluster, not Argo CD.** It gets the AppProject
   `argoCD.project` in `argoCD.namespace`: a missing CRD or a NotFound is
   `ErrCredentialRefused` with "Argo CD isn't installed" or "project X
   doesn't exist", Forbidden is `ErrCredentialRefused` naming the verb, and
   anything else is `ErrUnreachable`. `Ready`'s reasons stay the port's.

5. **What a Container needs: a `k8s-manifests` Deployment.** The member's
   row in `deployMembers` has one need, "a Deployment that writes
   deploy/k8s/"; a Container without it is `Incompatible` there, so a `cli`
   or an operator can't name an `argoCD` target. Operators stay out: their
   `install.yaml` is a release file, not a path in the repository, and the
   factory never installs one. A later issue may add a Deployment that
   commits the chart.

6. **`job` gets a `k8s-manifests` row.** The template renders `job.yaml`
   for type `job`: a Kubernetes `Job` from the image, `restartPolicy:
   Never`, `backoffLimit: 3`, and the annotation
   `argocd.argoproj.io/sync-options: Force=true,Replace=true`, so each
   release runs the job again (a `Job`'s template is immutable, and the
   annotation is inert outside Argo CD). `Force=true` is needed (#1628):
   `Replace=true` alone makes Argo CD run `kubectl replace`, an update of
   the existing Job, which the immutable template refuses with `field is
   immutable` at the second release; with `Force=true` it runs `kubectl
   replace --force`, a delete and a create, which is what rerunning a Job
   takes. The README's hand-run rerun does the same for the Job alone, a
   `kubectl delete job` then `kubectl apply -k`, so it recreates nothing
   else in the kustomization (the postgres StatefulSet, say). No
   `ttlSecondsAfterFinished` (#1587): decision 2 syncs with `selfHeal`, which
   reads a Job the TTL deleted as missing, recreates it and so reruns the job
   every TTL. The annotation runs it once per release, and the finished Job and its pod stay, with their logs, until the
   next release replaces them; Argo CD reads a completed Job as Healthy and
   a failed one as Degraded, so `status.deploys[]` shows the outcome. The
   `small` Compute's `compute.yaml` patch targets the workload, Deployment or
   Job. The template's README and kustomization comments say the factory
   ships the files through an `argoCD` target, and the pinned version bumps.

7. **The image, as the node sees it.** In the factory's own cluster the
   image's host is Gitea's in-cluster name, which a node can't resolve, over
   HTTP, which containerd refuses. In the Kind test environment the Gitea
   Service gets a fixed ClusterIP (Kind's service range is `10.96.0.0/16`)
   and `hack/kind.sh registry-hosts`, run by `make cluster`, writes
   containerd a `hosts.toml` into every node mapping
   `gitea.gitea.svc.cluster.local:3000` to `http://<that IP>:3000` for pull
   and resolve, so the image reference in the Deployment is exactly the
   digest #1069 pushed, with no rewrite. It can't live in `hack/kind.yaml`:
   `extraMounts` needs an absolute host path, and the `registry.mirrors`
   patch is ignored once containerd reads `certs.d`. The fixed `clusterIP`
   is immutable, so `make gitea` deletes a Service from before the pin
   (`hack/kind.sh gitea-service`) before it applies `config/gitea` (#1679). The owner organization is public,
   so the pull needs no Secret, as the clone needs none. This is the test
   environment's shortcut and says so: a real cluster pulls from a registry
   with a real name and TLS (#1070's GHCR, or step 6's copy).

8. **The Kind environment installs Argo CD core.** `config/argocd` is a
   kustomization of Argo CD's `core-install.yaml` at a pinned version into
   namespace `argocd` (no API server, UI or dex: the operator reads
   Applications through the Kubernetes API, and a person uses `kubectl`),
   with `make argocd` in `make e2e`'s chain, beside `make agent-sandbox`.
   The webshop sample gets a DeployTarget `kind-cluster` (`argoCD`,
   destination namespace `webshop-apps`, tier development) and `spec.deploy`
   on each Container a `k8s-manifests` Deployment ships; the getting-started
   shows the Application and `status.deploys[]`.

9. **Another cluster is step 6, not the first.** It adds the `kubeconfig`
   key to the Secret (the adapter builds its client from it; in-cluster
   otherwise), the Git host's address as that cluster sees it, and the image
   at a registry its nodes can pull from: `argoCD.imageRegistry`, the
   registry as the nodes see it, and a copy there with `internal/oci`'s copy
   (#1069 decision 7) under the Secret's `registry` keys. It is outside the
   webshop's path, so priority/4.

## Steps

Each step is a GitHub issue that names #1072, lands on its own with `make
check`, and leaves main working.

| Step | Issue | Waits on | How we know |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1072 (`Refs`) | | landed in `docs/plans/` and `docs/design.md` |
| 2. The Argo CD adapter | #1586 | #1445 | `internal/adapters/argocd` on the port, `Shipment.Repository`, `model.ArgoCDDeployState` table-tested on each sync, health and operation state; `test/crds/argocd` holds the `Application` and `AppProject` CRDs; the adapter's tests start envtest with them and run `contract.Deployer`, and show the Application `Ship` writes (source, tag, path, image, sync policy, finalizer, labels), a repeat applying nothing, no image as `ErrArtifactMissing`, and `Check`'s three answers |
| 3. The `argoCD` member and the loop | #1588 | step 2 | the member on `DeployTargetSpec` with its CEL rules (one member; the Secret optional for `argoCD` only), the row in `deployMembers`, `deployTarget`'s conversion, the RBAC markers, the wiring in `cmd/main.go`; `make validate-yaml` on a sample; envtest: a Ready `k8s-manifests` Container whose release has an image gets its Application with the digest, faked Application status makes `status.deploys[]` `Deployed`/`Healthy`, `Degraded`, and `Failed` with `SyncFailed`; a Container without the Deployment is `Incompatible`; a target without a Secret is `Ready`; the reconciler runs under the generated role |
| 4. `job` in `k8s-manifests` | #1587 | | `job.yaml` rendered for type `job` with `Force=true,Replace=true` (#1628) and no TTL (decision 6), `compute.yaml` patching a Job, the README and comments, the version bump; the scaffold's table tests and `make validate-yaml` on the rendered files |
| 5. The Kind environment and the webshop | #1589 | steps 3, 4 | `config/argocd` and `make argocd` in `make e2e`; Gitea's fixed ClusterIP and containerd's `hosts.toml` written by `hack/kind.sh registry-hosts`, tested in `hack/kind_test.sh`; the seed's organization public; the webshop's `kind-cluster` DeployTarget and `spec.deploy` entries pass `make validate-yaml` and `make seed-local`; the getting-started's deploy section |
| 6. Another cluster | #1590 | step 3 | the `kubeconfig` key, `argoCD.imageRegistry` and the copy; the adapter's tests on a second envtest as the remote cluster; priority/4 |

A human run of Kind would show what no tier below it can: Argo CD syncing
the webshop's `orders-api` Application from Gitea, the node pulling the
image by digest through the containerd mapping, and `status.deploys[]`
reaching `Deployed` and `Healthy`.

## Out of scope

- Flux (a later member), and operators (decision 5).
- Copying the image to GHCR (#1070) or ECR (#1071); a pull Secret for a
  private organization.
- An ingress or URL for the app, and reading metrics or logs back.
- A rollback by Argo CD's history: a person pins `version` in `spec.deploy`,
  as the deploy model says.
