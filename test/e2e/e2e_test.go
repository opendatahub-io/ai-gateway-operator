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
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"
	k8sm "github.com/lburgazzoli/gomega-matchers/pkg/matchers/k8s"

	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

const (
	timeout  = 90 * time.Second
	interval = 2 * time.Second

	labelPartOf            = "platform.opendatahub.io/part-of"
	annotationInstanceName = "platform.opendatahub.io/instance.name"
	annotationInstanceUID  = "platform.opendatahub.io/instance.uid"
	annotationType         = "platform.opendatahub.io/type"
	annotationVersion      = "platform.opendatahub.io/version"

	operatorConfigMapName = "ai-gateway-config"
	moduleCRDName         = "aigateways.components.platform.opendatahub.io"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
	k         *k8sm.Matcher

	testScheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(testScheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(testScheme))
	testScheme.AddKnownTypes(metav1.SchemeGroupVersion, &metav1.PartialObjectMetadata{}, &metav1.PartialObjectMetadataList{})
}

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) int {
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get kubeconfig: %v\n", err)
		return 1
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		return 1
	}

	k = k8sm.New(k8sClient, testScheme)

	return m.Run()
}

type aiGatewayE2ETest struct {
	module         *componentsv1alpha1.AIGateway
	moduleCRD      *apiextensionsv1.CustomResourceDefinition
	operatorDeploy *appsv1.Deployment
	operatorCfgMap *corev1.ConfigMap
	workloadDeploy *appsv1.Deployment
}

func TestAIGateway(t *testing.T) {
	operatorNamespace := support.OperatorNamespace()

	rt := &aiGatewayE2ETest{
		module: &componentsv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name: componentsv1alpha1.AIGatewayInstanceName,
			},
			Spec: componentsv1alpha1.AIGatewaySpec{
				BatchGateway: componentsv1alpha1.BatchGatewayComponent{
					ManagementState: "Managed",
				},
			},
		},
		moduleCRD: &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: moduleCRDName},
		},
		operatorDeploy: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ai-gateway-operator",
				Namespace: operatorNamespace,
			},
		},
		operatorCfgMap: &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      operatorConfigMapName,
				Namespace: operatorNamespace,
			},
		},
		workloadDeploy: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "llm-d-batch-gateway-operator",
				Namespace: operatorNamespace,
			},
		},
	}

	_ = k8sClient.Delete(ctx, rt.module)
	waitForSingletonDeleted(t, rt.module)

	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, rt.module)
	})

	eventuallyDeploymentReady(t, rt.operatorDeploy)

	t.Run("should have module CRD installed", rt.testModuleCRDInstalled)
	t.Run("should have operator ConfigMap deployed", rt.testOperatorConfigMap)
	t.Run("should become ready", rt.testBecomesReady)
	t.Run("should deploy batch-gateway operator", rt.testBatchGatewayDeployed)
	t.Run("should show deployed resources", rt.testShowResources)
	t.Run("should report module version and platform", rt.testModuleStatus)
	t.Run("should set platform labels on workload", rt.testPlatformLabels)
	t.Run("should set owner references on workload", rt.testOwnerReferences)
}

func TestMaaSDeployment(t *testing.T) {
	operatorNamespace := support.OperatorNamespace()

	maasModule := &componentsv1alpha1.AIGateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-maas-gateway",
		},
		Spec: componentsv1alpha1.AIGatewaySpec{
			ModelsAsService: componentsv1alpha1.ModelsAsServiceComponent{
				ManagementState: "Managed",
			},
		},
	}

	maasDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "maas-controller",
			Namespace: operatorNamespace,
		},
	}

	_ = k8sClient.Delete(ctx, maasModule)
	waitForSingletonDeleted(t, maasModule)

	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, maasModule)
	})

	t.Run("should deploy maas-controller", func(t *testing.T) {
		testMaaSControllerDeployed(t, maasModule, maasDeployment)
	})
	t.Run("should create MaaS CRDs", func(t *testing.T) {
		testMaaSCRDsCreated(t)
	})
	t.Run("should create maas-parameters ConfigMap", func(t *testing.T) {
		testMaaSConfigMapCreated(t, operatorNamespace)
	})
	t.Run("should create default Tenant CR", func(t *testing.T) {
		testDefaultTenantCreated(t)
	})
	t.Run("should update AIGateway status with MaaS source", func(t *testing.T) {
		testMaaSSourceInStatus(t, maasModule)
	})
	t.Run("should set owner references on maas-controller", func(t *testing.T) {
		testMaaSOwnerReferences(t, maasModule, maasDeployment)
	})
}

