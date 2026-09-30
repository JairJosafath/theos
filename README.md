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

Design stage. [docs/design.md](docs/design.md) holds the design and the MVP v1 plan:
six milestones from a local test cluster to a read-only UI.

## Getting started

Open the repository in the dev container. It includes Go, kubebuilder, Docker, Kind,
kubectl, Helm, uv and Node; see [.devcontainer/README.md](.devcontainer/README.md).

Everything runs in one local Kind cluster: Gitea for the repositories, the operator,
and a sample `webshop` project. The Make targets planned for milestone 1 are
`make cluster gitea seed`, then `make deploy sample`, or `make e2e` for all of it.

## Contributing

Read [AGENTS.md](AGENTS.md) for the project layout and the rules for changing it.
They apply to humans and AI agents alike.
