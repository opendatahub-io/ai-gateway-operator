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
	"fmt"
	"strconv"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	dsciv2 "github.com/opendatahub-io/opendatahub-operator/v2/api/dscinitialization/v2"
	serviceApi "github.com/opendatahub-io/opendatahub-operator/v2/api/services/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/controller/status"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/conditions"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	odhAnnotations "github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/annotations"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()

	cfg := &moduleconfig.Config{
		PlatformType:          "OpenDataHub",
		PlatformVersion:       "1.0.0",
		ManifestsPath:         "/manifests",
		ApplicationsNamespace: "test-ns",
	}

	m, err := NewModule(cfg)
	NewWithT(t).Expect(err).NotTo(HaveOccurred())

	return m
}

func newTestModuleWithNamespace(t *testing.T, applicationsNamespace string) *Module {
	t.Helper()

	cfg := &moduleconfig.Config{
		PlatformType:          "OpenDataHub",
		PlatformVersion:       "1.0.0",
		ManifestsPath:         "/manifests",
		ApplicationsNamespace: applicationsNamespace,
	}

	m, err := NewModule(cfg)
	NewWithT(t).Expect(err).NotTo(HaveOccurred())

	return m
}

func newTestRR(obj *componentApi.AIGateway) *odhtypes.ReconciliationRequest {
	return &odhtypes.ReconciliationRequest{
		Instance:          obj,
		ManifestsBasePath: "/manifests",
		Release: (&moduleconfig.Config{
			PlatformType:    "OpenDataHub",
			PlatformVersion: "1.0.0",
		}).Release(),
	}
}

func newTestAIGateway() *componentApi.AIGateway {
	return &componentApi.AIGateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: componentApi.AIGatewayInstanceName,
		},
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(extv1.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))

	return scheme
}

func TestNewModule(t *testing.T) {
	g := NewWithT(t)

	cfg := &moduleconfig.Config{
		PlatformType:    "OpenDataHub",
		PlatformVersion: "1.0.0",
		ManifestsPath:   "/manifests",
	}

	m, err := NewModule(cfg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(m.version.String()).To(Equal(version.Version))
	g.Expect(m.cfg).To(Equal(cfg))
	g.Expect(m.batchGatewayManifestInfo.ContextDir).To(Equal("batchgateway"))
	g.Expect(m.batchGatewayManifestInfo.SourcePath).To(Equal("base"))
	g.Expect(m.maasManifestInfo.ContextDir).To(Equal("maascontroller"))
	g.Expect(m.maasManifestInfo.SourcePath).To(Equal("base"))
}

func TestNewModuleXKS(t *testing.T) {
	g := NewWithT(t)

	cfg := &moduleconfig.Config{
		PlatformType:    string(cluster.XKS),
		PlatformVersion: "1.0.0",
		ManifestsPath:   "/manifests",
	}

	m, err := NewModule(cfg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(m.maasManifestInfo.ContextDir).To(Equal("maascontroller"))
	g.Expect(m.maasManifestInfo.SourcePath).To(Equal("overlays/xks"))
	g.Expect(m.batchGatewayManifestInfo.SourcePath).To(Equal("base"))
}

func TestNewModuleInvalidVersion(t *testing.T) {
	g := NewWithT(t)

	orig := version.Version
	version.Version = "not-a-version"

	t.Cleanup(func() { version.Version = orig })

	_, err := NewModule(&moduleconfig.Config{})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("invalid semver"))
}

func TestInitializeManaged(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.BatchGateway.ManagementState = "Managed"
	rr := newTestRR(obj)

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(HaveLen(1))
	g.Expect(rr.Manifests[0].Path).To(Equal("/manifests"))
	g.Expect(rr.Manifests[0].ContextDir).To(Equal("batchgateway"))
}

func TestInitializeRemoved(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.BatchGateway.ManagementState = "Removed"
	rr := newTestRR(obj)

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(BeEmpty())
}

func TestInitializeRemovedKeepsMaaSWhileCleanupPending(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)

	// maas-controller has not reported TeardownCompletedAnnotation yet, so the
	// bundle (including its own Deployment) must stay rendered.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
		},
	}

	rr.Client = fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(dep).
		Build()

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(HaveLen(1))
	g.Expect(rr.Manifests[0].ContextDir).To(Equal("maascontroller"))
}

