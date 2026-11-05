{{/*
Chat (docs/design.md, Chat; docs/plans/done/chat-matrix.md): theos-chat, which
posts in each Chat's Matrix rooms and runs its people's commands as them, and
an in-cluster Matrix homeserver. Hand-written, as templates/ui: config/chat is
not in config/default, so the helm plugin generates none of it.
*/}}
{{- define "theos.chatName" -}}
{{- include "theos.resourceName" (dict "suffix" "chat" "context" .) }}
{{- end }}

{{- define "theos.chatSelectorLabels" -}}
app.kubernetes.io/name: {{ include "theos.name" . }}-chat
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "theos.chatLabels" -}}
{{ include "theos.chatSelectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "theos.name" . }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{/* The in-cluster homeserver: its StatefulSet, Service and the names of its Secrets. */}}
{{- define "theos.matrixName" -}}
{{- include "theos.resourceName" (dict "suffix" "matrix" "context" .) }}
{{- end }}

{{- define "theos.matrixSelectorLabels" -}}
app.kubernetes.io/name: {{ include "theos.name" . }}-matrix
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "theos.matrixLabels" -}}
{{ include "theos.matrixSelectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "theos.name" . }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{/*
Whether the in-cluster homeserver is installed: chat.homeserver.enabled, which
needs chat.enabled. "true" or empty.
*/}}
{{- define "theos.matrixEnabled" -}}
{{- $chat := .Values.chat | default dict }}
{{- if dig "homeserver" "enabled" false $chat }}
{{- if not $chat.enabled }}
{{- fail "chat.homeserver.enabled needs chat.enabled: the homeserver serves theos-chat's rooms" }}
{{- end }}true
{{- end }}
{{- end }}

{{/* The in-cluster homeserver's address inside the cluster. */}}
{{- define "theos.matrixURL" -}}
http://{{ include "theos.matrixName" . }}.{{ .Release.Namespace }}.svc:8008
{{- end }}

{{/*
The operator's --chat-homeserver, the homeserver of a Chat that names none:
chat.homeserverURL, else the in-cluster one with chat.homeserver.enabled, else
none. Only with chat.enabled.
*/}}
{{- define "theos.chatHomeserver" -}}
{{- $chat := .Values.chat | default dict }}
{{- if $chat.enabled }}
{{- $url := $chat.homeserverURL }}
{{- if $url }}
{{- if not (regexMatch "^https?://[^/?#[:space:]]+(/[^?#[:space:]]*)?$" $url) }}
{{- fail (printf "chat.homeserverURL %q must be an http or https address, with no query or fragment" $url) }}
{{- end }}
{{- $url }}
{{- else if include "theos.matrixEnabled" . }}
{{- include "theos.matrixURL" . }}
{{- end }}
{{- end }}
{{- end }}

{{/*
The homeserver's image: repository:tag@digest, pinned by digest unless the
digest is set empty.
*/}}
{{- define "theos.matrixImage" -}}
{{- $img := .Values.chat.homeserver.image }}
{{- $ref := printf "%s:%s" $img.repository (required "chat.homeserver.image.tag is required" $img.tag) }}
{{- with $img.digest }}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" .) }}
{{- fail (printf "chat.homeserver.image.digest %q must be sha256:<64 hex digits>" .) }}
{{- end }}
{{- $ref = printf "%s@%s" $ref . }}
{{- end }}
{{- $ref }}
{{- end }}

{{/*
The kubectl image the homeserver's credentials container and the accounts Job
run on: the seed's (gitea.seed.image), which has bash, curl, jq, kubectl and
openssl.
*/}}
{{- define "theos.chatToolsImage" -}}
{{- $seed := .Values.gitea.seed.image }}
{{- printf "%s:%s" $seed.repository ($seed.tag | default .Chart.AppVersion) }}
{{- end }}

{{/*
The namespaces the accounts Job puts the bot's token Secret in: each of
gitea.projects and chat.homeserver.bot.namespaces, once. A JSON list.
*/}}
{{- define "theos.chatBotNamespaces" -}}
{{- $out := include "theos.projectNames" . | fromJsonArray }}
{{- range .Values.chat.homeserver.bot.namespaces }}
{{- $out = append $out . }}
{{- end }}
{{- $out | uniq | toJson }}
{{- end }}

{{/*
chat.homeserver.accounts, each checked: a Matrix localpart that is also a
Secret name's suffix. A JSON list.
*/}}
{{- define "theos.matrixAccounts" -}}
{{- $out := list }}
{{- range .Values.chat.homeserver.accounts }}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9.-]{0,40}[a-z0-9])?$" .) }}
{{- fail (printf "chat.homeserver.accounts: %q must be a localpart of lowercase letters, digits, dots and dashes" .) }}
{{- end }}
{{- $out = append $out . }}
{{- end }}
{{- $out | uniq | toJson }}
{{- end }}
