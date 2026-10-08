# C4 Factory - AI Agent Guide

C4 Factory is a kubebuilder operator. Users describe software as a C4-style model in
custom resources (group `c4.factory.io`, version `v1alpha1`); the operator scaffolds
one Git repository per Container from pinned templates, then only reports. Read
[docs/design.md](docs/design.md) for the what and why, and
[docs/architecture.md](docs/architecture.md) for where code goes.

This file is loaded into every session, so it holds only what is true for every
task. Read the rest when the task needs it:

| When you | Read |
| --- | --- |
| change what the factory does | [docs/design.md](docs/design.md): the spec |
| add or move code | [docs/architecture.md](docs/architecture.md) and [docs/dev/structure.md](docs/dev/structure.md) |
| change API types, reconcilers or log lines | [docs/dev/api-and-controllers.md](docs/dev/api-and-controllers.md) |
| set up a device, or change `./dev` or `.vscode/` | [DEVELOPMENT.md](DEVELOPMENT.md) |
| run tests, the Kind cluster, images or a deploy | [docs/dev/testenv.md](docs/dev/testenv.md) |
| watch or drive the running factory (Crews, Tasks, Gitea) | the `factory-ops` skill |
| act as the maintainer, decide who decides, or review and clean up what the relay's agents did | [GOVERNANCE.md](GOVERNANCE.md) and the `maintainer` skill |
| plan or cut a release, or set a milestone | [ROADMAP.md](ROADMAP.md) |
| make, store, pass or test a credential, or a secret scanner fails a pull request | the `secrets` skill |
| take an issue, or continue work someone left in flight (a relay session, or the user said to) | `hack/lib/github.sh take N` and the `handoff` skill: claims, your agent name, the branch, the newest handoff |
| work on a plan step | its plan in [docs/plans/](docs/plans/); finished plans are in [docs/plans/done/](docs/plans/done/) |

## Project Architecture

Business rules live in `internal/model` and `internal/scaffold` and must not
import Kubernetes, controller-runtime, `net/http` or `api/`; `make lint` enforces
the layering. Run `make check` before finishing a change; a tree that already
passed on this device answers from the cache, which counts as a pass
(`FORCE=1 make check` reruns it).

## Critical Rules

### Never Edit These (Auto-Generated)
- `config/crd/bases/*.yaml` - from `make manifests`
- `config/rbac/role.yaml` - from `make manifests`
- `**/zz_generated.*.go` - from `make generate`
- `PROJECT` - from `kubebuilder [OPTIONS]`

### Never Remove Scaffold Markers
Do NOT delete `// +kubebuilder:scaffold:*` comments. The CLI injects code at these markers.

### Keep Project Structure
Do not move files around. The CLI expects files in specific locations, and the
layering rules above depend on the package paths.

### Always Use CLI Commands
Always use `kubebuilder create api` to scaffold new kinds. Do NOT create API or
controller files manually.

### The Design Doc Is the Spec
Behaviour comes from [docs/design.md](docs/design.md). If code and design disagree,
fix the code or propose a change to the design; don't silently diverge. Settled rules
worth repeating:
- **The CRDs are the truth.** The UI and the Session client enforce nothing; rules
  live in CRD schemas (CEL), the admission policy and the operator.
- **Scaffold once.** A Container is scaffolded at creation and never scaffolded
  again; only its Standard's files follow a bump of the Standard's pinned `ref`,
  through a pull request the operator opens and merges. Relationships added
  later show `Applied=False`, reason `AddedAfterScaffold`.
- **Every file belongs to exactly one layer.** Two layers writing the same path fail
  the Container, with the path in the message.
- **Templates are pinned.** A template `ref` is a tag or commit, never a branch.
- **Children name their parent in `spec`;** the operator sets the ownerReference so
  deletion cascades.

