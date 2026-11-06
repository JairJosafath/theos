# Tests, the Kind cluster and deploying

"The test environment" here is the Kind cluster a human tests the factory in, not a
`TestEnvironment`, the tools agents lease ([plans/done/test-environments.md](../plans/done/test-environments.md)).

How to test and run the factory. Go tests are the gate: `make check` for
every change. A Kind cluster exists only when a human asks to run tests in it;
agents never create, lease or deploy to one, and never run `make e2e`. The
human runs it against the dedicated Kind clusters of the device's pool
(`theos`, `theos-2`, ...; `make cluster`), never a real cluster.
Every make target works on `KIND_CLUSTER`, by default the pool cluster this
checkout leased, else `theos`; kubectl calls name it with `--context
kind-<name>`.

## Test tiers

Test a rule at the lowest tier that can see it ([design](../design.md#test-tiers)):

| Tier | Runs | Verifies |
| --- | --- | --- |
| 1. Table tests | `make test`, no API server | `internal/model` decisions, `internal/scaffold` rendering of every template, the Agent Sandbox and pod specs and the fence's NetworkPolicy (`sandbox_fence_unit_test.go`), the generated RBAC role naming only CRD plurals and subresources they have (`rbac_unit_test.go`) |
| 2. Offline YAML | `make validate-yaml`, in `make check` | samples, e2e fixtures, kustomize builds, the chart, rendered manifests, against the CRDs: valid YAML passes their create-time CEL rules |
| 3. Local Gitea | `make test-gitea`, `make seed-local` | the Gitea API's real behaviour, the seed, template tags |
| 4. envtest | `make test` | reconcilers, status, ownerReferences, the admission policy, every CEL rule's failing case (create-time and on `oldSelf`), the reaction to faked Sandbox and pod status, the generated RBAC, for the calls the specs make: the reconcilers run as the operator's ServiceAccount bound to `config/rbac/role.yaml`, so a call no spec makes is not checked |
| 5. Kind | `make e2e`, factory-ops, only when a human asks | the whole loop on stand-in agents: four specs on the `hello-html` app in `test/e2e/testdata/apps` |

Tiers 1 to 4 run on any device, lite ones included, with no lease; `make
check` runs 1, 2 and 4, and they are the gate. Tier 5 is a human's: no issue
needs it, no agent runs it, and no workflow starts Kind except on
`workflow_dispatch`. What only a real node would catch (Agent Sandbox pods
running, the fence enforced by a CNI, node and kubelet behaviour, the Gitea
Actions runner itself) is not tested before release; a customer's issue
reports it. Test output is the evidence.

The e2e suite runs only stand-in agents and needs no model keys: its
`BeforeSuite` fails if any Agent's harness isn't `stand-in` or any Agent
references a model credential Secret, and a unit test (no build tag) shows
that guard failing. `go vet -tags=e2e ./test/e2e/...` checks the suite
compiles without a cluster.

The scripts in `hack/` (the heavy-job slots, the check cache, the cluster
pool, the seed) have shell tests of their own, `hack/**/*_test.sh`.
Each fakes what it calls (docker, kind, kubectl, gh, claude, curl), so none
needs a cluster or the network; `make test-hack` runs them all at once
(`hack/test-hack.sh`), and `make check` runs that. A new `*_test.sh` under
`hack/` joins it by its name alone; keep it free of a real cluster and the
network.

## Testing & Development

```bash
make test                # Unit and envtest tests of the packages the change affects (real K8s API + etcd, no cluster needed)
make test TEST_ALL=1     # The same, every package
make check               # manifests, generate, fmt, vet, lint, validate-yaml, test-hack, ui-test and test
make validate-yaml       # The YAML against the built-in schemas and our CRDs, offline
make test-hack           # The shell tests of hack/ (hack/**/*_test.sh), all at once, about a minute
make test-templates-operator  # The operator combo's make check before and after kubebuilder create api, then its make dist; not in make check
make test-templates           # The webshop's, go cli's, vscode extension's and AWS template combos' make check, kustomize build and make dist (COMBO=... for one); fetches from npm, PyPI and the Go proxy, never calls AWS; not in make check
make cluster gitea seed  # Kind cluster, Gitea, and the seeded templates
make deploy sample       # Build and deploy the operator into Kind, apply the webshop sample
make session             # Run the test Session pod; make session-kill kills its client
make gitea-local seed-local  # A Gitea of this checkout's own, seeded like Kind's (no kubectl)
make test-gitea          # the Gitea adapter against a local Gitea (make gitea-local), or THEOS_GITEA_URL if set
make test-gitea-kind     # the Gitea adapter against the seeded Gitea of the Kind cluster
make toolchain-base runner-base agent-sandbox agent-image-base  # Minimal toolchain (with uv), CI runner (label base), Agent Sandbox, agent image
make argocd              # Argo CD core (config/argocd), where the webshop's Containers deploy to
make e2e-apps            # The e2e fixture project apps: one hello-world Container (test/e2e/testdata/apps)
make toolchain-go runner-go agent-image-go  # Go, Node, uv and kubebuilder toolchain, CI runner (label go) and agent image
make runner-image        # CI runner (label image): rootless BuildKit, which builds release images
make claude-secret       # The real agent's token, once for every project, from .dev/secrets/claude-token (devices.md) or CLAUDE_TOKEN=...
make e2e                 # Only when the user asks: all of the above but the Go images, then check the whole loop in Kind
make down / make up       # Stop the factory to free the machine; bring it back healthy
make doctor              # Check every part of the factory and say what is wrong
```

Controller tests use **Ginkgo + Gomega** (BDD style) with the real manager and every
reconciler; see `internal/controller/suite_test.go`. A fake Git host serves the real
`templates/`, so envtest also proves some templates render together. Business-rule tests
use the standard `testing` package with table tests; `internal/scaffold/templates_test.go`
renders every folder in `templates/` in a realistic combination, without envtest, and
fails on a render error, a path two layers write, or YAML that doesn't parse. A new
template needs a row there. In tests, write with merge patches
(`patch(obj, func(){...})`), not `Update`: the operator may adopt the object meanwhile.

`make test` tests only the packages the change since `origin/main` (its merge
base, with uncommitted and untracked files) can affect: a package with a changed
file (its directory or one below it, so embedded files count) and every package
whose test binary links one, transitively (`hack/test-packages.sh`, #1295). It
prints which ran and why: `make test: testing 2 of 21 packages, ...; changed:
internal/model`, or `testing every package (21): <reason>`. A changed non-Go
file in a package, or a changed `//go:embed` line, also tests `cmd`: its
`TestDockerContextHasEmbeddedFiles` checks the embeds of every package a
Dockerfile builds without linking them (#1321). A file directly under
`test/e2e/` (the e2e suite) tests nothing; one in `test/e2e/standin/` tests
that package. It tests every package on the branch `main` (a detached HEAD at
main's commit is no branch: it diffs against `origin/main` and tests no
package), without a merge base, with `TEST_ALL=1` or `FORCE=1`, and
after a change to a file a Go test may read without importing it: `go.mod`,
`go.sum`, the `Makefile`, the `hack/` files Go tests read (`hack/seed/`,
`hack/boilerplate.go.txt`, `hack/setup.sh`, `hack/release.sh`, `hack/new.sh`,
`hack/go-test.sh`), `config/` (the CRDs and RBAC envtest loads), `api/`,
`dist/`, `templates/`, `test/crds/`, any `testdata/`, the workflows, a
Dockerfile, `README.md`, `docs/getting-started.md` or a file in no listed path.
A change only to `docs/`, `.claude/`, `.vscode/`, `ui/`, the rest of
`.github/`, a top-level `*.md` or the rest of `hack/` outside its Go packages
tests no package; `make test-hack` gates the shell scripts (#1318). A Go test
that names a new `hack/` path fails `hack/test-packages_test.sh` until the
path joins that list. In the factory's own CI (`.gitea/workflows/check.yaml`,
#2058) the checkout has `origin/main` and the commit's history without blobs, so
a pull request selects against `origin/main` as on a device; a push to main
sets `TEST_BASE` to main's previous head (`TEST_ALL=1` when that head is zeros
or can't be fetched), since its detached HEAD is no branch. The workflow also
sets `LINT_CHANGED_ONLY=1`, which makes `make check` lint only when
`hack/go-changed.sh` finds a `*.go`, `go.mod`, `go.sum`, `.golangci.yml` or
`.custom-gcl.yml` changed since the same base. `land`'s `make check` selects
the same way, and so does the *Tests* workflow on a pull request (against
its base branch); its `paths` filter skips a pull request that changes only
`*.md`, `docs/`, `ui/`, `config/ui/`, `config/samples/`, `test/e2e/`, the
linter configs, `hack/` (but the four files it names), `dev`, `.vscode/` or
`.github/` (but its own workflow) files. *Tests* runs on pushes to main
too, with the same `paths`, and on a pushed `vX.Y.Z` tag: there and on a
manual run it sets `TEST_ALL=1` and tests every package, and that run
catches what a selection missed (#1522). Main's run is the one that shows a
red main while yolo mode merges a pull request unchecked (#1556). With `COVER=1`,
`cover.out` covers the packages that ran (`mode: set` and nothing else when none
did); run `make test TEST_ALL=1 COVER=1` for the whole module's coverage. DigitalOcean's `gta`
selects the same way, but it loads every package of every module in the build
list (`go list ...`), which this module's `go.mod` can't (`go mod tidy` wants
more requirements) and which takes a minute where `go list -test ./...` takes
half a second.

`make test` bounds each package at `TEST_TIMEOUT` (20m; the Makefile says why;
the *Tests* workflow gives its loaded self-hosted runners 60m, #1504). The
controller suite ends itself 30 s before that, failing the spec that was running by
name. With `THEOS_SLOWEST_SPECS=FILE` it writes its 10 slowest specs to FILE; the
*Tests* workflow prints them, so a slow run's log says which specs to speed up.

The controller suite runs its specs one after another, so `make test` runs it
as `THEOS_TEST_PROCS` go tests at once (default 2), beside one go test of the
other packages (`hack/go-test.sh`, #1283). Each part gets
`THEOS_TEST_SHARD=i/n` and runs its share of the suite's top-level containers
against its own control plane and manager; a container is never split, since
Ginkgo already runs them in random order but the specs inside one may share
state. The split is the same in every part, and a part that ran another
number of specs than its containers hold fails. Go's test cache answers each
part on its own, and their profiles (`COVER=1`) and slowest specs
(`THEOS_SLOWEST_SPECS`) are merged into one. With `THEOS_TEST_PROCS` unset and less
than `THEOS_TEST_MIN_MEM_GIB` (default 4) of `MemAvailable`, the suite runs in
one process; `THEOS_TEST_PROCS=1` always does. A spec run outside `make test`
(an IDE, `go test` by hand) runs the whole suite in one process, as before.
What it costs is under [Memory on the cluster device](devices.md#memory-on-the-cluster-device).

`make validate-yaml` checks the YAML without a cluster or a network, with
`kubectl-validate` and `helm` from the device's tool directory: the samples, the e2e fixtures, the
`kustomize build` of `config/default`, `config/gitea` and `config/ui`,
`config/runner`, `hack/seed/job.yaml`, `hack/session/pod.yaml`, the chart
(`make helm-lint`, Gitea on and off) and the webshop's `orders-api` repository
rendered with `hack/render`. Built-in kinds are checked against the schemas of
the envtest Kubernetes release, downloaded once next to the tools (`k8s-openapi-*`); our
kinds against `config/crd/bases` and `test/crds/agent-sandbox`, create-time CEL
rules included, so it shows that valid YAML passes them; it holds no YAML that
breaks a rule. Rules on `oldSelf` need an update, so envtest covers them.
It also fails when a CRD in `dist/chart/templates/crd` differs from
`config/crd/bases` beyond the chart's `crd.enabled` and `crd.keep` guard
(`hack/chart-crds.sh`), or when the chart's manager role's rules differ from
`config/rbac/role.yaml` (`hack/chart-role.sh`): `make manifests` writes both
copies, and an API change commits them. The chart's `catalog.bundles` are
checked too (`hack/chart-bundles.sh`): each Bundle names entries the chart
installs in its namespace that share a Container type, with a Standard that
supports the Framework. In CI, *Lint* runs `make check-generated`
after it: a generated file that differs from what is committed fails the job (#1689).
Every CEL rule has an envtest spec that expects its message: a create-time rule's
spec creates an object that breaks it, a rule on `oldSelf` creates, patches and
expects the message. A new rule adds one.
`config/agent-sandbox`, `config/argocd` and `config/metrics-server` fetch remote
URLs and are skipped. What it rendered is in `bin/yaml/`.

How `make test` reuses the build and test caches every checkout on a device
shares, and the agents' Claude token `make claude-secret` stores, are in
[devices.md](devices.md#caches-every-checkout-shares).

`make run` runs the operator on your machine, but it can't reach Gitea's in-cluster
address; use `make deploy` for anything that scaffolds.

## Local Gitea

`make gitea-local` runs a Gitea of this checkout's own, with no Kubernetes or
Docker, so the Gitea tests run on any device, lite ones included:

- It downloads the Gitea binary `config/gitea` runs (`GITEA_VERSION`, linux
  amd64 or arm64) into `bin/` and checks its sha256.
- It starts it from `.gitea-local/` (gitignored): SQLite, `INSTALL_LOCK`,
  offline, listening on 127.0.0.1 only, with mail, SSH and LFS off, and Actions on
  with no runner, so a pushed workflow's run stays queued.
- The first start picks a free port in 13000-13999 near one derived from the
  checkout's path, so worktrees don't collide, and keeps it in
  `.gitea-local/app.ini`. There is no lease.
- It also creates admin `c4admin` with a random password and an access token
  (the seed's scopes) and writes them to `.gitea-local/credentials.env`
  (mode 0600; `THEOS_GITEA_URL`, `THEOS_GITEA_USERNAME`, `THEOS_GITEA_PASSWORD`,
  `THEOS_GITEA_TOKEN`). Nothing prints them; source the file to use them.
- Starting again while it runs is a no-op. `make gitea-local-stop` stops it
  and keeps its data; after a stop, `rm -rf .gitea-local` starts over with a
  new port and new credentials.

`make test-gitea` starts it, seeds the templates (`hack/gitea-local.sh
fixtures`: `hack/seed/seed.sh` with no projects, so no Secrets, plus the
`webshop` organization) and runs the `Integration` tests in
`internal/adapters/gitea`. A first run takes about three minutes, mostly
pushing the templates; later runs take about half a minute. The packages
test (`TestGiteaPackagesIntegration`) installs what it publishes with the
real tools, so it wants `npm` and `uv` on PATH, and skips, saying which one
is missing, when either isn't. With
`THEOS_GITEA_URL` set (and `THEOS_GITEA_USERNAME`, `THEOS_GITEA_TOKEN`) it tests that
Gitea instead, for example the Kind one behind `make gitea-forward`;
`make test-gitea-kind` does that forward and reads the token from the cluster.
Sourcing `credentials.env` into your shell exports `THEOS_GITEA_URL` too, so
`make test-gitea` then skips starting the local Gitea; unset it after a stop.

`make seed-local` starts it and seeds it as `make seed` seeds Kind's, with no
kubectl: the `templates` organization with every tag in
`templates/versions.txt`, and the `webshop` organization. The seed's file mode
(`CREDENTIALS_DIR` in `hack/seed/seed.sh`) writes webshop's token to
`.gitea-local/webshop.env` (mode 0600; `THEOS_GITEA_URL`, `THEOS_GITEA_USERNAME`,
`THEOS_GITEA_TOKEN`) instead of Secret `gitea-credentials`, and skips the runner
token. Use it to check template work on any device: a wording fix, a new tag in
`versions.txt`, or how the seed pushes and tags. A second run changes nothing,
and warns when a template no longer matches its tag. `hack/seed_test.sh`
covers the file mode against a stand-in Gitea.

The Tests workflow runs `make test-gitea` on pull requests to main and on
main, with node and uv set up in its job, so the tests that install the
packages they publish run there in full; run it yourself while you change
`internal/adapters/gitea` or the seed. Without `npm` or `uv` on PATH only the
install subtests skip, and say so; the publish still runs (#1702).

## Deployment Workflow

```bash
# 1. Regenerate manifests
make manifests generate

# 2. Build, load into Kind, deploy
make deploy            # builds theos/operator:dev-<checkout>, kind-loads it as :dev, applies config/default, restarts

# 3. Try it (kubectl names the cluster: kind-theos, or the one you leased)
make sample            # the webshop story
kubectl --context kind-theos get theos -n webshop

# 4. Debug
kubectl --context kind-theos logs -n theos-system deployment/theos-controller-manager -c manager -f
make gitea-forward     # then open http://localhost:3000
```

The Kind test environment installs with kustomize. Devices install the Helm
chart in `dist/chart/` instead ([its README](../../dist/chart/README.md)).
After a change under `config/` or `api/`, `make chart` regenerates it from the kustomize
output (`make manifests` alone keeps its CRDs and manager role in step) and `make
helm-lint` checks it, offline, in `make validate-yaml` and so in `make check`. `make chart` keeps `templates/manager/manager.yaml`, which the plugin would
regenerate without the `/tmp` mount, `POD_NAMESPACE` and the shared Secrets; it
writes what the plugin would change to `bin/chart/manager.plugin.diff`, outside the
`bin/yaml/` that `make validate-yaml` parses, to port by hand,
and `hack/chart-manager.sh` fails `make validate-yaml` when the render lacks them; the *Test Chart* workflow, dispatched by hand, installs it into a
fresh Kind cluster on GitHub. Don't `helm install` it into the shared
`theos` cluster: `make deploy` owns the CRDs there.

A pushed `vX.Y.Z` tag runs the *Release* workflow, which builds the operator,
UI, toolchain, agent and runner images, pushes them and the chart to GHCR and
creates the GitHub release ([plan](../plans/done/release-workflow.md)), its
notes drafted by `hack/release-notes.sh` from the pull requests merged since
the previous release. The previous release is the highest published release
below the tag (`hack/previous-release.sh`; drafts and prereleases don't
count): a tag whose *Release* failed has no release, so the next notes carry
what it did. The tag must be the `version` of `dist/chart/Chart.yaml`, or
*Release* stops; the commit that bumps it bumps `VERSION` in
[getting-started](../getting-started.md) too, so the guide at a tag installs
that release (`make check` holds the two together).

A release builds on the self-hosted runners' Docker, whose build cache a rerun
reuses, so a step that leaves a broken layer fails every rerun too. Every
image it builds starts from bases pinned by version and digest
(`hack/check-pinned-images.sh`, in `make check`, #919), except the agent and
runner images' `${BASE}`, our own toolchain image built in the same run; to
take a newer base, bump the tag and its digest together (`docker buildx
imagetools inspect IMAGE:TAG` prints the digest). The UI's `npm ci` step fails unless every native binding the
lockfile lists for the platform is installed (`ui/scripts/check-native.mjs`):
npm skips an optional package it fails to fetch without failing, which broke
v0.19.0 (#912). Every download in those Dockerfiles retries, since the
runners share a busy device and one TLS handshake timeout failed v0.28.1
(#1494): curl takes `--retry 5 --retry-all-errors` and writes to a file, as
a retried transfer piped to `tar` would arrive twice; `go install`, `go get`,
`go mod download`, `setup-envtest use` and `apk add` run under a `retry`
shell function that the same `RUN` defines; apt-get sets `Acquire::Retries`
and npm `--fetch-retries` (`hack/check-download-retries.sh`, in `make
check`).

Lint, Tests, UI and Vulnerabilities run on pull requests to main and after a
merge to main, each only when its paths change, or on demand; the jobs a pull
request waits for are listed in `.github/required-checks`. No
workflow starts a Kind cluster on its own: *Test Chart* and *E2E Tests* run
only when a human dispatches them, and *Verify Release* when a human does or
*Release* has published a minor. A pushed tag runs *Release*, which builds and
publishes and tests nothing. `make check` is the gate before a merge; it runs
`make ui-test` (the UI workflow's `npm run check`: typecheck, eslint, Prettier
and its tests) too, so a UI change can't merge with what would turn *UI* red
(#1006). *UI* then runs `npm run test:e2e` as well, the built UI in Chromium
against a fake API, which `make check` leaves out as a device may have no
Chromium (theos#31): run `cd ui && npm run test:e2e` for a change to the UI's
pages, its fixture or its server.

## Work on the factory: GitHub issues by hand

The relay is retired, and its scripts are gone: the factory works
its own issues in Gitea, through the Crew `theos`. How that runs is in
[docs/plans/self-hosting.md](../plans/self-hosting.md).

A human who works an issue by hand still finds it on GitHub
(`JairJosafath/theos`), and `hack/lib/github.sh` does every GitHub call,
with the token from the git credential helper. Sessions hand off through issue
comments (the `handoff` skill).

```bash
hack/lib/github.sh pick            # the issue to work next
hack/lib/github.sh show 14         # an issue, its newest handoff comment and what came after
hack/lib/github.sh take 14         # claim it for this checkout; prints the agent's name and branch
hack/lib/github.sh claims          # who holds which issue, with heartbeat age and branch
THEOS_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire # a human only: lease a pool cluster; prints its name (waits up to 30m; exit 4 names the holders, exit 5 without the opt-in)
hack/lib/github.sh cluster acquire --fresh # the same, on a clean cluster: deletes and recreates the one it leased
hack/lib/github.sh cluster acquire --fresh --bare # the same, with nothing installed: for a release install (getting-started)
hack/lib/github.sh cluster name    # the cluster make uses in this checkout: its lease's, else theos
hack/lib/github.sh cluster release # drop it once verification is done
hack/lib/github.sh cluster holder  # who holds which cluster
hack/lib/github.sh cluster pool    # each pool cluster: leased, free or not created yet
hack/lib/github.sh labels          # create the status/priority/kind/source labels (safe to repeat)
hack/lib/github.sh yolo on|off|status  # yolo mode: a pull request merges once made (no make check, review or merge turn) until only the Maintainer report is open
```

`pick` chooses by `priority/N`, 1 first: an issue with no priority label counts
as 5, and of several labels the lowest counts. 1 is something broken now, 3 what
makes development itself faster
([docs/plans/done/velocity.md](../plans/done/velocity.md)), 10 last. Within a
number, the oldest `status/in-progress` issue nobody holds goes first, else the
oldest `status/ready` one, skipping issues with a live claim or an open
`Depends on #M`.

`hack/lib/github.sh create --next TITLE FILE kind/<bug|chore|docs>` outside a
worker's session files the issue `status/ready` and reserves nothing. Follow-ups
that need a decision are filed `status/proposed`.

A claim whose heartbeat stays live but whose work shows no progress for
`THEOS_CLAIM_STALE` (2h; `0` turns it off) is stale: `hack/lib/github.sh stale`
lists such claims, and `stale --apply` sets the issue to
`status/needs-maintainer` with one comment naming the last progress, the holder
and the branch. The claim is left alone, since the live heartbeat says it may
still be worked.

Yolo mode (#1534) is the owner's switch: while it is on, a pull request merges
once it is made, with no `make check`, no review pass, no merge turn and no
bug-fix-test gate; only the closing-keyword refusals stay, since they guard the
issues' state, not the code. `hack/lib/github.sh yolo on` puts the `mode/yolo`
label on the pinned Maintainer report issue, so every device sees the same
answer, and `yolo off` takes it off; `land` and `merge` ask `yolo status` first
and, while it is on, merge at once from any checkout (a branch behind main gets
main merged in by GitHub first). `yolo status` turns the mode off itself when
the Maintainer report is the only open issue.

The simple path (#1524) needs no switch: a pull request that changes only files
nothing but the docs read (`docs/` but `docs/getting-started.md`,
`.claude/skills/`, and `*.md` anywhere but the root README.md and under
`templates/`, `dist/` or `ui/`), under 200 lines in all, merges through `land`
without a heavy-job slot or the merge turn, with `make test-hack` only when one
of them is `docs/dev/testenv.md` or under `.claude/skills/`; `land` says it took
the simple path, and a move of main by such files under the turn is merged in
unchecked the same way.

### Sharing the Kind clusters between checkouts

A human who runs Kind leases a cluster of the device's pool around the steps
that touch it (`THEOS_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire`, then
`cluster release`); agents never do. The pool and its leases are in
[devices.md](devices.md#sharing-the-kind-clusters-between-checkouts).

### Checking a release

The maintainer cuts a release from a sweep with one command:
`hack/release.sh minor`, `patch` or `X.Y.Z`. From origin/main, in a worktree
of its own, it names the version in `dist/chart/Chart.yaml` and in
`docs/getting-started.md` (its `VERSION` and the release its prose names),
merges that bump as a pull request of its own with a merge commit, and pushes
the tag `vX.Y.Z` on the merge commit, so the guide at a tag installs that
tag's release (#370). It refuses a version that isn't above the chart's, or
whose tag already exists. The tag starts *Release*, which first runs
`hack/release.sh --check vX.Y.Z` and builds nothing when the chart or the
guide names another version; `hack/release_test.sh` holds both to that, and
`test/chart/guide_version_test.go` keeps main's guide on the chart's version.

A release that deprecates a version gives its drop issue the date: once the
release is published, the maintainer writes the earliest drop date (the
release's date plus 90 days) into that issue's body. The CRDs' warnings,
the guide and the chart's README name the rule ("a release 90 or more days
after vX.Y.Z"), never the date (#1983). No version is deprecated today:
every kind is served at `v1beta1` alone, `v1alpha1` dropped before theos's
first release (#1963).

No agent can pull a release (the GHCR packages are private and no device's
gh token has `read:packages`), so a workflow does. Once *Release* has
published `vX.Y.Z`, *Verify Release* (`.github/workflows/verify-release.yml`)
checks it twice, each on a Kind cluster of its own, building nothing.
*Release* dispatches it on main, with `from` empty, for every minor it
publishes (`vX.Y.0`, #1105); `hack/check-workflows.sh` keeps that step and its
condition, and the last step of each workflow, under `if: always()`, that
removes the images it built or pulled from the device's Docker ("Disk space
on WSL"). A patch isn't checked: it ships a fix within the hour on the
upgrade path its minor checked. A human checks any other release by hand:
Actions → Verify Release → Run workflow, or `gh workflow run
verify-release.yml -f tag=vX.Y.Z`. Agents never dispatch it. A failed run on
main files (or comments on) `Release vX.Y.Z fails its check`, ready and
`priority/1`, as for a red main.

The install check installs the published chart and images on a fresh cluster,
waits for the seed's repositories, the runners, the manager and the UI, and
scaffolds the release's `orders-api` sample. Then it follows
[getting-started](../getting-started.md) for the first app: the `ghcr` pull
Secret on `webshop`'s `default` ServiceAccount (it pulls `agent-go` through
it) and `config/samples/first-app/`, set to the release's agent tag and
template refs, until `orders-cli` is scaffolded and its Crew is `Idle`; no
Claude Agent runs, as CI has no Claude token. A tag without `first-app` skips that
part and says so. Then it follows the guide's *Without a token* command for
command: a Brief on the chart's Agent `stand-in` plans, its Crew's stand-in
Tasks merge the issue and "Prepare v0.1.0", the Crew tags `v0.1.0` and the
release run puts the binaries and `SHA256SUMS` on the Gitea release, in up to
75 minutes (#1083). `test/chart/verify_release_guide_test.go` holds that step
to the guide's commands. A tag before v0.24.0, whose stand-in releases
nothing, stops at the Crew's first Task; from v0.24.0 on a Crew without
`tagReleases` fails the step (#1240). Publishing to GitHub isn't checked: it
needs a repository and token of the owner's (#1216).

The upgrade check takes the path a device takes: `helm upgrade` from the
release it already runs. It installs the previous release (the highest
published release below this one; the summary names any tag it skipped for
having none) the same way, with its own `orders-api` sample and its
`first-app` (else the guide's) until `orders-cli`'s Crew is `Idle`, notes what
is there,
and `helm upgrade`s to the new release with the same values. Then it checks
that the CRDs are the new chart's (`kubectl diff` against the CRDs it
renders), that every earlier `c4` and `c4catalog` object is still
there with its phase and its `Ready` condition, that the new manager logs no
`Reconciler error` in its first two minutes (optimistic-lock conflicts
aside), that Secrets `gitea-admin` and `gitea-runner` are unchanged (one
exception: a `gitea-runner` that held an instance token before, with no
`theos.factory.io/token-owner` annotation, is replaced once by the templates
organization's token the seed stores, and the check wants that one to be
there after), and that
every Gitea repository is still there with its earlier head commit (the seed
may add template tags). The chart up to v0.12.0 recreated its GitProviders,
catalog and Agents as hooks on every upgrade; later ones have the seed apply
them in place, keeping their uid, but objects are still matched by kind,
namespace and name, not uid. On the first release there is no previous one, and the
check is skipped and says so. A release whose chart has no catalog (v0.3.0 and
older) has no `first-app` of its own that works on it, so either check takes
the newest samples that made their own catalog and Agents, from history.

Their results are the run's job summaries and the artifacts
`release-vX.Y.Z-install` and `release-vX.Y.Z-upgrade` (Actions → Verify
Release). When a dispatch on main fails it files `Release vX.Y.Z fails its check` (`status/ready`, `priority/1`), or
comments on the open one; the issue says which check
failed and if the runner ran short of memory or disk. A dispatch on another
branch only reports in its run. Any published release can be checked; add
`--ref <branch>` to try a branch's workflow, and `-f from=vA.B.C` to upgrade
from another release than the one below (the same tag upgrades it to itself,
which tries the check's own steps). The install check takes the `first-app` samples from the ref it runs on, so on main it checks
the guide's current samples against that release.

An upgrade from v0.2.0 is one Verify Release no longer takes: from v0.4.0
on it upgrades from the release just below. What that upgrade alone runs is
the chart's pre-upgrade hook `templates/gitea/keep-secrets.yaml`, which marks
the Secrets `gitea-admin` and `gitea-runner` that a chart up to 0.2.0 rendered
`helm.sh/resource-policy: keep`, so the upgrade keeps Gitea's admin password
and runner token. It renders only on an upgrade whose `lookup` finds those
Secrets owned by the release, so no `helm template` shows it. `make test`
covers it in `test/chart`: `helm template --is-upgrade --dry-run=server`
against envtest's API server, holding such Secrets, must render the hook for
the release's own Secrets alone and nothing on an install; the test creates
what it renders and, as the hook's ServiceAccount, does what its Job's
`kubectl annotate` does; and the seed image the Job runs must still install
kubectl. What it leaves to a node, the Job's pod running, was checked by hand
on Kind in #329; `gh workflow run verify-release.yml -f tag=vX.Y.Z -f
from=v0.2.0` runs the whole upgrade again.

## Devices

The device's limits (memory, workers, `HEAVY_JOBS`, disk, CPU) and the
self-hosted GitHub runners are in [devices.md](devices.md).
