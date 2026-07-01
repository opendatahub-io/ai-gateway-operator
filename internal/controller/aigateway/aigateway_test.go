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
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
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
	g.Expect(m.maasManifestInfo.SourcePath).To(Equal("default"))
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
