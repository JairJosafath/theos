{{/*
Expand the name of the chart.
*/}}
{{- define "c4-factory.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "c4-factory.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Namespace for generated references.
Always uses the Helm release namespace.
*/}}
{{- define "c4-factory.namespaceName" -}}
{{- .Release.Namespace }}
{{- end }}

{{/*
Resource name with proper truncation for Kubernetes 63-character limit.
Takes a dict with:
  - .suffix: Resource name suffix (e.g., "metrics", "webhook")
  - .context: Template context (root context with .Values, .Release, etc.)
Dynamically calculates safe truncation to ensure total name length <= 63 chars.
*/}}
{{- define "c4-factory.resourceName" -}}
{{- $fullname := include "c4-factory.fullname" .context }}
{{- $suffix := .suffix }}
{{- $maxLen := sub 62 (len $suffix) | int }}
{{- if gt (len $fullname) $maxLen }}
{{- printf "%s-%s" (trunc $maxLen $fullname | trimSuffix "-") $suffix | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" $fullname $suffix | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
ServiceAccount name to use.
When enabled, use the chart's ServiceAccount name.
When disabled, serviceAccount.name must be set; use "default" to pick the namespace default ServiceAccount.
*/}}
{{- define "c4-factory.serviceAccountName" -}}
{{- if .Values.serviceAccount.enabled }}
{{- include "c4-factory.resourceName" (dict "suffix" "controller-manager" "context" .) }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.enabled=false (set name: default explicitly to use the namespace default ServiceAccount)" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
gitea.projects, one object per name in order: an entry is a name, or an object
with a name and, optionally, the GitProvider's packages (spec.packages). A JSON
list; read it with fromJsonArray.
*/}}
{{- define "c4-factory.projects" -}}
{{- $projects := list }}
{{- $seen := dict }}
{{- range $i, $p := .Values.gitea.projects }}
{{- if kindIs "string" $p }}
{{- $p = dict "name" $p }}
{{- end }}
{{- $name := required (printf "gitea.projects[%d]: a project is a name, or an object with a name" $i) $p.name }}
{{- if not (hasKey $seen $name) }}
{{- $_ := set $seen $name true }}
{{- $projects = append $projects $p }}
{{- end }}
{{- end }}
{{- $projects | toJson }}
{{- end }}

{{/*
The names of gitea.projects, each once, in order. A JSON list; read it with
fromJsonArray.
*/}}
{{- define "c4-factory.projectNames" -}}
{{- $names := list }}
{{- range include "c4-factory.projects" . | fromJsonArray }}
{{- $names = append $names .name }}
{{- end }}
{{- $names | toJson }}
{{- end }}
