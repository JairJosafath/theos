# Devices: the cluster pool, the runners, memory, disk and CPU

What one device of the project holds and how it is tuned: the caches every
checkout on it shares, the agents' Claude token, the Kind clusters of its
pool, the relay watch as a service, the self-hosted GitHub runners, and the
memory, disk and CPU numbers behind the relay's and the heavy jobs' limits.
How to test, deploy and run the relay is in [testenv.md](testenv.md); how to
set a device up is in [DEVELOPMENT.md](../../DEVELOPMENT.md).

## Caches every checkout shares

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

In a checkout, `make test` answers each unchanged package from Go's test
cache, `ok <package> (cached)`, in no time (#1282). Go keys a package's result
on its test binary (the package and all it imports), the `go test` flags, the
value of each environment variable the test read (`KUBEBUILDER_ASSETS`,
`HELM`, the `ETCD_*` of `ENVTEST_ENV`) and the size and mtime of each file in
the module it opened (the CRDs, the role, the admission policy, `templates/`).
So a change to `hack/`, docs or the UI reruns no Go test, and a changed CRD
reruns the suites that read it. controller-gen rewrites every file it
generates, changed or not, so `manifests` and `generate` run it through
`hack/keep-mtime.sh`, which gives a file rewritten with the same bytes its old
mtime back. envtest reserves each port it hands out as a file in
`$XDG_CACHE_HOME/kubebuilder-envtest`, and `./dev` puts `XDG_CACHE_HOME` in
`.dev/`, inside the module, where each envtest run changed a directory the
other suites had read; `make test` gives the tests `TEST_CACHE_HOME` instead
(`~/.cache/theos/test-cache`, one per device, outside any checkout, #1326) and keeps `GOCACHE`. A
result is not cached when a file the test read changed in the 2 seconds
before; the next run caches it. Coverage is collected only when
asked: `COVER=1 make test` writes `cover.out`, and CI's Tests workflow asks.
To rerun what is cached, `FORCE=1 make test` (or `FORCE=1 make check`, which
skips the check cache too) passes `-count=1`; `go clean -testcache` forgets
every cached result. `hack/keep-mtime_test.sh` tests the script and the flags
`make test` passes.

## The agents' Claude token

The agents' Claude token lives in one file per device, outside git:
`.dev/secrets/claude-token` in the main checkout (the `.dev/` every worktree
shares), mode 0600. Make it once, from the main checkout:

```sh
claude setup-token                                    # prints the token
mkdir -p .dev/secrets && install -m 600 /dev/stdin .dev/secrets/claude-token
# paste the token, then Enter and Ctrl-D
```

After `make deploy`, `make claude-secret` stores it as Secret
`claude-credentials` in `CLAUDE_NAMESPACE` (default `theos-system`, the
operator's namespace) of the leased cluster, without the token in its argv or
transcript. The test environment shares it like the release: `config/manager`
runs the operator with `--shared-secret=claude-credentials` and `POD_NAMESPACE`,
and with `--model-proxy-url` naming the model proxy's Service, so a sandbox on
the shared token gets `ANTHROPIC_BASE_URL` pointing at the proxy instead of the
token (the chart passes both when `agents.enabled`). The operator copies no
token: the proxy reads it in the operator's namespace, and a rotated token
reaches it within the kubelet's sync period, about a minute. The label
`theos.factory.io/shared-secrets: "true"` still gates the proxy's NetworkPolicy
and the pull secrets the operator copies (`--shared-pull-secret`): `make seed`
labels `webshop` and `apps`, and the webshop sample and the e2e fixture label
theirs, so one Secret serves every project. Another namespace opts in with
`kubectl label namespace NAME theos.factory.io/shared-secrets=true`. A project
with a token of its own needs a Secret of another name in its namespace and
Agents naming it (its keys become the sandbox's environment); a
`claude-credentials` that `make claude-secret CLAUDE_NAMESPACE=NAME` writes
there is unused, since an Agent naming the shared name goes through the
proxy. The operator leaves a Secret it didn't make alone. `make claude-secret`
fails,
naming the path, when the file is missing, empty, or readable by group or
others; it never prints the token. `CLAUDE_TOKEN=...` on the command line or in
the environment wins over the file; `CLAUDE_TOKEN_FILE` names another file.

## Sharing the Kind clusters between checkouts

This is the human path: a person who wants to run Kind sets
`THEOS_HUMAN_CLUSTER=1` in their own shell; without it `cluster acquire` exits 5
and names the opt-in. The relay never sets it and unsets it for its sessions
(`hack/lib/session-env.sh`), and no prompt or skill tells an agent to. Two humans' checkouts (or a human's worktrees) may still run Kind at
once. A deploy and its verification take a whole cluster: `make deploy` from a branch
replaces the operator, CRDs and admission policies another session deployed,
an outage test or `make down`/`make up` stops the node, and Standard tags land
in Gitea's shared `templates` org. So the device runs a pool of
`THEOS_CLUSTERS` Kind clusters (default 2): `theos`, `theos-2`, ...,
each with its own Gitea and seeded templates, and a checkout leases one only
around the steps that touch it:

1. Do the work that needs no cluster first (code, `make check`, envtest).
2. Right before the first cluster step, `THEOS_HUMAN_CLUSTER=1
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
   (`hack/kind.sh keep-context`). After the create, `make cluster` writes
   containerd's `hosts.toml` into every node (`hack/kind.sh registry-hosts`),
   mapping `gitea.gitea.svc.cluster.local:3000` to the fixed ClusterIP of
   `config/gitea/service.yaml` (`10.96.0.30`), so a node pulls a Container's
   image from the in-cluster Gitea by the name the Deployment carries; the
   test environment's shortcut, not the chart's. It can't live in
   `hack/kind.yaml`: `extraMounts` needs an absolute host path, and the
   `registry.mirrors` patch is ignored once containerd reads `certs.d`.
   That `clusterIP` is immutable, so `make gitea` deletes a Gitea Service
   from before the pin, one whose IP differs (`hack/kind.sh gitea-service`),
   before it applies `config/gitea`; the apply recreates it (#1679).
   Images built for a deploy (`make deploy`,
   the agent, runner, seed, session and UI images) are tagged per checkout
   (`theos/operator:dev-relay-2-1a2b3c`, from `hack/kind.sh checkout`),
   so two checkouts building at once never share a tag; `hack/kind.sh load`
   loads one into the leased cluster under the name the manifests use
   (`theos/operator:dev`). The toolchain base images
   (`theos/go:1.26`, `theos/base:dev`) stay shared: they change
   rarely and only from `hack/toolchain-*`.
   `make gitea-forward` and `make test-gitea-kind` use port 3000 for `theos`
   and 3000+K-1 for `theos-K` (`GITEA_PORT`), so two forwards don't
   collide; the e2e tests pick a free one.
4. `hack/lib/github.sh cluster release`.

Each cluster's lease is a file in the git common dir (`.git/` of the main
checkout): `c4-cluster.lease` for `theos`, `c4-cluster-<name>.lease` for
the others, naming the cluster, agent, issue and worktree. A checkout holds
at most one. `beat` (the heartbeat hook) refreshes it, and it is stale after
`THEOS_LEASE_TTL` (2h) or once its worktree is gone; `acquire` takes a stale one
over. `release`, `handoff`, `close`, `status N waiting`, `status N
needs-maintainer` and `status N needs-human` drop it. In the relay and its
sessions (`THEOS_HEADLESS=1`) `beat` and those commands touch no lease: they
hold none.
`hack/heavy-guard.sh` denies `make deploy`, `sample`, `e2e`, `test-e2e`, `up`
and `down` while another checkout holds the cluster the command targets
(`KIND_CLUSTER=` in the command, else the checkout's lease, else
`theos`). The memory gates below stay as they are.
`hack/lib/github_test.sh`, `hack/relay_test.sh` and `hack/heavy-guard_test.sh`
test the leases.

Raise `THEOS_CLUSTERS` only after measuring: each idle cluster takes about 1.1
GiB, and up to 4.5 GiB with a Crew running.

## The relay watch as a service

Install the watch once on each device that runs the relay overnight, from the
main checkout: `hack/relay-watch.sh --install --workers N`. It writes the
systemd user unit `theos-relay-watch.service` (from
`hack/relay-watch.service`, with this checkout, N and your `PATH`), enables and
starts it, and turns on `loginctl enable-linger`, so the watch starts at boot
without a login. A crash, an OOM kill or `kill <pid>` brings the watch back
within a minute; a watch you started by hand keeps running, and the unit's
watch waits for its lock and takes over when it ends. On a device without
systemd, `--install` adds two crontab lines instead, at boot and every 5
minutes, which start the watch unless one runs. `touch .relay/stop` still stops
it for good: the unit doesn't restart a watch that exits for the stop file, and
cron doesn't start one while the file is there. `hack/relay-watch.sh` starts
the unit again (with the unit's worker count); on a cron install, `rm -f
.relay/stop` alone, and cron's next tick starts the watch under its own
login ([testenv.md](testenv.md), "The restore depends on the install").
`--uninstall` removes the unit or the crontab lines. Install from the main checkout, not a worktree; the unit and
cron get your `PATH` and `RELAY_*`, `WATCH_*`, `HEAVY_JOBS` and `THEOS_CLUSTER*`
settings, and the GitHub token from the git credential helper, not `GH_TOKEN`.
They also get the Claude Code login of the shell you install from
(`CLAUDE_CONFIG_DIR`, or `~/.claude` when unset), set after `./dev run`, which
would otherwise point every session at `.dev/claude` (#1435); `--install`
refuses a login without `.credentials.json`, so install from a logged-in
shell: `CLAUDE_CONFIG_DIR=$HOME/.claude hack/relay-watch.sh --install` uses
the VS Code login. `systemctl --user status theos-relay-watch` shows the
unit.

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

## Memory on the cluster device

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
  `vulncheck`, `docker-build`, `deploy`, `e2e`, `test-e2e`, `test-templates-operator` and `test-templates` take one of `HEAVY_JOBS` slots
  (default 2; 0 turns the queue off) before anything builds, shared by every
  checkout on the device (`hack/heavy-slot.sh`: `flock`ed files in
  `<git common dir>/heavy-slots`). A `make` that finds them all taken waits,
  printing who holds them (pid, checkout, goals, since when) and how many jobs
  wait ahead of it; the wait is not a hang, so give the command a long timeout
  or run it in the background. Waiters get slots first come, first served:
  each holds a numbered ticket, and a waiter that dies loses its turn. One
  job goes first: the `make check` that `land` runs under the merge turn
  (`THEOS_HEAVY_FIRST=1`, which the Makefile turns into `hack/heavy-slot.sh
  acquire --first`) takes the next slot that frees, ahead of every waiter,
  since every device's lands wait on it while the other checks hold up only
  their own. It never takes a held slot, and one pull request at a time holds
  the turn across every device, so the other waiters can't starve. The check
  `land` runs before it queues for the turn waits its turn like any other. A
  job that ends, fails or is killed frees its slot within a second; its
  sub-makes don't take another. `hack/heavy-slot.sh status` shows the slots
  and the waiters, the one going first marked `goes first` and the slot it
  took `went first`.
- The controller suite runs in `THEOS_TEST_PROCS` processes (default 2), each
  with its own control plane and manager (#1283). Measured on 2026-10-16 on
  this device (9.6 GiB, `HEAVY_JOBS=1` then), the suite alone, the peak RSS
  of its whole process tree (go test, the test binaries, etcd,
  kube-apiserver):

  | `THEOS_TEST_PROCS` | Wall time | Peak RSS |
  | --- | --- | --- |
  | 1 | 834 s | 1.0 GiB |
  | 2 | 484 s | 2.2 GiB |
  | 4 | 242 s | 4.4 GiB |

  Each part costs about 1.1 GiB. Two checks at once at 2 take about 4.4 GiB
  for their suites and leave the device above `RELAY_MIN_MEM_GIB`; at 4 they
  would take 8.8 GiB, more than it has free. So the default is 2, and below
  `THEOS_TEST_MIN_MEM_GIB` (4 GiB, two parts and the relay's 2 GiB of
  headroom) of `MemAvailable` the suite runs in one process. A device with
  memory to spare runs `THEOS_TEST_PROCS=4 make check`.
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
  `generate` and `fmt`) in `.dev/cache/theos/checks/<tree>`: when, which
  checkout, which agent. A plain `make check` on a tree that already passed, in
  any checkout, prints `make check: passed on tree <hash> at <time> in
  <checkout> (cached); FORCE=1 to rerun` and exits 0 without a heavy slot. A
  cached pass counts as a pass. A failure records nothing; `FORCE=1 make
  check` always runs the whole check and records it. Tool versions,
  `go.mod`/`go.sum` and the Makefile are in the tree, so bumping them misses.
  On a miss, Go's test cache (above) still answers each unchanged package,
  and three tiers answer from their own cache (#1285): `validate-yaml`,
  `test-hack` and `ui-test` each run through `hack/check-cache.sh run TIER`,
  which skips a tier whose key passed on this device, in any checkout, and
  says so in one line (`ui-test: cached (ui/ unchanged since <time>, passed
  in <checkout>); FORCE=1 to rerun`). A key is the hash of `git ls-files -s`
  over the files the tier reads, in the working state after `manifests`,
  `generate` and `fmt`, and the versions of the tools the Makefile doesn't
  pin; every key covers the Makefile, `hack/*.mk` and `hack/check-cache.sh`,
  whose pins and rules the tier runs by:
  - `ui-test`: `ui/`, `node --version` and `npm --version`.
  - `test-hack`: everything but `api/`, `cmd/`, `internal/`, `templates/`,
    `test/`, `features/`, `docs/` and `ui/`, since the shell tests read the
    Makefile, the chart, `config/`, `.claude/` and `.github/` as well as
    `hack/`; plus the files there they read (`docs/getting-started.md`,
    `docs/dev/testenv.md`, `ui/Dockerfile`, `ui/package.json`,
    `ui/package-lock.json`), and the bash and git versions.
  - `validate-yaml`: `config/`, `dist/`, `api/`, `templates/`,
    `test/e2e/testdata`, `test/crds`, `test/invalid`, its scripts
    (`hack/validate-yaml.sh`, `hack/chart-*.sh`, `hack/k8s-openapi.sh`), the
    seed and session manifests, `go.mod`, `go.sum`, the module's packages
    `go run ./hack/render` and `./hack/runner-fence` build (`go list -deps`),
    and `go version`.

  `lint` keeps golangci-lint's own cache. Tier passes live in
  `.dev/cache/theos/checks/tiers/<tier>-<key>`; a failing tier records
  nothing, `FORCE=1` runs every tier, and a dry run (`make -n check`)
  records nothing. So a `hack/`-only change runs the shell tests and skips
  the UI's check and `validate-yaml`; one under `ui/` runs only the UI's check.
- A PreToolUse hook on Bash, `hack/heavy-guard.sh`, reads `MemAvailable`
  before a heavy job (a `make` of a goal in the Makefile's `HEAVY_GOALS`:
  `check`, `test`, `lint`, `lint-fix`, `vulncheck`, `docker-build`, `deploy`,
  `e2e`, `test-e2e`, `test-templates-operator`, `test-templates`;
  `devcontainer up`; `docker build`). `hack/heavy-guard_test.sh` reads
  `HEAVY_GOALS` from the Makefile, so a goal added there fails the test until
  the hook flags it too (#1324). In an agent sandbox (`IS_SANDBOX=1`) it, and
  every other hook `.claude/settings.json` runs, exits 0 before reading its
  input (#1894). Below `RELAY_MIN_MEM_GIB` it asks the user before the job
  runs, naming the processes with the most memory and the Kind node. A
  headless session (`THEOS_HEADLESS=1`, set by the relay) gets
  that warning in its context and the job runs, since in `claude -p` a
  question nobody can answer blocks the command. When every slot is taken, it
  tells the session a `make` job will wait, and that the wait is not a hang.
  Other commands cost one `bash` and nothing more.

`hack/relay_test.sh` tests the defaults, the cap and the wait against a faked
`/proc/meminfo` (`RELAY_MEMINFO`), `hack/heavy-guard_test.sh` the hook,
with `ps` and `docker` faked too, and `hack/heavy-slot_test.sh` the slots: two
jobs running while a third waits, a slot freed by a job that ends or is killed,
the oldest waiter taking the next free slot and a dead waiter skipped, a
`--first` waiter taking the next free slot ahead of older waiters but never a
held one while the others keep their order, and the Makefile queueing its
heavy goals, going first only with `THEOS_HEAVY_FIRST`, which its recipes don't
inherit. `hack/check-cache_test.sh` tests the
check cache: a pass, then a hit on the same tree from another worktree, a miss
on one changed byte (tracked or untracked), and `FORCE=1` queueing again; and
the tiers: which keys a change under `ui/`, `hack/`, `config/`, `docs/` or a
package `hack/render` builds moves, a hit that doesn't run the tier, `FORCE=1`
and a failure that do, and the Makefile's cached and dry-run paths.

## Disk space on WSL

On 2026-10-16 the Windows drive C: filled while the relay ran; the machine had
to be powered off, and tracked files in two checkouts came back 0 bytes
(#1344). Inside WSL `df /` showed 930 GB free the whole time: WSL's disk is a
dynamic `ext4.vhdx` on C: (74 GB, 60 GB in use), and Docker Desktop's
`docker_data.vhdx` another (22.5 GB). Neither file shrinks when files inside
are deleted, so the real limit is the drive that holds them.

So before each session, where it waits for memory, a worker checks the free
space on each watched filesystem: `/`, the checkouts' and, under WSL, the
drive of each distro's `BasePath` (Docker Desktop's included), read from
`HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss` with `reg.exe` and
mapped with `wslpath`, or `/mnt/c` when that fails. `RELAY_DISK_PATHS`
(space-separated) overrides the list. While one has less than
`RELAY_MIN_FREE_GB` (default 15) free, the worker starts no session, logs the
path, its free space and the minimum (at most once per path an hour, across
workers: `.relay/disk-low-<path>`), and prunes the caches at once
(`hack/prune-caches.sh`). The wait counts toward `RELAY_MEM_WAIT` like the
wait for memory. Sessions already running go on.

Docker's disk grew most. `docker_data.vhdx` went from 22.5 GB on 2026-10-16
to 102 GB on 2026-10-22, about 13 GB for each of six releases (#1636): the
self-hosted runners (`hack/gh-runner`) use the device's Docker through
`docker.sock`, *Release* built ten images a release, three of them about
5 GB, and kept them, *Verify Release* pulled them again, and the build cache
reached 20 GB within the 72h the prune kept. What keeps it under the peak
it reached, all tested without Docker (`hack/check-workflows_test.sh`,
`hack/prune-caches_test.sh`):

- *Release*'s last step, under `if: always()`, removes every image the build
  tagged once they are pushed, whatever the steps before did: they are on
  GHCR. *Verify Release*'s last step removes the agent images it pulled
  (`hack/load-images.sh` removes each once Kind has it; the step catches what
  a failed or cancelled load left). `hack/check-workflows.sh` (in `make
  check`) fails either workflow without that step, and *Release* with it
  before the push. The base images the Dockerfiles pin stay: local builds
  share them, and only a bump leaves an old one behind.
- `hack/prune-caches.sh` caps Docker's build cache at `THEOS_PRUNE_DOCKER_MAX_GB`
  (10 GB, oldest first) on top of the 72h rule, and drops the dangling images,
  every 30 minutes from `hack/relay-watch.sh` and when a worker finds a
  watched filesystem low.

Deleting files inside WSL frees space in the distro, not on C:: the `.vhdx`
files never shrink. We no longer compact them (`diskpart`'s `compact vdisk`)
or purge Docker Desktop's data: compacting proved too buggy (the owner,
2026-10-22, #1635). WSL's own `wsl --manage <distro> --set-sparse true` would
shrink the disk as it goes, but WSL marks sparse VHDs unsafe (they can corrupt
the disk). So what the disks hold has to stay under the peak they already
reached: the release images are deleted once pushed (#1636), and the floor is
15 GB, down from 30 once C: no longer got back there (#1635).

`hack/relay_test.sh` tests the guard with a stub `df`: no session below the
minimum on any watched path and the path in the log, sessions above it, the
settings, one line an hour; and the `.vhdx` lookup with stub `reg.exe` and
`wslpath`, the `/mnt/c` fallback included.

## CPU on the cluster device

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
make gh-runner RUNNERS=3                    # build, register theos-<hostname>-1..3 once, start them
gh variable set RUNS_ON --body self-hosted  # every job goes to them
make gh-runner-status                       # whether Docker answers, their containers, whether GitHub sees them online
gh variable delete RUNS_ON                  # back to ubuntu-latest
make gh-runner-down                         # stop them all and remove them from GitHub
docker logs -f theos-gh-runner-1        # what runner 1 is doing
hack/gh-runner/gh-runner.sh install         # the keeper: starts the ones that are gone, every 2 minutes
```

- Runner *i* is the container `theos-gh-runner-i`, registered as
  `theos-<hostname>-i` with the labels `self-hosted`, `linux`, `x64` and
  `theos`, with a token from the device's `gh` login. Its registration
  lives on a volume of its own (`theos-gh-runner-i-config`), so `docker
  restart`, a reboot and a new image keep it. GitHub drops a runner that stays
  offline for 14 days; `make gh-runner` then registers it again. With
  `RUNS_ON` set and the runners down, jobs wait in the queue: nothing fails,
  and GitHub has no runner to fall back on.
- The runners run on the device's Docker. On WSL that is Docker Desktop, with
  WSL integration on for the distro. The containers (`--restart
  unless-stopped`) stop when it stops and start again with it, unless it
  comes back without them (the keeper, below). The relay watch
  notes runs queued on main for over 30 minutes in the Maintainer report
  ([testenv.md](testenv.md)). `make gh-runner-status` then says which part is down. Docker not
  answering from this distro means starting Docker Desktop. A container down
  means running `make gh-runner RUNNERS=N`. A runner GitHub sees offline while
  its container is up has its reason in `docker logs --tail 50
  theos-gh-runner-i`. It exits 1 unless every runner is online. Setting
  `RUNS_ON` back is the owner's call.
- **The keeper.** Docker Desktop on WSL loses its data disk now and then
  (2026-10-08, 10-09, 10-12, 10-15): it comes back without the containers,
  volumes and image, so `--restart unless-stopped` has nothing to restart,
  and jobs waited hours. `hack/gh-runner/gh-runner.sh install`, run once per
  device from the main checkout, installs a systemd user timer
  (`theos-gh-runner-keep.timer`; without systemd, a crontab line) that
  runs `gh-runner.sh keep` every 2 minutes. `make gh-runner RUNNERS=N` records
  N, with the names, work directories and repository it used, in
  `~/.local/state/theos/gh-runner/runners`; `keep` starts the recorded
  runners whose container is gone or not up (building the image and
  registering them again when those went too), and nothing else:
  - it never recreates a container that is up, which would kill its job,
    even when the checkout's image is newer;
  - it never changes the count, and `make gh-runner-down` removes the
    record, so the runners stay down until the next `make gh-runner`;
  - with Docker not reachable it starts nothing and logs that once: only the
    owner restarts Docker Desktop (after a data-disk failure, `wsl
    --shutdown`), and the relay watch's *Runners offline* note is the alarm;
  - it skips its turn while `make gh-runner` or `make gh-runner-down` runs.

  Its log is `~/.local/state/theos/gh-runner/keep.log`, under `$HOME`
  since `./dev` points the XDG folders into `.dev/`. It runs without `./dev
  run`, with the installer's `PATH` and the device's `gh` login. The systemd
  user manager needs the `docker` group, and `install` warns when it lacks it
  (as `hack/relay-watch.sh --install` does). A container stopped by hand with
  `docker stop` starts again; `make gh-runner-down` is the way to keep them
  down. `gh-runner.sh uninstall` removes the keeper.
- `make gh-runner RUNNERS=N` again with a smaller N removes the runners above
  N, from Docker and from GitHub; `RUNNERS` defaults to 1. A device that ran
  the single runner from before (container `theos-gh-runner`, named
  `theos-<hostname>`) has it removed and registered again as runner 1 on
  the first `make gh-runner`.
- **One Kind job per device.** *Test Chart*, *E2E Tests* and *Verify Release*
  make Kind clusters with fixed names (`chart-test`, `ci-e2e`,
  `release-<check>`) and take several GB of the device's memory. Only runner 1
  of a device carries the label `theos-kind`, and while `RUNS_ON` is
  `self-hosted` those jobs ask for it (`runs-on: ${{ vars.RUNS_ON ==
  'self-hosted' && fromJSON('["self-hosted", "theos-kind"]') || ... }}`),
  so a device runs one of them at a time and two devices run one each. A
  label, not a `concurrency` group: a group spans the repository, not a
  device, and keeps only the newest pending run, cancelling the others; and
  per-runner cluster names would still put two clusters in one device's
  memory. `hack/check-workflows.sh` (in `make check`) fails a job whose
  `runs-on` doesn't use `vars.RUNS_ON`, and a job that makes a Kind cluster
  without the label. Every job sets `timeout-minutes`, a few times its
  normal duration (Lint 30, Tests 60, Release 180, the Kind jobs 60):
  without one GitHub lets a hung job run 6 hours, and a hung Lint job on
  main held its concurrency group, so every later run on main waited behind
  it and nothing said whether main was green (#1655); the check fails a job
  without one.
- **Lint, Tests, UI and Vulnerabilities run on pull requests; Tests tests
  the newest head of main.**
  *Lint* (with its `test-hack` job, `make test-hack`), *Tests* (the unit and
  envtest suites and `make test-gitea`), *UI* and *Vulnerabilities* run on
  pushes to main and on pull requests to main, with the same path filters, so
  a pull request's own runs answer before `hack/lib/github.sh land` (#1380).
  Each job a pull request runs has a distinct `name:` (*Tests*, *Lint*,
  *Shell tests*, *UI*, *Vulnerabilities*), listed in `.github/required-checks`
  as `<workflow file> <job name>`: the checks `land` waits for and a main
  ruleset can require. A pull request whose changes miss a workflow's paths
  gets no run of it. On a pull request, the `concurrency` group is one per
  pull request with `cancel-in-progress: ${{ github.event_name ==
  'pull_request' }}`, so a new push cancels the run in progress and only the
  newest head counts. On main, Lint, UI and Vulnerabilities group by
  `${{ github.workflow }}-${{ github.ref }}` and cancel nothing, so a
  running job always finishes and GitHub cancels only older pending runs.
  Tests groups by the same ref with `cancel-in-progress: ${{
  github.event_name == 'pull_request' || github.ref == 'refs/heads/main' }}`,
  so a push to main cancels the run of the head it supersedes and only the
  newest head of main is tested: the runners are the owner's devices, and
  since yolo mode merges unchecked, main's run is what shows a red main
  (#1556). A tag's ref is its own, so no release run is dropped. The "main is
  red" watch (`gh_red` in `hack/lib/github.sh`) skips cancelled runs,
  and failed runs a self-hosted runner lost (`gh_lost`): a runner stopped
  mid-job cancels its step ("The operation was canceled.", Ginkgo prints
  "Interrupted by User"), and a job no runner took is cancelled without steps.
  Neither says anything about the code. *Issue intake*
  and *Issue closed* have no group, since each issue event must run.
  `hack/check-workflows.sh` fails a workflow on pushes to a branch without
  one of those groups, or cancelling a run on main; a workflow on pull
  requests to another branch, with other paths there or without the
  pull-request `cancel-in-progress`; Lint, UI or Tests off pull requests to
  main; Lint without its `test-hack` job; Tests off pushes to main, grouped
  by anything but the ref, or not cancelling a superseded head of main
  (#1556); and a job on pull requests without a `name:` or missing
  from `.github/required-checks`, a line there naming no such job, or two
  checks with one name.
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
  (`~/.cache/theos/gh-runner/work/i`) has the same path in the container,
  so a job's bind mounts resolve.
- Each runner takes one job at a time. They run on the device's Docker
  outside the `HEAVY_JOBS` slots: a release builds eight images and *Tests*
  runs `make test` whatever the device's sessions are doing, so pick N for the
  device's memory.
- The Go module, build and golangci-lint caches and the npm cache are one
  volume the runners share (`theos-gh-runner-cache`): Go, golangci-lint and
  npm lock what they write there. The Makefile's tools (controller-gen,
  golangci-lint, the envtest binaries, Gitea) are kept there too, in
  `~/.cache/make-bin` (`LOCALBIN`), since `actions/checkout` deletes the
  checkout's `.dev/` before each job; they install through a temporary
  directory, as when worktrees share `.dev/`. The tool cache setup-go and setup-node unpack
  into is a volume per runner (`theos-gh-runner-i-toolcache`): two runners
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
