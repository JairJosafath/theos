# Monitoring the factory

The manager serves the factory's own numbers on its `/metrics` endpoint, next
to controller-runtime's (#59). They are the numbers the UI's *How well it
works* shows, for the tools a cluster already has: Prometheus scrapes them,
Grafana draws them, Alertmanager wakes someone.
[design.md](design.md#measuring-the-factory) has the design.

## Turn it on

The chart serves `/metrics` by default (`metrics.enabled`). With
prometheus-operator in the cluster, `prometheus.enabled=true` adds the
ServiceMonitor:

```bash
helm upgrade theos oci://ghcr.io/jairjosafath/theos/charts/theos --reuse-values --set prometheus.enabled=true
```

Without the operator, point a scrape job at the `…-controller-manager-metrics-service`
Service's port 8443 over HTTPS, with a ServiceAccount token that may `get`
the non-resource URL `/metrics`.

## What is served

Every series is computed from the Tasks that exist, at the scrape. Nothing is
counted when something happens, so a restarted manager counts nothing twice
and needs no state. The other side of that: they are gauges, not counters. A
value falls when Tasks are deleted, and a Task's runs and steps beyond its
newest 64 are not in the run and phase series (`theos_tasks_cost_usd` still
has their cost). Use `delta()` with `clamp_min(…, 0)` for "how much in a
day", as [the example alerts](monitoring/alerts.yaml) do, never `rate()`.

| Series | Labels | What it is |
| --- | --- | --- |
| `theos_tasks` | `namespace`, `container`, `kind`, `phase` | Tasks, by where they are |
| `theos_tasks_merged` | `namespace`, `container`, `kind`, `code` | merged Tasks by who wrote the code: `factory` (no commit by a person), `person` or `unknown` (the Git host wasn't read) |
| `theos_tasks_cost_usd` | `namespace`, `container`, `kind` | what the Tasks' agent runs cost, as the agents reported it |
| `theos_task_seconds` (histogram) | `namespace`, `kind`, `outcome` | a finished Task's time from its first step to its last |
| `theos_task_phase_seconds` (histogram) | `namespace`, `kind`, `phase` | each stay in a phase that ended |
| `theos_task_rounds` (histogram) | `namespace`, `kind`, `outcome` | implement rounds a finished Task took |
| `theos_agent_runs` | `namespace`, `agent`, `harness`, `model`, `role` | ended agent runs |
| `theos_agent_runs_cost_usd` | the same | what those runs cost |
| `theos_agent_runs_without_cost` | the same | runs whose agent reported no cost, left out of the cost |
| `theos_agent_runs_tokens` | the same, and `type` | tokens: `input`, `output`, `cache_read`, `cache_write` |

`namespace` is the Project. `kind` is the Task's (`Implement`, `Triage`,
`Spec`, `Plan`, `Score`), or `Benchmark` for a Benchmark's Tasks, which never
merge and would bend every rate if they counted as work. A label a Task
doesn't have reads `none`. No Task name and no issue number is a label, so
the number of series grows with Containers and Agents, not with work done.

The number most people ask for first, the share of merged changes with no
code by a person:

```promql
sum(theos_tasks_merged{kind="Implement",code="factory"})
  / sum(theos_tasks_merged{kind="Implement",code=~"factory|person"})
```

## A dashboard and alerts

- [monitoring/theos-dashboard.json](monitoring/theos-dashboard.json): import
  it in Grafana (*Dashboards → New → Import*) and pick the Prometheus data
  source. It shows the share merged with no person's code, cost per merged
  Task, what waits for a person, Tasks in flight, time per phase, cost by
  Agent and model, tokens and rounds, for one Project or all.
- [monitoring/alerts.yaml](monitoring/alerts.yaml): a PrometheusRule with
  four examples: a day's cost over a budget, more Tasks failing than
  merging, a spec that waits a day for a person, and an Agent whose runs
  report no cost. Set the numbers to your own.

Neither is installed by the chart yet.
