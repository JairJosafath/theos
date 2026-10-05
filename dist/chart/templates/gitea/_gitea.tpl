{{/*
Gitea, the seed and the CI runners. Hand-written: the helm plugin generates only
from dist/install.yaml, and the Kind test environment installs these from
config/gitea, hack/seed and config/runner instead. A change to Gitea's settings
belongs in both config/gitea and here.
*/}}
{{- define "c4-factory.giteaNamespace" -}}
{{- .Values.gitea.namespace | default .Release.Namespace }}
{{- end }}

{{/* Gitea's in-cluster URL, without a trailing slash. */}}
{{- define "c4-factory.giteaURL" -}}
{{- printf "http://gitea.%s.svc.cluster.local:3000" (include "c4-factory.giteaNamespace" .) }}
{{- end }}

{{/* Labels of a Gitea-side part; the name is its app.kubernetes.io/name. */}}
{{- define "c4-factory.giteaLabels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .context.Release.Name }}
app.kubernetes.io/managed-by: {{ .context.Release.Service }}
app.kubernetes.io/part-of: {{ include "c4-factory.name" .context }}
helm.sh/chart: {{ .context.Chart.Name }}-{{ .context.Chart.Version | replace "+" "_" }}
{{- end }}

{{/* The admin Secret: the chart's, or gitea.admin.existingSecret. */}}
{{- define "c4-factory.giteaAdminSecret" -}}
{{- .Values.gitea.admin.existingSecret | default "gitea-admin" }}
{{- end }}

{{/* global.imagePullSecrets as a pod's imagePullSecrets, or nothing. */}}
{{- define "c4-factory.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
