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
	"encoding/json"
	"fmt"
	"os"

	"github.com/invopop/jsonschema"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	gvk "github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
)

const (
	defaultImageRepository = "controller"
	defaultImageTag        = "latest"
	defaultLimitsCPU       = "500m"
	defaultLimitsMemory    = "128Mi"
	defaultRequestsCPU     = "10m"
	defaultRequestsMemory  = "64Mi"

	resourceKeyLimits   = "limits"
	resourceKeyRequests = "requests"
	resourceKeyCPU      = "cpu"
	resourceKeyMemory   = "memory"
)

// Values defines the Helm chart values structure.
type Values struct {
	// Image configures the container image for the manager.
	Image ImageSpec `json:"image"`

	// Replicas is the number of manager pod replicas.
	Replicas int32 `json:"replicas" jsonschema:"default=1,minimum=1"`

	// Resources configures CPU and memory requests/limits for the manager.
	Resources ResourceSpec `json:"resources"`

	// LeaderElect enables leader election for high availability.
	LeaderElect bool `json:"leaderElect" jsonschema:"default=true"`

	// ServiceAccount configures the operator's ServiceAccount.
	ServiceAccount ServiceAccountSpec `json:"serviceAccount"`

	// ImagePullSecret is the name of a pull secret for the manager image.
	// When set it is also injected into the controller ConfigMap so the
	// operator can propagate it to child resources.
	ImagePullSecret string `json:"imagePullSecret"`

	// OperatorNamespace is the namespace where the operator is deployed.
	// Injected at render time by the platform operator.
	OperatorNamespace string `json:"operatorNamespace"`

	// Config provides additional controller configuration entries that are
	// merged into the controller ConfigMap.
	Config map[string]string `json:"config,omitempty"`
}

// ImageSpec describes a container image.
type ImageSpec struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	FullRef    string `json:"fullRef"`
}

// ResourceSpec mirrors corev1.ResourceRequirements but with simpler
// serialization for Helm values.
type ResourceSpec struct {
	Limits   ResourceList `json:"limits,omitempty"`
	Requests ResourceList `json:"requests,omitempty"`
}

// ResourceList maps resource names to quantities.
type ResourceList struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// ServiceAccountSpec configures the operator's ServiceAccount.
type ServiceAccountSpec struct {
	// Name overrides the ServiceAccount name (defaults to release fullname).
	Name string `json:"name"`

	// Annotations are additional annotations on the ServiceAccount.
	Annotations map[string]string `json:"annotations"`
}

// DefaultValues returns a Values instance with sensible defaults.
func DefaultValues() Values {
	return Values{
		Image: ImageSpec{
			Repository: defaultImageRepository,
			Tag:        defaultImageTag,
		},
		Replicas: 1,
		Resources: ResourceSpec{
			Limits: ResourceList{
				CPU:    defaultLimitsCPU,
				Memory: defaultLimitsMemory,
			},
			Requests: ResourceList{
				CPU:    defaultRequestsCPU,
				Memory: defaultRequestsMemory,
			},
		},
		LeaderElect: true,
		ServiceAccount: ServiceAccountSpec{
			Annotations: map[string]string{},
		},
		Config: map[string]string{
			"platform-type":    "OpenDataHub",
			"platform-version": "unknown",
		},
	}
}

// ExtractDefaults extracts default values from the kustomize resources,
// primarily from the Deployment spec.
func ExtractDefaults(resources []unstructured.Unstructured) Values {
	values := DefaultValues()

	for _, r := range resources {
		if r.GroupVersionKind() != gvk.Deployment {
			continue
		}

		// Extract image
		containers, found, _ := unstructured.NestedSlice(r.Object, "spec", "template", "spec", "containers")
		if found && len(containers) > 0 {
			c, ok := containers[0].(map[string]any)
			if ok {
				if img, exists := c["image"].(string); exists {
					// Split image:tag
					parts := splitImageTag(img)
					values.Image.Repository = parts[0]
					values.Image.Tag = parts[1]
				}

				if res, exists := c["resources"].(map[string]any); exists {
					values.Resources = extractResources(res)
				}
			}
		}

		// Extract replicas
		replicas, found, _ := unstructured.NestedInt64(r.Object, "spec", "replicas")
		if found {
			values.Replicas = int32(replicas)
		}

		break // Only process the first Deployment
	}

	return values
}

func splitImageTag(image string) [2]string {
	// Handle images with digest
	if idx := len(image) - 1; idx > 0 {
		for i := idx; i >= 0; i-- {
			if image[i] == ':' {
				return [2]string{image[:i], image[i+1:]}
			}
			if image[i] == '/' {
				break
			}
		}
	}

	return [2]string{image, defaultImageTag}
}

func extractResources(res map[string]any) ResourceSpec {
	spec := ResourceSpec{}

	if limits, ok := res[resourceKeyLimits].(map[string]any); ok {
		if cpu, ok := limits[resourceKeyCPU].(string); ok {
			spec.Limits.CPU = cpu
		} else if cpu, ok := limits[resourceKeyCPU]; ok {
			spec.Limits.CPU = resource.NewMilliQuantity(int64(cpu.(float64)*1000), resource.DecimalSI).String()
		}

		if mem, ok := limits[resourceKeyMemory].(string); ok {
			spec.Limits.Memory = mem
		}
	}

	if requests, ok := res[resourceKeyRequests].(map[string]any); ok {
		if cpu, ok := requests[resourceKeyCPU].(string); ok {
			spec.Requests.CPU = cpu
		} else if cpu, ok := requests[resourceKeyCPU]; ok {
			spec.Requests.CPU = resource.NewMilliQuantity(int64(cpu.(float64)*1000), resource.DecimalSI).String()
		}

		if mem, ok := requests[resourceKeyMemory].(string); ok {
			spec.Requests.Memory = mem
		}
	}

	return spec
}

// WriteValuesYAML writes the values to a YAML file.
func WriteValuesYAML(v Values, path string) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling values: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}

// WriteValuesSchema generates a JSON Schema from the Values struct and
// writes it to the given path.
func WriteValuesSchema(path string) error {
	reflector := &jsonschema.Reflector{}
	schema := reflector.Reflect(&Values{})

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}
