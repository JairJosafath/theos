# C4 Factory

**Tell the story first. The factory builds the workspace.**

C4 Factory turns an idea into working, scaffolded repositories. You describe your
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

## How it works

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

Shipping, runtime health and provisioning real databases are left to existing tools
such as ArgoCD, Flux and Crossplane.

## Status

MVP v1 runs end to end in a local Kind cluster: the story model, the catalog,
scaffolding into Gitea, Sessions and a read-only UI. [docs/design.md](docs/design.md)
holds the design and plan, [docs/results.md](docs/results.md) shows each milestone
working, with screenshots.

## Try it

Open the repository in the dev container. It includes Go, kubebuilder, Docker, Kind,
kubectl, Helm, uv and Node; see [.devcontainer/README.md](.devcontainer/README.md).

```sh
make cluster gitea seed   # a Kind cluster with Gitea and the tagged templates
make deploy sample        # the operator, and the webshop story
kubectl get c4 -n webshop # the story as the operator sees it
make session              # a test Session: a pod working on orders-api
make ui ui-forward        # the read-only UI on http://localhost:8080
make e2e                  # all of the above, then check every milestone
```

A minute after `make sample`, Gitea (`make gitea-forward`, http://localhost:3000)
holds `webshop/orders-api`: a uv project with FastAPI, Kubernetes manifests and a
devcontainer with Postgres, pushed as one commit. The repositories are public; to sign
in, `make gitea-admin` prints the admin password, which `make gitea` generates for
each cluster.

## A device without the cluster

The Kind cluster wants about 32 GB of disk. A device with less can still work the
factory's issues in the light dev container,
[`.devcontainer/lite`](.devcontainer/lite/devcontainer.json): Go, Node, git, the
GitHub CLI and Claude Code, without Docker, Kind, kubectl or Helm, and with
`C4_CLUSTER=none` set.

- **It can** build and test the operator (`make check`: envtest runs a local API
  server, no cluster), change scripts, templates and docs, and run the relay.
- **It can't** deploy, run Crews, `make e2e`, or check anything in Gitea.

Issues whose work or "Done when" needs the cluster carry the label `needs/cluster`;
whoever files an issue sets it. With `C4_CLUSTER=none`, `hack/lib/github.sh pick`
skips them and `take` refuses them. A session that finds mid-way that its issue
needs the cluster lands what it can (the code pull request, with unit and envtest
tests), labels the issue `needs/cluster` and hands off with what is left; a device
with the cluster takes it from there. On that device one checkout at a time works a
`needs/cluster` issue: the cluster is shared.

To set one up:

1. Clone `JairJosafath/c4factory`, open it in VS Code, run "Dev Containers: Reopen in
   Container" and choose **C4 Factory (lite, no cluster)**. Sign in to GitHub in VS
   Code: the git credential helper gives `hack/lib/github.sh` its token.
2. Run `claude` once to log in, and check that `echo $C4_CLUSTER` prints `none`.
3. `hack/relay.sh`; the log is in `.relay/relay-1.log`, `touch .relay/stop` stops it.

The container empties Go's build cache on start when it has grown past 4 GB
(`hack/trim-go-cache.sh`, `C4_GOCACHE_MAX_GB`); the full container does the same.

## Contributing

Read [AGENTS.md](AGENTS.md) for the project layout and the rules for changing it.
They apply to humans and AI agents alike.
