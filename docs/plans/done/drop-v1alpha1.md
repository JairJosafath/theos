# Plan: drop `theos.factory.io/v1alpha1` before the first release

theos issue #24, from [c4factory#1963](https://github.com/JairJosafath/c4factory/issues/1963).
The owner's decision: drop it now, before theos's first release, rather than
wait for the date the 90 days of [Compatibility](../../design.md#compatibility)
would give.

## Why now

`theos.factory.io` is a new API group: an install of c4factory isn't
converted, theos is installed fresh ([theos-rename.md](theos-rename.md)). No
release has ever served `theos.factory.io/v1alpha1`: c4factory's v0.30.0
deprecated `c4.factory.io/v1alpha1`, another group. So no install stores an
object at it, and the 90-day promise covers no theos user. The promise
itself stays, for the version after `v1beta1`.

## Decisions

1. **Every kind is served at `v1beta1` alone.** `api/v1alpha1` goes: its
   types, the generic converter and its round-trip test, the envtest specs
   that read each kind at both versions, the scheme entries in
   `cmd/main.go` and the suite, and its `PROJECT` resources (by hand, as
   the rename did: kubebuilder has no "delete api"; each kind's
   `controller: true` moves to its `v1beta1` entry). `api/v1beta1`'s `Hub()`
   methods go too: a hub needs a spoke, and the next version, as the
   storage version, becomes the hub. The three kinds whose shapes differed
   (Container, Crew, Task) lose the `theos.factory.io/conversion=webhook`
   mark, since a CRD that serves one version converts nothing.
2. **The conversion machinery stays, since it is generic and cheap.** The
   webhook server, its self-issued certificate (`WebhookCert`), the
   `/convert` handler, the chart's `webhook.enabled`, the kustomize patches,
   `hack/chart-crds.sh`'s reading of the mark, the storage migrator and
   their tests are what api-versions.md built so the first real change
   brings none of them; they hold nothing of `v1alpha1`. With no CRD at
   strategy `Webhook`, nothing in the read path depends on the operator,
   and the cost left is a webhook port and one Secret. The converter is
   the one piece that leaves with `api/v1alpha1`: it lived in the spoke's
   package, and the next spoke brings it back from the history.
3. **`WebhookCRDs` is every CRD of the group.** The `bornAtV1beta1` set
   (Chat, Scorer, Benchmark) existed only because those kinds served one
   version while the rest served two; now every kind does, so the list
   and the RBAC markers of the webhook certificate and the migrator name
   every CRD, and `TestWebhookCRDsAreEveryCRD` says so. The operator gains
   `patch` on chats, scorers and benchmarks, for the migrator's empty
   patch the day their storage version moves.
4. **The migrator's envtest adds the next version.** With one version per
   CRD, the spec adds `v1` beside `v1beta1` on the Person CRD (strategy
   `None`), stores objects at `v1beta1`, moves the storage to `v1` and runs
   the migrator under the generated role; the wait for the webhook on a
   CRD at strategy `Webhook` moves to a fake-client unit test, since
   envtest has no webhook-converted kind left to wait on.
5. **`test/compat` keeps its baseline.** The baseline is c4factory's
   v0.30.0 CRDs, renamed; it serves `v1alpha1`, so `test/compat/dropped.txt`
   names `v1alpha1` with the release notes' line, and the commit carries a
   `Removed:` line for the notes. `hack/release.sh`'s first theos release
   replaces the baseline and empties the entry.
6. **A plan at `v1alpha1` is refused.** `model.planAPIVersions` names the
   served versions, `v1beta1` alone now.

## Done when

`grep -rn v1alpha1` finds only history (`docs/plans/done`, design history,
screenshots, the compat baseline, the docs that say it was dropped),
`dropped.txt` and `test/compat`'s own cases, the release-notes fixtures, the
UI's fixtures of a deprecation warning, the refused-input cases, Argo CD's
own `v1alpha1` and the kubebuilder template's default for apps; every CRD
serves `v1beta1` alone (`TestEveryCRDServesV1beta1Alone`); `test/compat`
passes only with `dropped.txt` naming the version; the migrator and
webhook certificate specs pass. Test output:
[../../screenshots/1963-drop-v1alpha1.txt](../../screenshots/1963-drop-v1alpha1.txt).
