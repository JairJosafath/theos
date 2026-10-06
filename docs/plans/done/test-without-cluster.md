# Test without a cluster

**Goal:** Go tests are the gate. `make check` (table tests, offline YAML,
envtest) and the component tests (`make test-gitea`, `make seed-local`) verify
every change; a Kind cluster exists only when a human asks to run tests in it.
Agents never create, lease or deploy to one, and `make e2e` is a small suite on
stand-in agents that needs no model keys. Issue #344.

**Why:** The owner wants to move fast and stop paying for clusters. The pool
of Kind clusters costs memory, lease waits and sessions spent deploying, for
evidence the lower tiers mostly already give
([verify-without-cluster.md](verify-without-cluster.md) moved most rules
below Kind). What only a real node would catch is not tested before release:
if it breaks, customers file an issue. That is the trade the owner chose.

## Shape

The tiers, lowest first. A rule is tested at the first tier that can see it.

| Tier | Runs | Gate | Verifies |
| --- | --- | --- | --- |
| Table tests | `make test` | every change | `internal/model`, `internal/scaffold`, the Sandbox and pod specs the controllers build |
| Offline YAML | `make validate-yaml` | every change | samples, fixtures, kustomize builds, the chart (`helm lint`/`template`) |
| envtest | `make test` | every change | reconcilers, status, CEL, the admission policy, the reaction to faked Sandbox and pod status, RBAC as generated |
| Components | `make test-gitea`, `make seed-local` | changes to the Gitea code or the seed | the Gitea API, the seed, template tags, whatever a local Gitea shows of Actions |
| Kind | `make e2e`, factory-ops | only when a human asks | the whole loop on stand-in agents |

What the old Kind tier covered goes here:

- **Agent Sandbox pods:** the Sandbox and pod specs (`buildSandbox`, the
  fence's NetworkPolicy, no ServiceAccount token) in Go tests, and the Task
  controller's reaction to Sandbox and pod status in envtest with that status
  faked, as `finishSandbox` in `task_controller_test.go` already does.
- **RBAC as deployed:** envtest runs the reconcilers with a client that
  impersonates a user bound to `config/rbac/role.yaml`, so a call the role
  doesn't grant fails a spec. That checks the generated role against what the
  controllers actually call, not against the markers that generated it.
- **Gitea Actions runners:** whatever `make test-gitea` sees (workflow files
  present, commit statuses read back). The runner itself is not tested.
- **Node and kubelet behaviour:** not tested before release.

**The human opt-in.** `hack/lib/github.sh cluster acquire` refuses unless
`C4_HUMAN_CLUSTER=1` is set. The relay never sets it and no prompt or skill
tells an agent to; a human sets it in their own shell. `make e2e`, `make
cluster` and the other targets stay as they are: a human runs them.

**`needs/cluster` goes.** `pick` and `take` ignore it, it leaves the label set
in `hack/lib/github.sh labels`, and it is removed from open issues. A lite
device (`C4_CLUSTER=none`) then picks every ready issue.

**The e2e suite.** One hello-world static page and four specs:

1. A Container is scaffolded.
2. A Brief plans it.
3. A Task goes two rounds to a merge.
4. A Standard bump reaches main.

The app is a new Framework template, `hello-html`: an `index.html` that says
"hello, world", and a `check` that greps it. `hello` (a shell script) stays,
since existing catalogs pin it. Every Agent in the fixture runs on the
stand-in. The suite fails in its `BeforeSuite` if any Agent's harness is not
`stand-in`, or if any Agent references a model credential Secret. The guard is
a plain function over the fixture's objects, with a unit test (no build tag)
that feeds it a `claude-code` Agent and one with a credential Secret and sees
it fail. The milestone 1–5 specs in `e2e_test.go` (the webshop and
python-fastapi sample) move down to envtest or `test-gitea`, or are deleted
where a lower tier already covers them; so are "fences agent sandboxes" and
"runs CI on main".

**CI starts no Kind on its own.** `test-chart.yml` and `verify-release.yml`
run on `workflow_dispatch` only, as `test-e2e.yml` already does. The chart is
checked offline by `make validate-yaml` (which runs `helm-lint`) in every
`make check`.

## Steps

Each step is its own issue and pull request and leaves main working; a step
that builds on another names it (`Depends on #M`). None of them needs a
cluster. #344 closes with this plan; step 9 carries the *Done when* below.

1. **Plan and design** (#344). This plan, and *Test tiers* in
   `docs/design.md`.
2. **The opt-in and the label** (#348). `cluster acquire` refuses without
   `C4_HUMAN_CLUSTER=1`; `pick` and `take` ignore `needs/cluster`; the label
   leaves `labels` and the open issues. `hack/lib/github_test.sh` shows the
   refusal and `pick` handing a former `needs/cluster` issue to a device with
   `C4_CLUSTER=none`.
3. **No prompt tells an agent to use a cluster** (#353, after 2). The relay's
   worker prompt, the handoff, factory-ops and maintainer skills,
   `hack/heavy-guard.sh`, `hack/doctor.sh` and `hack/up.sh`. `grep -rn
   "cluster acquire" hack/relay.sh .claude/skills` finds only the human path.
4. **Sandbox and RBAC below Kind** (#349). The fence and pod-spec checks the
   Go tests lack, and the RBAC envtest above.
5. **The `hello-html` template** (#350). Template, tag in `versions.txt`, the
   seed and the render test in `internal/scaffold`.
6. **The milestone specs move down** (#351). Each spec in `e2e_test.go` gets a
   lower-tier test or is named in the pull request as already covered, then
   `e2e_test.go` goes.
7. **The e2e suite on stand-ins** (#354, after 5 and 6). The fixture on
   `hello-html`, the four specs, the stand-in guard and its unit test. `go vet
   -tags=e2e ./test/e2e/...` passes.
8. **CI starts no Kind** (#352). `test-chart.yml` and `verify-release.yml` on
   `workflow_dispatch` only.
9. **The docs** (#355, after 2–8). AGENTS.md (the e2e section, "Test the rules
   where they live", "Show, don't claim", the cluster bullets under "Working
   in Sessions"), `docs/dev/testenv.md`, `docs/design.md` (*Test environment*),
   DEVELOPMENT.md, ROADMAP.md (no milestone's *Done when* says "on Kind"), and
   this plan moves to `done/`.

Steps 2, 4, 5, 6 and 8 are independent and can run in parallel.

## Done when

- Every step has landed.
- `hack/lib/github_test.sh` shows `cluster acquire` refusing without the
  opt-in, and `pick` handing out a former `needs/cluster` issue on a device
  without a cluster.
- `grep -rn "cluster acquire" hack/relay.sh .claude/skills` finds only the
  human path.
- `go vet -tags=e2e ./test/e2e/...` passes; the suite has only the
  `hello-html` app and the four specs, and a test shows the stand-in guard
  failing on a non-stand-in Agent. A human run of `make e2e` is not required.
- No workflow in `.github/workflows` starts Kind except on
  `workflow_dispatch`.
- The docs listed in step 9 state the new tiers, and `make check` passes.
