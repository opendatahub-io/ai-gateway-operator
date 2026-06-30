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
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

const (
	moduleCRDName = "aigateways.components.platform.opendatahub.io"
)

func TestAIGateway(t *testing.T) {
	t.Run("should have module CRD installed", testModuleCRDInstalled)
	t.Run("should have operator ConfigMap deployed", testOperatorConfigMap)
	t.Run("should be ready", testModuleReady)
	t.Run("should report module version and platform", testModuleStatus)
	t.Run("should show deployed resources", testShowResources)
}

func testModuleCRDInstalled(t *testing.T) {
	g := NewWithT(t)

	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: moduleCRDName},
	}

	g.Eventually(k.Get(crd)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.name == "%s"`, moduleCRDName),
	)
}

func testOperatorConfigMap(t *testing.T) {
	g := NewWithT(t)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorConfigMapName,
			Namespace: operatorNamespace,
		},
	}

	g.Eventually(k.Get(cm)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.data."%s" != ""`, moduleconfig.KeyPlatformType),
		jq.Match(`.data."%s" != ""`, moduleconfig.KeyPlatformVersion),
	))
}

func testModuleReady(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .status == "True"`),
	))
}

func testModuleStatus(t *testing.T) {
	g := NewWithT(t)

	operatorCfg := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorConfigMapName,
			Namespace: support.OperatorNamespace(),
		},
	}

	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(operatorCfg), operatorCfg)).To(Succeed())

	platformType := operatorCfg.Data[moduleconfig.KeyPlatformType]

	g.Eventually(k.Get(module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.module.version == "%s"`, version.Version),
		jq.Match(`.status.module.buildSource == "%s@%s/%s"`,
			version.Repo, version.Branch, version.Commit),
		jq.Match(`.status.module.platform.name == "%s"`, platformType),
		jq.Match(`.status.module.sources | length > 0`),
		jq.Match(`.status.module.sources[0].path != ""`),
		jq.Match(`.status.module.sources[0].renderer == "kustomize"`),
	))
}

func testShowResources(t *testing.T) {
	g := NewWithT(t)

	var sb strings.Builder

	var deployList appsv1.DeploymentList
	g.Expect(k8sClient.List(ctx, &deployList, client.InNamespace(operatorNamespace))).To(Succeed())

	fmt.Fprintf(&sb, "Deployments in %s:\n", operatorNamespace)
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
	g.Expect(k8sClient.List(ctx, &podList, client.InNamespace(operatorNamespace))).To(Succeed())

	fmt.Fprintf(&sb, "Pods in %s:\n", operatorNamespace)
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
