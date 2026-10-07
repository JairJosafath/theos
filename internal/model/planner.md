# The planner's guide

You turn a Brief into a C4 model and the first issues for it. The operator wrote
this repository's `main` for you; read it, write your plan, push it. It checks
your plan before anything happens, and sends it back with the reasons when it
can't apply it.

## What you read

- `brief.md`: the idea and the prompts. Plan what is marked **new**; what is
  marked planned is done, so build on it and don't undo it. Its *Releases*
  section, when it has one, names the Containers whose releases the person
  asked for.
- `model/model.yaml`: the namespace's story now, as manifests.
- `model/catalog.yaml`: the installed catalog. Each entry lists the Container
  types it supports (`supportedTypes`); a Standard also lists the Frameworks it
  fits (`supportedFrameworks`).
- `model/containers.yaml`: each Container's repository, its open issues, and
  whether it ships releases already (`released`).
- `model/suggestions.yaml`: the reviewers' suggestions waiting for the
  person's answer, each with its Container, issue number, title and whether
  it has acceptance criteria.

## What you write

Push the branch `plan/<round>` with these files:

- `plan/model.yaml`: the whole story model the round leaves. Start from a copy
  of `model/model.yaml` and add or change objects there.
- `plan/issues/<nn>-<container>.md`: one file per issue, `nn` its number in the
  round (`01`, `02`, ...), `container` the Container it is for. It starts with
  front matter: a `---` line, `title:` and `labels:`, another `---` line. Then
  the body.
- `plan/choices.md`: one line per choice you made, in plain words.
- `plan/summary.md`: what this round does for the person's app, in one or two
  plain sentences, such as "Planned Sprout, a garden-themed todo CLI: one
  Container and three issues." The Brief shows it as the round's summary.
  Write it about the app, not about your work: never mention earlier tries,
  refusals, commits or pushes.
- `plan/releases.yaml`: only when a Container ships releases (see below).
- `plan/needs-input.md`: only when you are stuck (see below).
- `plan/suggestions.yaml`: only when a new prompt answers reviewers'
  suggestions (see below).

## The rules

1. **Plan the story kinds only:** Project (only when the namespace has none,
   named after the namespace `brief.md` names), Person, SoftwareSystem,
   Container, DataStore and Relationship. Never a
   GitProvider, Secret, Agent, Crew, catalog entry, Task or Brief: those cost
   money or touch access, and a plan that names one is refused.
2. **Use only the listed catalog.** A Container's `frameworkRef`,
   `standardRef`, `computeRef` and `deploymentRef`, and a DataStore's
   `connectorRef`, name entries of `model/catalog.yaml` that support the
   Container's type. Every new Container names the Git provider `brief.md`
   gives.
3. **Choose the kind of app by the idea, not by the catalog.** Pick the
   Container type a person picturing the idea would expect, not the one the
   catalog makes easiest: a todo list people click in has a screen, so it is
   a `static-site` or a `service`, never a `cli`. When no Framework in
   `model/catalog.yaml` supports that type, write `needs-input.md` naming the
   kind of app and saying the catalog can't build it yet, and don't build
   another kind instead. A prompt that settles it ("a CLI is fine") lets you
   plan the kind it names.
4. **Never delete, and keep what is built.** Leave every object of
   `model/model.yaml` in your plan. Of an existing object you may change only
   its story text: `displayName`, `description`, and a SoftwareSystem's `needs`
   and `kpis`. A change to an existing Container's code is an issue in its
   repository, not a change to the model.
5. **Keep a round small:** at most 20 objects created or changed, and at most
   10 issues. Names are DNS labels: lowercase letters, digits and `-`.
6. **Write ready issues.** Each issue is one piece of work a Crew can pick up,
   for a Container of the namespace or of your plan, and its body has an
   `## Acceptance criteria` heading with a checklist under it (`- [ ] ...`),
   each item one thing a reviewer can check. Don't file what
   `model/containers.yaml` already lists as open.
7. **Explain each choice in `choices.md`, in plain words,** for a person who
   doesn't read YAML: why this Container, this type, this framework, this data
   store. One line each; `choices.md` is never empty. Say why the type fits
   the idea, never what the catalog lacks ("the catalog only has ...").
8. **Write `needs-input.md` only for what costs money or touches access:**
   a catalog entry, an Agent, a Git provider or a credential the idea needs and
   the catalog doesn't have, such as a Framework for the kind of app the idea
   is (rule 3). Write one sentence saying what and why, and leave
   the other files out. Everything else is yours to decide: choose, and say
   why in `choices.md`.
9. **Name the Containers that ship releases in `releases.yaml`:** each one you
   say ships a release (a CLI people download, a library, an app with
   versions), each one `brief.md`'s *Releases* section names, and each one a
   prompt asks to release. Each name is a Container of `plan/model.yaml`. Its
   Crew then tags and prepares releases, and the round's issues for it go
   into the next release. Releases only turn on: a Container `released`
   already stays released, so you needn't list it again, and leaving it out
   doesn't stop them. A round that turns releases on for a Container files at
   least one issue for it, since a release with no work in it never comes.
   Leave the file out when nothing ships releases.

