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
nothing ready a worker claims the "Factory evaluations" issue and runs
`hack/evaluate.sh` if its newest evaluation is 6 hours old, then picks once
more. After each session it runs `make backup-releases` if the target exists,
except with `C4_CLUSTER=none`, where it logs the skip.
Worker k logs to `.relay/relay-k.log`, with one account per session next to
it. A handoff that couldn't reach GitHub waits in the worktree's
`.relay/pending-handoff-<issue>.md`, and the worker's next round posts it first.

The relay stops when no issue is ready, so an issue marked ready or handed back
later waits until someone starts it again. `hack/relay-watch.sh` stays up
instead: every minute it asks `pick` (which changes nothing) and, when there is
an issue and a worker is free, runs `hack/relay.sh --workers N`, which leaves
running workers alone. Asking first keeps the relay's logs free of a worker that
starts, finds nothing and stops every minute. On the device with the cluster it
also starts the relay every 30 minutes, so a worker can run the evaluator when
it is due. It logs only changes, to `.relay/watch.log`, pulls main every 5
minutes and restarts itself when that changed its code.
`hack/relay-watch_test.sh` tests its rounds with `pick` and the relay faked.

### Memory on the cluster device

The device with the Kind cluster has 14.7 GiB; the Kind node takes about 4.5
with a Crew's agent sandboxes running, and one `make check` (Go builds,
envtest, golangci-lint) 2 to 3 GiB at its peak. Four sessions and a nested
devcontainer build on it stalled every task for minutes and took down VS Code
and every Claude session with it (#57). So:

- There, the relay runs at most `RELAY_MAX_WORKERS` workers (default 1), counting
  those already running; `--workers N` above it refuses to start.
- On every device, a worker waits before each pick until `MemAvailable` is at
  least `RELAY_MIN_MEM_GIB` (default 4) and the last session start across
  workers is `RELAY_ADMIT_GAP` seconds old (default 300), and stops after
  `RELAY_MEM_WAIT` seconds (default 3600) without headroom. It logs the wait.
- Sessions check `free -g` before a heavy job (`make check`, image builds,
  `make deploy`), never run two at once, and build a second devcontainer only
  on a device without the cluster.

`hack/relay_test.sh` tests the cap and the wait against a faked
`/proc/meminfo` (`RELAY_MEMINFO`).

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
- **Gitea requests 500m**, a runner's share. On a busy node each pod gets CPU
  in proportion to its request; at 50m Gitea got a tenth of a runner's share,
  and its liveness probe restarted it just before that release (#17). It
  stays without a limit, so it can use idle cores.
