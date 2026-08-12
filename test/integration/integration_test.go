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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"

	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	aigatewaycontroller "github.com/opendatahub-io/ai-gateway-operator/internal/controller/aigateway"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
	dsciv2 "github.com/opendatahub-io/opendatahub-operator/v2/api/dscinitialization/v2"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhmanager "github.com/opendatahub-io/opendatahub-operator/v2/pkg/manager"
)

const (
	timeout  = 90 * time.Second
	interval = 2 * time.Second

	moduleCRDName            = "aigateways.components.platform.opendatahub.io"
	batchGatewayOperatorName = "llm-d-batch-gateway-operator"
)

var (
	ctx             context.Context
	cancel          context.CancelFunc
	k8sClient       client.Client
	k               *k8sm.Matcher
	operatorCfgData map[string]string
	testScheme      = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(testScheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(dsciv2.AddToScheme(testScheme))
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

	// Simulate deployment readiness so the test does not depend on the CI cluster
	// being able to pull the real batch-gateway image. The controller determines
	// AIGateway readiness from deployment.status.readyReplicas, so we patch it
	// once the deployment exists — the same signal kubelet would send on a real pull.
	patchCtx, patchCancel := context.WithTimeout(ctx, timeout)
	defer patchCancel()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-patchCtx.Done():
				return
			case <-ticker.C:
				deploy := rt.workloadDeploy.DeepCopy()
				if err := k8sClient.Get(patchCtx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
					continue
				}
				if deploy.Status.ReadyReplicas >= 1 {
					return
				}
				patch := client.MergeFrom(deploy.DeepCopy())
				deploy.Status.ReadyReplicas = 1
				deploy.Status.AvailableReplicas = 1
				deploy.Status.Replicas = 1
				_ = k8sClient.Status().Patch(patchCtx, deploy, patch)
			}
		}
	}()

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
		jq.Match(`.status.module.platform == "%s"`,
			operatorCfgData[moduleconfig.KeyPlatformType]),
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
		jq.Match(`.status.releases[0].name == "AI Gateway Operator"`),
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

// TestAIGateway_MaaS runs the full AGO controller reconciliation loop against
// a real API server and verifies the MaaS-specific sub-module condition path.
// Unit tests call reportSubModuleStatus() directly on a fake client; this test
// exercises the complete reconcile pipeline (action ordering, re-queue logic,
// condition-update ordering) that only a real API server can surface.
func TestAIGateway_MaaS(t *testing.T) {
	g := NewWithT(t)
	ns := support.IntegrationTestNamespace()

	// Register cleanup before setup so partially created resources are removed
	// even if installMaaSCRDStubs fails mid-way (e.g. a CRD Established wait times out).
	t.Cleanup(func() {
		if err := removeMaaSCRDStubs(ctx, k8sClient); err != nil {
			t.Errorf("cleanup: failed to remove MaaS CRD stubs: %v", err)
		}
	})

	// Prometheus and optional CRD stubs so kustomize can resolve all resource
	// types in the maas-controller bundle without needing a full cluster stack.
	g.Expect(installMaaSCRDStubs(ctx, k8sClient)).To(Succeed())

	module := &componentsv1alpha1.AIGateway{
		ObjectMeta: metav1.ObjectMeta{Name: componentsv1alpha1.AIGatewayInstanceName},
		Spec: componentsv1alpha1.AIGatewaySpec{
			ModelsAsAService: componentsv1alpha1.ModelsAsAServiceComponent{
				ManagementState: "Managed",
			},
		},
	}
	maasControllerDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "maas-controller",
			Namespace: ns,
		},
	}

	_ = k8sClient.Delete(ctx, module)
	waitForSingletonDeleted(t, module)

	t.Cleanup(func() {
		err := k8sClient.Delete(ctx, module)
		if err != nil && !k8serr.IsNotFound(err) {
			t.Logf("cleanup: unexpected error deleting AIGateway: %v", err)
			return
		}
		if err == nil {
			waitForSingletonDeleted(t, module)
		}
	})

	t.Run("should set Ready=False when maas-controller is unavailable", func(t *testing.T) {
		testMaaSReadyFalseOnOperandFailure(t, module, maasControllerDeploy)
	})
}

