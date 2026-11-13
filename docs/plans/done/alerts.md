# An alert from a running app files a bug

GitHub issue #64, a step of [open-source-launch.md](../open-source-launch.md)
(#46). Written 2026-11-13 in a session the owner ran, and changed the same
day after step 1's two reviews: where a decision below says "the review" or
"the second review", that is what changed and why.

## Goal

The loop's last stage listens to the tools that watch a running app. An
alert from Alertmanager, an issue alert from Sentry, or a small JSON body
from any other tool becomes one `kind/bug`, `source/monitor` issue in the
repository of the Container it concerns, and goes to triage like any issue.
The same alert firing again is counted on that issue, a storm becomes a
handful of issues and one "and 40 more", and nothing is fixed outside the
normal loop.

theos still monitors nothing itself ([design.md](../../design.md), "Out of
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
  ([done/git-events.md](git-events.md)). A delivery there is a hint:
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
   | `containerLabel` | the label, tag or field of an alert that names its Container; 1 to 100 characters, none of them white space or a control character | `theos_container` |
   | `namespaceLabel` | the label of an Alertmanager alert, or of a generic body that has no `namespace` field, that names the namespace its app runs in; 1 to 100 characters, as `containerLabel` | `namespace` |
   | `routes[]` | at most 32 of `container` and exactly one of `namespace` or `project`: where alerts go that name no Container (decision 4) | none |
   | `commentInterval` | the least time between two comments about one alert, and between two "and N more"; a duration of at least `1m` | `1h` |
   | `dailyLimit` | issues the source files for one Container in one UTC day, 1 to 100 | `5` |

   `namespaceLabel` is a setting because the name is one the user holds in
   their Prometheus: `namespace` is what its Kubernetes metrics carry, and a
   Prometheus that relabels has `exported_namespace` or
   `kubernetes_namespace` (the review; AGENTS.md, "What can be configured is
   configured"). The parsers take both names as `model.AlertLabels`.

   Both labels have a minimum length of 1 and the pattern
   `^[^\p{Z}\p{C}]+$` in the CRD: no space or other separator, no control
   or format character. A field left out gets its default from the CRD; an
   empty one is refused, and so is `" "`, which the parsers would read as
   the label named " " and find on no alert (the second review).

   `status`, written by the AlertSource's reconciler alone:

   | Field | Type | What |
   | --- | --- | --- |
   | `conditions` | | `Ready`: `Receiving`, or false with `SecretMissing` (no Secret, none of the keys its type reads, or an empty value under the key: decision 3) or `ReceiverOff` (the operator's listener is off) |
   | `url` | string | where the sender delivers: the path, after the operator's `--git-events-url` when that is set |
   | `deliveries`, `lastDeliveryTime` | int64, time | deliveries taken, and the last |
   | `unmatched` | `count` int64, `lastTime`, `message` | alerts that named no known Container, and why the last one didn't (`model.AlertContainer`'s second result) |
   | `unread` | `count` int64, `lastTime` | alerts that deliveries held past the first 100, or that their sender said it left out (Alertmanager's `truncatedAlerts`): `model.ParseAlerts`' `more`, at most a million a delivery |
   | `alerts[]` | at most 100 (`model.MaxAlertRecords`), keyed by `key` | `container`, `name` (the alert's, at most 120 characters, for people), `issue` (int32, its number), `folded`, `resolved`, `filedAt`, `lastSeen`, `count` and `reported` (int64), `reportedAt`, `saidResolved`: `model.AlertRecord` field for field |
   | `containers[]` | keyed by `name` | `date`, `filed` (int32), `lastIssue` (int32), `folded`, `unrecorded` and `foldReported` (int64), `foldReportedAt`: `model.AlertDay` field for field |

   An issue's number is `int32`, as every kind holds one (`Task.spec.issue`),
   and so is `filed`, which `dailyLimit` bounds. A count that only grows is
   `int64`. Status holds an issue's number and not its URL: the model
   decides on numbers alone, the URL is the Container's repository and the
   number, and a second copy would be one more thing to keep right when a
   repository moves (the review found `alerts[]` promising a `url` that
   `model.AlertRecord` has not; the record gained `Name` and the table lost
   the URL).

   Step 2 writes `conditions` alone: `SecretMissing`, else `ReceiverOff`,
   since that build has no receiver. Step 3 writes `Receiving`, `url` and
   every count, and drops the `alerts[]` and `containers[]` entries of a
   Container that is gone, and a `containers[]` entry of an earlier day once
   nothing is owed on it (`model.AlertDayOwed`).

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
   from the inbox. A failed call leaves it there for the next pass.

   What this loses, said plainly (the review: the first text understated
   it). An alert that was answered 202 and is still in the inbox is gone
   when the operator's process ends, and the receiver runs on the leader,
   so a leader change empties the inbox too. The window is not a second: an
   alert waits in the inbox for as long as the Git host is down. A sender
   that repeats makes the loss good, which Alertmanager does every
   `repeat_interval` (four hours by default) while the alert fires. These
   do not come again:
   - a Sentry issue alert whose rule fires once for an issue ("a new issue
     is created"): one delivery;
   - Alertmanager's resolved notice, sent once: the issue is never told;
   - an alert that resolves before its `repeat_interval`: the firing is
     lost, and the resolved that follows is then for an alert never seen;
   - a generic post from a tool that posts once.

   We keep the memory inbox all the same: a Sentry webhook has one second,
   and an inbox on disk or in the API server is a queue to build and to
   bound, for deliveries that monitoring tools already treat as best
   effort. Step 3 narrows the loss instead:
   - on SIGTERM the receiver first stops taking deliveries (503, so a
     sender that retries does), then drains the inbox until a deadline.
     The deadline is the operator's flag `--alert-drain-timeout`, by
     default `5s`. It has to fit the pod's grace period, which is 10
     seconds in `config/manager` and in the chart's
     `manager.terminationGracePeriodSeconds`, a value the user sets: so
     the deadline is a setting too, and the chart's `manager.args` takes
     the flag. A deadline longer than the grace period is cut by the
     kill, and what the inbox held then is lost unsaid; the flag's help
     says so. (The second review: the first text said 20 seconds, twice
     the grace period.) A longer default would mean a longer grace
     period, which is `config/manager` and `dist/chart`, protected paths:
     this plan changes neither;
   - on leadership lost nothing is drained. controller-runtime ends the
     manager at once then (it sets its graceful shutdown to zero when the
     lock is lost without intending to), and another operator may already
     lead: a drain would file and write status beside it. What the inbox
     holds counts as dropped. The receiver tells the two apart by the
     signal handler's context, which only a signal ends: when its own
     context ends and that one has not, it drains nothing;
   - what is dropped, at the deadline or with the lock, is counted in
     `theos_alert_deliveries_total` with `result="dropped"` and logged by
     AlertSource and count, with no alert's text, as far as the process
     gets before it ends;
   - one envtest spec holds it, with a short deadline: an alert taken
     while the Git host fails is filed when the host answers, one still
     in the inbox at the deadline is counted as dropped, and when the
     context ends without a signal nothing more is filed.

3. **Every delivery is authenticated, by what its sender can do.** The
   Secret is the user's: it holds what the sender holds too.

   | Type | The delivery carries | Secret key |
   | --- | --- | --- |
   | `alertmanager` | `Authorization: Bearer <token>` (Alertmanager's `http_config.authorization`; it cannot sign) | `token` |
   | `sentry` | `Sentry-Hook-Signature`, the hex HMAC-SHA256 of the body with the integration's client secret | `secret` |
   | `generic` | `X-Theos-Signature: sha256=<hex HMAC-SHA256 of the body>` when the Secret has `secret`; else `Authorization: Bearer <token>` | `secret`, else `token` |

   No type takes an unauthenticated delivery and no setting turns the check
   off. `model.AlertDeliveryAuthentic` (step 3) decides, and:
   - an empty value under `token` or `secret` is no credential. The
     AlertSource is `SecretMissing` and authenticates nothing: an empty
     bearer token would equal an empty `token`, and an HMAC with an empty
     key is one anyone can compute. A delivery to a source whose Secret is
     missing is answered 401, as one that fails the check is;
   - comparisons are constant-time, and a bearer token is compared by its
     SHA-256 digest, so the comparison doesn't tell the token's length.

   No timestamp is checked: Sentry signs the body alone, and Alertmanager
   signs nothing. So a captured delivery can be replayed, and a replay is
   one more delivery of the same alert. Within a UTC day it is counted and
   files nothing. On a later day, once the alert's issue was closed on a
   day before, a replayed firing delivery files one issue, at most one a
   day and within `dailyLimit` (decision 8). The first text said "never a
   second issue"; the review showed that holds only within the day.

   The listener is plain HTTP inside the cluster, as for the Git host. For
   an HMAC that exposes deliveries; for a bearer token it exposes the
   credential itself, to whoever can read the cluster's traffic. A sender
   outside the cluster (Sentry's cloud) comes through an Ingress with TLS,
   which step 4 gives a chart value. Steps 3 and 4 read the `secrets` skill
   first.

4. **An alert finds its Container in this order** (`model.AlertContainer`):
   1. the Container it names itself, by `containerLabel`: an Alertmanager
      label, a Sentry tag, a generic body's `container` field or label. A
      name that is no Container of the namespace is unmatched, never
      second-guessed;
   2. the first of `routes` whose `namespace` or `project` is the alert's;
   3. the alert's namespace (the label `namespaceLabel` names, by default
      `namespace` as Prometheus's Kubernetes metrics carry it; a Sentry
      alert names none): the one Container whose `spec.deploy`
      names an `argoCD` DeployTarget with that destination namespace. Two
      Containers there is unmatched, and the message says to name one;
   4. the alert's project (Sentry's slug): the Container of that name.

   Unmatched alerts are counted in `status.unmatched` and file nothing. A
   delivery whose key has a record takes the record's Container: a
   resolution needs no label.

5. **Alertmanager** (`model.ParseAlertmanager`): one alert for each entry
   of `alerts`, the first 100 of a delivery; the rest, and
   `truncatedAlerts`, are counted in `status.unread` (a `truncatedAlerts`
   below zero counts as none, and the sum stops at a million: it is the
   sender's word). Alertmanager sends a group's alerts in the same order
   each time, so the ones past the first 100 are not read on a repeat
   either, their resolutions included: a group that large is a storm, and
   `unread` says so. The key is a digest of the alert's
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
   (`model.AlertStep`): it is new, its issue was filed and closed on
   earlier days, or it was folded on an earlier day. With an open issue it
   is counted. With an issue filed or closed today (UTC) it is counted
   too. Filed today: an alert files at most once a day, so an alert triage
   closes as not worth fixing costs one triage a day, not one a delivery.
   Closed today: an issue is mostly closed by the merge of its fix, and
   the alert goes on firing until the release is out, so the fix gets the
   day to reach the app; if it didn't hold, the alert files a new bug the
   next day. The rule needs when the issue was closed, which both hosts
   give: `model.AlertFacts.ClosedAt`. (The review: the first rule looked
   at the filing day alone, so a bug filed Monday whose fix merged Tuesday
   morning was filed again at the next repeat.) A host that gives no
   closing time is taken as "not closed today". Closing an issue doesn't
   silence an alert; that is done where the alert comes from, and the
   issue says so. Filing first looks for the alert's marker among the
   repository's open `source/monitor` issues (decision 12), so a status
   write lost after filing doesn't file a second, and neither does an
   alert whose record made room while its issue stayed open (decision
   10: the second review showed `model.AlertStep` alone says `File`
   there).

   An issue found that way is taken as the one filed: the decision's
   `Filed` gets its number. So it counts in the day's `filed`, a slot of
   the limit for an issue nobody filed today, and it is the day's
   `lastIssue`, the issue an "and N more" of today is then said on. The
   record starts anew at `count` 1 and `reported` 1, and finding the
   issue says nothing on it: its next word is a count, an interval
   later, of the firings since it was found. Step 3 keeps no second
   kind of filing for it: the limit errs on the quiet side, and the case
   takes a day of a hundred new alerts.

   The day is the UTC day of the operator's clock, and it doesn't start
   twice: when status holds the day after the clock's (a new leader on a
   node whose clock is behind, just after midnight), that day and its
   limit go on. A day further ahead than one is no day a clock gets back
   from, and the count starts afresh.

9. **Repeats are counted on delivery and said by a rule of their own**
   (`model.AlertRecordOwed`): an open issue is owed a comment when its
   alert fired since the issue last said so and `commentInterval` has
   passed since then. Filing is the issue's first word, so the first count
   comes an interval after it. The reconciler asks after each delivery and
   when the wait it was told has passed, so "at most once an hour" also
   means "no later than an hour": a count doesn't wait for the next
   delivery.

   This reads the issue's "a second comments" differently than its words:
   the second delivery of an alert comments once an interval has passed
   since the issue was filed, not when it arrives. Otherwise every alert
   would comment within minutes of filing, at Alertmanager's
   `group_interval` repeat or Sentry's second event, and say what the
   issue just said. Step 3's spec is written that way: "a second comments
   once the interval passed".

10. **The daily limit folds the rest** (`model.AlertStep`,
    `model.AlertDayOwed`): past `dailyLimit` issues for a Container in a
    UTC day, a new alert files nothing and is counted on the day. The last
    issue filed that day gets "And N more alerts fired for this Container
    ...": at once for the first, then at most once a `commentInterval`
    with the day's count, and once more when the day is over. A storm of
    45 is five issues, of which the fifth says "and 40 more"
    (`TestAlertStorm`). A folded alert that still fires the next day gets
    its issue then.

    The default stays 5, where the issue's "a storm becomes one issue"
    would be 1 (the review asked for the reason). At 1, the second
    unrelated alert of a day gets no issue until tomorrow: two alerts are
    not a storm, and the one folded is as likely the one that matters. At
    5 a real storm still costs triage five issues, mostly of one cause,
    and `dailyLimit: 1` is there for who wants one.

    **Past what status remembers the day counts deliveries.** Status
    holds 100 alerts, and a storm can have more. An alert past the limit
    that has no record, and that status has no room to remember
    (`model.AlertRecordsFull`: 100 records, each seen today), is not
    folded but counted in the day's `unrecorded` (`AlertOverflow`): its
    next delivery can't be told from another alert's, so what is counted
    is deliveries. An alert that has a record needs no room and is folded
    (the second review: it was counted unrecorded). The comment then
    says both, as what each is: "And 95 more alerts fired ... of those it
    counts the deliveries, 150, and cannot say how many alerts they are."
    When a record has to make room (`model.PutAlertRecord`), one not seen
    today goes first, then a folded one before a filed one, a resolved one
    before a firing one, the one seen longest ago. (The review: the first
    rule went by last seen alone, so in a storm of 150 the five filed
    alerts lost their records first and their issues their counts, and
    each forgotten alert was folded again at every delivery: "445 more"
    where 145 were folded. `TestAlertStormPastTheRecords`.) A record of
    an earlier day goes before a folded one of today, filed or not,
    because that is what keeps a count from doubling: its alert, if it
    comes back, files or finds its open issue by the marker.

    That costs an open issue its alert for a while (the second review).
    After 96 to 100 new alerts in a day, the record of an alert filed on
    an earlier day, its issue still open, has made room. Under the limit
    its next delivery finds the issue again (decision 8). Past the limit
    nothing is looked up: the alert is folded as any alert without a
    record, or counted unrecorded, so the day's "and N more ... filed no
    issue" counts one that has an issue, and that issue gets no count and
    no word that the alert resolved until the alert files again on a
    later day and finds it. A lookup for every folded alert would be a
    call to the Git host for each alert of a storm, which is what the
    limit is there to spare.

    **The old day is settled before the new one starts.** `containers[]`
    holds one day a Container, and `model.AlertStep` on a new day puts
    the new day in its place. So before the first step of a Container's
    new day, a midnight in the middle of a batch included, step 3 asks
    `model.AlertDayOwed` about the day status holds and makes the comment
    it is owed; only then does it step. A day is over for both rules at
    once: `AlertDayOwed` settles at once every day that `AlertStep` would
    start afresh, one far ahead of the clock included.

    **An alert is folded once a day while status stays full.** A record
    folded today makes room for a filed alert alone, and status is then
    full, so its alert's next delivery is counted unrecorded. Step 3
    drops the records of a Container that is gone (decision 1), which
    makes room in the middle of a day: an alert whose folded record had
    made room is then folded once more, and the day's count is one too
    high for it. That takes a storm past 100 records and a Container
    deleted on the same day; the comment says "more", and we leave it.

11. **Resolved is said once and closes nothing.** The alert's open issue
    gets a comment that its source reports it resolved, with what fired
    unsaid; closing is triage's and people's. An alert that flaps gets at
    most one resolved comment and one count comment an interval
    (`TestAlertCommentsKeepTheirInterval`). Resolved for an alert never
    seen, a folded one or one whose issue is closed says nothing.

12. **The issue** (`model.AlertIssue`): `Alert: <name>`, labelled
    `kind/bug`, `source/monitor` and `status/proposed`, so triage decides
    and the Crew's approval rules hold. A Git host reads a title as text,
    where `#12`, `!12`, `owner/repo#3` and `@name` are references and
    mentions, and an alert's name is an app's error message: the title
    puts a space after the sign (`# 12`, `@ name`), so an alert leaves no
    "mentioned" note on another issue and notifies nobody. In the
    operator's words: the Container and source, since when, when the
    operator saw it, what the Container's `status.deploys[]` says runs
    where, and the link back.
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
      Only the block's own headings (`Name:`, `Description:`, `Labels:`)
      start a line: a description's lines are indented as labels are, so
      none of them reads as a second `Labels:` section or as a marker.
    - The link back is shown only when it is a plain `http` or `https`
      address, and between angle brackets (`<https://...>`), where the
      address is the link and its own text. Written bare, Markdown in an
      address (`https://x/[a](https://evil)`) dressed one link as another
      (the review).
    - The address of an issue on the Git host itself is accepted, in the
      link back and in a title, and so is `GH-12` in a title: the host
      notes on that issue that it was mentioned (for `GH-12`, GitHub on
      issue 12 of the Container's own repository). Step 3 drops no link
      back for its host and the title is not cut further (the second
      review asked which). Every delivery is authenticated; the note
      notifies nobody and changes nothing; it lands only where the
      operator's account can read; and the limit bounds it to
      `dailyLimit` issues a Container a day. Dropping the link back alone
      would leave the same address in the title, and telling an address
      of the Git host from other text there needs the host's address in
      `internal/model`, which knows no host: a door half closed would
      read as closed.
    - The marker is read only outside the block
      (`model.HasAlertMarker`): an alert whose text holds another alert's
      marker is not that alert's issue.
    - A body of the wrong shape is refused whole (400). What a wrong
      type in one field does depends on whose shape it is. Sentry's and
      the generic body are read loosely, since Sentry's fields vary by
      SDK and the generic body is hand-written: a text field that holds a
      number or a boolean is read as that text, and one that holds an
      object, a list or null is a missing field. Alertmanager's body has
      one published shape, and a field of another type than it gives (a
      label that is no string, `truncatedAlerts` that is no integer)
      makes it no Alertmanager body: refused. In every type, `labels` or
      `annotations` that are no object refuse the body.

14. **What we leave out.** No time zone for the day (UTC; the limit is a
    brake, not a calendar). No Sentry metric alerts and no `basic_auth`,
    until someone asks. No silencing from theos: the tools have it. The
    UI shows nothing of an AlertSource until a later issue.

## Steps

| Step | Issue | Done when |
| --- | --- | --- |
| 1. The plan and the rules (this one) | #64 | this file; `docs/design.md` and `ROADMAP.md` say what is planned; `internal/model/alert*.go` with table tests on the recorded deliveries in `internal/model/testdata/alerts/`, deduplication, the daily limit and hostile input included |
| 2. The `AlertSource` kind (done) | #64, #187 | `kubebuilder create api --group theos --version v1beta1 --kind AlertSource --resource --controller`; spec and status as decision 1, field names and integer widths included, with the CRD's patterns and CEL (`type` immutable, a route has exactly one of `namespace` and `project`, `commentInterval` at least `1m`, the two labels' minimum length and pattern, and the two labels differ with their defaults applied, `theos_container` and `namespace` for one left out, at create and at update: #187); a sample in `config/samples`; envtest: each rule's failing case, and for #187 one spec per case (both set and equal; one set to the other's default) that fails without the rule; the reconciler sets `Ready` from the Secret (`SecretMissing`, an empty value included, else `ReceiverOff`); `make validate-yaml` passes. `make manifests` writes the CRD and the role into `dist/chart/` too, a protected path: the owner's session may, and the commit says so |
| 3. The receiver and the filing (done) | #64 | `model.AlertDeliveryAuthentic` (decision 3) table-tested, an empty `token` and an empty `secret` among its cases; `POST /alerts/{namespace}/{name}` on the Git events listener; the reconciler drains the inbox as decision 2 says, on SIGTERM too, until `--alert-drain-timeout` (default `5s`) and never on leadership lost, and requeues for what is owed, on a clock the specs move (`Now`, as the Crew's reconciler has); it settles a Container's old day (`model.AlertDayOwed`) before the first `model.AlertStep` of a new one, passes `ClosedAt` and `Full` to `model.AlertStep`, gives `Filed` the number of an open issue the marker finds (decision 8) and keeps no record of an `AlertOverflow`; the deploy monitor's bug gets `source/monitor`; the metric, with `result="dropped"`; envtest: a signed delivery files one issue, a second comments once the interval passed, an unsigned one is refused and writes nothing, an alert that names no known Container is counted on `status.unmatched` and files nothing, the limit folds, a lost status write files no second, an alert whose record made room and whose issue is open files no second, a fold of yesterday is said before today's first alert steps, an alert taken while the Git host fails is filed when it answers, one still in the inbox at the drain's deadline is counted as dropped. `make manifests` regenerates the role in `dist/chart/` (protected path, as step 2) |
| 4. The chart and the guide (done) | #64, #236 | the chart's Service exposes the path and a value gives it an Ingress for senders outside the cluster (`dist/chart` is a protected path); `docs/alerts.md` shows the Alertmanager receiver's YAML, the Sentry integration's settings and a `curl` for the generic type; README, `docs/comparison.md`, `docs/design.md` ("Out of scope", the Alerts rule) and `ROADMAP.md` say it is built |

## What step 2 found

- **The Secret's key is a rule of `internal/model`:** `model.AlertSecretKey`
  says which key a type checks deliveries with, and
  `model.AlertSecretMissing` why a Secret can't (table-tested in
  `TestAlertSecretMissing`). For `generic`, `secret` is the key whenever the
  Secret has it, an empty one included: an empty `secret` beside a valid
  `token` is `SecretMissing`, not a fall back to the token, so the Secret
  never means an HMAC anyone can compute. Step 3's
  `model.AlertDeliveryAuthentic` reads the key through `AlertSecretKey`, so
  the condition and the check can't disagree.
- **The Secret is polled, not watched:** the operator caches no Secrets, so
  the reconciler reads it every minute, as the DeployTarget's does; a Secret
  made or fixed after the AlertSource turns it from `SecretMissing` within a
  minute.
- **Status times are pointers** (`*metav1.Time`, left out when unset):
  `model.AlertRecord`'s and `model.AlertDay`'s zero times are `nil` in
  status, which step 3's conversion keeps.
- **#187 is the CRD's:** the spec's CEL rule compares the two labels with
  their defaults applied. The API server applies the defaults before it
  validates, so a label left out is compared as its default on update too.
  `docs/screenshots/187-labels-differ-without-the-rule.txt` shows the six
  specs that name #187 failing with the rule dropped.
- **The chart's helper roles** (`rbac.helpers.enabled`) for the kind were
  written as the TestEnvironment's are, not by `make chart`: the CRD and the
  manager role come from `make manifests`.

## Step 3 as built (#64)

Where the decisions above said nothing, these are the choices.

- **The receiver is on with the listener.** `AlertReceiver`
  (`internal/controller/alert_receiver.go`) is mounted on the Git events
  listener (`GitEventsReceiver.Alerts`), so `--git-events-bind-address` turns
  both on, and a build without the listener makes every source
  `ReceiverOff`. With it a source whose Secret checks deliveries is
  `Receiving`, and `status.url` is `model.AlertsURL(--git-events-url, ...)`.
- **The check** is `model.AlertDeliveryAuthentic`, which reads the key
  through `model.AlertSecretKey`, the one `Ready` checks. A missing Secret
  is no data, and is answered 401 as a wrong credential is.
- **The inbox** holds at most 1000 alerts a source; a delivery whose alerts
  don't all fit is refused whole (503), so a retry repeats nothing half
  taken. The inbox also counts the deliveries and `unread`; those counts
  ride on the next status write and leave the inbox once it succeeded.
- **One status write an alert,** under `client.MergeFromWithOptimisticLock`;
  a conflict requeues at once. The source is read live (`APIReader`) at the
  start of a drain, so the write starts from its latest. The reconcile and
  the drain on SIGTERM take a lock per source, so they never file one alert
  side by side. The clock is read for each alert, so one taken after
  midnight counts on its own day however long the drain ran
  (`TestAlertDrainCountsAnAlertTakenAfterMidnightOnItsDay`).
- **A comment is said once, but for a lost status write.** A step that
  fails after saying something (yesterday's fold, settled before the step)
  still writes what it said, and the alert stays in the inbox, so the next
  pass doesn't say it again
  (`TestAlertDrainSaysYesterdaysFoldOnceAcrossAFailedStep`). A comment is
  made before the status write that records it, so when that write itself
  is lost or conflicts, the next pass says it again: comments are
  at-least-once across a lost status write, as filing is not (the marker
  finds the issue).
- **The drain on SIGTERM** (`AlertReceiver.Start`) tries the inboxes again
  every 100 ms while the Git host fails, until `--alert-drain-timeout`; a
  context that ends while the signal handler's hasn't is leadership lost,
  and nothing is drained. `theos_alert_deliveries_total` counts each
  delivery by `result` (`accepted`, `unauthorized`, `bad_request`,
  `not_found`, `too_large`, `unavailable`) and, as `dropped`, the alerts an
  inbox held when the operator stopped. The chart's `manager.args` taking
  the flag is step 4's, with the rest of `dist/chart`.
- **The old day is settled before every step,** not only the first of a new
  day: a step asks `model.AlertDayOwed` first, and a fold owed by the
  interval is said a little sooner than the poll would. The envtest spec
  sees the outcome (yesterday's count on yesterday's last issue, today's
  first alert filed with today's count) whether the delivery or the poll
  settles it first; the unit test of the drain, with no poll beside it,
  sees the step settle it, and fails when the step doesn't.
- **`ClosedAt` is the host's `closed_at`:** `ports.Issue.ClosedAt`, read by
  the GitHub and Gitea adapters, and set by the fake when it closes an
  issue, on the clock a test gives it (`SetClock`; the suite gives it the
  alerts' clock its specs move). An issue the host no longer has is taken
  as closed with no time.
- **A body that isn't the type's shape** is answered 400 with a fixed
  message, not the parser's words, which would echo what the body held.
- **A closed issue is owed nothing.** When a record's count is due and its
  issue is closed, the record's `reported` takes its `count`: the repeats
  stay unsaid, and the host is not asked again at every pass.
- **A record of a Container that is gone** leads nowhere: its alert finds a
  Container anew (decision 4), and the record is dropped at the end of the
  pass with the Container's day.
- **Status times are seconds,** as `metav1.Time` keeps them.
- **The deploy monitor's bug** is labelled `kind/bug`, `source/monitor` and
  `status/proposed`; it still looks its marker up among `kind/bug` issues,
  so one filed before this step is found.

## Step 4 as built (#64)

- **The path is the Git events Service's,** port `gitEvents.port`: no
  Service or port of its own, since it is one listener. `values.yaml`,
  the Service and the README say so.
- **`alerts.drainTimeout`** (default `5s`) is the manager's
  `--alert-drain-timeout`, rendered with the listener's flags. The chart
  refuses a value that is no Go duration, so the manager doesn't fail to
  start on one; it does not compare it with
  `manager.terminationGracePeriodSeconds` (Helm parses no duration), and
  the values comment says to keep it under.
- **Who reaches the port is `alerts.from`, empty by default.** The
  NetworkPolicy on `gitEvents.port` gets a second rule with those peers,
  rendered only when the list names one: an empty `from` in a rule admits
  everyone, which is what the default must not do. One list serves
  in-cluster senders (Alertmanager's namespace) and the Ingress
  controller's pods, since both are peers of the same port. A
  NetworkPolicy fences a port, not a path, so a peer there reaches
  `/git-events/` too; every delivery there is signed per GitProvider, and
  the guide says so.
- **`alerts.ingress`** (off) renders Ingress `<release>-alerts`: the path
  `/alerts/` alone (`Prefix`), so `/git-events/` stays inside the cluster,
  and always a `tls` section for `host` (decision 3: a bearer token over
  plain HTTP is the credential in the clear). `className`, `host`,
  `annotations` and `tlsSecretName` are settings; an empty
  `tlsSecretName` leaves the certificate to the controller's default or
  to cert-manager's annotation. The chart refuses the Ingress without
  `gitEvents.enabled`, without a host, and without `alerts.from`: an
  Ingress the policy keeps its controller out of would fail quietly.
- **No credential in the chart:** each AlertSource's Secret is the
  user's; the guide makes them with `kubectl create secret` from
  `/dev/urandom` or from a file of Sentry's client secret.
- **Tests:** `hack/chart-alerts_test.sh` renders the chart with the values
  off and on and checks the policy's rules, the Ingress and each refusal;
  `make helm-lint` renders the Ingress on as `chart-alerts.yaml`, which
  `make validate-yaml` validates, and `HELM_ALL_ON` turns it on for the
  no-Secret check; `hack/chart-manager.sh` keeps the drain flag.
- **`status.url` is the in-cluster address** even with the Ingress on: it
  is `--git-events-url`'s, where the Git host delivers. The guide gives
  the Ingress's address for Sentry.
- **The guide** is [../../alerts.md](../../alerts.md). `docs/architecture.md`
  names the route and `controller.AlertReceiver` under the Git events
  listener's flow (#236).

## Done when

Step 4 is merged, and the issue's own list holds: the plan and the docs;
the parsers in `internal/model` with their table tests; the envtest
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
