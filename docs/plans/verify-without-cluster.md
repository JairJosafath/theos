# Most work verifies without a cluster

**Goal:** Every rule is verified at the lowest tier that can see it, so most
factory work verifies on any device, lite ones included, with no Kind cluster
and no lease. `needs/cluster` is left for what needs a real Kubernetes node:
Agent Sandbox pods, node or kubelet behaviour, RBAC as deployed, and Gitea
Actions runners. Issue #142.

**Why:** The cluster has become the bottleneck for the work around it:
- **Issues:** 9 of 10 open issues carry `needs/cluster`, and a lite device
  (`C4_CLUSTER=none`) has about one issue it can take.
- **The cluster device:** it leases from a pool of two Kind clusters at
  1.1–4.5 GiB each, with waits of up to 30 minutes, and its heavy jobs queue
  for two slots.
- **What the label really means:** of 14 cluster-labelled issues sampled, only
  4 needed a real kube (#112, #111, #70, #123). The rest were Gitea work, YAML
  that offline tools can check, or live evidence for logic envtest already
  covers.
- **Gaps below Kind:**
  - Nothing checks the samples, the e2e fixtures or the kustomize builds
    against the CRDs.
  - The chart is rendered only after the merge.
  - Six of the ten templates are rendered by no test.
  - Several state machines are written in the controllers, so only envtest
    reaches them.

## Shape

The tiers, lowest first. A rule is tested at the first tier that can see it.

| Tier | Runs | Verifies |
| --- | --- | --- |
| Table tests | `make test`, no API server | `internal/model` decisions, `internal/scaffold` rendering of every template |
| Offline YAML | `make validate-yaml`, part of `make check` | samples, e2e fixtures, kustomize builds, the chart, rendered manifests: against the built-in schemas and our CRDs, create-time CEL rules included |
| Local Gitea | `make test-gitea`, `make seed-local` | the Gitea API's real behaviour, the seed, template tags |
| envtest | `make test` | wiring: reconcilers, status, ownerReferences, CEL transition rules, the admission policy |
| Kind | `make e2e`, factory-ops | Agent Sandbox pods and the agent fence, node and kubelet behaviour, RBAC as deployed, Gitea Actions runners |

- **Offline YAML** uses `kubectl-validate`, which evaluates CRD CEL rules
  without an API server. It checks single objects, so rules on `oldSelf` stay
  in envtest. `kubectl-validate` and `helm` download into `bin/` like the other
  Makefile tools. A lite device needs no new system tool.
- **Local Gitea** is the same static binary and version as the cluster's,
  run per checkout from `.gitea-local/` with SQLite, on a port of its own.
  There's no lease, and its credentials follow the `secrets` skill.
- **Decisions move to `internal/model`.** The controller gathers facts, calls a
  pure function and does what it returns, as `model.NextTaskStep` already does.
  Behaviour doesn't change, and the envtest specs pass unchanged.
- **envtest stays.** It is binaries, not a cluster, and it already runs on lite
  devices. The fake client would skip CEL, the admission policy and the status
  semantics, which is where "the CRDs are the truth" lives.

## Steps

Each step is its own issue and pull request, and leaves main working. Every
step waits for this plan (`Depends on #142`), and a step that builds on
another names it too. None of them needs the cluster.

1. **Plan and design** (#142). This plan, and `docs/design.md` gets *Test
   tiers*.
2. **Offline YAML in `make check`** (#143). `make validate-yaml` with
   `kubectl-validate` and `helm` in `bin/`, run in pull request CI.
3. **Every CEL rule tested** (#149, after #143). Failing fixtures for the
   create-time rules, envtest specs for the transition rules.
4. **Every template renders** (#144). A table test in `internal/scaffold` over
   `templates/`, with a guard for new folders.
5. **Local Gitea** (#145). `make gitea-local`. `make test-gitea` runs without
   Kubernetes and in pull request CI.
6. **Local seed** (#150, after #145). `seed.sh` writes credentials to a file;
   `make seed-local`.
7. **Standard update in the model** (#146). The pull request state machine of
   `standardUpdate`.
8. **Task transitions in the model** (#147). `apply`, `merge`, `propose`,
   `startRun`.
9. **Smaller rules in the model** (#148). Scaffold recovery, Relationship
   `Applied`, free Crew slots.
10. **The label and the docs** (#151, after 2–6). `needs/cluster` means a real
    node, the tiers are in AGENTS.md and `docs/dev/testenv.md`, the open
    issues are re-triaged, and this plan moves to `done/`.

Steps 2, 4, 5, 7, 8 and 9 are independent and can run in parallel.

**Later, not designed yet** (#152, proposed): a local factory mode. It would
run the Crew and Task loop on envtest, the local Gitea and `c4agent` as a
subprocess, for live evidence without Kind. It can't prove the sandbox fence,
so Kind stays for that.

## Done when

- `make check` validates the samples, fixtures, kustomize builds and the chart
  offline, and pull request CI runs it.
- Every CEL rule in `config/crd/bases` has a fixture or an envtest spec.
- `make test` renders every template.
- `make test-gitea` and `make seed-local` run on a device with
  `C4_CLUSTER=none`.
- The decisions listed in steps 7–9 live in `internal/model` with table tests.
- `needs/cluster` is on an open issue only when it needs a real node.