// testMaaSReadyFalseOnOperandFailure verifies that:
//  1. When maas-controller has readyReplicas >= 1, ModelsAsAServiceReady=True
//  2. When maas-controller is scaled to 0, ModelsAsAServiceReady=False
//  3. After restoring replicas, ModelsAsAServiceReady=True again
func testMaaSReadyFalseOnOperandFailure(t *testing.T, module *componentsv1alpha1.AIGateway, maasControllerDeploy *appsv1.Deployment) {
	t.Helper()
	g := NewWithT(t)

	module.ResourceVersion = ""
	g.Expect(k8sClient.Create(ctx, module)).To(Succeed())

	// Continuously simulate maas-controller readiness so the test does not
	// depend on the CI cluster being able to pull the real maas-controller image.
	// The controller reads deployment.status.readyReplicas — we patch it as
	// kubelet would once the pod is running.
	//
	// Use a raw MergePatch with hardcoded JSON so all three status fields
	// (replicas, readyReplicas, availableReplicas) are applied atomically.
	// client.MergeFrom() may omit replicas=1 when the base has replicas=0
	// (omitempty), which causes OpenShift admission to reject readyReplicas>replicas.
	readyStatusPatch := client.RawPatch(types.MergePatchType,
		[]byte(`{"status":{"replicas":1,"readyReplicas":1,"availableReplicas":1}}`))
	patchCtx, patchCancel := context.WithTimeout(ctx, timeout)
	defer patchCancel()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-patchCtx.Done():
				return
			case <-ticker.C:
				deploy := maasControllerDeploy.DeepCopy()
				if err := k8sClient.Get(patchCtx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
					continue
				}
				if deploy.Status.ReadyReplicas >= 1 {
					return
				}
				if k8sClient.Status().Patch(patchCtx, deploy, readyStatusPatch) == nil {
					return // patch succeeded — controller will see readyReplicas=1 on next reconcile
				}
			}
		}
	}()

	// Both aggregate Ready and the MaaS sub-module condition must be True.
	// Use `// []` to coerce null to an empty array when conditions haven't been
	// set yet — jq treats `null | .[]` as an error which stops Eventually retrying.
	g.Eventually(k.Get(module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`(.status.conditions // []) | any(.[]; .type == "Ready" and .status == "True")`),
		jq.Match(`(.status.conditions // []) | any(.[]; .type == "ModelsAsAServiceReady" and .status == "True")`),
	))

	// Stop the goroutine before scaling to 0 so it cannot race against the
	// test body and restore readyReplicas=1 after we zero it out.
	patchCancel()

	// Scale maas-controller to 0 to simulate operand failure.
	// Scaling avoids a race where the controller re-creates the Deployment
	// and the pod starts before the test can observe the NotReady transition.
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(maasControllerDeploy), maasControllerDeploy)).To(Succeed())
	zero := int32(0)
	g.Expect(k8sClient.Patch(ctx, maasControllerDeploy, func() client.Patch {
		p := client.MergeFrom(maasControllerDeploy.DeepCopy())
		maasControllerDeploy.Spec.Replicas = &zero
		return p
	}())).To(Succeed())

	// Zero out status immediately so the controller sees 0 ready replicas.
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(maasControllerDeploy), maasControllerDeploy)).To(Succeed())
	g.Expect(k8sClient.Status().Patch(ctx, maasControllerDeploy, func() client.Patch {
		p := client.MergeFrom(maasControllerDeploy.DeepCopy())
		maasControllerDeploy.Status.ReadyReplicas = 0
		maasControllerDeploy.Status.AvailableReplicas = 0
		return p
	}())).To(Succeed())

	// Both aggregate Ready and the MaaS sub-module condition must be False.
	// DeploymentsAvailable=False (Error severity) when a managed sub-module is
	// unavailable drives Ready=False via the reconcile condition pipeline.
	g.Eventually(k.Get(module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`(.status.conditions // []) | any(.[]; .type == "ModelsAsAServiceReady" and .status == "False")`),
		jq.Match(`(.status.conditions // []) | any(.[]; .type == "Ready" and .status == "False")`),
	))

	// Restore to 1 replica and simulate recovery.
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(maasControllerDeploy), maasControllerDeploy)).To(Succeed())
	one := int32(1)
	g.Expect(k8sClient.Patch(ctx, maasControllerDeploy, func() client.Patch {
		p := client.MergeFrom(maasControllerDeploy.DeepCopy())
		maasControllerDeploy.Spec.Replicas = &one
		return p
	}())).To(Succeed())

	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(maasControllerDeploy), maasControllerDeploy)).To(Succeed())
	g.Expect(k8sClient.Status().Patch(ctx, maasControllerDeploy,
		client.RawPatch(types.MergePatchType,
			[]byte(`{"status":{"replicas":1,"readyReplicas":1,"availableReplicas":1}}`)))).To(Succeed())

	g.Eventually(k.Get(module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`(.status.conditions // []) | any(.[]; .type == "Ready" and .status == "True")`),
		jq.Match(`(.status.conditions // []) | any(.[]; .type == "ModelsAsAServiceReady" and .status == "True")`),
	))
}

