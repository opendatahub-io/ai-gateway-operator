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
	"errors"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

const (
	mtIPPDefaultGatewayName = "maas-default-gateway"
	mtIPPDefaultTenantName  = "models-as-a-service"

	mtIPPPayloadProcessingName    = "payload-processing"
	mtIPPPayloadPreProcessingName = "payload-pre-processing"
	mtIPPMaaSAPIRouteName         = "maas-api-route"
	mtIPPGRPCPort                 = 9004
	mtIPPComponentLabelKey        = "app.kubernetes.io/component"
)

var (
	mtAITenantGVK            = schema.GroupVersionKind{Group: "maas.opendatahub.io", Version: "v1alpha1", Kind: "AITenant"}
	mtAITenantListGVK        = schema.GroupVersionKind{Group: "maas.opendatahub.io", Version: "v1alpha1", Kind: "AITenantList"}
	mtGatewayListGVK         = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayList"}
	mtAITenantWatchPredicate = predicate.ResourceVersionChangedPredicate{}

	mtIPPRuntimeResourceNames = map[string]struct{}{
		mtIPPPayloadProcessingName:    {},
		mtIPPPayloadPreProcessingName: {},
		"payload-processing-plugins":  {},
		"payload-processing-reader":   {},
	}
)

type mtIPPGatewayBinding struct {
	GatewayName      string
	GatewayNamespace string
	TenantNames      []string
}

// customizeMultitenantIPPResources expands the IPP manifests supplied by the
// IPP subcomponent into shared runtime resources and per-Gateway networking resources.
// Until that subcomponent is installed, this action is a no-op.
func (m *Module) customizeMultitenantIPPResources(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	bindings, available, err := activeMultitenantIPPBindings(ctx, rr)
	if err != nil || !available {
		return err
	}

	// AITenant changes do not change AIGateway.metadata.generation. Force GC to
	// evaluate per-Gateway resources removed by an AITenant lifecycle event.
	rr.Generated = true

	return renderMultitenantIPPResources(rr, bindings, deriveInfrastructureNamespace(m.cfg.ApplicationsNamespace))
}

func activeMultitenantIPPBindings(
	ctx context.Context,
	rr *odhtypes.ReconciliationRequest,
) ([]mtIPPGatewayBinding, bool, error) {
	hasAITenant, err := cluster.HasCRD(ctx, rr.Client, mtAITenantGVK)
	if err != nil {
		return nil, false, fmt.Errorf("check AITenant CRD: %w", err)
	}
	if !hasAITenant {
		return nil, false, nil
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(mtAITenantListGVK)
	if err := rr.Client.List(ctx, list); err != nil {
		return nil, true, fmt.Errorf("list AITenants: %w", err)
	}

	gateways := &unstructured.UnstructuredList{}
	gateways.SetGroupVersionKind(mtGatewayListGVK)
	if err := rr.Client.List(ctx, gateways); err != nil {
		return nil, true, fmt.Errorf("list Gateways: %w", err)
	}

	return multitenantIPPBindingsFromAITenants(list.Items, gateways.Items), true, nil
}

func multitenantIPPBindingsFromAITenants(
	items []unstructured.Unstructured,
	gateways []unstructured.Unstructured,
) []mtIPPGatewayBinding {
	existingGateways := make(map[types.NamespacedName]struct{}, len(gateways))
	for i := range gateways {
		if gateways[i].GetDeletionTimestamp().IsZero() {
			existingGateways[types.NamespacedName{
				Namespace: gateways[i].GetNamespace(),
				Name:      gateways[i].GetName(),
			}] = struct{}{}
		}
	}

	bindings := make([]mtIPPGatewayBinding, 0, len(items))
	bindingIndexes := make(map[string]int, len(items))
	for i := range items {
		item := &items[i]
		if !item.GetDeletionTimestamp().IsZero() {
			continue
		}

		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		gatewayName, _, _ := unstructured.NestedString(item.Object, "status", "gatewayRef", "name")
		gatewayNamespace, _, _ := unstructured.NestedString(item.Object, "status", "gatewayRef", "namespace")
		if phase != "Active" || gatewayName == "" || gatewayNamespace == "" {
			continue
		}

		gateway := types.NamespacedName{Namespace: gatewayNamespace, Name: gatewayName}
		if _, found := existingGateways[gateway]; !found {
			continue
		}

		key := gateway.String()
		if index, found := bindingIndexes[key]; found {
			bindings[index].TenantNames = append(bindings[index].TenantNames, item.GetName())
			continue
		}

		bindingIndexes[key] = len(bindings)
		bindings = append(bindings, mtIPPGatewayBinding{
			GatewayName:      gateway.Name,
			GatewayNamespace: gateway.Namespace,
			TenantNames:      []string{item.GetName()},
		})
	}

	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].GatewayNamespace+"/"+bindings[i].GatewayName <
			bindings[j].GatewayNamespace+"/"+bindings[j].GatewayName
	})
	for i := range bindings {
		sort.Strings(bindings[i].TenantNames)
	}

	return bindings
}

