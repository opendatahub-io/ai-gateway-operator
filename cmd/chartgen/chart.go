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
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	gvk "github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
)

const (
	yamlFieldNamespace          = "namespace:"
	yamlFieldServiceAccountName = "serviceAccountName:"
	yamlFieldSubjects           = "subjects:"
	yamlFieldName               = "name:"
	yamlFieldImage              = "image:"
	yamlFieldImagePullPolicy    = "imagePullPolicy:"
	yamlFieldReplicas           = "replicas:"
	yamlFieldResources          = "resources:"
	yamlFieldData               = "data:"
	yamlFieldMetadata           = "metadata:"

	tplReleaseNamespace   = "namespace: {{ .Values.operatorNamespace }}"
	tplServiceAccountName = `{{ default (include "chart.fullname" .) .Values.serviceAccount.name }}`

	annotationCertManagerInjectCAFrom = "cert-manager.io/inject-ca-from"
)

// renderGroup renders a group of resources with the same GVK into a single
// Helm template file, applying kind-specific transformations.
func renderGroup(
	resourceGVK schema.GroupVersionKind,
	resources []unstructured.Unstructured,
) (string, error) {
	var parts []string

	for i := range resources {
		transformed, err := transformResource(resourceGVK, &resources[i])
		if err != nil {
			return "", fmt.Errorf("transforming %s/%s: %w", resourceGVK.Kind, resources[i].GetName(), err)
		}

		parts = append(parts, transformed)
	}

	return strings.Join(parts, "\n---\n"), nil
}

// transformResource applies Helm template transformations to a resource
// based on its kind.
func transformResource(
	resourceGVK schema.GroupVersionKind,
	obj *unstructured.Unstructured,
) (string, error) {
	switch resourceGVK {
	case gvk.Deployment:
		return transformDeployment(obj)
	case gvk.ServiceAccount:
		return transformServiceAccount(obj)
	case gvk.ConfigMap:
		return transformConfigMap(obj)
	case gvk.ClusterRoleBinding, gvk.RoleBinding:
		return transformRoleBinding(obj)
	case gvk.MutatingWebhookConfiguration, gvk.ValidatingWebhookConfiguration:
		return transformWebhook(obj)
	case gvk.CertManagerCertificate:
		return transformCertificate(obj)
	default:
		return transformGeneric(obj)
	}
}

// transformDeployment injects Helm value references for image, resources,
// replicas, imagePullSecrets, and serviceAccountName.
func transformDeployment(obj *unstructured.Unstructured) (string, error) {
	raw, err := marshalResource(obj)
	if err != nil {
		return "", err
	}

	raw = replaceNamespace(raw)

	// Replace the image field value
	raw = replaceImageField(raw)

	// Replace replicas
	raw = replaceReplicas(raw)

	// Replace resources block
	raw = replaceResources(raw)

	// Replace serviceAccountName
	raw = replaceServiceAccountName(raw)

	// Add imagePullSecrets
	raw = addImagePullSecrets(raw)

	return raw, nil
}

// transformServiceAccount injects Helm value references for the name and
// annotations, and always creates the ServiceAccount.
func transformServiceAccount(obj *unstructured.Unstructured) (string, error) {
	raw, err := marshalResource(obj)
	if err != nil {
		return "", err
	}

	raw = replaceNamespace(raw)
	raw = replaceServiceAccountMetadata(raw, obj.GetName())

	return raw, nil
}

// transformConfigMap injects Helm value merging for .Values.config and
// .Values.imagePullSecret.
func transformConfigMap(obj *unstructured.Unstructured) (string, error) {
	raw, err := marshalResource(obj)
	if err != nil {
		return "", err
	}

	raw = replaceNamespace(raw)
	raw = injectConfigMapValues(raw)

	return raw, nil
}

// transformRoleBinding replaces the subjects namespace with the Helm
// release namespace and serviceAccountName with the values reference.
func transformRoleBinding(obj *unstructured.Unstructured) (string, error) {
	raw, err := marshalResource(obj)
	if err != nil {
		return "", err
	}

	raw = replaceNamespace(raw)
	raw = replaceSubjectsNamespace(raw)
	raw = replaceSubjectsServiceAccount(raw)

	return raw, nil
}

// transformWebhook replaces webhook service namespace references and
// cert-manager annotation namespace.
func transformWebhook(obj *unstructured.Unstructured) (string, error) {
	raw, err := marshalResource(obj)
	if err != nil {
		return "", err
	}

	raw = replaceWebhookNamespace(raw)

	return raw, nil
}

