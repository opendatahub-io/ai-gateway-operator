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
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/event"

	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

func TestMultitenantIPPResourceNames(t *testing.T) {
	g := NewWithT(t)
	g.Expect(mtIPPResourceNameForGateway(mtIPPPayloadProcessingName, mtIPPDefaultGatewayName)).
		To(Equal(mtIPPPayloadProcessingName))
	g.Expect(mtIPPResourceNameForGateway(mtIPPPayloadProcessingName, "gateway-a")).
		To(Equal("payload-processing-gateway-a"))
	g.Expect(mtIPPMaaSAPIRouteNameForTenant(mtIPPDefaultTenantName)).To(Equal(mtIPPMaaSAPIRouteName))
}

func TestMultitenantIPPAITenantWatchPredicate(t *testing.T) {
	g := NewWithT(t)
	pending := mtIPPTestAITenant("tenant-a", "Pending", "gateway-a")
	pending.SetResourceVersion("1")
	active := pending.DeepCopy()
	active.SetResourceVersion("2")
	g.Expect(unstructured.SetNestedField(active.Object, "Active", "status", "phase")).To(Succeed())

	g.Expect(mtAITenantWatchPredicate.Create(event.CreateEvent{Object: &pending})).To(BeTrue())
	g.Expect(mtAITenantWatchPredicate.Update(event.UpdateEvent{ObjectOld: &pending, ObjectNew: active})).To(BeTrue())
	g.Expect(mtAITenantWatchPredicate.Delete(event.DeleteEvent{Object: active})).To(BeTrue())
}

func TestMultitenantIPPBindingsFromAITenants(t *testing.T) {
	g := NewWithT(t)
	deletingTenant := mtIPPTestAITenant("deleting", "Active", "gateway-c")
	now := metav1.Now()
	deletingTenant.SetDeletionTimestamp(&now)
	deletingGateway := mtIPPTestGateway("gateway-c")
	deletingGateway.SetDeletionTimestamp(&now)

	g.Expect(multitenantIPPBindingsFromAITenants([]unstructured.Unstructured{
		mtIPPTestAITenant("tenant-a", "Active", "gateway-a"),
		mtIPPTestAITenant("tenant-b", "Active", "gateway-a"),
		mtIPPTestAITenant("pending", "Pending", "gateway-pending"),
		mtIPPTestAITenant("stale", "Active", "gateway-missing"),
		deletingTenant,
		mtIPPTestAITenant("terminating-gateway", "Active", "gateway-c"),
	}, []unstructured.Unstructured{
		mtIPPTestGateway("gateway-a"),
		deletingGateway,
	})).To(Equal([]mtIPPGatewayBinding{{
		GatewayName:      "gateway-a",
		GatewayNamespace: "openshift-ingress",
		TenantNames:      []string{"tenant-a", "tenant-b"},
	}}))
}

func TestRenderMultitenantIPPResources(t *testing.T) {
	g := NewWithT(t)
	rr := &odhtypes.ReconciliationRequest{Resources: mtIPPTestTemplates()}
	bindings := []mtIPPGatewayBinding{
		{GatewayName: "gateway-a", GatewayNamespace: "openshift-ingress", TenantNames: []string{"tenant-a", "tenant-shared"}},
		{GatewayName: "gateway-b", GatewayNamespace: "openshift-ingress", TenantNames: []string{"tenant-b"}},
	}

	g.Expect(renderMultitenantIPPResources(rr, bindings, "redhat-ai-gateway-infra")).To(Succeed())
	g.Expect(mtIPPTestResourcesByKind(rr.Resources, "Deployment")).To(HaveLen(2))
	g.Expect(mtIPPTestResourcesByKind(rr.Resources, "Service")).To(HaveLen(4))
	g.Expect(mtIPPTestResourcesByKind(rr.Resources, "DestinationRule")).To(HaveLen(4))
	g.Expect(mtIPPTestResourcesByKind(rr.Resources, "EnvoyFilter")).To(HaveLen(2))
	g.Expect(mtIPPTestResourcesByKind(rr.Resources, "Deployment")[0].GetNamespace()).To(Equal("openshift-ingress"))

	for _, binding := range bindings {
		serviceName := mtIPPResourceNameForGateway(mtIPPPayloadProcessingName, binding.GatewayName)
		mtIPPTestFindResource(t, rr.Resources, "Service", binding.GatewayNamespace, serviceName)

		destinationRule := mtIPPTestFindResource(t, rr.Resources, "DestinationRule", binding.GatewayNamespace, serviceName)
		host, found, err := unstructured.NestedString(destinationRule.Object, "spec", "host")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(found).To(BeTrue())
		g.Expect(host).To(Equal(mtIPPServiceHost(serviceName, binding.GatewayNamespace)))

		envoyFilter := mtIPPTestFindResource(t, rr.Resources, "EnvoyFilter", binding.GatewayNamespace, serviceName)
		mtIPPTestExpectEnvoyFilter(t, envoyFilter, binding)
	}
}

