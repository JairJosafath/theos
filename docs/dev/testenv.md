# Tests, the Kind cluster and deploying

How to test and run the factory. The e2e tests run only against the dedicated
Kind cluster `c4factory` (`make cluster`), never a real cluster.

## Testing & Development

```bash
make test                # Unit and envtest tests (real K8s API + etcd, no cluster needed)
make check               # manifests, generate, fmt, vet, lint and test
make cluster gitea seed  # Kind cluster, Gitea, and the seeded templates
make deploy sample       # Build and deploy the operator into Kind, apply the webshop sample
make session             # Run the test Session pod; make session-kill kills its client
make test-gitea          # gitprovider against the seeded Gitea
make toolchain runner agent-sandbox agent-image  # Slim toolchain, CI runner, Agent Sandbox, agent image
make toolchain-android runner-android  # Optional: the 20 GB release runner for signed APKs
make toolbox             # The toolbox story; then kubectl apply -k config/samples/toolbox/tasks
make toolchain-go runner-go agent-image-go  # Go and Node toolchain, CI runner (label go) and agent image
make cel-lens cel-lens-backlog  # The CEL Lens story and its issues; then kubectl apply -k config/samples/cel-lens/crews
make claude-secret CLAUDE_TOKEN=...  # The real agent's token, from `claude setup-token`
make claude-secret-copy FROM=toolbox TO=cel-lens  # Reuse that Secret in another project
make e2e                 # All of the above, then check every milestone in Kind
make down / make up       # Stop the factory to free the machine; bring it back healthy
make doctor              # Check every part of the factory and say what is wrong
```

Controller tests use **Ginkgo + Gomega** (BDD style) with the real manager and every
reconciler; see `internal/controller/suite_test.go`. A fake Git host serves the real
`templates/`, so envtest also proves the templates render together. Business-rule tests
use the standard `testing` package with table tests. In tests, write with merge patches
(`patch(obj, func(){...})`), not `Update`: the operator may adopt the object meanwhile.

`make run` runs the operator on your machine, but it can't reach Gitea's in-cluster
address; use `make deploy` for anything that scaffolds.

## Deployment Workflow

```bash
# 1. Regenerate manifests
make manifests generate

# 2. Build, load into Kind, deploy
make deploy            # builds c4factory/operator:dev, kind-loads it, applies config/default, restarts

# 3. Try it
make sample            # the webshop story
kubectl get c4 -n webshop

# 4. Debug
kubectl logs -n c4-factory-system deployment/c4-factory-controller-manager -c manager -f
make gitea-forward     # then open http://localhost:3000
```

The Kind test environment installs with kustomize. Devices install the Helm
chart in `dist/chart/` instead ([its README](../../dist/chart/README.md)).
After a change under `config/`, `make chart` regenerates it from the kustomize
output and `make helm-lint` checks it; the *Test Chart* workflow installs it into
a fresh Kind cluster on GitHub. Don't `helm install` it into the shared
`c4factory` cluster: `make deploy` owns the CRDs there.

A GitHub workflow that needs a Kind cluster (*Test Chart*, *E2E Tests*) never
runs on pull requests, which it would hold up for many minutes. It runs on a
push to main, on a release or on demand (*E2E Tests* only on demand for now),
so a broken chart shows up on main after the merge, not on the pull request.

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
hack/lib/github.sh cluster acquire # take the Kind cluster lease before deploying (waits up to 30m; exit 4 names the holder)
hack/lib/github.sh cluster release # drop it once verification is done
hack/lib/github.sh cluster holder  # who holds the cluster lease
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
it (see [docs/plans/parallel-agents.md](../plans/parallel-agents.md)). Worker
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

### Sharing the Kind cluster between checkouts

Every checkout on the cluster device may work a `needs/cluster` issue; they
share the one Kind cluster through a lease, held only around the steps that
touch it. The checkouts can't share its state while one deploys and verifies:
`make deploy` from a branch replaces the operator and CRDs another session
deployed, an outage or `make down`/`make up` cuts every session off, and a
second cluster (about 4.5 GiB) doesn't fit in memory. So a session does the
work that needs no cluster first (code, `make check`, envtest), runs
`hack/lib/github.sh cluster acquire` right before its first cluster step,
deploys its own branch (`make deploy`, never trusting what is already
deployed), verifies, and runs `cluster release`. `acquire` waits up to
`--wait` (default 30m) while another checkout holds a live lease, then exits 4
naming it; the session then hands off with only the cluster steps left.

