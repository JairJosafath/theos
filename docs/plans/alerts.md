# An alert from a running app files a bug

GitHub issue #64, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-13 in a session the owner ran.

## Goal

The loop's last stage listens to the tools that watch a running app. An
alert from Alertmanager, an issue alert from Sentry, or a small JSON body
from any other tool becomes one `kind/bug`, `source/monitor` issue in the
repository of the Container it concerns, and goes to triage like any issue.
The same alert firing again is counted on that issue, a storm becomes a
handful of issues and one "and 40 more", and nothing is fixed outside the
normal loop.

theos still monitors nothing itself ([design.md](../design.md), "Out of
scope"): it scrapes no app and holds no alert rules. It takes deliveries.

## Where we stand

- **Monitor means one thing today:** a deploy its target's tool reports
  `Degraded` or `Failed` files a bug (`model.DeployMonitorDue`,
  `internal/controller/container_deploy_monitor.go`). An app that deploys
  healthy and then throws errors files nothing.
- **A receiver exists, for the Git host alone:** a plain HTTP listener in
  the operator (`internal/controller/git_events.go`,
  `--git-events-bind-address`, off by default), `POST
  /git-events/{namespace}/{name}`, each delivery checked against a Secret
  ([done/git-events.md](done/git-events.md)). A delivery there is a hint:
  the reconcile reads the host again. An alert is not a hint. Nobody can be
  asked again what fired, so the delivery is the fact.
- **Issue text is data.** A prompt puts an issue's text between two marker
  lines that grow past any copy of themselves in it
  (`internal/agentrunner/prompt.go`, `issueBlock`). An alert's text comes
  from further away than a person's issue: from a monitoring tool, and
  through it from whatever the app logged, a visitor's input included.

## Decisions, and why

1. **An `AlertSource`, a small kind in the Project's namespace,** names one
   sender, as a GitProvider names one Git host. It has no parent in `spec`:
   like a GitProvider and a DeployTarget it belongs to the namespace, and
   any number of Containers share it.

   `spec`:

   | Field | What | Default |
   | --- | --- | --- |
   | `type` | `alertmanager`, `sentry` or `generic`; immutable, since the type decides what makes two deliveries the same alert | required |
   | `secretRef.name` | a Secret in the namespace that checks each delivery (decision 3) | required |
   | `containerLabel` | the label, tag or field of an alert that names its Container; at most 100 characters | `theos_container` |
   | `routes[]` | at most 32 of `container` and exactly one of `namespace` or `project`: where alerts go that name no Container (decision 4) | none |
   | `commentInterval` | the least time between two comments about one alert, and between two "and N more"; a duration of at least `1m` | `1h` |
   | `dailyLimit` | issues the source files for one Container in one UTC day, 1 to 100 | `5` |

   `status`, written by the AlertSource's reconciler alone:

   | Field | What |
   | --- | --- |
   | `conditions` | `Ready`: `Receiving`, or false with `SecretMissing` (no Secret, or none of the keys its type reads) or `ReceiverOff` (the operator's listener is off) |
   | `url` | where the sender delivers: the path, after the operator's `--git-events-url` when that is set |
   | `deliveries`, `lastDeliveryTime` | deliveries taken, and the last |
   | `unmatched` | `count`, `lastTime` and `message`: alerts that named no known Container, and why the last one didn't (`model.AlertContainer`'s second result) |
   | `alerts[]` | at most 100 (`model.MaxAlertRecords`), keyed by `key`: `container`, `name` (the alert's, cut to 120 characters, for people), `issue` (`number`, `url`), `folded`, `resolved`, `filedAt`, `lastSeen`, `count`, `reported`, `reportedAt`, `saidResolved`: `model.AlertRecord` field for field |
   | `containers[]` | keyed by `name`: `date`, `filed`, `lastIssue`, `folded`, `foldReported`, `foldReportedAt`: `model.AlertDay` field for field |

   The counts live here because the source is what counts: a Container's
   status has one writer, its own reconciler, and a second would race it.
   The price is that the daily limit is per Container *and source*: two
   sources that name the same Container each file up to their limit. A
   project normally has one source per tool, and the limit is a setting.

   Status holds no text of an alert but its name: text from an app's errors
   doesn't belong in an object every reader of the namespace sees, and a
   storm would grow it. A delivery nobody authenticated writes nothing to
   the API server; it is counted in a metric, `theos_alert_deliveries_total`
   by `result`.

2. **The receiver is the one that takes the Git host's deliveries,** on the
   same listener and Service: `POST /alerts/{namespace}/{name}`
   (`model.AlertsPath`, `model.AlertsURL`). No flag of its own: an
   AlertSource is the switch, and without one every path there answers 404.
   It reads at most 1 MiB (413 past it), finds the AlertSource (404),
   checks the delivery (401), parses it (400 for a body that isn't the
   type's shape), puts its alerts in the source's inbox and answers 202. It
   answers before anything is filed because Sentry gives a webhook one
   second, and a Git host that is down must not make a sender retry.

   The inbox is memory, at most 1000 alerts a source (503 past it, so the
   sender retries). The AlertSource's reconciler drains it: for each alert
   the Container (decision 4), the facts, `model.AlertStep`, the call to
   the Git host, then one status write, and only then is the alert dropped
   from the inbox. A failed call leaves it there for the next pass. What
   this doesn't promise: an alert taken in the second before the operator
   stops is not filed until its sender repeats it, which Alertmanager does
   every `repeat_interval` while the alert fires and Sentry at the rule's
   action interval while events arrive.

3. **Every delivery is authenticated, by what its sender can do.** The
   Secret is the user's: it holds what the sender holds too.

   | Type | The delivery carries | Secret key |
   | --- | --- | --- |
   | `alertmanager` | `Authorization: Bearer <token>` (Alertmanager's `http_config.authorization`; it cannot sign) | `token` |
   | `sentry` | `Sentry-Hook-Signature`, the hex HMAC-SHA256 of the body with the integration's client secret | `secret` |
   | `generic` | `X-Theos-Signature: sha256=<hex HMAC-SHA256 of the body>` when the Secret has `secret`; else `Authorization: Bearer <token>` | `secret`, else `token` |

   Comparisons are constant-time. No type takes an unauthenticated
   delivery and no setting turns the check off. A replayed delivery is one
   more delivery of the same alert: counted, never a second issue, so no
   timestamp is checked. The listener is plain HTTP inside the cluster, as
   for the Git host; a sender outside it (Sentry's cloud) comes through an
   Ingress with TLS, which step 4 gives a chart value. Steps 3 and 4 read
   the `secrets` skill first.

4. **An alert finds its Container in this order** (`model.AlertContainer`):
   1. the Container it names itself, by `containerLabel`: an Alertmanager
      label, a Sentry tag, a generic body's `container` field or label. A
      name that is no Container of the namespace is unmatched, never
      second-guessed;
   2. the first of `routes` whose `namespace` or `project` is the alert's;
   3. the alert's namespace (the `namespace` label, as Prometheus's
      Kubernetes metrics carry it): the one Container whose `spec.deploy`
      names an `argoCD` DeployTarget with that destination namespace. Two
      Containers there is unmatched, and the message says to name one;
   4. the alert's project (Sentry's slug): the Container of that name.

   Unmatched alerts are counted in `status.unmatched` and file nothing. A
   delivery whose key has a record takes the record's Container: a
   resolution needs no label.

5. **Alertmanager** (`model.ParseAlertmanager`): one alert for each entry
   of `alerts`, the first 100 of a delivery; the rest, and
   `truncatedAlerts`, are counted. The key is a digest of the alert's
   labels, which is what Alertmanager's own `fingerprint` is; we compute it
   so an alert means the same whether or not a version sends the field. A
   repeat, a resolution and a change of annotations are the same alert;
   another label value is another. `status: resolved` resolves, with
   `endsAt`.

6. **Sentry** (`model.ParseSentry`): an internal integration with *Alert
   Rule Action* on. Its issue alert (`Sentry-Hook-Resource: event_alert`)
   is one firing alert, keyed by the Sentry issue (`issue_id`), so every
   event of an issue is the same alert. Sentry sends no "resolved" for an
   alert rule; when the integration also subscribes to *issue* webhooks,
   `issue` with action `resolved` resolves that alert. Every other
   resource and action is answered 202 and does nothing, so Sentry never
   sees a failing integration.

7. **Generic** (`model.ParseGenericAlert`) is our own shape, for a tool
   that can post JSON it is told the shape of: one object, one alert a
   delivery, `title` required, and optionally `key` (what makes two
   deliveries the same alert; the title when absent), `status` (`firing`,
   the default, or `resolved`), `summary`, `description`, `container`,
   `namespace`, `startsAt`, `endsAt` (RFC 3339), `url`, `labels` and
   `annotations`. One alert a delivery because webhook templates write one
   object easily and lists badly.

8. **A firing alert files an issue when it has none to go on with**
   (`model.AlertStep`): it is new, its issue was closed on an earlier day,
   or it was folded on an earlier day. With an open issue it is counted.
   With an issue closed today it is counted too: an alert files at most
   once a UTC day, so an alert triage closes as not worth fixing costs one
   triage a day, not one a delivery, and a fix that didn't hold files a
   new bug the next day. Closing an issue doesn't silence an alert; that
   is done where the alert comes from, and the issue says so. Filing
   first looks for the alert's marker among the repository's open
   `source/monitor` issues (decision 12), so a status write lost after
   filing doesn't file a second.

9. **Repeats are counted on delivery and said by a rule of their own**
   (`model.AlertRecordOwed`): an open issue is owed a comment when its
   alert fired since the issue last said so and `commentInterval` has
   passed since then. Filing is the issue's first word, so the first count
   comes an interval after it. The reconciler asks after each delivery and
   when the
   wait it was told has passed, so "at most once an hour" also means "no
   later than an hour": a count doesn't wait for the next delivery.

10. **The daily limit folds the rest** (`model.AlertStep`,
    `model.AlertDayOwed`): past `dailyLimit` issues for a Container in a
    UTC day, a new alert files nothing and is counted on the day. The last
    issue filed that day gets "And N more alerts fired for this Container
    ...": at once for the first, then at most once a `commentInterval`
    with the day's count, and once more when the day is over. A storm of
    45 is five issues, of which the fifth says "and 40 more"
    (`TestAlertStorm`); `dailyLimit: 1` makes it one issue. A folded alert
    that still fires the next day gets its issue then.

11. **Resolved is said once and closes nothing.** The alert's open issue
    gets a comment that its source reports it resolved, with what fired
    unsaid; closing is triage's and people's. An alert that flaps gets at
    most one resolved comment and one count comment an interval
    (`TestAlertCommentsKeepTheirInterval`). Resolved for an alert never
    seen, a folded one or one whose issue is closed says nothing.

12. **The issue** (`model.AlertIssue`): `Alert: <name>`, labelled
    `kind/bug`, `source/monitor` and `status/proposed`, so triage decides
    and the Crew's approval rules hold. In the operator's words: the
    Container and source, since when, when the operator saw it, what the
    Container's `status.deploys[]` says runs where, and the link back.
    Then what the alert says (name, summary, description, labels,
    annotations) between `======== alert ========` and `======== end of
    alert ========`, lines of the shape a prompt's issue block has, which
    grow past any copy of themselves in the text. Last, the line
    `<!-- theos-alert: <source> <key> -->`. Step 3 gives the deploy
    monitor's bug `source/monitor` too: one label for what the monitor
    files.

13. **An alert's text is hostile until shown otherwise.**
    - The parsers cut every field (a name to 120 characters, a
      description to 2000, 40 labels and 40 annotations of 500) and drop
      control characters and the format characters that hide or reorder
      text, so nothing downstream sees a megabyte.
    - Inside the markers the text is a fenced code block whose fence
      grows past any run of backticks in it: the Git host renders none of
      it, so no mention notifies, no image loads, no link is dressed up.
    - The link back is shown only when it is a plain `http` or `https`
      address.
    - The marker is read only outside the block
      (`model.HasAlertMarker`): an alert whose text holds another alert's
      marker is not that alert's issue.
    - A field of the wrong JSON type is a missing field; a body of the
      wrong shape is refused whole.

14. **What we leave out.** No time zone for the day (UTC; the limit is a
    brake, not a calendar). No Sentry metric alerts and no `basic_auth`,
    until someone asks. No silencing from theos: the tools have it. The
    UI shows nothing of an AlertSource until a later issue.

## Steps

| Step | Issue | Done when |
| --- | --- | --- |
| 1. The plan and the rules (this one) | #64 | this file; `docs/design.md` and `ROADMAP.md` say what is planned; `internal/model/alert*.go` with table tests on the recorded deliveries in `internal/model/testdata/alerts/`, deduplication, the daily limit and hostile input included |
| 2. The `AlertSource` kind | #64 | `kubebuilder create api --group theos --version v1beta1 --kind AlertSource --resource --controller`; spec and status as decision 1, with the CRD's patterns and CEL (`type` immutable, a route has exactly one of `namespace` and `project`, `commentInterval` at least `1m`); a sample in `config/samples`; envtest: each rule's failing case; the reconciler sets `Ready` from the Secret (`SecretMissing`); `make validate-yaml` passes |
| 3. The receiver and the filing | #64 | `model.AlertDeliveryAuthentic` (decision 3) table-tested; `POST /alerts/{namespace}/{name}` on the Git events listener; the reconciler drains the inbox as decision 2 says and requeues for what is owed, on a clock the specs move (`Now`, as the Crew's reconciler has); the deploy monitor's bug gets `source/monitor`; the metric; envtest: a signed delivery files one issue, a second comments once the interval passed, an unsigned one is refused and writes nothing, an alert that names no known Container is counted on `status.unmatched` and files nothing, the limit folds, a lost status write files no second |
| 4. The chart and the guide | #64 | the chart's Service exposes the path and a value gives it an Ingress for senders outside the cluster (`dist/chart` is a protected path); `docs/alerts.md` shows the Alertmanager receiver's YAML, the Sentry integration's settings and a `curl` for the generic type; README, `docs/comparison.md`, `docs/design.md` ("Out of scope", the Alerts rule) and `ROADMAP.md` say it is built |

## Done when

Step 4 is merged, and the issue's own list holds: the plan and the docs;
the parsers in `internal/model` with their table tests; the four envtest
specs of step 3; the chart's path and the guide's Alertmanager YAML.
`docs/comparison.md` then has no "not yet (#64)" in theos's column.

## Where the recorded deliveries come from

`internal/model/testdata/alerts/` holds bodies written from each tool's
documentation, with our own values. None was captured from a running tool:
no agent runs one. Step 4's guide is the first place a real delivery meets
the parsers, and a field that differs is a bug with a fixture to add.

| File | Shape |
| --- | --- |
| `alertmanager-firing.json`, `alertmanager-resolved.json` | Alertmanager's webhook body, version 4: `webhook_config` in <https://prometheus.io/docs/alerting/latest/configuration/> |
| `sentry-issue-alert.json` | the issue alert webhook of Sentry's integration platform, resource `event_alert`: <https://docs.sentry.io/organization/integrations/integration-platform/webhooks/issue-alerts/> |
| `sentry-issue-resolved.json` | its issue webhook, resource `issue`, action `resolved`: <https://docs.sentry.io/organization/integrations/integration-platform/webhooks/issues/> |
| `generic-firing.json` | ours, decision 7 |
