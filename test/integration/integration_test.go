//go:build integration

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

package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/spf13/viper"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"
	k8sm "github.com/lburgazzoli/gomega-matchers/pkg/matchers/k8s"

	aigatewaycontroller "github.com/opendatahub-io/ai-gateway-operator/internal/controller/aigateway"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhmanager "github.com/opendatahub-io/opendatahub-operator/v2/pkg/manager"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

const (
	timeout  = 90 * time.Second
	interval = 2 * time.Second

	moduleCRDName              = "aigateways.components.platform.opendatahub.io"
	batchGatewayOperatorName   = "llm-d-batch-gateway-operator"
)

var (
	ctx                    context.Context
	cancel                 context.CancelFunc
	k8sClient              client.Client
	k                      *k8sm.Matcher
	operatorCfgData        map[string]string
	operatorReleaseVersion string
	testScheme             = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(testScheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(testScheme))
}

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) int {
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get kubeconfig: %v\n", err)
		return 1
	}

	directClient, err := client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		return 1
	}

	testNamespace := support.IntegrationTestNamespace()

	if err := support.EnsureNamespace(ctx, directClient, testNamespace); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create namespace: %v\n", err)
		return 1
	}

	moduleCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: moduleCRDName},
	}
	if err := directClient.Get(ctx, client.ObjectKeyFromObject(moduleCRD), moduleCRD); err != nil {
		fmt.Fprintf(os.Stderr, "Expected CRD %s to be installed before running integration tests: %v\n", moduleCRDName, err)
		return 1
	}

	_ = directClient.DeleteAllOf(ctx, &componentsv1alpha1.AIGateway{})
	_ = directClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(testNamespace))
	_ = directClient.DeleteAllOf(ctx, &corev1.Service{}, client.InNamespace(testNamespace))

	viper.Set("rhai-applications-namespace", testNamespace)
	cluster.SetRHAIApplicationNamespace(testNamespace)

	operatorCfgData = support.MustReadConfigMapData(
		support.MustProjectFile("config", "manager", "configmap.yaml"))

	moduleCfg := &moduleconfig.Config{
		PlatformType:          operatorCfgData[moduleconfig.KeyPlatformType],
		PlatformVersion:       operatorCfgData[moduleconfig.KeyPlatformVersion],
		ApplicationsNamespace: testNamespace,
		ManifestsPath:         support.MustProjectFile("config", "manifests"),
	}
	operatorReleaseVersion = moduleCfg.Release().Version.String()

	ctrlMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         testScheme,
		Metrics:        metricsserver.Options{BindAddress: "0"},
		LeaderElection: false,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				testNamespace:       {},
				cache.AllNamespaces: {},
			},
		},
		Client: client.Options{
			Cache: &client.CacheOptions{
				Unstructured: true,
				DisableFor: []client.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
			},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create manager: %v\n", err)
		return 1
	}

	mgr := odhmanager.New(ctrlMgr, odhmanager.WithManifestsBasePath(
		support.MustProjectFile("config", "manifests")))

	if err := aigatewaycontroller.NewReconciler(ctx, mgr, moduleCfg, moduleCfg.Release()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create reconciler: %v\n", err)
		return 1
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Manager exited with error: %v\n", err)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		fmt.Fprintf(os.Stderr, "Failed to sync manager cache\n")
		return 1
	}

	k8sClient = mgr.GetClient()
	k = k8sm.New(k8sClient, testScheme)

	_ = directClient.Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: ctrl.ObjectMeta{Name: "integration-test-role"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"*"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		}},
	})
	_ = directClient.Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: ctrl.ObjectMeta{Name: "integration-test-binding"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "integration-test-role",
		},
		Subjects: []rbacv1.Subject{{
			Kind:     "Group",
			Name:     "system:masters",
			APIGroup: "rbac.authorization.k8s.io",
		}},
	})

	return m.Run()
}

type aiGatewayTest struct {
	module         *componentsv1alpha1.AIGateway
	moduleCRD      *apiextensionsv1.CustomResourceDefinition
	workloadDeploy *appsv1.Deployment
}

func TestAIGateway(t *testing.T) {
	rt := &aiGatewayTest{
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
		workloadDeploy: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      batchGatewayOperatorName,
				Namespace: support.IntegrationTestNamespace(),
			},
		},
	}

	_ = k8sClient.Delete(ctx, rt.module)
	waitForSingletonDeleted(t, rt.module)

	t.Cleanup(func() {
		// testCRDeletionCleanup may have already removed the CR.
		err := k8sClient.Delete(ctx, rt.module)
		if err != nil && !k8serr.IsNotFound(err) {
			t.Logf("cleanup: unexpected error deleting AIGateway: %v", err)
		}
	})

	t.Run("should have module CRD installed", rt.testModuleCRDInstalled)
	t.Run("should reject non-singleton CR name", rt.testSingletonCELRejection)
	t.Run("should become ready", rt.testBecomesReady)
	t.Run("should set observedGeneration after reconciliation", rt.testObservedGeneration)
	t.Run("should populate status.releases", rt.testReleasesPopulated)
	t.Run("should deploy batch-gateway operator", rt.testBatchGatewayDeployed)
	t.Run("should show deployed resources", rt.testShowResources)
	t.Run("should report module version and platform", rt.testModuleStatus)
	t.Run("should set owner references on workload", rt.testOwnerReferences)
	t.Run("should set Ready=False when operand unavailable", rt.testReadyFalseOnOperandFailure)
	t.Run("should garbage-collect owned resources on CR deletion", rt.testCRDeletionCleanup)
}

