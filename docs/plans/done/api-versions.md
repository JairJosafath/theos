# Plan: a compatibility promise across versions, and `v1beta1`

GitHub issue #1082, filed by the maintainer (`source/dev`, priority/4,
milestone *1.0: a stable API*). Agent: otter-1082 (the plan). It follows
decision 1 of [before-clients-return.md](../before-clients-return.md): the
shapes `v1beta1` waited for, the ports (#1093) and the deploy model
(#1068), are settled, so `v1beta1` is cut once, with conversion from
`v1alpha1` for every kind. The promise within `v1alpha1` is written
(#1097, [design.md](../../design.md#compatibility)); this plan writes what
holds across versions and builds the second served version.

## Goal

A device that upgrades keeps every object it stored: `c4.factory.io` serves
`v1beta1` as its storage version beside `v1alpha1`, every object of every
kind reads and writes at either version through a conversion the operator
serves, and a written promise says what a served version keeps, how one is
deprecated and when one may be dropped. The first roadmap criterion of 1.0
("a written compatibility promise for `c4.factory.io`, and a served version
past `v1alpha1` that existing objects convert to") holds.

Done when every step below is merged: the promise is in `docs/design.md`;
every kind serves `v1alpha1` and `v1beta1` with `v1beta1` stored;
envtest creates each kind at `v1alpha1`, reads it at `v1beta1` and back
unchanged, and rewrites an object stored at `v1alpha1`; `test/compat`
guards both versions; the operator, the UI, the Session client, the
planner and the samples speak `v1beta1`; `v1alpha1` is marked deprecated
with a warning naming the release that may drop it. Test output goes to
`docs/screenshots/`.

## Where we stand

- Every kind in `api/v1alpha1` is served and stored at `v1alpha1` only, no
  `conversion` stanza (`strategy: None` by default), no webhook server
  running: `cmd/main.go` has kubebuilder's webhook flags, the chart has
  `webhook.enabled: false` and `certManager.enabled: false`, and nothing
  issues a certificate.
- `test/compat` compares `config/crd/bases` with the last release's copy
  in `test/compat/baseline/`, one version per CRD assumed.
- `c4.factory.io/v1alpha1` is written in the UI (`ui/src/k8s/types.ts`,
  `VERSION`), the planner (`model.PlanAPIVersion`, what a Brief's plan
  says), the samples, the seed, the e2e fixture, the templates' docs and
  the guides; the controllers import `api/v1alpha1` in about 2,700
  places; the admission policy matches `apiVersions: ["*"]`.
- The one deprecated field is `Crew.spec.publishReleases` (#1070): the
  design says dropping it is `v1beta1`'s, and a Brief keeps writing it on
  its Crews until a Brief can name DeployTargets, a planner follow-up
  [deploy.md](deploy.md) put after #1070 to #1072, two of which are
  open.

## Decisions

1. **The promise across versions** (written into `docs/design.md`,
   "Compatibility", by step 1):
   - A served version keeps the additive rules of `v1alpha1` from the
     first release that serves it; a change those rules forbid is a new
     version, never a change to a served one.
   - Every served version converts to and from the storage version without
     failing and without loss: a field with no home in the target version
     rides in the annotation `c4.factory.io/<version>-fields` as JSON, so a
     round trip gives the object back exactly. A kind's conversion is
     tested as a round trip over fuzzed objects of every served version.
   - A release deprecates a version by saying so under *Deprecated* in its
     notes and marking the CRD version `deprecated: true` with a
     `deprecationWarning` that names the successor and the earliest release
     that may drop it. The API server sends the warning on every request
     at that version, so `kubectl` prints it; the UI shows it on the
     project page when any object it watches came in at a deprecated
     version.
   - A deprecated version stays served for at least 90 days after the
     release that deprecated it, and is dropped only when every stored
     object has been rewritten to the storage version (`storedVersions`
     of every CRD holds the storage version alone) and the notes say so
     under *Removed*. The drop is its own issue, filed when the version is
     deprecated, with the earliest date in its body.
   - `test/compat` guards both the schema of each served version against
     the baseline's and that no version the baseline served is missing; a
     pull request that drops one names it in `test/compat/dropped.txt`
     with the release notes' line, where review sees it.
   - What the promise leaves out stays as #1097 wrote it: what the operator
     does with an object may change, as the release notes say.

2. **`v1beta1` changes no shape.** Its kinds are `v1alpha1`'s, field for
   field, with `v1beta1` the hub and `v1alpha1` the spoke. The one
   candidate, dropping `Crew.spec.publishReleases`, waits on a Brief naming
   DeployTargets, which waits on two open plans (#1071, #1072); coupling
   the 1.0 criterion to them would hold it for months, and a `v1beta1` that
   serves the deprecated field costs a user nothing. So the field stays in
   `v1beta1`, deprecated as it is in `v1alpha1`, and the version after
   `v1beta1` drops it once nothing writes it; step 1 rewords the two lines
   in `docs/design.md` and [github-deploy.md](github-deploy.md) that said
   `v1beta1` would. A shape change found while building `v1beta1` is filed
   `status/proposed` for the next version, never slipped into this one.
   Why cut a version that changes nothing: the machinery a breaking
   change needs (the webhook, its certificate, the storage migration, the
   deprecation and drop rules, every client's move) is built and proven
   while nothing depends on it being right the first time, and the
   promise can be kept from then on. Kubernetes did the same with many of
   its own groups.

3. **Conversion is a webhook the operator serves, with a certificate it
   issues itself, and a CRD uses it only while its kind's versions differ
   in shape.** The webhook, the certificate, the chart wiring and the
   envtest wiring are built and proven now, so the first real change
   brings none of them. But a webhook in the read path costs: discovery
   prefers `v1beta1` while storage is `v1alpha1`, so `kubectl`, dynamic
   clients and the namespace controller read through `/convert`, and
   while the operator is down, or after `helm uninstall` with `crd.keep`
   left the CRDs pointing at a Service that is gone, `kubectl get` of
   that kind fails and deleting a Project namespace hangs in Terminating
   (#1830). So while a kind's versions are field for field the same, as
   every kind's are after step 3, its CRD stays at `strategy: None`: the
   API server converts by rewriting `apiVersion`, and nothing in the read
   path depends on the operator. The change that first makes a kind's
   shape differ (its first hook in the generic converter) also marks its
   hub type `+kubebuilder:metadata:annotations="c4.factory.io/conversion=webhook"`;
   the annotation lands on the base CRD, kustomize's conversion patch
   selects on it, the chart's generator (`hack/chart-crds.sh`) reads it,
   and a unit test in `internal/controller` fails when the mark and the
   versions' schemas (descriptions aside) disagree, either way. Such a
   kind's CRD gets `conversion.strategy: Webhook` pointing at the
   manager's Service, path `/convert`, served by controller-runtime's
   conversion handler from `conversion.Hub` on each `v1beta1` type and
   `Convertible` on each `v1alpha1` type; its cost is the one above
   (reads need the operator, and the chart's `crd.keep` uninstall leaves
   it unreadable until the chart is installed again or the CRD's
   conversion set back to `None` by hand), said in the chart's README
   beside `crd.keep`; the chart refuses to render it with
   `webhook.enabled: false`, since `None` would lose what differs. Why
   not a pre-delete hook that rewrites kept CRDs: it covers uninstall, not
   a crashed operator. Why not hold `v1beta1` back until a shape differs:
   it is served, and the machinery is proven only by running. One generic converter in `api/v1alpha1/conversion.go`
   does the work for every kind (an unstructured round trip, since the
   shapes match) with a per-kind hook for differences, empty now, and the
   `-fields` annotation of decision 1 for a field the hook can't place.
   The certificate: at start, before the manager runs, the operator reads
   the Secret `<release>-webhook-cert` in its namespace, or makes a CA and
   a serving certificate for `<service>.<namespace>.svc` (valid one year)
   and creates the Secret; it re-issues when under 30 days remain; a second
   replica reads what the first wrote (create, then get on conflict). It
   then patches the CA into the `conversion` stanza of every
   `c4.factory.io` CRD. The generated role gains `secrets` get/create/update
   in its own namespace and `customresourcedefinitions` get/patch by name.
   Readiness includes the webhook server's started check, so the Service
   has the pod before any cache needs a conversion, and controller-runtime
   starts the webhook server before the caches. cert-manager stays
   optional and unbuilt: `certManager.enabled` keeps meaning what
   kubebuilder's chart gives it, and a device that wants it files an issue.
   envtest installs the CRDs with `WebhookInstallOptions`, which rewrites
   the stanza to the suite's own URL and CA, so every envtest spec runs
   with conversion on. What only Kind shows (the Service, the certificate
   under a real node, the rotation) is said in the pull requests for a
   human run.

4. **`v1beta1` becomes the storage version, and the operator rewrites what
   was stored at `v1alpha1`.** Once every CRD stores `v1beta1`, objects
   written before the upgrade stay at `v1alpha1` in etcd until something
   writes them. The operator does it: at start, whatever the webhook
   setting (#1884), a migrator lists each kind at the hub version, page by
   page (a kind that converts by the webhook waits for it to be ready
   first; one at strategy `None` converts in the API server), and issues
   an empty patch for each object whose CRD's `storedVersions` still lists
   a version other than the storage one; when a kind's objects are done it
   patches that CRD's `status.storedVersions` down to the storage version.
   It runs under leader election, after the certificate, and is idempotent;
   a device with thousands of objects sees one pass of patches per upgrade
   that changes the storage version and nothing otherwise. This is what
   makes dropping `v1alpha1` later safe; the drop itself is not this
   plan's.

5. **Every client speaks the storage version.** The controllers import
   `api/v1beta1` (an import rename, one mechanical pull request); the UI's
   `VERSION`, the Session client, `model.PlanAPIVersion`, the samples, the
   seed, the e2e fixture, the guides and the templates' docs say
   `v1beta1`. The operator reads and writes the hub only; a client still
   writing `v1alpha1` works through conversion and gets the warning. The
   generated RBAC role does not change: it names resources, not versions.

6. **`v1alpha1` is deprecated in the release that serves `v1beta1`.** Its
   CRD versions carry `deprecated: true` and the warning of decision 1,
   the notes say *Deprecated*, and the follow-up to drop it is filed
   `status/proposed` with its earliest date, 90 days after that release.

## Steps

Each step is one issue, `status/ready`, in this order; each says `Depends
on` the one before. Steps 2 to 6 join milestone *1.0: a stable API*.

1. **The promise across versions, written.** `docs/design.md`
   "Compatibility" gains decision 1 (the served-version rules, conversion
   without loss, deprecation, the 90 days, the drop); its line on
   `publishReleases` and the one in `github-deploy.md` say the version
   after `v1beta1` drops it (decision 2); ROADMAP's criterion links the
   section. `kind/docs`. Done when the section says each rule above and
   `make check` passes.

2. **The operator serves a webhook with a certificate it issues.**
   Decision 3 without the types: the self-issued CA and serving
   certificate in the Secret, re-issued under 30 days, the CRD `caBundle`
   patch, the role's new rules, the chart and kustomize wiring (the
   manager Service's webhook port, `webhook.enabled: true` by default),
   readiness. The CRDs keep `strategy: None` until step 3. `kind/feature`.
   Done when a unit test covers issue, reuse and re-issue of the
   certificate (`#<step>` named), an envtest shows the CRDs patched with
   the CA and the role allowing it, `make validate-yaml` passes the
   chart, and the pull request says what a human's Kind run would check.
   Built (#1614) with two choices the decision left open: the CA lasts ten
   years and a re-issue keeps it, renewing only the serving certificate, so
   a replica that hasn't re-read the Secret stays trusted by the
   `caBundle` (only the CA's own renewal, once a decade, leaves another
   replica untrusted until its daily recheck); and the CA goes only into CRDs at `strategy: Webhook`, since
   the API server refuses a webhook stanza beside `None` (step 3 flips
   them). The generated role is cluster-wide, so its `secrets` rule already
   covers the namespace; no namespaced Role was added. With
   `networkPolicy.enabled` the chart opens the webhook port to any source:
   the API server calls from outside the pod network, and TLS guards it.

3. **`api/v1beta1` for every kind, converting.** The kinds a new version
   covers are `WebhookCRDs` in `internal/controller/webhook_cert.go`, the
   list its unit test pins to `config/crd/bases` (no fixed count: Bundle
   made it 19, #1829). Each of them is scaffolded with
   `kubebuilder create api --version v1beta1 --kind K --resource
   --controller=false`, its types copied from `v1alpha1` with the hub
   marker, `Convertible` on `v1alpha1` through the generic converter, the
   conversion handler registered, every CRD at `strategy: Webhook` (set
   back to `None` by #1830: a kind converts by the webhook only once its
   shape differs), storage still `v1alpha1`. `kind/feature`. Done when
   a round-trip test over fuzzed objects of every kind passes, envtest
   (with `WebhookInstallOptions`) creates each kind at `v1alpha1`, reads it
   at `v1beta1` and back unchanged, and `make check` passes with every
   existing spec unchanged.

4. **`v1beta1` stored, and the migrator.** The storage marker moves to
   `v1beta1`; the migrator of decision 4; `test/compat` compares per
   version, refuses a missing one unless `test/compat/dropped.txt` names
   it, and its baseline gains the second version at the next release.
   `kind/feature`. Done when an envtest installs the CRDs storing
   `v1alpha1`, creates an object, moves the storage version, runs the
   migrator and sees `storedVersions` down to `v1beta1`; a `test/compat`
   case fails on a dropped version and passes on an added one.

5. **The operator and the planner speak `v1beta1`.** The controllers,
   `internal/agentrunner`, the seed, the e2e fixture and
   `model.PlanAPIVersion` move to `v1beta1`; the Session client too.
   `kind/chore`. Done when `grep -rn v1alpha1 internal cmd hack/seed
   test/e2e` finds only the conversion and the compat baseline, and `make
   check` passes.

6. **The UI, the samples and the guides speak `v1beta1`, and `v1alpha1`
   is deprecated.** `ui/src/k8s/types.ts` and the fixtures, the samples,
   `test/invalid`, the guides and the templates' docs; the UI shows the
   deprecation warning it receives; the CRDs mark `v1alpha1` deprecated
   with the warning; the drop follow-up filed `status/proposed`.
   `kind/feature`. Done when `make check` and `make ui-test` pass, a UI
   test shows the warning from a watch response's `Warning` header, and
   `docs/screenshots/` holds the test output of steps 3, 4 and 6.
   Built (#1618) with three choices the decision left open: the UI shows
   whatever `Warning` header the API server puts on the list response of
   each watcher (the ndjson mux `/k8s-watch` carries no headers), one
   banner per distinct text on the project, story and factory pages,
   cleared on a relist without one; since the UI itself reads `v1beta1`,
   the `v1alpha1` warning reaches kubectl and clients at that version, and
   the banner shows the day a version the UI reads is deprecated in turn; the deprecating release is named v0.30.0
   in the marker (so the earliest drop is 2027-01-22, #1963); and the
   release notes read *Deprecated* and *Removed* entries from a merged
   commit's body lines `Deprecated: <text>` and `Removed: <text>`, whatever
   the pull request's type. Test output: `docs/screenshots/api-versions-step3-conversion.txt`,
   `api-versions-step4-storage.txt` and `api-versions-step6-ui-deprecation.txt`.

## Not in this plan

- Dropping `v1alpha1` (its own issue, filed by step 6 with the date).
- Dropping `Crew.spec.publishReleases` and a Brief naming DeployTargets:
  the next version's, after #1071 and #1072.
- cert-manager for the webhook's certificate; a validating or defaulting
  webhook (the roadmap's "later").
- Any shape change: one found on the way is filed `status/proposed`.