func isMultitenantIPPRuntimeResource(resource unstructured.Unstructured) bool {
	_, found := mtIPPRuntimeResourceNames[resource.GetName()]
	return found
}

func isMultitenantIPPManagedResource(resource unstructured.Unstructured) bool {
	return resource.GetLabels()[mtIPPComponentLabelKey] == mtIPPPayloadProcessingName
}

func renderMultitenantIPPResources(
	rr *odhtypes.ReconciliationRequest,
	bindings []mtIPPGatewayBinding,
	maasAPINamespace string,
) error {
	templates := make([]unstructured.Unstructured, 0)
	retained := make([]unstructured.Unstructured, 0, len(rr.Resources))
	for i := range rr.Resources {
		if isMultitenantIPPRuntimeResource(rr.Resources[i]) {
			templates = append(templates, rr.Resources[i])
			continue
		}
		retained = append(retained, rr.Resources[i])
	}

	if len(templates) == 0 || len(bindings) == 0 {
		rr.Resources = retained
		return nil
	}

	generated := make([]unstructured.Unstructured, 0, len(templates)+3*(len(bindings)-1))
	for i := range templates {
		template := &templates[i]
		switch template.GetKind() {
		case "Service", "DestinationRule", "EnvoyFilter":
			for j := range bindings {
				resource := template.DeepCopy()
				if err := customizeMultitenantIPPGatewayResource(resource, bindings[j], maasAPINamespace); err != nil {
					return err
				}
				generated = append(generated, *resource)
			}
		default:
			generated = append(generated, *template.DeepCopy())
		}
	}

	rr.Resources = append(retained, generated...)
	return nil
}

func customizeMultitenantIPPGatewayResource(
	resource *unstructured.Unstructured,
	binding mtIPPGatewayBinding,
	maasAPINamespace string,
) error {
	baseName := resource.GetName()
	resource.SetNamespace(binding.GatewayNamespace)
	resource.SetName(mtIPPResourceNameForGateway(baseName, binding.GatewayName))

	switch resource.GetKind() {
	case "DestinationRule":
		return unstructured.SetNestedField(
			resource.Object,
			mtIPPServiceHost(mtIPPResourceNameForGateway(baseName, binding.GatewayName), binding.GatewayNamespace),
			"spec", "host",
		)
	case "EnvoyFilter":
		return patchMultitenantIPPEnvoyFilter(resource, binding, maasAPINamespace)
	default:
		return nil
	}
}

