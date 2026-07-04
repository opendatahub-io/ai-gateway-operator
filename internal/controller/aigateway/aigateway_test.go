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
	dsciv2 "github.com/opendatahub-io/opendatahub-operator/v2/api/dscinitialization/v2"
	serviceApi "github.com/opendatahub-io/opendatahub-operator/v2/api/services/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
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

func TestInitializeManagedMaaS(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsService.ManagementState = "Managed"
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

func TestOwnDerivedResourcesMaaSConfig(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestAIGateway()
	obj.Spec.ModelsAsService.ManagementState = "Managed"
	obj.UID = types.UID("test-aigateway-uid")
	rr := newTestRR(obj)

	scheme := runtime.NewScheme()
	utilruntime.Must(componentApi.AddToScheme(scheme))

	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(gvk.MaasConfig)
	cfg.SetName(maasClusterConfigName)

	rr.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()

	g.Expect(m.ownDerivedResources(context.Background(), rr)).To(Succeed())

	updated := &unstructured.Unstructured{}
	updated.SetGroupVersionKind(gvk.MaasConfig)
	g.Expect(rr.Client.Get(context.Background(), types.NamespacedName{Name: maasClusterConfigName}, updated)).To(Succeed())
	g.Expect(updated.GetOwnerReferences()).To(HaveLen(1))
	g.Expect(updated.GetOwnerReferences()[0].Kind).To(Equal(componentApi.AIGatewayKind))
	g.Expect(updated.GetOwnerReferences()[0].Name).To(Equal(componentApi.AIGatewayInstanceName))
	g.Expect(updated.GetOwnerReferences()[0].UID).To(Equal(types.UID("test-aigateway-uid")))
	g.Expect(updated.GetOwnerReferences()[0].Controller).NotTo(BeNil())
	g.Expect(*updated.GetOwnerReferences()[0].Controller).To(BeTrue())
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

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(m.reportStatus(context.Background(), rr)).To(Succeed())

	g.Expect(obj.Status.Module.Version.String()).To(Equal(version.Version))
	g.Expect(obj.Status.Module.Platform.Name).To(Equal("OpenDataHub"))
	g.Expect(obj.Status.Module.Platform.Version.String()).To(Equal("1.0.0"))
	g.Expect(obj.Status.Module.Sources).To(HaveLen(1))
	g.Expect(obj.Status.Module.Sources[0].Renderer).To(Equal(componentApi.SourceRendererKustomize))
}
