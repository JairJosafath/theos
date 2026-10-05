# The Helm release installs the whole factory

**Goal:** A device runs a working factory with two commands: apply Agent
Sandbox, `helm install`. The release brings Gitea, seeds it, and runs the
`base` and `go` CI runners next to the operator and the UI, all on published
images. Issue #127; follows the chart ([helm-chart.md](helm-chart.md), #98) and
the release workflow ([release-workflow.md](release-workflow.md), #99).

**Why:** Today the chart installs only the operator and the UI. Gitea, the seed
Job and the runners come from `make gitea seed` and `config/runner/`, which need
a checkout and locally built `:dev` images (`imagePullPolicy: Never`).

## Shape

- **Gitea** is a chart part behind `gitea.enabled` (on by default once the last
  step lands). It runs in its own namespace, `gitea.namespace` (default
  `gitea`), so its in-cluster URL stays `http://gitea.gitea.svc.cluster.local:3000`,
  the one the templates, samples and docs name. The chart creates that
  namespace unless `gitea.createNamespace=false`; the Namespace and Gitea's
  volume carry `helm.sh/resource-policy: keep`, so `helm uninstall` never
  deletes repositories. The manifests follow `config/gitea/` (rootless image,
  SQLite with WAL, the same probes, requests and settings), with the storage
  class and size as values.
- **The admin** comes from Secret `gitea-admin` in that namespace. The chart
  generates its password on install and keeps it on upgrade (`lookup`); or
  `gitea.admin.existingSecret` names one with `username`, `password` and
  `email`.
- **Gitea off:** `gitea.enabled=false` installs none of Gitea, the seed or the
  runners; `gitProviders` then point at an existing Gitea, as in v0.1.0.
- **The seed** is a `post-install,post-upgrade` hook Job in Gitea's namespace
  running the published `seed` image, with its ServiceAccount and Roles as
  ordinary release resources. It runs after every upgrade too: it changes
  nothing that is in place, and a new chart version brings new template tags
  (`templates/versions.txt`) that Standards may pin. It waits for Gitea for up
  to `gitea.seed.waitSeconds` (new `GITEA_WAIT` in `seed.sh`, default 120 s as
  now), since on a fresh install the Gitea image may still be pulling. The Job
  stays after a failure, for its log; the next upgrade replaces it.
- **Projects:** `gitea.projects` lists project names. For each, the seed
  creates the organization and stores Secret `gitea-credentials` in the
  namespace of that name (which must exist, as for `gitProviders`), and the
  chart creates GitProvider `gitea` there, pointing at the release's Gitea with
  that Secret, as a hook after the seed. Default: none; a device lists its
  projects and runs `helm upgrade` to add one.
- **Runners:** `runners.base` and `runners.go` (each `enabled`, default on with
  Gitea, `image`, `resources`) are Deployments in Gitea's namespace on the
  released `gitea-runner-base` and `gitea-runner-go` images. Their
  registration token is the chart's, in Secret `gitea-runner` (generated on
  install, kept on upgrade), which Gitea takes as its global token on start
  (`GITEA_RUNNER_REGISTRATION_TOKEN`); the seed leaves it alone
  (`RUNNER_SECRET=""`). So the runners don't wait for a hook, and
  `helm install --wait` works. The `go` runner
  keeps its Go build cache on the node (`runners.go.goCache`, default
  `/var/cache/c4factory/go/ci`), as `config/runner/go.yaml` does.
- **The UI's health view** lists Gitea and the runners when Gitea is enabled.
- **Private images.** GHCR packages are private until the owner makes them
  public. The chart supports both ways:
  - `global.imagePullSecrets` (`[{name: ...}]`, `kubernetes.io/dockerconfigjson`
    Secrets) goes on the pods of the UI, Gitea, the seed and the runners;
    the operator's template is generated, so it takes the same list as
    `manager.imagePullSecrets`. The Secret must exist in the release namespace
    and in Gitea's. A device makes it from a gh token with `read:packages`.
  - On Kind, `docker pull` with that token and `kind load docker-image`, and no
    pull secret. This is also the way for the agent images, which run in
    project namespaces in Sandboxes the operator creates, outside the chart.