func TestMaasAwareGCPredicateRemovesOnlyStaleMultitenantIPPResources(t *testing.T) {
	g := NewWithT(t)
	obj := newTestAIGateway()
	obj.UID = "test-uid"
	obj.Generation = 3
	rr := newTestRR(obj)
	rr.Resources = []unstructured.Unstructured{
		mtIPPTestResource("v1", "Service", "openshift-ingress", "payload-processing-gateway-b", nil),
	}

	candidate := notStaleCandidate(obj, rr)
	candidate.SetAPIVersion("v1")
	candidate.SetKind("Service")
	candidate.SetNamespace("openshift-ingress")
	candidate.SetName("payload-processing-gateway-b")
	candidate.SetLabels(map[string]string{mtIPPComponentLabelKey: mtIPPPayloadProcessingName})

	deletable, err := (&Module{}).maasAwareGCPredicate(rr, candidate)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deletable).To(BeFalse())

	candidate.SetName("payload-processing-gateway-a")
	deletable, err = (&Module{}).maasAwareGCPredicate(rr, candidate)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deletable).To(BeTrue())
}

func mtIPPTestGateway(name string) unstructured.Unstructured {
	return mtIPPTestResource("gateway.networking.k8s.io/v1", "Gateway", "openshift-ingress", name, nil)
}

func mtIPPTestAITenant(name, phase, gatewayName string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": mtAITenantGVK.GroupVersion().String(),
		"kind":       mtAITenantGVK.Kind,
		"metadata": map[string]any{
			"name": name,
		},
		"status": map[string]any{
			"phase": phase,
			"gatewayRef": map[string]any{
				"name":      gatewayName,
				"namespace": "openshift-ingress",
			},
		},
	}}
}

func mtIPPTestTemplates() []unstructured.Unstructured {
	return []unstructured.Unstructured{
		mtIPPTestResource("apps/v1", "Deployment", "openshift-ingress", mtIPPPayloadProcessingName, nil),
		mtIPPTestResource("apps/v1", "Deployment", "openshift-ingress", mtIPPPayloadPreProcessingName, nil),
		mtIPPTestResource("v1", "Service", "models-as-a-service", mtIPPPayloadProcessingName, map[string]any{"spec": map[string]any{}}),
		mtIPPTestResource("v1", "Service", "models-as-a-service", mtIPPPayloadPreProcessingName, map[string]any{"spec": map[string]any{}}),
		mtIPPTestResource("networking.istio.io/v1", "DestinationRule", "models-as-a-service", mtIPPPayloadProcessingName, map[string]any{
			"spec": map[string]any{"host": "placeholder"},
		}),
		mtIPPTestResource("networking.istio.io/v1", "DestinationRule", "models-as-a-service", mtIPPPayloadPreProcessingName, map[string]any{
			"spec": map[string]any{"host": "placeholder"},
		}),
		mtIPPTestEnvoyFilter(),
	}
}

