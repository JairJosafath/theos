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

{{/*
How the UI acts (design.md, UI actions), the server's UI_ACTIONS: empty with
ui.actions.enabled off; oidc with ui.auth.oidc set (even empty), every write as the person
signed in; else service-account, over port-forward. Behind an Ingress
(ui.allowedHosts) the ServiceAccount never acts: anything else that reaches
the pod could act through it.
*/}}
{{- define "c4-factory.uiActions" -}}
{{- if dig "actions" "enabled" false .Values.ui }}
{{- if kindIs "map" (dig "auth" "oidc" nil .Values.ui) }}
{{- $oidc := .Values.ui.auth.oidc }}
{{- range list "issuerURL" "clientID" "existingSecret" }}
{{- if not (get $oidc .) }}
{{- fail (printf "ui.auth.oidc.%s is required: the UI signs people in with it" .) }}
{{- end }}
{{- end }}
{{- if not (hasPrefix "https://" $oidc.issuerURL) }}
{{- fail "ui.auth.oidc.issuerURL must be https: the UI reads the issuer's keys from it" }}
{{- end }}oidc
{{- else if .Values.ui.allowedHosts }}
{{- fail "ui.actions.enabled with ui.allowedHosts needs ui.auth.oidc: behind an Ingress the UI acts only as a person signed in" }}
{{- else }}service-account
{{- end }}
{{- end }}
{{- end }}

{{/*
A ui.auth.oidc setting, or its default when unset: "" is a setting, so an
empty prefix stays empty. Takes (list $oidc key default).
*/}}
{{- define "c4-factory.uiOIDC" -}}
{{- $oidc := index . 0 }}{{- $key := index . 1 }}
{{- if hasKey $oidc $key }}{{ get $oidc $key }}{{ else }}{{ index . 2 }}{{ end }}
{{- end }}

{{/*
The default sessionBinaryURL, where a scaffolded devcontainer's c4session
feature downloads the Session client. The UI image carries the client for
linux amd64 and arm64 and serves it under /downloads/ (ui/Dockerfile), so a
release of the private repository needs no GitHub login to download it
(#1151): behind an Ingress (ui.allowedHosts) its first host over https, else
the port-forward's localhost and the UI's port. With the UI off, the binaries
attached to this chart's GitHub release.
*/}}
{{- define "c4-factory.sessionBinaryURL" -}}
{{- if not .Values.ui.enabled -}}
https://github.com/JairJosafath/c4factory/releases/download/v{{ .Chart.AppVersion }}/c4session-linux-${arch}
{{- else if .Values.ui.allowedHosts -}}
https://{{ index .Values.ui.allowedHosts 0 }}/downloads/c4session-linux-${arch}
{{- else -}}
http://localhost:{{ .Values.ui.service.port }}/downloads/c4session-linux-${arch}
{{- end -}}
{{- end }}
