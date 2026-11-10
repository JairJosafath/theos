# theos is a distributed factory on Kubernetes, not a tool for one device

GitHub issue #119. The owner's session of 2026-11-10. Written 2026-11-10.

## Goal

The owner's ask: change "the narrative that theos is meant for a device and
to be run on kind, that was for testing reasons, the reason why this is
being run on kubernetes is to make it an effective distributed solution for
agentic work".

Done when no page says or implies that one device or a Kind cluster is what
theos is for, every page that positions theos says why it runs on
Kubernetes in the words below, every Kind mention reads as the test rig or
the one-machine try-out, and no page claims what a gap issue says is not
there yet.

This plan changes words, not behaviour. What the audit found in code, the
chart and the scripts that really assumes one node is filed, not fixed and
not talked away (*The gaps*).

## The positioning

The paragraph every package reuses. A page takes it whole or takes the
sentences it needs; none adds a claim to it.

> theos runs on Kubernetes because agentic work is distributed work: many
> agents, each in a sandbox of its own, building at once. An agent's run is
> a sandboxed pod, and so are a project's CI jobs and its local test
> environments; the scheduler places each on any node with room. The model
> is custom resources in the cluster's API, the one truth every client
> reads and writes, and the operator reconciles it. The cluster enforces
> the fence: NetworkPolicies, and pods that hold no ServiceAccount token. A
> run that a node shutdown or an eviction interrupts resumes from its
> checkpoint. How many agents work at once is a setting, and the cluster's
> nodes are what bounds it. It installs from its Helm chart on a Kubernetes
> 1.30 or later cluster. A Kind cluster on one machine is how theos is
> tested and the quickest way to try it; it is not what theos is for.

What backs each sentence:

| Sentence | In the code |
| --- | --- |
| a run is a sandboxed pod | `internal/controller/task_sandbox.go` builds an Agent Sandbox `Sandbox` per run; `ci_runner_pod.go` a pod per CI job of a project's pool; `internal/adapters/localenv` a pod per local lease |
| the scheduler places it | no sandbox or runner pod names a node; a local TestEnvironment passes on the `nodeSelector` its author wrote |
| the model is the one truth | `api/v1beta1`, the CEL rules, the admission policy; the UI and the Session client enforce nothing |
| the cluster enforces the fence | the per-project NetworkPolicies and `automountServiceAccountToken: false` (design.md, *The fence*, *The CI fence*) |
| an interrupted run resumes | `internal/controller/task_checkpoint.go`: a volume of the run's own, `Interrupted` resumes in place |
| how many at once is a setting | the operator's `--max-agent-sandboxes`; an Agent's `resources` are the pod's requests |
| installs from a chart | `dist/chart`, `docs/getting-started.md` (Kubernetes 1.30 or later, Agent Sandbox first) |