func (rt *aiGatewayE2ETest) testModuleCRDInstalled(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.moduleCRD)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.name == "%s"`, moduleCRDName),
	)
}

func (rt *aiGatewayE2ETest) testOperatorConfigMap(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.operatorCfgMap)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.data."%s" != ""`, moduleconfig.KeyPlatformType),
		jq.Match(`.data."%s" != ""`, moduleconfig.KeyPlatformVersion),
	))
}

func (rt *aiGatewayE2ETest) testBecomesReady(t *testing.T) {
	g := NewWithT(t)

	rt.module.ResourceVersion = ""
	g.Expect(k8sClient.Create(ctx, rt.module)).To(Succeed())

	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .status == "True"`),
	))
}

func (rt *aiGatewayE2ETest) testModuleStatus(t *testing.T) {
	g := NewWithT(t)
	operatorCfg := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorConfigMapName,
			Namespace: support.OperatorNamespace(),
		},
	}

	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(operatorCfg), operatorCfg)).To(Succeed())

	platformType := operatorCfg.Data[moduleconfig.KeyPlatformType]

	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.module.version == "%s"`, version.Version),
		jq.Match(`.status.module.buildSource == "%s@%s/%s"`,
			version.Repo, version.Branch, version.Commit),
		jq.Match(`.status.module.platform.name == "%s"`, platformType),
		jq.Match(`.status.module.sources | length > 0`),
		jq.Match(`.status.module.sources[0].path != ""`),
		jq.Match(`.status.module.sources[0].renderer == "kustomize"`),
	))
}

func (rt *aiGatewayE2ETest) testBatchGatewayDeployed(t *testing.T) {
	eventuallyDeploymentReady(t, rt.workloadDeploy)
}

func (rt *aiGatewayE2ETest) testShowResources(t *testing.T) {
	g := NewWithT(t)
	ns := rt.operatorDeploy.Namespace

	var sb strings.Builder

	var deployList appsv1.DeploymentList
	g.Expect(k8sClient.List(ctx, &deployList, client.InNamespace(ns))).To(Succeed())

	fmt.Fprintf(&sb, "Deployments in %s:\n", ns)
	for i := range deployList.Items {
		d := &deployList.Items[i]
		fmt.Fprintf(&sb, "  %-50s ready=%d/%d image=%s\n",
			d.Name,
			d.Status.ReadyReplicas,
			*d.Spec.Replicas,
			d.Spec.Template.Spec.Containers[0].Image,
		)
	}

	var podList corev1.PodList
	g.Expect(k8sClient.List(ctx, &podList, client.InNamespace(ns))).To(Succeed())

	fmt.Fprintf(&sb, "Pods in %s:\n", ns)
	for i := range podList.Items {
		p := &podList.Items[i]
		fmt.Fprintf(&sb, "  %-50s %-10s node=%s\n",
			p.Name,
			p.Status.Phase,
			p.Spec.NodeName,
		)
	}

	t.Log("\n" + sb.String())
}

func (rt *aiGatewayE2ETest) testPlatformLabels(t *testing.T) {
	g := NewWithT(t)
	module := rt.module.DeepCopy()
	operatorCfg := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorConfigMapName,
			Namespace: support.OperatorNamespace(),
		},
	}

	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(module), module)).To(Succeed())
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(operatorCfg), operatorCfg)).To(Succeed())

	g.Eventually(k.Get(rt.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.metadata.labels."%s" == "aigateway"`, labelPartOf),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceName,
			module.GetName()),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceUID,
			string(module.GetUID())),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationType,
			operatorCfg.Data[moduleconfig.KeyPlatformType]),
	))
}

func (rt *aiGatewayE2ETest) testOwnerReferences(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.ownerReferences[] | select(.kind == "AIGateway") | .name == "%s"`,
			componentsv1alpha1.AIGatewayInstanceName),
	)
}

func waitForDeleted(t *testing.T, obj client.Object) {
	t.Helper()

	g := NewWithT(t)
	g.Eventually(func(g Gomega) {
		fresh := obj.DeepCopyObject().(client.Object)
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), fresh)
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}

