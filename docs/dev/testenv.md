# Tests, the Kind cluster and deploying

How to test and run the factory. Go tests are the gate: `make check` for
every change. A Kind cluster exists only when a human asks to run tests in it;
agents never create, lease or deploy to one, and never run `make e2e`. The
human runs it against the dedicated Kind clusters of the device's pool
(`c4factory`, `c4factory-2`, ...; `make cluster`), never a real cluster.
Every make target works on `KIND_CLUSTER`, by default the pool cluster this
checkout leased, else `c4factory`; kubectl calls name it with `--context
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

The scripts in `hack/` (the relay, the heavy-job slots, the check cache, the
cluster pool, the seed) have shell tests of their own, `hack/**/*_test.sh`.
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
make test-gitea          # gitprovider against a local Gitea (make gitea-local), or C4_GITEA_URL if set
make test-gitea-kind     # gitprovider against the seeded Gitea of the Kind cluster
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
path joins that list. `land`'s `make check` selects
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
name. With `C4_SLOWEST_SPECS=FILE` it writes its 10 slowest specs to FILE; the
*Tests* workflow prints them, so a slow run's log says which specs to speed up.

The controller suite runs its specs one after another, so `make test` runs it
as `C4_TEST_PROCS` go tests at once (default 2), beside one go test of the
other packages (`hack/go-test.sh`, #1283). Each part gets
`C4_TEST_SHARD=i/n` and runs its share of the suite's top-level containers
against its own control plane and manager; a container is never split, since
Ginkgo already runs them in random order but the specs inside one may share
state. The split is the same in every part, and a part that ran another
number of specs than its containers hold fails. Go's test cache answers each
part on its own, and their profiles (`COVER=1`) and slowest specs
(`C4_SLOWEST_SPECS`) are merged into one. With `C4_TEST_PROCS` unset and less
than `C4_TEST_MIN_MEM_GIB` (default 4) of `MemAvailable`, the suite runs in
one process; `C4_TEST_PROCS=1` always does. A spec run outside `make test`
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
  (mode 0600; `C4_GITEA_URL`, `C4_GITEA_USERNAME`, `C4_GITEA_PASSWORD`,
  `C4_GITEA_TOKEN`). Nothing prints them; source the file to use them.
- Starting again while it runs is a no-op. `make gitea-local-stop` stops it
  and keeps its data; after a stop, `rm -rf .gitea-local` starts over with a
  new port and new credentials.

`make test-gitea` starts it, seeds the templates (`hack/gitea-local.sh
fixtures`: `hack/seed/seed.sh` with no projects, so no Secrets, plus the
`webshop` organization) and runs the `Integration` tests in
`internal/gitprovider`. A first run takes about three minutes, mostly
pushing the templates; later runs take about half a minute. The packages
test (`TestGiteaPackagesIntegration`) installs what it publishes with the
real tools, so it wants `npm` and `uv` on PATH, and skips, saying which one
is missing, when either isn't. With
`C4_GITEA_URL` set (and `C4_GITEA_USERNAME`, `C4_GITEA_TOKEN`) it tests that
Gitea instead, for example the Kind one behind `make gitea-forward`;
`make test-gitea-kind` does that forward and reads the token from the cluster.
Sourcing `credentials.env` into your shell exports `C4_GITEA_URL` too, so
`make test-gitea` then skips starting the local Gitea; unset it after a stop.

`make seed-local` starts it and seeds it as `make seed` seeds Kind's, with no
kubectl: the `templates` organization with every tag in
`templates/versions.txt`, and the `webshop` organization. The seed's file mode
(`CREDENTIALS_DIR` in `hack/seed/seed.sh`) writes webshop's token to
`.gitea-local/webshop.env` (mode 0600; `C4_GITEA_URL`, `C4_GITEA_USERNAME`,
`C4_GITEA_TOKEN`) instead of Secret `gitea-credentials`, and skips the runner
token. Use it to check template work on any device: a wording fix, a new tag in
`versions.txt`, or how the seed pushes and tags. A second run changes nothing,
and warns when a template no longer matches its tag. `hack/seed_test.sh`
covers the file mode against a stand-in Gitea.

The Tests workflow runs `make test-gitea` on pull requests to main and on
main, with node and uv set up in its job, so the tests that install the
packages they publish run there in full; run it yourself while you change
`internal/gitprovider` or the seed. Without `npm` or `uv` on PATH only the
install subtests skip, and say so; the publish still runs (#1702).

## Deployment Workflow

```bash
# 1. Regenerate manifests
make manifests generate

