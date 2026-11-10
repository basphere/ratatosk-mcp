{{- define "ratatosk-mcp.fullname" -}}
{{- if contains .Chart.Name .Release.Name }}{{ .Release.Name | trunc 63 | trimSuffix "-" }}{{ else }}{{ printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}{{ end }}
{{- end }}

{{- define "ratatosk-mcp.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "ratatosk-mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
In-cluster MCP endpoint, fully qualified. kagent 1.x agents run in a sandbox
whose DNS answers only the hostnames its egress policy names, so a short
Service name (ratatosk-mcp, ratatosk-mcp.kagent) never resolves there; the
full name works for kagent 0.x and every other client too. (kagent
v1.0.0-rc1, docs/architecture/runtime-and-lifecycle.md, read 2026-11-03.)
*/}}
{{- define "ratatosk-mcp.mcpURL" -}}
http://{{ include "ratatosk-mcp.fullname" . }}.{{ .Release.Namespace }}.svc.{{ .Values.clusterDomain }}:{{ .Values.service.port }}/mcp
{{- end }}

{{/*
"true" when the cluster serves the kagent 1.x API (api.kagent.dev/v1alpha3),
empty otherwise. kagent 1.x does not convert kagent.dev/v1alpha2 objects (its
CRDs serve v1alpha3 only, no conversion; v1.0.0-rc1, 2026-10-30), so the
chart has to pick the shape the cluster actually serves. A cluster with both
groups (0.x CRDs left behind) gets 1.x. helm template sees no cluster: pass
--api-versions api.kagent.dev/v1alpha3 to render 1.x.
*/}}
{{- define "ratatosk-mcp.kagentV1" -}}
{{- if .Capabilities.APIVersions.Has "api.kagent.dev/v1alpha3" }}true{{ end }}
{{- end }}