func TestInitializeRemovedExcludesMaaSOnceTeardownCompleted(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
			Annotations: map[string]string{
				maasTeardownCompletedKey: "true",
			},
		},
	}

	rr.Client = fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(dep).
		Build()

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(BeEmpty())
}

func TestInitializeManagedMaaS(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = "Managed"
	rr := newTestRR(obj)

	scheme := runtime.NewScheme()
	utilruntime.Must(dsciv2.AddToScheme(scheme))
	rr.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(&dsciv2.DSCInitialization{
		ObjectMeta: metav1.ObjectMeta{Name: "default-dsci"},
		Spec: dsciv2.DSCInitializationSpec{
			Monitoring: serviceApi.DSCIMonitoring{
				MonitoringCommonSpec: serviceApi.MonitoringCommonSpec{
					Namespace: "test-monitoring",
				},
			},
		},
	}).Build()

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(HaveLen(1))
	g.Expect(rr.Manifests[0].Path).To(Equal("/manifests"))
	g.Expect(rr.Manifests[0].ContextDir).To(Equal("maascontroller"))
}

func TestMaaSRemovalPendingWaitsWhileControllerHasNotCompletedTeardown(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(dep).
		Build()

	pending, err := m.maasRemovalPending(context.Background(), cli)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pending).To(BeTrue())
}

func TestMaaSRemovalPendingWaitsForControllerDeploymentRemovalAfterCompletion(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
			Annotations: map[string]string{
				maasTeardownCompletedKey: "true",
			},
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(dep).
		Build()

	// maas-controller reported completion, but its Deployment is still present
	// (excluding it from this pass's render hasn't been garbage-collected yet);
	// removal must not be considered done yet.
	pending, err := m.maasRemovalPending(context.Background(), cli)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pending).To(BeTrue())
}

func TestMaaSRemovalPendingFalseAfterControllerGoneLeavesCRDsInPlace(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	crd := &extv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "aitenants.maas.opendatahub.io",
			Labels: map[string]string{
				maasCRDComponentLabelKey: maasCRDComponentLabelValue,
			},
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithRuntimeObjects(crd).
		Build()

	// No maas-controller Deployment at all: teardown is complete and the
	// controller is gone, so removal is no longer pending even though the
	// vendored CRD (never deleted by design) is still present.
	pending, err := m.maasRemovalPending(context.Background(), cli)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pending).To(BeFalse())

	g.Expect(cli.Get(context.Background(), types.NamespacedName{Name: "aitenants.maas.opendatahub.io"}, &extv1.CustomResourceDefinition{})).To(Succeed())
}

func TestEnsureInfraSecretMigrationRBACCreatesNamespaceAndRBACWhenManaged(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, odhApplicationsNS)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState
	rr := newTestRR(obj)
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	g.Expect(m.ensureInfraSecretMigrationRBAC(context.Background(), rr)).To(Succeed())

	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: odhInfrastructureNS}, &corev1.Namespace{})).To(Succeed())
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{
		Name:      secretMigrateRoleName,
		Namespace: odhInfrastructureNS,
	}, &rbacv1.Role{})).To(Succeed())
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{
		Name:      secretMigrateRoleName,
		Namespace: odhInfrastructureNS,
	}, &rbacv1.RoleBinding{})).To(Succeed())
}

func TestEnsureInfraSecretMigrationRBACLabelsPreExistingNamespace(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, odhApplicationsNS)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = managedState

	preExistingNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   odhInfrastructureNS,
			Labels: map[string]string{"existing-label": "keep-me"},
		},
	}
	rr := newTestRR(obj)
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(preExistingNS).Build()

	g.Expect(m.ensureInfraSecretMigrationRBAC(context.Background(), rr)).To(Succeed())

	var ns corev1.Namespace
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: odhInfrastructureNS}, &ns)).To(Succeed())
	g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/part-of", "ai-gateway"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "ai-gateway-operator"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("opendatahub.io/generated-namespace", "true"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("existing-label", "keep-me"))

	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{
		Name:      secretMigrateRoleName,
		Namespace: odhInfrastructureNS,
	}, &rbacv1.Role{})).To(Succeed())
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{
		Name:      secretMigrateRoleName,
		Namespace: odhInfrastructureNS,
	}, &rbacv1.RoleBinding{})).To(Succeed())
}

