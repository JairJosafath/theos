# MVP v1 results

Each milestone of the [plan](design.md#mvp-v1-plan) ends in a check you can run in
the Kind cluster. This page shows each check passing, with screenshots of real
output. [`hack/evidence.sh`](../hack/evidence.sh) takes them again.

| # | Milestone | Done when | Result |
| --- | --- | --- | --- |
| 1 | Test environment | `make cluster gitea seed` gives a Gitea with tagged templates and the `gitea-credentials` Secret | Passed |
| 2 | Story model | Applying the sample builds the tree, deleting `webshop-core` cascades, and envtest covers both | Passed |
| 3 | Catalog and GitProvider | A `static-site` Container using `python-fastapi` shows `ChoicesValid=False` with a clear message | Passed |
| 4 | Scaffolding | `orders-api` gets a repository with the uv project, manifests and a devcontainer with Postgres; `Ready` with `scaffoldedFrom`; a repeat reconcile changes nothing; `make e2e` passes | Passed |
| 5 | Sessions | The test pod registers a Session that shows Active on `orders-api`, and it turns `Stale` after the pod is killed | Passed |
| 6 | Read-only UI | Someone who has never used kubectl can read the webshop story in a browser | Passed |

## The automated checks

`make e2e` builds the whole environment and checks every milestone above against
the cluster; `make check` runs code generation, formatting, vet, lint (including the
[layering rules](architecture.md)) and the unit and envtest suites.

The same suite runs in GitHub Actions on every push, building everything from
scratch on a fresh runner (about 12 minutes, most of it images and the two-minute
Stale wait).

![make e2e](screenshots/e2e.png)

![make check](screenshots/check.png)

## 1. Test environment

One Kind cluster holds Gitea, the operator, the sample project and a test Session.

![Pods in the cluster](screenshots/m1-pods.png)

The seed job pushes the four templates to Gitea's `templates` organization and tags
each at the version in [`templates/versions.txt`](../templates/versions.txt).

![Gitea's templates organization](screenshots/m1-gitea-templates.png)

![Template tags](screenshots/m1-template-tags.png)

## 2. Story model

The webshop sample, as `kubectl` sees it. Every kind is in category `c4`.

![kubectl get c4 -n webshop](screenshots/m2-story.png)

Children name their parent in `spec`; the operator turns that into the owner
reference, so deleting a parent deletes its children. `make e2e` deletes a
SoftwareSystem and checks its Containers, DataStores, Relationships and Sessions go
with it.

![Owner references](screenshots/m2-ownership.png)

On a copy of the story in its own namespace: deleting `webshop-core` takes its
Container, DataStore and their Relationship with it. The Person's link to the system
stays, because the Person owns it.

![Deleting webshop-core cascades](screenshots/m2-cascade.png)

A ValidatingAdmissionPolicy keeps each Project named after its namespace.

![The admission policy](screenshots/m2-policy.png)

Envtest runs a real API server with the real manager and every reconciler. It
covers ownership, CEL rules, the policy, roll-ups, catalog choices, scaffolding with
the real templates, and Sessions. It has no garbage collector, so the cascade itself
is checked in Kind, above.

![Controller specs](screenshots/m2-envtest.png)

## 3. Catalog and GitProvider

![ChoicesValid=False](screenshots/m3-choices.png)

## 4. Scaffolding

`orders-api` is `Ready`, with the templates it was made from.

![orders-api Ready](screenshots/m4-container.png)

The repository in Gitea: one commit, by C4 Factory, with trailers the operator uses
to recognise its own work when it retries.

![The orders-api repository](screenshots/m4-gitea-repo.png)

![The initial commit](screenshots/m4-gitea-commit.png)

And it works: ruff and pytest pass, the manifests build with the Compute patch and
the Postgres connection settings, and the devcontainer's Docker Compose files merge
Postgres into the app service.

![The scaffolded repository works](screenshots/m4-works.png)

## 5. Sessions

The test pod clones `orders-api` and runs `c4session` under the project's
`c4-session` ServiceAccount. Its Session shows on the Container and rolls up to the
SoftwareSystem and the Project.

![An active Session](screenshots/m5-active.png)

Killed with SIGKILL, the client stops sending heartbeats, and two minutes later the
operator marks the Session `Stale`.

![The Session turns Stale](screenshots/m5-stale.png)

The same, from a real devcontainer. `devcontainer up` on a freshly scaffolded
repository (`carts-api`) built the image with the c4session feature, which installed
the client, and the devcontainer registered a `human` Session. Inside it the tests
pass and Postgres, from `compose.postgres.yaml`, answers. Two things were set by
hand, as a user would: the feature's `binaryUrl` (the operator's
`--session-binary-url`), and a kubeconfig for the project's `c4-session` account.

![A human in the scaffolded devcontainer](screenshots/m5-devcontainer.png)

Stopping the devcontainer, which is what closing the editor does, deletes the Session.

![Stopping the devcontainer deletes its Session](screenshots/m5-devcontainer-stop.png)

## 6. Read-only UI

A web page for people who have never used kubectl. It reads the same resources the
operator writes, and watches keep it live: a phase change or a new Session shows up
without a reload. `make ui` deploys it in Kind; `make ui-forward` opens it on
http://localhost:8080.

The system context view: who uses which system, and what for.

![System context](screenshots/m6-ui-context.png)

The container view: what `webshop-core` is made of, the templates `orders-api` started
from, its repository, and who is working on it right now. The stale Session is the
one killed above.

![Containers of Webshop Core](screenshots/m6-ui-containers.png)

In dark mode, and on a phone.

![Dark mode](screenshots/m6-ui-containers-dark.png)

<img src="screenshots/m6-ui-phone.png" alt="On a phone" width="320">

Demo mode (`?demo=1`) shows every state the live sample doesn't: Containers that are
Pending, Scaffolding and Failed, Sessions from humans and agents, and a Relationship
added after scaffolding, drawn dashed and marked as not applied.

![Demo mode](screenshots/m6-ui-demo-containers.png)

The UI can't change anything. Its gateway forwards only GET requests, only under
`c4.factory.io`, and its ServiceAccount may only get, list and watch.

![The UI is read-only](screenshots/m6-ui-readonly.png)

## What's left out, on purpose

As the design says, v1 doesn't ship, doesn't read runtime health, doesn't adopt
existing repositories, and only supports Gitea. Things found while building:

- **The devcontainer feature needs a binary to download.** The repository is
  private, so `go install` can't fetch the client; set `--session-binary-url` on the
  operator to a place that serves `c4session`. Without it the devcontainer still
  starts, it just isn't a Session.
- **A laptop devcontainer needs a kubeconfig** for the project's `c4-session`
  ServiceAccount to register Sessions, and a route to the API server; in-cluster pods
  get both for free.
- **Devcontainers without Connectors** are image-based, with no compose file to
  pass SIGTERM on, so their Sessions turn `Stale` on stop instead of being deleted.
- **`make run` can't scaffold**, because the operator on your machine can't resolve
  Gitea's in-cluster name. Use `make deploy`.