// Labels used to identify integration-test-owned CRD stubs so teardown never
// touches pre-existing cluster CRDs (e.g. Prometheus on OpenShift).
const (
	maasIntegrationCRDLabel = "integration.ai-gateway-operator.io/managed-by"
	maasIntegrationCRDValue = "maas-integration-setup"
)

func maasStubCRD(name, group, ver, kind, plural, singular string, scope apiextensionsv1.ResourceScope) apiextensionsv1.CustomResourceDefinition {
	t := true
	return apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{maasIntegrationCRDLabel: maasIntegrationCRDValue},
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    ver,
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type:                   "object",
						XPreserveUnknownFields: &t,
					},
				},
			}},
			Scope: scope,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   plural,
				Singular: singular,
				Kind:     kind,
			},
		},
	}
}

// installMaaSCRDStubs creates the CRD stubs and webhook cert secret needed by
// the maas-controller kustomize bundle. The webhook cert secret allows the
// maas-controller pod to start normally on clusters where pods can run (kind),
// so readyReplicas reaches 1 naturally without fighting the deployment controller.
// Each CRD is polled until Established=True so the REST mapper is ready.
func installMaaSCRDStubs(ctx context.Context, cli client.Client) error {
	ns := support.IntegrationTestNamespace()
	if err := ensureMaaSWebhookCertSecret(ctx, cli, ns); err != nil {
		return fmt.Errorf("creating maas webhook cert secret: %w", err)
	}
	crds := []apiextensionsv1.CustomResourceDefinition{
		// Prometheus Operator — maas-controller bundle applies PodMonitor/ServiceMonitor.
		maasStubCRD("podmonitors.monitoring.coreos.com", "monitoring.coreos.com", "v1",
			"PodMonitor", "podmonitors", "podmonitor", apiextensionsv1.NamespaceScoped),
		maasStubCRD("servicemonitors.monitoring.coreos.com", "monitoring.coreos.com", "v1",
			"ServiceMonitor", "servicemonitors", "servicemonitor", apiextensionsv1.NamespaceScoped),
		maasStubCRD("prometheusrules.monitoring.coreos.com", "monitoring.coreos.com", "v1",
			"PrometheusRule", "prometheusrules", "prometheusrule", apiextensionsv1.NamespaceScoped),
		// Optional watches — maas-controller informers timeout without these CRDs.
		maasStubCRD("authpolicies.kuadrant.io", "kuadrant.io", "v1",
			"AuthPolicy", "authpolicies", "authpolicy", apiextensionsv1.NamespaceScoped),
		maasStubCRD("tokenratelimitpolicies.kuadrant.io", "kuadrant.io", "v1beta3",
			"TokenRateLimitPolicy", "tokenratelimitpolicies", "tokenratelimitpolicy", apiextensionsv1.NamespaceScoped),
		maasStubCRD("llminferenceservices.serving.kserve.io", "serving.kserve.io", "v1alpha1",
			"LLMInferenceService", "llminferenceservices", "llminferenceservice", apiextensionsv1.NamespaceScoped),
	}
	for i := range crds {
		if err := cli.Create(ctx, &crds[i]); err != nil && !k8serr.IsAlreadyExists(err) {
			return fmt.Errorf("creating CRD %s: %w", crds[i].Name, err)
		}
		if err := waitForCRDEstablished(ctx, cli, crds[i].Name); err != nil {
			return fmt.Errorf("waiting for CRD %s to become Established: %w", crds[i].Name, err)
		}
	}
	return nil
}

