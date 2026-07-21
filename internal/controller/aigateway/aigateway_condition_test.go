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
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

// newSubModuleRR builds a ReconciliationRequest with a fake client pre-populated
// with the given objects, for testing reportSubModuleStatus.
func newSubModuleRR(t *testing.T, obj *componentApi.AIGateway, objs ...client.Object) *odhtypes.ReconciliationRequest {
	t.Helper()
	rr := newReadinessRR(obj)
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()
	return rr
}

func readyDeploy(name, ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
}

// TestReportSubModuleStatus_MaaSManaged_DeploymentReady verifies ModelsAsAServiceReady=True
// when modelsAsAService is Managed and the maas-controller Deployment is ready.
func TestReportSubModuleStatus_MaaSManaged_DeploymentsAvailable(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, "opendatahub")
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	rr := newSubModuleRR(t, obj, readyDeploy(maasControllerDeploymentName, "opendatahub"))

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	cond := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(Equal(status.SubModuleReadyReason))
}

// TestReportSubModuleStatus_MaaSManaged_DeploymentAbsent verifies ModelsAsAServiceReady=False
// when modelsAsAService is Managed but the maas-controller Deployment does not exist yet.
func TestReportSubModuleStatus_MaaSManaged_DeploymentsNotAvailable(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, "opendatahub")
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	// No Deployment object in the fake client → IsNotFound → (false, nil)
	rr := newSubModuleRR(t, obj)

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	cond := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal(status.SubModuleNotReadyReason))
}

// TestReportSubModuleStatus_MaaSManaged_DeploymentZeroReplicas verifies ModelsAsAServiceReady=False
// when the Deployment exists but has ReadyReplicas=0 (e.g. pod crash-looping).
func TestReportSubModuleStatus_MaaSManaged_DeploymentZeroReplicas(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, "opendatahub")
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	// Deployment exists but no ready replicas
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: maasControllerDeploymentName, Namespace: "opendatahub"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
	}
	rr := newSubModuleRR(t, obj, deploy)

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	cond := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal(status.SubModuleNotReadyReason))
}

// TestReportSubModuleStatus_MaaSRemoved verifies ModelsAsAServiceReady=False (Removed)
// when modelsAsAService is Removed, regardless of whether the Deployment exists.
func TestReportSubModuleStatus_MaaSRemoved(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, "opendatahub")
	obj := newTestAIGateway()
	rr := newSubModuleRR(t, obj, readyDeploy(maasControllerDeploymentName, "opendatahub"))

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	cond := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal(status.SubModuleRemovedReason))
}

// TestReportSubModuleStatus_BothManaged_IndependentConditions verifies that conditions
// are independent: if only maas-controller is ready and batch-gateway is not,
// ModelsAsAServiceReady=True and BatchGatewayReady=False simultaneously.
func TestReportSubModuleStatus_BothManaged(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, "opendatahub")
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	obj.Spec.BatchGateway.ManagementState = managedState
	// Only maas-controller is ready; batch-gateway Deployment is absent
	rr := newSubModuleRR(t, obj, readyDeploy(maasControllerDeploymentName, "opendatahub"))

	g.Expect(m.reportSubModuleStatus(context.Background(), rr)).To(Succeed())

	maas := rr.Conditions.GetCondition(status.ConditionModelsAsAServiceReady)
	g.Expect(maas).NotTo(BeNil())
	g.Expect(maas.Status).To(Equal(metav1.ConditionTrue))

	// batch-gateway Deployment is absent → False independently of MaaS
	batch := rr.Conditions.GetCondition(status.ConditionBatchGatewayReady)
	g.Expect(batch).NotTo(BeNil())
	g.Expect(batch.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(batch.Reason).To(Equal(status.SubModuleNotReadyReason))
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
