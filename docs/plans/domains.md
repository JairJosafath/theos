# Domains: what the app is about, as a pinned template

Issue: #1512. Written 2026-10-22, from the maintainer's direction on the issue.

## Goal

A Standard tells the implementer how to build in a stack: lint, `AGENTS.md`,
the role guides. Nothing tells it about the *domain* it builds for: for a
webshop, how carts, stock and payments usually go wrong, the accepted
practices, the known controversies and the choice to make in each. A Domain is
that context as a pinned template in the catalog. A SoftwareSystem names the
Domains it is about; each of its Containers is scaffolded with them under
`docs/agents/domain/`, and the Standard's agent guide tells the implementer
and the reviewer to read them before they plan or review a change.

## Where things stand

- A catalog kind (Framework, Standard, Compute, Deployment, Connector) embeds
  `CatalogEntrySpec` (`api/v1alpha1/shared_types.go`): a template at a pinned
  ref plus a required `supportedTypes`. None has a reconciler. The chart
  installs them from `catalog.entries` (`dist/chart/templates/gitea/_catalog.tpl`),
  and `make validate-yaml` pairs each with `templates/versions.txt`.
- `scaffold.Build` (`internal/scaffold/scaffold.go`) renders the layers in
  `sideOrder` (Framework, Standard, Compute, Deployment, Connectors, Domains),
  `checkLayers` holds the order, the owner map fails two layers on one path
  (`ConflictError`), and each layer's `name@ref` goes to `scaffoldedFrom`.
  `resolve` (`internal/controller/container_resolve.go`) loads the entries,
  the system's Domains last. Step 2 (#1757) built the Domain layer:
  `domainTree` prefixes a Domain's files with `docs/agents/domain/<name>/`
  and fails a non-Markdown file (`DomainNotMarkdownError`) or a missing
  `README.md` (`DomainNoReadmeError`); `reportDomainsApplied`
  (`internal/controller/container_domains.go`) sets `DomainsApplied` on a
  Ready Container from the Domains the scaffold recorded against the
  system's `domainRefs`, compared by `model.DomainsDiff`
  (`internal/model/domain.go`, `TestDomainsDiff`). The scaffold records
  them in `status.scaffoldedDomains` and in its commit's `Scaffolded-Domain`
  trailers (`scaffold.CommitMessage`, `internal/scaffold/identity.go`);
  `ParseCommitMessage` reads the trailers back when a retry finds its own
  commit, and the controller's `scaffoldedDomains` reads the status, never
  inferring them from `scaffoldedFrom` (#1757).
- `model.AgentNeverPaths` (`internal/model/fence.go`) is `AGENTS.md`,
  `docs/agents/` and `.theos.yaml`: the fence refuses an agent's change
  there and the plan check refuses an issue asking for one. A file under
  `docs/agents/domain/` is already covered.
- Every Standard's `AGENTS.md.tmpl` points the agent at
  `docs/agents/implementer.md` or `reviewer.md`; none mentions a domain.
- The Standard's update (`internal/controller/container_standard.go`,
  `internal/model/standardupdate.go`) is Standard-specific: nothing in it is
  generic over a layer.
- The planner reads `model/catalog.yaml` (`workspaceCatalog`,
  `internal/model/brief_workspace.go`), its guide is `internal/model/planner.md`,
  and the plan check is `internal/model/brief.go`.

## Decisions

1. **A Domain is its own catalog kind, `Domain`,** namespaced, owned by the
   Project, no reconciler. Its spec is `displayName`, `description` and
   `template` (`url`, `ref`, a tag or commit like every template). It does
   not embed `CatalogEntrySpec`: what a system is about does not depend on a
   Container's type, so it has no `supportedTypes`, and every Container of a
   system that names it gets it. Not a Standard: a Standard is one per
   Container and about the stack; a system may be about two domains (a
   webshop with a loyalty scheme), and a Python and a TypeScript Container of
   one webshop share its domain.
2. **The SoftwareSystem names its Domains,** `spec.domainRefs`, a list of
   names, at most 5, unique (CEL), refused on an `External` system (CEL: the
   factory builds nothing there). Not the Container: the domain is what the
   system is for, and one list keeps a system's Containers agreeing. A
   Container that wants a different domain belongs to another system.
3. **A Domain renders last, into its own directory.** The layer order becomes
   Framework, Standard, Compute, Deployment, Connectors, Domains (sorted by
   name). The operator writes a Domain's files under
   `docs/agents/domain/<domain-name>/`, prefixing every path the template
   renders, so two Domains never collide and the template does not need to
   know where it lands. A Domain template holds Markdown only (`*.md`,
   `*.md.tmpl`); any other file fails the Container, `Scaffolded=False`,
   reason `DomainNotMarkdown`, naming the file. Its `README.md` is required
   (`DomainNoReadme`): the index the guides point to. The one-owner rule
   holds as for any layer: a Standard writing `docs/agents/domain/...` fails
   with the path. Each Domain's `name@ref` joins `scaffoldedFrom`.