### Agents Never Touch a Cluster
Go tests are the gate: `make check` for every change, `make test-gitea` and
`make seed-local` for the Gitea code and the seed. A Kind cluster exists only
when a human asks to run tests in it. Agents never create, lease or deploy to
one, and never run `make e2e`; `hack/lib/github.sh cluster acquire` refuses
without `C4_HUMAN_CLUSTER=1`, which only a human sets in their own shell. The
human runs `make e2e` against a Kind cluster of the device's pool, never a
real dev or prod cluster. What only a real node would catch is not tested
before release.

## After Making Changes

**After editing `*_types.go` or markers:**
```
make manifests  # Regenerate CRDs/RBAC from markers
make generate   # Regenerate DeepCopy methods
```

**After editing `*.go` files:**
```
make lint-fix   # Auto-fix code style
make test       # Run unit and envtest tests
```

**Before finishing any change:**
```
make check      # manifests, generate, fmt, vet, lint and test in one go
```

## How to Work

- **Plan before building.** For work that spans more than one package or issue,
  write a short plan in `docs/plans/` first: goal, steps, and how you'll know it's done.
- **Designing is our job.** Whoever files an issue says what they want; how the
  factory does it is ours to decide. When the design doesn't settle something,
  make the choice you think works, write it and why in a plan in `docs/plans/`
  (or the change to `docs/design.md`), and go on. Never ask the reporter how to
  build it, and never hand the user a list of open design questions. Ask the
  reporter only to elaborate, when what they want is unclear. When you're
  unsure which way to go, ask the user with the choice you'd make and why, so
  they can steer.
- **Test the rules where they live,** at the lowest tier that can see them:
  1. `internal/model` and `internal/scaffold` table tests, the Agent
     Sandbox and pod specs the controllers build, the fence included, and
     the generated RBAC role naming only real CRD resources and
     subresources (`make test`).
  2. `make validate-yaml`: samples, fixtures, kustomize builds and the chart,
     offline, against the CRDs; the valid YAML passes their create-time CEL
     rules.
  3. `make test-gitea` and `make seed-local`: a local Gitea, no Kubernetes.
  4. envtest (`make test`): reconcilers, status, the admission policy, and
     every CEL rule's failing case, create-time ones included, the reaction
     to faked Sandbox and pod status. The reconcilers run under the
     generated RBAC role, so a call a spec makes that the role doesn't
     grant fails that spec; a call no spec makes goes unchecked.
  5. Kind and `make e2e`, only when a human asks: the whole loop on stand-in
     agents. No agent runs it, and nothing waits for it.

  `make check` runs tiers 1, 2 and 4 for every change. Node and kubelet
  behaviour and the Gitea Actions runner itself are not tested before release.
