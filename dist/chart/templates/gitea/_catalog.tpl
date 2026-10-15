{{/*
The objects the seed applies in place (hack/seed/catalog.sh): each project's
GitProvider, catalog and Agents, rendered into ConfigMap c4factory-catalog
(catalog-configmap.yaml). Not release objects: on an install their kinds exist
only once the release's CRDs do. Not hooks: Helm re-creates a hook on every
upgrade, so running Tasks would lose their Agent and Git steps their
GitProvider, and hand edits would be lost (docs/plans/done/upgrade-in-place.md).
*/}}
{{- define "c4-factory.catalogObjects" -}}
{{- if .Values.gitea.enabled }}
{{- range .Values.gitea.projects | uniq }}
---
# Project {{ . }} scaffolds into its organization in the release's Gitea, with
# the token the seed stored.
apiVersion: c4.factory.io/v1alpha1
kind: GitProvider
metadata:
  name: gitea
  namespace: {{ . }}
  labels:
    app.kubernetes.io/managed-by: c4factory-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  provider: gitea
  url: {{ include "c4-factory.giteaURL" $ | quote }}
  owner: {{ . | quote }}
  credentialsSecretRef:
    name: gitea-credentials
{{- end }}
{{- end }}
{{- if and .Values.gitea.enabled .Values.catalog.enabled }}
{{- range $project := .Values.gitea.projects | uniq }}
{{- range $.Values.catalog.entries }}
---
# Project {{ $project }}'s catalog entry {{ .name }}: the template the seed
# pushed to the templates organization, pinned to its tag.
apiVersion: c4.factory.io/v1alpha1
kind: {{ .kind }}
metadata:
  name: {{ .name }}
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: c4factory-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  {{- toYaml (omit .spec "template") | nindent 2 }}
  template:
    url: {{ printf "%s/templates/%s" (include "c4-factory.giteaURL" $) .name | quote }}
    ref: {{ required (printf "catalog.entries: %s needs a ref, a tag the seed pushes" .name) .ref | quote }}
{{- end }}
{{- end }}
{{- end }}
{{- if and .Values.gitea.enabled .Values.agents.enabled }}
{{- $a := .Values.agents }}
{{- $image := printf "%s:%s" $a.image.repository ($a.image.tag | default .Chart.AppVersion) }}
{{- range $project := .Values.gitea.projects | uniq }}
{{- range $role := list "planner" "designer" "implementer" "reviewer" }}
{{- $agent := get $a $role }}
{{- if regexMatch "(?i)fable" $agent.model }}
{{- fail (printf "agents.%s.model: %s is a Fable model, which the factory never uses" $role $agent.model) }}
{{- end }}
---
# Project {{ $project }}'s {{ $role }}: Claude Code reading Secret
# {{ $a.credentialsSecret }}, which the person creates.
apiVersion: c4.factory.io/v1alpha1
kind: Agent
metadata:
  name: {{ $role }}
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: c4factory-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  displayName: {{ printf "Claude Code, %s (%s)" ($agent.model | trimPrefix "claude-" | splitList "-" | first | title) $role | quote }}
  description: {{ $agent.description | quote }}
  image: {{ $image | quote }}
  harness: claude-code
  model: {{ $agent.model | quote }}
  credentialsSecretRef:
    name: {{ $a.credentialsSecret }}
  maxRounds: {{ $agent.maxRounds }}
  timeoutMinutes: {{ $agent.timeoutMinutes }}
  {{- with $agent.resources }}
  resources:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
{{- with $a.standIn }}
{{- if .enabled }}
---
# Project {{ $project }}'s stand-in: no model and no token, for trying the loop.
apiVersion: c4.factory.io/v1alpha1
kind: Agent
metadata:
  name: stand-in
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: c4factory-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  displayName: Stand-in
  description: {{ .description | quote }}
  image: {{ $image | quote }}
  harness: stand-in
  maxRounds: {{ .maxRounds }}
  timeoutMinutes: {{ .timeoutMinutes }}
  {{- with .resources }}
  resources:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