// transformCertificate replaces hardcoded namespace references in
// cert-manager Certificate dnsNames with the Helm release namespace.
func transformCertificate(obj *unstructured.Unstructured) (string, error) {
	raw, err := marshalResource(obj)
	if err != nil {
		return "", err
	}

	raw = replaceNamespace(raw)
	raw = replaceCertificateDNSNames(raw)

	return raw, nil
}

// replaceCertificateDNSNames replaces hardcoded namespace segments in
// Certificate dnsNames entries with the Helm release namespace template.
// dnsNames follow the pattern: <service>.<namespace>.svc[.cluster.local]
func replaceCertificateDNSNames(raw string) string {
	lines := strings.Split(raw, "\n")
	inDNSNames := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "dnsNames:" {
			inDNSNames = true

			continue
		}

		// End dnsNames section when we hit a non-list-item line.
		if inDNSNames && !strings.HasPrefix(trimmed, "-") && trimmed != "" {
			inDNSNames = false
		}

		if inDNSNames && strings.HasPrefix(trimmed, "- ") && strings.Contains(trimmed, ".svc") {
			// Extract the service name (first segment before the first dot).
			entry := strings.TrimPrefix(trimmed, "- ")
			parts := strings.SplitN(entry, ".", 3)
			if len(parts) >= 3 {
				indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
				suffix := parts[2] // "svc" or "svc.cluster.local"
				lines[i] = indent + "- " + parts[0] + ".{{ .Values.operatorNamespace }}." + suffix
			}
		}
	}

	return strings.Join(lines, "\n")
}

// transformGeneric replaces the namespace for namespaced resources and
// passes cluster-scoped resources through as-is.
func transformGeneric(obj *unstructured.Unstructured) (string, error) {
	raw, err := marshalResource(obj)
	if err != nil {
		return "", err
	}

	if obj.GetNamespace() != "" {
		raw = replaceNamespace(raw)
	}

	return raw, nil
}

// marshalResource marshals an unstructured resource to YAML.
func marshalResource(obj *unstructured.Unstructured) (string, error) {
	data, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("marshaling resource: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}

// replaceNamespace replaces hardcoded namespace values with the Helm
// release namespace template.
func replaceNamespace(raw string) string {
	lines := strings.Split(raw, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, yamlFieldNamespace) && !strings.Contains(trimmed, "{{") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			lines[i] = indent + tplReleaseNamespace
		}
	}

	return strings.Join(lines, "\n")
}

// replaceImageField replaces the container image value and imagePullPolicy
// with Helm template references.
func replaceImageField(raw string) string {
	lines := strings.Split(raw, "\n")
	var result []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, yamlFieldImage) && !strings.Contains(trimmed, "{{"):
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			result = append(result, indent+`image: "{{ include "chart.imageRef" . }}"`)
			result = append(result, indent+"imagePullPolicy: Always")
		case strings.HasPrefix(trimmed, yamlFieldImagePullPolicy):
			// Drop the original -- the templated version is injected above.
		default:
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// replaceReplicas replaces the replicas field with a Helm template reference.
func replaceReplicas(raw string) string {
	lines := strings.Split(raw, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, yamlFieldReplicas) && !strings.Contains(trimmed, "{{") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			lines[i] = indent + "replicas: {{ .Values.replicas }}"
		}
	}

	return strings.Join(lines, "\n")
}

// replaceResources replaces the resources block with a Helm toYaml reference.
func replaceResources(raw string) string {
	lines := strings.Split(raw, "\n")
	var result []string

	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == yamlFieldResources {
			indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
			result = append(result, indent+"resources:")
			result = append(result, indent+"  {{- toYaml .Values.resources | nindent "+fmt.Sprintf("%d", len(indent)+2)+" }}")

			// Skip the original resources block
			i++
			baseIndent := len(indent) + 2
			for i < len(lines) {
				lineIndent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
				if strings.TrimSpace(lines[i]) == "" || lineIndent >= baseIndent {
					i++
				} else {
					break
				}
			}

			continue
		}

		result = append(result, lines[i])
		i++
	}

	return strings.Join(result, "\n")
}

// replaceServiceAccountName replaces the serviceAccountName field with a
// Helm template reference.
func replaceServiceAccountName(raw string) string {
	lines := strings.Split(raw, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, yamlFieldServiceAccountName) && !strings.Contains(trimmed, "{{") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			lines[i] = indent + yamlFieldServiceAccountName + " " + tplServiceAccountName
		}
	}

	return strings.Join(lines, "\n")
}