func TestEnsureInfraSecretMigrationRBACNoopWhenTeardownNotCompleted(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, odhApplicationsNS)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
		},
	}
	infraNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: odhInfrastructureNS},
	}
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep, infraNs).Build()

	g.Expect(m.ensureInfraSecretMigrationRBAC(context.Background(), rr)).To(Succeed())

	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: odhInfrastructureNS}, &corev1.Namespace{})).To(Succeed())
}

func TestEnsureInfraSecretMigrationRBACDeletesRBACButKeepsNamespaceOnceTeardownCompleted(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, odhApplicationsNS)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
			Annotations: map[string]string{
				maasTeardownCompletedKey: "true",
			},
		},
	}
	infraNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: odhInfrastructureNS},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretMigrateRoleName,
			Namespace: odhInfrastructureNS,
		},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretMigrateRoleName,
			Namespace: odhInfrastructureNS,
		},
	}
	dbSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasDBConfigSecret,
			Namespace: odhInfrastructureNS,
		},
	}
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(dep, infraNs, role, roleBinding, dbSecret).Build()

	g.Expect(m.ensureInfraSecretMigrationRBAC(context.Background(), rr)).To(Succeed())

	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: odhInfrastructureNS}, &corev1.Namespace{})).
		To(Succeed(), "namespace should be preserved")
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: secretMigrateRoleName, Namespace: odhInfrastructureNS}, &rbacv1.Role{})).
		ToNot(Succeed(), "Role should be deleted")
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: secretMigrateRoleName, Namespace: odhInfrastructureNS}, &rbacv1.RoleBinding{})).
		ToNot(Succeed(), "RoleBinding should be deleted")
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: maasDBConfigSecret, Namespace: odhInfrastructureNS}, &corev1.Secret{})).
		To(Succeed(), "maas-db-config secret should be preserved")
}

func TestEnsureInfraSecretMigrationRBACNoopWhenSeparationDisabled(t *testing.T) {
	g := NewWithT(t)

	// newTestModule uses ApplicationsNamespace "test-ns", which deriveInfrastructureNamespace
	// maps back to itself (no known separation mapping) - i.e. separation is effectively off.
	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
			Annotations: map[string]string{
				maasTeardownCompletedKey: "true",
			},
		},
	}
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()

	g.Expect(m.ensureInfraSecretMigrationRBAC(context.Background(), rr)).To(Succeed())
}

func TestEnsureInfraSecretMigrationRBACIdempotentWhenAlreadyAbsent(t *testing.T) {
	g := NewWithT(t)

	m := newTestModuleWithNamespace(t, odhApplicationsNS)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
			Annotations: map[string]string{
				maasTeardownCompletedKey: "true",
			},
		},
	}
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()

	g.Expect(m.ensureInfraSecretMigrationRBAC(context.Background(), rr)).To(Succeed())
}

// notStaleCandidate builds an unstructured object stamped so that
// gc.DefaultObjectPredicate considers it current (not eligible for deletion on its
// own), matching obj's generation/UID and rr's release info.
func notStaleCandidate(obj *componentApi.AIGateway, rr *odhtypes.ReconciliationRequest) unstructured.Unstructured {
	candidate := unstructured.Unstructured{}
	candidate.SetAnnotations(map[string]string{
		odhAnnotations.PlatformVersion:    rr.Release.Version.String(),
		odhAnnotations.PlatformType:       string(rr.Release.Name),
		odhAnnotations.InstanceGeneration: strconv.FormatInt(obj.GetGeneration(), 10),
		odhAnnotations.InstanceUID:        string(obj.GetUID()),
	})
	return candidate
}

func TestMaasAwareGCPredicateDelegatesForNonMaaSResources(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.UID = "test-uid"
	obj.Generation = 3
	rr := newTestRR(obj)
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	candidate := notStaleCandidate(obj, rr)

	deletable, err := m.maasAwareGCPredicate(rr, candidate)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deletable).To(BeFalse())
}

func TestMaasAwareGCPredicateWaitsForTeardownCompletion(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.UID = "test-uid"
	obj.Generation = 3
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
		},
	}
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()

	candidate := notStaleCandidate(obj, rr)
	candidate.SetLabels(map[string]string{maasCRDComponentLabelKey: maasCRDComponentLabelValue})

	deletable, err := m.maasAwareGCPredicate(rr, candidate)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deletable).To(BeFalse())
}

