# Writing a catalog template outside this repository

A catalog entry (a Framework, Standard, Compute, Deployment, Connector or
Domain) points at one template: a Git repository at a pinned ref. This guide
is for a template kept in a repository of your own rather than in
`templates/`: how to lay it out, how to check it on your machine, and how a
project installs it. What each of this repository's templates gives an app
is in [../templates.md](../templates.md); what the factory does with them is
in [../design.md](../design.md#scaffolding-a-container).

## One repository at one pinned ref

Every catalog kind names its template the same way, `spec.template`
(`TemplateSource` in `api/v1beta1/shared_types.go`):

```yaml
template:
  url: https://git.example.com/acme/legal-domain   # http:// or https://
  ref: v1.0.0                                      # a tag, or a full 40-character commit SHA
```

The operator fetches the template with plain Git, from the manager's pod,
when it scaffolds a Container. Nothing seeds it: the chart's seed pushes this
repository's own `templates/` to the in-cluster Gitea, and an outside
template never goes through it.

- **The ref is a tag or a full commit SHA, never a branch.** A branch fails
  the Container `Scaffolded=False`, reason `UnpinnedRef` ("main is a branch
  in ...; pin a tag or a full commit SHA"). A tag or SHA the repository
  doesn't have fails the same way; a short SHA is one of those. A SHA must be
  reachable from one of the repository's branches or tags.
- **Never move a tag.** A Container records the `name@ref` it was scaffolded
  from (`status.scaffoldedFrom`). Release a change as a new tag and bump the
  entry's `ref`.
- **A fetch that fails for another reason is retried:** the host can't be
  reached, or it asks for credentials. The Container shows
  `Scaffolded=False`, reason `GitError`, with the error and "retrying".

## How a template renders

The rules are the same for every kind (`internal/scaffold/scaffold.go`):

- Every file is copied to the same path in the new repository, except
  `.theos/`, which is input for the factory and is never copied. Keep the
  template's own notes in `.theos/README.md`, as every template in
  `templates/` does.
- A file ending in `.tmpl` is rendered with Go `text/template` and loses the
  suffix. A missing key is an error. A `.tmpl` that renders to whitespace only
  is left out, which is how a template writes a file for some Containers and
  not others. Every other file is copied byte for byte, with its executable
  bit.
- The data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`,
  `.Description`, `.Type`, the chosen `.Framework`, `.Standard`, `.Compute`
  and `.Deployment` (empty when none is chosen), `.Bundle`, `.Connectors` and
  `.Domains` (sorted names), `.Checks` (`EveryChange` or `BeforeRelease`),
  `.Operations`, `.CI` (`.CI.Name`, such as `giteaActions`), `.NpmScope`,
  `.License` (`.License.ID`, empty when none is set), and `.FrameworkFiles`,
  the paths the Framework rendered, for any layer after it. The `Input` type
  documents each field.
- The functions, besides the `text/template` builtins: `has`, `fail`,
  `trimPrefix`, `snake`, `upper`, `lower`, `ts`, `dart` and `buildsImage`.
  `{{ fail "why" }}` refuses a combination the layer can't serve: the
  Container fails with that message.
- **One owner per path.** Two layers that write the same path fail the
  Container, naming the path. The operator owns `.theos.yaml`,
  `.devcontainer/`, and `LICENSE` when a license is set, so no template may
  write them.
- **CI files live under `.theos/ci/<system>/`** and render at the root only
  for the Container's CI system, such as `.theos/ci/giteaActions/.gitea/workflows/check.yaml`.
  A template with CI files for other systems but none for the Container's
  fails `UnsupportedCI`. A template without `.theos/ci/` works with any CI.
- Layers render in this order: the Framework, the Standard, the Compute, the
  Deployment, each Connector, then each Domain. Only the Framework is
  required.

**Scaffold once.** A Container is rendered once, when it is created. Only a
Standard's later tags reach a Container that was already scaffolded: the
operator opens a pull request with the difference and merges it. A new tag
of any other kind reaches only Containers scaffolded after the bump.

## The kinds

Every kind but Domain has `displayName`, `description`, `supportedTypes`
(the Container types it can scaffold: `static-site`, `service`, `job`,
`worker`, `mobile-app`, `cli`, `extension`, `operator`, `function`) and
`template`. A Container whose type an entry doesn't list fails
`ChoicesValid=False`.

| Kind | What its template writes | Its own spec fields | Look at |
| --- | --- | --- | --- |
| Framework | The application: its code, tests, build files and README, and `.theos/devcontainer.json`, the base devcontainer settings the operator merges into `.devcontainer/` (with Connectors, they need an `image`). Exactly one per Container. | `deploysItself` (carries its own deployment, such as a CDK app: takes no Compute, and no Deployment but `gitea-release`), `readsOperations` (renders `infra/operations.ts` from `spec.operations`), `testEnvironments` | `templates/go-cli`, `templates/python-fastapi` |
| Standard | The rules: lint and format config, `AGENTS.md`, `docs/agents/implementer.md` and `reviewer.md`, the `make check` contract and its CI workflow. Its `AGENTS.md` and reviewer guide should name `.Domains` so agents read them, as `templates/go-standard/AGENTS.md.tmpl` does. | `supportedFrameworks` (required), `protectedPaths` (files agents may not change; the Git host refuses a pull request that does), `checksBeforeRelease`, `testEnvironments` | `templates/go-standard` |
| Compute | The resources the app runs with, as files a Deployment reads, such as `deploy/k8s/compute.yaml`. | none | `templates/small` |
| Deployment | How a release ships: a Dockerfile and `deploy/k8s/` (`k8s-manifests`), or a release workflow that attaches the build to a Git release (`gitea-release`). | none | `templates/k8s-manifests`, `templates/gitea-release` |
| Connector | What a Container needs to reach a DataStore on it: client config, `.env.example`, manifests, and `.theos/compose.yaml`, a service the operator adds to the devcontainer as `compose.<name>.yaml`. A Relationship from a Container to a DataStore applies it. | `wiredBy` (the Frameworks whose files call it; any other fails `NotWired`) | `templates/postgres` |
| Domain | Markdown about what the system is for, under `docs/agents/domain/<name>/`. Below. | no `supportedTypes` | `templates/webshop-domain` |

## Domains

A Standard tells the implementer how to build in a stack; a Domain tells it
what the system is for: a webshop's carts, stock and payments, the practices
that hold, the mistakes that recur, and the choice to make where people
disagree. It is plain prose for agents to read: no code to copy and no stack.

**The entry.** A `Domain` has a `displayName`, a `description` and a
`template`, and no `supportedTypes`: every Container of a system that names
it gets it, whatever the Container's type. A SoftwareSystem names its Domains
in `spec.domainRefs`, at most 5. An `External` system names none, since the
factory builds nothing there. The planner reads each installed Domain's name
and `description` to decide whether a Brief's idea is about it, so write the
description as a list of what the Domain covers.

**What the template holds.** Markdown only, and a `README.md`:

```text
legal-domain/
├── .theos/README.md        what this template is, for its maintainers; never rendered
├── README.md               required: what the domain is, a glossary, one line per topic file
├── contracts.md            one topic file per area
├── consent.md
└── retention.md.tmpl       a topic may be a template: {{ .SoftwareSystem }} and the rest
```

- Every file it renders must end in `.md` (a `.md.tmpl` renders to `.md`).
  Any other file fails the Container `Scaffolded=False`, reason
  `DomainNotMarkdown`, naming the file. No `README.md` fails it with
  `DomainNoReadme`.
- The operator writes every file under `docs/agents/domain/<name>/`, where
  `<name>` is the Domain object's `metadata.name`, not the repository's.
  It adds that prefix itself, so the files name each other by their bare
  names (`contracts.md`) and the template never knows where it lands. Two
  Domains never collide, and another layer that writes
  `docs/agents/domain/...` fails the Container with the path.
- Domains render last, after the Connectors, sorted by name.

**What reads it.** Each Standard's `AGENTS.md` tells the implementer to read
`docs/agents/domain/<name>/README.md` and the topic files it names for the
part it touches, and to make the Domain's choices unless the issue says
otherwise. The reviewer guide tells the reviewer to check a change against
each topic's *Common mistakes* and to block one that makes them. The files
belong to the factory: agents never edit them.

**The shape.** The operator enforces only the two rules above. The guides
rely on more, and every Domain in `templates/` keeps it, so keep it too
(`docs/plans/domains.md`, decision 7):

- `README.md`: what the domain is, a glossary of its terms, and a *Topics*
  list with one line per topic file, naming it in backticks
  (`` - `contracts.md`: what a contract record holds ``).
- Each topic file has the headings `## Practices`, `## Common mistakes`,
  `## Controversies` (each with the choice this Domain makes and why) and
  `## Sources`.
- Say what holds across systems of the kind, not how one stack does it: the
  Standard has the stack.

`templates/webshop-domain` is the model: read its `README.md` and `cart.md`.

**Written once.** A Domain is rendered at the scaffold and never again: a
later tag of it doesn't reach a scaffolded Container, and a person updates a
copy by hand. A Ready Container reports `DomainsApplied=True` while its
system still names the Domains it was scaffolded with, and `False`, reason
`AddedAfterScaffold`, naming those added or removed since.

## Checking a template on your machine

Run these from a clone of this repository (`gh repo clone JairJosafath/theos`),
at the release your cluster runs, so the render is the one its operator
makes. None of them needs a cluster.

**Render it as the operator does.** `hack/render` renders layers from a folder
of template folders, one per layer, the way the operator scaffolds a
Container. Give it your template as the tag holds it, not your working clone:
a `.git` folder fails the render ("templates can't write .git"), and only the
tag is what the cluster fetches. Put the layers you combine it with next to
it; a link to this repository's folders works.

```bash
mkdir -p /tmp/tpl/legal-domain
git -C ~/src/legal-domain archive v1.0.0 | tar -x -C /tmp/tpl/legal-domain
for t in python-fastapi python-standard small k8s-manifests; do ln -s "$PWD/templates/$t" /tmp/tpl/$t; done
go run ./hack/render -templates /tmp/tpl -out /tmp/orders -type service -container orders \
  Framework=python-fastapi Standard=python-standard Compute=small Deployment=k8s-manifests Domain=legal-domain
ls /tmp/orders/docs/agents/domain/legal
```

`hack/render` names a Domain after its folder without `-domain`, so name the
folder `<name>-domain` for the Domain object's name. A render that breaks a
rule above fails with the same error the Container would show: a
non-Markdown file, no `README.md`, two layers on one path, a missing key in a
`.tmpl`. The render's `AGENTS.md` names the Domain when the Standard points at
Domains.

**Check a Domain's shape.** `TestDomainTemplatesHaveTheShape` checks every
`*-domain` folder in `templates/`: Markdown only, a `README.md` naming every
topic file, the four headings in each, and a scaffold with
the webshop's layers that writes every file. Put your Domain there in the
clone, uncommitted, and run that test alone. The rest of the package's tests
want every folder there to be one of this repository's.

```bash
mkdir templates/legal-domain
git -C ~/src/legal-domain archive v1.0.0 | tar -x -C templates/legal-domain
go test ./internal/scaffold/ -run TestDomainTemplatesHaveTheShape -count=1
rm -r templates/legal-domain
```

**Run what the render's repository runs.** For a template with code (a
Framework, Standard, Compute, Deployment or Connector), the render is a
repository whose own checks must pass. `make test-templates` does this for
this repository's combinations: it renders each with `hack/render`, commits
it in a new Git repository and runs its `make check`, then
`kustomize build deploy/k8s` for `k8s-manifests` or `make dist` at a version
tag for `gitea-release`. `COMBO="go cli" make test-templates` runs one;
`KEEP=1` keeps the renders. Its combinations are the `combos` list in
`hack/test-templates.sh`, and it renders from `templates/`, so to run yours
the same way, put your template in `templates/` as above and add a row for it
to that list in your clone. Or do the same steps by hand on the render:

```bash
cd /tmp/orders && git init -q -b main && git add -A && git commit -qm "Scaffold orders"
make check
```

It fetches what the render's lock files pin from npm, PyPI and the Go module
proxy. `make test-templates-operator` does the same for the `operator`
combination (`go-kubebuilder`, `go-operator-standard`, `small`).

**Check the pin.** From a machine on the cluster's network, or the cluster
itself, `git ls-remote https://git.example.com/acme/legal-domain v1.0.0`
should print the tag. A branch of that name, or nothing, is `UnpinnedRef`.

## Installing it in a project

A project's catalog is the catalog objects in its namespace, which is named
after the project (`webshop` in the guide). Apply the entry there:

```yaml
apiVersion: theos.factory.io/v1beta1
kind: Domain
metadata:
  name: legal                  # the directory: docs/agents/domain/legal/
  namespace: webshop
spec:
  displayName: Legal
  description: Contracts, consent and retention; the practices, the common mistakes and the choice to make in each.
  template:
    url: https://git.example.com/acme/legal-domain
    ref: v1.0.0
```

```bash
kubectl apply -f legal-domain.yaml
kubectl -n webshop get domains
```

The operator makes the Project in that namespace the Domain's owner, so
deleting the Project deletes it. Then name it on a system:
`spec.domainRefs: [legal]`. Containers scaffolded from then on get its files.
A Container already scaffolded doesn't, and shows `DomainsApplied=False`,
reason `AddedAfterScaffold`. A system naming a Domain the namespace lacks
keeps its new Containers `Pending`, `ReferencesResolved=False`, naming it.
The planner names an installed Domain on a new system when an idea is about
it.

The other kinds install the same way: a `Framework`, `Standard`, `Compute`,
`Deployment` or `Connector` object in the project's namespace, with its
`supportedTypes` and its own fields. The chart's `catalog.entries` installs
only the templates its seed pushes, so a template kept elsewhere is always an
object you apply.

## A private repository

The manager's pod fetches the template over HTTP or HTTPS from inside the
cluster, so the cluster must reach the host: its DNS name must resolve there,
and the cluster's egress, proxy and firewall must let the pod through. The
chart limits only what reaches the manager, not what it calls.

The operator sends credentials only to the project's own Git host: a template
whose URL has the GitProvider's scheme and host is fetched with the
GitProvider's token, and a template on any other host is fetched with none.
So a private template must either live on the project's Git host, in a
repository its token can read, or be readable without credentials from the
cluster's network, such as a host only that network reaches. A private
repository elsewhere fails the fetch: `Scaffolded=False`, `GitError`, retried
until it can be read. The `url` is `http://` or `https://`; SSH isn't
supported.