- **Small pull requests to main.** One branch per issue or plan step, the one
  `take` names (`<type>/<N>-<slug>`, from an up-to-date main), pushed after
  every commit; open a pull request to main whose body
  says `Closes #N` for the GitHub issue it finishes (never `Refs #N`, in
  the body or a commit: that is only a plan's), and merge it with a merge
  commit once `make check` passes (`hack/lib/github.sh merge PR`). No long-lived or stacked branches: each plan
  step lands on its own and leaves main working, with new behaviour off until
  the step that turns it on. When an issue is split into a plan and steps,
  the plan's pull request says `Refs #N`, never `Closes #N`, and no close, fix
  or resolve word next to `#N` anywhere in its body or commits, even in prose
  ("its PR closes #N" closes N; `merge` refuses it); the issue stays
  open, `status/waiting` (waiting on its steps, not on an agent), its body a
  `Depends on #S` line per step so `pick` skips it, and it closes itself when
  its last step closes (`hack/lib/github.sh parents`). Never close it by
  hand: `hack/close-guard.sh` denies a pull request or commit, and
  `hack/lib/github.sh close` an issue, that would close it while a step is open.
- **Main keeps itself current.** `hack/pull.sh` fast-forwards local main when
  that is safe and never touches a branch or uncommitted work. VS Code runs it
  every 5 minutes, Claude Code at session start, the relay before each session;
  run it yourself before you branch or pick an issue.
- **Show, don't claim.** When a plan is done, save evidence in
  `docs/screenshots/`. Test output is the evidence; no plan asks an agent for
  Kind evidence (`kubectl get` output, screenshots of a running factory).
- **Report honestly.** If a check fails or a step is skipped, say so in the commit or
  PR with the output.

## Working in Sessions

Quality drops as a session's context grows, long before the window is full.
Treat about 140k tokens as the limit, and keep each session to one piece of work.

- **Work on the factory is GitHub issues** on `JairJosafath/c4factory`, one per
  piece of work, labeled `status/*`, `priority/urgent`, `kind/*`;
  `hack/lib/github.sh` reads and writes them. No issue needs a cluster:
  every one is verified by Go tests, so a device without Kind
  (`./dev setup --lite`, `C4_CLUSTER=none`) works every ready issue.
- **Every issue carries `source/dev`** (developing the factory) or `source/user`
  with `user/<name>` (reported from a device running the Helm release).
- **Issues are the relay's.** Relay agents pick up every ready issue. A session
  the user started picks or takes one only when the user says to work it in
  that session; an issue it files for the user is `status/ready`, for the
  relay.
- **One issue, plan step or fix per session.** Start the next one fresh.
- **Agents work in parallel.** Take an issue with `hack/lib/github.sh take N`
  before working it; it names you (`otter-23`). Sign commits with an
  `Agent: otter-23` trailer and say it in the pull request and handoff. One
  agent per checkout: a second one on the device uses a git worktree. If the
  heartbeat hook says your claim was taken over, push to `<branch>-<name>`,
  comment that on the issue and stop. A step that waits on another issue says
  `Depends on #M` in its body. The `handoff` skill has the details.
- **Read on demand.** Open the docs in the table above when the task needs them,
  not up front. Read files in parts, not whole.
- **Delegate broad searches** to a subagent; it returns the conclusion, not the
  file dumps. Keep large outputs out of the context: filter with `jq`, `grep`,
  `tail`, `--tail=N`, and summary flags.
- **Hand off before the context gets big**, or when work stops mid-way: the
  `handoff` skill posts the in-flight state as a comment on the issue, and the
  next session starts by reading the newest one. Close the issue when its work
  lands.
- **Hand off between tasks, never inside one.** Finish the task in flight (its
  implementation and review) first; after a task, past about 100k tokens, hand
  off instead of starting the next.
- **Overnight, `hack/relay.sh [--workers N]`** runs headless sessions, each on
  the in-progress or next ready issue; worker k works in the worktree
  `.claude/worktrees/relay-k` and logs to `.relay/relay-k.log`.
  `hack/relay-watch.sh [--workers N]` keeps it ready, starting it within a
  minute of an issue becoming ready. When nothing is ready, it starts a triage
  session (`hack/relay.sh --triage`) that readies the sound `status/proposed`
  issues and comments on the others what they lack; `status/needs-human`
  stays the user's. Every 2 hours when anything changed (`WATCH_MAINTAIN`),
  busy or idle, and within a minute of an issue reaching
  `status/needs-maintainer`, it starts the maintainer (`hack/relay.sh
  --maintain`, the `maintainer` skill), which decides for the owner and keeps
  the pinned *Maintainer report* issue (`hack/lib/github.sh report`).
  **Agents escalate to the maintainer** (`hack/lib/github.sh status N
  needs-maintainer` and a comment saying what is needed); only the maintainer
  sets `status/needs-human`. A usage limit or a failed login pauses the relay until it
  resets (`.relay/paused-until`); an issue whose sessions end three times in a
  row without a handoff or a commit goes to `status/needs-maintainer`. A session
  whose transcript stops changing for 45 minutes (`RELAY_IDLE`) is ended and
  counts as one of those.
- **On a device running many sessions, memory is the limit.** A session at rest is cheap;
  heavy jobs (`make check`, `test`, `lint`, `docker-build`, `deploy`, `e2e`) are
  not, so they queue device-wide for `HEAVY_JOBS` slots (default 2): a `make`
  that waits for one prints who holds them, and that wait is not a hang;
  [docs/dev/testenv.md](docs/dev/testenv.md) has the numbers.
- **Standing facts belong here or in docs,** in-flight state in a handoff. Ask:
  is it true next month?