// addImagePullSecrets adds an imagePullSecrets block after the
// serviceAccountName line if .Values.imagePullSecret is set.
func addImagePullSecrets(raw string) string {
	lines := strings.Split(raw, "\n")
	var result []string

	for _, line := range lines {
		result = append(result, line)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, yamlFieldServiceAccountName) {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			result = append(result,
				indent+"{{- with .Values.imagePullSecret }}",
				indent+"imagePullSecrets:",
				indent+"  - name: {{ . }}",
				indent+"{{- end }}",
			)
		}
	}

	return strings.Join(result, "\n")
}

// replaceServiceAccountMetadata replaces the ServiceAccount name and adds
// annotation templating.
func replaceServiceAccountMetadata(raw string, originalName string) string {
	lines := strings.Split(raw, "\n")
	var result []string

	inMetadata := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == yamlFieldMetadata {
			inMetadata = true
		} else if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			inMetadata = false
		}

		if inMetadata && strings.HasPrefix(trimmed, yamlFieldName) && strings.Contains(trimmed, originalName) {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			result = append(result, indent+yamlFieldName+" "+tplServiceAccountName)

			// Add annotations block
			result = append(result,
				indent+"{{- with .Values.serviceAccount.annotations }}",
				indent+"annotations:",
				indent+"  {{- toYaml . | nindent "+fmt.Sprintf("%d", len(indent)+2)+" }}",
				indent+"{{- end }}",
			)

			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// replaceSubjectsNamespace replaces the namespace in subjects entries
// with the Helm release namespace.
func replaceSubjectsNamespace(raw string) string {
	lines := strings.Split(raw, "\n")
	inSubjects := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == yamlFieldSubjects {
			inSubjects = true

			continue
		}

		// End subjects section when we hit a non-indented, non-list-item line
		if inSubjects && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
			!strings.HasPrefix(trimmed, "-") && trimmed != "" {
			inSubjects = false
		}

		if inSubjects && strings.HasPrefix(trimmed, yamlFieldNamespace) && !strings.Contains(trimmed, "{{") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			lines[i] = indent + tplReleaseNamespace
		}
	}

	return strings.Join(lines, "\n")
}

// replaceSubjectsServiceAccount replaces the service account name in
// subjects entries with the Helm value reference.
func replaceSubjectsServiceAccount(raw string) string {
	lines := strings.Split(raw, "\n")
	inSubjects := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == yamlFieldSubjects {
			inSubjects = true

			continue
		}

		// End subjects section when we hit a non-indented, non-list-item line
		if inSubjects && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
			!strings.HasPrefix(trimmed, "-") && trimmed != "" {
			inSubjects = false
		}

		if inSubjects && strings.HasPrefix(trimmed, yamlFieldName) && !strings.Contains(trimmed, "{{") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			lines[i] = indent + yamlFieldName + " " + tplServiceAccountName
		}
	}

	return strings.Join(lines, "\n")
}

// replaceWebhookNamespace replaces namespace references in webhook
// configurations (clientConfig.service.namespace and cert-manager annotations).
func replaceWebhookNamespace(raw string) string {
	// Replace service namespace in clientConfig
	raw = replaceNamespace(raw)

	// Replace namespace in cert-manager inject-ca-from annotation
	lines := strings.Split(raw, "\n")

	for i, line := range lines {
		if strings.Contains(line, annotationCertManagerInjectCAFrom) {
			// The annotation value is typically "namespace/certificate-name"
			// Replace the namespace part with the Helm template
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				value := strings.TrimSpace(parts[1])
				valueParts := strings.SplitN(value, "/", 2)
				if len(valueParts) == 2 {
					indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
					lines[i] = indent + strings.TrimSpace(parts[0]) + ": {{ .Values.operatorNamespace }}/" + valueParts[1]
				}
			}
		}
	}

	return strings.Join(lines, "\n")
}

// injectConfigMapValues replaces the ConfigMap data section with Helm
// template directives that merge .Values.config and .Values.imagePullSecret.
// The original static data entries are stripped because they are expected to
// come from .Values.config at render time.
func injectConfigMapValues(raw string) string {
	lines := strings.Split(raw, "\n")
	var result []string

	skipDataEntries := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == yamlFieldData {
			skipDataEntries = true
			result = append(result, line)

			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			result = append(result,
				indent+"  {{- with .Values.imagePullSecret }}",
				indent+"  imagePullSecret: {{ . }}",
				indent+"  {{- end }}",
				indent+"  {{- range $key, $val := .Values.config }}",
				indent+"  {{ $key }}: {{ $val | quote }}",
				indent+"  {{- end }}",
			)

			continue
		}

		if skipDataEntries {
			if trimmed != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				skipDataEntries = false
				result = append(result, line)
			}

			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}
