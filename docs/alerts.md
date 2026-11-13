# Alerts from a running app

theos monitors no app itself: it scrapes nothing and holds no alert rules.
It listens to the tools that do. An alert from Alertmanager, an issue alert
from Sentry, or a small JSON body from any other tool becomes one `kind/bug`,
`source/monitor`, `status/proposed` issue in the repository of the Container
it concerns, and goes to triage like any issue. The same alert firing again
is counted on that issue, at most once a `commentInterval`; past
`dailyLimit` issues a day for a Container the rest are folded into one
"and N more" on the day's last issue; a resolved alert is said once and
closes nothing. Why each rule is as it is:
[plans/done/alerts.md](plans/done/alerts.md).

This page sets up one sender of each type. It assumes the chart is
installed as release `theos` in namespace `theos-system`
([getting-started.md](getting-started.md)), and a Project in namespace
`webshop` with Container `orders-api`.

## Where alerts are delivered

The operator takes alerts on the listener that takes the Git host's
deliveries (`gitEvents.enabled`, on by default), at

```
POST /alerts/<namespace>/<alertsource>
```

on Service `theos-git-events`, port 8082:
`http://theos-git-events.theos-system.svc:8082/alerts/webshop/alertmanager`
from inside the cluster. Without an AlertSource of that name every path
answers 404.

