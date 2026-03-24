{{- define "forge-operator.labels" -}}
app.kubernetes.io/name: forge-operator
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: forge-platform
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{- define "forge-operator.selectorLabels" -}}
app.kubernetes.io/name: forge-operator
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "forge-operator.serviceAccountName" -}}
{{- default "forge-operator" .Values.serviceAccount.name }}
{{- end }}

{{- define "forge-operator.tokenSecretName" -}}
{{- if .Values.forge.existingSecret -}}
{{ .Values.forge.existingSecret }}
{{- else -}}
forge-operator-credentials
{{- end -}}
{{- end }}