- **Images:** *Release* publishes `seed` with the others; its default
  repository is `ghcr.io/jairjosafath/c4factory/seed`, tag `Chart.appVersion`.
- **Agent Sandbox** stays a prerequisite the device applies first: another
  project's controller and CRDs.
- **The Kind test environment** keeps `make gitea seed runner-*` and
  `config/gitea`, `config/runner`, `hack/seed` as they are; `make deploy` still
  installs the operator with kustomize. The chart's Gitea copies
  `config/gitea`'s settings, and both name the other in a comment so a change
  reaches both.
- **Versions:** v0.1.0 ships the chart as it is; this work ships as v0.2.0.
  Pushing either tag is the owner's call.

## Steps

Each step is a pull request to main that leaves main working; every step but
the last says `Part of #127`, the last `Closes #127`. New behaviour stays off
(`gitea.enabled: false`) until step 5.

1. **Plan and design.** This plan; `docs/design.md`, *Distribution and issues*,
   says what the release contains and what a device installs itself.
2. **The seed image is published.** `seed.sh` takes `GITEA_WAIT`; *Release*
   builds and pushes `seed` (and its dry run builds it); the chart README's
   image list names it.
3. **Gitea in the chart, off.** `templates/gitea/`: Namespace, admin Secret,
   ConfigMap, PVC, Deployment, Service; `gitea.*` values with `enabled: false`;
   `make helm-lint` also renders the chart with `gitea.enabled=true`.
4. **The seed, projects and runners in the chart, off.** The seed hook Job and
   its RBAC, the per-project GitProviders, the runner Deployments,
   `global.imagePullSecrets` on every pod, the UI components for Gitea and the
   runners; `make helm-lint` renders them.
5. **On by default, tested.** `gitea.enabled: true`. *Test Chart* builds the
   operator, UI, seed and runner images, kind-loads them (the pull request's
   code isn't published yet), installs Agent Sandbox and the chart with Gitea
   and project `webshop`, then checks: organizations `templates` and `webshop`
   exist, Secret `webshop/gitea-credentials` and GitProvider `webshop/gitea`
   exist (a GitProvider has no status), both runners are registered (Gitea's
   admin runner list), the manager and UI are ready. The Go toolchain is
   several GB and *Release* builds it (#156 checks it), so *Test Chart* runs
   the `go` runner on the `gitea-runner-base` image without its Go cache: it
   checks the chart's Deployment and registration, not the toolchain.
   `dist/chart/README.md` gives the device's install sequence and both ways to
   pull private images; README *Try it* points at it.
6. **Verified from a release.** Once the owner pushes `v0.2.0` and *Release*
   publishes it: on a fresh Kind cluster of the pool, apply Agent Sandbox,
   `helm install` from `oci://ghcr.io/jairjosafath/c4factory/charts`, nothing
   built or loaded locally (pulled with a pull secret or after making the
   packages public), and save the `kubectl get` output and a Gitea screenshot
   in `docs/screenshots/`. If the tag isn't pushed yet, step 5 closes #127 and
   this check is filed as its own issue (#156), waiting on nothing but the tag.

## Done when

- `make helm-lint` renders the chart with Gitea on and off, and `make check`
  passes.
- *Test Chart* installs the chart with Gitea on a fresh Kind cluster with
  Agent Sandbox and gets a seeded Gitea, the operator, the UI and the `base`
  and `go` runners, on main after the merge.
- *Release* builds and publishes `seed` (its pull request dry run, since
  removed from main, built it in #130).
- `dist/chart/README.md` gives the install sequence and how private images are
  pulled.
