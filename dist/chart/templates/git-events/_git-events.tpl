{{/*
The receiver of the Git host's deliveries (docs/plans/git-events.md, decision 10).
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
