/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package chartgen

import (
	"fmt"
	"os"
)

const helpersTpl = `{{/*
Chart name, truncated to 63 chars.
*/}}
{{- define "chart.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Release fullname, truncated to 63 chars.
*/}}
{{- define "chart.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Standard labels.
*/}}
{{- define "chart.labels" -}}
app.kubernetes.io/name: {{ include "chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{/*
Selector labels (subset of standard labels for matchLabels).
*/}}
{{- define "chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Canonical image reference. Prefer fullRef when explicitly set.
*/}}
{{- define "chart.imageRef" -}}
{{- if .Values.image.fullRef -}}
{{- .Values.image.fullRef -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- end -}}
{{- end }}
`

const chartYAMLTemplate = `apiVersion: v2
name: %s
description: ODH AI Gateway Operator Helm chart
version: %s
appVersion: "%s"
type: application
`

// writeHelpersTpl writes the _helpers.tpl file.
func writeHelpersTpl(path string) error {
	return os.WriteFile(path, []byte(helpersTpl), 0o644)
}

// writeChartYAML writes a Chart.yaml file.
func writeChartYAML(
	path string,
	name string,
	version string,
) error {
	content := fmt.Sprintf(chartYAMLTemplate, name, version, version)

	return os.WriteFile(path, []byte(content), 0o644)
}
