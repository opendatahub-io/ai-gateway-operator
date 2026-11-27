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
	"cmp"
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

type getFunc = func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error

func TestDeleteLegacyMaasControllerPodMonitor(t *testing.T) {
	// legacy returns the PodMonitor the 3.5 bundle shipped, as found after an upgrade.
	legacy := func(mutate func(pm *unstructured.Unstructured)) *unstructured.Unstructured {
		pm := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
			"selector":            map[string]any{"matchLabels": map[string]any{"control-plane": "maas-controller"}},
			"podMetricsEndpoints": []any{map[string]any{"port": "metrics", "path": "/metrics", "interval": "30s"}},
		}}}
		pm.SetGroupVersionKind(gvk.CoreosPodMonitor)
		pm.SetNamespace(odhApplicationsNS)
		pm.SetName(maasControllerLegacyPodMonitorName)
		pm.SetLabels(map[string]string{"app.kubernetes.io/part-of": "aigateway"}) // rewritten by the render
		if mutate != nil {
			mutate(pm)
		}
		return pm
	}
	setSpec := func(value any, fields ...string) func(*unstructured.Unstructured) {
		return func(pm *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(pm.Object, value, append([]string{"spec"}, fields...)...)
		}
	}
	unmanaged := func(pm *unstructured.Unstructured) {
		pm.SetAnnotations(map[string]string{"opendatahub.io/managed": "false"})
	}
	endpoint := func(port, path string) map[string]any { return map[string]any{"port": port, "path": path} }
	ownedBy := func(kind string) func(*unstructured.Unstructured) {
		return func(pm *unstructured.Unstructured) {
			pm.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "example.com/v1", Kind: kind, Name: "owner", UID: "owner-uid", Controller: new(true)}})
		}
	}
	failGet := func(err error) getFunc {
		return func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return err
		}
	}
	// racingGet changes the object right after reading it, so the Delete preconditions are stale.
	racingGet := func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if err := c.Get(ctx, key, obj, opts...); err != nil {
			return err
		}
		live := obj.DeepCopyObject().(client.Object) //nolint:forcetypeassert
		live.SetAnnotations(map[string]string{"changed": "true"})
		return c.Update(ctx, live)
	}

	refuseDelete := func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
		return k8serr.NewForbidden(gvk.CoreosPodMonitor.GroupVersion().WithResource("podmonitors").GroupResource(),
			maasControllerLegacyPodMonitorName, errors.New("denied"))
	}

	tests := []struct {
		name      string
		maasState string // empty: Managed
		existing  *unstructured.Unstructured
		get       getFunc
		del       func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error
		wantKept  bool
	}{
		{name: "deletes the leftover", existing: legacy(nil)},
		{name: "deletes one owned by the AIGateway", existing: legacy(ownedBy(componentApi.AIGatewayKind))},
		{name: "keeps one owned by another controller", existing: legacy(ownedBy("Other")), wantKept: true},
		{name: "keeps one with an HTTPS endpoint", existing: legacy(setSpec([]any{map[string]any{"port": "metrics", "scheme": "HTTPS"}}, "podMetricsEndpoints")), wantKept: true},
		{name: "keeps one with another port", existing: legacy(setSpec([]any{endpoint("web", "/metrics")}, "podMetricsEndpoints")), wantKept: true},
		{name: "keeps one with another path", existing: legacy(setSpec([]any{endpoint("metrics", "/custom")}, "podMetricsEndpoints")), wantKept: true},
		{name: "keeps one with two endpoints", existing: legacy(setSpec([]any{endpoint("metrics", "/metrics"), endpoint("web", "/metrics")}, "podMetricsEndpoints")), wantKept: true},
		{name: "keeps one with a namespaceSelector", existing: legacy(setSpec(map[string]any{"any": true}, "namespaceSelector")), wantKept: true},
		{name: "keeps one with another selector", existing: legacy(setSpec(map[string]any{"control-plane": "maas-controller", "app": "x"}, "selector", "matchLabels")), wantKept: true},
		{name: "keeps one with matchExpressions", existing: legacy(setSpec([]any{map[string]any{"key": "tier", "operator": "Exists"}}, "selector", "matchExpressions")), wantKept: true},
		{name: "keeps one annotated managed=false", existing: legacy(unmanaged), wantKept: true},
		{name: "keeps it while MaaS is not Managed", maasState: removedState, existing: legacy(nil), wantKept: true},
		{name: "keeps it when it changed after the read", existing: legacy(nil), get: racingGet, wantKept: true},
		{name: "tolerates a missing PodMonitor"},
		{name: "logs other read errors without failing the reconcile", get: failGet(errors.New("boom"))},
		{name: "logs a refused delete without failing the reconcile", existing: legacy(nil), del: refuseDelete, wantKept: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			builder := fake.NewClientBuilder().WithScheme(newTestScheme(t))
			if tt.existing != nil {
				builder = builder.WithObjects(tt.existing)
			}
			cli := builder.WithInterceptorFuncs(interceptor.Funcs{Get: tt.get, Delete: tt.del}).Build()
			gateway := newTestAIGateway()
			gateway.Spec.ModelsAsAService.ManagementState = cmp.Or(tt.maasState, managedState)

			// Reads through the reconciliation client fail: the cleanup must use
			// the module's uncached reader.
			m := newTestModuleWithNamespace(t, odhApplicationsNS)
			m.apiReader = cli
			rrClient := interceptor.NewClient(cli, interceptor.Funcs{Get: failGet(errors.New("cached read"))})
			err := m.deleteLegacyMaasControllerPodMonitor(t.Context(), &odhtypes.ReconciliationRequest{Client: rrClient, Instance: gateway})
			g.Expect(err).NotTo(HaveOccurred())
			if tt.existing == nil {
				return
			}
			err = cli.Get(t.Context(), client.ObjectKeyFromObject(tt.existing), legacy(nil))
			if tt.wantKept {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(k8serr.IsNotFound(err)).To(BeTrue(), "PodMonitor should be deleted, got %v", err)
			}
		})
	}
}