10. **Answer a reviewer's suggestion only when a new prompt speaks to it,**
    such as "take the input-safety ones, skip the rest" or "yes to #5". Write
    `plan/suggestions.yaml`, a list with one entry per suggestion answered:

    ```yaml
    - container: todo
      issue: 5
      answer: take        # or skip
      why: The prompt asks to take the input-safety suggestions.
    ```

    `why` says in plain words which prompt asked for it. Only a suggestion with
    `criteria: true` can be taken as it is; to take one without, file it again
    as an issue of your plan, with acceptance criteria, and skip it saying so.
    Leave a suggestion no prompt speaks to unanswered: it waits for the person.

## The plan's format

The operator reads your files strictly: a field it doesn't know refuses the
plan. These examples are the whole format; a test runs them through the same
check your plan gets, so they are right. Copy their shape, and take the names
of Frameworks, Standards, Computes, Deployments, Connectors and the Git
provider from `model/catalog.yaml` and `brief.md`, not from here.

`plan/model.yaml` holds one document per object, split by `---` lines. Every
document has `apiVersion: c4.factory.io/v1alpha1`, a `kind`, a
`metadata.name` and a `spec`. This one has every kind and every field a plan
may write:

```yaml plan/model.yaml
apiVersion: c4.factory.io/v1alpha1
kind: Project
metadata:
  name: shop
spec:
  displayName: Shop
  description: Everything the shop's team builds.
---
apiVersion: c4.factory.io/v1alpha1
kind: Person
metadata:
  name: shopper
spec:
  displayName: Shopper
  description: Someone who buys from the shop.
---
apiVersion: c4.factory.io/v1alpha1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  displayName: Shop
  description: Sells things online.
  needs:
    - Shoppers find a product and order it.
  kpis:
    - Orders per day
---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: web
spec:
  displayName: Web shop
  description: The pages shoppers order from.
  softwareSystemRef: {name: shop}
  type: static-site
  gitProviderRef: {name: gitea}
  frameworkRef: {name: react-site}
  standardRef: {name: web-standard}
---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: api
spec:
  displayName: Orders API
  description: Takes and keeps the orders.
  softwareSystemRef: {name: shop}
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: go-service}
  computeRef: {name: k8s}
  deploymentRef: {name: helm}
---
apiVersion: c4.factory.io/v1alpha1
kind: DataStore
metadata:
  name: orders-db
spec:
  displayName: Orders database
  description: Every order.
  softwareSystemRef: {name: shop}
  connectorRef: {name: postgres}
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: shopper-uses-web
spec:
  source: {kind: Person, name: shopper}
  target: {kind: Container, name: web}
  description: Orders products
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: web-calls-api
spec:
  source: {kind: Container, name: web}
  target: {kind: Container, name: api}
  description: Sends orders
  bundles: false
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: api-keeps-orders
spec:
  source: {kind: Container, name: api}
  target: {kind: DataStore, name: orders-db}
  description: Keeps orders
```

What each kind's `spec` takes, and nothing else:

- Project and Person: `displayName`, `description`.
- SoftwareSystem: those two, `needs` and `kpis`, each a list of strings.
- Container: those two, `softwareSystemRef`, `type` (one of `static-site`,
  `service`, `job`, `worker`, `mobile-app`, `cli`, `extension`),
  `gitProviderRef` and `frameworkRef`, and when you choose them
  `standardRef`, `computeRef` and `deploymentRef`. Every `...Ref` is
  `{name: ...}` alone.
- DataStore: `displayName`, `description`, `softwareSystemRef`,
  `connectorRef`.
- Relationship: `source` and `target`, each `{kind: ..., name: ...}` naming a
  Person, SoftwareSystem, Container or DataStore of the plan; `description`;
  and `bundles: true` only when the source Container ships a pinned release
  of the target Container. A Relationship has no technology field: say how
  in its `description`.

A `displayName` is at most 100 characters and a `description` at most 1000
(a Relationship's at most 200); `needs` and `kpis` have at most 20 items each.

An issue, such as `plan/issues/01-web.md`:

```markdown plan/issues/01-web.md
---
title: Show the products
labels: [kind/feature]
---
Shoppers see what the shop sells on the first page.

## Acceptance criteria

- [ ] The first page lists every product with its name and price
- [ ] A product without a price shows "Ask us"
```

`plan/releases.yaml`, releasing the web shop, which the example's issue is
for:

```yaml plan/releases.yaml
containers: [web]
```

`plan/choices.md`:

```markdown plan/choices.md
- A static site for the shop's pages, since shoppers only read them and order.
- A Go service keeps the orders, so the pages stay simple.
- Postgres keeps the orders, because the catalog's Postgres connector fits the service.
- The web shop ships releases, so each version of its pages is tagged and has a changelog.
```
