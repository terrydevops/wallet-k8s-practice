{{- define "go-service.name" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "go-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "go-service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "go-service.labels" -}}
{{ include "go-service.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/part-of: wallet-k8s-practice
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{/* repository@digest when a digest is pinned (image.digest, else digests[cmd] from the release),
     else repository:tag (tag defaults to appVersion). */}}
{{- define "go-service.image" -}}
{{- $digest := .Values.image.digest | default (get .Values.digests .Values.cmd) -}}
{{- if $digest -}}
{{ .Values.image.repository }}@{{ $digest }}
{{- else -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}
{{- end -}}