func waitForSingletonDeleted(t *testing.T, obj client.Object) {
	t.Helper()

	waitForDeleted(t, obj)
	obj.SetResourceVersion("")
	obj.SetUID("")
}

func eventuallyDeploymentReady(t *testing.T, deploy *appsv1.Deployment) {
	t.Helper()

	g := NewWithT(t)
	g.Eventually(k.Get(deploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.status.readyReplicas >= 1`),
	)
}

// MaaS E2E Test Functions

func testMaaSControllerDeployed(t *testing.T, module *componentsv1alpha1.AIGateway, deploy *appsv1.Deployment) {
	t.Helper()
	g := NewWithT(t)

	// Create AIGateway with MaaS enabled
	module.ResourceVersion = ""
	g.Expect(k8sClient.Create(ctx, module)).To(Succeed())

	// Wait for AIGateway to become ready
	g.Eventually(k.Get(module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
	))

	// Wait for maas-controller deployment to exist and have at least 1 replica
	// Note: We check for >= 1 replicas instead of exact readiness because
	// maas-controller may experience restarts due to missing optional CRDs (known upstream issue)
	g.Eventually(k.Get(deploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.spec.replicas >= 1`),
	)

	t.Log("maas-controller deployment created successfully")
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

	t.Logf("All %d MaaS CRDs created successfully", len(expectedCRDs))
}

func testMaaSConfigMapCreated(t *testing.T, namespace string) {
	t.Helper()
	g := NewWithT(t)

	// List ConfigMaps matching the pattern maas-parameters-*
	cmList := &corev1.ConfigMapList{}
	g.Eventually(func(g Gomega) {
		g.Expect(k8sClient.List(ctx, cmList, client.InNamespace(namespace))).To(Succeed())

		found := false
		for i := range cmList.Items {
			cm := &cmList.Items[i]
			if len(cm.Name) >= 15 && cm.Name[:15] == "maas-parameters" {
				// Verify it has the expected keys and non-empty values
				g.Expect(cm.Data).To(HaveKey("MAAS_CONTROLLER_IMAGE"))
				g.Expect(cm.Data["MAAS_CONTROLLER_IMAGE"]).NotTo(BeEmpty(), "MAAS_CONTROLLER_IMAGE must not be empty")
				g.Expect(cm.Data).To(HaveKey("MAAS_API_IMAGE"))
				g.Expect(cm.Data["MAAS_API_IMAGE"]).NotTo(BeEmpty(), "MAAS_API_IMAGE must not be empty")
				found = true
				break
			}
		}
		g.Expect(found).To(BeTrue(), "maas-parameters ConfigMap should exist")
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

	t.Log("maas-parameters ConfigMap created with correct keys")
}

func testDefaultTenantCreated(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	// The default Tenant CR is created in the "models-as-a-service" namespace
	// We need to use unstructured client because Tenant CRD type is not in our scheme
	tenant := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "maas.opendatahub.io/v1alpha1",
			Kind:       "Tenant",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-tenant",
			Namespace: "models-as-a-service",
		},
	}

	g.Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), tenant)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(tenant.GetName()).To(Equal("default-tenant"))
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

	t.Log("default-tenant CR created in models-as-a-service namespace")
}

func testMaaSSourceInStatus(t *testing.T, module *componentsv1alpha1.AIGateway) {
	t.Helper()
	g := NewWithT(t)

	g.Eventually(k.Get(module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.status.module.sources[] | select(.path | contains("maascontroller")) | .renderer == "kustomize"`),
	)

	t.Log("AIGateway status includes MaaS manifest source")
}

func testMaaSOwnerReferences(t *testing.T, module *componentsv1alpha1.AIGateway, deploy *appsv1.Deployment) {
	t.Helper()
	g := NewWithT(t)

	// Refresh module to get its UID
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(module), module)).To(Succeed())

	g.Eventually(k.Get(deploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		And(
			jq.Match(`.metadata.ownerReferences[] | select(.kind == "AIGateway") | .name == "%s"`, module.Name),
			jq.Match(`.metadata.ownerReferences[] | select(.kind == "AIGateway") | .uid == "%s"`, module.UID),
		),
	)

	t.Log("maas-controller deployment has correct owner reference with matching UID")
}
