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
	"reflect"
	"strings"

	k8serr "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	odhannotations "github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/annotations"
)

// maasControllerLegacyPodMonitorName is the PodMonitor the maas-controller bundle
// shipped up to RHOAI 3.5. A ServiceMonitor of the same name that scrapes over HTTPS
// replaced it.
const maasControllerLegacyPodMonitorName = "maas-controller-metrics"

// deleteLegacyMaasControllerPodMonitor deletes that PodMonitor. It has no owner, so an
// upgrade leaves it behind, scraping the now-HTTPS metrics port over plain HTTP:
// TargetDown fires and maas-controller logs a TLS handshake error every 30s. It runs on
// every reconcile rather than from upgrade(), which fires only when a recorded module
// version increases; once the PodMonitor is gone it costs one cache read.
func (m *Module) deleteLegacyMaasControllerPodMonitor(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return errors.New("instance is not an AIGateway")
	}
	// Otherwise a PodMonitor of that name belongs to whoever deploys maas-controller now.
	if obj.Spec.ModelsAsAService.ManagementState != managedState {
		return nil
	}
	if rr.Client == nil {
		return errors.New("reconciliation client is nil")
	}

	pm := &unstructured.Unstructured{}
	pm.SetGroupVersionKind(gvk.CoreosPodMonitor)
	key := client.ObjectKey{Namespace: m.cfg.ApplicationsNamespace, Name: maasControllerLegacyPodMonitorName}
	if err := rr.Client.Get(ctx, key, pm); err != nil {
		// NoMatch: without the PodMonitor CRD there is nothing to clean up.
		if k8serr.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("get PodMonitor %s: %w", key, err)
	}
	if !isLegacyMaasControllerPodMonitor(pm) || pm.GetAnnotations()[odhannotations.ManagedByODHOperator] == "false" {
		return nil
	}

	logf.FromContext(ctx).Info("deleting the maas-controller PodMonitor left over from RHOAI 3.5", "podMonitor", key)
	// As in migrateMaasControllerSelector, never delete an object other than the one
	// just checked (CWE-367); a Conflict leaves it for the next reconcile.
	preconditions := &client.Preconditions{UID: new(pm.GetUID()), ResourceVersion: new(pm.GetResourceVersion())}
	if err := rr.Client.Delete(ctx, pm, preconditions); err != nil && !k8serr.IsNotFound(err) && !k8serr.IsConflict(err) {
		return fmt.Errorf("delete PodMonitor %s: %w", key, err)
	}
	return nil
}

// isLegacyMaasControllerPodMonitor reports whether pm has the shape the old bundle
// shipped: exactly its pod selector and no HTTPS endpoint, so a PodMonitor a user
// created on purpose is kept. Labels are not compared: the render rewrites them.
func isLegacyMaasControllerPodMonitor(pm *unstructured.Unstructured) bool {
	selector, _, _ := unstructured.NestedFieldNoCopy(pm.Object, "spec", "selector")
	if !reflect.DeepEqual(selector, map[string]any{"matchLabels": map[string]any{"control-plane": "maas-controller"}}) {
		return false
	}
	endpoints, _, _ := unstructured.NestedSlice(pm.Object, "spec", "podMetricsEndpoints")
	for _, raw := range endpoints {
		endpoint, ok := raw.(map[string]any)
		if scheme, _ := endpoint["scheme"].(string); !ok || strings.EqualFold(scheme, "https") {
			return false
		}
	}
	return true
}