func TestMaasAwareGCPredicateAllowsMaaSResourcesOnceTeardownCompleted(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.UID = "test-uid"
	obj.Generation = 3
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
			Annotations: map[string]string{
				maasTeardownCompletedKey: "true",
			},
		},
	}
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()

	// Same generation/UID as the AIGateway CR - the default predicate alone would
	// treat this as still wanted, since nothing about the AIGateway spec changed.
	candidate := notStaleCandidate(obj, rr)
	candidate.SetLabels(map[string]string{maasCRDComponentLabelKey: maasCRDComponentLabelValue})

	deletable, err := m.maasAwareGCPredicate(rr, candidate)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deletable).To(BeTrue())
}

func TestMaasAwareGCPredicateBoundsDeploymentGet(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.UID = "test-uid"
	obj.Generation = 3
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
		},
	}

	var gotDeadline bool
	var hadDeadline bool
	rr.Client = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				_, hadDeadline = ctx.Deadline()
				gotDeadline = true
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	candidate := notStaleCandidate(obj, rr)
	candidate.SetLabels(map[string]string{maasCRDComponentLabelKey: maasCRDComponentLabelValue})

	_, err := m.maasAwareGCPredicate(rr, candidate)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(gotDeadline).To(BeTrue(), "expected the Deployment GET to be intercepted")
	g.Expect(hadDeadline).To(BeTrue(), "expected a bounded context, not context.Background()")
}

func TestOverWriteConditionReportsMaaSRemovalInProgress(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: m.cfg.ApplicationsNamespace,
		},
	}
	rr.Client = fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(dep).
		Build()
	rr.Conditions = conditions.NewManager(obj, status.ConditionDeploymentsAvailable)

	g.Expect(m.overWriteCondition(context.Background(), rr)).To(Succeed())

	condition := rr.Conditions.GetCondition(status.ConditionDeploymentsAvailable)
	g.Expect(condition).NotTo(BeNil())
	g.Expect(condition.Reason).To(Equal(status.MaaSRemovalInProgressReason))
}

func TestAnnotateMaaSRequestedTeardownAnnotatesRenderedControllerDeployment(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsAService.ManagementState = removedState
	rr := newTestRR(obj)
	rr.Resources = []unstructured.Unstructured{
		{
			Object: map[string]any{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]any{
					"name":      maasControllerDeploymentName,
					"namespace": "test-ns",
				},
			},
		},
	}

	g.Expect(m.annotateMaaSRequestedTeardown(context.Background(), rr)).To(Succeed())

	g.Expect(rr.Resources).To(HaveLen(1))
	g.Expect(rr.Resources[0].GetAnnotations()).To(HaveKeyWithValue(maasTeardownRequestedKey, "true"))
}

func TestInitializeDefault(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	rr := newTestRR(obj)

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(BeEmpty())
}

func TestUpgradeIfNeededFreshInstall(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	rr := newTestRR(obj)

	g.Expect(m.upgradeIfNeeded(context.Background(), rr)).To(Succeed())
}

func TestUpgradeIfNeededSameVersion(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()

	v, err := componentApi.NewSemVer(version.Version)
	g.Expect(err).NotTo(HaveOccurred())

	obj.Status.Module.Version = v
	rr := newTestRR(obj)

	g.Expect(m.upgradeIfNeeded(context.Background(), rr)).To(Succeed())
}

func TestReportStatus(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.BatchGateway.ManagementState = "Managed"
	rr := newTestRR(obj)

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	// No ConfigMap in the fake client — simulates older platform without handshake.
	rr.Client = fake.NewClientBuilder().WithScheme(scheme).Build()

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(m.reportStatus(context.Background(), rr)).To(Succeed())

	g.Expect(obj.Status.Module.Version.String()).To(Equal(version.Version))
	g.Expect(obj.Status.Module.Platform.Name).To(Equal("OpenDataHub"))
	g.Expect(obj.Status.Module.Platform.Version.String()).To(Equal("1.0.0"))
	g.Expect(obj.Status.Module.Sources).To(HaveLen(1))
	g.Expect(obj.Status.Module.Sources[0].Renderer).To(Equal(componentApi.SourceRendererKustomize))
	// No platform ConfigMap → no platform release entry.
	g.Expect(obj.Status.Releases).To(BeEmpty())
}

