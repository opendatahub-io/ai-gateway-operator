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

package v1alpha1_test

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
)

func TestPlatformName_UnmarshalJSON_String(t *testing.T) {
	g := NewWithT(t)

	var p componentApi.PlatformName
	g.Expect(json.Unmarshal([]byte(`"OpenDataHub"`), &p)).To(Succeed())
	g.Expect(p).To(Equal(componentApi.PlatformName("OpenDataHub")))
}

func TestPlatformName_UnmarshalJSON_LegacyObject(t *testing.T) {
	g := NewWithT(t)

	var p componentApi.PlatformName
	g.Expect(json.Unmarshal([]byte(`{"name":"OpenShift AI Self-Managed","version":"0.0.0"}`), &p)).To(Succeed())
	g.Expect(p).To(Equal(componentApi.PlatformName("OpenShift AI Self-Managed")))
}

func TestPlatformName_MarshalJSON_String(t *testing.T) {
	g := NewWithT(t)

	data, err := json.Marshal(componentApi.PlatformName("OpenDataHub"))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(data)).To(Equal(`"OpenDataHub"`))
}

// TestAIGateway_FromUnstructured_LegacyPlatformObject reproduces the cluster
// failure mode: status.module.platform stored as {"name","version"} must convert
// into the typed AIGateway without "unrecognized type: string".
func TestAIGateway_FromUnstructured_LegacyPlatformObject(t *testing.T) {
	g := NewWithT(t)

	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "components.platform.opendatahub.io/v1alpha1",
			"kind":       "AIGateway",
			"metadata": map[string]interface{}{
				"name": "default-aigateway",
			},
			"spec": map[string]interface{}{
				"modelsAsAService": map[string]interface{}{
					"managementState": "Managed",
				},
			},
			"status": map[string]interface{}{
				"module": map[string]interface{}{
					"version": "1.26.2",
					"platform": map[string]interface{}{
						"name":    "OpenShift AI Self-Managed",
						"version": "0.0.0",
					},
				},
			},
		},
	}

	obj := &componentApi.AIGateway{}
	g.Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)).To(Succeed())
	g.Expect(obj.Status.Module.Platform).To(Equal(componentApi.PlatformName("OpenShift AI Self-Managed")))
	g.Expect(obj.Status.Module.Version.String()).To(Equal("1.26.2"))
}

func TestAIGateway_FromUnstructured_PlatformString(t *testing.T) {
	g := NewWithT(t)

	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "components.platform.opendatahub.io/v1alpha1",
			"kind":       "AIGateway",
			"metadata": map[string]interface{}{
				"name": "default-aigateway",
			},
			"status": map[string]interface{}{
				"module": map[string]interface{}{
					"version":  "1.26.2",
					"platform": "OpenDataHub",
				},
			},
		},
	}

	obj := &componentApi.AIGateway{}
	g.Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)).To(Succeed())
	g.Expect(obj.Status.Module.Platform).To(Equal(componentApi.PlatformName("OpenDataHub")))
}

func TestAIGateway_RoundTrip_PlatformAsString(t *testing.T) {
	g := NewWithT(t)

	in := &componentApi.AIGateway{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "components.platform.opendatahub.io/v1alpha1",
			Kind:       "AIGateway",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "default-aigateway"},
		Status: componentApi.AIGatewayStatus{
			Module: componentApi.ModuleStatus{
				Version:  "1.0.0",
				Platform: "OpenDataHub",
			},
		},
	}

	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(in)
	g.Expect(err).NotTo(HaveOccurred())

	platform, found, err := unstructured.NestedFieldNoCopy(raw, "status", "module", "platform")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())
	// Canonical on-disk / API form is a string, not the legacy object.
	g.Expect(platform).To(Equal("OpenDataHub"))
}
