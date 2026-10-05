{{/*
The read-only UI. Hand-written: the helm plugin generates only from
dist/install.yaml, which holds config/default, not config/ui.
*/}}
{{- define "c4-factory.uiName" -}}
{{- include "c4-factory.resourceName" (dict "suffix" "ui" "context" .) }}
{{- end }}

{{- define "c4-factory.uiSelectorLabels" -}}
app.kubernetes.io/name: {{ include "c4-factory.name" . }}-ui
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "c4-factory.uiLabels" -}}
{{ include "c4-factory.uiSelectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "c4-factory.name" . }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{/* ui.components as JSON, an empty namespace replaced by the release's. */}}
{{- define "c4-factory.uiComponents" -}}
{{- $out := list }}
{{- range .Values.ui.components }}
{{- $c := deepCopy . }}
{{- if not $c.namespace }}
{{- $_ := set $c "namespace" $.Release.Namespace }}
{{- end }}
{{- $out = append $out $c }}
{{- end }}
{{- toJson $out }}
{{- end }}
