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
make test                # Unit and envtest tests (real K8s API + etcd, no cluster needed)
make check               # manifests, generate, fmt, vet, lint, validate-yaml, test-hack, ui-test and test
make validate-yaml       # The YAML against the built-in schemas and our CRDs, offline
make test-hack           # The shell tests of hack/ (hack/**/*_test.sh), all at once, about a minute
make test-templates-operator  # The operator combo's make check before and after kubebuilder create api, then its make dist; not in make check
make cluster gitea seed  # Kind cluster, Gitea, and the seeded templates
make deploy sample       # Build and deploy the operator into Kind, apply the webshop sample
make session             # Run the test Session pod; make session-kill kills its client
make gitea-local seed-local  # A Gitea of this checkout's own, seeded like Kind's (no kubectl)
make test-gitea          # gitprovider against a local Gitea (make gitea-local), or C4_GITEA_URL if set
make test-gitea-kind     # gitprovider against the seeded Gitea of the Kind cluster
make toolchain-base runner-base agent-sandbox agent-image-base  # Minimal toolchain (with uv), CI runner (label base), Agent Sandbox, agent image
make e2e-apps            # The e2e fixture project apps: one hello-world Container (test/e2e/testdata/apps)
make toolchain-go runner-go agent-image-go  # Go, Node, uv and kubebuilder toolchain, CI runner (label go) and agent image
make claude-secret       # The real agent's token, once for every project, from .dev/secrets/claude-token (below) or CLAUDE_TOKEN=...
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

`make test` bounds each package at `TEST_TIMEOUT` (20m; the Makefile says why). The
controller suite ends itself 30 s before that, failing the spec that was running by
name. With `C4_SLOWEST_SPECS=FILE` it writes its 10 slowest specs to FILE; the
*Tests* workflow prints them, so a slow run's log says which specs to speed up.

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

