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
The alert receiver's drain deadline, --alert-drain-timeout: alerts.drainTimeout,
refused here unless it is a Go duration, so the manager doesn't fail to start
on it.
*/}}
{{- define "theos.alertDrainTimeout" -}}
{{- $d := toString .Values.alerts.drainTimeout }}
{{- if not (regexMatch "^(0|([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$" $d) }}
{{- fail (printf "alerts.drainTimeout %q is no Go duration; give one such as 5s" $d) }}
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
