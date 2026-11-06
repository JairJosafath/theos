{{/*
The objects the seed applies in place (hack/seed/catalog.sh): each project's
GitProvider, catalog entries and bundles, and Agents, rendered into ConfigMap theos-catalog
(catalog-configmap.yaml). Not release objects: on an install their kinds exist
only once the release's CRDs do. Not hooks: Helm re-creates a hook on every
upgrade, so running Tasks would lose their Agent and Git steps their
GitProvider, and hand edits would be lost (docs/plans/done/upgrade-in-place.md).
*/}}
{{- define "theos.catalogObjects" -}}
{{- if .Values.gitea.enabled }}
{{- range include "theos.projects" . | fromJsonArray }}
---
# Project {{ .name }} scaffolds into its organization in the release's Gitea,
# with the token the seed stored.
apiVersion: theos.factory.io/v1beta1
kind: GitProvider
metadata:
  name: gitea
  namespace: {{ .name }}
  labels:
    app.kubernetes.io/managed-by: theos-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  provider: gitea
  url: {{ include "theos.giteaURL" $ | quote }}
  owner: {{ .name | quote }}
  credentialsSecretRef:
    name: gitea-credentials
  {{- with .packages }}
  packages:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- include "theos.gitProviderCI" $ | nindent 2 }}
{{- end }}
{{- end }}
{{- if and .Values.gitea.enabled .Values.catalog.enabled }}
{{- range $project := include "theos.projectNames" . | fromJsonArray }}
{{- range $.Values.catalog.entries }}
---
# Project {{ $project }}'s catalog entry {{ .name }}: the template the seed
# pushed to the templates organization, pinned to its tag. The template is
# named like the entry unless the entry's template says otherwise (the Domain
# webshop is templates/webshop-domain).
apiVersion: theos.factory.io/v1beta1
kind: {{ .kind }}
metadata:
  name: {{ .name }}
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: theos-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  {{- toYaml (omit .spec "template") | nindent 2 }}
  template:
    url: {{ printf "%s/templates/%s" (include "theos.giteaURL" $) (.template | default .name) | quote }}
    ref: {{ required (printf "catalog.entries: %s needs a ref, a tag the seed pushes" .name) .ref | quote }}
{{- end }}
{{- range $.Values.catalog.bundles }}
---
# Project {{ $project }}'s bundle {{ .name }}: entries above picked together,
# which a Container names with bundleRef instead of its four sides.
apiVersion: theos.factory.io/v1beta1
kind: Bundle
metadata:
  name: {{ .name }}
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: theos-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  {{- toYaml (required (printf "catalog.bundles: %s needs a spec with frameworkRef" .name) .spec) | nindent 2 }}
{{- end }}
{{- end }}
{{- end }}
{{- if and .Values.gitea.enabled .Values.testEnvironments.browser.enabled }}
{{- $b := .Values.testEnvironments.browser }}
{{- range $project := include "theos.projectNames" . | fromJsonArray }}
---
# Project {{ $project }}'s browser (docs/plans/done/test-environments.md, T14): a
# pod per lease, which a run of an issue labelled env/browser takes.
apiVersion: theos.factory.io/v1beta1
kind: TestEnvironment
metadata:
  name: browser
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: theos-chart
    app.kubernetes.io/instance: {{ $.Release.Name }}
spec:
  description: {{ $b.description | quote }}
  local:
    image: {{ printf "%s:%s" $b.image.repository ($b.image.tag | default $.Chart.AppVersion) | quote }}
    {{- with $b.resources }}
    resources:
      {{- toYaml . | nindent 6 }}
    {{- end }}
  {{- with $b.limits }}
  limits:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with $b.ci }}
  ci:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
{{- end }}
{{- if and .Values.gitea.enabled .Values.agents.enabled }}
{{- $a := .Values.agents }}
{{- $image := printf "%s:%s" $a.image.repository ($a.image.tag | default .Chart.AppVersion) }}
{{- range $project := include "theos.projectNames" . | fromJsonArray }}
{{- range $role := list "planner" "designer" "implementer" "reviewer" }}
{{- $agent := get $a $role }}
{{- if regexMatch "(?i)fable" $agent.model }}
{{- fail (printf "agents.%s.model: %s is a Fable model, which the factory never uses" $role $agent.model) }}
{{- end }}
---
# Project {{ $project }}'s {{ $role }}: Claude Code reading Secret
# {{ $a.credentialsSecret }}, which the person creates.
apiVersion: theos.factory.io/v1beta1
kind: Agent
metadata:
  name: {{ $role }}
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: theos-chart
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
apiVersion: theos.factory.io/v1beta1
kind: Agent
metadata:
  name: stand-in
  namespace: {{ $project }}
  labels:
    app.kubernetes.io/managed-by: theos-chart
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

{{/*
The ci block of every GitProvider the chart renders: a pool of single-job
runner pods per label of runners.* whose pool.size is above 0, on that
runner's image and resources, kept by the operator in the project's
namespace. Without it a project's checks have no runner: the shared runners
serve the templates organization only (#1698). Empty when no label has a pool.
*/}}
{{- define "theos.gitProviderCI" -}}
{{- $pools := list }}
{{- range $label, $r := .Values.runners }}
{{- if $r.pool }}{{ if gt (int $r.pool.size) 0 }}{{ $pools = append $pools $label }}{{ end }}{{ end }}
{{- end }}
{{- if $pools -}}
ci:
  giteaActions:
    runners:
{{- range $label := $pools }}
{{- $r := index $.Values.runners $label }}
      - label: {{ $label }}
        image: {{ printf "%s:%s" $r.image.repository ($r.image.tag | default $.Chart.AppVersion) | quote }}
        size: {{ int $r.pool.size }}
{{- if $r.goCache }}
        goCache: true
{{- else if $r.freshGoCache }}
        freshGoCache: true
{{- end }}
{{- with $r.resources }}
        resources:
          {{- toYaml . | nindent 10 }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