func patchMultitenantIPPEnvoyFilter(
	resource *unstructured.Unstructured,
	binding mtIPPGatewayBinding,
	maasAPINamespace string,
) error {
	targetRefs, found, err := unstructured.NestedSlice(resource.Object, "spec", "targetRefs")
	if err != nil {
		return fmt.Errorf("read EnvoyFilter targetRefs: %w", err)
	}
	if !found || len(targetRefs) == 0 {
		return errors.New("EnvoyFilter targetRefs not found")
	}
	ref, ok := targetRefs[0].(map[string]any)
	if !ok {
		return errors.New("EnvoyFilter Gateway targetRef is not an object")
	}
	ref["name"] = binding.GatewayName
	targetRefs[0] = ref
	if err := unstructured.SetNestedSlice(resource.Object, targetRefs, "spec", "targetRefs"); err != nil {
		return fmt.Errorf("write EnvoyFilter targetRefs: %w", err)
	}

	configPatches, found, err := unstructured.NestedSlice(resource.Object, "spec", "configPatches")
	if err != nil {
		return fmt.Errorf("read EnvoyFilter configPatches: %w", err)
	}
	if !found || len(configPatches) < 4 {
		return fmt.Errorf("expected at least 4 EnvoyFilter configPatches, got %d", len(configPatches))
	}

	anchorName := fmt.Sprintf(
		"extensions.istio.io/wasmplugin/%s.kuadrant-%s",
		binding.GatewayNamespace,
		binding.GatewayName,
	)
	clusters := []string{
		mtIPPGRPCClusterName(mtIPPResourceNameForGateway(mtIPPPayloadPreProcessingName, binding.GatewayName), binding.GatewayNamespace),
		mtIPPGRPCClusterName(mtIPPResourceNameForGateway(mtIPPPayloadProcessingName, binding.GatewayName), binding.GatewayNamespace),
	}

	for i, clusterName := range clusters {
		patch, ok := configPatches[i].(map[string]any)
		if !ok {
			return fmt.Errorf("EnvoyFilter configPatches[%d] is not an object", i)
		}
		if err := unstructured.SetNestedField(
			patch,
			anchorName,
			"match", "listener", "filterChain", "filter", "subFilter", "name",
		); err != nil {
			return fmt.Errorf("write EnvoyFilter configPatches[%d] subFilter name: %w", i, err)
		}
		if err := unstructured.SetNestedField(
			patch,
			clusterName,
			"patch", "value", "typed_config", "grpc_service", "envoy_grpc", "cluster_name",
		); err != nil {
			return fmt.Errorf("write EnvoyFilter configPatches[%d] cluster name: %w", i, err)
		}
		configPatches[i] = patch
	}

	routePatches := make([]any, 0, (len(configPatches)-2)*len(binding.TenantNames))
	for _, tenantName := range binding.TenantNames {
		for i := 2; i < len(configPatches); i++ {
			routePatch, ok := configPatches[i].(map[string]any)
			if !ok {
				return fmt.Errorf("EnvoyFilter configPatches[%d] is not an object", i)
			}
			patch := (&unstructured.Unstructured{Object: routePatch}).DeepCopy().Object
			routeName := fmt.Sprintf(
				"%s.%s.%d",
				maasAPINamespace,
				mtIPPMaaSAPIRouteNameForTenant(tenantName),
				i-2,
			)
			if err := unstructured.SetNestedField(
				patch,
				routeName,
				"match", "routeConfiguration", "vhost", "route", "name",
			); err != nil {
				return fmt.Errorf("write EnvoyFilter route-disable patch %d: %w", i-2, err)
			}
			routePatches = append(routePatches, patch)
		}
	}
	configPatches = append(configPatches[:2], routePatches...)

	if err := unstructured.SetNestedSlice(resource.Object, configPatches, "spec", "configPatches"); err != nil {
		return fmt.Errorf("write EnvoyFilter configPatches: %w", err)
	}
	return nil
}

func mtIPPResourceNameForGateway(baseName, gatewayName string) string {
	if gatewayName == "" || gatewayName == mtIPPDefaultGatewayName {
		return baseName
	}
	return baseName + "-" + gatewayName
}

func mtIPPServiceHost(serviceName, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
}

func mtIPPGRPCClusterName(serviceName, namespace string) string {
	return fmt.Sprintf("outbound|%d||%s", mtIPPGRPCPort, mtIPPServiceHost(serviceName, namespace))
}

func mtIPPMaaSAPIRouteNameForTenant(tenantName string) string {
	if tenantName == "" || tenantName == mtIPPDefaultTenantName {
		return mtIPPMaaSAPIRouteName
	}
	return mtIPPMaaSAPIRouteName + "-" + tenantName
}

func multitenantIPPResourceIsDesired(rr *odhtypes.ReconciliationRequest, object unstructured.Unstructured) bool {
	for i := range rr.Resources {
		desired := &rr.Resources[i]
		if desired.GroupVersionKind().GroupKind() == object.GroupVersionKind().GroupKind() &&
			desired.GetNamespace() == object.GetNamespace() &&
			desired.GetName() == object.GetName() {
			return true
		}
	}
	return false
}
