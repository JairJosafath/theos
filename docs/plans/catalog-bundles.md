# Catalog bundles: a Framework, Standard, Compute and Deployment picked together

Issue: #1092. Written 2026-10-22, after the deploy model
([done/deploy.md](done/deploy.md), decision 1) settled what a bundle can hold:
a `Deployment` stays a pinned template any number of Containers share, and the
place a Container ships to is a `DeployTarget` the Container names in
`spec.deploy`. So a bundle names catalog entries only.

## Goal

A team writes down its choice once, as a named bundle: one Framework, one
Standard, one Compute and one Deployment that fit together. A Container, or
the Brief's planner, names the bundle instead of the four entries. The
operator expands it, checks the expansion the way it checks four entries
named by hand, scaffolds from it once, and the Container's status says what
the bundle expanded to.

## Where things stand

- A Container names its sides one by one: `frameworkRef` (required),
  `standardRef`, `computeRef`, `deploymentRef` (`api/v1alpha1/container_types.go`).
  `resolve` (`internal/controller/container_resolve.go`) loads the entries;
  `model.CheckChoices`, `CheckStandard` and `CheckDeploysItself` check them;
  about 25 places in `internal/controller` read `Spec.*Ref` afterwards
  (scaffold, checks, the Standard update, deploys, protection).
- A catalog kind (Framework, Standard, Compute, Deployment, Connector) is a
  template at a pinned ref plus `supportedTypes`; none has a reconciler. The
  chart installs them from `catalog.entries` (`dist/chart/templates/gitea/_catalog.tpl`),
  and `make validate-yaml` pairs each with `templates/versions.txt`.
- The planner reads `model/catalog.yaml` (`workspaceCatalog`,
  `internal/model/brief_workspace.go`) and its guide
  (`internal/model/planner.md`) tells it to pick each side; the plan check
  (`internal/model/brief.go`) and the stand-in planner
  (`brief_standin.go`) pick and check sides the same way.
- The UI has a page per catalog kind (`ui/src/components/objects/CatalogPages.tsx`).
- A served version keeps `v1alpha1`'s additive rules
  ([api-versions.md](api-versions.md), decision 1): a new kind and a new
  optional field are allowed; a required field turning optional is too.

## Decisions

