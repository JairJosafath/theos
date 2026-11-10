# Writing a template outside this repository

The catalog is not closed. `templates/` is the catalog the chart seeds; any
Git repository can be a Framework, Standard, Compute, Deployment, Connector
or Domain, and a project installs it with one catalog object in its
namespace. This page says how to write one, test it on your machine before
you tag it, and install it. [design.md](design.md#standards-and-the-catalog)
holds the rules; [templates.md](templates.md) scores the seeded ones.

## What a template is

A template is one Git repository at a pinned ref. A catalog object names it:

```yaml
apiVersion: theos.factory.io/v1beta1
kind: Domain                 # or Framework, Standard, Compute, Deployment, Connector
metadata:
  name: church-ops           # the layer's name; a SoftwareSystem's domainRefs names it
  namespace: church-ops      # the project's namespace: each project has its own catalog
spec:
  displayName: Church operations
  description: Services, teams, streaming and sound for a church that organizes itself.
  template:
    url: https://github.com/you/church-ops-domain
    ref: v0.1.0              # a tag or a full commit SHA
```

- **The ref is a tag or a commit, never a branch.** A branch, or a ref the
  repository lacks, fails the Container with reason `UnpinnedRef`. To ship a
  change, tag a new version and bump `ref`.
- **The operator fetches it with plain Git** when it scaffolds a Container;
  nothing has to be seeded first. It sends the project's GitProvider
  credentials only to the GitProvider's own host. A template anywhere else is
  fetched with none, so it must be public, and the operator's pod must be able
  to reach its host. A private template goes into a repository on the
  GitProvider's host, such as the in-cluster Gitea's `templates`
  organization, where the seeded ones live.
- **The four sides besides Domain list `supportedTypes`,** the Container types
  they can scaffold (`service`, `cli`, `static-site`, ...). A Container whose
  type an entry doesn't support doesn't scaffold. A Domain has none: what a
  system is about doesn't depend on a Container's type.

## How a template renders

A Container's repository is its layers rendered in order: Framework,
Standard, Compute, Deployment, the Connectors, then the Domains. The operator
pushes the result as one commit and never scaffolds it again. Only a
Standard follows a later bump of its `ref`, through a pull request the
operator opens.

- Every file is copied to the same path, except `.theos/`, which is input for
  the factory and never copied.
- A file ending in `.tmpl` is rendered with Go `text/template` and loses the
  suffix. A missing key is an error, and a file that renders to whitespace
  only is left out, so `{{ if ... }}` can drop a file.
- **Each path belongs to exactly one layer.** Two layers writing the same path
  fail the Container, and the message names the path. `.devcontainer/` and
  `.theos.yaml` are the operator's.
- `.theos/README.md` documents the template for people and is not copied.
  `.theos/devcontainer.json` is merged into the Container's devcontainer,
  `.theos/compose.yaml` adds services to it, and `.theos/ci/<system>/`
  renders at the repository's root for that CI system only (`giteaActions`
  today).

What a `.tmpl` file sees is `scaffold.Input` in
[internal/scaffold/scaffold.go](../internal/scaffold/scaffold.go): `.Project`,
`.SoftwareSystem`, `.Container`, `.DisplayName`, `.Description`, `.Type`, the
chosen `.Framework`, `.Standard`, `.Compute` and `.Deployment`,
`.Connectors`, `.Domains`, `.NpmScope`, `.FrameworkFiles` and more; the
comments there say what each holds. Its functions: `has`, `fail`, `snake`,
`upper`, `lower`, `trimPrefix`, `ts`, `dart` and `buildsImage`. `fail`
stops the render with your message, for a combination your layer can't
serve:

```
{{ if not (has .FrameworkFiles "infra/connectors.ts") }}{{ fail "needs an AWS Framework" }}{{ end }}
```

## Each kind

