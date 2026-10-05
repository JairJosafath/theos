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
| take an issue, or continue work someone left in flight | `hack/lib/github.sh take N` and the `handoff` skill: claims, your agent name, the branch, the newest handoff |
| evaluate how the factory runs | `hack/evaluate.sh`, the `factory-evaluator` skill |
| work on a milestone | its plan in [docs/plans/](docs/plans/) and its results file |

## Project Architecture

Business rules live in `internal/model` and `internal/scaffold` and must not
import Kubernetes, controller-runtime, `net/http` or `api/`; `make lint` enforces
the layering. Run `make check` before finishing a change.

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

### E2E Tests Require an Isolated Kind Cluster
The e2e tests run against the dedicated Kind cluster created by `make cluster`
(named `c4factory`), never against a real dev or prod cluster.

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

- **Plan before building.** For work that spans more than one package or milestone,
  write a short plan in `docs/plans/` first: goal, steps, and how you'll know it's done.
- **One milestone at a time.** The six milestones in the design build on each other.
  Finish one, including its "Done when" check in the Kind cluster, before starting the next.
- **Test the rules where they live.** Table tests for `internal/model` and
  `internal/scaffold`, envtest for controllers, `make e2e` for the whole loop.
- **Small pull requests to main.** One branch per issue or plan step, the one
  `take` names (`<type>/<N>-<slug>`, from an up-to-date main), pushed after
  every commit; open a pull request to main whose body
  says `Closes #N` for the GitHub issue it finishes, and merge it with a merge
  commit once its checks are green. No long-lived or stacked branches: each plan
  step lands on its own and leaves main working, with new behaviour off until
  the step that turns it on.
- **Main keeps itself current.** `hack/pull.sh` fast-forwards local main when
  that is safe and never touches a branch or uncommitted work. VS Code runs it
  every 5 minutes, Claude Code at session start, the relay before each session;
  run it yourself before you branch or pick an issue.
- **Show, don't claim.** When a milestone is done, save evidence (test output,
  `kubectl get` output, UI or Gitea screenshots) in `docs/screenshots/`.
- **Report honestly.** If a check fails or a step is skipped, say so in the commit or
  PR with the output.

## Working in Sessions

Quality drops as a session's context grows, long before the window is full.
Treat about 140k tokens as the limit, and keep each session to one piece of work.

- **Work on the factory is GitHub issues** on `JairJosafath/c4factory`, one per
  piece of work, labeled `status/*`, `priority/urgent`, `kind/*`;
  `hack/lib/github.sh` reads and writes them. Product work stays in Gitea with
  the Crews. Issues that need the Kind cluster carry `needs/cluster`; a device
  without one sets `C4_CLUSTER=none` (`./dev setup --lite`) and skips them.
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
  minute of an issue becoming ready.
- **On the cluster device, memory is the limit.** Check `free -g` before a
  heavy job (`make check`, image builds, `make deploy`), never run two at once,
  and build a devcontainer (`devcontainer up`) only on a device without the cluster;
  [docs/dev/testenv.md](docs/dev/testenv.md) has the numbers.
- **Standing facts belong here or in docs,** in-flight state in a handoff. Ask:
  is it true next month?
