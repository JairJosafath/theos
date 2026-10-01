# Watching the factory

The factory now runs whole Task loops on its own, but the only way to see how it
is doing is `kubectl` and logs. This plan adds what helps through the factory's
life: starting and stopping it, checking it, and watching its processes, from the
read-only UI that already exists. The focus is the factory, not the apps it
builds, and every part must stay light: the laptop it runs on overheats.

## Goal

Someone opens one page and sees, without kubectl:

- **Is the factory healthy?** Each component (operator, Gitea, CI runner, Agent
  Sandbox controller, UI, metrics) with ready state, restarts, CPU and memory, and
  the node's CPU and memory.
- **What is it doing?** Every Task across projects, on a board by phase, with
  round, checks, verdict, time in phase, and the agent slots in use and queued.
- **Where does the time go?** Each Task's timeline (waiting, implementing, CI,
  review, per round) and totals across Tasks: merged, failed, rounds to merge,
  lead time, and the share of time spent per phase.
- **What needs attention?** Failed Tasks and Containers, Tasks stuck in a phase,
  Stale Sessions, components not ready or restarting.

And from a terminal: `make up` brings a stopped factory back, `make down` stops
it to save the machine, `make doctor` says what is wrong.

## Decisions

- **No Prometheus or Grafana.** They would cost more than the rest of the factory
  together. The numbers that matter for the process live in the resources
  (a Task's timeline in its status); live CPU and memory come from metrics-server
  (about 30 MiB), which `kubectl top` uses too.
- **A Task records its own timeline.** `status.history` gets an entry each time
  the phase, the round or the Progressing reason changes: when, phase, round,
  reason and a short message. It is capped (64 entries), lives as long as the
  Task, and needs no other store. Kubernetes Events expire after an hour, too
  soon for "where did the time go".
- **The UI stays read-only.** Health is one new GET endpoint, `/factory/health`,
  computed by `server.mjs`: it reads the component pods and metrics and returns
  only names, states, restarts and usage, never pod specs (which carry
  environment and arguments). Its RBAC is a Role per component namespace for
  pods and a ClusterRole for `metrics.k8s.io`; the browser still can't name any
  path outside `c4.factory.io`.
- **Slots come from the operator's arguments.** The health endpoint reads
  `--max-agent-sandboxes` from the operator pod's arguments, server-side.
- **Polling only while looking.** The Factory view polls health every 10 seconds
  while it is open; Tasks and Agents come through the existing watches.
- **The doctor is a shell script.** It must work when the cluster or the UI is
  down, which is exactly when it is needed.

## Steps

Each step is one or more small commits with tests.

1. **Task timeline.** `model.RecordTransition` (pure, table-tested) appends an
   entry when phase, round or reason changes and keeps the newest 64. The Task
   controller calls it in `writeStatus`. `status.history` in the CRD; envtest
   checks a Task's history through a round.
2. **Factory metrics in the test environment.** `make metrics-server`: pinned
   metrics-server with `--kubelet-insecure-tls` for Kind; part of `make e2e`.
3. **Health endpoint.** `/factory/health` in `server.mjs`: components from a
   built-in list (overridable with `FACTORY_COMPONENTS`), pod readiness and
   restarts, usage from `metrics.k8s.io` when present, node capacity and usage,
   agent slots. Node tests against a fake API. RBAC in `config/ui`.
4. **Factory view.** A third view next to System context and Containers:
   health strip, usage, slots and queue, Task board, Task panel with timeline,
   stats and attention list. Pure functions in `src/model/factory.ts` with
   vitest tests; the demo fixture gains Tasks, Agents and health.
5. **Lifecycle targets.** `make up` (Docker daemon, Kind container, API, restart
   pods stuck in back-off after a restart, wait), `make down`, `make doctor`.
6. **Footprint.** Measure the idle and busy factory with the new view, set
   requests and limits where missing, record the numbers.
7. **Docs and evidence.** design.md (a v3 section), architecture.md, the UI
   README, screenshots of the Factory view in the Kind cluster.

## Done when

- `make check` and `make ui-test` pass.
- In the Kind cluster, `make down && make up` brings the factory back without
  manual steps, and `make doctor` reports every component healthy.
- The Factory view, deployed with `make ui`, shows the toolbox Tasks with their
  timelines (Tasks that ran before step 1 have no history and say so), live
  component health and usage, and a screenshot is in `docs/screenshots/`.
- A stand-in Task run after the change shows its full timeline.

## Outcome (1 October 2026)

Done, with every "Done when" checked in Kind; see [results-v3.md](../results-v3.md).
Watching the factory found its worst problem on the first day: Flutter checks
writing to the disk etcd uses stalled the API server for up to 24 s. CI and
sandbox work now happen in memory. Added along the way, beyond the steps above:
the last hour of health samples (the plan had only live figures, which could not
show the stalls), retries for clones and pushes, and fixes for Agent Sandbox's
leader election and Gitea's liveness check. Step 6 set no new requests or
limits: the problem was I/O, not CPU or memory.