# 2. Build, load into Kind, deploy
make deploy            # builds c4factory/operator:dev-<checkout>, kind-loads it as :dev, applies config/default, restarts

# 3. Try it (kubectl names the cluster: kind-c4factory, or the one you leased)
make sample            # the webshop story
kubectl --context kind-c4factory get c4 -n webshop

# 4. Debug
kubectl --context kind-c4factory logs -n c4-factory-system deployment/c4-factory-controller-manager -c manager -f
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
`c4factory` cluster: `make deploy` owns the CRDs there.

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
(#1006).

## Work on the factory: GitHub issues and the relay

Work on the factory itself is tracked as issues on `JairJosafath/c4factory`; the
apps it builds keep their issues in Gitea. Sessions hand off through issue
comments (the `handoff` skill), and `hack/lib/github.sh` does every GitHub call,
with the token from the git credential helper.

```bash
hack/lib/github.sh pick            # the issue to work next
hack/lib/github.sh show 14         # an issue, its newest handoff comment and what came after
hack/lib/github.sh take 14         # claim it for this checkout; prints the agent's name and branch
hack/lib/github.sh claims          # who holds which issue, with heartbeat age and branch
C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire # a human only: lease a pool cluster; prints its name (waits up to 30m; exit 4 names the holders, exit 5 without the opt-in)
hack/lib/github.sh cluster acquire --fresh # the same, on a clean cluster: deletes and recreates the one it leased
hack/lib/github.sh cluster acquire --fresh --bare # the same, with nothing installed: for a release install (getting-started)
hack/lib/github.sh cluster name    # the cluster make uses in this checkout: its lease's, else c4factory
hack/lib/github.sh cluster release # drop it once verification is done
hack/lib/github.sh cluster holder  # who holds which cluster
hack/lib/github.sh cluster pool    # each pool cluster: leased, free or not created yet
hack/lib/github.sh labels          # create the status/priority/kind/source labels (safe to repeat)
hack/lib/github.sh yolo on|off|status  # yolo mode: a pull request merges once made (no make check, review or merge turn) until only the Maintainer report is open
RELAY_DRY_RUN=1 hack/relay.sh      # the issue and prompt the next headless session would get
hack/relay.sh                      # headless sessions one after another, overnight (billed)
hack/relay.sh --workers 2          # two relay workers side by side, each in its own worktree (no-cluster devices)
hack/relay-watch.sh --workers 2    # keep the relay ready: start it within a minute of an issue becoming ready
hack/relay-watch.sh --install --workers 2  # the same, kept up by systemd (or cron) across crashes and reboots
touch .relay/stop                  # stop the watch, and every worker after its current session
rm -f .relay/stop; hack/relay-watch.sh  # restore it on a systemd install: starts the unit, with its own worker count
rm -f .relay/stop                  # restore it on a cron install: cron's next tick (within 5 minutes) starts the watch
```

The restore depends on the install, and the two are told apart by what
`--install` wrote: `systemctl --user is-enabled c4factory-relay-watch` prints
`enabled` on a systemd install, and `crontab -l | grep -F 'c4factory relay
watch'` prints the two crontab lines on a cron install. On systemd,
`hack/relay-watch.sh` starts the unit, which runs under the login the install
recorded. On a cron install there is no unit to start: `hack/relay-watch.sh`
starts a detached watch under the caller's own login and the default worker
count, and that watch holds `watch.lock`, so cron's own watch cannot start
until it ends. Run from a maintainer or helper session, that is the *Not
logged in* trap below (#1981). So on a cron install, only remove the stop
file; cron's next tick starts the watch under its own login, with its own
worker count.

Every session the watch starts runs under the login `--install` recorded:
`CLAUDE_CONFIG_DIR` as it was in the installer's shell (`~/.claude` when
unset), written into the unit and the crontab lines, so `./dev run` cannot
point the sessions at `.dev/claude` (#1435). A `hack/relay.sh` started from
anywhere else, a maintainer session or another `claude -p` session, runs
outside that login and its sessions answer `Not logged in`; so does
`hack/relay-watch.sh` itself on a cron install (above). A quiet window for
one issue goes through the watch's own workers and the stop file (the
`maintainer` skill, Health).

The relay picks the issue its worker's last session reserved (`create --next`,
below), else by `priority/N`, 1 first: an issue with no priority label counts
as 5, and of several labels the lowest counts. 1 is something broken now (a
red main, a dead relay, a failed release check), 3 what makes development
itself faster ([docs/plans/velocity.md](../plans/velocity.md)), 10 last.
Within a number, the oldest `status/in-progress` issue nobody holds goes
first, else the oldest `status/ready` one, skipping issues with a live claim
or an open `Depends on #M`, and claims it (see
[docs/plans/parallel-agents.md](../plans/done/parallel-agents.md)). Worker
k runs its sessions in the git worktree `.claude/worktrees/relay-k`, switched
to the issue's branch; uncommitted changes a dead session left there are
stashed, not dropped. Before it claims anything, a worker checks that a
session there could commit (#1289): `git -C <worktree> var
GIT_COMMITTER_IDENT`, in the environment the session gets, which sees the
global, `$XDG_CONFIG_HOME/git/config` and repository configs alike. When git
has no committer identity there, the worker logs git's reason, starts no
session and stops, and puts one line under the Maintainer report's *Needs
you* naming the host and the worktree (`report-note ident-<host>-relay-k`),
so the owner sets `user.name` and `user.email`; the line goes away at the
first check that passes again. Triage and maintainer sessions commit nothing
and skip the check. `land`'s own merge of main says what stopped it when it
fails with no conflict, such as `fatal: empty ident name`. Claims keep workers and hand-started sessions off each
other's issues, so the relay starts right away. A session that ends still
holding its claim gets it released; a worker stops when a session leaves no
handoff comment and doesn't close the issue or escalate it
(`status/needs-maintainer`; only the maintainer sets `status/needs-human`). With
nothing ready a worker stops.
Yolo mode (#1534) is the owner's switch: while it is on, a pull request merges
once it is made, with no `make check`, no review pass, no merge turn and no
bug-fix-test gate; only the closing-keyword refusals stay, since they guard
the issues' state, not the code. `hack/lib/github.sh yolo on` puts the
`mode/yolo` label on the pinned Maintainer report issue, so every device sees
the same answer, and `yolo off` takes it off; `land` and `merge` ask `yolo
status` first and, while it is on, merge at once from any checkout (a branch
behind main gets main merged in by GitHub first). Before each session a relay
worker runs `yolo merge`, which merges every open non-draft pull request to
main, saying which one it could not and going on with the rest. `yolo status`
turns the mode off itself when the Maintainer report is the only open issue.
The simple path (#1524) needs no switch: a pull request that changes only
files nothing but the docs read (`docs/` but `docs/getting-started.md`,
`.claude/skills/`, and `*.md` anywhere but the root README.md and under
`templates/`, `dist/` or `ui/`), under 200 lines in all, merges without a
heavy-job slot or the merge turn, with `make test-hack` only when one of them
is `docs/dev/testenv.md` or under `.claude/skills/`; `land` says it took the
simple path, and a move of main by such files under the turn is merged in
unchecked the same way.
The relay's sessions run on `RELAY_MODEL` (default `opus`, #1727), and their
prompts tell them to name a subagent's model by the task's weight, sonnet for a
bounded check, opus for a review or a design, as the `handoff` skill does.
A follow-up a session decided goes to the front of its own worker's queue:
`hack/lib/github.sh create --next TITLE FILE kind/<bug|chore|docs>` files it
`status/ready` and reserves it, with a claim comment that says "reserved this
issue" and a line in the worktree's `.relay/next`. That worker's next `pick`
returns it before every other issue, `priority/1` ones too, and its `take` turns
the reservation into the claim; every other `pick` skips it and every other
`take` refuses it, until the reservation is `C4_LEASE_TTL` (2h) old, after
which anyone picks it. The log shows such a session as `(next)`. The watch's
`pick` counts every worker's `.relay/next`, so a reserved issue starts the
relay after its worker stopped. Outside a worker's session (a hand-started
one, the triage's, the maintainer's: `C4_RELAY_WORKER`, which the relay sets,
is no number) `--next` files the issue ready and reserves nothing.
Follow-ups that need a decision are still filed `status/proposed` for the
triage.
Each session, the triage's and the maintainer's too, gets
`TMPDIR=~/.cache/c4factory/tmp/<its worktree>` (#1269, #1307;
`RELAY_SCRATCH` moves the parent): on disk, not the device's `/tmp`, a small
tmpfs every session shares, which one session's scratch, Go's build dirs and
envtest's once filled; and outside any git checkout, so a test that runs git in
a `mktemp -d` directory finds no repository above it, as it would in the
worktree. The relay empties it before the session starts and removes it when
the session ends.
Its `OLDPWD` is its worktree too, not the main checkout the relay runs from,
so an agent's `cd -` stays in the worktree (#1371). A session that leaves the
main checkout with uncommitted changes, which stop `hack/pull.sh` for every
session on the device, is named with the paths in the worker's log and at the
end of its account; the relay leaves the files as they are.
Before the session, the relay also replaces a worktree's `.dev/` of its own,
the caches `make check` left there before `hack/dev/env.sh` pointed worktrees
at the main checkout's (#1132), with the link to the main checkout's `.dev/`,
as `./dev setup` makes in a worktree, and logs what that freed (#1246).
Worker k logs to `.relay/relay-k.log`, with one account per session next to
it. A handoff that couldn't reach GitHub waits in the worktree's
`.relay/pending-handoff-<issue>.md`, and the worker's next round posts it first.
A session that ends on a usage limit or a failed login pauses the relay until
the limit resets (`.relay/paused-until`): every worker waits, and the watch
starts nothing. So does one that exits non-zero before any API call, whatever
the CLI said (`a limit: no API call`, `RELAY_LIMIT_WAIT`): that is the device
failing, not the issue (#1421). Its claim's comment then names the limit and keeps its host,
as every released or expired claim does: `released this issue: session ended
without a handoff (a limit: OAuth session expired) · host ...`. A session whose transcript stops changing for `RELAY_IDLE`
(45m) is ended and counts as failed; a device that slept doesn't count (#1500):
when the clock between two looks at the transcript (`RELAY_IDLE_POLL`, 60s)
jumps more than a minute past the poll, the idle time starts over at the wake. A claim whose heartbeat stays live but
whose work shows no progress for `C4_CLAIM_STALE` (2h; `0` turns it off) is
stale (#1513, the owner asked for it on 2026-10-21): progress is the newest of
a commit on the claim's branch on origin or a `<branch>-*` takeover branch, a
handoff comment and the claim's start; heartbeats don't count. Every
`WATCH_PARENTS`, the relay watch (`hack/lib/github.sh stale --apply`; without
`--apply` it only lists) sets such an issue to `status/needs-maintainer` with
one comment marked `<!-- c4-claim-stale -->` naming the last progress, the
holder and its host, the heartbeat's age and the branch. The claim is left
alone: the live heartbeat says it may still be worked, so the maintainer
decides. A stretch is flagged once; after new progress, another
`C4_CLAIM_STALE` of none flags it again. Watches on two devices that flag at
once keep the older comment. A session that ends without a handoff and
without a new commit on the issue's branch, or on `<branch>-2` or
`<branch>-<name>` (local or pushed), is a failed one: a strike, a comment on
the issue naming the session, its host and the last lines of its stderr and
account. The count is read from the issue, so every device's strikes add up;
one older than `RELAY_STRIKE_TTL` (24h) doesn't count. At `RELAY_MAX_FAILS`
(3) the relay posts one comment linking them and sets the issue to
`status/needs-maintainer`; when only that status swap fails, the next failed
session on any device retries it without a new strike or comment: the newest
of the issue's handoff, reset and escalation comments is the escalation, and
the issue wasn't labeled `status/needs-maintainer` after it. A
handoff, a commit or a finish on any device resets the count: the relay posts
a reset comment when any strike counts. One that leaves the issue in
`status/waiting` (a plan's parent) has finished, like a handoff; one that sets
it back to `status/ready` or `status/proposed` isn't counted either way.

### Sharing the Kind clusters between checkouts

A human who runs Kind leases a cluster of the device's pool around the steps
that touch it (`C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire`, then
`cluster release`); agents never do. The pool and its leases are in
[devices.md](devices.md#sharing-the-kind-clusters-between-checkouts).

The relay stops when no issue is ready, so an issue marked ready or handed back
later waits until someone starts it again. `hack/relay-watch.sh` stays up
instead: every minute it asks `pick` (which changes nothing) and, when there is
an issue and a worker is free, runs `hack/relay.sh --workers N`, which leaves
running workers alone. Asking first keeps the relay's logs free of a worker that
starts, finds nothing and stops every minute. It logs only changes, to `.relay/watch.log`, pulls main every 5
minutes and restarts itself when that changed its code. The watch and the
workers run origin/main's scripts, whatever branch the main checkout has
(#1126): from the worktree `.claude/worktrees/relay-code`, which each pull moves
to origin/main, while their state (`.relay/`, the workers' worktrees) stays in
the main checkout. One started from the checkout moves there at its first pull,
and one whose code there changed restarts on it; `RELAY_PULL=0` keeps them on
the code where they were started, to try relay changes from a branch. A worker
started again while its last session still runs (after a crash, a kill or a
restart by hand) ends that session first, SIGTERM then SIGKILL after
`RELAY_IDLE_KILL`, before it releases the claim or starts a session in the
worktree: `.relay/session-K.pid` names the session while it runs (#1341). Every 30 minutes
(`WATCH_TRIM_EVERY`) it runs `hack/prune-caches.sh` and logs what that
dropped: nine workers grow Go's build cache by gigabytes an hour, and WSL's and
Docker Desktop's disk images never shrink (#1343). It prunes:
- with `hack/trim-go-cache.sh`, Go's build cache (`GOCACHE`), golangci-lint's
  and Go's default cache `~/.cache/go-build`, which `go` runs outside `./dev`
  fill, to `C4_GOCACHE_MAX_GB` (4) each; `C4_TRIM_GO=0` turns it off;
- the GH runners' cache volume `c4factory-gh-runner-cache`: the same trim over
  its `go-build` and `golangci-lint`, in a throwaway container of the runner
  image, keeping what was used in the last two hours, so a running job keeps
  its cache; skipped without the volume or the image,
  `C4_PRUNE_RUNNER_CACHE=0` turns it off;
- Docker's build cache unused for `C4_PRUNE_DOCKER_UNTIL` (72h, so the latest
  builds stay warm), then what is over `C4_PRUNE_DOCKER_MAX_GB` (10, oldest
  first; 0 for no cap: a release's builds grew it past 20 GB within 72h,
  #1636), and the dangling images; `C4_PRUNE_DOCKER=0` turns it off.

Without `docker` it skips the last two.

Busy or idle, with issues ready or not (#1191), it starts a triage of the
`status/proposed` issues: for a new one, for one that changed since the last
triage or waits on an issue that closed since, and every `WATCH_RETRIAGE` (3h)
in any case, at most every `WATCH_TRIAGE` (10m) and never while one runs. Past
`WATCH_TRIAGE` it asks GitHub for the proposed issues at most every
`WATCH_RETRY` (10m), as each ask reads every open issue. The triage runs in its own worktree,
`.claude/worktrees/relay-triage`, on origin/main, under `.relay/triage.lock`,
as the maintainer does: it needs no free worker, doesn't count against
`RELAY_MAX_WORKERS` and, running no `make`, takes no `HEAVY_JOBS` slot; it
goes through `admit` like any session and logs to `.relay/relay-triage.log`.
To triage now, say after a session files follow-ups `status/proposed`, run
`./dev run hack/relay.sh --triage`: it starts one triage session unless one
runs, and records its time and issues in `.relay/triaged`, as every triage
session does (the watch only reads that file), so the watch starts no second
triage for the same issues, only for one filed after it. Every `WATCH_CI_EVERY` (15 minutes) it checks main's CI:
a workflow that runs on a push to main and failed its two newest finished runs
there (`hack/lib/github.sh red`) gets one issue, `main is red: <workflow>`,
ready and `priority/1`, so the next session fixes main before anything else; a single
failure files nothing. When GitHub started none of the newest failed run's
jobs (no steps, an annotation saying the job was not started: failed
payments, a spending limit), no code is at fault: the watch files one
`GitHub Actions starts no jobs` issue in `status/needs-maintainer`, carrying
the annotation, and no `main is red`. It keeps that issue's number in
`.relay/ci-stopped` until every workflow runs its jobs again, and files no
other while the outage lasts, even after the issue is closed as a duplicate.
A flaky spec rarely fails twice in a row, so in the same round the watch
records each Ginkgo spec that failed on main (the `[FAIL]` lines of each failed
run's log, `hack/lib/github.sh flakes`) in `.relay/flakes.tsv`, spec, run and
time and red streak, and files `Flaky: <spec>`, a ready `kind/bug` with
`priority/3`, when one failed in two runs within `WATCH_FLAKE_DAYS` days (7)
with a pass of the workflow between them; failures in a row (a cancelled run
between them counts as neither) are main being red, which `main is red`
tracks, and count once (#1398). While it is open, a third failure files nothing.
Once it is closed, the runs it listed are its fixed flake: before filing, the
watch asks GitHub for the newest closed `Flaky: <spec>` bug and counts only
the runs after it closed, so a watch that starts fresh (an empty
`.relay/flakes-runs`) and reads the window's failed runs again refiles no
fixed flake, and a spec that fails twice again after its fix is filed anew
(#1824).
`hack/lib/github.sh flakes --count` lists the specs that failed on main this
week, from GitHub, on any device.
A queued run never fails, so `red` can't see that every self-hosted runner is
offline (Docker Desktop stopped). So in the same round the watch asks
`hack/lib/github.sh queued`. When a run on main has had a job waiting more
than `WATCH_QUEUED` minutes (30) for a runner, it puts a note under the
Maintainer report's Health, and as a task under Needs you
(`hack/lib/github.sh report-note runners`). With no runner of the repository
online the note is `Runners offline since T: N runs queued`, and the task asks
for Docker Desktop. With some online it is `N runs queued since T with M
runners online`: the runners are busy, or none has the labels the jobs ask
for. Each line is marked `<!-- watch:runners -->`, the maintainer's sweep
keeps it, and later rounds update it or put it back. `.relay/runners-offline`
holds the note while it lasts. The first round in which no run waits that
long removes both lines. `make gh-runner-status` says why ([devices.md](devices.md#github-actions-on-self-hosted-runners)). A run
GitHub made no jobs for stays queued for good, and GitHub won't cancel it
(#1236): no runner can take it, so the watch logs it once, by id
(`.relay/runs-without-jobs`), and never notes it.
In the same round, when `/tmp` on the device is at least `WATCH_TMP_FULL`
percent full (90), the watch puts `/tmp on HOST is N% full; largest: ...`,
naming its three largest entries, under the Maintainer report's Health only,
with no Needs you line (`hack/lib/github.sh report-note tmp-<host> TEXT ""`).
`.relay/tmp-full` holds the note while it lasts; the first round under the
threshold removes it. Relay sessions keep their scratch in
`~/.cache/c4factory/tmp`, so what fills `/tmp` is someone else's.
With nothing ready, it also logs, once per change, which skipped issue waits on
which issue nobody works (`hack/lib/github.sh blocked --unworked`; `blocked`
alone lists every skipped issue's blockers, with status and claim), and when
issues wait on each other in a cycle, `cycles --report` comments each of them
once and moves the newest to `status/needs-maintainer`.
Every `WATCH_PARENTS` seconds (600; 0 turns it off), busy or idle, it tends the
plans' parents (`hack/lib/github.sh parents --apply`): an issue in progress or
waiting, with a `Depends on #S` line per step and a merged pull request that
says `Refs #N` and adds its plan in `docs/plans/`, closes with a comment naming
the steps once every step closed; while a step is open, one no live claim holds
goes to `status/waiting`, with a comment naming the steps left. `pick` takes
neither. `close` and the *Issue closed* workflow do the same as each step
closes; the watch catches what they missed while the relay was down.
`parents` alone lists each parent and the steps it still waits on.
On every pass, busy or idle, it may start the maintainer
(`hack/relay.sh --maintain`), a headless opus session that acts as the
project's maintainer on the owner's behalf ([GOVERNANCE.md](../../GOVERNANCE.md),
the `maintainer` skill): it decides escalations, corrects agents, answers
reporters, cuts releases and rewrites the pinned *Maintainer report* issue
(label `maintainer/report`, set with `hack/lib/github.sh report FILE`), and
commits nothing. The watch starts it when none runs, the last sweep is
`WATCH_MAINTAIN` old (7200 s, 2h; 0 turns it off) and
`hack/lib/github.sh activity` (the newest change to an issue or pull request,
the report left out, and main's commit) differs from the one that sweep
recorded, so a quiet night costs nothing. The last sweep is the project's,
not the device's (#1322): the newer of this device's `.relay/maintained` and
the `<!-- c4-swept -->` record on the Maintainer report issue
(`hack/lib/github.sh swept`), which every full sweep writes in place; the
watch reads the record only when its own says a sweep is due, and the
sweep's prompt names the newer of the two.
Agents escalate to the maintainer, never to the owner: a session, the triage,
three failed sessions in a row and `cycles --report` set
`status/needs-maintainer` (which releases the claim and lease, and ends the
session as closing would); only the maintainer sets `status/needs-human`, for
what [GOVERNANCE.md](../../GOVERNANCE.md) reserves to the owner. When an
issue got `status/needs-maintainer` after the last full sweep started
(`hack/lib/github.sh escalated` lists them, with when each got it), the watch
starts the maintainer within a pass, however recent the last sweep, at most
once per `WATCH_RETRY`; one the sweep left there waits for the next regular
sweep, or for an agent to escalate it again. It runs in
its own worktree, `.claude/worktrees/relay-maintainer`, on origin/main, under
`.relay/maintain.lock`, and doesn't count against `RELAY_MAX_WORKERS`, but
goes through `admit` like any session, except that it goes first: while it
waits, `.relay/admit-first` holds its PID and no worker takes the start slot
(#262). It runs once per project, not once per device (#526): before its
session it takes the maintainer lease, a `<!-- c4-maintainer -->` comment on
the Maintainer report issue that it refreshes every `GH_BEAT_EVERY` seconds
and deletes when the session ends. While another device holds a live one it
starts nothing and logs the holder; one that missed `GH_MAINT_BEATS`
heartbeats (3, 45 minutes; not the claims' `C4_LEASE_TTL`, #1322), or this
host's own (a crashed maintainer's), is taken over. It logs to
`.relay/relay-maintainer.log`.
`hack/relay-watch_test.sh` tests its rounds with `pick` and the relay faked,
the triage's reasons with `proposed --since` against a stubbed GitHub, and its
CI rounds with `gh` faked.

Install the watch once on each device that runs the relay overnight:
[devices.md](devices.md#the-relay-watch-as-a-service).

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

The relay can't pull a release (the GHCR packages are private and no device's
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
`priority/1`, as the watch does for a red main.

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
`c4.factory.io/token-owner` annotation, is replaced once by the templates
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
comments on the open one, so the relay picks it up; the issue says which check
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
