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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

func init() {
	registerModuleSpec(func(spec *componentsv1alpha1.AIGatewaySpec) {
		spec.ModelsAsService = componentsv1alpha1.DSCModelsAsServiceSpec{
			ManagementState: "Managed",
		}
	})
}

func TestModelsAsService(t *testing.T) {
	maasControllerDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "maas-controller",
			Namespace: operatorNamespace,
		},
	}

	t.Run("should deploy maas-controller", func(t *testing.T) {
		eventuallyDeploymentReady(t, maasControllerDeploy)
	})
	t.Run("should create MaaS CRDs", func(t *testing.T) {
		testMaaSCRDsCreated(t)
	})
	t.Run("should create maas-parameters ConfigMap", func(t *testing.T) {
		testMaaSConfigMapCreated(t)
	})
	t.Run("should create default Tenant CR", func(t *testing.T) {
		testDefaultTenantCreated(t)
	})
	t.Run("should set platform labels on maas-controller", func(t *testing.T) {
		testMaaSControllerPlatformLabels(t, maasControllerDeploy)
	})
	t.Run("should set owner references on maas-controller", func(t *testing.T) {
		testMaaSControllerOwnerReferences(t, maasControllerDeploy)
	})
}

func testMaaSCRDsCreated(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	expectedCRDs := []string{
		"aitenants.maas.opendatahub.io",
		"configs.maas.opendatahub.io",
		"externalmodels.maas.opendatahub.io",
		"maasauthpolicies.maas.opendatahub.io",
		"maasmodelrefs.maas.opendatahub.io",
		"maassubscriptions.maas.opendatahub.io",
		"tenants.maas.opendatahub.io",
		"externalmodels.inference.opendatahub.io",
		"externalproviders.inference.opendatahub.io",
	}

	for _, crdName := range expectedCRDs {
		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: crdName},
		}
		g.Eventually(k.Get(crd)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
			jq.Match(`.metadata.name == "%s"`, crdName),
		)
	}
}

func testMaaSConfigMapCreated(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "maas-parameters",
			Namespace: operatorNamespace,
		},
	}

	g.Eventually(k.Get(configMap)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.data.MAAS_CONTROLLER_IMAGE != ""`),
		jq.Match(`.data.MAAS_API_IMAGE != ""`),
		jq.Match(`.data.MAAS_API_KEY_CLEANUP_IMAGE != ""`),
	))
}

func testDefaultTenantCreated(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	// The default Tenant CR is created in the operator's namespace
	// We need to use unstructured client because Tenant CRD type is not in our scheme
	tenant := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "maas.opendatahub.io/v1alpha1",
			Kind:       "Tenant",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-tenant",
			Namespace: operatorNamespace,
		},
	}

	g.Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), tenant)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(tenant.GetName()).To(Equal("default-tenant"))
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}

func testMaaSControllerPlatformLabels(t *testing.T, maasControllerDeploy *appsv1.Deployment) {
	t.Helper()
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

	g.Eventually(k.Get(maasControllerDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
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

func testMaaSControllerOwnerReferences(t *testing.T, maasControllerDeploy *appsv1.Deployment) {
	t.Helper()
	g := NewWithT(t)

	g.Eventually(k.Get(maasControllerDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.ownerReferences[] | select(.kind == "AIGateway") | .name == "%s"`,
			componentsv1alpha1.AIGatewayInstanceName),
	)
}