By default a NetworkPolicy lets only the Git host reach that port, so a
sender needs one of two chart values
([dist/chart/README.md](../dist/chart/README.md#alerts)):

- **A sender in the cluster** (Alertmanager): name its pods in
  `alerts.from`, as NetworkPolicy peers.
- **A sender outside the cluster** (Sentry's cloud): `alerts.ingress` gives
  `/alerts/` an Ingress with TLS, and no other path. Plain HTTP would show a
  bearer token to whoever can read the traffic, so the Ingress always
  serves TLS. The Ingress controller's pods go in `alerts.from` too, or the
  policy keeps them out; the chart refuses an Ingress without them.

```yaml
alerts:
  from:
    # Alertmanager, from the namespace it runs in
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: monitoring
    # the Ingress controller, for alerts.ingress
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: ingress-nginx
      podSelector:
        matchLabels:
          app.kubernetes.io/name: ingress-nginx
  ingress:
    enabled: true
    className: nginx
    host: alerts.example.com
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt
    # the certificate's Secret, made by you or by cert-manager; empty: the
    # Ingress controller's default certificate
    tlsSecretName: theos-alerts-tls
```

A NetworkPolicy fences a port, not a path, so a peer in `alerts.from` can
reach `/git-events/` too; every delivery there is signed with its
GitProvider's own Secret. The Ingress serves `/alerts/` alone.

The AlertSource's `status.url` shows the address a sender outside the
cluster delivers to when there is one, else the in-cluster one:

- With `alerts.ingress.enabled`, the Ingress's:
  `https://alerts.example.com/alerts/webshop/alertmanager`. The chart passes
  `https://<alerts.ingress.host>` to the operator as `--alerts-url`.
- With `alerts.url` set, that address instead, for a receiver behind a
  proxy of your own: `alerts.url: https://hooks.example.com` gives
  `https://hooks.example.com/alerts/webshop/alertmanager`.
- Otherwise the in-cluster address above, under `gitEvents.url`.

The in-cluster address stays valid for senders inside the cluster, such as
Alertmanager, whichever address `status.url` shows: give them
`http://theos-git-events.theos-system.svc:8082/alerts/<namespace>/<alertsource>`.

Alerts the operator took and has not filed when it is stopped are filed
for `alerts.drainTimeout` (default `5s`) before it exits; keep it under
`manager.terminationGracePeriodSeconds` (default 10). An alert taken while
the Git host is down waits in memory, and a restart loses it: Alertmanager
repeats a firing alert every `repeat_interval`, which makes that good;
Sentry and a tool that posts once do not repeat.

## The AlertSource and its Secret

An AlertSource names one sender, in the Project's namespace. Its Secret
holds what the sender holds too, and checks every delivery; no delivery is
taken unchecked, and no setting turns the check off.

| `type` | The delivery carries | Secret key |
| --- | --- | --- |
| `alertmanager` | `Authorization: Bearer <token>` | `token` |
| `sentry` | `Sentry-Hook-Signature`: the hex HMAC-SHA256 of the body, keyed with the integration's client secret | `secret` |
| `generic` | `X-Theos-Signature: sha256=<hex HMAC-SHA256 of the body>` when the Secret has `secret`; else `Authorization: Bearer <token>` | `secret`, else `token` |

Make the Secret in the cluster, so the token is in no file and no history:

```bash
kubectl -n webshop create secret generic alertmanager-webhook \
  --from-literal=token="$(head -c 24 /dev/urandom | base64)"
```

```yaml
apiVersion: theos.factory.io/v1beta1
kind: AlertSource
metadata:
  name: alertmanager
  namespace: webshop
spec:
  type: alertmanager # or sentry, or generic; it can't change later
  secretRef:
    name: alertmanager-webhook
  # The label (Alertmanager), tag (Sentry) or field (generic) that names
  # the Container. Default theos_container.
  containerLabel: theos_container
  # The label that names the namespace an app runs in; a Prometheus that
  # relabels has exported_namespace. Default namespace.
  namespaceLabel: namespace
  # Where alerts go that name no Container: by namespace or Sentry project.
  routes:
    - container: orders-api
      namespace: orders
  commentInterval: 1h # the least time between two comments on one alert
  dailyLimit: 5       # issues a day for one Container; the rest fold
```

An alert finds its Container in this order: the Container its
`containerLabel` names; the first route whose `namespace` or `project` is
the alert's; the one Container whose `spec.deploy` names an Argo CD
DeployTarget with the alert's namespace as destination; the Container named
like its Sentry project. An alert that finds none files nothing and is
counted in `status.unmatched`, with why.

`kubectl -n webshop get alertsource alertmanager -o yaml` shows how it
goes: `Ready` is `Receiving` once the Secret holds the key its type reads,
`SecretMissing` while it doesn't (an empty value included), and
`ReceiverOff` when the chart's `gitEvents.enabled` is off. `status.url` is
where to deliver from outside the cluster, or from inside it when nothing
outside reaches the receiver ([above](#where-alerts-are-delivered)), and
`deliveries`, `unmatched` and `unread` count what came. A delivery that
fails the check writes nothing to the AlertSource; the operator's metric
`theos_alert_deliveries_total` counts it, by `result`.

## Alertmanager

Mount the Secret in Alertmanager (with the Prometheus Operator: copy it to
Alertmanager's namespace and list it in the Alertmanager resource's
`spec.secrets`, which mounts it at `/etc/alertmanager/secrets/<name>/`), then
give it a receiver and a route:

```yaml
route:
  receiver: default
  routes:
    - receiver: theos
      matchers:
        - theos_container=~".+"
      continue: true
receivers:
  - name: default
  - name: theos
    webhook_configs:
      - url: http://theos-git-events.theos-system.svc:8082/alerts/webshop/alertmanager
        send_resolved: true
        http_config:
          authorization:
            type: Bearer
            credentials_file: /etc/alertmanager/secrets/alertmanager-webhook/token
```

With the Prometheus Operator's `AlertmanagerConfig`, in the namespace that
holds a copy of the Secret:

```yaml
apiVersion: monitoring.coreos.com/v1alpha1
kind: AlertmanagerConfig
metadata:
  name: theos
spec:
  route:
    receiver: theos
    matchers:
      - name: theos_container
        matchType: =~
        value: .+
  receivers:
    - name: theos
      webhookConfigs:
        - url: http://theos-git-events.theos-system.svc:8082/alerts/webshop/alertmanager
          sendResolved: true
          httpConfig:
            authorization:
              type: Bearer
              credentials:
                name: alertmanager-webhook
                key: token
```

By default the Prometheus Operator adds a `namespace=<the AlertmanagerConfig's
namespace>` matcher to every route, so this config only takes alerts labelled
with its own namespace. For alerts from other namespaces, set
`alertmanagerConfigMatcherStrategy: None` on the Alertmanager resource.

The route sends theos the alerts that name a Container. To send alerts by
the namespace their app runs in instead, match on that label (for example
`namespace="orders"`) and let a route of the AlertSource, or the Container
that deploys there, take them. An alert rule names its Container with a
label:

```yaml
- alert: OrdersAPIErrors
  expr: sum(rate(http_requests_total{job="orders-api",code=~"5.."}[5m])) > 1
  for: 10m
  labels:
    theos_container: orders-api
  annotations:
    summary: orders-api answers 5xx
```

An alert is one alert by its labels, as Alertmanager's own fingerprint is:
a repeat, a resolution and a change of annotations are the same alert,
another label value is another. A delivery is read up to its first 100
alerts; the rest, and Alertmanager's `truncatedAlerts`, are counted in
`status.unread`.

## Sentry

Sentry delivers from its cloud, so it needs `alerts.ingress`. In Sentry:

1. **Settings, Developer Settings, Custom Integrations, Create New
   Integration, Internal Integration.**
2. **Webhook URL:** `https://alerts.example.com/alerts/webshop/sentry`.
3. **Alert Rule Action:** on. It makes the integration an action of
   issue alert rules.
4. **Permissions:** Issue & Event, Read. **Webhooks:** `issue`, so that a
   Sentry issue resolved tells the alert's issue so; without it theos hears
   only firings.
5. Save, and copy the integration's **Client Secret** into a file only you
   can read, say `./sentry-client-secret` (`chmod 600`). Then:

   ```bash
   kubectl -n webshop create secret generic sentry-webhook \
     --from-file=secret=./sentry-client-secret
   rm ./sentry-client-secret
   ```

6. In the project's **Alerts**, create an issue alert whose action is
   *Send a notification via an integration*, with this integration.

```yaml
apiVersion: theos.factory.io/v1beta1
kind: AlertSource
metadata:
  name: sentry
  namespace: webshop
spec:
  type: sentry
  secretRef:
    name: sentry-webhook
```

A Sentry issue is one alert, every event of it the same. Its Container is
the tag `theos_container` (`sentry_sdk.set_tag("theos_container",
"orders-api")`, or the SDK's `initialScope` tags), else a route by the
project's slug, else the Container named like the project. Every other
webhook Sentry sends is answered 202 and does nothing, so the integration
never shows as failing.

## Any other tool: the generic type

A tool that can post JSON posts one alert a delivery:

| Field | What |
| --- | --- |
| `title` | required: the alert's name |
| `key` | what makes two deliveries the same alert; the title when absent |
| `status` | `firing` (the default) or `resolved` |
| `summary`, `description` | text for the issue |
| `container`, `namespace` | where it goes (or the labels `containerLabel` and `namespaceLabel` name) |
| `startsAt`, `endsAt` | RFC 3339 times |
| `url` | the link back, shown when it is `http` or `https` |
| `labels`, `annotations` | objects of text |

Signed with an HMAC (the Secret's key `secret`):

```bash
kubectl -n webshop create secret generic probe-webhook \
  --from-literal=secret="$(head -c 24 /dev/urandom | base64)"
kubectl -n webshop apply -f - <<'EOF'
apiVersion: theos.factory.io/v1beta1
kind: AlertSource
metadata:
  name: probe
spec:
  type: generic
  secretRef:
    name: probe-webhook
EOF

key=$(kubectl -n webshop get secret probe-webhook -o jsonpath='{.data.secret}' | base64 -d)
body='{"title":"Checkout probe fails","key":"checkout-probe","container":"orders-api","summary":"3 failures in a row"}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$key" -r | cut -d' ' -f1)
curl -sS -i https://alerts.example.com/alerts/webshop/probe \
  -H 'Content-Type: application/json' \
  -H "X-Theos-Signature: sha256=$sig" \
  --data-binary "$body"
```

Sign the exact bytes you send. With a `token` in the Secret instead of a
`secret`, send `-H "Authorization: Bearer $token"` and no signature. From a
machine with `kubectl` and no Ingress,
`kubectl -n theos-system port-forward svc/theos-git-events 8082` and
`http://localhost:8082/alerts/webshop/probe` reach the same receiver.

## What the receiver answers

| Code | When |
| --- | --- |
| 202 | taken; the operator files it soon after |
| 400 | the body is not the type's shape |
| 401 | the check failed, or the Secret is missing |
| 404 | no AlertSource of that name |
| 413 | a body over 1 MiB |
| 503 | the inbox is full, the operator is stopping, or reading the AlertSource or its Secret failed: send again |

The answer comes before anything is filed, since Sentry gives a webhook one
second. A 202 for an alert that files no issue means it was counted:
`status.unmatched` says why one found no Container, and the alert's issue
says how often it fired.

## The issue

`Alert: <name>`, labelled `kind/bug`, `source/monitor` and
`status/proposed`, so triage decides and the Crew's approval rules hold.
The operator's own words come first (the Container, the source, since when,
what is deployed where, the link back), then what the alert says, between
`======== alert ========` and `======== end of alert ========`, in a code
block. That text comes from the tool, and through it from whatever the app
logged, so agents read it as data. Closing the issue silences nothing: an
alert that still fires on a later day files again. Silence it where it
comes from.
