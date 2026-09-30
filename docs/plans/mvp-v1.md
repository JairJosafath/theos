# Plan: MVP v1, end to end

Goal: build the six milestones of [design.md](../design.md#mvp-v1-plan) on the branch
`explore/mvp-v1`, each checked in the Kind cluster, with evidence in
[docs/screenshots/](../screenshots/).

## Decisions the design leaves open

These are the calls made while building. Each is small and easy to change.

| Topic | Decision |
| --- | --- |
| Operator names | Project name `c4-factory`, so the namespace is `c4-factory-system` and the Deployment `c4-factory-controller-manager`. Kind cluster `c4factory`. |
| Who sets ownerReferences | Each child's own reconciler, from the parent named in `spec`. Kinds owned by the Project (Person, SoftwareSystem, GitProvider, catalog entries) have no parent field, so the Project reconciler adopts them. |
| Ancestor labels | `c4.factory.io/project`, `c4.factory.io/software-system`, `c4.factory.io/container`, set by the same reconcilers. |
| Template files | Files ending in `.tmpl` are rendered with `text/template` and lose the suffix; other files are copied as-is, so templates can hold `{{` safely. |
| Template metadata | A template's `.c4factory/` folder is input to the factory, never copied. Framework: `.c4factory/devcontainer.json` (base settings). Connector: `.c4factory/compose.yaml` (its service, plus `environment` for the `app` service). |
| Devcontainer | No Connectors: an image-based `devcontainer.json`. With Connectors: `compose.yaml` with an `app` service, one `compose.<connector>.yaml` each, and `devcontainer.json` pointing at them. Docker Compose merges them. |
| Pinned refs | The fetcher accepts a tag or a full commit SHA, and fails the Container for anything else, including branch names. |
| Recognising our own commit | The operator commits as `C4 Factory <factory@c4.factory.io>` with trailers `C4-Factory-Container:` and `Scaffolded-From:`. A repository whose only commit has those trailers counts as already scaffolded, so a lost status write is recovered, not repeated. |
| Git access | go-git in memory for fetch and push; Gitea's REST API for creating repositories. Secret `gitea-credentials` holds `username` and `token`. |
| Template repositories | Public in Gitea, so fetching needs no credentials. The provider's credentials are used anyway when the template is on the same host. |
| Seed job | An image built from `hack/seed/` with the templates baked in; `make seed` kind-loads it and runs it as a Job in `gitea`. |
| Session ServiceAccount | The Project reconciler creates `c4-session` with a Role limited to Sessions in its namespace. |
| Session client | `cmd/c4session`, with `run` (register, heartbeat, delete on SIGTERM). The devcontainer feature lives in `features/src/c4session/`; its install never fails a devcontainer build. |
| Stale | The Session reconciler requeues at `lastHeartbeat + 2m` and marks `Stale` then. The window is a manager flag for tests. |
| Relationship `Applied` | Only for Container to DataStore. The Container records the Relationships it applied in `status.appliedRelationships`; one missing from a Ready Container is `AddedAfterScaffold`. |
| UI | Vite, React and TypeScript in `ui/`, served with a GET-only proxy to the Kubernetes API (`ui/server.mjs`), so it is read-only by construction. Watches keep it live. |
| Screenshots | Playwright and Chromium: the UI, Gitea pages, and terminal output rendered to HTML. |

## Steps

1. **Test environment (M1).** `hack/kind.yaml`, `config/gitea/` (Deployment,
   Service, PVC, SQLite, admin from a Secret), the seed image and Job, the four
   templates in `templates/`, and Make targets `cluster`, `gitea`, `seed`.
   Check: tagged templates visible in Gitea; Secret `gitea-credentials` in `webshop`.
2. **Story model (M2).** Types, CEL rules, printer columns and categories, the
   ValidatingAdmissionPolicy in `config/policy/`, ownership and labels in the
   reconcilers, roll-up status. Envtest: tree, ownerReferences, CEL rejections,
   policy rejection. Check in Kind: sample builds the tree; deleting
   `webshop-core` cascades.
3. **Catalog and GitProvider (M3).** Types; `internal/model` compatibility rules
   with table tests; Container reconciler resolves references and checks types.
   Check: a `static-site` Container using `python-fastapi` shows `ChoicesValid=False`.
4. **Scaffolding (M4).** `internal/gitprovider` (interface, Gitea), `internal/scaffold`
   (layers, conflicts, devcontainer, identity file), Container reconciler pipeline,
   operator image and `make deploy`, `make sample`, `make e2e`.
   Check: `orders-api` repository with the uv project, manifests and a devcontainer
   with Postgres; `Ready` with `scaffoldedFrom`; a repeat reconcile changes nothing.
5. **Sessions (M5).** Session reconciler, `c4session` client and image, the feature,
   the test pod. Check: Active on `orders-api`, then `Stale` after the client is killed.
6. **Read-only UI (M6).** System context and container views, live through watches.
   Check: the webshop story reads in a browser without kubectl.
7. **Wrap up.** `make check` and `make e2e` green, screenshots, docs updated,
   branch pushed.

## Layering check

`make lint` runs depguard with the rules in [architecture.md](../architecture.md).
`make check` runs manifests, generate, fmt, vet, lint and test.
