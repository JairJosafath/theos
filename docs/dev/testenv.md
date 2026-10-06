# Tests, the Kind cluster and deploying

How to test and run the factory. The e2e tests run only against the dedicated
Kind clusters of the device's pool (`c4factory`, `c4factory-2`, ...; `make
cluster`), never a real cluster. Run `make e2e` only when the user explicitly
asks for it; `make check` is the gate for every change. Every make target
works on `KIND_CLUSTER`, by default the pool cluster this checkout leased,
else `c4factory`; kubectl calls name it with `--context kind-<name>`.

## Testing & Development

```bash
make test                # Unit and envtest tests (real K8s API + etcd, no cluster needed)
make check               # manifests, generate, fmt, vet, lint and test
make cluster gitea seed  # Kind cluster, Gitea, and the seeded templates
make deploy sample       # Build and deploy the operator into Kind, apply the webshop sample
make session             # Run the test Session pod; make session-kill kills its client
make gitea-local seed-local  # A Gitea of this checkout's own, seeded like Kind's (no kubectl)
make test-gitea          # gitprovider against a local Gitea (make gitea-local), or C4_GITEA_URL if set
make test-gitea-kind     # gitprovider against the seeded Gitea of the Kind cluster
make toolchain-base runner-base agent-sandbox agent-image-base  # Minimal toolchain, CI runner (label base), Agent Sandbox, agent image
make e2e-apps            # The e2e fixture project apps: one hello-world Container (test/e2e/testdata/apps)
make toolchain-go runner-go agent-image-go  # Go and Node toolchain, CI runner (label go) and agent image
make claude-secret CLAUDE_TOKEN=...  # The real agent's token, from `claude setup-token`
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
After a change under `config/`, `make chart` regenerates it from the kustomize
output and `make helm-lint` checks it; the *Test Chart* workflow installs it into
a fresh Kind cluster on GitHub. Don't `helm install` it into the shared
`c4factory` cluster: `make deploy` owns the CRDs there.

A pushed `vX.Y.Z` tag runs the *Release* workflow, which builds the operator,
UI, toolchain, agent and runner images, pushes them and the chart to GHCR and
creates the GitHub release ([plan](../plans/done/release-workflow.md)).

GitHub workflows never run on pull requests. Lint, tests, UI checks and *Test
Chart* run after a merge to main, each only when its paths change, or on demand;
*E2E Tests* only on demand. A pushed tag runs *Release*, which builds and
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
hack/lib/github.sh cluster name    # the cluster make uses in this checkout: its lease's, else c4factory
hack/lib/github.sh cluster release # drop it once verification is done
hack/lib/github.sh cluster holder  # who holds which cluster
hack/lib/github.sh cluster pool    # each pool cluster: leased, free or not created yet
hack/lib/github.sh labels          # create the status/priority/kind/source labels (safe to repeat)
RELAY_DRY_RUN=1 hack/relay.sh      # the issue and prompt the next headless session would get
hack/relay.sh                      # headless sessions one after another, overnight (billed)
hack/relay.sh --workers 2          # two relay workers side by side, each in its own worktree (no-cluster devices)
hack/relay-watch.sh --workers 2    # keep the relay ready: start it within a minute of an issue becoming ready
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
handoff comment and doesn't close the issue or set `status/needs-human`. With
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
   off with only the cluster steps left.
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
over. `release`, `handoff`, `close` and `status N needs-human` drop it, and
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
minutes and restarts itself when that changed its code.
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
once and moves the newest to `status/needs-human`.
`hack/relay-watch_test.sh` tests its rounds with `pick` and the relay faked,
the triage's reasons with `proposed --since` against a stubbed GitHub, and its
CI rounds with `gh` faked.

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
  printing who holds them (pid, checkout, goals, since when); the wait is not a
  hang, so give the command a long timeout or run it in the background. A job
  that ends, fails or is killed frees its slot within a second; its sub-makes
  don't take another. `hack/heavy-slot.sh status` shows the slots.
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
and the Makefile queueing its heavy goals. `hack/check-cache_test.sh` tests the
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