After `make deploy`, `make claude-secret` stores it as Secret
`claude-credentials` in `CLAUDE_NAMESPACE` (default `c4-factory-system`, the
operator's namespace) of the leased cluster, without the token in its argv or
transcript. The test environment shares it like the release: `config/manager`
runs the operator with `--shared-secret=claude-credentials` and `POD_NAMESPACE`,
and the operator copies the Secret into every namespace with
the label `c4.factory.io/shared-secrets: "true"`, keeping each copy in step
with it. `make seed` labels `webshop` and `apps`, and the webshop sample and
the e2e fixture label theirs, so one Secret serves every project; a rotated
token reaches them all within five minutes. Another namespace opts in with
`kubectl label namespace NAME c4.factory.io/shared-secrets=true`. A project
with a token of its own runs `make claude-secret CLAUDE_NAMESPACE=NAME`: the
operator leaves a Secret it didn't make alone. `make claude-secret` fails,
naming the path, when the file is missing, empty, or readable by group or
others; it never prints the token. `CLAUDE_TOKEN=...` on the command line or in
the environment wins over the file; `CLAUDE_TOKEN_FILE` names another file.

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
check`. `make chart` keeps `templates/manager/manager.yaml`, which the plugin would
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
v0.19.0 (#912).

GitHub workflows never run on pull requests. Lint, tests and UI checks run
after a merge to main, each only when its paths change, or on demand. No
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
A session that ends on a usage limit or a failed login pauses the relay until
the limit resets (`.relay/paused-until`): every worker waits, and the watch
starts nothing. A session whose transcript stops changing for `RELAY_IDLE`
(45m) is ended and counts as failed. A session that ends without a handoff and
without a new commit on the issue's branch, or on `<branch>-2` or
`<branch>-<name>` (local or pushed), is a failed one: a strike, a comment on
the issue naming the session, its host and the last lines of its stderr and
account. The count is read from the issue, so every device's strikes add up;
one older than `RELAY_STRIKE_TTL` (24h) doesn't count. At `RELAY_MAX_FAILS`
(3) the relay posts one comment linking them and sets the issue to
`status/needs-maintainer`; when only that status swap fails, the next failed
session retries it without a new comment (`.relay/escalated-<issue>`). A
handoff, a commit or a finish on any device resets the count: the relay posts
a reset comment when any strike counts. One that leaves the issue in
`status/waiting` (a plan's parent) has finished, like a handoff; one that sets
it back to `status/ready` or `status/proposed` isn't counted either way.

### Sharing the Kind clusters between checkouts

This is the human path: a person who wants to run Kind sets
`C4_HUMAN_CLUSTER=1` in their own shell; without it `cluster acquire` exits 5
and names the opt-in. The relay never sets it and unsets it for its sessions
(`hack/lib/session-env.sh`), and no prompt or skill tells an agent to. Two humans' checkouts (or a human's worktrees) may still run Kind at
once. A deploy and its verification take a whole cluster: `make deploy` from a branch
replaces the operator, CRDs and admission policies another session deployed,
an outage test or `make down`/`make up` stops the node, and Standard tags land
in Gitea's shared `templates` org. So the device runs a pool of
`C4_CLUSTERS` Kind clusters (default 2): `c4factory`, `c4factory-2`, ...,
each with its own Gitea and seeded templates, and a checkout leases one only
around the steps that touch it:

1. Do the work that needs no cluster first (code, `make check`, envtest).
2. Right before the first cluster step, `C4_HUMAN_CLUSTER=1
   hack/lib/github.sh cluster acquire`.
   It leases this checkout the cluster it already holds, else a free one that
   exists, else the first free one, which it then creates
   (`make cluster gitea seed KIND_CLUSTER=<name>`, a few minutes). It prints
   the name on stdout; while other checkouts hold every cluster it waits up
   to `--wait` (default 30m), then exits 4 naming them. A pool cluster
   belongs to nobody between leases: it keeps whatever the last checkout
   installed. When you want a fresh cluster, or what is installed isn't
   yours, pass `--fresh`: it prefers a cluster not created yet, else deletes
   the one it leased (`make cluster-delete`) and creates it again as above.
   It touches only the cluster this checkout now leases, never one another
   checkout holds; nothing else deletes a pool cluster. To
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
over. `release`, `handoff`, `close`, `status N waiting`, `status N
needs-maintainer` and `status N needs-human` drop it. In the relay and its
sessions (`C4_HEADLESS=1`) `beat` and those commands touch no lease: they
hold none.
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
minutes and restarts itself when that changed its code. The watch and the
workers run origin/main's scripts, whatever branch the main checkout has
(#1126): from the worktree `.claude/worktrees/relay-code`, which each pull moves
to origin/main, while their state (`.relay/`, the workers' worktrees) stays in
the main checkout. One started from the checkout moves there at its first pull,
and one whose code there changed restarts on it; `RELAY_PULL=0` keeps them on
the code where they were started, to try relay changes from a branch. Every 30 minutes
(`WATCH_TRIM_EVERY`) it runs `hack/trim-go-cache.sh` and logs what that
dropped: nine workers grow Go's build cache by gigabytes an hour.
Busy or idle, with issues ready or not (#1191), it starts a triage of the
`status/proposed` issues: for a new one, for one that changed since the last
triage or waits on an issue that closed since, and every `WATCH_RETRIAGE` (3h)
in any case, at most every `WATCH_TRIAGE` (30m) and never while one runs. Past
`WATCH_TRIAGE` it asks GitHub for the proposed issues at most every
`WATCH_RETRY` (10m), as each ask reads every open issue. The triage runs in its own worktree,
`.claude/worktrees/relay-triage`, on origin/main, under `.relay/triage.lock`,
as the maintainer does: it needs no free worker, doesn't count against
`RELAY_MAX_WORKERS` and, running no `make`, takes no `HEAVY_JOBS` slot; it
goes through `admit` like any session and logs to `.relay/relay-triage.log`.
To triage now, say after a plan files its steps `status/proposed`, run
`./dev run hack/relay.sh --triage`: it starts one triage session unless one
runs, and records its time and issues in `.relay/triaged`, as every triage
session does (the watch only reads that file), so the watch starts no second
triage for the same issues, only for one filed after it. Every `WATCH_CI_EVERY` (15 minutes) it checks main's CI:
a workflow that runs on a push to main and failed its two newest finished runs
there (`hack/lib/github.sh red`) gets one issue, `main is red: <workflow>`,
ready and urgent, so the next session fixes main before anything else; a single
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
time, and files `Flaky: <spec>`, a ready `kind/bug` that isn't urgent, when one
failed in two runs within `WATCH_FLAKE_DAYS` days (7); while it is open, a
third failure files nothing. `hack/lib/github.sh flakes --count` lists the
specs that failed on main this week, from GitHub, on any device.
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
long removes both lines. `make gh-runner-status` says why (below). A run
GitHub made no jobs for stays queued for good, and GitHub won't cancel it
(#1236): no runner can take it, so the watch logs it once, by id
(`.relay/runs-without-jobs`), and never notes it.
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
(#262). It runs once per project, not once per device (#526): before its
session it takes the maintainer lease, a `<!-- c4-maintainer -->` comment on
the Maintainer report issue that it refreshes every `GH_BEAT_EVERY` seconds
and deletes when the session ends. While another device holds a live one it
starts nothing and logs the holder; one past `C4_LEASE_TTL`, or this host's
own (a crashed maintainer's), is taken over. It logs to
`.relay/relay-maintainer.log`.
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
it when it starts. Until then its sessions can't reach Docker, so
`make test-gitea` fails in them; they never need a cluster.

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
condition. A patch isn't checked: it ships a fix within the hour on the
upgrade path its minor checked. A human checks any other release by hand:
Actions → Verify Release → Run workflow, or `gh workflow run
verify-release.yml -f tag=vX.Y.Z`. Agents never dispatch it. A failed run on
main files (or comments on) `Release vX.Y.Z fails its check`, ready and
urgent, as the watch does for a red main.

The install check installs the published chart and images on a fresh cluster,
waits for the seed's repositories, both runners, the manager and the UI, and
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
to the guide's commands. A tag whose stand-in releases nothing stops at the
Crew's first Task. Publishing to GitHub isn't checked: it needs a repository
and token of the owner's (#1216).

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
may add template tags). The chart up to v0.12.0 recreated its GitProviders,
catalog and Agents as hooks on every upgrade; later ones have the seed apply
them in place, keeping their uid, but objects are still matched by kind,
namespace and name, not uid. On the first release there is no previous one, and the
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
  `vulncheck`, `docker-build`, `deploy`, `e2e`, `test-e2e` and `test-templates-operator` take one of `HEAVY_JOBS` slots
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
- Two checks at once still start several envtest control planes at once, so
  `make test` gives them slack: 2 minutes for etcd and kube-apiserver to
  start (`ENVTEST_START_TIMEOUT`), and etcd a 10 s election timeout
  (`ENVTEST_ETCD_ELECTION_TIMEOUT`, in ms), which makes its request timeout
  25 s instead of 7 s. Running a spec outside `make test` (an IDE) uses
  envtest's defaults.
- The UI's unit tests run on three Vitest workers (`ui/vite.config.ts`),
  not one per core but one: on a busy device eleven workers, each spawning a
  process per file, stalled tests that take milliseconds past Vitest's 5 s
  timeout (#1193). Three take half the memory and finish sooner on a busy
  device, a few seconds later on a quiet one.
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

## GitHub Actions on self-hosted runners

When GitHub's hosted runners are slow to start or start no jobs, the workflows
can run on runners in containers on a device's Docker (`hack/gh-runner/`).
Every workflow's `runs-on` uses the repository variable `RUNS_ON`, so switching
needs no code change. On the device, with `gh` logged in to the repository:

```bash
make gh-runner RUNNERS=3                    # build, register c4factory-<hostname>-1..3 once, start them
gh variable set RUNS_ON --body self-hosted  # every job goes to them
make gh-runner-status                       # whether Docker answers, their containers, whether GitHub sees them online
gh variable delete RUNS_ON                  # back to ubuntu-latest
make gh-runner-down                         # stop them all and remove them from GitHub
docker logs -f c4factory-gh-runner-1        # what runner 1 is doing
```

- Runner *i* is the container `c4factory-gh-runner-i`, registered as
  `c4factory-<hostname>-i` with the labels `self-hosted`, `linux`, `x64` and
  `c4factory`, with a token from the device's `gh` login. Its registration
  lives on a volume of its own (`c4factory-gh-runner-i-config`), so `docker
  restart`, a reboot and a new image keep it. GitHub drops a runner that stays
  offline for 14 days; `make gh-runner` then registers it again. With
  `RUNS_ON` set and the runners down, jobs wait in the queue: nothing fails,
  and GitHub has no runner to fall back on.
- The runners run on the device's Docker. On WSL that is Docker Desktop, with
  WSL integration on for the distro. The containers (`--restart
  unless-stopped`) stop when it stops and start again with it. The relay watch
  notes runs queued on main for over 30 minutes in the Maintainer report
  (above). `make gh-runner-status` then says which part is down. Docker not
  answering from this distro means starting Docker Desktop. A container down
  means running `make gh-runner RUNNERS=N`. A runner GitHub sees offline while
  its container is up has its reason in `docker logs --tail 50
  c4factory-gh-runner-i`. It exits 1 unless every runner is online. Setting
  `RUNS_ON` back is the owner's call.
- `make gh-runner RUNNERS=N` again with a smaller N removes the runners above
  N, from Docker and from GitHub; `RUNNERS` defaults to 1. A device that ran
  the single runner from before (container `c4factory-gh-runner`, named
  `c4factory-<hostname>`) has it removed and registered again as runner 1 on
  the first `make gh-runner`.
- **One Kind job per device.** *Test Chart*, *E2E Tests* and *Verify Release*
  make Kind clusters with fixed names (`chart-test`, `ci-e2e`,
  `release-<check>`) and take several GB of the device's memory. Only runner 1
  of a device carries the label `c4factory-kind`, and while `RUNS_ON` is
  `self-hosted` those jobs ask for it (`runs-on: ${{ vars.RUNS_ON ==
  'self-hosted' && fromJSON('["self-hosted", "c4factory-kind"]') || ... }}`),
  so a device runs one of them at a time and two devices run one each. A
  label, not a `concurrency` group: a group spans the repository, not a
  device, and keeps only the newest pending run, cancelling the others; and
  per-runner cluster names would still put two clusters in one device's
  memory. `hack/check-workflows.sh` (in `make check`) fails a job whose
  `runs-on` doesn't use `vars.RUNS_ON`, and a job that makes a Kind cluster
  without the label.
- **Lint and UI run on pull requests too; Tests tests every merge commit.**
  *Lint* (with its `test-hack` job, `make test-hack`) and *UI* run on pushes
  to main and on pull requests to main, with the same path filters, so vet,
  lint and the shell tests answer before `hack/lib/github.sh land`. They carry
  the `concurrency` group `${{ github.workflow }}-${{ github.ref }}` without
  `cancel-in-progress`: a running job always finishes, and only the newest
  pending run per branch or pull request waits; GitHub cancels the older
  pending ones. *Tests* runs on pushes to main only (a run takes about twelve
  minutes, and `land` runs `make check` on the merged tree), grouped by
  `${{ github.workflow }}-${{ github.sha }}`, so no run is dropped and every
  merge commit gets its own verdict. The "main is red" watch (`gh_red` in `hack/lib/github.sh`) skips cancelled runs,
  and failed runs a self-hosted runner lost (`gh_lost`): a runner stopped
  mid-job cancels its step ("The operation was canceled.", Ginkgo prints
  "Interrupted by User"), and a job no runner took is cancelled without steps.
  Neither says anything about the code. *Issue intake*
  and *Issue closed* have no group, since each issue event must run.
  `hack/check-workflows.sh` fails a workflow on pushes to a branch without
  one of the two groups, or with `cancel-in-progress`; Lint or UI off pull
  requests to main or with other paths there; Lint without its `test-hack`
  job; and Tests on pull requests or grouped by anything but the commit.
- **Vulnerabilities runs govulncheck.** *Vulnerabilities* (`vulncheck.yml`)
  runs `make vulncheck` (`govulncheck ./...`, which needs the network and so
  stays out of `make check`) on pushes to main and pull requests to main that
  change the Go code, the modules or the `Makefile`, and every Monday at 04:23
  UTC, so a new advisory shows up without a change. It fails on a known
  vulnerability the code reaches, in a module or in the standard library of
  `go.mod`'s toolchain; two failed runs on main file "main is red:
  Vulnerabilities", which a bump of the toolchain or the module fixes.
- Jobs use the device's Docker through its socket, on the host's network, so
  `docker build` and Kind work and a Kind cluster a job makes answers on
  127.0.0.1. The socket is root on the device, which is acceptable for a
  private repository with one owner. Runner *i*'s work directory
  (`~/.cache/c4factory/gh-runner/work/i`) has the same path in the container,
  so a job's bind mounts resolve.
- Each runner takes one job at a time. They run on the device's Docker
  outside the `HEAVY_JOBS` slots: a release builds eight images and *Tests*
  runs `make test` whatever the device's sessions are doing, so pick N for the
  device's memory.
- The Go module, build and golangci-lint caches and the npm cache are one
  volume the runners share (`c4factory-gh-runner-cache`): Go, golangci-lint and
  npm lock what they write there. The Makefile's tools (controller-gen,
  golangci-lint, the envtest binaries, Gitea) are kept there too, in
  `~/.cache/make-bin` (`LOCALBIN`), since `actions/checkout` deletes the
  checkout's `.dev/` before each job; they install through a temporary
  directory, as when worktrees share `.dev/`. The tool cache setup-go and setup-node unpack
  into is a volume per runner (`c4factory-gh-runner-i-toolcache`): two runners
  unpacking one Go version into it at once would race. The workflows skip
  GitHub's cache on self-hosted runners. `make gh-runner-down` keeps the caches.
- **What a job leaves behind.** The containers live on, job after job, so the
  caches stay warm. Between jobs the runner empties `_temp`, and before each
  job the image's job-started hook (`hack/gh-runner/job-started.sh`, set as
  `ACTIONS_RUNNER_HOOK_JOB_STARTED`) resets the rest a job could leave:
  - it makes the work directory writable again (and the runner's, through
    `sudo`, where a container wrote as root), so `actions/checkout` can clean
    it;
  - it empties the runner's home of everything the image didn't put there
    (the list in `/etc/gh-runner/home.list`, recorded at build time): the
    `docker login` and `helm registry login` of *Release* and *Verify
    Release*, `~/.kube/config` with the contexts of deleted Kind clusters,
    `gh`'s login and any other file.

  What persists: `~/.cache` (the shared cache volume, `LOCALBIN` included),
  the tool cache, the runner itself with its registration, logs and updates,
  and the image's own files, as the last job left them. Kind clusters and
  images on the device's Docker are out of the hook's reach; the Kind
  workflows delete their cluster before making it and after the job. What the
  hook can't reset is a warning in the job's *Set up runner* log, not a
  failed job. `hack/gh-runner/job-started_test.sh` checks it on a seeded home
  and work directory.
- The image pins the runner (`RUNNER_VERSION` in `hack/gh-runner/Dockerfile`),
  which updates itself when GitHub asks. Bump the pin to
  `gh api repos/actions/runner/releases/latest --jq .tag_name` now and then
  and run `make gh-runner`: it rebuilds the image and starts new containers
  on the same registrations.