1. **A bundle is its own kind, `Bundle`,** a catalog entry beside the five
   others: namespaced, owned by the Project, no template and no ref of its
   own. Its spec names `frameworkRef` (required), `standardRef`,
   `computeRef` and `deploymentRef` (optional), with `displayName` and
   `description`. It never names a Connector (those come through
   Relationships), a DeployTarget or a credential. Not a field on Framework
   (a Framework's "defaults"): a bundle is a team's choice, and a team may
   keep two bundles on one Framework. Not a label or annotation: the CRDs
   are the truth, and a schema can be validated.
2. **No reconciler, no status.** Like every catalog entry, a Bundle is
   checked where it is used. What a Bundle supports is the intersection of
   its entries' `supportedTypes`, computed where it is read: by the operator
   for the planner's catalog and by the UI for its page. A Bundle whose
   entries don't fit each other fails the Containers that name it, naming
   the bundle, as four mismatched refs fail them today.
3. **A Container names a bundle or its sides, never both.** `spec.bundleRef`
   is new and optional; `frameworkRef` turns optional; a CEL rule asks for
   exactly one of `bundleRef` and `frameworkRef`, and refuses `standardRef`,
   `computeRef` or `deploymentRef` next to `bundleRef`
   ("bundleRef replaces frameworkRef, standardRef, computeRef and
   deploymentRef"). No overrides: a Container that wants one side different
   names the four sides, and the bundle stays what the team wrote. Like
   `softwareSystemRef`, `bundleRef` is immutable.
4. **The operator expands at resolve and pins at scaffold.** `resolve` loads
   the Bundle (missing: `Pending`, `ReferencesResolved=False`, `NotFound`,
   "Bundle web-service"), then its entries as if the Container had named
   them. The expansion is written to `status.bundle`:
   `{name, framework, standard, compute, deployment}`, entry names, with the
   pinned refs in `scaffoldedFrom` as today. Until the Container is
   scaffolded, every reconcile expands the Bundle afresh, so a fix to the
   Bundle reaches a `Pending` or `Failed` Container. From the scaffold on,
   the operator reads `status.bundle` and never the Bundle again: that is
   "scaffold once". A method on the API type, `Container.Choices()`, gives
   the effective names (the spec's refs, else `status.bundle`), and every
   reader after resolve uses it instead of `Spec.*Ref`.
5. **The same checks, naming the bundle.** The expanded entries go through
   `CheckChoices`, `CheckStandard` and `CheckDeploysItself` unchanged; when
   the Container named a bundle, the message starts with it
   ("bundle web-service: python-fastapi does not support static-site";
   "bundle aws-fn: aws-lambda-ts deploys itself ... remove computeRef
   small"), and the reasons stay `UnsupportedType`, `UnsupportedFramework`,
   `DeploysItself`. The fix is to the Bundle, or to name another.
6. **A changed Bundle is reported, never applied.** A Ready Container whose
   Bundle now names other entries than `status.bundle` gets
   `BundleCurrent=False`, reason `BundleChanged`, listing the sides that
   differ; `True` otherwise, and absent on a Container that named its sides.
   The operator changes nothing: a Standard bump still reaches the Container
   through the Standard, and the other sides are scaffold-once. A deleted
   Bundle leaves a Ready Container as it is (`BundleCurrent=False`,
   `BundleChanged`, "Bundle web-service is gone").
7. **The planner prefers a bundle.** `model/catalog.yaml` gains `bundles:`,
   each with its name, description, entries and the computed
   `supportedTypes`; the guide says: name a bundle that supports the
   Container's type when one does, else the sides. The plan check expands a
   `bundleRef` through the same `model` function the operator uses, so the
   round fails with the same message the Container would. The stand-in
   planner picks a bundle when one fits the type, so the tests cover the
   path.
8. **The chart ships bundles beside entries,** in `catalog.bundles` (no
   template, so not in `catalog.entries` and not paired with
   `versions.txt`); `make validate-yaml` checks each bundle names entries the
   chart installs. Two to start: `python-service` (python-fastapi,
   python-standard, small, k8s-manifests) and `go-cli` (go-cli, go-standard,
   gitea-release). The webshop sample and the first-app sample keep naming
   their sides, so both paths stay exercised.

## Steps

1. **The `Bundle` kind** (`kubebuilder create api`, no controller): spec,
   printcolumns, CEL (`frameworkRef` required, the three optional), samples
   under `config/samples/`, `make validate-yaml`, the design's model table
   (nineteen kinds) and `docs/dev/structure.md`. `internal/model`:
   `BundleChoices` expanding a bundle's names and the computed
   `supportedTypes`, table tests.
2. **A Container names a bundle:** `spec.bundleRef`, `frameworkRef`
   optional, the exactly-one CEL rule with its failing cases in envtest;
   `resolve` expands and writes `status.bundle`; `Container.Choices()` and
   every `Spec.*Ref` reader in `internal/controller` on it; the checks name
   the bundle; envtest: a bundled Container scaffolds with the same
   `scaffoldedFrom` as its hand-named twin, a Bundle changed before the
   scaffold is re-expanded, one changed after is not.
3. **The Container reports a changed Bundle:** `BundleCurrent`, the watch on
   Bundles, envtest for changed and deleted.
4. **The planner picks bundles:** `catalog.yaml`'s `bundles:`, the guide,
   the plan check, the stand-in planner; `internal/model` tests.
5. **The chart's bundles:** `catalog.bundles`, the seed, `validate-yaml`'s
   check, `make test-gitea` and `make seed-local` where the seed changes.
6. **The UI:** a Bundles catalog page (entries, computed types, the
   Containers naming it) and the Container page showing `bundleRef`, the
   expansion and `BundleCurrent`; `make ui-test`.

Step 2 depends on 1; steps 3 to 6 depend on 2.

## Done when

Every step's pull request has landed with its tests, `make check` green: a
Container that names `python-service` is Ready with the same
`scaffoldedFrom` as one naming the four entries, its status shows the
expansion, and a stand-in Brief plans a Container on a bundle.