func (rt *aiGatewayTest) testModuleCRDInstalled(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.moduleCRD)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.name == "%s"`, moduleCRDName),
	)
}

func (rt *aiGatewayTest) testBecomesReady(t *testing.T) {
	g := NewWithT(t)

	rt.module.ResourceVersion = ""
	g.Expect(k8sClient.Create(ctx, rt.module)).To(Succeed())

	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .status == "True"`),
	))
}

func (rt *aiGatewayTest) testModuleStatus(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.module.version == "%s"`, version.Version),
		jq.Match(`.status.module.buildSource == "%s@%s/%s"`,
			version.Repo, version.Branch, version.Commit),
		jq.Match(`.status.module.platform.name == "%s"`,
			operatorCfgData[moduleconfig.KeyPlatformType]),
		jq.Match(`.status.module.platform.version == "%s"`,
			operatorReleaseVersion),
		jq.Match(`.status.module.sources | length > 0`),
		jq.Match(`.status.module.sources[0].path != ""`),
		jq.Match(`.status.module.sources[0].renderer == "kustomize"`),
	))
}

func (rt *aiGatewayTest) testBatchGatewayDeployed(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.status.readyReplicas >= 1`),
	)
}

func (rt *aiGatewayTest) testShowResources(t *testing.T) {
	g := NewWithT(t)
	ns := rt.workloadDeploy.Namespace

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
			string(p.Status.Phase),
			p.Spec.NodeName,
		)
	}

	t.Log("\n" + sb.String())
}

func (rt *aiGatewayTest) testOwnerReferences(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.ownerReferences[] | select(.kind == "AIGateway") | .name == "%s"`,
			componentsv1alpha1.AIGatewayInstanceName),
	)
}

func (rt *aiGatewayTest) testSingletonCELRejection(t *testing.T) {
	g := NewWithT(t)

	badModule := &componentsv1alpha1.AIGateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: "not-the-default",
		},
		Spec: componentsv1alpha1.AIGatewaySpec{
			BatchGateway: componentsv1alpha1.BatchGatewayComponent{
				ManagementState: "Managed",
			},
		},
	}

	err := k8sClient.Create(ctx, badModule)
	g.Expect(err).To(HaveOccurred())
	g.Expect(k8serr.IsInvalid(err)).To(BeTrue(), "expected Invalid error, got: %v", err)
	g.Expect(err.Error()).To(ContainSubstring("AIGateway name must be default-aigateway"))
}

func (rt *aiGatewayTest) testObservedGeneration(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.observedGeneration > 0`),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
	))
}

func (rt *aiGatewayTest) testReleasesPopulated(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.releases | length > 0`),
		jq.Match(`.status.releases[0].name == "LLM-D AI Gateway Operator"`),
		jq.Match(`.status.releases[0].version != ""`),
	))
}

func (rt *aiGatewayTest) testReadyFalseOnOperandFailure(t *testing.T) {
	g := NewWithT(t)

	// Precondition: CR is Ready and Deployment is available.
	g.Eventually(k.Get(rt.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.status.readyReplicas >= 1`),
	)

	// Scale the deployment to 0 replicas to simulate operand failure.
	// Scaling (instead of deleting) avoids a race where the controller
	// re-creates the Deployment and pods start before the test can
	// observe the NotReady transition.
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rt.workloadDeploy), rt.workloadDeploy)).To(Succeed())
	zero := int32(0)
	patch := client.MergeFrom(rt.workloadDeploy.DeepCopy())
	rt.workloadDeploy.Spec.Replicas = &zero
	g.Expect(k8sClient.Patch(ctx, rt.workloadDeploy, patch)).To(Succeed())

	// The controller sees readyReplicas == 0 and sets Ready=False.
	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "False"`),
	))

	// Scale back to 1 — the kustomize merge patch may not restore replicas
	// if the manifest omits the field, so we do it explicitly.
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rt.workloadDeploy), rt.workloadDeploy)).To(Succeed())
	restorePatch := client.MergeFrom(rt.workloadDeploy.DeepCopy())
	one := int32(1)
	rt.workloadDeploy.Spec.Replicas = &one
	g.Expect(k8sClient.Patch(ctx, rt.workloadDeploy, restorePatch)).To(Succeed())

	// Wait for recovery so subsequent tests start from a clean state.
	g.Eventually(k.Get(rt.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
	))
}

func (rt *aiGatewayTest) testCRDeletionCleanup(t *testing.T) {
	g := NewWithT(t)
	ns := support.IntegrationTestNamespace()

	// Verify representative owned resources exist before deletion.
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: batchGatewayOperatorName, Namespace: ns}}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)).To(Succeed())

	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: batchGatewayOperatorName, Namespace: ns}}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), sa)).To(Succeed())

	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: batchGatewayOperatorName}}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(crb), crb)).To(Succeed())

	// Delete the AIGateway CR.
	g.Expect(k8sClient.Delete(ctx, rt.module)).To(Succeed())
	waitForSingletonDeleted(t, rt.module)

	// Verify owned resources are garbage-collected.
	waitForDeleted(t, deploy)
	waitForDeleted(t, sa)
	waitForDeleted(t, crb)
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
