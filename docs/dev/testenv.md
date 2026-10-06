# Tests, the Kind cluster and deploying

How to test and run the factory. The e2e tests run only against the dedicated
Kind clusters of the device's pool (`c4factory`, `c4factory-2`, ...; `make
cluster`), never a real cluster. Run `make e2e` only when the user explicitly
asks for it; `make check` is the gate for every change. Every make target
works on `KIND_CLUSTER`, by default the pool cluster this checkout leased,
else `c4factory`; kubectl calls name it with `--context kind-<name>`.

## Test tiers

Test a rule at the lowest tier that can see it ([design](../design.md#test-tiers)):

| Tier | Runs | Verifies |
| --- | --- | --- |
| 1. Table tests | `make test`, no API server | `internal/model` decisions, `internal/scaffold` rendering of every template, the agent pod spec and the fence's NetworkPolicy (`sandbox_fence_unit_test.go`) |
| 2. Offline YAML | `make validate-yaml`, in `make check` | samples, e2e fixtures, kustomize builds, the chart, rendered manifests, against the CRDs: valid YAML passes their create-time CEL rules |
| 3. Local Gitea | `make test-gitea`, `make seed-local` | the Gitea API's real behaviour, the seed, template tags |
| 4. envtest | `make test` | reconcilers, status, ownerReferences, the admission policy, every CEL rule's failing case (create-time and on `oldSelf`), the generated RBAC: the reconcilers run as the operator's ServiceAccount bound to `config/rbac/role.yaml` |
| 5. Kind | `make e2e`, factory-ops | Agent Sandbox pods running, the fence enforced by a CNI, node and kubelet behaviour, Gitea Actions runners |

Tiers 1 to 4 run on any device, lite ones included, with no lease. Only
tier 5 needs a Kind cluster, and only issues that need it carry
`needs/cluster`; Gitea work, YAML, templates and controller logic don't. Test
output is the evidence for logic; Kind evidence (`kubectl get` output,
screenshots of the running factory) is needed only for tier 5 behaviour.

The scripts in `hack/` (the relay, the heavy-job slots, the check cache, the
cluster pool, the seed) have shell tests of their own, `hack/**/*_test.sh`.
Each fakes what it calls (docker, kind, kubectl, gh, claude, curl), so none
needs a cluster or the network; `make test-hack` runs them all at once
(`hack/test-hack.sh`), and `make check` runs that. A new `*_test.sh` under
`hack/` joins it by its name alone; keep it free of a real cluster and the
network.

## Testing & Development

```bash
make test                # Unit and envtest tests (real K8s API + etcd, no cluster needed)
make check               # manifests, generate, fmt, vet, lint, validate-yaml, test-hack and test
make validate-yaml       # The YAML against the built-in schemas and our CRDs, offline
make test-hack           # The shell tests of hack/ (hack/**/*_test.sh), all at once, about a minute
make cluster gitea seed  # Kind cluster, Gitea, and the seeded templates
make deploy sample       # Build and deploy the operator into Kind, apply the webshop sample
make session             # Run the test Session pod; make session-kill kills its client
make gitea-local seed-local  # A Gitea of this checkout's own, seeded like Kind's (no kubectl)
make test-gitea          # gitprovider against a local Gitea (make gitea-local), or C4_GITEA_URL if set
make test-gitea-kind     # gitprovider against the seeded Gitea of the Kind cluster
make toolchain-base runner-base agent-sandbox agent-image-base  # Minimal toolchain, CI runner (label base), Agent Sandbox, agent image
make e2e-apps            # The e2e fixture project apps: one hello-world Container (test/e2e/testdata/apps)
make toolchain-go runner-go agent-image-go  # Go and Node toolchain, CI runner (label go) and agent image
make claude-secret       # The real agent's token, from .dev/secrets/claude-token (below) or CLAUDE_TOKEN=...
make claude-secret-copy FROM=webshop TO=apps  # Reuse that Secret in another project
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
(`hack/chart-crds.sh`): an API change runs `make chart` and commits the chart.
Every CEL rule has an envtest spec that expects its message: a create-time rule's
spec creates an object that breaks it, a rule on `oldSelf` creates, patches and
expects the message. A new rule adds one.
`config/agent-sandbox` and `config/metrics-server` fetch remote URLs and are
skipped. What it rendered is in `bin/yaml/`.