The lease is the file `c4-cluster.lease` in the git common dir (`.git/` of
the main checkout), naming the agent, issue and worktree. `beat` (the
heartbeat hook) refreshes it, and it is stale after `C4_LEASE_TTL` (2h) or
once its worktree is gone; `acquire` takes a stale one over. `release`,
`handoff`, `close` and `status N needs-human` drop it, and the relay drops one
its session left behind, as it does a claim. `hack/heavy-guard.sh` denies
`make deploy`, `sample`, `e2e`, `test-e2e`, `up` and `down` while another
checkout holds the lease. The memory gates below stay as they are.
`hack/lib/github_test.sh`, `hack/relay_test.sh` and `hack/heavy-guard_test.sh`
test the lease.

The relay stops when no issue is ready, so an issue marked ready or handed back
later waits until someone starts it again. `hack/relay-watch.sh` stays up
instead: every minute it asks `pick` (which changes nothing) and, when there is
an issue and a worker is free, runs `hack/relay.sh --workers N`, which leaves
running workers alone. Asking first keeps the relay's logs free of a worker that
starts, finds nothing and stops every minute. It logs only changes, to `.relay/watch.log`, pulls main every 5
minutes and restarts itself when that changed its code.
`hack/relay-watch_test.sh` tests its rounds with `pick` and the relay faked.

### Memory on the cluster device

The device with the Kind cluster has 13.4 GiB and 4 GiB of swap. Four sessions
and a nested devcontainer build on it once stalled every task for minutes and
took down VS Code and every Claude session with it (#57), when the Kind node
took about 4.5 GiB with a Crew's agent sandboxes running. Measured again on
2026-10-05, after the products went (#86):

- the Kind node with Gitea and the seeded templates: 1.2 GiB;
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
- Build a devcontainer (`devcontainer up`) only on a device without the
  cluster.
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
and the Makefile queueing its heavy goals.

### CPU on the cluster device

The Kind node has 12 cores and runs etcd, the API server, Gitea, the operator,
the CI runners and the agent sandboxes together. A build without a CPU limit
takes what it can: an uncapped fuzz test took 11 cores in a sandbox
([results-v4](../results-v4.md)), and a `cel-language-server` release
(`make dist`) took 8.6 in the Go runner while etcd logged 76 slow requests a
minute and the API server failed its probes (#66). So every pod that builds
has a CPU limit, and what must keep answering has a request large enough to
hold its own:

| Pod | CPU request | CPU limit | Set in |
| --- | --- | --- | --- |
| CI runners `gitea-runner`, `-go`, `-android` | 500m | 4 | `config/runner/*.yaml` |
| Agent sandbox, implementer | 1 | 4 | the Agent, e.g. `config/samples/cel-lens/factory.yaml` |
| Agent sandbox, reviewer | 500m | 2 | the Agent, e.g. `config/samples/cel-lens/factory.yaml` |
| Gitea | 500m | none | `config/gitea/deployment.yaml` |

- **4 cores per runner**, like the implementer: a runner takes one job at a
  time (`capacity: 1` in `hack/runner/config.yaml`), so one job gets 4 cores.
  A runner and a Crew's implementer and reviewer at their limits take 10 of
  the 12 cores and leave 2 for the control plane, Gitea and the operator.
  Go 1.25 and later set `GOMAXPROCS` from the limit, so Go builds and tests
  run 4 threads; Node and Gradle still see 12 cores and the kernel throttles
  them to 4.
- **An Agent without a separate reviewer takes the implementer's values**,
  like the toolbox's `claude`: a Task reviews each round after implementing
  it, never beside it. So do the stand-ins: they write no code, but their
  implement rounds still run `make check`.
- **Gitea requests 500m**, a runner's share. On a busy node each pod gets CPU
  in proportion to its request; at 50m Gitea got a tenth of a runner's share,
  and its liveness probe restarted it just before that release (#17). It
  stays without a limit, so it can use idle cores.
