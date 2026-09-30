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

## Contributing

Read [AGENTS.md](AGENTS.md) for the project layout and the rules for changing it.
They apply to humans and AI agents alike.