Every checkout on a device shares one Go build cache. `make test`, `vet` and
`build` pass `-trimpath`, which keeps the checkout's absolute directory out of Go's
cache keys, so a worktree reuses the packages and test results another worktree
built for the same code (`(cached)`); a plain `go test` without it doesn't. The
flag is in the Makefile, not `GOFLAGS`, so a debugger keeps its paths. A test
result is still keyed on the absolute path of every file the test opens, so suites
that read repo files at run time rerun in each worktree, though they reuse the
compiled test binary: `internal/controller` (envtest CRDs, `templates/`, and the
sources `typeneutral_test.go` reads), `internal/sessionclient` (envtest CRDs) and
`internal/scaffold` (`templates_test.go` reads `templates/`). Those files sit
outside the package, so `//go:embed` can't reach them.

The agents' Claude token lives in one file per device, outside git:
`.dev/secrets/claude-token` in the main checkout (the `.dev/` every worktree
shares), mode 0600. Make it once, from the main checkout:

```sh
claude setup-token                                    # prints the token
mkdir -p .dev/secrets && install -m 600 /dev/stdin .dev/secrets/claude-token
# paste the token, then Enter and Ctrl-D
```

`make claude-secret` then stores it as Secret `claude-credentials` in
`CLAUDE_NAMESPACE` (default `apps`) of the leased cluster, so a session on a fresh
cluster makes the Secret without the token in its argv or transcript. It fails,
naming the path, when the file is missing, empty, or readable by group or others;
it never prints the token. `CLAUDE_TOKEN=...` on the command line or in the
environment wins over the file; `CLAUDE_TOKEN_FILE` names another file.

`make run` runs the operator on your machine, but it can't reach Gitea's in-cluster
address; use `make deploy` for anything that scaffolds.

## Local Gitea

`make gitea-local` runs a Gitea of this checkout's own, with no Kubernetes or
Docker, so the Gitea tests run on any device, lite ones included:

- It downloads the Gitea binary `config/gitea` runs (`GITEA_VERSION`, linux
  amd64 or arm64) into `bin/` and checks its sha256.
- It starts it from `.gitea-local/` (gitignored): SQLite, `INSTALL_LOCK`,
  offline, listening on 127.0.0.1 only, with Actions, mail, SSH and LFS off.
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
pushing the templates; later runs take about half a minute. With
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

No workflow runs on pull requests, so run `make test-gitea` before merging a
change to `internal/gitprovider` or the seed; the Tests workflow runs it on
main.

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
output and `make helm-lint` checks it, offline, in `make validate-yaml` and so in `make
check`; the *Test Chart* workflow, dispatched by hand, installs it into a
fresh Kind cluster on GitHub. Don't `helm install` it into the shared
`c4factory` cluster: `make deploy` owns the CRDs there.

A pushed `vX.Y.Z` tag runs the *Release* workflow, which builds the operator,
UI, toolchain, agent and runner images, pushes them and the chart to GHCR and
creates the GitHub release ([plan](../plans/done/release-workflow.md)), its
notes drafted by `hack/release-notes.sh` from the pull requests merged since
the previous release. The previous release is the highest published release
below the tag (`hack/previous-release.sh`; drafts and prereleases don't
count): a tag whose *Release* failed has no release, so the next notes carry
what it did.

