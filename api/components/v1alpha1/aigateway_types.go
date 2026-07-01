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

package v1alpha1

import (
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AIGatewayComponentName = "aigateway"
	AIGatewayInstanceName  = "default-aigateway"
	AIGatewayKind          = "AIGateway"
)

var _ common.PlatformObject = (*AIGateway)(nil)

// AIGatewaySpec defines the desired state of AIGateway.
type AIGatewaySpec struct {
	// BatchGateway controls the batch-gateway operator sub-component.
	BatchGateway BatchGatewayComponent `json:"batchGateway,omitempty"`
	// ModelsAsService controls the Models as a Service sub-component.
	ModelsAsService ModelsAsServiceComponent `json:"modelsAsService,omitempty"`
}

// BatchGatewayComponent configures the batch-gateway operator lifecycle.
type BatchGatewayComponent struct {
	// ManagementState controls whether the batch-gateway operator is deployed.
	// +kubebuilder:validation:Enum=Managed;Removed
	// +kubebuilder:default=Removed
	ManagementState string `json:"managementState,omitempty"`
}

// ModelsAsServiceComponent configures the maas-controller lifecycle.
type ModelsAsServiceComponent struct {
	// ManagementState controls whether the maas-controller is deployed.
	// +kubebuilder:validation:Enum=Managed;Removed
	// +kubebuilder:default=Removed
	ManagementState string `json:"managementState,omitempty"`
}

// AIGatewayStatus defines the observed state of AIGateway.
type AIGatewayStatus struct {
	common.Status                 `json:",inline"`
	common.ComponentReleaseStatus `json:",inline"`

	// Module reports the module operator's runtime information.
	Module ModuleStatus `json:"module,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'default-aigateway'",message="AIGateway name must be default-aigateway"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Ready"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`,description="Reason"
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.module.version`,description="Module Version"

// AIGateway is the Schema for the aigateways API.
type AIGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIGatewaySpec   `json:"spec,omitempty"`
	Status AIGatewayStatus `json:"status,omitempty"`
}

func (c *AIGateway) GetStatus() *common.Status {
	return &c.Status.Status
}

func (c *AIGateway) GetConditions() []common.Condition {
	return c.Status.GetConditions()
}

func (c *AIGateway) SetConditions(conditions []common.Condition) {
	c.Status.SetConditions(conditions)
}

func (c *AIGateway) GetReleaseStatus() *[]common.ComponentRelease {
	return &c.Status.Releases
}

func (c *AIGateway) SetReleaseStatus(releases []common.ComponentRelease) {
	c.Status.Releases = releases
}

// +kubebuilder:object:root=true

// AIGatewayList contains a list of AIGateway.
type AIGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIGateway{}, &AIGatewayList{})
}