What it must not claim: more than one cluster per install; high
availability of the factory (the chart's Gitea is one pod, #124); that
anything was run on more than one node (#125); a shared or multi-tenant
cluster (one install is one trust domain, and that sentence stays wherever
it stands).

## Decisions, and why

1. **"A device" stops meaning an install.** The docs used it for two
   things: a contributor's machine in the project's own fleet
   (`DEVELOPMENT.md`, `docs/dev/devices.md`, the cluster pool, the caches),
   and an install of theos that builds products and reports issues. The
   second is the narrative the owner names. An install is "an install", on
   "a cluster of your own"; whoever runs it is "you" or "its users". The
   first meaning stays in the dev docs, where it is true: those pages are
   about one machine.
2. **The trust sentence keeps its force.** "One install is one trust domain:
   run it on a device you own ... never on a shared or production cluster"
   becomes "run it on a cluster you own ...". Only the noun changes; the
   warning is about who may run code, not about size.
3. **Kind is named for what it is,** the same two ways everywhere: "the test
   rig" (how the factory is tested: `make cluster`, `make e2e`, the release
   workflows) and "a way to try it on one machine" (`hack/quickstart.sh`).
   Records of past runs on Kind (`docs/results.md`, `docs/results-v3.md`,
   `docs/plans/done/`, `docs/design-history.md`) stay as they are: they are
   history, and a line at the top of the two results pages says so.
4. **Gaps are named where a claim meets them.** A page that says "any node"
   next to something that assumes one node names the issue (`#120`), as
   `docs/design.md`'s *Out of scope and open points* now does. No page says
   a gap is closed before its issue is.
5. **The report form is renamed with its users,** in one package: the issue
   template, the intake labels' descriptions, the README and
   `docs/design.md` said *Report from a device* until package 3 made them
   say *Report from an install*. The heading `### Reported by` and the labels
   `source/user` and `user/<name>` are what `hack/lib/github.sh intake`
   reads and stay; nothing reads the form's name. The labels on GitHub keep
   their old descriptions until a person runs `hack/lib/github.sh labels`
   (`source/user`); a `user/<name>` label made before keeps its own.
6. **The boards are redrawn, not captioned away.** Both Excalidraw boards
   draw "one Kind cluster on a laptop". Until they are redrawn, the text
   beside each says the board draws the test rig.

## Packages

| # | Files | State |
| --- | --- | --- |
| 1 | `README.md`, `docs/design.md`, `docs/architecture.md`, this plan | done with this plan |
| 2 | `docs/getting-started.md`, `dist/chart/README.md`, `dist/chart/values.yaml` (comments only), `dist/chart/templates/NOTES.txt` if it frames, `docs/comparison.md`, `docs/adopt.md`, `ROADMAP.md` | done; `NOTES.txt` and `docs/adopt.md` framed nothing and stay as they were |
| 3 | `.github/ISSUE_TEMPLATE/user-report.yml`, `hack/lib/github.sh` (`gh_intake`'s label description) and its test, `hack/github-templates_test.sh`, then the form's name in `README.md` and `docs/design.md` | done; `hack/github-templates_test.sh` reads no changed string and stays |
| 4 | `ui/src/components/Panels.tsx`, `Shell.tsx`, `factory/Health.tsx`, `model/attention.ts`: copy and comments only; the one-node read is #122 | open |
| 5 | `docs/images/how-it-works.excalidraw` and `architecture.excalidraw` with their `.svg` and `.png`: "Where it runs" and "Kind cluster theos" | open; needs a person or a renderer |
| 6 | a line at the top of `docs/results.md` and `docs/results-v3.md`; `docs/templates.md`, `docs/template-authoring.md`, `ui/README.md`, `docs/dev/*`, `DEVELOPMENT.md`, `.claude/skills/factory-ops`, `hack/kind.yaml`'s comment: read once more, change only a sentence that calls Kind or a laptop the home | open |

`dist/chart/`, `hack/`, `.github/` and `.claude/` are protected paths
(`.github/CODEOWNERS`); packages 2, 3 and 6 touch them for their words
only, in the owner's session.

## The gaps

Found by the audit, filed on 2026-11-10, fixed by none of the packages:

| Issue | What assumes one node | Where |
| --- | --- | --- |
| #120 | the Go build caches are hostPath directories: one per node, refused under Pod Security | `internal/controller/task_sandbox.go`, `ci_runner_pod.go`, `dist/chart/templates/gitea/runners.yaml`, `dist/chart/values.yaml` |
| #121 | the chart's Gitea has no address outside the cluster: nodes can't pull its images, people port-forward | `dist/chart/templates/gitea/configmap.yaml`, `hack/kind.sh` (`registry_hosts`) |
| #122 | the UI's last-hour health reads the first node | `ui/health.mjs` (`sampleOf`), `ui/src/components/factory/Health.tsx` |
| #123 | the chart's defaults are the rig's (four sandboxes, workspaces in memory), and an Agent's sandboxes can't be placed | `dist/chart/values.yaml` (`manager.args`), `api/v1beta1/agent_types.go` |
| #124 | one node's loss stops the factory: Gitea is one pod on a ReadWriteOnce volume, the UI and chat single replicas | `dist/chart/templates/gitea/`, `ui/`, `chat/` |
| #125 | nothing runs theos on more than one node before a release | `hack/kind.yaml`, `test-chart.yml`, `test-e2e.yml`, `verify-release.yml` |

## How we know it is done

- `grep -rnE "\b[Dd]evices?\b" README.md ROADMAP.md docs/design.md docs/getting-started.md docs/comparison.md docs/adopt.md dist/chart/README.md .github/ISSUE_TEMPLATE`
  finds only test environments' devices (phones, device plugins, Device
  Farm) and the README's section for contributors.
- `grep -rnE "laptop|local Kind|Kind cluster" README.md docs/design.md docs/getting-started.md dist/chart/README.md ROADMAP.md ui/src`
  finds only sentences that call Kind the test rig or the try-out.
- The README, `docs/design.md` and `dist/chart/README.md` each carry the
  positioning, and each names the gaps or links to where they are named.
- `hack/check-doc-links.sh` and `make check` pass.
- #119 closes with the last package; #120 to #125 stay open.