GitHub workflows never run on pull requests. Lint, tests and UI checks run
after a merge to main, each only when its paths change, or on demand. No
workflow starts a Kind cluster on its own: *Test Chart*, *E2E Tests* and
*Verify Release* run only when a human dispatches them. A pushed tag runs *Release*, which builds and
publishes and tests nothing. `make check` is the gate before a merge.

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
hack/lib/github.sh cluster acquire # lease a pool cluster before deploying; prints its name (waits up to 30m; exit 4 names the holders)
hack/lib/github.sh cluster acquire --fresh # the same, on a clean cluster: deletes and recreates the one it leased
hack/lib/github.sh cluster acquire --fresh --bare # the same, with nothing installed: for a release install (getting-started)
hack/lib/github.sh cluster name    # the cluster make uses in this checkout: its lease's, else c4factory
hack/lib/github.sh cluster release # drop it once verification is done
hack/lib/github.sh cluster holder  # who holds which cluster
hack/lib/github.sh cluster pool    # each pool cluster: leased, free or not created yet
hack/lib/github.sh labels          # create the status/priority/kind/source labels (safe to repeat)
RELAY_DRY_RUN=1 hack/relay.sh      # the issue and prompt the next headless session would get
hack/relay.sh                      # headless sessions one after another, overnight (billed)
hack/relay.sh --workers 2          # two relay workers side by side, each in its own worktree (no-cluster devices)
hack/relay-watch.sh --workers 2    # keep the relay ready: start it within a minute of an issue becoming ready
hack/relay-watch.sh --install --workers 2  # the same, kept up by systemd (or cron) across crashes and reboots
touch .relay/stop                  # stop the watch, and every worker after its current session
```

The relay picks the oldest `status/in-progress` issue nobody holds, else the
oldest `status/ready` + `priority/urgent` one, else the oldest `status/ready`
one, skipping issues with a live claim or an open `Depends on #M`, and claims
it (see [docs/plans/parallel-agents.md](../plans/done/parallel-agents.md)). Worker
k runs its sessions in the git worktree `.claude/worktrees/relay-k`, switched
to the issue's branch; uncommitted changes a dead session left there are
stashed, not dropped. Claims keep workers and hand-started sessions off each
other's issues, so the relay starts right away. A session that ends still
holding its claim gets it released; a worker stops when a session leaves no
handoff comment and doesn't close the issue or escalate it
(`status/needs-maintainer`; only the maintainer sets `status/needs-human`). With
nothing ready a worker stops.
Worker k logs to `.relay/relay-k.log`, with one account per session next to
it. A handoff that couldn't reach GitHub waits in the worktree's
`.relay/pending-handoff-<issue>.md`, and the worker's next round posts it first.

### Sharing the Kind clusters between checkouts

Every checkout on the cluster device may work a `needs/cluster` issue. A
deploy and its verification take a whole cluster: `make deploy` from a branch
replaces the operator, CRDs and admission policies another session deployed,
an outage test or `make down`/`make up` stops the node, and Standard tags land
in Gitea's shared `templates` org. So the device runs a pool of
`C4_CLUSTERS` Kind clusters (default 2): `c4factory`, `c4factory-2`, ...,
each with its own Gitea and seeded templates, and a session leases one only
around the steps that touch it:

1. Do the work that needs no cluster first (code, `make check`, envtest).
2. Right before the first cluster step, `hack/lib/github.sh cluster acquire`.
   It leases this checkout the cluster it already holds, else a free one that
   exists, else the first free one, which it then creates
   (`make cluster gitea seed KIND_CLUSTER=<name>`, a few minutes). It prints
   the name on stdout; while other checkouts hold every cluster it waits up
   to `--wait` (default 30m), then exits 4 naming them, and the session hands
   off with only the cluster steps left. A pool cluster belongs to nobody
   between leases: it keeps whatever the last session installed. When the
   issue's "Done when" asks for a fresh cluster, or what is installed isn't
   yours, pass `--fresh`: it prefers a cluster not created yet, else deletes
   the one it leased (`make cluster-delete`) and creates it again as above.
   It touches only the cluster this checkout now leases, never one another
   checkout holds; nothing else in a session deletes a pool cluster. To
   install a release on it ([getting-started](../getting-started.md), the
   chart), pass `--fresh --bare`: the same, created with `make cluster` only,
   no testenv Gitea, seed, `webshop` or `apps`; the chart brings its own
   Gitea into `gitea`, and helm can't take over the testenv's. Such a
   cluster stays marked bare (`cluster pool` says `free, bare`): a plain
   acquire takes it only when no other is free, and then recreates it with
   Gitea and the seed, so it never hands out a bare cluster as a testenv one.
