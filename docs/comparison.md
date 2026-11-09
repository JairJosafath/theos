# theos next to Warp's software factory

Last checked: 2026-11-09 (#65). Recheck at each minor release
([ROADMAP.md](../ROADMAP.md)); a cell about Warp is only what Warp's own
pages, or the coverage linked under [Sources](#sources), said on that date.

## What a software factory is

The term now means an automated development loop: agents carry a change
through triage, spec, implementation, review, verification, shipping and
monitoring, and a person steps in only where a person should decide. Warp's
chief executive describes the path there as "agents in the cloud, then agents
in the cloud on a timer, then a full loop".

theos runs that loop, and one stage before it: it builds the repositories
themselves from a model of the software and pinned templates, and keeps them
current. [README.md](../README.md#the-loop) draws the loop,
[design.md](design.md) is the specification.

## The short answer

- **Pick theos** when the code, the model credentials and the agents must
  stay on machines you run, when you want to read and change every part of
  the factory, or when you are starting new software and want it scaffolded
  to a standard and kept there.
- **Pick Warp** when your code is on GitHub, you want a first agent pull
  request this afternoon without running a cluster, and a hosted service is
  fine.

## Side by side

| | theos | Warp (Oz, `oz-for-oss`, Warp Factories) |
| --- | --- | --- |
| What it is | a Kubernetes operator, a UI and a Helm chart you install | a hosted agent platform (Oz), GitHub Actions workflows that call it (`oz-for-oss`), and a managed factory on top (Warp Factories) |
| Source | all of it in this repository, under the Apache License 2.0 | the Warp client is open source; the factory's workflows and skills are public; Oz and Warp Factories are a service |
| Availability | install the release | Oz: all plans. Warp Factories: closed early access, on request |
| Where agents run | in sandboxes on your cluster ([Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox)) | in Warp's cloud environments; the workflow steps on GitHub's runners |
| Where the model key lives | in a Secret only a proxy reads; it never enters a sandbox | a `WARP_API_KEY` secret in the repository; Warp reports zero-data-retention options and bring-your-own hosting for Factories |
| Git host | the Gitea the chart installs. **GitHub: not yet (#57)** | GitHub |
| Loop: triage, spec, implement, review | yes, each an Agent a Crew names | yes, each a label-triggered job |
| Loop: verify | a verifier Agent runs the change against each acceptance criterion before the merge | listed as a stage of Warp Factories |
| Loop: ship | releases, and deploys through GitHub, CloudFormation, Argo CD or Flux, with your approval | not described in the public workflows |
| Loop: monitor | a failed or degraded deploy files a bug. **Alerts from a running app: not yet (#64)** | listed as a stage of Warp Factories |
| Who merges | the factory, once CI, the reviewer and the verifier pass; a person approves specs and deploys. **Rules for which changes wait for a person: not yet (#62)** | a person merges each pull request in the documented workflow; setup asks where people enter, such as high-risk changes |
| New software | describe a C4-style model or a Brief in plain words; the operator scaffolds each repository from pinned templates, with CI, a devcontainer and agent guides | starts from a repository that exists |
| Existing software | `spec.adopt` takes over a repository. **A guided path: not yet (#60)** | built for it |
| Keeping repositories to a standard | Standards: a version bump arrives in every Container as a pull request the operator opens and merges | skills shared across repositories |
| Agent harness | Claude Code, and a scripted stand-in for demos and tests. **Others: not yet (#58)** | Warp Agent, Claude Code, Codex |
| Models | Claude's. **Others and self-hosted: not yet (#58)** | any, per Warp, open-weight ones included |
| The fence | no service account token and no model key in the sandbox, egress limited by NetworkPolicy, protected paths the agent's credential can't change, issue text handled as data | isolated cloud sandboxes |
| Requests come from | Gitea issues, GitHub issues for a Brief, a Matrix room. **Slack, Linear, Jira: not yet (#61)** | GitHub, Linear, Jira, Slack, Teams |
| Chat | Matrix: a room per kind of work, a thread per decision | Slack and Teams |
| Work on a timer | **not yet (#63)** | yes |
| Numbers | the UI's *How well it works*: merged, share with no person's code, time per stage, cost per merged Task by size, Agent and model; the same as Prometheus series, with a Grafana dashboard and example alerts ([monitoring.md](monitoring.md)) | cost, throughput and code quality in the factory app |
| Comparing configurations | Benchmarks run the same goals under two to four Agent setups and report each rate with its 95% interval; Scorers judge finished work against criteria you write | evaluation and benchmarking tools in Warp Factories |
| First run | a cluster, a Helm install and a long guide. **One command: not yet (#53)** | a workflow file and an API key |
| Price | the software is free of charge; you pay your cluster and your model use | usage-based; early-access credits were reported |

## Where theos is behind

Each has an issue under #46, and the order is in
[plans/open-source-launch.md](plans/open-source-launch.md):

1. It works only against the Gitea it installs, so a team must move its work
   to try it (#57).
2. It runs one vendor's agent (#58).
3. The first run is long (#53), and adopting a repository you already have
   is poorly guided (#60).
4. Requests and chat reach it from fewer places (#61), nothing runs on a
   timer (#63), and a person can't yet say which changes wait for them (#62).
5. It publishes no numbers about itself (#66). Warp runs its open-source repository with its own factory in
   public and quotes the share of pull requests merged with no person.

## Where theos leads

1. **It is yours.** Every part, the agents' sandboxes and the Git host
   included, runs where you install it, and the code that decides what an
   agent may do is in this repository to read.
2. **The fence is part of the design,** not a setting: what an agent can
   reach, change and spend is enforced by the cluster
   ([design.md](design.md#the-fence)).
3. **It builds, not only maintains.** A model and a catalog of templates
   give repositories that pass their own checks on the first commit, and
   Standards keep every one of them current.
4. **It merges.** The default is a change that lands with no person once
   the checks, the reviewer and the verifier agree, with honest evidence
   rules for what agents report.
5. **It measures with intervals.** A Benchmark says "no clear difference"
   when the data doesn't separate two setups.

## What we couldn't verify

- Whether any part of Oz or Warp Factories can be self-hosted. Warp says
  teams can bring their own compute and keep conversations and memory on
  infrastructure they control; we found no way to run the orchestration
  yourself.
- Warp Factories' features beyond its announcement: it is in closed early
  access, and we have not used it.
- Prices after early access.

A correction is welcome as an issue, with the source.

## Sources

Read on 2026-11-09:

- Warp, [Introducing Oz: the orchestration platform for cloud agents](https://www.warp.dev/blog/oz-orchestration-platform-cloud-agents)
- Warp's documentation, [Set up your software factory](https://docs.warp.dev/guides/agent-workflows/set-up-a-software-factory/)
- Warp, [Warp is now open-source](https://www.warp.dev/blog/warp-is-now-open-source)
- It's FOSS, [Warp Factories launched](https://itsfoss.com/news/warp-factories-launched/)
- swyx, [Warp CEO Zach Lloyd on why software factories are the next phase of coding](https://swyx.substack.com/p/software-factories)
