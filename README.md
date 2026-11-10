# theos

**THE Open Software-factory. Tell the story first; the factory builds the workspace.**

theos turns an idea into working, scaffolded repositories. You describe your
software as a small [C4](https://c4model.com)-style model: who uses it, which
systems exist, and which applications and data stores they are made of. A Kubernetes
operator reads that model and creates a ready-to-code repository for every
application.

```
Person ──uses──▶ SoftwareSystem ──contains──▶ Container ──reads/writes──▶ DataStore
                                                  │
                                                  ▼
                               a Git repository, scaffolded once from pinned templates
```

## Why theos

A software factory carries a change from an idea to production with agents,
and asks a person only where a person should decide. theos is the one you
run yourself: the agents, the Git host, CI and the model credentials stay on
your cluster, every part is in this repository, and it builds new software
from a model as well as maintaining what exists.
[docs/comparison.md](docs/comparison.md) puts it next to Warp's factory, row
by row, with what theos doesn't do yet.

## Quickstart

With Docker running, on Linux, macOS or WSL, from a clone at a release's tag:

```sh
hack/quickstart.sh
```

It makes a Kind cluster of its own, installs Agent Sandbox and the release's
chart (the operator, the UI, Gitea and its CI runners), and gives the
stand-in Agent a Brief, so the whole loop runs with no model and no token:
a plan, a scaffolded repository, an issue, a change, CI, a review, a merge.
Then it opens the UI on <http://localhost:8080>. It needs about 4 CPUs and
8 GiB free, uses a kubeconfig of its own and never yours, and downloads
`kind`, `kubectl` and `helm` only when they aren't on your PATH, checked
against pinned checksums. `hack/quickstart.sh --down` removes all of it.

While the chart and the images on GHCR are private, give it a GitHub user
and a token with `read:packages`: `GHCR_USER=you GHCR_TOKEN=... hack/quickstart.sh`.

[docs/getting-started.md](docs/getting-started.md) goes on from there: real
agents with a Claude token, a model and a Crew of your own, releases and
deploys. Have a repository already?
[docs/adopt.md](docs/adopt.md) puts a Crew on it as it is, checked by your
own command and CI, with every change waiting for your approval until you
say otherwise.

## How it works

[![How theos works: describe, scaffold once, build from issues, keep current, watch, the fence and where it runs](docs/images/how-it-works.png)](docs/images/how-it-works.svg)

The board's source is [docs/images/how-it-works.excalidraw](docs/images/how-it-works.excalidraw); open it in Excalidraw to edit it. What runs where, the code layers and where state lives are on the [architecture board](docs/images/architecture.png) in [docs/architecture.md](docs/architecture.md).

1. **The model lives in Kubernetes.** Each element (Project, Person, SoftwareSystem,
   Container, DataStore, Relationship) is a custom resource. Anything can write them:
   kubectl, GitOps, agents or a UI.
2. **Pick from a catalog.** A Container chooses a Framework, Compute and Deployment
   template. A DataStore picks a Connector, such as Postgres.
3. **The operator scaffolds once.** It creates the repository, renders the templates,
   adds a devcontainer, pushes one commit and reports `Ready`. After that it only
   reports.
4. **Work shows up in the model.** Humans and agents code in devcontainers. A small
   client registers each one as a Session, so you can see who is working on what.

Shipping goes through the tool that owns each place an app runs (the GitHub API,
CloudFormation, Argo CD or Flux): the factory hands it each release and shows
the health that tool reports. Monitoring the apps and provisioning real
databases are left to existing tools such as Crossplane.

## The loop

theos carries work around the whole software-development cycle, with a person
only where a person should decide:

```
idea or bug ─▶ triage ─▶ spec ─▶ [you review the spec] ─▶ implement ─▶ code review
     ▲                                                                     │
     └── monitor ◀── ship ◀── [you review the product] ◀── verify ◀────────┘
```

- **Triage.** A triager Agent reads each proposed issue and decides: ready
  (it writes the acceptance criteria), needs a spec, parked with a question
  for a person, or a duplicate.
- **Spec.** A spec writer opens a pull request with `docs/specs/<issue>.md`:
  the problem, the approach and the acceptance criteria. You approve it on the
  Git host (or let the reviewer, with `specApproval: Reviewer`), and the issue
  goes on to be built.
- **Implement and review.** As before: an implementer in a sandbox, CI, a
  reviewer Agent, rounds until it merges. A Crew's `approval` rules say
  which changes wait for you first, by path, label and size, and which merge
  on CI alone ([Start small](docs/getting-started.md#start-small-let-the-factory-merge-only-what-you-are-sure-of)).
- **Verify.** A verifier runs the change and checks each acceptance criterion
  against what it does before it merges.
- **Ship and review the product.** Releases, and the deploys you approve.
- **Monitor.** A deploy that turns degraded or fails files a bug, which goes
  back to triage.

Each stage is off until a Crew names its Agent:

```yaml
apiVersion: theos.factory.io/v1beta1
kind: Crew
metadata:
  name: orders-cli
spec:
  containerRef: {name: orders-cli}
  implementer: {name: implementer}
  reviewer: {name: reviewer}
  triager: {name: triager}        # sorts status/proposed issues
  specWriter: {name: implementer} # writes specs for status/needs-spec issues
  specApproval: Person            # or Reviewer, for a factory nobody watches
  verifier: {name: reviewer}      # runs each change against its criteria
```

The UI's **Loop** area draws one project on this cycle, with the two places
that wait for you marked
([screenshot](docs/screenshots/sdlc-loop-ui.png)).
[docs/plans/done/sdlc-loop.md](docs/plans/done/sdlc-loop.md) has the design.

## Chat

A project can be followed and steered from [Matrix](https://matrix.org) too:
a `Chat` in its namespace gets a Space named after the Project with a room
per kind of work, where each item that needs a decision is a thread the bot
edits as it moves:

| Room | Posts | You can |
| --- | --- | --- |
| `updates` | merges, releases, deploys and Brief rounds, batched per `digestMinutes` | read |
| `general` | answers | `status`, `brief <idea>`, `prompt <brief>: <text>`, `pause <brief>`, `resume <brief>`, `help` |
| `triage` | each triage decision; a parked issue's question as a thread | answer in the thread |
| `specs` | a thread per spec waiting for approval | `approve`, or `changes: <text>` |
| `reviews` | a thread per deploy waiting for you and per change a Crew's approval rules hold for you, and each review's verdict | `approve <version>` for a deploy; `approve`, or `changes: <text>`, for a change |
| `alerts` | failed deploys and Tasks, a red base, an unhealthy factory part | `retry` on a failed Container |

Chat enforces nothing: each command is one request the UI could make, sent as
you, so the cluster's RBAC decides. The chart turns it on with
`chat.enabled`, with an in-cluster homeserver or your own
([dist/chart/README.md](dist/chart/README.md#chat)); you add yourself to the
Chat's `people`. [docs/plans/done/chat-matrix.md](docs/plans/done/chat-matrix.md)
has the design.

## Status

The factory runs end to end in a local Kind cluster: the story model, the
catalog, scaffolding into Gitea, Sessions, Tasks and Crews that build from
issues, Standards that keep their Containers current, and a UI with the story,
the work and what needs a person. [docs/design.md](docs/design.md) holds the design,
[docs/results.md](docs/results.md) shows v1 working, with screenshots, and
[docs/results-v3.md](docs/results-v3.md) the factory watching itself.

Devices install the factory from its Helm chart (see
[docs/getting-started.md](docs/getting-started.md)), build their products with
it, and report problems here (see *Report an issue*). The Kind path below is
for developing the factory itself.

[ROADMAP.md](ROADMAP.md) has the release train, the milestones and what 1.0
means.

## Develop the factory

Clone the repository on Linux or WSL and open it in VS Code: `./dev setup` downloads
Go, kubebuilder, Kind, kubectl, Helm, uv and Node into `.dev/`, and Kind runs on
your Docker (Docker Desktop with WSL integration). Run the commands below in the
project shell, which every VS Code terminal opens in; see
[DEVELOPMENT.md](DEVELOPMENT.md).

```sh
make cluster gitea seed   # a Kind cluster with Gitea and the tagged templates
make deploy sample        # the operator, and the webshop story
kubectl get theos -n webshop # the story as the operator sees it
make session              # a test Session: a pod working on orders-api
make ui ui-forward        # the UI on http://localhost:8080
make e2e                  # all of the above, then check the whole loop
```

A minute after `make sample`, Gitea (`make gitea-forward`, http://localhost:3000)
holds the webshop's repositories, such as `webshop/orders-api`: a uv project with
FastAPI, Kubernetes manifests and a devcontainer with Postgres, pushed as one commit. The repositories are public; to sign
in, `make gitea-admin` prints the admin password, which `make gitea` generates for
each cluster.

To use theos on your own cluster instead, install a release: apply Agent
Sandbox, make the project namespaces, then `helm install` the chart with
`gitea.projects`, which brings the operator, the UI and a seeded Gitea with its
CI runners. The chart and images are published to GHCR by a version tag.
[docs/getting-started.md](docs/getting-started.md) takes a device from there to
its first app: the install, the agents' Claude token, a model, an Agent and a
Crew that turn a Gitea issue into a merged pull request.
[The chart's README](dist/chart/README.md) gives the install sequence, its
values and how to pull private images.

## A device without the cluster

The Kind cluster wants about 32 GB of disk. A device with less can still work the
factory's issues with the light setup, `./dev setup --lite`: Go, Node, git, the
GitHub CLI and Claude Code, without Docker, Kind, kubectl or Helm, and with
`THEOS_CLUSTER=none` set.

- **It can** build and test the operator (`make check`: envtest runs a local API
  server, no cluster), test against a local Gitea (`make test-gitea`), change
  scripts, templates and docs.
- **It can't** deploy, run Crews or `make e2e`.

No issue needs the cluster: Go tests are the gate, and a Kind cluster runs only
when a human asks for it (`THEOS_HUMAN_CLUSTER=1 hack/lib/github.sh cluster
acquire`; agents never lease one). So a device without it works every ready
issue.

To set one up:

1. Clone `JairJosafath/theos` on Linux or WSL and run `./dev setup --lite` in
   it; the choice is remembered, and VS Code runs `./dev setup` on every open
   after that. Run `gh auth login` and then `gh auth setup-git`: the git
   credential helper gives `hack/lib/github.sh` its token.
2. Open a new terminal (the project shell), run `claude` once to log in, and
   check that `echo $THEOS_CLUSTER` prints `none`.

Opening the folder in VS Code trims Go's build cache,
oldest entries first, when it has grown past 4 GB (`hack/trim-go-cache.sh`, `THEOS_GOCACHE_MAX_GB`),
on every device.

## Report an issue

[Open an issue](https://github.com/JairJosafath/theos/issues/new/choose) and
pick the form: a bug, a feature or improvement, or a catalog template. A
vulnerability goes to [SECURITY.md](SECURITY.md) instead.

An install that reports under a name of its own (`laptop-2`) uses the
*Report from a device* form, or the GitHub CLI: a body with a `### Reported by`
heading and the name under it gets the labels `source/user` and
`user/laptop-2`.

```
gh issue create -R JairJosafath/theos --title "Crew stalls" --label source/user --body-file report.md
```

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) takes you from a fork to a merged pull
request. [AGENTS.md](AGENTS.md) has the project layout and the rules for
changing it; they apply to humans and AI agents alike.

## History

theos began as C4 Factory and keeps its history. Most issue and pull request
numbers in the docs and plans (`#1094`) are from that project's repository,
which stays private as the archive, so they are not links; numbers from
2026-11 on are this repository's.

## License

theos is licensed under the [Apache License 2.0](LICENSE). The applications
it scaffolds are yours and carry the license their Project names
(`spec.license`), or none.