3. Deploy this branch's own build (`make deploy`; never trust what is
   deployed) and verify. Every make target in the checkout uses the leased
   cluster without being told (`hack/lib/github.sh cluster name`), and so do
   `hack/up.sh`, `hack/doctor.sh` and `hack/evidence.sh`; a Bash call doesn't
   keep exported variables, so kubectl takes `--context kind-<name>` each
   time. Never switch the kubeconfig's current context (`kubectl config
   use-context`): every checkout shares `.dev/kube/config` of the main
   checkout, and plain kubectl in another session would follow it. The
   `cluster` target and `hack/up.sh` put it back after kind sets it
   (`hack/kind.sh keep-context`). Images built for a deploy (`make deploy`,
   the agent, runner, seed, session and UI images) are tagged per checkout
   (`c4factory/operator:dev-relay-2-1a2b3c`, from `hack/kind.sh checkout`),
   so two checkouts building at once never share a tag; `hack/kind.sh load`
   loads one into the leased cluster under the name the manifests use
   (`c4factory/operator:dev`). The toolchain base images
   (`c4factory/go:1.26`, `c4factory/base:dev`) stay shared: they change
   rarely and only from `hack/toolchain-*`.
   `make gitea-forward` and `make test-gitea-kind` use port 3000 for `c4factory`
   and 3000+K-1 for `c4factory-K` (`GITEA_PORT`), so two forwards don't
   collide; the e2e tests pick a free one.
4. `hack/lib/github.sh cluster release`.

Each cluster's lease is a file in the git common dir (`.git/` of the main
checkout): `c4-cluster.lease` for `c4factory`, `c4-cluster-<name>.lease` for
the others, naming the cluster, agent, issue and worktree. A checkout holds
at most one. `beat` (the heartbeat hook) refreshes it, and it is stale after
`C4_LEASE_TTL` (2h) or once its worktree is gone; `acquire` takes a stale one
over. `release`, `handoff`, `close`, `status N needs-maintainer` and
`status N needs-human` drop it, and
the relay drops one its session left behind, as it does a claim.
`hack/heavy-guard.sh` denies `make deploy`, `sample`, `e2e`, `test-e2e`, `up`
and `down` while another checkout holds the cluster the command targets
(`KIND_CLUSTER=` in the command, else the checkout's lease, else
`c4factory`). The memory gates below stay as they are.
`hack/lib/github_test.sh`, `hack/relay_test.sh` and `hack/heavy-guard_test.sh`
test the leases.

Raise `C4_CLUSTERS` only after measuring: each idle cluster takes about 1.1
GiB, and up to 4.5 GiB with a Crew running.

The relay stops when no issue is ready, so an issue marked ready or handed back
later waits until someone starts it again. `hack/relay-watch.sh` stays up
instead: every minute it asks `pick` (which changes nothing) and, when there is
an issue and a worker is free, runs `hack/relay.sh --workers N`, which leaves
running workers alone. Asking first keeps the relay's logs free of a worker that
starts, finds nothing and stops every minute. It logs only changes, to `.relay/watch.log`, pulls main every 5
minutes and restarts itself when that changed its code. Every 30 minutes
(`WATCH_TRIM_EVERY`) it runs `hack/trim-go-cache.sh` and logs what that
dropped: nine workers grow Go's build cache by gigabytes an hour.
With nothing ready, it starts a triage of the `status/proposed` issues: for a
new one, for one that changed since the last triage or waits on an issue that
closed since, and every `WATCH_RETRIAGE` (3h) in any case, at most every
`WATCH_TRIAGE` (30m). Every `WATCH_CI_EVERY` (15 minutes) it checks main's CI:
a workflow that runs on a push to main and failed its two newest finished runs
there (`hack/lib/github.sh red`) gets one issue, `main is red: <workflow>`,
ready and urgent, so the next session fixes main before anything else; a single
failure files nothing.
With nothing ready, it also logs, once per change, which skipped issue waits on
which issue nobody works (`hack/lib/github.sh blocked --unworked`; `blocked`
alone lists every skipped issue's blockers, with status and claim), and when
issues wait on each other in a cycle, `cycles --report` comments each of them
once and moves the newest to `status/needs-maintainer`.
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
recorded in `.relay/maintained`, so a quiet night costs nothing.
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
(#262). It logs to `.relay/relay-maintainer.log`.
`hack/relay-watch_test.sh` tests its rounds with `pick` and the relay faked,
the triage's reasons with `proposed --since` against a stubbed GitHub, and its
CI rounds with `gh` faked.

Install the watch once on each device that runs the relay overnight, from the
main checkout: `hack/relay-watch.sh --install --workers N`. It writes the
systemd user unit `c4factory-relay-watch.service` (from
`hack/relay-watch.service`, with this checkout, N and your `PATH`), enables and
starts it, and turns on `loginctl enable-linger`, so the watch starts at boot
without a login. A crash, an OOM kill or `kill <pid>` brings the watch back
within a minute; a watch you started by hand keeps running, and the unit's
watch waits for its lock and takes over when it ends. On a device without
systemd, `--install` adds two crontab lines instead, at boot and every 5
minutes, which start the watch unless one runs. `touch .relay/stop` still stops
it for good: the unit doesn't restart a watch that exits for the stop file, and
cron doesn't start one while the file is there. `hack/relay-watch.sh` starts
the unit again (with the unit's worker count), `--uninstall` removes the unit or
the crontab lines. Install from the main checkout, not a worktree; the unit and
cron get your `PATH` and `RELAY_*`, `WATCH_*`, `HEAVY_JOBS` and `C4_CLUSTER*`
settings, and the GitHub token from the git credential helper, not `GH_TOKEN`. `systemctl --user status
c4factory-relay-watch` shows the unit.

On the device with the Kind cluster, the systemd user manager must have the
`docker` group: the watch, its workers and their sessions inherit the
manager's groups, and a process keeps the groups it started with, so a manager
that started before you joined `docker` can't reach Docker however
`/etc/group` reads now. Check it with
`grep Groups /proc/$(pgrep -u "$USER" -x systemd)/status`: the list must hold
the gid of `docker` (`getent group docker`). If it doesn't, restart the user
manager: log out and in again, or on WSL run `wsl --shutdown` from Windows.
`--install` warns when the manager lacks one of your groups, and the watch logs
it when it starts. Until then, `hack/relay.sh` checks Docker (`docker info`,
`RELAY_DOCKER_WAIT` seconds, default 10) before each pick and, when it can't
reach it, runs that session as `C4_CLUSTER=none`, so it skips `needs/cluster`
issues instead of failing them; its log says why once (permission denied,
daemon down or not answering) and again when Docker answers.

### Checking a release

The relay can't pull a release (the GHCR packages are private and no device's
gh token has `read:packages`), so a workflow does. Once *Release* has
published `vX.Y.Z`, *Verify Release* (`.github/workflows/verify-release.yml`)
checks it twice, each on a Kind cluster of its own, building nothing. It
starts Kind, so it runs only when a human dispatches it: Actions → Verify
Release → Run workflow, or `gh workflow run verify-release.yml -f
tag=vX.Y.Z`. Agents never dispatch it.

The install check installs the published chart and images on a fresh cluster,
waits for the seed's repositories, both runners, the manager and the UI, and
scaffolds the release's `orders-api` sample. Then it follows
[getting-started](../getting-started.md) for the first app: the `ghcr` pull
Secret on `webshop`'s `default` ServiceAccount (it pulls `agent-go` through
it) and `config/samples/first-app/`, set to the release's agent tag and
template refs, until `orders-cli` is scaffolded and its Crew is `Idle`; no
Agent runs, as CI has no Claude token. A tag without `first-app` skips that
part and says so.

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
aside), that Secrets `gitea-admin` and `gitea-runner` are unchanged, and that
every Gitea repository is still there with its earlier head commit (the seed
may add template tags). The chart recreates its hooks (GitProviders, the
catalog) on every upgrade, so objects are matched by kind, namespace and name,
not uid. On the first release there is no previous one, and the
check is skipped and says so. A release whose chart has no catalog (v0.3.0 and
older) has no `first-app` of its own that works on it, so either check takes
the newest samples that made their own catalog and Agents, from history.

Their results are the run's job summaries and the artifacts
`release-vX.Y.Z-install` and `release-vX.Y.Z-upgrade` (Actions → Verify
Release). When a dispatch on main fails it files `Release vX.Y.Z fails its check` (`status/ready`, `priority/urgent`), or
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

### Memory on the cluster device

The device with the Kind clusters has 13.4 GiB and 4 GiB of swap. Four sessions
and a nested devcontainer build on it once stalled every task for minutes and
took down VS Code and every Claude session with it (#57), when the Kind node
took about 4.5 GiB with a Crew's agent sandboxes running. Measured again on
2026-10-05, after the products went (#86):

- each Kind node of the pool with Gitea and the seeded templates: 1.1 to
  1.2 GiB idle, up to 4.5 GiB with a Crew running; the default pool of two
  idles at about 2.5 GiB;
- a relay session (`claude -p` and its children): 0.5 to 1.1 GiB;
- one `make check` at its peak: still 2 to 3 GiB (Go builds, envtest,
  golangci-lint).

A session at rest is cheap; what can still take the device down is several
heavy jobs at once. So the relay admits sessions by memory, and heavy jobs
queue:

- There, the relay runs at most `RELAY_MAX_WORKERS` workers (default 9, every
  worker `--workers` allows), counting those already running; `--workers N`
  above it refuses to start. The memory gate below, not this count, is the
  limit.
- On every device, a worker waits before each pick until `MemAvailable` is at
  least `RELAY_MIN_MEM_GIB` (default 2) and the last session start across
  workers is `RELAY_ADMIT_GAP` seconds old (default 120), and stops after
  `RELAY_MEM_WAIT` seconds (default 3600) without headroom. It logs the wait.
- Heavy jobs queue device-wide: `make check`, `test`, `lint`, `lint-fix`,
  `docker-build`, `deploy`, `e2e` and `test-e2e` take one of `HEAVY_JOBS` slots
  (default 2; 0 turns the queue off) before anything builds, shared by every
  checkout on the device (`hack/heavy-slot.sh`: `flock`ed files in
  `<git common dir>/heavy-slots`). A `make` that finds them all taken waits,
  printing who holds them (pid, checkout, goals, since when) and how many jobs
  wait ahead of it; the wait is not a hang, so give the command a long timeout
  or run it in the background. Waiters get slots first come, first served:
  each holds a numbered ticket, and a waiter that dies loses its turn. A job
  that ends, fails or is killed frees its slot within a second; its sub-makes
  don't take another. `hack/heavy-slot.sh status` shows the slots and the
  waiters.
- `make check` remembers a passing tree, device-wide (`hack/check-cache.sh`).
  A pass is recorded under the git tree hash of the working state as the
  check left it (tracked and untracked non-ignored files, after `manifests`,
  `generate` and `fmt`) in `.dev/cache/c4factory/checks/<tree>`: when, which
  checkout, which agent. A plain `make check` on a tree that already passed, in
  any checkout, prints `make check: passed on tree <hash> at <time> in
  <checkout> (cached); FORCE=1 to rerun` and exits 0 without a heavy slot. A
  cached pass counts as a pass. A failure records nothing; `FORCE=1 make
  check` always runs the whole check and records it. Tool versions,
  `go.mod`/`go.sum` and the Makefile are in the tree, so bumping them misses.
- A PreToolUse hook on Bash, `hack/heavy-guard.sh`, reads `MemAvailable`
  before a heavy job (`make check`, `test`, `lint`, `lint-fix`,
  `docker-build`, `deploy`, `e2e`, `test-e2e`; `devcontainer up`;
  `docker build`). Below `RELAY_MIN_MEM_GIB` it asks the user before the job
  runs, naming the processes with the most memory and the Kind node. A
  headless session (`C4_HEADLESS=1`, set by the relay) gets
  that warning in its context and the job runs, since in `claude -p` a
  question nobody can answer blocks the command. When every slot is taken, it
  tells the session a `make` job will wait, and that the wait is not a hang.
  Other commands cost one `bash` and nothing more.

`hack/relay_test.sh` tests the defaults, the cap and the wait against a faked
`/proc/meminfo` (`RELAY_MEMINFO`), `hack/heavy-guard_test.sh` the hook,
with `ps` and `docker` faked too, and `hack/heavy-slot_test.sh` the slots: two
jobs running while a third waits, a slot freed by a job that ends or is killed,
the oldest waiter taking the next free slot and a dead waiter skipped, and the
Makefile queueing its heavy goals. `hack/check-cache_test.sh` tests the
check cache: a pass, then a hit on the same tree from another worktree, a miss
on one changed byte (tracked or untracked), and `FORCE=1` queueing again.

### CPU on the cluster device

The Kind node has 12 cores and runs etcd, the API server, Gitea, the operator,
the CI runners and the agent sandboxes together. A build without a CPU limit
takes what it can: an uncapped fuzz test took 11 cores in a sandbox, and a
Go release build (`make dist`) took 8.6 in the Go runner while etcd logged 76 slow requests a
minute and the API server failed its probes (#66). So every pod that builds
has a CPU limit, and what must keep answering has a request large enough to
hold its own:

| Pod | CPU request | CPU limit | Set in |
| --- | --- | --- | --- |
| CI runners `gitea-runner-base`, `-go` | 500m | 4 | `config/runner/*.yaml` |
| Agent sandbox, implementer | 1 | 4 | the Agent's `resources` |
| Agent sandbox, reviewer | 500m | 2 | the Agent's `resources` |
| Gitea | 500m | none | `config/gitea/deployment.yaml` |

- **4 cores per runner**, like the implementer: a runner takes one job at a
  time (`capacity: 1` in `hack/runner/config.yaml`), so one job gets 4 cores.
  A runner and a Crew's implementer and reviewer at their limits take 10 of
  the 12 cores and leave 2 for the control plane, Gitea and the operator.
  Go 1.25 and later set `GOMAXPROCS` from the limit, so Go builds and tests
  run 4 threads; Node still sees 12 cores and the kernel throttles
  them to 4.
- **An Agent without a separate reviewer takes the implementer's values**,
  like a single `claude` Agent: a Task reviews each round after implementing
  it, never beside it. So do the stand-ins: they write no code, but their
  implement rounds still run `make check`.
- **Gitea requests 500m**, a runner's share. On a busy node each pod gets CPU
  in proportion to its request; at 50m Gitea got a tenth of a runner's share,
  and its liveness probe restarted it just before that release (#17). It
  stays without a limit, so it can use idle cores.

## GitHub Actions on a self-hosted runner

When GitHub's hosted runners are slow to start, the workflows can run on a
runner in a container on a device's Docker (`hack/gh-runner/`). Every
workflow's `runs-on` is `${{ vars.RUNS_ON || 'ubuntu-latest' }}`, so switching
needs no code change.

```bash
make gh-runner                              # build, register c4factory-<hostname> once, start it
make gh-runner-status                       # its container, and whether GitHub sees it online
make gh-runner-down                         # stop it and remove it from GitHub
gh variable set RUNS_ON --body self-hosted  # every job goes to it
gh variable delete RUNS_ON                  # back to ubuntu-latest
docker logs -f c4factory-gh-runner          # what it is doing
```

- It registers with the labels `self-hosted`, `linux`, `x64` and `c4factory`,
  with a token from the device's `gh` login. The registration lives on a
  volume, so `docker restart`, a reboot and a new image keep it. GitHub drops
  a runner that stays offline for 14 days; `make gh-runner` then registers it
  again. With `RUNS_ON` set and the runner down, jobs wait in the queue.
- Jobs use the device's Docker through its socket, on the host's network, so
  `docker build` and Kind work and a Kind cluster a job makes answers on
  127.0.0.1. The socket is root on the device, which is acceptable for a
  private repository with one owner. The work directory
  (`~/.cache/c4factory/gh-runner/work`) has the same path in the container, so
  a job's bind mounts resolve.
- One runner, so jobs go one at a time. They run on the device's Docker
  outside the `HEAVY_JOBS` slots: a release builds eight images and *Tests*
  runs `make test` whatever the device's sessions are doing.
- The tool cache (setup-go, setup-node) and the Go, npm and golangci-lint
  caches stay on volumes, so the workflows skip GitHub's cache there. *Test
  Chart* and *E2E Tests* make Kind clusters of their own (`chart-test`,
  `ci-e2e`) and delete them after, never one of the pool.
- The image pins the runner (`RUNNER_VERSION` in `hack/gh-runner/Dockerfile`),
  which updates itself when GitHub asks. Bump the pin to
  `gh api repos/actions/runner/releases/latest --jq .tag_name` now and then
  and run `make gh-runner`: it rebuilds the image and starts a new container
  on the same registration.
