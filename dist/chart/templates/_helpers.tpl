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

{{/*
The peers the CI fence lets DNS (port 53) out to: one per clusterDNS entry,
namespace/key=value[,key=value] (a namespace and pod labels) or an IPv4
address block inside the private ranges (a bare address means /32). The
operator's --cluster-dns takes the same entries with the same grammar, for the
agent fence (#1257), so one value renders the same peer for both. An empty
list, a public block or a selector expression fails the render: the operator
would refuse the same value, and a DNS rule with no peer would let port 53 out
to every address.
*/}}
{{- define "c4-factory.clusterDNSPeers" -}}
{{- if not .Values.clusterDNS }}
{{- fail "clusterDNS: want at least one entry, where cluster DNS runs (kube-system/k8s-app=kube-dns by default); none would open the CI fence on port 53 to every address" }}
{{- end }}
{{- $octet := "(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])" }}
{{- $address := printf "^%s(\\.%s){3}(/([0-9]|[12][0-9]|3[0-2]))?$" $octet $octet }}
{{- range .Values.clusterDNS }}
{{- $entry := toString . }}
{{- if regexMatch "^[0-9.]+(/[0-9]+)?$" $entry }}
{{- if not (regexMatch $address $entry) }}
{{- fail (printf "clusterDNS entry %q: not an IPv4 address block" $entry) }}
{{- end }}
{{- $cidr := ternary $entry (printf "%s/32" $entry) (contains "/" $entry) }}
{{- $bits := (splitn "/" 2 $cidr)._1 | int }}
{{- $private := or (and (hasPrefix "10." $cidr) (ge $bits 8)) (and (regexMatch "^172\\.(1[6-9]|2[0-9]|3[01])\\." $cidr) (ge $bits 12)) (and (hasPrefix "192.168." $cidr) (ge $bits 16)) (and (hasPrefix "169.254." $cidr) (ge $bits 16)) }}
{{- if not $private }}
{{- fail (printf "clusterDNS entry %q: not an address block inside the private ranges 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16" $entry) }}
{{- end }}
- ipBlock:
    cidr: {{ $cidr }}
{{- else if contains "/" $entry }}
{{- $parts := splitn "/" 2 $entry }}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $parts._0) }}
{{- fail (printf "clusterDNS entry %q: namespace %q: want a DNS label" $entry $parts._0) }}
{{- end }}
- namespaceSelector:
    matchLabels: {kubernetes.io/metadata.name: {{ $parts._0 | quote }}}
  podSelector:
    matchLabels:
      {{- range splitList "," $parts._1 }}
      {{- if not (regexMatch "^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)?=[A-Za-z0-9._-]*$" .) }}
      {{- fail (printf "clusterDNS entry %q: pod label %q: want key=value" $entry .) }}
      {{- end }}
      {{- $kv := splitn "=" 2 . }}
      {{ $kv._0 | quote }}: {{ $kv._1 | quote }}
      {{- end }}
{{- else }}
{{- fail (printf "clusterDNS entry %q: want namespace/key=value[,key=value] or an IPv4 address block inside the private ranges" $entry) }}
{{- end }}
{{- end }}
{{- end }}
