{{/*
Chart name, used as a label value.
*/}}
{{- define "loom.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{/*
Release-scoped resource name prefix.
*/}}
{{- define "loom.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "loom.labels" -}}
app.kubernetes.io/name: {{ include "loom.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "loom.selectorLabels" -}}
app.kubernetes.io/name: {{ include "loom.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Name of the Secret carrying CE_DATABASE_URL / CE_EMBEDDER_API_KEY: either the
chart's own (rendered by templates/secret.yaml) or the caller's existingSecret.
*/}}
{{- define "loom.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{ .Values.secrets.existingSecret }}
{{- else -}}
{{ include "loom.fullname" . }}
{{- end -}}
{{- end -}}

{{/*
CE_DATABASE_URL: an explicit override wins; otherwise derive from the
in-chart Postgres StatefulSet (postgres.enabled) or fall back to
postgres.externalUrl (ADR-0007: postgres.enabled vs. externalUrl).
*/}}
{{- define "loom.databaseUrl" -}}
{{- if .Values.secrets.CE_DATABASE_URL -}}
{{ .Values.secrets.CE_DATABASE_URL }}
{{- else if .Values.postgres.enabled -}}
{{ printf "postgres://%s:%s@%s-postgres:5432/%s?sslmode=disable" .Values.postgres.auth.user .Values.postgres.auth.password (include "loom.fullname" .) .Values.postgres.auth.database }}
{{- else -}}
{{ .Values.postgres.externalUrl }}
{{- end -}}
{{- end -}}
