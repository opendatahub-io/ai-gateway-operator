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

package aigateway

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/controller/status"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/conditions"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

// readyCondition is the aggregate happy condition; DeploymentsAvailable is one
// of its dependents, so an Error-severity DeploymentsAvailable=False drives
// Ready=False while an Info-severity one does not.
const readyCondition = "Ready"

func newReadinessRR(obj *componentApi.AIGateway) *odhtypes.ReconciliationRequest {
	return &odhtypes.ReconciliationRequest{
		Instance:   obj,
		Conditions: conditions.NewManager(obj, readyCondition, status.ConditionDeploymentsAvailable),
	}
}

// TestOverWriteConditionWhenAnySubRemoved verifies that with no sub-module Managed, the
// 0/0 DeploymentsAvailable failure is downgraded to informational severity and
// no longer drags the aggregate Ready condition down.
func TestOverWriteConditionWhenAnySubRemoved(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	rr := newReadinessRR(obj)

	// Simulate deployments.NewAction finding zero deployments: False at Error severity.
	rr.Conditions.MarkFalse(
		status.ConditionDeploymentsAvailable,
		conditions.WithMessage("0/0 deployments ready"),
	)
	g.Expect(rr.Conditions.GetCondition(readyCondition).Status).To(Equal(metav1.ConditionFalse))

	g.Expect(m.overWriteCondition(context.Background(), rr)).To(Succeed())

	da := rr.Conditions.GetCondition(status.ConditionDeploymentsAvailable)
	g.Expect(da).NotTo(BeNil())
	g.Expect(da.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(da.Severity).To(Equal(common.ConditionSeverityInfo))
	g.Expect(da.Reason).To(Equal(status.NoSubModuleManagedReason))

	g.Expect(rr.Conditions.GetCondition(readyCondition).Status).To(Equal(metav1.ConditionTrue))
}

// TestReportSubModuleStatus_MaaSManaged verifies that when modelsAsAService is Managed
// and deployments are available, ModelsAsAServiceReady=True is set on the AIGateway CR.
func TestReportSubModuleStatus_MaaSManaged_DeploymentsAvailable(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	rr := newReadinessRR(obj)

	rr.Conditions.MarkTrue(status.ConditionDeploymentsAvailable)

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	cond := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(Equal(status.SubModuleReadyReason))
}

// TestReportSubModuleStatus_MaaSManaged_DeploymentsNotAvailable verifies that when
// modelsAsAService is Managed but deployments are not yet available, ModelsAsAServiceReady=False.
func TestReportSubModuleStatus_MaaSManaged_DeploymentsNotAvailable(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	rr := newReadinessRR(obj)

	rr.Conditions.MarkFalse(status.ConditionDeploymentsAvailable,
		conditions.WithMessage("0/1 deployments ready"))

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	cond := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal(status.SubModuleNotReadyReason))
}

// TestReportSubModuleStatus_MaaSRemoved verifies that when modelsAsAService is Removed,
// ModelsAsAServiceReady=False with Removed reason regardless of deployment state.
func TestReportSubModuleStatus_MaaSRemoved(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	// modelsAsAService defaults to Removed
	rr := newReadinessRR(obj)

	rr.Conditions.MarkTrue(status.ConditionDeploymentsAvailable)

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	cond := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal(status.SubModuleRemovedReason))
}

// TestReportSubModuleStatus_BothManaged verifies that both sub-module conditions
// are set independently when both are Managed.
func TestReportSubModuleStatus_BothManaged(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	obj.Spec.BatchGateway.ManagementState = managedState
	rr := newReadinessRR(obj)

	rr.Conditions.MarkTrue(status.ConditionDeploymentsAvailable)

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	maas := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(maas).NotTo(BeNil())
	g.Expect(maas.Status).To(Equal(metav1.ConditionTrue))

	batch := rr.Conditions.GetCondition(status.ConditionBatchGatewayReady)
	g.Expect(batch).NotTo(BeNil())
	g.Expect(batch.Status).To(Equal(metav1.ConditionTrue))
}

// TestOverWriteConditionWhenManaged verifies that when a sub-module is Managed,
// overWriteCondition keeps DeploymentsAvailable as-is, so a real failure stays
// Error and Ready stays False.
func TestOverWriteConditionWhenManaged(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.BatchGateway.ManagementState = managedState
	rr := newReadinessRR(obj)

	// batch-gateway deployment not yet ready: a real failure that must be preserved.
	rr.Conditions.MarkFalse(
		status.ConditionDeploymentsAvailable,
		conditions.WithMessage("0/1 deployments ready"),
	)

	g.Expect(m.overWriteCondition(context.Background(), rr)).To(Succeed())

	da := rr.Conditions.GetCondition(status.ConditionDeploymentsAvailable)
	g.Expect(da).NotTo(BeNil())
	g.Expect(da.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(da.Severity).To(Equal(common.ConditionSeverityError))

	g.Expect(rr.Conditions.GetCondition(readyCondition).Status).To(Equal(metav1.ConditionFalse))
}
