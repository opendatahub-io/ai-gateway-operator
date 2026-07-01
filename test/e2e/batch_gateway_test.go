//go:build e2e

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

package e2e

import (
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

func init() {
	registerModuleSpec(func(spec *componentsv1alpha1.AIGatewaySpec) {
		spec.BatchGateway = componentsv1alpha1.BatchGatewayComponent{
			ManagementState: "Managed",
		}
	})
}

func TestBatchGateway(t *testing.T) {
	workloadDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "llm-d-batch-gateway-operator",
			Namespace: operatorNamespace,
		},
	}

	t.Run("should deploy batch-gateway operator", func(t *testing.T) {
		eventuallyDeploymentReady(t, workloadDeploy)
	})
	t.Run("should set platform labels on workload", func(t *testing.T) {
		testWorkloadPlatformLabels(t, workloadDeploy)
	})
	t.Run("should set owner references on workload", func(t *testing.T) {
		testWorkloadOwnerReferences(t, workloadDeploy)
	})
}

func testWorkloadPlatformLabels(t *testing.T, workloadDeploy *appsv1.Deployment) {
	g := NewWithT(t)

	fresh := module.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(fresh), fresh)).To(Succeed())

	operatorCfg := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorConfigMapName,
			Namespace: support.OperatorNamespace(),
		},
	}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(operatorCfg), operatorCfg)).To(Succeed())

	g.Eventually(k.Get(workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.metadata.labels."%s" == "aigateway"`, labelPartOf),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceName,
			fresh.GetName()),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceUID,
			string(fresh.GetUID())),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationType,
			operatorCfg.Data[moduleconfig.KeyPlatformType]),
	))
}

func testWorkloadOwnerReferences(t *testing.T, workloadDeploy *appsv1.Deployment) {
	g := NewWithT(t)

	g.Eventually(k.Get(workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.ownerReferences[] | select(.kind == "AIGateway") | .name == "%s"`,
			componentsv1alpha1.AIGatewayInstanceName),
	)
}
