{{/*
The receiver of the Git host's deliveries (docs/plans/done/git-events.md, decision 10).
*/}}

{{/* The receiver's Service. */}}
{{- define "theos.gitEventsName" -}}
{{- include "theos.resourceName" (dict "suffix" "git-events" "context" .) }}
{{- end }}

{{/* Where the Git host delivers: gitEvents.url, else the Service's cluster name. */}}
{{- define "theos.gitEventsURL" -}}
{{- if .Values.gitEvents.url }}
{{- .Values.gitEvents.url | trimSuffix "/" }}
{{- else }}
{{- printf "http://%s.%s.svc:%v" (include "theos.gitEventsName" .) .Release.Namespace .Values.gitEvents.port }}
{{- end }}
{{- end }}

{{/* The URL's host without its port: the one host the chart's Gitea delivers webhooks to. */}}
{{- define "theos.gitEventsHost" -}}
{{- $host := (urlParse (include "theos.gitEventsURL" .)).host }}
{{- if not $host }}
{{- fail (printf "gitEvents.url %q has no host; give an absolute http(s) URL" .Values.gitEvents.url) }}
{{- end }}
{{- regexReplaceAll ":[0-9]+$" $host "" }}
{{- end }}

{{/*
Where senders outside the cluster deliver alerts, --alerts-url (#240):
alerts.url, else https://<alerts.ingress.host> with the alert Ingress on,
else empty, and AlertSource status.url shows the in-cluster address.
*/}}
{{- define "theos.alertsURL" -}}
{{- if .Values.alerts.url }}
{{- .Values.alerts.url | trimSuffix "/" }}
{{- else if and .Values.alerts.ingress.enabled .Values.alerts.ingress.host }}
{{- printf "https://%s" .Values.alerts.ingress.host }}
{{- end }}
{{- end }}

{{/*
The alert receiver's drain deadline, --alert-drain-timeout: alerts.drainTimeout,
refused here unless it is a Go duration, so the manager doesn't fail to start
on it, or unless it is under manager.terminationGracePeriodSeconds (30 when
null; a drain timeout of 0 counts as the receiver's 5s default): the kill at
the end of the grace period would cut the drain.
*/}}
{{- define "theos.alertDrainTimeout" -}}
{{- $d := toString .Values.alerts.drainTimeout }}
{{- if not (regexMatch "^(0|([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$" $d) }}
{{- fail (printf "alerts.drainTimeout %q is no Go duration; give one such as 5s" $d) }}
{{- end }}
{{- $secs := 0.0 }}
{{- $nums := regexFindAll "[0-9]+(\\.[0-9]+)?" $d -1 }}
{{- $units := regexFindAll "ns|us|µs|ms|s|m|h" (regexReplaceAll "[0-9.]+" $d " ") -1 }}
{{- range $i, $n := $nums }}
{{- $u := "s" }}
{{- if lt $i (len $units) }}{{- $u = index $units $i }}{{- end }}
{{- $per := dict "h" 3600.0 "m" 60.0 "s" 1.0 "ms" 0.001 "us" 0.000001 "µs" 0.000001 "ns" 0.000000001 }}
{{- $secs = addf $secs (mulf (float64 $n) (index $per $u)) }}
{{- end }}
{{- /* A sum of 0 is the receiver's 5s default. */}}
{{- if le $secs 0.0 }}{{- $secs = 5.0 }}{{- end }}
{{- $grace := 30.0 }}
{{- if and (hasKey .Values.manager "terminationGracePeriodSeconds") (ne .Values.manager.terminationGracePeriodSeconds nil) }}
{{- $grace = float64 .Values.manager.terminationGracePeriodSeconds }}
{{- end }}
{{- if ge $secs $grace }}
{{- fail (printf "alerts.drainTimeout %s is not under manager.terminationGracePeriodSeconds (%v s): the kill would cut the drain; lower it or raise the grace period" $d $grace) }}
{{- end }}
{{- $d }}
{{- end }}

{{/*
The alert Ingress's checks: it routes to the git-events Service, so it needs
gitEvents.enabled; it needs a host to serve TLS for; and the NetworkPolicy on
the port admits the Ingress controller only from alerts.from.
*/}}
{{- define "theos.alertsIngressChecks" -}}
{{- if not .Values.gitEvents.enabled }}
{{- fail "alerts.ingress.enabled needs gitEvents.enabled: the alert receiver is the Git events listener" }}
{{- end }}
{{- if not .Values.alerts.ingress.host }}
{{- fail "alerts.ingress.enabled needs alerts.ingress.host, the host senders deliver to over TLS" }}
{{- end }}
{{- if not .Values.alerts.from }}
{{- fail "alerts.ingress.enabled needs alerts.from: the Ingress controller's pods as NetworkPolicy peers, or the policy on gitEvents.port keeps them out" }}
{{- end }}
{{- end }}