func mtIPPTestEnvoyFilter() unstructured.Unstructured {
	filterPatch := func() map[string]any {
		return map[string]any{
			"applyTo": "HTTP_FILTER",
			"match": map[string]any{"listener": map[string]any{"filterChain": map[string]any{
				"filter": map[string]any{"subFilter": map[string]any{"name": "placeholder"}},
			}}},
			"patch": map[string]any{"value": map[string]any{"typed_config": map[string]any{
				"grpc_service": map[string]any{"envoy_grpc": map[string]any{"cluster_name": "placeholder"}},
			}}},
		}
	}
	routePatch := func() map[string]any {
		return map[string]any{
			"applyTo": "HTTP_ROUTE",
			"match": map[string]any{"routeConfiguration": map[string]any{"vhost": map[string]any{
				"route": map[string]any{"name": "placeholder"},
			}}},
		}
	}

	return mtIPPTestResource("networking.istio.io/v1alpha3", "EnvoyFilter", "models-as-a-service", mtIPPPayloadProcessingName, map[string]any{
		"spec": map[string]any{
			"targetRefs":    []any{map[string]any{"name": mtIPPDefaultGatewayName}},
			"configPatches": []any{filterPatch(), filterPatch(), routePatch(), routePatch()},
		},
	})
}

func mtIPPTestResource(apiVersion, kind, namespace, name string, fields map[string]any) unstructured.Unstructured {
	object := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
	}
	for key, value := range fields {
		object[key] = value
	}
	return unstructured.Unstructured{Object: object}
}

func mtIPPTestResourcesByKind(resources []unstructured.Unstructured, kind string) []unstructured.Unstructured {
	result := make([]unstructured.Unstructured, 0)
	for i := range resources {
		if resources[i].GetKind() == kind {
			result = append(result, resources[i])
		}
	}
	return result
}

func mtIPPTestFindResource(
	t *testing.T,
	resources []unstructured.Unstructured,
	kind, namespace, name string,
) unstructured.Unstructured {
	t.Helper()
	for i := range resources {
		if resources[i].GetKind() == kind && resources[i].GetNamespace() == namespace && resources[i].GetName() == name {
			return resources[i]
		}
	}
	t.Fatalf("resource %s %s/%s not found", kind, namespace, name)
	return unstructured.Unstructured{}
}

func mtIPPTestExpectEnvoyFilter(t *testing.T, resource unstructured.Unstructured, binding mtIPPGatewayBinding) {
	t.Helper()
	g := NewWithT(t)

	targetRefs, found, err := unstructured.NestedSlice(resource.Object, "spec", "targetRefs")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())
	g.Expect(targetRefs[0].(map[string]any)["name"]).To(Equal(binding.GatewayName))

	patches, found, err := unstructured.NestedSlice(resource.Object, "spec", "configPatches")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())

	preCluster, _, _ := unstructured.NestedString(
		patches[0].(map[string]any), "patch", "value", "typed_config", "grpc_service", "envoy_grpc", "cluster_name",
	)
	postCluster, _, _ := unstructured.NestedString(
		patches[1].(map[string]any), "patch", "value", "typed_config", "grpc_service", "envoy_grpc", "cluster_name",
	)
	g.Expect(preCluster).To(Equal(mtIPPGRPCClusterName(
		mtIPPResourceNameForGateway(mtIPPPayloadPreProcessingName, binding.GatewayName), binding.GatewayNamespace,
	)))
	g.Expect(postCluster).To(Equal(mtIPPGRPCClusterName(
		mtIPPResourceNameForGateway(mtIPPPayloadProcessingName, binding.GatewayName), binding.GatewayNamespace,
	)))

	routeNames := make([]string, 0, 2*len(binding.TenantNames))
	for i := 2; i < len(patches); i++ {
		routeName, _, _ := unstructured.NestedString(
			patches[i].(map[string]any), "match", "routeConfiguration", "vhost", "route", "name",
		)
		routeNames = append(routeNames, routeName)
	}
	expectedRouteNames := make([]string, 0, 2*len(binding.TenantNames))
	for _, tenantName := range binding.TenantNames {
		for routeIndex := 0; routeIndex < 2; routeIndex++ {
			expectedRouteNames = append(expectedRouteNames, fmt.Sprintf(
				"redhat-ai-gateway-infra.maas-api-route-%s.%d", tenantName, routeIndex,
			))
		}
	}
	g.Expect(routeNames).To(Equal(expectedRouteNames))
}