4. **The Standard points the agent at it.** `scaffold.Input` gains
   `Domains []string` (names, sorted), set before the first layer renders.
   Each Standard's `AGENTS.md.tmpl` says, when `.Domains` is set: "This
   system is about <the domains>. Before you plan a change, read
   `docs/agents/domain/<name>/README.md` and the files it names for the part
   you touch; follow their practices, and where they list a choice, make
   theirs unless the issue says otherwise." The reviewer guide says to check
   the change against each domain's common mistakes. A Standard update
   renders with the same `Domains` (read from `status.scaffoldedDomains`,
   which the scaffold records by name: inferring them from `scaffoldedFrom`
   misreads a changed choice or a Domain named like a Connector), so it does
   not drop the paragraph.
5. **Scaffold once, no update path.** A Domain is written at scaffold and a
   later bump of its `ref` does not reach a scaffolded Container; not the way
   a Standard's does. Why: the update machinery is Standard-specific, and
   generalising it is a refactor of the factory's own machinery (priority 8)
   before anything shows the domain text helps. The files are in
   `AgentNeverPaths`, so no agent drifts them; a person may update them by
   hand. When the reference app shows stale domain text costs something, a
   follow-up proposes a Domain update on the Standard's machinery.
6. **A Domain named after the scaffold is reported, never applied.** A Ready
   Container whose system's `domainRefs` differ from its
   `status.scaffoldedDomains` gets `DomainsApplied=False`, reason `AddedAfterScaffold`,
   listing the names added and removed; `True` when they agree; absent when
   the system names none and none was scaffolded. As with Relationships.
   A missing Domain keeps an unscaffolded Container `Pending`,
   `ReferencesResolved=False`, naming it.
7. **What a domain template holds.** `README.md`: what the domain is, a
   glossary, and one line per topic file. A topic file per area, each with
   *Practices*, *Common mistakes*, *Controversies* (each with the choice the
   template makes and why) and *Sources*. Plain prose an agent reads; no
   code to copy, no stack: the Standard has the stack. A test in
   `internal/scaffold/templates_test.go` checks every Domain template in
   `templates/` against this shape (Markdown only, a README naming every
   file, the four headings).
8. **The first Domain is the webshop's**, `templates/webshop-domain`,
   catalog entry `webshop`: catalog and search, cart, checkout and orders,
   stock (reservations, oversell), payments (provider redirect, idempotency,
   webhooks, never a card number), pricing and money (integer minor units,
   currency, tax, rounding), accounts and personal data (GDPR, passwords).
   The chart installs it; the webshop sample's `webshop-core` names it, so
   the reference app shows whether it helps.
9. **The planner picks Domains.** `model/catalog.yaml` gains `domains:`
   (name, description). The guide says: name the installed Domains the idea
   is about on each new Internal system, and say which in the summary; none
   when none fits. The plan check refuses a Domain that is not installed and
   a `domainRefs` change on an existing system (it is not story text; a
   later Domain is a prompt for a new system, or a person's edit). The
   stand-in planner names each installed Domain whose name is a word of the
   idea, so the tests cover the path.

## Steps

1. **The `Domain` kind and `domainRefs`** (#1756) (`kubebuilder create api`, no
   controller): `DomainSpec`, printcolumns, `SoftwareSystem.spec.domainRefs`
   with its CEL rules and their failing cases in envtest, samples under
   `config/samples/`, `make validate-yaml`, the design's model table and a
   *Domains* paragraph in `docs/design.md`, `docs/dev/structure.md`.
2. **The Domain layer** (#1757) (`internal/model`, `internal/scaffold`,
   `internal/controller`): the side after Connectors, the path prefix, the
   Markdown and README rules, `Input.Domains`, `resolve` loading them,
   `scaffoldedFrom`, `DomainsApplied` and the watch on SoftwareSystems; the
   Standard update passing `Domains`. Table tests in `internal/scaffold`;
   envtest: a Container on a system naming a stand-in Domain is Ready with
   its files under `docs/agents/domain/`, a non-Markdown file fails it, a
   Domain added after shows `AddedAfterScaffold`.
3. **The Standards point at it** (#1758): each Standard's `AGENTS.md.tmpl` and
   reviewer guide (decision 4), their versions bumped in `versions.txt`
   with changelogs; `internal/scaffold` tests render with and without
   `Domains`.
4. **The webshop Domain** (#1759): `templates/webshop-domain` (decisions 7 and 8),
   the shape test, `versions.txt`, the chart's `catalog.entries` and
   `_catalog.tpl` for kind `Domain`, the seed, the webshop sample's
   `domainRefs`, `docs/templates.md`; `make validate-yaml`,
   `make test-gitea` and `make seed-local` where the seed changes.
5. **The planner picks Domains** (#1760) (decision 9): `catalog.yaml`, the guide,
   the plan check, the stand-in planner; `internal/model` tests and the
   Brief envtest.
6. **The UI** (#1761): a Domains catalog page, the system page's domains, the
   Container page's `DomainsApplied`; `make ui-test`.

Step 2 depends on 1; 3 and 6 on 2; 4 on 3; 5 on 1.

## Done when

Every step has landed with its tests, `make check` green: a Container on
`webshop-core` scaffolds with `docs/agents/domain/webshop/README.md` and its
topic files, `scaffoldedFrom` names `webshop@<ref>`, its `AGENTS.md` tells the
implementer to read them, and a stand-in Brief whose idea says "webshop"
plans a system naming the Domain.
