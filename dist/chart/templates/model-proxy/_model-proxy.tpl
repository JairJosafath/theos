{{/*
The model proxy (docs/plans/model-proxy.md, decisions 2 and 7): the one place
the factory's Claude token is used. Hand-written, after templates/ui: the helm
plugin regenerates only templates/manager; if make chart ever emits copies of
config/model-proxy's objects elsewhere under templates/, delete them.

The names are fixed, not the release's: a sandbox reaches the proxy at
http://c4-model-proxy.<release namespace>.svc:8080 (decision 4) and the
agent fence names its pods by app.kubernetes.io/name (decision 6).
*/}}
{{- define "c4-factory.modelProxyName" -}}
c4-model-proxy
{{- end }}

{{- define "c4-factory.modelProxySelectorLabels" -}}
app.kubernetes.io/name: c4-model-proxy
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "c4-factory.modelProxyLabels" -}}
{{ include "c4-factory.modelProxySelectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "c4-factory.name" . }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{/*
The operator image: the proxy is its second binary (Dockerfile), so a release
builds and signs no image of its own for it.
*/}}
{{- define "c4-factory.modelProxyImage" -}}
{{- $repo := .Values.manager.image.repository | default "controller" -}}
{{- $repo }}{{- if not (contains "@" $repo) }}:{{ .Values.manager.image.tag | default .Chart.AppVersion }}{{- end }}
{{- end }}
