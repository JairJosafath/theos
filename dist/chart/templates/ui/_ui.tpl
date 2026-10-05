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

{{/*
ui.components as JSON: an empty namespace is the release's, and the ui entry's
empty selector is the UI's own selector labels. With gitea.enabled, Gitea and the
CI runners follow; the UI sums the runners' CPU as CI's (key runner).
*/}}
{{- define "c4-factory.uiComponents" -}}
{{- $out := list }}
{{- range .Values.ui.components }}
{{- $c := deepCopy . }}
{{- if not $c.namespace }}
{{- $_ := set $c "namespace" $.Release.Namespace }}
{{- end }}
{{- if and (eq $c.key "ui") (not $c.selector) }}
{{- $_ := set $c "selector" (printf "app.kubernetes.io/name=%s-ui,app.kubernetes.io/instance=%s" (include "c4-factory.name" $) $.Release.Name) }}
{{- end }}
{{- $out = append $out $c }}
{{- end }}
{{- if .Values.gitea.enabled }}
{{- $ns := include "c4-factory.giteaNamespace" . }}
{{- $sel := printf "app.kubernetes.io/instance=%s" .Release.Name }}
{{- $out = append $out (dict "key" "gitea" "name" "Gitea" "role" "Holds the templates and the Container repositories" "namespace" $ns "selector" (printf "app.kubernetes.io/name=gitea,%s" $sel)) }}
{{- $out = append $out (dict "key" "runner" "name" "CI runners" "role" "Run Container CI jobs" "namespace" $ns "selector" (printf "app.kubernetes.io/component=ci-runner,%s" $sel)) }}
{{- end }}
{{- toJson $out }}
{{- end }}