func TestReportStatus_WithPlatformConfigMap(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	// Simulate releases.NewAction having already populated module metadata.
	obj.Status.Releases = []common.ComponentRelease{{
		Name:    "LLM-D AI Gateway Operator",
		Version: "v0.1.0",
		RepoURL: "https://github.com/opendatahub-io/ai-gateway-operator",
	}}
	rr := newTestRR(obj)

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))

	platformCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      platformConfigName,
			Namespace: "test-ns",
		},
		Data: map[string]string{
			platformVersionKey: "2.20.0",
		},
	}
	rr.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(platformCM).Build()

	g.Expect(m.reportStatus(context.Background(), rr)).To(Succeed())

	g.Expect(obj.Status.Releases).To(HaveLen(2))

	releasesByName := make(map[string]common.ComponentRelease, len(obj.Status.Releases))
	for _, r := range obj.Status.Releases {
		releasesByName[r.Name] = r
	}

	moduleRelease, ok := releasesByName["LLM-D AI Gateway Operator"]
	g.Expect(ok).To(BeTrue())
	g.Expect(moduleRelease.Version).To(Equal("v0.1.0"))

	platformRelease, ok := releasesByName[platformReleaseName]
	g.Expect(ok).To(BeTrue())
	g.Expect(platformRelease.Version).To(Equal("2.20.0"))
}

func TestReportStatus_NoPlatformConfigMap(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Status.Releases = []common.ComponentRelease{{
		Name:    "LLM-D AI Gateway Operator",
		Version: "v0.1.0",
	}}
	rr := newTestRR(obj)

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	rr.Client = fake.NewClientBuilder().WithScheme(scheme).Build()

	g.Expect(m.reportStatus(context.Background(), rr)).To(Succeed())

	// Module release preserved; platform entry omitted when ConfigMap is absent.
	g.Expect(obj.Status.Releases).To(HaveLen(1))
	g.Expect(obj.Status.Releases[0].Name).To(Equal("LLM-D AI Gateway Operator"))
}

func TestSetPlatformRelease_UpdatesExisting(t *testing.T) {
	g := NewWithT(t)

	obj := newTestAIGateway()
	obj.Status.Releases = []common.ComponentRelease{
		{Name: "LLM-D AI Gateway Operator", Version: "v0.1.0"},
		{Name: platformReleaseName, Version: "2.19.0"},
	}

	setPlatformRelease(obj, "2.20.0")

	g.Expect(obj.Status.Releases).To(HaveLen(2))
	g.Expect(obj.Status.Releases[1].Name).To(Equal(platformReleaseName))
	g.Expect(obj.Status.Releases[1].Version).To(Equal("2.20.0"))
}

func TestWithPreservedPlatformRelease(t *testing.T) {
	g := NewWithT(t)

	obj := newTestAIGateway()
	obj.Status.Releases = []common.ComponentRelease{
		{Name: "LLM-D AI Gateway Operator", Version: "v0.1.0"},
		{Name: platformReleaseName, Version: "2.19.0"},
	}
	rr := newTestRR(obj)

	inner := func(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
		inst := rr.Instance.(*componentApi.AIGateway)
		// Simulate releases.NewAction replacing the full list with metadata only.
		inst.SetReleaseStatus([]common.ComponentRelease{{
			Name:    "LLM-D AI Gateway Operator",
			Version: "v0.1.0",
		}})
		return nil
	}

	g.Expect(withPreservedPlatformRelease(inner)(context.Background(), rr)).To(Succeed())

	g.Expect(obj.Status.Releases).To(HaveLen(2))
	g.Expect(getPlatformRelease(obj).Version).To(Equal("2.19.0"))
}

func TestWithPreservedPlatformRelease_OnError(t *testing.T) {
	g := NewWithT(t)

	obj := newTestAIGateway()
	obj.Status.Releases = []common.ComponentRelease{
		{Name: "LLM-D AI Gateway Operator", Version: "v0.1.0"},
		{Name: platformReleaseName, Version: "2.19.0"},
	}
	rr := newTestRR(obj)

	inner := func(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
		inst := rr.Instance.(*componentApi.AIGateway)
		// Simulate releases.NewAction partially replacing the list before failing.
		inst.SetReleaseStatus([]common.ComponentRelease{{
			Name:    "LLM-D AI Gateway Operator",
			Version: "v0.1.0",
		}})
		return fmt.Errorf("simulated failure")
	}

	err := withPreservedPlatformRelease(inner)(context.Background(), rr)
	g.Expect(err).To(HaveOccurred())

	// Platform entry must be preserved even when inner fails.
	g.Expect(getPlatformRelease(obj).Version).To(Equal("2.19.0"))
}