| Kind | Holds | Start from |
| --- | --- | --- |
| Framework | the application: its code, its build, its README | `templates/hello`, then `python-fastapi` or `go-cli` |
| Standard | the rules: `make check` (the target CI and agents run), lint config, `AGENTS.md` and the role guides, the CI workflow under `.theos/ci/` | `templates/hello-standard`, then `python-standard` |
| Compute | sizing and runtime settings | `templates/small` |
| Deployment | what a release ships and how | `templates/k8s-manifests`, `templates/gitea-release` |
| Connector | what a DataStore needs in the app: config, a dev service in `.theos/compose.yaml`, maybe its manifests | `templates/postgres` |
| Domain | what the system is about, in Markdown | `templates/webshop-domain` |

A few kinds carry more than the template:

- A **Standard** lists `supportedFrameworks`, the Frameworks whose code it can
  check (required), and `protectedPaths`, the files agents must not change,
  such as the check contract and the lint config: the Git host refuses to
  merge a pull request that changes them. `checksBeforeRelease` runs the full
  check before a release rather than on every change. `schedules` proposes
  recurring goals, such as the language's weekly dependency update, to the
  Crews of its Containers, each with a `name`, a five-field `cron`, a
  `timeZone` (UTC when unset) and a `goal`; a proposal turns nothing on
  until a person adds it to a Crew's own `spec.schedules`.
- A **Connector**'s `wiredBy` lists the Frameworks that wire it into their
  code; a Container on another Framework fails with `ChoicesValid=False`,
  reason `NotWired`. Leave it unset for a Connector whose files stand alone.
  A Framework wires the Connectors it knows from `.Connectors`, so a new
  Connector needs a Framework release that knows its name.

## A Domain

A Domain tells the implementer and the reviewer what the system is for: its
glossary, the practices that hold in the field, the mistakes that recur, and
the choices with the pick this domain makes. A Standard says how to build in
a stack; a Domain has no stack.

- It holds Markdown only (`*.md` and `*.md.tmpl`) and a `README.md`. Another
  file fails the scaffold with reason `DomainNotMarkdown`, naming the file;
  no `README.md` fails it with `DomainNoReadme`.
- It renders under `docs/agents/domain/<name>/` in every Container of each
  SoftwareSystem that names it in `spec.domainRefs` (at most 5). The
  Standards' agent guides tell the agents to read it.
- It is written once, at the scaffold. A later bump of its `ref` reaches only
  Containers scaffolded after it; one that existed before reports
  `DomainsApplied=False`, reason `AddedAfterScaffold`.
- Write it as `webshop-domain` is: a `README.md` with the purpose, a glossary
  and the rule that agents read these files and never edit them, then one
  file per topic, each with *Practices*, *Common mistakes* and *Choices*.

## Test it before you tag it

`hack/render` renders layers from a folder the way the operator does, so you
can run what comes out. It reads each layer from `<folder>/<name>`; a folder
of links mixes your templates with the seeded ones. A Domain's folder is
named `<name>-domain`. From a theos checkout:

```bash
mkdir -p ~/catalog
ln -s ~/src/church-ops-domain ~/catalog/church-ops-domain
for t in hello hello-standard; do ln -s "$PWD/templates/$t" ~/catalog/$t; done
go run ./hack/render -templates ~/catalog -out /tmp/app -type cli \
  Framework=hello Standard=hello-standard Domain=church-ops-domain
cd /tmp/app && make check
```

`go run ./hack/render -h` lists the rest of its flags (`-container`,
`-npm-scope`, `-ci`, the operations ones). A layer conflict, a template error
or a Domain's non-Markdown file fails the render with the message the
Container would show. For a Framework or a Standard, render it with the
layers it pairs with and run the result's own `make check`, as
`make test-templates` does for the seeded combinations.

## Install it

1. Tag the commit: `git tag v0.1.0 && git push origin v0.1.0`.
2. Apply the catalog object above in each project's namespace that uses it.
3. Name it: a Container's `frameworkRef`, `standardRef`, `computeRef`,
   `deploymentRef` or a Bundle; a DataStore's `connectorRef`; a
   SoftwareSystem's `domainRefs`. A Brief's planner sees the installed
   entries and may pick it.

The Container's `status.scaffoldedFrom` then lists it as `name@ref`.