// ensureMaaSWebhookCertSecret creates a self-signed TLS secret for the
// maas-controller webhook server so the pod can mount the volume and start
// normally. Without this secret the pod stays Pending (volume not found) on
// clusters where pod creation is allowed (kind), keeping readyReplicas=0.
func ensureMaaSWebhookCertSecret(ctx context.Context, cli client.Client, ns string) error {
	const secretName = "maas-controller-webhook-cert"
	existing := &corev1.Secret{}
	err := cli.Get(ctx, client.ObjectKey{Name: secretName, Namespace: ns}, existing)
	if err == nil {
		return nil // already exists
	}
	if !k8serr.IsNotFound(err) {
		return err
	}
	certPEM, keyPEM, err := generateIntegrationTestCert(ns)
	if err != nil {
		return fmt.Errorf("generating TLS cert: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ns,
			Labels:    map[string]string{maasIntegrationCRDLabel: maasIntegrationCRDValue},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM},
	}
	return cli.Create(ctx, secret)
}

// generateIntegrationTestCert returns a PEM-encoded self-signed TLS cert and key
// for the maas-controller webhook service in the given namespace.
func generateIntegrationTestCert(ns string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serialMax := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialMax)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	svc := fmt.Sprintf("maas-controller-webhook-service.%s.svc", ns)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: svc},
		DNSNames:              []string{svc, svc + ".cluster.local"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// waitForCRDEstablished polls until the named CRD reaches the Established condition.
// NotFound is treated as "cache not synced yet" and retried rather than returned as an
// error — the manager's informer cache may lag briefly after a Create call.
func waitForCRDEstablished(ctx context.Context, cli client.Client, crdName string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := cli.Get(ctx, client.ObjectKey{Name: crdName}, crd); err != nil {
			if k8serr.IsNotFound(err) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return fmt.Errorf("getting CRD %s: %w", crdName, err)
		}
		for _, cond := range crd.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("CRD %s did not become Established within %v", crdName, timeout)
}

// removeMaaSCRDStubs deletes the CRDs and webhook cert secret created by
// installMaaSCRDStubs. Pre-existing cluster CRDs are left untouched.
// NotFound is treated as successful cleanup; other errors are collected and returned.
func removeMaaSCRDStubs(ctx context.Context, cli client.Client) error {
	ns := support.IntegrationTestNamespace()
	var errs []string

	webhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "maas-controller-webhook-cert", Namespace: ns},
	}
	if err := cli.Get(ctx, client.ObjectKeyFromObject(webhookSecret), webhookSecret); err != nil {
		if !k8serr.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("get webhook cert secret: %v", err))
		}
	} else if webhookSecret.Labels[maasIntegrationCRDLabel] == maasIntegrationCRDValue {
		if err := cli.Delete(ctx, webhookSecret); err != nil && !k8serr.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("delete webhook cert secret: %v", err))
		}
	}

	names := []string{
		"podmonitors.monitoring.coreos.com",
		"servicemonitors.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com",
		"authpolicies.kuadrant.io",
		"tokenratelimitpolicies.kuadrant.io",
		"llminferenceservices.serving.kserve.io",
	}
	for _, name := range names {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := cli.Get(ctx, client.ObjectKey{Name: name}, crd); err != nil {
			if !k8serr.IsNotFound(err) {
				errs = append(errs, fmt.Sprintf("get %s: %v", name, err))
			}
			continue
		}
		if crd.Labels[maasIntegrationCRDLabel] != maasIntegrationCRDValue {
			continue // pre-existing CRD — leave untouched
		}
		if err := cli.Delete(ctx, crd); err != nil && !k8serr.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("delete %s: %v", name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("removing MaaS CRD stubs: %s", strings.Join(errs, "; "))
	}
	return nil
}
